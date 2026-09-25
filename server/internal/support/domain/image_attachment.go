package domain

import "time"

const (
	ImageMediaTypeJPEG = "image/jpeg"
	ImageMediaTypePNG  = "image/png"

	MaxImagesPerMessage = 4
	MaxImageBytes       = 5 * 1024 * 1024
	MaxImagePixels      = 24_000_000
)

type EncryptedImage struct {
	Ciphertext  []byte
	Nonce       []byte
	KeyVersion  string
	Fingerprint string
}

// ImageAttachment is the encrypted persistence record. Plain image bytes and
// the uploader's original filename are deliberately absent.
type ImageAttachment struct {
	ID                  string
	MessageID           string
	UserID              string
	Ordinal             int
	MediaType           string
	Width               int
	Height              int
	ByteSize            int
	ContentSHA256       string
	Encrypted           EncryptedImage
	CreatedAt           time.Time
	RetentionUntil      *time.Time
	LegalHold           bool
	LegalHoldReasonHash string
	PurgedAt            *time.Time
}

type ImageAttachmentMetadata struct {
	ID             string     `json:"id"`
	MediaType      string     `json:"mediaType"`
	Width          int        `json:"width"`
	Height         int        `json:"height"`
	ByteSize       int        `json:"byteSize"`
	ContentSHA256  string     `json:"contentSha256"`
	CreatedAt      time.Time  `json:"createdAt"`
	RetentionUntil *time.Time `json:"retentionUntil,omitempty"`
	LegalHold      bool       `json:"legalHold"`
	DownloadName   string     `json:"downloadName"`
	CacheControl   string     `json:"cacheControl"`
}

func (a ImageAttachment) Metadata() ImageAttachmentMetadata {
	extension := ".jpg"
	if a.MediaType == ImageMediaTypePNG {
		extension = ".png"
	}
	return ImageAttachmentMetadata{
		ID: a.ID, MediaType: a.MediaType, Width: a.Width, Height: a.Height,
		ByteSize: a.ByteSize, ContentSHA256: a.ContentSHA256,
		CreatedAt: a.CreatedAt, RetentionUntil: a.RetentionUntil,
		LegalHold:    a.LegalHold,
		DownloadName: "support-image-" + itoaSmall(a.Ordinal) + extension,
		CacheControl: "private, no-store",
	}
}

func itoaSmall(value int) string {
	if value >= 1 && value <= 9 {
		return string(rune('0' + value))
	}
	return "image"
}
