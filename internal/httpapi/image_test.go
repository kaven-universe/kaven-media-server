package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"kaven.xyz/kaven/kaven-media-server/internal/imagehost"
	"kaven.xyz/kaven/kaven-media-server/internal/imageproc"
	"kaven.xyz/kaven/kaven-media-server/internal/repository"
	"kaven.xyz/kaven/kaven-media-server/internal/storage"
)

func TestImageHandlerMetadataByEveryIdentifier(t *testing.T) {
	timestamp := time.Date(2026, time.September, 4, 1, 2, 3, 4_000_000, time.UTC)
	image := repository.Image{
		ID: "abcdef0123456789abcdef01", UUID: "abcdef0123456789abcdef0123456789",
		SHA1: "abcdef0123456789abcdef0123456789abcdef01", Folder: "images/2026/09",
		Name: "stored.png", OriginalName: "original.png", MIMEType: "image/png",
		Size: 123, UploadDate: timestamp, UploadIP: "127.0.0.1",
		CreatedAt: timestamp, UpdatedAt: timestamp,
	}
	fake := &imageLookupRepository{image: image}
	handler, _ := newImageTestHandler(t, fake)
	for _, identifier := range []string{image.ID, image.UUID, image.SHA1} {
		t.Run(identifier, func(t *testing.T) {
			response := serveImageRequest(handler, "/image/"+identifier+"?json=false")
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			if got := response.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
				t.Fatalf("Content-Type = %q", got)
			}
			var metadata ImageMetadata
			if err := json.Unmarshal(response.Body.Bytes(), &metadata); err != nil {
				t.Fatalf("decode metadata: %v", err)
			}
			if metadata.ID != image.ID || metadata.UUID != image.UUID || metadata.SHA1 != image.SHA1 || metadata.Path != "images/2026/09/stored.png" {
				t.Fatalf("metadata = %#v", metadata)
			}
		})
	}
}

func TestImageHandlerLookupErrors(t *testing.T) {
	tests := []struct {
		name       string
		identifier string
		repository error
		status     int
	}{
		{name: "invalid", identifier: "invalid", status: http.StatusBadRequest},
		{name: "missing", identifier: "abcdef0123456789abcdef01", repository: repository.ErrNotFound, status: http.StatusNotFound},
		{name: "database failure", identifier: "abcdef0123456789abcdef01", repository: errors.New("database failure"), status: http.StatusInternalServerError},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake := &imageLookupRepository{err: test.repository}
			handler, _ := newImageTestHandler(t, fake)
			response := serveImageRequest(handler, "/image/"+test.identifier+"?json")
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d", response.Code, test.status)
			}
			if response.Body.Len() != 0 {
				t.Fatalf("error body = %q, want empty", response.Body.String())
			}
		})
	}
}

