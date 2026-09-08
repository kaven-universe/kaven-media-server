package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kaven.xyz/kaven/kaven-media-server/internal/config"
	"kaven.xyz/kaven/kaven-media-server/internal/hfs"
	"kaven.xyz/kaven/kaven-media-server/internal/storage"
)

func TestHFSRootHandlerReturnsLegacyEntriesInConfigurationOrder(t *testing.T) {
	store, err := storage.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	registry, err := hfs.NewRegistry(store, []config.HFSRoot{
		{Name: "uploaded", Path: "hfs/uploaded"},
		{Name: "shared", Path: "hfs/shared", Public: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	modified := time.Date(2026, time.September, 4, 1, 2, 3, 4_000_000, time.UTC)
	for _, path := range []string{"hfs/uploaded", "hfs/shared"} {
		resolved, err := store.ResolveExisting(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(resolved, modified, modified); err != nil {
			t.Fatal(err)
		}
	}

	request := httptest.NewRequest(http.MethodGet, "/hfs", nil)
	response := httptest.NewRecorder()
	NewHFSRootHandler(registry).ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "application/json; charset=utf-8" ||
		response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("response = %d, headers = %#v", response.Code, response.Header())
	}
	var entries []HFSEntry
	if err := json.Unmarshal(response.Body.Bytes(), &entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Name != "uploaded" || entries[0].Link != "/hfs/uploaded" ||
		entries[0].IsDirectory == nil || !*entries[0].IsDirectory || entries[0].Size != nil ||
		entries[0].LastModified == nil || !entries[0].LastModified.Equal(modified) || entries[1].Name != "shared" {
		t.Fatalf("entries = %#v", entries)
	}
}

func TestHFSPathHandlerListsDirectoryAndEscapesLinks(t *testing.T) {
	store, registry, root := testHFSPathHandler(t, true)
	if err := store.MkdirAll("hfs/shared/images", 0o755); err != nil {
		t.Fatal(err)
	}
	writeHFSFile(t, store, "hfs/shared/hello world.txt", []byte("hello"))
	modified := time.Date(2026, time.September, 4, 1, 2, 3, 4_000_000, time.UTC)
	for _, path := range []string{"hfs/shared/images", "hfs/shared/hello world.txt"} {
		resolved, err := store.ResolveExisting(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(resolved, modified, modified); err != nil {
			t.Fatal(err)
		}
	}

	response := serveHFSPath(NewHFSPathHandler(registry, root, nil), "/hfs/shared")
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "public, max-age=0" {
		t.Fatalf("response = %d, headers = %#v", response.Code, response.Header())
	}
	var entries []HFSEntry
	if err := json.Unmarshal(response.Body.Bytes(), &entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Name != "hello world.txt" || entries[0].Link != "/hfs/shared/hello%20world.txt" ||
		entries[0].Size == nil || *entries[0].Size != 5 || entries[0].IsDirectory == nil || *entries[0].IsDirectory ||
		entries[1].Name != "images" || entries[1].Link != "/hfs/shared/images" || entries[1].IsDirectory == nil || !*entries[1].IsDirectory {
		t.Fatalf("entries = %#v", entries)
	}
}

func TestHFSPathHandlerRendersSafeTypesAndDownloadsOthers(t *testing.T) {
	store, registry, root := testHFSPathHandler(t, true)
	text := []byte("Kaven Media Server legacy HFS fixture.\n")
	writeHFSFile(t, store, "hfs/shared/hello world.txt", text)
	png := []byte("\x89PNG\r\n\x1a\ncontents")
	writeHFSFile(t, store, "hfs/shared/pixel.bin", png)
	writeHFSFile(t, store, "hfs/shared/page.html", []byte("<script>alert(1)</script>"))
	writeHFSFile(t, store, "hfs/shared/%2e%2e", []byte("literal"))

	tests := []struct {
		name        string
		target      string
		contentType string
		disposition bool
		body        []byte
	}{
		{name: "text", target: "/hfs/shared/hello%20world.txt", contentType: "text/plain", body: text},
		{name: "image magic", target: "/hfs/shared/pixel.bin", contentType: "image/png", body: png},
		{name: "active type downloads", target: "/hfs/shared/page.html", contentType: "application/octet-stream", disposition: true, body: []byte("<script>alert(1)</script>")},
		{name: "forced download", target: "/hfs/shared/hello%20world.txt?download", contentType: "application/octet-stream", disposition: true, body: text},
		{name: "decode once", target: "/hfs/shared/%252e%252e", contentType: "application/octet-stream", disposition: true, body: []byte("literal")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := serveHFSPath(NewHFSPathHandler(registry, root, nil), test.target)
			if response.Code != http.StatusOK || response.Header().Get("Content-Type") != test.contentType || !bytes.Equal(response.Body.Bytes(), test.body) {
				t.Fatalf("response = %d %q %q", response.Code, response.Header().Get("Content-Type"), response.Body.Bytes())
			}
			hasDisposition := strings.HasPrefix(response.Header().Get("Content-Disposition"), "attachment")
			if hasDisposition != test.disposition {
				t.Fatalf("Content-Disposition = %q", response.Header().Get("Content-Disposition"))
			}
			if response.Header().Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(response.Header().Get("Content-Security-Policy"), "sandbox") {
				t.Fatalf("security headers = %#v", response.Header())
			}
		})
	}
}

func TestHFSPathHandlerSupportsRanges(t *testing.T) {
	store, registry, root := testHFSPathHandler(t, true)
	writeHFSFile(t, store, "hfs/shared/file.txt", []byte("abcdef"))
	request := httptest.NewRequest(http.MethodGet, "/hfs/shared/file.txt", nil)
	request.Header.Set("Range", "bytes=1-3")
	response := httptest.NewRecorder()
	NewHFSPathHandler(registry, root, nil).ServeHTTP(response, request)
	if response.Code != http.StatusPartialContent || response.Body.String() != "bcd" || response.Header().Get("Content-Range") != "bytes 1-3/6" {
		t.Fatalf("response = %d %q, Content-Range = %q", response.Code, response.Body.String(), response.Header().Get("Content-Range"))
	}
}

func TestHFSPathHandlerRecordsOnlyCompletedDownloads(t *testing.T) {
	store, registry, root := testHFSPathHandler(t, true)
	writeHFSFile(t, store, "hfs/shared/file.bin", []byte("abcdef"))
	writeHFSFile(t, store, "hfs/shared/file.txt", []byte("text"))
	recorder := &recordingHFSDownloadRecorder{}
	handler := NewHFSPathHandler(registry, root, recorder)

	request := httptest.NewRequest(http.MethodGet, "/hfs/shared/file.bin?download", nil)
	request.RemoteAddr = "192.0.2.10:1234"
	request.Header.Set("User-Agent", "Kaven client")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "abcdef" {
		t.Fatalf("download response = %d %q", response.Code, response.Body.String())
	}
	if len(recorder.records) != 1 || recorder.records[0] != (hfsDownloadRecord{
		file: "hfs/shared/file.bin", originalURL: "/hfs/shared/file.bin?download",
		ip: "192.0.2.10", userAgent: "Kaven client",
	}) {
		t.Fatalf("download records = %#v", recorder.records)
	}

	request = httptest.NewRequest(http.MethodGet, "/hfs/shared/file.bin?download", nil)
	request.Header.Set("Range", "bytes=1-3")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusPartialContent || response.Body.String() != "bcd" || len(recorder.records) != 2 {
		t.Fatalf("range response = %d %q, records = %#v", response.Code, response.Body.String(), recorder.records)
	}

	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/hfs/shared/file.txt", nil),
		httptest.NewRequest(http.MethodHead, "/hfs/shared/file.bin?download", nil),
	} {
		response = httptest.NewRecorder()
		handler.ServeHTTP(response, request)
	}
	request = httptest.NewRequest(http.MethodGet, "/hfs/shared/file.bin?download", nil)
	request.Header.Set("If-Modified-Since", time.Now().Add(time.Hour).UTC().Format(http.TimeFormat))
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotModified || len(recorder.records) != 2 {
		t.Fatalf("conditional response = %d, records = %#v", response.Code, recorder.records)
	}
}

func TestHFSPathHandlerDoesNotRecordFailedResponse(t *testing.T) {
	store, registry, root := testHFSPathHandler(t, true)
	writeHFSFile(t, store, "hfs/shared/file.bin", []byte("abcdef"))
	recorder := &recordingHFSDownloadRecorder{}
	request := httptest.NewRequest(http.MethodGet, "/hfs/shared/file.bin?download", nil)
	writer := &failingHFSResponseWriter{header: make(http.Header)}
	NewHFSPathHandler(registry, root, recorder).ServeHTTP(writer, request)
	if len(recorder.records) != 0 {
		t.Fatalf("download records = %#v, want none", recorder.records)
	}
}

func TestHFSPathHandlerRejectsUnsafeAndMissingPaths(t *testing.T) {
	_, registry, root := testHFSPathHandler(t, true)
	tests := []struct {
		target string
		status int
	}{
		{target: "/hfs/shared/%2e%2e/secret", status: http.StatusBadRequest},
		{target: "/hfs/shared/a%2Fb", status: http.StatusBadRequest},
		{target: "/hfs/shared/a%5Cb", status: http.StatusBadRequest},
		{target: "/hfs/shared/a%3Ab", status: http.StatusBadRequest},
		{target: "/hfs/shared/a//b", status: http.StatusBadRequest},
		{target: "/hfs/shared/line%0Abreak", status: http.StatusBadRequest},
		{target: "/hfs/other/file", status: http.StatusBadRequest},
		{target: "/hfs/shared/missing", status: http.StatusNotFound},
	}
	for _, test := range tests {
		response := serveHFSPath(NewHFSPathHandler(registry, root, nil), test.target)
		if response.Code != test.status {
			t.Errorf("%s status = %d, want %d", test.target, response.Code, test.status)
		}
	}
}

type hfsDownloadRecord struct {
	file        string
	originalURL string
	ip          string
	userAgent   string
}

type recordingHFSDownloadRecorder struct {
	records []hfsDownloadRecord
}

func (recorder *recordingHFSDownloadRecorder) Record(file, originalURL, ip, userAgent string) bool {
	recorder.records = append(recorder.records, hfsDownloadRecord{
		file: file, originalURL: originalURL, ip: ip, userAgent: userAgent,
	})
	return true
}

type failingHFSResponseWriter struct {
	header http.Header
}

func (writer *failingHFSResponseWriter) Header() http.Header {
	return writer.header
}

func (*failingHFSResponseWriter) WriteHeader(int) {}

func (*failingHFSResponseWriter) Write([]byte) (int, error) {
	return 0, errors.New("client disconnected")
}

func TestHFSWriteHandlerCreatesDirectoryWithLegacyResponses(t *testing.T) {
	store, registry, root := testHFSPathHandler(t, false)
	handler := NewHFSWriteHandler(registry, root, store, 3, 10)

	response := serveHFSWrite(handler, httptest.NewRequest(http.MethodPost, "/hfs/shared/new-folder?mkdir", nil))
	assertHFSMutation(t, response, http.StatusOK, ErrorNone)
	path, err := store.ResolveExisting("hfs/shared/new-folder")
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		t.Fatalf("created path is not a directory: %v", err)
	}
	response = serveHFSWrite(handler, httptest.NewRequest(http.MethodPost, "/hfs/shared/new-folder?mkdir", nil))
	assertHFSMutation(t, response, http.StatusBadRequest, ErrorFileAlreadyExists)
	response = serveHFSWrite(handler, httptest.NewRequest(http.MethodPost, "/hfs/shared/missing/nested?mkdir", nil))
	assertHFSMutation(t, response, http.StatusBadRequest, ErrorFolderNotFound)
}

func TestHFSWriteHandlerUploadsSingleAndMultipleFiles(t *testing.T) {
	store, registry, root := testHFSPathHandler(t, false)
	handler := NewHFSWriteHandler(registry, root, store, 3, 20)

	request := newHFSMultipartRequest(t, "/hfs/shared", []hfsMultipartFile{{field: "file", name: "folder/hello.txt", body: "hello"}}, nil)
	response := serveHFSWrite(handler, request)
	assertHFSMutation(t, response, http.StatusOK, ErrorNone)
	contents, err := os.ReadFile(filepath.Join(store.Root(), "hfs", "shared", "hello.txt"))
	if err != nil || string(contents) != "hello" {
		t.Fatalf("uploaded file = %q, error = %v", contents, err)
	}

	request = newHFSMultipartRequest(t, "/hfs/shared", []hfsMultipartFile{
		{field: "file", name: "one.bin", body: "one"},
		{field: "file", name: "two.bin", body: "two"},
	}, map[string]string{"ignored": "value"})
	response = serveHFSWrite(handler, request)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("response = %d, headers = %#v", response.Code, response.Header())
	}
	var results []UploadResponse
	if err := json.Unmarshal(response.Body.Bytes(), &results); err != nil || len(results) != 2 ||
		results[0].ErrorCode != ErrorNone || results[1].ErrorCode != ErrorNone {
		t.Fatalf("results = %#v, error = %v", results, err)
	}
}

func TestHFSWriteHandlerRejectsInvalidAndOversizedUploads(t *testing.T) {
	store, registry, root := testHFSPathHandler(t, false)
	handler := NewHFSWriteHandler(registry, root, store, 1, 4)
	tests := []struct {
		name  string
		files []hfsMultipartFile
		code  ErrorCode
	}{
		{name: "empty", code: ErrorUnexpected},
		{name: "wrong field", files: []hfsMultipartFile{{field: "other", name: "file.txt", body: "data"}}, code: ErrorUnexpected},
		{name: "too many", files: []hfsMultipartFile{{field: "file", name: "one", body: "1"}, {field: "file", name: "two", body: "2"}}, code: ErrorUnexpected},
		{name: "too large", files: []hfsMultipartFile{{field: "file", name: "large", body: "12345"}}, code: ErrorFileTooLarge},
		{name: "invalid name", files: []hfsMultipartFile{{field: "file", name: "bad:name", body: "data"}}, code: ErrorUnexpected},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := newHFSMultipartRequest(t, "/hfs/shared", test.files, nil)
			response := serveHFSWrite(handler, request)
			assertHFSMutation(t, response, http.StatusBadRequest, test.code)
		})
	}
	temporary, err := os.ReadDir(filepath.Join(store.Root(), "tmp"))
	if err != nil || len(temporary) != 0 {
		t.Fatalf("temporary files = %#v, error = %v", temporary, err)
	}
}

