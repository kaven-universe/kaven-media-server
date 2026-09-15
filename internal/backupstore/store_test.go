package backupstore

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"kaven.xyz/kaven/kaven-media-server/internal/backup"
	"kaven.xyz/kaven/kaven-media-server/internal/database"
	"kaven.xyz/kaven/kaven-media-server/internal/storage"
)

func TestQueueApplyListAndResolve(t *testing.T) {
	dataDir := fixture(t)
	store, err := New(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Queue("snapshot-1"); err != nil {
		t.Fatal(err)
	}
	processed, err := store.ApplyPending(context.Background(), backup.DefaultMaxBytes)
	if err != nil || !processed {
		t.Fatalf("apply pending = %t, %v", processed, err)
	}
	status, err := store.List()
	if err != nil || len(status.Backups) != 1 || status.Backups[0].Name != "snapshot-1" || status.Backups[0].Files == 0 {
		t.Fatalf("backup status = %+v, %v", status, err)
	}
	resolved, err := store.Resolve("snapshot-1")
	if err != nil || resolved != filepath.Join(dataDir, DirectoryName, "snapshot-1") {
		t.Fatalf("resolve = %q, %v", resolved, err)
	}
	if err := store.Queue("snapshot-1"); err == nil {
		t.Fatal("accepted duplicate backup name")
	}
}

func TestApplyPendingRecordsFailureAndReturnsToService(t *testing.T) {
	dataDir := fixture(t)
	store, err := New(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Queue("too-small"); err != nil {
		t.Fatal(err)
	}
	processed, err := store.ApplyPending(context.Background(), 1)
	if err != nil || !processed {
		t.Fatalf("failed backup processing = %t, %v", processed, err)
	}
	status, err := store.List()
	if err != nil || status.LastError == "" || len(status.Backups) != 0 {
		t.Fatalf("failure status = %+v, %v", status, err)
	}
	processed, err = store.ApplyPending(context.Background(), backup.DefaultMaxBytes)
	if err != nil || processed {
		t.Fatalf("failed request was retried = %t, %v", processed, err)
	}
}

func TestApplyPendingClearsMalformedCrashRequest(t *testing.T) {
	store, err := New(fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store.root, requestName), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	processed, err := store.ApplyPending(context.Background(), backup.DefaultMaxBytes)
	if err != nil || !processed {
		t.Fatalf("malformed request = %t, %v", processed, err)
	}
	status, err := store.List()
	if err != nil || status.LastError == "" {
		t.Fatalf("malformed request status = %+v, %v", status, err)
	}
}

func TestNamesAndSymlinksAreRejected(t *testing.T) {
	for _, name := range []string{"", ".", "../outside", "nested/name", "trailing.", "with space"} {
		if err := ValidateName(name); err == nil {
			t.Errorf("accepted name %q", name)
		}
	}
	dataDir := fixture(t)
	store, err := New(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(store.root, "linked")); err == nil {
		if _, err := store.Resolve("linked"); err == nil {
			t.Fatal("accepted symlinked backup")
		}
	}
}

func fixture(t *testing.T) string {
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
	return dataDir
}