func TestImageHandlerServesOriginalWithHeadersAndRanges(t *testing.T) {
	modified := time.Date(2026, time.September, 4, 1, 2, 3, 0, time.UTC)
	contents := []byte("\x89PNG\r\n\x1a\noriginal-image-bytes")
	image := repository.Image{
		ID: "abcdef0123456789abcdef01", SHA1: "abcdef0123456789abcdef0123456789abcdef01",
		Folder: "images/2026/09", Name: "stored.png", OriginalName: "original.png",
		MIMEType: "text/html",
	}
	fake := &imageLookupRepository{image: image}
	handler, store := newImageTestHandler(t, fake)
	if err := store.MkdirAll(image.Folder, 0o755); err != nil {
		t.Fatalf("create image folder: %v", err)
	}
	filePath := filepath.Join(store.Root(), filepath.FromSlash(image.Folder), image.Name)
	if err := os.WriteFile(filePath, contents, 0o600); err != nil {
		t.Fatalf("write image: %v", err)
	}
	if err := os.Chtimes(filePath, modified, modified); err != nil {
		t.Fatalf("set image timestamp: %v", err)
	}

	response := serveImageRequest(handler, "/image/"+image.ID)
	if response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), contents) {
		t.Fatalf("response = %d %q", response.Code, response.Body.Bytes())
	}
	wantHeaders := map[string]string{
		"Content-Type":            "image/png",
		"Content-Length":          "28",
		"Accept-Ranges":           "bytes",
		"Last-Modified":           "Fri, 04 Sep 2026 01:02:03 GMT",
		"ETag":                    `"abcdef0123456789abcdef0123456789abcdef01"`,
		"Cache-Control":           "public, max-age=0",
		"X-Content-Type-Options":  "nosniff",
		"Content-Security-Policy": "default-src 'none'; style-src 'unsafe-inline'; sandbox",
	}
	for name, want := range wantHeaders {
		if got := response.Header().Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}

	rangeResponse := serveImageRequestWithHeaders(handler, http.MethodGet, "/image/"+image.ID, map[string]string{"Range": "bytes=8-15"})
	if rangeResponse.Code != http.StatusPartialContent || rangeResponse.Body.String() != "original" {
		t.Fatalf("range response = %d %q", rangeResponse.Code, rangeResponse.Body.String())
	}
	if got, want := rangeResponse.Header().Get("Content-Range"), "bytes 8-15/28"; got != want {
		t.Fatalf("Content-Range = %q, want %q", got, want)
	}
	unsatisfied := serveImageRequestWithHeaders(handler, http.MethodGet, "/image/"+image.ID, map[string]string{"Range": "bytes=100-200"})
	if unsatisfied.Code != http.StatusRequestedRangeNotSatisfiable || unsatisfied.Header().Get("Content-Range") != "bytes */28" {
		t.Fatalf("unsatisfied range = %d, Content-Range %q", unsatisfied.Code, unsatisfied.Header().Get("Content-Range"))
	}

	notModified := serveImageRequestWithHeaders(handler, http.MethodGet, "/image/"+image.ID, map[string]string{"If-None-Match": wantHeaders["ETag"]})
	if notModified.Code != http.StatusNotModified || notModified.Body.Len() != 0 {
		t.Fatalf("conditional response = %d %q", notModified.Code, notModified.Body.String())
	}

	head := serveImageRequestWithHeaders(handler, http.MethodHead, "/image/"+image.ID, nil)
	if head.Code != http.StatusOK || head.Body.Len() != 0 || head.Header().Get("Content-Length") != "28" {
		t.Fatalf("HEAD response = %d, length %q, body %q", head.Code, head.Header().Get("Content-Length"), head.Body.String())
	}
}

func TestImageHandlerRejectsAbsoluteReference(t *testing.T) {
	contents := []byte("\x89PNG\r\n\x1a\nreferenced-image")
	external := filepath.Join(t.TempDir(), "referenced.png")
	if err := os.WriteFile(external, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	image := repository.Image{
		ID: "abcdef0123456789abcdef01", Folder: filepath.Dir(external), Name: filepath.Base(external),
		OriginalName: "referenced.png",
	}
	handler, _ := newImageTestHandler(t, &imageLookupRepository{image: image})
	response := serveImageRequest(handler, "/image/"+image.ID)
	if response.Code != http.StatusInternalServerError || response.Body.Len() != 0 {
		t.Fatalf("response = %d %q", response.Code, response.Body.Bytes())
	}
}

func TestImageHandlerRejectsMissingUnsafeAndInvalidStoredFiles(t *testing.T) {
	tests := []struct {
		name   string
		image  repository.Image
		write  []byte
		status int
	}{
		{
			name: "missing", image: repository.Image{ID: "abcdef0123456789abcdef01", Folder: "images", Name: "missing.png"},
			status: http.StatusNotFound,
		},
		{
			name: "unsafe", image: repository.Image{ID: "abcdef0123456789abcdef01", Folder: "..", Name: "escape.png"},
			status: http.StatusInternalServerError,
		},
		{
			name: "invalid content", image: repository.Image{ID: "abcdef0123456789abcdef01", Folder: "images", Name: "invalid.png", MIMEType: "image/png"},
			write: []byte("<html>not an image</html>"), status: http.StatusUnsupportedMediaType,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake := &imageLookupRepository{image: test.image}
			handler, store := newImageTestHandler(t, fake)
			if test.write != nil {
				if err := store.MkdirAll(test.image.Folder, 0o755); err != nil {
					t.Fatalf("create folder: %v", err)
				}
				if err := os.WriteFile(filepath.Join(store.Root(), test.image.Folder, test.image.Name), test.write, 0o600); err != nil {
					t.Fatalf("write file: %v", err)
				}
			}
			response := serveImageRequest(handler, "/image/"+test.image.ID)
			if response.Code != test.status || response.Body.Len() != 0 {
				t.Fatalf("response = %d %q, want %d", response.Code, response.Body.String(), test.status)
			}
		})
	}
}

