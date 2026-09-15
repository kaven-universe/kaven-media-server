package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"kaven.xyz/kaven/kaven-media-server/internal/backup"
	"kaven.xyz/kaven/kaven-media-server/internal/backupstore"
	"kaven.xyz/kaven/kaven-media-server/internal/config"
	"kaven.xyz/kaven/kaven-media-server/internal/database"
	"kaven.xyz/kaven/kaven-media-server/internal/repository"
	"kaven.xyz/kaven/kaven-media-server/internal/storage"
	"kaven.xyz/kaven/kaven-media-server/internal/uirestore"
)

func TestBackupStoreHandlerListsAndQueuesPrivateBackups(t *testing.T) {
	dataDir := backupStoreFixture(t)
	store, err := backupstore.New(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	restarts := 0
	db, err := database.Open(context.Background(), dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	handler := NewBackupStoreHandler(store, db, dataDir, backup.DefaultMaxBytes, func() { restarts++ }, func([]config.HFSRoot) error { return nil })

	list := httptest.NewRecorder()
	handler.List(list, httptest.NewRequest(http.MethodGet, "/api/v1/admin/backups", nil))
	if list.Code != http.StatusOK {
		t.Fatalf("list status = %d: %s", list.Code, list.Body.String())
	}

	create := httptest.NewRecorder()
	handler.Create(create, backupStoreJSONRequest(t, "/api/v1/admin/backups", map[string]string{"name": "snapshot-1"}))
	if create.Code != http.StatusAccepted || restarts != 1 {
		t.Fatalf("create = %d, restarts %d: %s", create.Code, restarts, create.Body.String())
	}
	if processed, err := store.ApplyPending(context.Background(), backup.DefaultMaxBytes); err != nil || !processed {
		t.Fatalf("apply queued backup = %t, %v", processed, err)
	}
	status, err := store.List()
	if err != nil || len(status.Backups) != 1 || status.Backups[0].Name != "snapshot-1" {
		t.Fatalf("backups = %+v, %v", status, err)
	}
}

func TestBackupStoreHandlerRestoresWithoutMultipartUpload(t *testing.T) {
	dataDir := backupStoreFixture(t)
	store, err := backupstore.New(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	snapshotDB, err := database.Open(context.Background(), dataDir)
	if err != nil {
		t.Fatal(err)
	}
	snapshotSettings := config.DefaultRuntimeSettings(dataDir)
	snapshotSettings.UploadDirectory = "media/uploads"
	snapshotSettings.DownloadDirectory = "media/downloads"
	rootPaths := []string{
		filepath.Join(dataDir, "hfs", "uploaded"),
		filepath.Join(dataDir, "hfs", "bing"),
		filepath.Join(dataDir, "hfs", "blog"),
		filepath.Join(dataDir, "hfs", "pub"),
	}
	snapshotSettings.HFSRoots = []config.HFSRoot{
		{Name: "uploaded", Path: rootPaths[0]},
		{Name: "bing", Path: rootPaths[1]},
		{Name: "blog", Path: rootPaths[2], Public: true},
		{Name: "pub", Path: rootPaths[3], Public: true},
	}
	if err := repository.NewAdminSettingsRepository(snapshotDB).Update(context.Background(), snapshotSettings); err != nil {
		t.Fatal(err)
	}
	if err := snapshotDB.Close(); err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(dataDir, backupstore.DirectoryName, "stored")
	if _, err := backup.Create(context.Background(), dataDir, snapshot, backup.DefaultMaxBytes); err != nil {
		t.Fatal(err)
	}
	restarts := 0
	db, err := database.Open(context.Background(), dataDir)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewBackupStoreHandler(store, db, dataDir, backup.DefaultMaxBytes, func() { restarts++ }, func([]config.HFSRoot) error { return nil })
	mappingsRequest := httptest.NewRequest(http.MethodGet, "/api/v1/admin/backups/stored/mappings", nil)
	mappingsRequest.SetPathValue("name", "stored")
	mappingsResponse := httptest.NewRecorder()
	handler.Mappings(mappingsResponse, mappingsRequest)
	if mappingsResponse.Code != http.StatusOK {
		t.Fatalf("mappings = %d: %s", mappingsResponse.Code, mappingsResponse.Body.String())
	}
	var mappings BackupMappingsResponse
	if err := json.Unmarshal(mappingsResponse.Body.Bytes(), &mappings); err != nil {
		t.Fatal(err)
	}
	if mappings.UploadDirectory != "media/uploads" || mappings.DownloadDirectory != "media/downloads" ||
		len(mappings.HFSRoots) != 4 || mappings.HFSRoots[2].Name != "blog" || !mappings.HFSRoots[3].Public {
		t.Fatalf("backup mappings = %+v", mappings.HFSRoots)
	}
	for index, rootPath := range rootPaths {
		if mappings.HFSRoots[index].Path != rootPath {
			t.Fatalf("backup mapping %d path = %q", index, mappings.HFSRoots[index].Path)
		}
	}
	response := httptest.NewRecorder()
	handler.Restore(response, backupStoreJSONRequest(t, "/api/v1/admin/backups/restore", map[string]any{"name": "stored", "hfsRoots": config.DefaultHFSRoots()}))
	if response.Code != http.StatusAccepted || restarts != 1 {
		t.Fatalf("restore = %d, restarts %d: %s", response.Code, restarts, response.Body.String())
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if applied, err := uirestore.ApplyPending(dataDir); err != nil || !applied {
		t.Fatalf("apply pending restore = %t, %v", applied, err)
	}
	if _, err := os.Stat(snapshot); err != nil {
		t.Fatalf("restore removed private backup: %v", err)
	}
}

func TestBackupStoreHandlerRejectsUnsafeRequests(t *testing.T) {
	dataDir := backupStoreFixture(t)
	store, err := backupstore.New(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(context.Background(), dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	handler := NewBackupStoreHandler(store, db, dataDir, backup.DefaultMaxBytes, func() {}, func([]config.HFSRoot) error { return nil })
	for _, body := range []string{`{"name":"../escape"}`, `{"name":"safe","extra":true}`, `{"name":"safe"}{}`} {
		response := httptest.NewRecorder()
		handler.Create(response, httptest.NewRequest(http.MethodPost, "/api/v1/admin/backups", bytes.NewBufferString(body)))
		if response.Code != http.StatusBadRequest {
			t.Errorf("body %q status = %d", body, response.Code)
		}
	}
}

func backupStoreJSONRequest(t *testing.T, target string, value any) *http.Request {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, target, bytes.NewReader(encoded))
	request.Header.Set("Content-Type", "application/json")
	return request
}

func backupStoreFixture(t *testing.T) string {
	t.Helper()
	dataDir := t.TempDir()
	store, err := storage.New(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"upload", "cache", "download/bing", "hfs"} {
		if err := store.MkdirAll(name, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	db, err := database.Open(context.Background(), dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "hfs", "fixture.txt"), []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dataDir
}
