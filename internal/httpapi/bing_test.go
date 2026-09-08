package httpapi

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"kaven.xyz/kaven/kaven-media-server/internal/repository"
	"kaven.xyz/kaven/kaven-media-server/internal/storage"
)

func TestBingImageHandlerRetriesMissingFileAndServesImage(t *testing.T) {
	contents := []byte("\x89PNG\r\n\x1a\narchived-bing-image")
	modified := time.Date(2026, time.September, 4, 1, 2, 3, 0, time.UTC)
	handler, store, images := newBingTestHandler(t,
		bingResult{image: bingImageWithFile("bing/missing.png")},
		bingResult{image: bingImageWithFile("bing/2026/wallpaper.png")},
	)
	if err := store.MkdirAll("bing/2026", 0o755); err != nil {
		t.Fatal(err)
	}
	filePath := filepath.Join(store.Root(), "bing", "2026", "wallpaper.png")
	if err := os.WriteFile(filePath, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filePath, modified, modified); err != nil {
		t.Fatal(err)
	}

	response := serveBingRequest(handler, http.MethodGet, nil)
	if response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), contents) {
		t.Fatalf("response = %d %q", response.Code, response.Body.Bytes())
	}
	if images.calls != 2 {
		t.Fatalf("repository calls = %d, want 2", images.calls)
	}
	wantHeaders := map[string]string{
		"Content-Type":            "image/png",
		"Content-Length":          "27",
		"Accept-Ranges":           "bytes",
		"Last-Modified":           "Fri, 04 Sep 2026 01:02:03 GMT",
		"Cache-Control":           "public, max-age=0",
		"X-Content-Type-Options":  "nosniff",
		"Content-Security-Policy": "default-src 'none'; style-src 'unsafe-inline'; sandbox",
	}
	for name, want := range wantHeaders {
		if got := response.Header().Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

func TestBingImageHandlerRejectsAbsoluteReference(t *testing.T) {
	contents := []byte("\x89PNG\r\n\x1a\nreferenced-bing-image")
	external := filepath.Join(t.TempDir(), "referenced.png")
	if err := os.WriteFile(external, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	handler, _, _ := newBingTestHandler(t, bingResult{image: bingImageWithFile(external)})
	response := serveBingRequest(handler, http.MethodGet, nil)
	if response.Code != http.StatusInternalServerError || response.Body.Len() != 0 {
		t.Fatalf("response = %d %q", response.Code, response.Body.Bytes())
	}
}

func TestBingImageHandlerSupportsHeadRangesAndConditionalRequests(t *testing.T) {
	contents := []byte("\xff\xd8\xffabcdefgh")
	handler, store, images := newBingTestHandler(t, bingResult{image: bingImageWithFile("bing/image.jpg")})
	if err := store.MkdirAll("bing", 0o755); err != nil {
		t.Fatal(err)
	}
	filePath := filepath.Join(store.Root(), "bing", "image.jpg")
	if err := os.WriteFile(filePath, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	modified := time.Date(2026, time.September, 4, 1, 2, 3, 0, time.UTC)
	if err := os.Chtimes(filePath, modified, modified); err != nil {
		t.Fatal(err)
	}

	rangeResponse := serveBingRequest(handler, http.MethodGet, map[string]string{"Range": "bytes=3-5"})
	if rangeResponse.Code != http.StatusPartialContent || rangeResponse.Body.String() != "abc" {
		t.Fatalf("range response = %d %q", rangeResponse.Code, rangeResponse.Body.String())
	}
	if got := rangeResponse.Header().Get("Content-Range"); got != "bytes 3-5/11" {
		t.Fatalf("Content-Range = %q", got)
	}

	images.calls = 0
	head := serveBingRequest(handler, http.MethodHead, nil)
	if head.Code != http.StatusOK || head.Body.Len() != 0 || head.Header().Get("Content-Length") != "11" {
		t.Fatalf("HEAD response = %d, length %q, body %q", head.Code, head.Header().Get("Content-Length"), head.Body.String())
	}

	images.calls = 0
	notModified := serveBingRequest(handler, http.MethodGet, map[string]string{
		"If-Modified-Since": "Fri, 04 Sep 2026 01:02:03 GMT",
	})
	if notModified.Code != http.StatusNotModified || notModified.Body.Len() != 0 {
		t.Fatalf("conditional response = %d %q", notModified.Code, notModified.Body.String())
	}
}

func TestBingImageHandlerReturnsNotFoundAfterTwentyMissingFiles(t *testing.T) {
	results := make([]bingResult, maxRandomBingAttempts)
	for index := range results {
		results[index].image = bingImageWithFile("bing/missing.jpg")
	}
	handler, _, images := newBingTestHandler(t, results...)
	response := serveBingRequest(handler, http.MethodGet, nil)
	if response.Code != http.StatusNotFound || response.Body.Len() != 0 {
		t.Fatalf("response = %d %q", response.Code, response.Body.String())
	}
	if images.calls != maxRandomBingAttempts {
		t.Fatalf("repository calls = %d, want %d", images.calls, maxRandomBingAttempts)
	}
}

func TestBingImageHandlerMapsRepositoryErrors(t *testing.T) {
	for _, test := range []struct {
		name   string
		err    error
		status int
	}{
		{name: "no downloaded images", err: repository.ErrNotFound, status: http.StatusNotFound},
		{name: "database failure", err: errors.New("database failure"), status: http.StatusInternalServerError},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler, _, _ := newBingTestHandler(t, bingResult{err: test.err})
			response := serveBingRequest(handler, http.MethodGet, nil)
			if response.Code != test.status || response.Body.Len() != 0 {
				t.Fatalf("response = %d %q, want %d", response.Code, response.Body.String(), test.status)
			}
		})
	}
}

func TestBingImageHandlerRejectsUnsafeAndInvalidStoredFiles(t *testing.T) {
	for _, test := range []struct {
		name     string
		file     string
		contents []byte
		status   int
	}{
		{name: "outside Bing archive", file: "images/image.png", status: http.StatusInternalServerError},
		{name: "path traversal", file: "bing/../image.png", status: http.StatusInternalServerError},
		{name: "invalid bytes", file: "bing/invalid.png", contents: []byte("not an image"), status: http.StatusUnsupportedMediaType},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler, store, _ := newBingTestHandler(t, bingResult{image: bingImageWithFile(test.file)})
			if test.contents != nil {
				if err := store.MkdirAll("bing", 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(store.Root(), "bing", "invalid.png"), test.contents, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			response := serveBingRequest(handler, http.MethodGet, nil)
			if response.Code != test.status || response.Body.Len() != 0 {
				t.Fatalf("response = %d %q, want %d", response.Code, response.Body.String(), test.status)
			}
		})
	}
}

func TestBingImageHandlerRejectsSymlink(t *testing.T) {
	handler, store, _ := newBingTestHandler(t, bingResult{image: bingImageWithFile("bing/link.jpg")})
	if err := store.MkdirAll("bing", 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(store.Root(), "target.jpg")
	if err := os.WriteFile(target, []byte("\xff\xd8\xffimage"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(store.Root(), "bing", "link.jpg")); err != nil {
		t.Skipf("create symlink: %v", err)
	}

	response := serveBingRequest(handler, http.MethodGet, nil)
	if response.Code != http.StatusInternalServerError || response.Body.Len() != 0 {
		t.Fatalf("response = %d %q, want 500", response.Code, response.Body.String())
	}
}

type bingResult struct {
	image repository.BingImage
	err   error
}

type sequenceBingRepository struct {
	results []bingResult
	calls   int
}

func (images *sequenceBingRepository) RandomWithFile(context.Context) (repository.BingImage, error) {
	index := images.calls
	images.calls++
	if index >= len(images.results) {
		return repository.BingImage{}, repository.ErrNotFound
	}
	return images.results[index].image, images.results[index].err
}

func bingImageWithFile(file string) repository.BingImage {
	return repository.BingImage{ID: "bing-image", File: &file}
}

func newBingTestHandler(t *testing.T, results ...bingResult) (*BingImageHandler, *storage.Store, *sequenceBingRepository) {
	t.Helper()
	store, err := storage.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	images := &sequenceBingRepository{results: results}
	return NewBingImageHandler(images, store), store, images
}

func serveBingRequest(handler http.Handler, method string, headers map[string]string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	mux.Handle("GET /image/bing/random", handler)
	request := httptest.NewRequest(method, "/image/bing/random", nil)
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	return response
}
