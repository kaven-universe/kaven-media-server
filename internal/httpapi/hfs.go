package httpapi

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"

	"kaven.xyz/kaven/kaven-media-server/internal/hfs"
	"kaven.xyz/kaven/kaven-media-server/internal/imageproc"
	"kaven.xyz/kaven/kaven-media-server/internal/storage"
)

const hfsSniffBytes = 4096

type HFSRootHandler struct {
	registry *hfs.Registry
}

func NewHFSRootHandler(registry *hfs.Registry) *HFSRootHandler {
	return &HFSRootHandler{registry: registry}
}

func (handler *HFSRootHandler) ServeHTTP(writer http.ResponseWriter, _ *http.Request) {
	entries, err := handler.registry.List()
	if err != nil {
		slog.Error("list HFS roots", "error", err)
		http.Error(writer, "HFS roots unavailable", http.StatusInternalServerError)
		return
	}
	directory := true
	response := make([]HFSEntry, 0, len(entries))
	for _, entry := range entries {
		modified := NewTimestamp(entry.LastModified)
		response = append(response, HFSEntry{
			Name: entry.Name, Link: "/hfs/" + url.PathEscape(entry.Name),
			IsDirectory: &directory, LastModified: &modified,
		})
	}
	writer.Header().Set("Cache-Control", "no-store")
	writeJSONResponse(writer, http.StatusOK, response)
}

type HFSPathHandler struct {
	registry         *hfs.Registry
	root             hfs.Root
	downloadRecorder HFSDownloadRecorder
}

type HFSDownloadRecorder interface {
	Record(file, originalURL, ip, userAgent string) bool
}

type HFSWriteHandler struct {
	registry     *hfs.Registry
	root         hfs.Root
	store        *storage.Store
	maxFileCount int
	maxFileSize  int64
}

func NewHFSWriteHandler(registry *hfs.Registry, root hfs.Root, store *storage.Store, maxFileCount int, maxFileSize int64) *HFSWriteHandler {
	return &HFSWriteHandler{
		registry: registry, root: root, store: store, maxFileCount: maxFileCount, maxFileSize: maxFileSize,
	}
}

func NewHFSPathHandler(registry *hfs.Registry, root hfs.Root, downloadRecorder HFSDownloadRecorder) *HFSPathHandler {
	return &HFSPathHandler{registry: registry, root: root, downloadRecorder: downloadRecorder}
}

func GuardHFSPaths(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if err := validateHFSEscapedPath(request.URL.EscapedPath()); err != nil {
			http.Error(writer, "invalid HFS path", http.StatusBadRequest)
			return
		}
		next.ServeHTTP(writer, request)
	})
}

func (handler *HFSPathHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	segments, err := decodedHFSSegments(request, handler.root.Name)
	if err != nil {
		http.Error(writer, "invalid HFS path", http.StatusBadRequest)
		return
	}
	file, info, err := handler.registry.Open(handler.root, segments)
	if err != nil {
		handler.writeOpenError(writer, err)
		return
	}
	defer file.Close()
	if info.IsDir() {
		handler.serveDirectory(writer, file, segments)
		return
	}
	handler.serveFile(writer, request, file, info, segments)
}