func TestHFSWriteHandlerCleansUpPartialMultipartUpload(t *testing.T) {
	store, registry, root := testHFSPathHandler(t, false)
	handler := NewHFSWriteHandler(registry, root, store, 3, 100)
	body, contentType := hfsMultipartBody(t, []hfsMultipartFile{
		{field: "file", name: "complete.txt", body: "complete"},
		{field: "file", name: "partial.txt", body: "partial contents"},
	}, nil)
	partialAt := bytes.Index(body, []byte("partial contents")) + len("partial")
	if partialAt < len("partial") {
		t.Fatal("partial fixture contents were not found")
	}
	request := httptest.NewRequest(http.MethodPost, "/hfs/shared", io.LimitReader(bytes.NewReader(body), int64(partialAt)))
	request.Header.Set("Content-Type", contentType)
	response := serveHFSWrite(handler, request)
	assertHFSMutation(t, response, http.StatusBadRequest, ErrorUnexpected)
	for _, name := range []string{"complete.txt", "partial.txt"} {
		if _, err := os.Stat(filepath.Join(store.Root(), "hfs", "shared", name)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("partial request published %q: %v", name, err)
		}
	}
	temporary, err := os.ReadDir(filepath.Join(store.Root(), "tmp"))
	if err != nil || len(temporary) != 0 {
		t.Fatalf("temporary files = %#v, error = %v", temporary, err)
	}
}

