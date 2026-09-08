package webui

import (
	"bytes"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandlerServesEmbeddedFiles(t *testing.T) {
	handler, err := NewHandler()
	if err != nil {
		t.Fatal(err)
	}
	root, err := fs.Sub(assets, assetRoot)
	if err != nil {
		t.Fatal(err)
	}
	err = fs.WalkDir(root, ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		t.Run(name, func(t *testing.T) {
			want, err := fs.ReadFile(root, name)
			if err != nil {
				t.Fatal(err)
			}
			target := "/" + name
			if name == "index.html" {
				target = "/"
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
			if response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), want) {
				t.Fatalf("GET %s = %d, bytes match = %v", target, response.Code, bytes.Equal(response.Body.Bytes(), want))
			}
			if response.Header().Get("Content-Type") == "" || response.Header().Get("Cache-Control") != "no-cache" || response.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Fatalf("headers = %v", response.Header())
			}
			response = httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodHead, target, nil))
			if response.Code != http.StatusOK || response.Body.Len() != 0 {
				t.Fatalf("HEAD %s = %d, bytes %d", target, response.Code, response.Body.Len())
			}
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestHandlerDoesNotExposeDirectoriesOrFallbackToHTML(t *testing.T) {
	handler, err := NewHandler()
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"/assets", "/assets/", "/missing.js", "/hfs", "/hfs/private", "/api/missing", "/images", "/../fallback/index.html", "/%2e%2e/fallback/index.html"} {
		t.Run(target, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
			if response.Code != http.StatusNotFound {
				t.Fatalf("GET %s = %d", target, response.Code)
			}
		})
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("POST / = %d", response.Code)
	}
}