func (handler *HFSWriteHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	segments, err := decodedHFSSegments(request, handler.root.Name)
	if err != nil {
		writeHFSMutationResponse(writer, http.StatusBadRequest, UploadResponse{ErrorCode: ErrorUnexpected})
		return
	}
	release, err := handler.registry.AcquireMutation(request.Context())
	if err != nil {
		writeHFSMutationResponse(writer, http.StatusServiceUnavailable, UploadResponse{ErrorCode: ErrorUnexpected})
		return
	}
	defer release()
	if request.URL.Query().Has("mkdir") {
		err := handler.registry.CreateDirectory(handler.root, segments)
		handler.writeResult(writer, err)
		return
	}
	if handler.maxFileCount <= 0 || handler.maxFileSize <= 0 || handler.store == nil {
		writeHFSMutationResponse(writer, http.StatusServiceUnavailable, UploadResponse{ErrorCode: ErrorUnexpected})
		return
	}
	directory, info, err := handler.registry.Open(handler.root, segments)
	if err != nil || !info.IsDir() {
		if directory != nil {
			_ = directory.Close()
		}
		code := ErrorFolderNotFound
		if err != nil && !errors.Is(err, storage.ErrNotFound) && !errors.Is(err, hfs.ErrNotDirectory) {
			code = ErrorUnexpected
		}
		writeHFSMutationResponse(writer, http.StatusBadRequest, UploadResponse{ErrorCode: code})
		return
	}
	_ = directory.Close()

	request.Body = http.MaxBytesReader(writer, request.Body, maximumUploadRequestBytes(handler.maxFileCount, handler.maxFileSize))
	reader, err := request.MultipartReader()
	if err != nil {
		writeHFSMutationResponse(writer, http.StatusBadRequest, UploadResponse{ErrorCode: ErrorUnexpected})
		return
	}
	files, code, err := handler.readMultipart(request, reader)
	defer abortHFSUploads(files)
	if err != nil {
		if !errors.Is(err, storage.ErrTooLarge) {
			slog.Warn("reject HFS upload", "root", handler.root.Name, "error", err)
		}
		writeHFSMutationResponse(writer, http.StatusBadRequest, UploadResponse{ErrorCode: code})
		return
	}
	if err := handler.registry.CommitFiles(handler.root, segments, files); err != nil {
		handler.writeResult(writer, err)
		return
	}
	responses := make([]UploadResponse, len(files))
	if len(responses) == 1 {
		writeHFSMutationResponse(writer, http.StatusOK, responses[0])
		return
	}
	writeHFSMutationResponse(writer, http.StatusOK, responses)
}

func (handler *HFSWriteHandler) readMultipart(request *http.Request, reader *multipart.Reader) ([]hfs.UploadFile, ErrorCode, error) {
	files := make([]hfs.UploadFile, 0)
	var fieldBytes int64
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			if len(files) == 0 {
				return files, ErrorUnexpected, errors.New("HFS upload contains no files")
			}
			return files, ErrorNone, nil
		}
		if err != nil {
			return files, ErrorUnexpected, fmt.Errorf("read HFS multipart upload: %w", err)
		}
		if part.FileName() == "" {
			read, readErr := io.Copy(io.Discard, io.LimitReader(part, maxUploadFormBytes-fieldBytes+1))
			_ = part.Close()
			fieldBytes += read
			if readErr != nil {
				return files, ErrorUnexpected, fmt.Errorf("read HFS upload field: %w", readErr)
			}
			if fieldBytes > maxUploadFormBytes {
				return files, ErrorUnexpected, errors.New("HFS upload fields are too large")
			}
			continue
		}
		if part.FormName() != "file" {
			_ = part.Close()
			return files, ErrorUnexpected, fmt.Errorf("unexpected HFS upload field %q", part.FormName())
		}
		if len(files) >= handler.maxFileCount {
			_ = part.Close()
			return files, ErrorUnexpected, errors.New("too many HFS upload files")
		}
		name, err := safeHFSFilename(part.FileName())
		if err != nil {
			_ = part.Close()
			return files, ErrorUnexpected, err
		}
		staged, err := handler.store.Stage(request.Context(), part, handler.maxFileSize)
		_ = part.Close()
		if err != nil {
			code := ErrorUnexpected
			if errors.Is(err, storage.ErrTooLarge) {
				code = ErrorFileTooLarge
			}
			return files, code, err
		}
		files = append(files, hfs.UploadFile{Name: name, Staged: staged})
	}
}

func (handler *HFSWriteHandler) writeResult(writer http.ResponseWriter, err error) {
	status := http.StatusOK
	code := ErrorNone
	switch {
	case errors.Is(err, hfs.ErrRollbackFailed):
		status, code = http.StatusInternalServerError, ErrorUnexpected
		slog.Error("roll back HFS upload", "root", handler.root.Name, "error", err)
	case errors.Is(err, storage.ErrExists):
		status, code = http.StatusBadRequest, ErrorFileAlreadyExists
	case errors.Is(err, storage.ErrNotFound), errors.Is(err, hfs.ErrNotDirectory):
		status, code = http.StatusBadRequest, ErrorFolderNotFound
	case errors.Is(err, storage.ErrTooLarge):
		status, code = http.StatusBadRequest, ErrorFileTooLarge
	case err != nil:
		status, code = http.StatusInternalServerError, ErrorUnexpected
		slog.Error("mutate HFS path", "root", handler.root.Name, "error", err)
	}
	writeHFSMutationResponse(writer, status, UploadResponse{ErrorCode: code})
}

