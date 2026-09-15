package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"mime/multipart"
	"net"
	"net/http"
	"strings"

	"kaven.xyz/kaven/kaven-media-server/internal/imageproc"
	"kaven.xyz/kaven/kaven-media-server/internal/imageupload"
	"kaven.xyz/kaven/kaven-media-server/internal/storage"
)

const maxUploadFormBytes = int64(1024 * 1024)

type UploadHandler struct {
	store        *storage.Store
	service      *imageupload.Service
	enabled      bool
	maxFileCount int
	maxFileSize  int64
}

func NewUploadHandler(store *storage.Store, service *imageupload.Service, enabled bool, maxFileCount int, maxFileSize int64) *UploadHandler {
	return &UploadHandler{
		store: store, service: service, enabled: enabled,
		maxFileCount: maxFileCount, maxFileSize: maxFileSize,
	}
}

func (handler *UploadHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if !handler.enabled {
		writeJSONResponse(writer, http.StatusForbidden, UploadResponse{ErrorCode: ErrorUnexpected})
		return
	}
	if handler.maxFileCount <= 0 || handler.maxFileSize <= 0 {
		writeJSONResponse(writer, http.StatusServiceUnavailable, UploadResponse{ErrorCode: ErrorUnexpected})
		return
	}

	request.Body = http.MaxBytesReader(writer, request.Body, maximumUploadRequestBytes(handler.maxFileCount, handler.maxFileSize))
	reader, err := request.MultipartReader()
	if err != nil {
		writeJSONResponse(writer, http.StatusBadRequest, UploadResponse{ErrorCode: ErrorUnexpected})
		return
	}

	files, fields, code, err := handler.readMultipart(request, reader)
	if err != nil {
		abortUploads(files)
		if !errors.Is(err, storage.ErrTooLarge) {
			slog.Warn("reject image upload", "error", err)
		}
		writeJSONResponse(writer, http.StatusBadRequest, UploadResponse{ErrorCode: code})
		return
	}
	defer abortUploads(files)

	responses := make([]UploadResponse, 0, len(files))
	failed := false
	for _, file := range files {
		metadata := uploadMetadata{}
		if encoded := fields[file.originalName]; encoded != "" {
			if err := json.Unmarshal([]byte(encoded), &metadata); err != nil {
				slog.Warn("ignore invalid image upload metadata", "file", file.originalName, "error", err)
			}
		}
		if metadata.UUID == "" {
			metadata.UUID = fields["uuid"]
		}
		name := file.originalName
		if metadata.Name != "" {
			name = metadata.Name
		}

		result, saveErr := handler.service.Save(request.Context(), imageupload.Input{
			File: file.staged, OriginalName: name, RequestedUUID: metadata.UUID,
			UploadIP: requestIP(request),
		})
		response := uploadResponse(result, saveErr)
		if response.ErrorCode != ErrorNone {
			failed = true
		}
		if saveErr != nil && !errors.Is(saveErr, imageproc.ErrUnsupportedFormat) {
			slog.Error("save uploaded image", "file", file.originalName, "error", saveErr)
		}
		responses = append(responses, response)
	}

	status := http.StatusOK
	if failed {
		status = http.StatusBadRequest
	}
	if len(responses) == 1 {
		writeJSONResponse(writer, status, responses[0])
		return
	}
	writeJSONResponse(writer, status, responses)
}

type pendingUpload struct {
	originalName string
	staged       *storage.StagedFile
}

type uploadMetadata struct {
	UUID string `json:"uuid"`
	Name string `json:"name"`
}

func (handler *UploadHandler) readMultipart(request *http.Request, reader *multipart.Reader) ([]pendingUpload, map[string]string, ErrorCode, error) {
	files := make([]pendingUpload, 0)
	fields := make(map[string]string)
	var fieldBytes int64
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			return files, fields, ErrorNone, nil
		}
		if err != nil {
			return files, fields, ErrorUnexpected, fmt.Errorf("read multipart upload: %w", err)
		}

		fileName := part.FileName()
		if fileName == "" {
			value, readErr := io.ReadAll(io.LimitReader(part, maxUploadFormBytes-fieldBytes+1))
			part.Close()
			fieldBytes += int64(len(value))
			if readErr != nil {
				return files, fields, ErrorUnexpected, fmt.Errorf("read upload field: %w", readErr)
			}
			if fieldBytes > maxUploadFormBytes {
				return files, fields, ErrorUnexpected, errors.New("image upload fields are too large")
			}
			fields[part.FormName()] = string(value)
			continue
		}
		if part.FormName() != "images" {
			part.Close()
			return files, fields, ErrorUnexpected, fmt.Errorf("unexpected image upload field %q", part.FormName())
		}
		if len(files) >= handler.maxFileCount {
			part.Close()
			return files, fields, ErrorUnexpected, errors.New("too many uploaded image files")
		}
		staged, stageErr := handler.store.Stage(request.Context(), part, handler.maxFileSize)
		part.Close()
		if stageErr != nil {
			code := ErrorUnexpected
			if errors.Is(stageErr, storage.ErrTooLarge) {
				code = ErrorFileTooLarge
			}
			return files, fields, code, stageErr
		}
		files = append(files, pendingUpload{originalName: fileName, staged: staged})
	}
}

func uploadResponse(result imageupload.Result, err error) UploadResponse {
	code := ErrorNone
	switch {
	case result.Duplicate:
		code = ErrorFileAlreadyExists
	case errors.Is(err, imageproc.ErrUnsupportedFormat):
		code = ErrorInvalidFileType
	case err != nil:
		code = ErrorUnexpected
	}
	response := UploadResponse{ErrorCode: code}
	if result.UUID != "" {
		response.Image = &ImageIdentity{ID: result.ID, UUID: result.UUID, Name: result.Name, SHA1: result.SHA1}
	}
	return response
}

func abortUploads(files []pendingUpload) {
	for _, file := range files {
		_ = file.staged.Abort()
	}
}

func maximumUploadRequestBytes(fileCount int, fileSize int64) int64 {
	if int64(fileCount) > (math.MaxInt64-maxUploadFormBytes)/fileSize {
		return math.MaxInt64
	}
	return int64(fileCount)*fileSize + maxUploadFormBytes
}

func requestIP(request *http.Request) string {
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err == nil {
		return host
	}
	if value := strings.TrimSpace(request.RemoteAddr); value != "" {
		return value
	}
	return "unknown"
}

func writeJSONResponse(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	if err := json.NewEncoder(writer).Encode(value); err != nil {
		slog.Error("write upload response", "error", err)
	}
}
