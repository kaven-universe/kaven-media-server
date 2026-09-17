package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"kaven.xyz/kaven/kaven-media-server/internal/database"
	"kaven.xyz/kaven/kaven-media-server/internal/repository"
)

func TestImagesHandler(t *testing.T) {
	db, err := database.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	images := repository.NewImageRepository(db)
	handler := NewImagesHandler(images)
	serve := func(method, target string) *httptest.ResponseRecorder {
		t.Helper()
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(method, target, nil))
		return response
	}
	if response := serve("GET", "/images"); response.Code != 200 || response.Body.String() != "[]" {
		t.Fatalf("empty list = %d %q", response.Code, response.Body.String())
	}
	record := repository.Image{
		ID: "0123456789abcdef01234567", UUID: "0123456789abcdef0123456789abcdef",
		SHA1: "0123456789abcdef0123456789abcdef01234567", Folder: "2026/09",
		Name: "image.png", OriginalName: "image.png", MIMEType: "image/png", Size: 8,
		UploadDate: time.Date(2026, 9, 15, 1, 2, 3, 0, time.UTC), UploadIP: "127.0.0.1",
		CreatedAt: time.Date(2026, 9, 15, 1, 2, 3, 0, time.UTC), UpdatedAt: time.Date(2026, 9, 15, 1, 2, 3, 0, time.UTC),
	}
	if err := images.Create(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	response := serve("GET", "/images")
	if response.Code != 200 || response.Header().Get("Content-Type") != "application/json; charset=utf-8" || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("response = %d %v", response.Code, response.Header())
	}
	var listed []ImageMetadata
	if err := json.Unmarshal(response.Body.Bytes(), &listed); err != nil || len(listed) != 1 || listed[0].ID != record.ID || listed[0].Path != "2026/09/image.png" {
		t.Fatalf("listed images = %#v, %v", listed, err)
	}
	if response := serve("HEAD", "/images"); response.Code != 200 || response.Body.Len() != 0 {
		t.Fatalf("HEAD = %d %q", response.Code, response.Body.String())
	}
	for i := 0; i < 101; i++ {
		image := record
		image.ID, image.UUID, image.SHA1 = fmt.Sprintf("%024x", i), fmt.Sprintf("%032x", i), fmt.Sprintf("%040x", i)
		image.CreatedAt = record.CreatedAt.Add(time.Second)
		if err := images.Create(context.Background(), image); err != nil {
			t.Fatal(err)
		}
	}
	for _, target := range []string{"/images", "/images?limit=1000000&skip=100&sort=asc", "/images?limit=-1&limit=garbage"} {
		response := serve("GET", target)
		var got []ImageMetadata
		if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if response.Code != 200 || len(got) != 100 || got[0].ID != fmt.Sprintf("%024x", 100) || got[99].ID != fmt.Sprintf("%024x", 1) {
			t.Fatalf("bounded ordered list for %s = %d, count %d", target, response.Code, len(got))
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "/images", nil).WithContext(ctx))
	if response.Code != http.StatusInternalServerError || response.Body.Len() != 0 {
		t.Fatalf("cancelled query = %d %q", response.Code, response.Body.String())
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	response = serve("GET", "/images")
	if response.Code != http.StatusInternalServerError || response.Body.Len() != 0 {
		t.Fatalf("database failure = %d %q", response.Code, response.Body.String())
	}
}
