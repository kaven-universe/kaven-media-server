package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"kaven.xyz/kaven/kaven-media-server/internal/database"
	"kaven.xyz/kaven/kaven-media-server/internal/repository"
)

func TestActivityHandlerListsAccessAndDownloadRecords(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	timestamp := time.Date(2026, time.September, 16, 1, 2, 3, 0, time.UTC)
	image := repository.Image{
		ID: "image-1", UUID: "uuid-1", SHA1: "sha1-1", Folder: "2026/09", Name: "one.png",
		OriginalName: "one.png", MIMEType: "image/png", Size: 8, UploadDate: timestamp,
		UploadIP: "127.0.0.1", CreatedAt: timestamp, UpdatedAt: timestamp,
	}
	if err := repository.NewImageRepository(db).Create(ctx, image); err != nil {
		t.Fatal(err)
	}
	accesses := repository.NewAccessRecordRepository(db)
	if err := accesses.Create(ctx, repository.AccessRecord{
		ID: "access-1", ImageID: image.ID, IP: "192.0.2.1", OriginalURL: "/image/image-1",
		CreatedAt: timestamp, UpdatedAt: timestamp,
	}); err != nil {
		t.Fatal(err)
	}
	downloads := repository.NewDownloadRecordRepository(db)
	agent := "test-agent"
	if err := downloads.Create(ctx, repository.DownloadRecord{
		ID: "download-1", File: "/srv/public/file.zip", IP: "192.0.2.2",
		OriginalURL: "/hfs/public/file.zip?download", UserAgent: &agent,
		CreatedAt: timestamp.Add(time.Minute), UpdatedAt: timestamp.Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	handler := NewActivityHandler(accesses, downloads)

	access := serveActivity(t, handler, "/api/v1/admin/activity?page=1&pageSize=10")
	if access.Kind != "access" || access.Total != 1 || len(access.Records) != 1 || access.Records[0].ImageID != image.ID {
		t.Fatalf("access response = %#v", access)
	}
	download := serveActivity(t, handler, "/api/v1/admin/activity?kind=download&page=1&pageSize=10")
	if download.Total != 1 || len(download.Records) != 1 || download.Records[0].Resource != "/srv/public/file.zip" || download.Records[0].UserAgent == nil || *download.Records[0].UserAgent != agent {
		t.Fatalf("download response = %#v", download)
	}

	for _, target := range []string{
		"/api/v1/admin/activity?kind=unknown",
		"/api/v1/admin/activity?page=0",
		"/api/v1/admin/activity?pageSize=101",
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("GET", target, nil))
		if response.Code != 400 {
			t.Fatalf("invalid request %s returned %d", target, response.Code)
		}
	}
}

func serveActivity(t *testing.T, handler *ActivityHandler, target string) ActivityResponse {
	t.Helper()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", target, nil))
	if response.Code != 200 {
		t.Fatalf("activity response = %d: %s", response.Code, response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("activity cache control = %q", response.Header().Get("Cache-Control"))
	}
	var result ActivityResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}
