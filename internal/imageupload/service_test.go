package imageupload

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"kaven.xyz/kaven/kaven-media-server/internal/database"
	"kaven.xyz/kaven/kaven-media-server/internal/repository"
	"kaven.xyz/kaven/kaven-media-server/internal/storage"
)

func TestSaveImageAndDetectDuplicate(t *testing.T) {
	ctx := context.Background()
	store, images := uploadTestDependencies(t)
	service := NewService(store, images)
	service.now = func() time.Time { return uploadTestTime() }
	service.random = bytes.NewReader(make([]byte, 16))
	contents := append([]byte("\x89PNG\r\n\x1a\n"), []byte("fixture")...)

	staged := stageUpload(t, store, contents)
	result, err := service.Save(ctx, Input{
		File: staged, OriginalName: `..\unsafe\display.png`, UploadIP: "127.0.0.1",
	})
	if err != nil {
		t.Fatalf("save image: %v", err)
	}
	if result.ID != "00000000000040008000000000000000" || result.UUID != result.ID || result.Name != "display.png" || result.Duplicate {
		t.Fatalf("result = %#v", result)
	}
	digest := sha1.Sum(contents)
	if result.SHA1 != hex.EncodeToString(digest[:]) {
		t.Fatalf("SHA-1 = %q", result.SHA1)
	}

	stored, err := images.GetBySHA1(ctx, result.SHA1)
	if err != nil {
		t.Fatalf("get stored image: %v", err)
	}
	if stored.ID != result.ID || stored.Folder != "images/2026/09" || stored.Name != result.UUID+".png" || stored.MIMEType != "image/png" || stored.OriginalName != "display.png" {
		t.Fatalf("stored image = %#v", stored)
	}
	file, err := os.ReadFile(filepath.Join(store.Root(), filepath.FromSlash(stored.Folder), stored.Name))
	if err != nil || !bytes.Equal(file, contents) {
		t.Fatalf("stored bytes = %v, error = %v", file, err)
	}

	duplicateUUID := strings.Repeat("1", 32)
	duplicate, err := service.Save(ctx, Input{
		File: stageUpload(t, store, contents), OriginalName: "duplicate.png",
		RequestedUUID: duplicateUUID, UploadIP: "127.0.0.2",
	})
	if err != nil {
		t.Fatalf("save duplicate: %v", err)
	}
	if !duplicate.Duplicate || duplicate.ID != result.ID || duplicate.UUID != duplicateUUID || duplicate.Name != "duplicate.png" || duplicate.SHA1 != result.SHA1 {
		t.Fatalf("duplicate result = %#v", duplicate)
	}
	listed, err := images.List(ctx, 100)
	if err != nil || len(listed) != 1 {
		t.Fatalf("images after duplicate = %#v, error = %v", listed, err)
	}
	assertUploadTempEmpty(t, store)
}

func TestSaveRejectsInvalidContentAndUUID(t *testing.T) {
	store, images := uploadTestDependencies(t)
	service := NewService(store, images)
	service.random = bytes.NewReader(make([]byte, 16))

	result, err := service.Save(context.Background(), Input{
		File: stageUpload(t, store, []byte("not an image")), OriginalName: "fake.jpg",
	})
	if err == nil || result.UUID == "" || result.Name != "fake.jpg" {
		t.Fatalf("invalid content result = %#v, error = %v", result, err)
	}
	if _, err := service.Save(context.Background(), Input{
		File:         stageUpload(t, store, []byte("\x89PNG\r\n\x1a\nfixture")),
		OriginalName: "image.png", RequestedUUID: "not-a-uuid",
	}); !errors.Is(err, ErrInvalidUUID) {
		t.Fatalf("invalid UUID error = %v, want ErrInvalidUUID", err)
	}
	assertUploadTempEmpty(t, store)
}

