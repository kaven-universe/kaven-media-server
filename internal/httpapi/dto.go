package httpapi

import (
	"encoding/json"
	"fmt"
	"time"
)

// ErrorCode is the stable numeric result code used by legacy upload endpoints.
type ErrorCode int

const (
	ErrorNone              ErrorCode = 0
	ErrorUnexpected        ErrorCode = 1
	ErrorInvalidFileType   ErrorCode = 10
	ErrorFileAlreadyExists ErrorCode = 11
	ErrorFileTooLarge      ErrorCode = 12
	ErrorFolderNotFound    ErrorCode = 13
)

// ServerInfoResponse describes public server capabilities used by the frontend.
type ServerInfoResponse struct {
	Upload UploadLimits `json:"upload"`
}

// UploadLimits contains byte-based upload limits and the per-request file cap.
type UploadLimits struct {
	MaxFileCount     int   `json:"maxFileCount"`
	MaxImageFileSize int64 `json:"maxImageFileSize"`
	MaxHFSFileSize   int64 `json:"maxHfsFileSize"`
}

// UploadResponse is returned for each uploaded image or HFS file.
type UploadResponse struct {
	ErrorCode ErrorCode      `json:"errorCode"`
	Image     *ImageIdentity `json:"image,omitempty"`
}

// ImageIdentity contains the stable lookup values returned after image upload.
type ImageIdentity struct {
	ID   string `json:"id"`
	UUID string `json:"uuid"`
	Name string `json:"name"`
	SHA1 string `json:"sha1"`
}

// ImageMetadata preserves the public Mongoose document shape returned by the
// legacy image metadata and image-list endpoints.
type ImageMetadata struct {
	ID           string    `json:"_id"`
	Folder       string    `json:"folder"`
	Name         string    `json:"name"`
	OriginalName string    `json:"originalName"`
	Path         string    `json:"path"`
	UploadDate   Timestamp `json:"uploadDate"`
	UUID         string    `json:"uuid"`
	MIMEType     string    `json:"mimeType"`
	Size         int64     `json:"size"`
	UploadIP     string    `json:"uploadIP"`
	SHA1         string    `json:"sha1"`
	CreatedAt    Timestamp `json:"createdAt"`
	UpdatedAt    Timestamp `json:"updatedAt"`
	Version      int       `json:"__v"`
}

// HFSEntry describes a virtual root, directory, or file returned by HFS
// listings. Pointer fields distinguish omitted legacy values from zero values.
type HFSEntry struct {
	Name         string     `json:"name"`
	Link         string     `json:"link"`
	IsDirectory  *bool      `json:"isDirectory,omitempty"`
	Size         *int64     `json:"size,omitempty"`
	LastModified *Timestamp `json:"lastModified,omitempty"`
}

// Timestamp preserves JavaScript Date.toISOString compatibility: UTC with
// exactly three fractional-second digits.
type Timestamp struct {
	time.Time
}

func NewTimestamp(value time.Time) Timestamp {
	return Timestamp{Time: value.UTC()}
}

func (timestamp Timestamp) MarshalJSON() ([]byte, error) {
	if timestamp.Time.IsZero() {
		return nil, fmt.Errorf("marshal zero timestamp")
	}
	return json.Marshal(timestamp.UTC().Format("2006-01-02T15:04:05.000Z"))
}

func (timestamp *Timestamp) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("decode timestamp: %w", err)
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return fmt.Errorf("parse timestamp %q: %w", value, err)
	}
	timestamp.Time = parsed.UTC()
	return nil
}