func TestImageHandlerServesTransformation(t *testing.T) {
	image := repository.Image{ID: "abcdef0123456789abcdef01", Folder: "images", Name: "source.jpg"}
	transformer := &testImageTransformer{result: imageproc.Result{
		Bytes: []byte("transformed"), MIMEType: "image/webp", Width: 80, Height: 60,
	}}
	handler, store := newImageTestHandlerWithTransformer(t, &imageLookupRepository{image: image}, transformer)
	if err := store.MkdirAll(image.Folder, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store.Root(), image.Folder, image.Name), []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}

	response := serveImageRequest(handler, "/image/"+image.ID+"?width=80&height=60&quality=75")
	if response.Code != http.StatusOK || response.Body.String() != "transformed" {
		t.Fatalf("response = %d %q", response.Code, response.Body.String())
	}
	if got := response.Header().Get("Content-Type"); got != "image/webp" {
		t.Fatalf("Content-Type = %q", got)
	}
	if got := response.Header().Get("Content-Length"); got != "11" {
		t.Fatalf("Content-Length = %q", got)
	}
	if transformer.options != (imageproc.Options{Width: 80, Height: 60, Quality: 75}) {
		t.Fatalf("options = %#v", transformer.options)
	}
	if !filepath.IsAbs(transformer.filePath) {
		t.Fatalf("transform path = %q, want absolute", transformer.filePath)
	}

	head := serveImageRequestWithHeaders(handler, http.MethodHead, "/image/"+image.ID+"?quality=90", nil)
	if head.Code != http.StatusOK || head.Body.Len() != 0 || transformer.options.Quality != 90 {
		t.Fatalf("HEAD response = %d %q, options %#v", head.Code, head.Body.String(), transformer.options)
	}
}

func TestImageHandlerRejectsInvalidTransformationParameters(t *testing.T) {
	image := repository.Image{ID: "abcdef0123456789abcdef01", Folder: "images", Name: "source.jpg"}
	handler, _ := newImageTestHandler(t, &imageLookupRepository{image: image})
	for _, query := range []string{"width=", "width=abc", "width=0", "height=-1", "quality=101", "width=1&width=2"} {
		response := serveImageRequest(handler, "/image/"+image.ID+"?"+query)
		if response.Code != http.StatusBadRequest {
			t.Errorf("query %q status = %d, want 400", query, response.Code)
		}
	}
}

func TestImageHandlerMapsTransformationErrors(t *testing.T) {
	image := repository.Image{ID: "abcdef0123456789abcdef01", Folder: "images", Name: "source.jpg"}
	tests := []struct {
		err    error
		status int
	}{
		{err: imageproc.ErrInvalid, status: http.StatusBadRequest},
		{err: imageproc.ErrUnsupported, status: http.StatusUnsupportedMediaType},
		{err: imageproc.ErrUnavailable, status: http.StatusNotImplemented},
		{err: errors.New("processing failed"), status: http.StatusInternalServerError},
	}
	for _, test := range tests {
		transformer := &testImageTransformer{err: test.err}
		handler, store := newImageTestHandlerWithTransformer(t, &imageLookupRepository{image: image}, transformer)
		if err := store.MkdirAll(image.Folder, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(store.Root(), image.Folder, image.Name), []byte("source"), 0o600); err != nil {
			t.Fatal(err)
		}
		response := serveImageRequest(handler, "/image/"+image.ID+"?width=10")
		if response.Code != test.status {
			t.Errorf("error %v status = %d, want %d", test.err, response.Code, test.status)
		}
	}
}