func TestGuardHFSPathsRejectsEncodedTraversalBeforeDispatch(t *testing.T) {
	dispatched := 0
	handler := GuardHFSPaths(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		dispatched++
	}))
	for _, target := range []string{
		"/hfs/shared/%2e%2e/secret",
		"/hfs/shared/%2E%2E/secret",
		"/hfs/shared/folder%2fsecret",
		"/hfs/shared/folder%5Csecret",
		"/hfs%2Fshared/file",
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, target, strings.NewReader("must not be read")))
		if response.Code != http.StatusBadRequest {
			t.Errorf("%s status = %d, want 400", target, response.Code)
		}
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/hfs/shared/%252e%252e", nil))
	if response.Code != http.StatusOK || dispatched != 1 {
		t.Fatalf("double-encoded literal status = %d, dispatched = %d", response.Code, dispatched)
	}
}

func TestHFSWriteHandlerPreservesExistingFileAndRollsBackBatch(t *testing.T) {
	store, registry, root := testHFSPathHandler(t, false)
	handler := NewHFSWriteHandler(registry, root, store, 3, 20)
	writeHFSFile(t, store, "hfs/shared/existing.txt", []byte("original"))
	request := newHFSMultipartRequest(t, "/hfs/shared", []hfsMultipartFile{
		{field: "file", name: "created.txt", body: "created"},
		{field: "file", name: "existing.txt", body: "replacement"},
	}, nil)
	response := serveHFSWrite(handler, request)
	assertHFSMutation(t, response, http.StatusBadRequest, ErrorFileAlreadyExists)
	if _, err := os.Stat(filepath.Join(store.Root(), "hfs", "shared", "created.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rolled-back file exists: %v", err)
	}
	contents, err := os.ReadFile(filepath.Join(store.Root(), "hfs", "shared", "existing.txt"))
	if err != nil || string(contents) != "original" {
		t.Fatalf("existing file = %q, error = %v", contents, err)
	}
}