func safeHFSFilename(name string) (string, error) {
	name = path.Base(strings.ReplaceAll(name, `\`, "/"))
	if err := hfs.ValidateSegment(name); err != nil {
		return "", fmt.Errorf("invalid HFS upload filename: %w", err)
	}
	return name, nil
}

func abortHFSUploads(files []hfs.UploadFile) {
	for _, file := range files {
		if file.Staged != nil {
			_ = file.Staged.Abort()
		}
	}
}

func writeHFSMutationResponse(writer http.ResponseWriter, status int, response any) {
	writer.Header().Set("Cache-Control", "no-store")
	writeJSONResponse(writer, status, response)
}

func (handler *HFSPathHandler) serveDirectory(writer http.ResponseWriter, directory *os.File, segments []string) {
	entries, err := hfs.ReadDirectory(directory)
	if err != nil {
		if errors.Is(err, hfs.ErrDirectoryTooLarge) {
			http.Error(writer, "HFS directory has too many entries", http.StatusRequestEntityTooLarge)
			return
		}
		slog.Error("read HFS directory", "root", handler.root.Name, "error", err)
		http.Error(writer, "HFS directory unavailable", http.StatusInternalServerError)
		return
	}
	response := make([]HFSEntry, 0, len(entries))
	for _, entry := range entries {
		directory := entry.IsDirectory
		modified := NewTimestamp(entry.LastModified)
		item := HFSEntry{
			Name: entry.Name, Link: hfsLink(handler.root.Name, append(append([]string(nil), segments...), entry.Name)),
			IsDirectory: &directory, LastModified: &modified,
		}
		if !entry.IsDirectory {
			size := entry.Size
			item.Size = &size
		}
		response = append(response, item)
	}
	writer.Header().Set("Cache-Control", handler.cacheControl())
	writeJSONResponse(writer, http.StatusOK, response)
}

func (handler *HFSPathHandler) serveFile(writer http.ResponseWriter, request *http.Request, file *os.File, info os.FileInfo, segments []string) {
	mediaType, inline, err := hfsFileMediaType(file, info.Name())
	if err != nil {
		slog.Error("inspect HFS file", "root", handler.root.Name, "error", err)
		http.Error(writer, "HFS file unavailable", http.StatusInternalServerError)
		return
	}
	if request.URL.Query().Has("download") {
		inline = false
	}
	if !inline {
		mediaType = "application/octet-stream"
		disposition := mime.FormatMediaType("attachment", map[string]string{"filename": info.Name()})
		if disposition == "" {
			disposition = "attachment"
		}
		writer.Header().Set("Content-Disposition", disposition)
	}
	writer.Header().Set("Content-Type", mediaType)
	writer.Header().Set("Cache-Control", handler.cacheControl())
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	if inline || handler.downloadRecorder == nil {
		http.ServeContent(writer, request, info.Name(), info.ModTime(), file)
		return
	}
	completed := &completedResponseWriter{ResponseWriter: writer}
	http.ServeContent(completed, request, info.Name(), info.ModTime(), file)
	if request.Method != http.MethodGet || completed.status < http.StatusOK || completed.status >= http.StatusMultipleChoices || completed.writeErr != nil {
		return
	}
	expected, err := strconv.ParseInt(writer.Header().Get("Content-Length"), 10, 64)
	if err != nil || expected < 0 || completed.written != expected {
		return
	}
	originalURL := request.RequestURI
	if originalURL == "" {
		originalURL = request.URL.RequestURI()
	}
	handler.downloadRecorder.Record(
		path.Join(append([]string{handler.root.Path}, segments...)...), originalURL, requestIP(request), request.UserAgent(),
	)
}

type completedResponseWriter struct {
	http.ResponseWriter
	status   int
	written  int64
	writeErr error
}

func (writer *completedResponseWriter) WriteHeader(status int) {
	if writer.status == 0 {
		writer.status = status
	}
	writer.ResponseWriter.WriteHeader(status)
}

func (writer *completedResponseWriter) Write(value []byte) (int, error) {
	if writer.status == 0 {
		writer.status = http.StatusOK
	}
	written, err := writer.ResponseWriter.Write(value)
	writer.written += int64(written)
	if err != nil && writer.writeErr == nil {
		writer.writeErr = err
	}
	return written, err
}

func (writer *completedResponseWriter) ReadFrom(reader io.Reader) (int64, error) {
	if writer.status == 0 {
		writer.status = http.StatusOK
	}
	readerFrom, ok := writer.ResponseWriter.(io.ReaderFrom)
	if !ok {
		return io.Copy(writerOnly{writer}, reader)
	}
	written, err := readerFrom.ReadFrom(reader)
	writer.written += written
	if err != nil && writer.writeErr == nil {
		writer.writeErr = err
	}
	return written, err
}

func (writer *completedResponseWriter) Unwrap() http.ResponseWriter {
	return writer.ResponseWriter
}

type writerOnly struct {
	io.Writer
}

func (handler *HFSPathHandler) writeOpenError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, storage.ErrNotFound), errors.Is(err, hfs.ErrUnsupportedEntry):
		http.NotFound(writer, nil)
	case errors.Is(err, storage.ErrInvalidPath), errors.Is(err, storage.ErrPathEscape):
		http.Error(writer, "invalid HFS path", http.StatusBadRequest)
	default:
		slog.Error("open HFS path", "root", handler.root.Name, "error", err)
		http.Error(writer, "HFS path unavailable", http.StatusInternalServerError)
	}
}

func (handler *HFSPathHandler) cacheControl() string {
	if handler.root.Public {
		return "public, max-age=0"
	}
	return "private, no-store"
}

func decodedHFSSegments(request *http.Request, rootName string) ([]string, error) {
	prefix := "/hfs/" + url.PathEscape(rootName)
	escaped := request.URL.EscapedPath()
	if escaped == prefix || escaped == prefix+"/" {
		return nil, nil
	}
	if !strings.HasPrefix(escaped, prefix+"/") {
		return nil, errors.New("request path does not match HFS root")
	}
	rawSegments := strings.Split(strings.TrimSuffix(strings.TrimPrefix(escaped, prefix+"/"), "/"), "/")
	segments := make([]string, 0, len(rawSegments))
	for _, raw := range rawSegments {
		segment, err := url.PathUnescape(raw)
		if err != nil {
			return nil, fmt.Errorf("decode HFS path segment: %w", err)
		}
		if err := hfs.ValidateSegment(segment); err != nil {
			return nil, err
		}
		segments = append(segments, segment)
	}
	return segments, nil
}

func validateHFSEscapedPath(escaped string) error {
	decoded, err := url.PathUnescape(escaped)
	if err != nil {
		return err
	}
	if (decoded == "/hfs" || strings.HasPrefix(decoded, "/hfs/")) &&
		escaped != "/hfs" && !strings.HasPrefix(escaped, "/hfs/") {
		return errors.New("HFS route prefix must not contain escapes")
	}
	if escaped == "/hfs" || escaped == "/hfs/" || !strings.HasPrefix(escaped, "/hfs/") {
		return nil
	}
	rawSegments := strings.Split(strings.TrimPrefix(escaped, "/hfs/"), "/")
	if rawSegments[len(rawSegments)-1] == "" {
		rawSegments = rawSegments[:len(rawSegments)-1]
	}
	for _, raw := range rawSegments {
		segment, err := url.PathUnescape(raw)
		if err != nil {
			return err
		}
		if err := hfs.ValidateSegment(segment); err != nil {
			return err
		}
	}
	return nil
}

func hfsLink(rootName string, segments []string) string {
	parts := make([]string, 0, 2+len(segments))
	parts = append(parts, "", "hfs", url.PathEscape(rootName))
	for _, segment := range segments {
		parts = append(parts, url.PathEscape(segment))
	}
	return strings.Join(parts, "/")
}

func hfsFileMediaType(file *os.File, name string) (string, bool, error) {
	header, err := io.ReadAll(io.LimitReader(file, hfsSniffBytes))
	if err != nil {
		return "", false, err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", false, err
	}
	if format, err := imageproc.Detect(header); err == nil {
		return format.MIMEType, true, nil
	}
	detected := http.DetectContentType(header)
	baseType, _, err := mime.ParseMediaType(detected)
	if err == nil && (strings.HasPrefix(baseType, "audio/") || strings.HasPrefix(baseType, "video/") || baseType == "application/pdf") {
		return detected, true, nil
	}
	switch strings.ToLower(strings.TrimPrefix(filepathExtension(name), ".")) {
	case "json":
		return "application/json", true, nil
	case "txt", "log", "err", "error", "ini":
		return "text/plain", true, nil
	default:
		return "application/octet-stream", false, nil
	}
}

func filepathExtension(name string) string {
	index := strings.LastIndexByte(name, '.')
	if index < 0 {
		return ""
	}
	return name[index:]
}
