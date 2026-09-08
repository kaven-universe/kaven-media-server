package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path"
	"testing"
	"time"

	"kaven.xyz/kaven/kaven-media-server/internal/database"
	"kaven.xyz/kaven/kaven-media-server/internal/repository"
)

func TestImagesHandlerLegacyContract(t *testing.T) {
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
	var fixture []ImageMetadata
	if err := json.Unmarshal(readRelativeFixture(t, "responses/images-list.json"), &fixture); err != nil {
		t.Fatal(err)
	}
	want := fixture[0]
	// Managed storage deliberately exposes a relative path, unlike the legacy
	// installation-specific absolute path captured in the fixture.
	want.Path = path.Join(want.Folder, want.Name)
	record := repository.Image{
		ID: want.ID, UUID: want.UUID, SHA1: want.SHA1, Folder: want.Folder,
		Name: want.Name, OriginalName: want.OriginalName, MIMEType: want.MIMEType,
		Size: want.Size, UploadDate: want.UploadDate.Time, UploadIP: want.UploadIP,
		CreatedAt: want.CreatedAt.Time, UpdatedAt: want.UpdatedAt.Time,
	}
	if err := images.Create(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	response := serve("GET", "/images")
	if response.Code != 200 || response.Header().Get("Content-Type") != "application/json; charset=utf-8" || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("response = %d %v", response.Code, response.Header())
	}
	expected, err := json.Marshal([]ImageMetadata{want})
	if err != nil {
		t.Fatal(err)
	}
	assertEquivalentJSON(t, response.Body.Bytes(), expected)
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
