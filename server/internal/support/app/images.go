package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/jpeg"
	"image/png"
	"strings"
	"time"

	supportdomain "github.com/vitlane/vitlane/server/internal/support/domain"
)

type ImageInput struct {
	MediaType      string
	Data           []byte
	RetentionUntil *time.Time
}

type ImageDownload struct {
	Data         []byte
	MediaType    string
	DownloadName string
	CacheControl string
}

func (s *Service) prepareImages(
	ctx context.Context,
	messageID, userID string,
	inputs []ImageInput,
	now time.Time,
) ([]supportdomain.ImageAttachment, error) {
	if len(inputs) == 0 {
		return nil, nil
	}
	if len(inputs) > supportdomain.MaxImagesPerMessage {
		return nil, supportdomain.ErrImageAttachmentInvalid
	}
	if s.imageCipher == nil {
		return nil, supportdomain.ErrImageCipherUnavailable
	}
	attachments := make([]supportdomain.ImageAttachment, 0, len(inputs))
	for index, input := range inputs {
		if input.RetentionUntil != nil && !input.RetentionUntil.After(now) {
			return nil, supportdomain.ErrImageAttachmentInvalid
		}
		canonical, mediaType, width, height, err := canonicalImage(input)
		if err != nil {
			return nil, err
		}
		digest := sha256.Sum256(canonical)
		attachmentID := s.ids.NewID()
		encrypted, err := s.imageCipher.Encrypt(
			ctx, canonical, imageBinding(userID, messageID, attachmentID),
		)
		if err != nil {
			return nil, err
		}
		if len(encrypted.Ciphertext) == 0 || len(encrypted.Nonce) == 0 ||
			strings.TrimSpace(encrypted.KeyVersion) == "" ||
			strings.TrimSpace(encrypted.Fingerprint) == "" {
			return nil, supportdomain.ErrImageCipherUnavailable
		}
		attachments = append(attachments, supportdomain.ImageAttachment{
			ID: attachmentID, MessageID: messageID, UserID: userID,
			Ordinal: index + 1, MediaType: mediaType,
			Width: width, Height: height, ByteSize: len(canonical),
			ContentSHA256: hex.EncodeToString(digest[:]),
			Encrypted:     encrypted, CreatedAt: now,
			RetentionUntil: input.RetentionUntil,
		})
	}
	return attachments, nil
}

func canonicalImage(input ImageInput) ([]byte, string, int, int, error) {
	mediaType := strings.ToLower(strings.TrimSpace(input.MediaType))
	if mediaType != supportdomain.ImageMediaTypeJPEG &&
		mediaType != supportdomain.ImageMediaTypePNG {
		return nil, "", 0, 0, supportdomain.ErrImageAttachmentInvalid
	}
	if len(input.Data) == 0 || len(input.Data) > supportdomain.MaxImageBytes {
		return nil, "", 0, 0, supportdomain.ErrImageAttachmentInvalid
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(input.Data))
	if err != nil || config.Width <= 0 || config.Height <= 0 ||
		int64(config.Width)*int64(config.Height) > supportdomain.MaxImagePixels {
		return nil, "", 0, 0, supportdomain.ErrImageAttachmentInvalid
	}
	expectedFormat := "jpeg"
	if mediaType == supportdomain.ImageMediaTypePNG {
		expectedFormat = "png"
	}
	if format != expectedFormat {
		return nil, "", 0, 0, supportdomain.ErrImageAttachmentInvalid
	}
	decoded, decodedFormat, err := image.Decode(bytes.NewReader(input.Data))
	if err != nil || decodedFormat != expectedFormat {
		return nil, "", 0, 0, supportdomain.ErrImageAttachmentInvalid
	}
	// Decode and re-encode removes EXIF and ancillary chunks. No original
	// filename or undecoded bytes cross the persistence boundary.
	var canonical bytes.Buffer
	if mediaType == supportdomain.ImageMediaTypeJPEG {
		err = jpeg.Encode(&canonical, decoded, &jpeg.Options{Quality: 90})
	} else {
		encoder := png.Encoder{CompressionLevel: png.BestSpeed}
		err = encoder.Encode(&canonical, decoded)
	}
	if err != nil || canonical.Len() == 0 ||
		canonical.Len() > supportdomain.MaxImageBytes {
		return nil, "", 0, 0, supportdomain.ErrImageAttachmentInvalid
	}
	return canonical.Bytes(), mediaType, config.Width, config.Height, nil
}

func (s *Service) DownloadImage(
	ctx context.Context,
	userID, attachmentID string,
) (ImageDownload, error) {
	return s.downloadImage(
		ctx, strings.TrimSpace(userID), strings.TrimSpace(attachmentID),
	)
}

// DownloadImageForOperator keeps authorization at the service boundary: the
// caller must already be an authenticated operator and identifies the exact
// conversation owner. HTTP wiring remains responsible for fresh-auth policy.
func (s *Service) DownloadImageForOperator(
	ctx context.Context,
	targetUserID, attachmentID string,
) (ImageDownload, error) {
	targetUserID = strings.TrimSpace(targetUserID)
	if _, found, err := s.resolveContact(ctx, targetUserID); err != nil {
		return ImageDownload{}, err
	} else if !found {
		return ImageDownload{}, supportdomain.ErrUserNotFound
	}
	return s.downloadImage(ctx, targetUserID, strings.TrimSpace(attachmentID))
}

func (s *Service) downloadImage(
	ctx context.Context,
	userID, attachmentID string,
) (ImageDownload, error) {
	if s.imageCipher == nil {
		return ImageDownload{}, supportdomain.ErrImageCipherUnavailable
	}
	attachmentRepository, ok := s.repository.(AttachmentRepository)
	if !ok {
		return ImageDownload{}, supportdomain.ErrImageAttachmentNotFound
	}
	attachment, err := attachmentRepository.GetImageAttachment(
		ctx, attachmentID, userID,
	)
	if err != nil {
		return ImageDownload{}, err
	}
	plaintext, err := s.imageCipher.Decrypt(
		ctx, attachment.Encrypted,
		imageBinding(attachment.UserID, attachment.MessageID, attachment.ID),
	)
	if err != nil {
		return ImageDownload{}, err
	}
	digest := sha256.Sum256(plaintext)
	if hex.EncodeToString(digest[:]) != attachment.ContentSHA256 {
		return ImageDownload{}, supportdomain.ErrImageAttachmentInvalid
	}
	metadata := attachment.Metadata()
	return ImageDownload{
		Data: plaintext, MediaType: attachment.MediaType,
		DownloadName: metadata.DownloadName,
		CacheControl: metadata.CacheControl,
	}, nil
}

func imageBinding(userID, messageID, attachmentID string) string {
	return "support-image:v1:" + userID + ":" + messageID + ":" + attachmentID
}
