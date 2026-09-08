package repository

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
	"time"

	"kaven.xyz/kaven/kaven-media-server/internal/database"
)

func TestImageRepository(t *testing.T) {
	ctx := context.Background()
	db := openTestDatabase(t)
	repository := NewImageRepository(db)

	first := testImage("first", testTime())
	if err := repository.Create(ctx, first); err != nil {
		t.Fatalf("create image: %v", err)
	}

	for name, lookup := range map[string]func(context.Context, string) (Image, error){
		"ID":   repository.GetByID,
		"UUID": repository.GetByUUID,
		"SHA1": repository.GetBySHA1,
	} {
		t.Run("lookup by "+name, func(t *testing.T) {
			value := map[string]string{"ID": first.ID, "UUID": first.UUID, "SHA1": first.SHA1}[name]
			got, err := lookup(ctx, value)
			if err != nil {
				t.Fatalf("lookup image: %v", err)
			}
			if !reflect.DeepEqual(got, first) {
				t.Fatalf("image = %#v, want %#v", got, first)
			}
		})
	}

	second := testImage("second", testTime().Add(time.Second))
	if err := repository.Create(ctx, second); err != nil {
		t.Fatalf("create second image: %v", err)
	}
	images, err := repository.List(ctx, 1)
	if err != nil {
		t.Fatalf("list images: %v", err)
	}
	if len(images) != 1 || images[0].ID != second.ID {
		t.Fatalf("limited images = %#v, want second image", images)
	}
	images, err = repository.List(ctx, 0)
	if err != nil {
		t.Fatalf("list default images: %v", err)
	}
	if len(images) != 2 {
		t.Fatalf("default list length = %d, want 2", len(images))
	}

	duplicate := testImage("duplicate", testTime().Add(2*time.Second))
	duplicate.SHA1 = first.SHA1
	if err := repository.Create(ctx, duplicate); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate create error = %v, want ErrConflict", err)
	}
	if _, err := repository.GetByID(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing lookup error = %v, want ErrNotFound", err)
	}
	if err := repository.Delete(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing delete error = %v, want ErrNotFound", err)
	}

	var stored string
	if err := db.QueryRow("SELECT created_at FROM images WHERE id = ?", first.ID).Scan(&stored); err != nil {
		t.Fatalf("read stored timestamp: %v", err)
	}
	if stored != "2026-09-04T01:02:03.004Z" {
		t.Fatalf("stored timestamp = %q", stored)
	}
}

func TestImageRelationsRepositories(t *testing.T) {
	ctx := context.Background()
	db := openTestDatabase(t)
	images := NewImageRepository(db)
	caches := NewImageCacheRepository(db)
	accesses := NewAccessRecordRepository(db)

	image := testImage("related", testTime())
	if err := images.Create(ctx, image); err != nil {
		t.Fatalf("create image: %v", err)
	}
	cache := ImageCache{
		ID: "cache-1", ImageID: image.ID, OriginalURL: "/image/" + image.SHA1 + "?width=100",
		Folder: "cache", Name: "derived.svg", CreatedAt: testTime(), UpdatedAt: testTime(),
	}
	if err := caches.Create(ctx, cache); err != nil {
		t.Fatalf("create cache: %v", err)
	}
	gotCache, err := caches.GetByOriginalURL(ctx, cache.OriginalURL)
	if err != nil {
		t.Fatalf("get cache: %v", err)
	}
	if !reflect.DeepEqual(gotCache, cache) {
		t.Fatalf("cache = %#v, want %#v", gotCache, cache)
	}
	if err := caches.DeleteByOriginalURL(ctx, cache.OriginalURL); err != nil {
		t.Fatalf("delete cache: %v", err)
	}
	if _, err := caches.GetByOriginalURL(ctx, cache.OriginalURL); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cache after delete error = %v, want ErrNotFound", err)
	}
	if err := caches.Create(ctx, cache); err != nil {
		t.Fatalf("recreate cache: %v", err)
	}
	duplicateCache := cache
	duplicateCache.ID = "cache-2"
	if err := caches.Create(ctx, duplicateCache); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate cache error = %v, want ErrConflict", err)
	}

	access := AccessRecord{
		ID: "access-1", ImageID: image.ID, IP: "127.0.0.1",
		OriginalURL: "/image/" + image.SHA1, CreatedAt: testTime(), UpdatedAt: testTime(),
	}
	if err := accesses.Create(ctx, access); err != nil {
		t.Fatalf("create access record: %v", err)
	}
	count, err := accesses.CountByImageID(ctx, image.ID)
	if err != nil || count != 1 {
		t.Fatalf("access count = %d, error = %v, want 1", count, err)
	}

	badAccess := access
	badAccess.ID = "access-bad"
	badAccess.ImageID = "missing"
	if err := accesses.Create(ctx, badAccess); err == nil {
		t.Fatal("create access record with missing image succeeded")
	}

	if err := images.Delete(ctx, image.ID); err != nil {
		t.Fatalf("delete image: %v", err)
	}
	if _, err := caches.GetByOriginalURL(ctx, cache.OriginalURL); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cache after cascade error = %v, want ErrNotFound", err)
	}
	count, err = accesses.CountByImageID(ctx, image.ID)
	if err != nil || count != 0 {
		t.Fatalf("access count after cascade = %d, error = %v, want 0", count, err)
	}
}

