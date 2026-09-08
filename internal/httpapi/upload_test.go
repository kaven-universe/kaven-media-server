package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kaven.xyz/kaven/kaven-media-server/internal/database"
	"kaven.xyz/kaven/kaven-media-server/internal/imageupload"
	"kaven.xyz/kaven/kaven-media-server/internal/repository"
	"kaven.xyz/kaven/kaven-media-server/internal/storage"
)

func TestUploadHandlerDisabledByDefault(t *testing.T) {
	handler, _, _ := newUploadTestHandler(t, false, 10, 1024)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/images/upload", nil))
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusForbidden)
	}
	assertUploadResponse(t, response, UploadResponse{ErrorCode: ErrorUnexpected})
}

func TestUploadHandlerSingleAndDuplicate(t *testing.T) {
	handler, store, images := newUploadTestHandler(t, true, 10, 1024)
	contents := pngUpload("single")
	uuid := strings.Repeat("1", 32)
	request := multipartUploadRequest(t,
		map[string]string{"original.png": `{"uuid":"` + uuid + `","name":"../../renamed.png"}`},
		[]uploadPart{{field: "images", name: "original.png", contents: contents}},
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var uploaded UploadResponse
	decodeUploadResponse(t, response, &uploaded)
	if uploaded.ErrorCode != ErrorNone || uploaded.Image == nil || uploaded.Image.ID != uuid || uploaded.Image.UUID != uuid || uploaded.Image.Name != "renamed.png" {
		t.Fatalf("upload response = %#v", uploaded)
	}
	stored, err := images.GetByUUID(context.Background(), uuid)
	if err != nil {
		t.Fatalf("get uploaded image: %v", err)
	}
	if stored.MIMEType != "image/png" || stored.UploadIP != "192.0.2.1" || stored.OriginalName != "renamed.png" {
		t.Fatalf("stored image = %#v", stored)
	}

	duplicateUUID := strings.Repeat("2", 32)
	request = multipartUploadRequest(t,
		map[string]string{"duplicate.dat": `{"uuid":"` + duplicateUUID + `"}`},
		[]uploadPart{{field: "images", name: "duplicate.dat", contents: contents}},
	)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("duplicate status = %d, body = %s", response.Code, response.Body.String())
	}
	var duplicate UploadResponse
	decodeUploadResponse(t, response, &duplicate)
	if duplicate.ErrorCode != ErrorFileAlreadyExists || duplicate.Image == nil || duplicate.Image.ID != uuid || duplicate.Image.UUID != duplicateUUID {
		t.Fatalf("duplicate response = %#v", duplicate)
	}
	assertHTTPUploadTempEmpty(t, store)
}

func TestUploadHandlerMixedBatch(t *testing.T) {
	handler, store, _ := newUploadTestHandler(t, true, 10, 1024)
	firstUUID, secondUUID := strings.Repeat("3", 32), strings.Repeat("4", 32)
	request := multipartUploadRequest(t,
		map[string]string{
			"valid.bin":   `{"uuid":"` + firstUUID + `"}`,
			"invalid.jpg": `{"uuid":"` + secondUUID + `"}`,
		},
		[]uploadPart{
			{field: "images", name: "valid.bin", contents: pngUpload("batch")},
			{field: "images", name: "invalid.jpg", contents: []byte("not an image")},
		},
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var responses []UploadResponse
	decodeUploadResponse(t, response, &responses)
	if len(responses) != 2 || responses[0].ErrorCode != ErrorNone || responses[1].ErrorCode != ErrorInvalidFileType {
		t.Fatalf("responses = %#v", responses)
	}
	if responses[1].Image == nil || responses[1].Image.UUID != secondUUID || responses[1].Image.Name != "invalid.jpg" {
		t.Fatalf("invalid image response = %#v", responses[1])
	}
	assertHTTPUploadTempEmpty(t, store)
}

func TestUploadHandlerRequestErrors(t *testing.T) {
	tests := []struct {
		name     string
		maxFiles int
		maxSize  int64
		files    []uploadPart
		wantCode ErrorCode
	}{
		{
			name: "file too large", maxFiles: 1, maxSize: 8,
			files:    []uploadPart{{field: "images", name: "large.png", contents: pngUpload("large")}},
			wantCode: ErrorFileTooLarge,
		},
		{
			name: "too many files", maxFiles: 1, maxSize: 1024,
			files: []uploadPart{
				{field: "images", name: "one.png", contents: pngUpload("one")},
				{field: "images", name: "two.png", contents: pngUpload("two")},
			},
			wantCode: ErrorUnexpected,
		},
		{
			name: "unexpected field", maxFiles: 1, maxSize: 1024,
			files:    []uploadPart{{field: "file", name: "wrong.png", contents: pngUpload("wrong")}},
			wantCode: ErrorUnexpected,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler, store, _ := newUploadTestHandler(t, true, test.maxFiles, test.maxSize)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, multipartUploadRequest(t, nil, test.files))
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			assertUploadResponse(t, response, UploadResponse{ErrorCode: test.wantCode})
			assertHTTPUploadTempEmpty(t, store)
		})
	}
}

func TestUploadHandlerEmptyBatch(t *testing.T) {
	handler, _, _ := newUploadTestHandler(t, true, 10, 1024)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, multipartUploadRequest(t, map[string]string{"uuid": strings.Repeat("5", 32)}, nil))
	if response.Code != http.StatusOK || strings.TrimSpace(response.Body.String()) != "[]" {
		t.Fatalf("empty upload response = %d %q", response.Code, response.Body.String())
	}
}

type uploadPart struct {
	field    string
	name     string
	contents []byte
}

func multipartUploadRequest(t *testing.T, fields map[string]string, files []uploadPart) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for name, value := range fields {
		if err := writer.WriteField(name, value); err != nil {
			t.Fatalf("write multipart field: %v", err)
		}
	}
	for _, file := range files {
		part, err := writer.CreateFormFile(file.field, file.name)
		if err != nil {
			t.Fatalf("create multipart file: %v", err)
		}
		if _, err := part.Write(file.contents); err != nil {
			t.Fatalf("write multipart file: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart body: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/images/upload", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	return request
}

func newUploadTestHandler(t *testing.T, enabled bool, maxFiles int, maxSize int64) (*UploadHandler, *storage.Store, *repository.ImageRepository) {
	t.Helper()
	dataDir := t.TempDir()
	store, err := storage.New(dataDir)
	if err != nil {
		t.Fatalf("create storage: %v", err)
	}
	db, err := database.Open(context.Background(), dataDir)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	images := repository.NewImageRepository(db)
	service := imageupload.NewService(store, images)
	return NewUploadHandler(store, service, enabled, maxFiles, maxSize), store, images
}

func pngUpload(suffix string) []byte {
	return append([]byte("\x89PNG\r\n\x1a\n"), []byte(suffix)...)
}

func decodeUploadResponse(t *testing.T, response *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.Unmarshal(response.Body.Bytes(), target); err != nil {
		t.Fatalf("decode response %q: %v", response.Body.String(), err)
	}
}

func assertUploadResponse(t *testing.T, response *httptest.ResponseRecorder, want UploadResponse) {
	t.Helper()
	var got UploadResponse
	decodeUploadResponse(t, response, &got)
	if got.ErrorCode != want.ErrorCode || got.Image != want.Image {
		t.Fatalf("upload response = %#v, want %#v", got, want)
	}
}

func assertHTTPUploadTempEmpty(t *testing.T, store *storage.Store) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(store.Root(), "tmp"))
	if err != nil {
		t.Fatalf("read temp directory: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("temporary files remain: %#v", entries)
	}
}