func TestSaveRollsBackPublishedFileWhenDatabaseWriteFails(t *testing.T) {
	store, _ := uploadTestDependencies(t)
	repositoryFailure := errors.New("database unavailable")
	images := &failingImageRepository{createError: repositoryFailure}
	service := NewService(store, images)
	service.now = func() time.Time { return uploadTestTime() }
	service.random = bytes.NewReader(make([]byte, 16))

	_, err := service.Save(context.Background(), Input{
		File: stageUpload(t, store, []byte("\x89PNG\r\n\x1a\nfixture")), OriginalName: "image.png",
	})
	if !errors.Is(err, repositoryFailure) {
		t.Fatalf("Save error = %v, want database failure", err)
	}
	entries, err := os.ReadDir(filepath.Join(store.Root(), "images", "2026", "09"))
	if err != nil {
		t.Fatalf("read image directory: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("rollback left %d image files", len(entries))
	}
	assertUploadTempEmpty(t, store)
}

func TestConcurrentDuplicateSavesKeepOneFileAndRecord(t *testing.T) {
	store, images := uploadTestDependencies(t)
	service := NewService(store, images)
	service.now = func() time.Time { return uploadTestTime() }
	contents := []byte("\x89PNG\r\n\x1a\nconcurrent")
	inputs := []Input{
		{File: stageUpload(t, store, contents), OriginalName: "one.png", RequestedUUID: strings.Repeat("a", 32)},
		{File: stageUpload(t, store, contents), OriginalName: "two.png", RequestedUUID: strings.Repeat("b", 32)},
	}

	start := make(chan struct{})
	results := make([]Result, len(inputs))
	errorsList := make([]error, len(inputs))
	var wait sync.WaitGroup
	for index := range inputs {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			results[index], errorsList[index] = service.Save(context.Background(), inputs[index])
		}(index)
	}
	close(start)
	wait.Wait()

	duplicates := 0
	for index, err := range errorsList {
		if err != nil {
			t.Fatalf("save %d: %v", index, err)
		}
		if results[index].Duplicate {
			duplicates++
		}
	}
	if duplicates != 1 || results[0].ID != results[1].ID {
		t.Fatalf("concurrent results = %#v", results)
	}
	listed, err := images.List(context.Background(), 100)
	if err != nil || len(listed) != 1 {
		t.Fatalf("stored images = %#v, error = %v", listed, err)
	}
	entries, err := os.ReadDir(filepath.Join(store.Root(), "images", "2026", "09"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("stored files = %#v, error = %v", entries, err)
	}
	assertUploadTempEmpty(t, store)
}

func TestImageUUIDUsesVersionFourAndVariant(t *testing.T) {
	service := &Service{random: bytes.NewReader(bytes.Repeat([]byte{0xff}, 16))}
	uuid, err := service.imageUUID("")
	if err != nil {
		t.Fatalf("generate UUID: %v", err)
	}
	if uuid != "ffffffffffff4fffbfffffffffffffff" {
		t.Fatalf("UUID = %q", uuid)
	}
	normalized, err := service.imageUUID("01234567-89AB-CDEF-0123-456789ABCDEF")
	if err != nil || normalized != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("normalized UUID = %q, error = %v", normalized, err)
	}
}

type failingImageRepository struct {
	createError error
}

func (fake *failingImageRepository) Create(context.Context, repository.Image) error {
	return fake.createError
}

func (*failingImageRepository) GetBySHA1(context.Context, string) (repository.Image, error) {
	return repository.Image{}, repository.ErrNotFound
}

func uploadTestDependencies(t *testing.T) (*storage.Store, *repository.ImageRepository) {
	t.Helper()
	dataDir := t.TempDir()
	store, err := storage.New(dataDir)
	if err != nil {
		t.Fatalf("create storage: %v", err)
	}
	db, err := database.Open(context.Background(), dataDir)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return store, repository.NewImageRepository(db)
}

func stageUpload(t *testing.T, store *storage.Store, contents []byte) *storage.StagedFile {
	t.Helper()
	staged, err := store.Stage(context.Background(), bytes.NewReader(contents), int64(len(contents)))
	if err != nil {
		t.Fatalf("stage upload: %v", err)
	}
	return staged
}

func uploadTestTime() time.Time {
	return time.Date(2026, time.September, 4, 1, 2, 3, 4_000_000, time.UTC)
}

func assertUploadTempEmpty(t *testing.T, store *storage.Store) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(store.Root(), "tmp"))
	if err != nil {
		t.Fatalf("read temp directory: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("temporary files remain: %#v", entries)
	}
}