func TestImageHandlerRecordsByteRequestsWithoutChangingResponse(t *testing.T) {
	image := repository.Image{ID: "abcdef0123456789abcdef01", Folder: "images", Name: "source.jpg"}
	store, err := storage.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MkdirAll(image.Folder, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store.Root(), image.Folder, image.Name), []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	accesses := &testImageAccessRecorder{accepted: false}
	handler := NewImageHandler(
		imagehost.NewLookupService(&imageLookupRepository{image: image}), store,
		&testImageTransformer{result: imageproc.Result{Bytes: []byte("derived"), MIMEType: "image/jpeg"}}, accesses,
	)

	metadata := serveImageRequest(handler, "/image/"+image.ID+"?json")
	if metadata.Code != http.StatusOK || accesses.calls != 0 {
		t.Fatalf("metadata response = %d, access calls = %d", metadata.Code, accesses.calls)
	}
	response := serveImageRequest(handler, "/image/"+image.ID+"?width=10&tracking=value")
	if response.Code != http.StatusOK || response.Body.String() != "derived" {
		t.Fatalf("response = %d %q", response.Code, response.Body.String())
	}
	if accesses.calls != 1 || accesses.imageID != image.ID ||
		accesses.originalURL != "/image/"+image.ID+"?width=10&tracking=value" || accesses.ip != "192.0.2.1" {
		t.Fatalf("access record = %#v", accesses)
	}
}

type imageLookupRepository struct {
	image repository.Image
	err   error
}

type testImageTransformer struct {
	result   imageproc.Result
	err      error
	filePath string
	options  imageproc.Options
}

func (transformer *testImageTransformer) Transform(_ context.Context, filePath string, _ repository.Image, options imageproc.Options) (imageproc.Result, error) {
	transformer.filePath = filePath
	transformer.options = options
	return transformer.result, transformer.err
}

func (fake *imageLookupRepository) GetByID(context.Context, string) (repository.Image, error) {
	return fake.image, fake.err
}

func (fake *imageLookupRepository) GetByUUID(context.Context, string) (repository.Image, error) {
	return fake.image, fake.err
}

func (fake *imageLookupRepository) GetBySHA1(context.Context, string) (repository.Image, error) {
	return fake.image, fake.err
}

func serveImageRequest(handler http.Handler, target string) *httptest.ResponseRecorder {
	return serveImageRequestWithHeaders(handler, http.MethodGet, target, nil)
}

func serveImageRequestWithHeaders(handler http.Handler, method, target string, headers map[string]string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	mux.Handle("GET /image/{id}", handler)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(method, target, nil)
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	mux.ServeHTTP(response, request)
	return response
}

func newImageTestHandler(t *testing.T, repository *imageLookupRepository) (*ImageHandler, *storage.Store) {
	return newImageTestHandlerWithTransformer(t, repository, &testImageTransformer{err: errors.New("unexpected transformation")})
}

func newImageTestHandlerWithTransformer(t *testing.T, repository *imageLookupRepository, transformer imageTransformer) (*ImageHandler, *storage.Store) {
	t.Helper()
	store, err := storage.New(t.TempDir())
	if err != nil {
		t.Fatalf("create storage: %v", err)
	}
	return NewImageHandler(imagehost.NewLookupService(repository), store, transformer, &testImageAccessRecorder{}), store
}

type testImageAccessRecorder struct {
	imageID     string
	originalURL string
	ip          string
	calls       int
	accepted    bool
}

func (recorder *testImageAccessRecorder) Record(imageID, originalURL, ip string) bool {
	recorder.imageID, recorder.originalURL, recorder.ip = imageID, originalURL, ip
	recorder.calls++
	return recorder.accepted
}
