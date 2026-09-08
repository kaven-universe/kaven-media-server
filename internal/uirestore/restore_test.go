package uirestore

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"testing"

	"kaven.xyz/kaven/kaven-media-server/internal/backup"
	"kaven.xyz/kaven/kaven-media-server/internal/database"
	"kaven.xyz/kaven/kaven-media-server/internal/storage"
)

func TestPrepareAndApplyPending(t *testing.T) {
	dataDir := t.TempDir()
	initializeData(t, dataDir)
	original := filepath.Join(dataDir, "hfs", "original.txt")
	if err := os.WriteFile(original, []byte("from backup"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(t.TempDir(), "snapshot")
	if _, err := backup.Create(context.Background(), dataDir, snapshot, backup.DefaultMaxBytes); err != nil {
		t.Fatalf("create fixture backup: %v", err)
	}
	if err := os.WriteFile(original, []byte("current data"), 0o600); err != nil {
		t.Fatal(err)
	}

	body, contentType := multipartSnapshot(t, snapshot)
	reader := multipart.NewReader(bytes.NewReader(body), contentType)
	report, err := Prepare(context.Background(), dataDir, reader, backup.DefaultMaxBytes)
	if err != nil {
		t.Fatalf("prepare restore: %v", err)
	}
	if report.Files == 0 {
		t.Fatal("restore report contains no files")
	}
	if content, err := os.ReadFile(original); err != nil || string(content) != "current data" {
		t.Fatalf("active data changed before apply: %q, %v", content, err)
	}

	applied, err := ApplyPending(dataDir)
	if err != nil || !applied {
		t.Fatalf("apply pending = %v, %v", applied, err)
	}
	content, err := os.ReadFile(original)
	if err != nil || string(content) != "from backup" {
		t.Fatalf("restored content = %q, %v", content, err)
	}
	if _, err := os.Lstat(filepath.Join(dataDir, markerName)); !os.IsNotExist(err) {
		t.Fatalf("restore marker remains: %v", err)
	}
	db, err := database.Open(context.Background(), dataDir)
	if err != nil {
		t.Fatalf("open restored database: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestPrepareRejectsUnsafeMultipartPath(t *testing.T) {
	dataDir := t.TempDir()
	initializeData(t, dataDir)
	var encoded bytes.Buffer
	writer := multipart.NewWriter(&encoded)
	part, err := writer.CreateFormFile("../manifest.json", "manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte("{}"))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	reader := multipart.NewReader(bytes.NewReader(encoded.Bytes()), writer.Boundary())
	if _, err := Prepare(context.Background(), dataDir, reader, backup.DefaultMaxBytes); err == nil {
		t.Fatal("unsafe upload path was accepted")
	}
	if _, err := os.Lstat(filepath.Join(dataDir, markerName)); !os.IsNotExist(err) {
		t.Fatalf("restore marker created after rejection: %v", err)
	}
}

func initializeData(t *testing.T, dataDir string) {
	t.Helper()
	store, err := storage.New(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, directory := range []string{"images", "cache", "bing", "hfs"} {
		if err := store.MkdirAll(directory, 0o755); err != nil {
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
}

func multipartSnapshot(t *testing.T, snapshot string) ([]byte, string) {
	t.Helper()
	var encoded bytes.Buffer
	writer := multipart.NewWriter(&encoded)
	err := filepath.WalkDir(snapshot, func(name string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		relative, err := filepath.Rel(snapshot, name)
		if err != nil {
			return err
		}
		part, err := writer.CreateFormFile(filepath.ToSlash(relative), entry.Name())
		if err != nil {
			return err
		}
		file, err := os.Open(name)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(part, file)
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes(), writer.Boundary()
}
