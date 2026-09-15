package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kaven.xyz/kaven/kaven-media-server/internal/database"
	"kaven.xyz/kaven/kaven-media-server/internal/repository"
)

func TestIntegrityHandlerReportsMissingDatabaseFile(t *testing.T) {
	dataDir := t.TempDir()
	for _, directory := range []string{"upload", "cache", "download/bing"} {
		if err := os.MkdirAll(filepath.Join(dataDir, filepath.FromSlash(directory)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	db, err := database.Open(context.Background(), dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC()
	if err := repository.NewImageRepository(db).Create(context.Background(), repository.Image{
		ID: "missing-image", UUID: "missing-uuid", SHA1: strings.Repeat("a", 40),
		Folder: "2026", Name: "missing.jpg", OriginalName: "missing.jpg",
		MIMEType: "image/jpeg", Size: 1, UploadDate: now, UploadIP: "127.0.0.1",
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	NewIntegrityHandler(db, dataDir).ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/admin/integrity/check", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	var report struct {
		Healthy           bool `json:"healthy"`
		ReferencesChecked int  `json:"referencesChecked"`
		IssueCount        int  `json:"issueCount"`
		Issues            []struct {
			Kind string `json:"kind"`
			Path string `json:"path"`
		} `json:"issues"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Healthy || report.ReferencesChecked != 1 || report.IssueCount == 0 {
		t.Fatalf("report = %+v", report)
	}
	foundMissing := false
	for _, issue := range report.Issues {
		if issue.Kind == "missing_file" && issue.Path == "2026/missing.jpg" {
			foundMissing = true
		}
	}
	if !foundMissing {
		t.Fatalf("missing file issue not found: %+v", report.Issues)
	}
}

func TestIntegrityHandlerReportsUnavailableService(t *testing.T) {
	handler := NewIntegrityHandler(nil, "")
	handler.db = nil
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/admin/integrity/check", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", response.Code)
	}
}