func TestHFSWriteHandlerReportsMissingDestinationBeforeReadingMultipart(t *testing.T) {
	store, registry, root := testHFSPathHandler(t, false)
	handler := NewHFSWriteHandler(registry, root, store, 1, 4)
	response := serveHFSWrite(handler, httptest.NewRequest(http.MethodPost, "/hfs/shared/missing", strings.NewReader("not multipart")))
	assertHFSMutation(t, response, http.StatusBadRequest, ErrorFolderNotFound)
}

func TestSafeHFSFilenameUsesOnlyBasename(t *testing.T) {
	for input, want := range map[string]string{
		"file.txt": "file.txt", "folder/file.txt": "file.txt", `folder\file.txt`: "file.txt",
	} {
		if got, err := safeHFSFilename(input); err != nil || got != want {
			t.Errorf("safeHFSFilename(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
	for _, input := range []string{".", "..", "bad:name", "line\nbreak"} {
		if _, err := safeHFSFilename(input); err == nil {
			t.Errorf("unsafe filename %q succeeded", input)
		}
	}
}

type hfsMultipartFile struct {
	field string
	name  string
	body  string
}

func newHFSMultipartRequest(t *testing.T, target string, files []hfsMultipartFile, fields map[string]string) *http.Request {
	t.Helper()
	body, contentType := hfsMultipartBody(t, files, fields)
	request := httptest.NewRequest(http.MethodPost, target, bytes.NewReader(body))
	request.Header.Set("Content-Type", contentType)
	return request
}

func hfsMultipartBody(t *testing.T, files []hfsMultipartFile, fields map[string]string) ([]byte, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for name, value := range fields {
		if err := writer.WriteField(name, value); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range files {
		part, err := writer.CreateFormFile(file.field, file.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(part, file.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return body.Bytes(), writer.FormDataContentType()
}

func serveHFSWrite(handler http.Handler, request *http.Request) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func assertHFSMutation(t *testing.T, response *httptest.ResponseRecorder, status int, code ErrorCode) {
	t.Helper()
	if response.Code != status || response.Header().Get("Content-Type") != "application/json; charset=utf-8" {
		t.Fatalf("response = %d, headers = %#v", response.Code, response.Header())
	}
	var result UploadResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || result.ErrorCode != code {
		t.Fatalf("response = %q, result = %#v, error = %v", response.Body.String(), result, err)
	}
}

func testHFSPathHandler(t *testing.T, public bool) (*storage.Store, *hfs.Registry, hfs.Root) {
	t.Helper()
	store, err := storage.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	registry, err := hfs.NewRegistry(store, []config.HFSRoot{{Name: "shared", Path: "hfs/shared", Public: public}})
	if err != nil {
		t.Fatal(err)
	}
	root, found := registry.Lookup("shared")
	if !found {
		t.Fatal("root was not registered")
	}
	return store, registry, root
}

func writeHFSFile(t *testing.T, store *storage.Store, relative string, contents []byte) {
	t.Helper()
	path, err := store.Resolve(relative)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
}

func serveHFSPath(handler http.Handler, target string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, target, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestHFSRootHandlerOmitsRemovedRoot(t *testing.T) {
	store, err := storage.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	registry, err := hfs.NewRegistry(store, []config.HFSRoot{{Name: "removed", Path: "hfs/removed"}})
	if err != nil {
		t.Fatal(err)
	}
	path, err := store.ResolveExisting("hfs/removed")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	NewHFSRootHandler(registry).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/hfs", nil))
	if response.Code != http.StatusOK || response.Body.String() != "[]\n" {
		t.Fatalf("response = %d %q", response.Code, response.Body.String())
	}
}
