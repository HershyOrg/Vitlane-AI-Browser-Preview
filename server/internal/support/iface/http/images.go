package http

import (
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"

	"github.com/vitlane/vitlane/server/internal/shared/iface/httpapi"
	supportapp "github.com/vitlane/vitlane/server/internal/support/app"
	supportdomain "github.com/vitlane/vitlane/server/internal/support/domain"
)

const (
	maxSupportMultipartBytes = int64(supportdomain.MaxImagesPerMessage)*
		int64(supportdomain.MaxImageBytes) + 256*1024
	maxSupportBodyFieldBytes  = 8 * 1024
	maxSupportOrderFieldBytes = 512
)

type messagePayload struct {
	Body          string
	AgencyOrderID string
	Images        []supportapp.ImageInput
}

// DownloadImage: GET /api/v1/support/images/{attachmentId}. Repository lookup
// is constrained by the authenticated conversation owner, so a foreign UUID
// receives the same 404 as a missing attachment.
func (h *Handler) DownloadImage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	download, err := h.service.DownloadImage(
		r.Context(), userID, r.PathValue("attachmentId"),
	)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeImageDownload(w, download)
}

// DownloadImageForOperator: GET
// /api/v1/admin/support/conversations/{userId}/images/{attachmentId}.
// Route middleware authenticates an operator; the handler additionally
// requires that identity when invoked directly.
func (h *Handler) DownloadImageForOperator(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if _, ok := httpapi.RequireAuthenticatedUserID(w, r); !ok {
		return
	}
	download, err := h.service.DownloadImageForOperator(
		r.Context(), r.PathValue("userId"), r.PathValue("attachmentId"),
	)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeImageDownload(w, download)
}

func decodeMessagePayload(
	w http.ResponseWriter,
	r *http.Request,
	allowAgencyOrder bool,
) (messagePayload, bool) {
	mediaType, _, mediaErr := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaErr == nil && strings.EqualFold(mediaType, "multipart/form-data") {
		payload, err := decodeMultipartMessage(w, r, allowAgencyOrder)
		if err != nil {
			writeError(w, r, err)
			return messagePayload{}, false
		}
		return payload, true
	}
	if allowAgencyOrder {
		var request sendMessageRequest
		if !httpapi.DecodeJSON(w, r, &request) {
			return messagePayload{}, false
		}
		return messagePayload{
			Body: request.Body, AgencyOrderID: request.AgencyOrderID,
		}, true
	}
	var request struct {
		Body string `json:"body"`
	}
	if !httpapi.DecodeJSON(w, r, &request) {
		return messagePayload{}, false
	}
	return messagePayload{Body: request.Body}, true
}

func decodeMultipartMessage(
	w http.ResponseWriter,
	r *http.Request,
	allowAgencyOrder bool,
) (messagePayload, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxSupportMultipartBytes)
	reader, err := r.MultipartReader()
	if err != nil {
		return messagePayload{}, supportdomain.ErrImageAttachmentInvalid
	}
	var payload messagePayload
	var bodySeen, orderSeen bool
	for {
		part, nextErr := reader.NextPart()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			return messagePayload{}, supportdomain.ErrImageAttachmentInvalid
		}
		name := part.FormName()
		switch name {
		case "body":
			if bodySeen || part.FileName() != "" {
				part.Close()
				return messagePayload{}, supportdomain.ErrImageAttachmentInvalid
			}
			bodySeen = true
			payload.Body, err = readMultipartText(part, maxSupportBodyFieldBytes)
		case "agencyOrderId":
			if !allowAgencyOrder || orderSeen || part.FileName() != "" {
				part.Close()
				return messagePayload{}, supportdomain.ErrImageAttachmentInvalid
			}
			orderSeen = true
			payload.AgencyOrderID, err = readMultipartText(
				part, maxSupportOrderFieldBytes,
			)
		case "images":
			if part.FileName() == "" ||
				len(payload.Images) >= supportdomain.MaxImagesPerMessage {
				part.Close()
				return messagePayload{}, supportdomain.ErrImageAttachmentInvalid
			}
			var input supportapp.ImageInput
			input.MediaType, err = multipartImageMediaType(part)
			if err == nil {
				input.Data, err = readMultipartBytes(
					part, supportdomain.MaxImageBytes,
				)
			}
			if err == nil {
				payload.Images = append(payload.Images, input)
			}
		default:
			part.Close()
			return messagePayload{}, supportdomain.ErrImageAttachmentInvalid
		}
		part.Close()
		if err != nil {
			return messagePayload{}, supportdomain.ErrImageAttachmentInvalid
		}
	}
	if !bodySeen {
		return messagePayload{}, supportdomain.ErrImageAttachmentInvalid
	}
	return payload, nil
}

func readMultipartText(part *multipart.Part, limit int64) (string, error) {
	value, err := readMultipartBytes(part, limit)
	if err != nil {
		return "", err
	}
	return string(value), nil
}

func readMultipartBytes(part *multipart.Part, limit int64) ([]byte, error) {
	value, err := io.ReadAll(io.LimitReader(part, limit+1))
	if err != nil || int64(len(value)) > limit {
		return nil, supportdomain.ErrImageAttachmentInvalid
	}
	return value, nil
}

func multipartImageMediaType(part *multipart.Part) (string, error) {
	mediaType, _, err := mime.ParseMediaType(part.Header.Get("Content-Type"))
	if err != nil {
		return "", supportdomain.ErrImageAttachmentInvalid
	}
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	if mediaType != supportdomain.ImageMediaTypeJPEG &&
		mediaType != supportdomain.ImageMediaTypePNG {
		return "", supportdomain.ErrImageAttachmentInvalid
	}
	return mediaType, nil
}

func writeImageDownload(w http.ResponseWriter, download supportapp.ImageDownload) {
	w.Header().Set("Content-Type", download.MediaType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType(
		"attachment", map[string]string{"filename": download.DownloadName},
	))
	w.Header().Set("Cache-Control", download.CacheControl)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Length", strconv.Itoa(len(download.Data)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(download.Data)
}