func TestBingImageRepository(t *testing.T) {
	ctx := context.Background()
	db := openTestDatabase(t)
	repository := NewBingImageRepository(db)

	startDate, urlBase := "20260904", "/az/example"
	copyright, hash, file := "Example", "hash-1", "bing/example.jpg"
	dark, top, bottom := int64(1), int64(2), int64(3)
	image := BingImage{
		ID: "bing-1", StartDate: &startDate, URL: "https://example.test/image.jpg",
		URLBase: &urlBase, Copyright: &copyright, WP: true, Hash: &hash,
		Dark: &dark, Top: &top, Bottom: &bottom, Hotspots: []string{"one", "two"},
		File: &file, CreatedAt: testTime(), UpdatedAt: testTime(),
	}
	if err := repository.Upsert(ctx, image); err != nil {
		t.Fatalf("upsert Bing image: %v", err)
	}
	got, err := repository.GetByURL(ctx, image.URL)
	if err != nil {
		t.Fatalf("get Bing image: %v", err)
	}
	if !reflect.DeepEqual(got, image) {
		t.Fatalf("Bing image = %#v, want %#v", got, image)
	}

	updatedCopyright := "Updated"
	updated := image
	updated.ID = "ignored-on-update"
	updated.Copyright = &updatedCopyright
	updated.Hotspots = []string{}
	updated.UpdatedAt = testTime().Add(time.Second)
	if err := repository.Upsert(ctx, updated); err != nil {
		t.Fatalf("update Bing image: %v", err)
	}
	got, err = repository.GetByURL(ctx, image.URL)
	if err != nil {
		t.Fatalf("get updated Bing image: %v", err)
	}
	if got.ID != image.ID || got.CreatedAt != image.CreatedAt || got.UpdatedAt != updated.UpdatedAt || !reflect.DeepEqual(got.Copyright, updated.Copyright) {
		t.Fatalf("updated Bing image = %#v", got)
	}

	random, err := repository.RandomWithFile(ctx)
	if err != nil || random.ID != image.ID {
		t.Fatalf("random downloaded image = %#v, error = %v", random, err)
	}

	withoutFile := BingImage{
		ID: "bing-2", URL: "https://example.test/other.jpg",
		Hash: &hash, CreatedAt: testTime(), UpdatedAt: testTime(),
	}
	if err := repository.Upsert(ctx, withoutFile); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate Bing hash error = %v, want ErrConflict", err)
	}

	if _, err := repository.GetByURL(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing Bing image error = %v, want ErrNotFound", err)
	}
}

func TestDownloadRecordRepository(t *testing.T) {
	ctx := context.Background()
	db := openTestDatabase(t)
	repository := NewDownloadRecordRepository(db)
	userAgent := "fixture-agent"
	record := DownloadRecord{
		ID: "download-1", File: "hfs/hello.txt", IP: "127.0.0.1",
		OriginalURL: "/hfs/uploaded/hello.txt?download", UserAgent: &userAgent,
		CreatedAt: testTime(), UpdatedAt: testTime(),
	}
	if err := repository.Create(ctx, record); err != nil {
		t.Fatalf("create download record: %v", err)
	}
	if err := repository.Create(ctx, record); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate download error = %v, want ErrConflict", err)
	}

	var file, storedAgent, createdAt string
	if err := db.QueryRow("SELECT file, user_agent, created_at FROM download_records WHERE id = ?", record.ID).Scan(&file, &storedAgent, &createdAt); err != nil {
		t.Fatalf("read download record: %v", err)
	}
	if file != record.File || storedAgent != userAgent || createdAt != "2026-09-04T01:02:03.004Z" {
		t.Fatalf("stored download = %q, %q, %q", file, storedAgent, createdAt)
	}
}

func openTestDatabase(t *testing.T) *sql.DB {
	t.Helper()
	db, err := database.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	return db
}

func testImage(suffix string, timestamp time.Time) Image {
	return Image{
		ID: "image-" + suffix, UUID: "uuid-" + suffix, SHA1: "sha1-" + suffix,
		Folder: "images", Name: suffix + ".svg", OriginalName: suffix + ".svg",
		MIMEType: "image/svg+xml", Size: 121, UploadDate: timestamp,
		UploadIP: "127.0.0.1", CreatedAt: timestamp, UpdatedAt: timestamp,
	}
}

func testTime() time.Time {
	return time.Date(2026, time.September, 4, 1, 2, 3, 4_000_000, time.UTC)
}
