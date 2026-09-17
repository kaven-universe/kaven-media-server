package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kaven.xyz/kaven/kaven-media-server/internal/backup"
	"kaven.xyz/kaven/kaven-media-server/internal/backupstore"
	"kaven.xyz/kaven/kaven-media-server/internal/bing"
	"kaven.xyz/kaven/kaven-media-server/internal/database"
	"kaven.xyz/kaven/kaven-media-server/internal/repository"
	"kaven.xyz/kaven/kaven-media-server/internal/storage"
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

func TestIntegrityRepairRequiresValidatedBackupAndPassesOrphans(t *testing.T) {
	dataDir := t.TempDir()
	store, err := storage.New(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, directory := range []string{"upload", "cache", "download/bing/2026"} {
		if err := store.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	orphan := "download/bing/2026/legacy_a18e0e490a887155b8a95dc0ad42f6e6.png"
	resolved, err := store.Resolve(orphan)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(resolved, []byte("\x89PNG\r\n\x1a\ncontents"), 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(context.Background(), dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	backups, err := backupstore.New(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(dataDir, backupstore.DirectoryName, "before-repair")
	if _, err := backup.Create(context.Background(), dataDir, snapshot, backup.DefaultMaxBytes); err != nil {
		t.Fatal(err)
	}
	db, err = database.Open(context.Background(), dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repairer := &recordingIntegrityRepairer{}
	handler := NewRepairableIntegrityHandler(db, dataDir, backups, backup.DefaultMaxBytes, repairer)

	missingBackup := httptest.NewRecorder()
	handler.Repair(missingBackup, httptest.NewRequest(http.MethodPost, "/api/v1/admin/integrity/repair", bytes.NewBufferString(`{"backupName":"missing"}`)))
	if missingBackup.Code != http.StatusBadRequest || repairer.calls != 0 {
		t.Fatalf("missing backup response = %d, calls = %d: %s", missingBackup.Code, repairer.calls, missingBackup.Body.String())
	}

	response := httptest.NewRecorder()
	handler.Repair(response, httptest.NewRequest(http.MethodPost, "/api/v1/admin/integrity/repair", bytes.NewBufferString(`{"backupName":"before-repair"}`)))
	if response.Code != http.StatusOK || repairer.calls != 1 {
		t.Fatalf("repair response = %d, calls = %d: %s", response.Code, repairer.calls, response.Body.String())
	}
	if len(repairer.candidates) != 1 || repairer.candidates[0] != orphan {
		t.Fatalf("repair candidates = %#v", repairer.candidates)
	}
	var result struct {
		BackupName string `json:"backupName"`
		Integrity  struct {
			Healthy    bool `json:"healthy"`
			IssueCount int  `json:"issueCount"`
		} `json:"integrity"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.BackupName != "before-repair" || result.Integrity.Healthy || result.Integrity.IssueCount != 1 {
		t.Fatalf("repair result = %#v", result)
	}
}

type recordingIntegrityRepairer struct {
	calls      int
	candidates []string
}

func (repairer *recordingIntegrityRepairer) RepairOrphanRecords(_ context.Context, candidates []string) (bing.RepairReport, error) {
	repairer.calls++
	repairer.candidates = append([]string(nil), candidates...)
	return bing.RepairReport{Candidates: len(candidates)}, nil
}
