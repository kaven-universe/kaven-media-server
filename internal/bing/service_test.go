package bing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"kaven.xyz/kaven/kaven-media-server/internal/database"
	"kaven.xyz/kaven/kaven-media-server/internal/repository"
	"kaven.xyz/kaven/kaven-media-server/internal/storage"
)

var testPNG = []byte("\x89PNG\r\n\x1a\ncontents")

func TestServiceDownloadsPersistsAndReusesBingImage(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	db, err := database.Open(ctx, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store, err := storage.New(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ConfigureMediaDirectories("media/uploads", "archive"); err != nil {
		t.Fatal(err)
	}
	var downloads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		downloads.Add(1)
		if request.URL.Path != "/th" || request.URL.Query().Get("id") != "OHR.Example" ||
			request.URL.Query().Has("w") || request.URL.Query().Has("h") || request.URL.Query().Get("keep") != "yes" {
			t.Errorf("download URL = %s", request.URL.String())
		}
		_, _ = writer.Write(testPNG)
	}))
	defer server.Close()
	metadata := &stubMetadataClient{images: []Image{{
		StartDate: "20260904", FullStartDate: "202609040700", EndDate: "20260905",
		URL: "/th?id=OHR.Example&w=1920&h=1080&keep=yes", URLBase: "/th?id=OHR.Example",
		Copyright: "Example", Hash: "hash", WP: true, Dark: 1, Top: 2, Bottom: 3,
		Hotspots: []json.RawMessage{json.RawMessage(`{"desc":"one"}`), json.RawMessage(`"plain"`)},
	}}}
	images := repository.NewBingImageRepository(db)
	service := newArchiveService(t, metadata, images, store, server.Client(), server.URL)
	service.now = func() time.Time {
		return time.Date(2026, time.September, 4, 1, 2, 3, 0, time.FixedZone("test", 8*60*60))
	}
	service.random = bytes.NewReader(make([]byte, 32))

	report, err := service.Sync(ctx)
	if err != nil || report != (SyncReport{Fetched: 1, Synchronized: 1, Downloaded: 1}) {
		t.Fatalf("report = %#v, error = %v", report, err)
	}
	if metadata.index != 0 || metadata.count != MaxArchiveImages || downloads.Load() != 1 {
		t.Fatalf("metadata window = %d,%d; downloads = %d", metadata.index, metadata.count, downloads.Load())
	}
	record, err := images.GetByURL(ctx, metadata.images[0].URL)
	if err != nil {
		t.Fatal(err)
	}
	expectedFile := filepath.ToSlash(filepath.Join("2026", "Example_hash.png"))
	if record.ID != "00000000000040008000000000000000" || record.File == nil || *record.File != expectedFile ||
		record.CreatedAt.Location() != time.UTC || !reflect.DeepEqual(record.Hotspots, []string{`{"desc":"one"}`, "plain"}) {
		t.Fatalf("record = %#v", record)
	}
	contents, err := os.ReadFile(filepath.Join(store.Root(), "archive", "bing", filepath.FromSlash(expectedFile)))
	if err != nil || !bytes.Equal(contents, testPNG) {
		t.Fatalf("stored image = %q, error = %v", contents, err)
	}

	metadata.images[0].Copyright = "Updated"
	service.now = func() time.Time { return time.Date(2026, time.September, 5, 1, 2, 3, 0, time.UTC) }
	report, err = service.Sync(ctx)
	if err != nil || report != (SyncReport{Fetched: 1, Synchronized: 1, Reused: 1}) || downloads.Load() != 1 {
		t.Fatalf("second report = %#v, downloads = %d, error = %v", report, downloads.Load(), err)
	}
	updated, err := images.GetByURL(ctx, metadata.images[0].URL)
	if err != nil || updated.Copyright == nil || *updated.Copyright != "Updated" || !updated.CreatedAt.Equal(record.CreatedAt) || !updated.UpdatedAt.After(record.UpdatedAt) {
		t.Fatalf("updated record = %#v, error = %v", updated, err)
	}
}

func TestServiceMigratesURLDigestArchiveFilename(t *testing.T) {
	for _, collision := range []bool{false, true} {
		t.Run(fmt.Sprintf("collision=%v", collision), func(t *testing.T) {
			ctx := context.Background()
			dataDir := t.TempDir()
			db, err := database.Open(ctx, dataDir)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			store, err := storage.New(dataDir)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.MkdirAll("download/bing/2026", 0o755); err != nil {
				t.Fatal(err)
			}
			metadata := Image{
				StartDate: "20260904", URL: "/th?id=OHR.Example&w=1920&h=1080",
				Copyright: "Readable example", Hash: "c4ce126fc7443a9885bd64539221a834",
			}
			opaqueReference := "2026/" + archiveKey(metadata.URL) + ".png"
			opaqueStoragePath := path.Join(store.BingArchiveDirectory(), opaqueReference)
			opaqueResolved, err := store.Resolve(opaqueStoragePath)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(opaqueResolved, testPNG, 0o600); err != nil {
				t.Fatal(err)
			}
			wantReference := "2026/" + archiveFilename(metadata, ".png")
			if collision {
				canonical, err := store.Resolve(path.Join(store.BingArchiveDirectory(), wantReference))
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(canonical, []byte("existing"), 0o600); err != nil {
					t.Fatal(err)
				}
				wantReference = "2026/" + archiveCollisionFilename(metadata, ".png")
			}
			images := repository.NewBingImageRepository(db)
			timestamp := time.Date(2026, time.September, 4, 0, 0, 0, 0, time.UTC)
			file := opaqueReference
			hash := metadata.Hash
			if err := images.Upsert(ctx, repository.BingImage{
				ID: "existing", URL: metadata.URL, Hash: &hash, File: &file,
				CreatedAt: timestamp, UpdatedAt: timestamp,
			}); err != nil {
				t.Fatal(err)
			}
			var requests atomic.Int32
			service := newArchiveService(t, &stubMetadataClient{images: []Image{metadata}}, images, store, &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				requests.Add(1)
				return nil, errors.New("download should not run")
			})}, "https://example.test")
			report, err := service.Sync(ctx)
			if err != nil || report.Reused != 1 || requests.Load() != 0 {
				t.Fatalf("report = %#v, requests = %d, error = %v", report, requests.Load(), err)
			}
			record, err := images.GetByURL(ctx, metadata.URL)
			if err != nil || record.File == nil || *record.File != wantReference {
				t.Fatalf("record = %#v, error = %v, want reference %q", record, err, wantReference)
			}
			if _, err := os.Stat(opaqueResolved); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("opaque archive file still exists: %v", err)
			}
			readable, err := store.ResolveStoredFileReference(wantReference, "bing_image")
			if err != nil {
				t.Fatal(err)
			}
			if contents, err := os.ReadFile(readable); err != nil || !bytes.Equal(contents, testPNG) {
				t.Fatalf("renamed contents = %q, %v", contents, err)
			}
		})
	}
}

func TestServiceRollsBackReadableRenameWhenDatabaseUpdateFails(t *testing.T) {
	store, err := storage.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MkdirAll("download/bing/2026", 0o755); err != nil {
		t.Fatal(err)
	}
	metadata := Image{
		StartDate: "20260904", URL: "/th?id=OHR.Rollback",
		Copyright: "Rollback example", Hash: "a18e0e490a887155b8a95dc0ad42f6e6",
	}
	opaqueReference := "2026/" + archiveKey(metadata.URL) + ".png"
	opaqueStoragePath := path.Join(store.BingArchiveDirectory(), opaqueReference)
	opaqueResolved, err := store.Resolve(opaqueStoragePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(opaqueResolved, testPNG, 0o600); err != nil {
		t.Fatal(err)
	}
	file := opaqueReference
	repository := &failingUpdateBingRepository{existing: repository.BingImage{
		ID: "existing", URL: metadata.URL, File: &file,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}}
	service := newArchiveService(t, &stubMetadataClient{images: []Image{metadata}}, repository, store, http.DefaultClient, "https://example.test")
	report, err := service.Sync(context.Background())
	if err == nil || report.Failed != 1 {
		t.Fatalf("report = %#v, error = %v", report, err)
	}
	if contents, err := os.ReadFile(opaqueResolved); err != nil || !bytes.Equal(contents, testPNG) {
		t.Fatalf("rolled-back source = %q, %v", contents, err)
	}
	readableStoragePath := path.Join(store.BingArchiveDirectory(), "2026", archiveFilename(metadata, ".png"))
	if _, err := store.ResolveExisting(readableStoragePath); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("readable destination remained after rollback: %v", err)
	}
}

func TestServiceAdoptsValidOrphanWithoutDownloading(t *testing.T) {
	store, err := storage.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	metadata := &stubMetadataClient{images: []Image{{StartDate: "20260904", URL: "/image?id=orphan"}}}
	relative := filepath.ToSlash(filepath.Join("2026", "_"+archiveKey(metadata.images[0].URL)+".png"))
	storageRelative := filepath.ToSlash(filepath.Join("download", "bing", relative))
	if err := store.MkdirAll("download/bing/2026", 0o755); err != nil {
		t.Fatal(err)
	}
	resolved, err := store.Resolve(storageRelative)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(resolved, testPNG, 0o600); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	repository := &recordingBingRepository{}
	service := newArchiveService(t, metadata, repository, store, &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		requests.Add(1)
		return nil, errors.New("download should not run")
	})}, "https://example.test")
	service.random = bytes.NewReader(make([]byte, 16))
	report, err := service.Sync(context.Background())
	if err != nil || report != (SyncReport{Fetched: 1, Synchronized: 1, Reused: 1}) || requests.Load() != 0 {
		t.Fatalf("report = %#v, requests = %d, error = %v", report, requests.Load(), err)
	}
	if len(repository.records) != 1 || repository.records[0].File == nil || *repository.records[0].File != relative {
		t.Fatalf("records = %#v", repository.records)
	}
}

func TestArchiveFilenamePreservesReadableLegacyFormat(t *testing.T) {
	tests := []struct {
		name      string
		metadata  Image
		extension string
		want      string
	}{
		{
			name:      "readable copyright and hash",
			metadata:  Image{Copyright: "地肤田，中国 (© lingqi xie/Getty Images)", Hash: "c4ce126fc7443a9885bd64539221a834"},
			extension: ".jpg",
			want:      "地肤田，中国 (© lingqi xieGetty Images)_c4ce126fc7443a9885bd64539221a834.jpg",
		},
		{
			name:      "portable invalid characters removed",
			metadata:  Image{Copyright: "a<b>:c/d\\e|f?g*h\x00", Hash: "hash"},
			extension: ".png",
			want:      "abcdefgh_hash.png",
		},
		{
			name:      "missing hash falls back to URL digest",
			metadata:  Image{URL: "/th?id=example"},
			extension: ".webp",
			want:      "_" + archiveKey("/th?id=example") + ".webp",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := archiveFilename(test.metadata, test.extension); got != test.want {
				t.Fatalf("archive filename = %q, want %q", got, test.want)
			}
		})
	}
}

func TestArchiveFilenameLimitsUTF8ComponentLength(t *testing.T) {
	filename := archiveFilename(Image{Copyright: strings.Repeat("图", 100), Hash: strings.Repeat("a", 32)}, ".jpg")
	if len(filename) > maxArchiveFilenameBytes || !utf8.ValidString(filename) {
		t.Fatalf("archive filename has %d bytes and valid UTF-8 = %v", len(filename), utf8.ValidString(filename))
	}
	if !strings.HasSuffix(filename, "_"+strings.Repeat("a", 32)+".jpg") {
		t.Fatalf("archive filename lost stable suffix: %q", filename)
	}
}

func TestServiceRepairsRecentLegacyBingRecordWithoutChangingFile(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	db, err := database.Open(ctx, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store, err := storage.New(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MkdirAll("download/bing/2026", 0o755); err != nil {
		t.Fatal(err)
	}
	const hash = "a18e0e490a887155b8a95dc0ad42f6e6"
	relative := "2026/legacy_" + hash + ".png"
	storagePath := "download/bing/" + relative
	resolved, err := store.Resolve(storagePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(resolved, testPNG, 0o600); err != nil {
		t.Fatal(err)
	}
	metadata := &stubMetadataClient{images: []Image{{StartDate: "20260907", URL: "/image?id=repair", Hash: hash}}}
	images := repository.NewBingImageRepository(db)
	service := newArchiveService(t, metadata, images, store, http.DefaultClient, "https://example.test")
	service.random = bytes.NewReader(make([]byte, 16))

	report, err := service.RepairOrphanRecords(ctx, []string{storagePath, "upload/unrelated.png"})
	if err != nil || report.Candidates != 1 || report.Repaired != 1 || report.Failed != 0 {
		t.Fatalf("report = %#v, error = %v", report, err)
	}
	record, err := images.GetByHash(ctx, hash)
	if err != nil || record.File == nil || *record.File != relative {
		t.Fatalf("record = %#v, error = %v", record, err)
	}
	if contents, err := os.ReadFile(resolved); err != nil || !bytes.Equal(contents, testPNG) {
		t.Fatalf("orphan file changed: %q, %v", contents, err)
	}

	metadata.err = errors.New("metadata should not be needed for an existing hash")
	report, err = service.RepairOrphanRecords(ctx, []string{storagePath})
	if err != nil || report.AlreadyRecorded != 1 || report.Repaired != 0 {
		t.Fatalf("second report = %#v, error = %v", report, err)
	}

	duplicateRelative := "2026/duplicate_" + hash + ".png"
	duplicateStoragePath := "download/bing/" + duplicateRelative
	duplicateResolved, err := store.Resolve(duplicateStoragePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(duplicateResolved, testPNG, 0o600); err != nil {
		t.Fatal(err)
	}
	report, err = service.RepairOrphanRecords(ctx, []string{duplicateStoragePath})
	if err != nil || report.Duplicates != 1 || report.AlreadyRecorded != 0 || report.Repaired != 0 {
		t.Fatalf("duplicate report = %#v, error = %v", report, err)
	}
	if len(report.DuplicateDetails) != 1 || report.DuplicateDetails[0].Path != duplicateStoragePath || report.DuplicateDetails[0].ExistingReference != storagePath {
		t.Fatalf("duplicate details = %#v", report.DuplicateDetails)
	}
}

func TestServiceRejectsUnsafeFailedAndUnboundedDownloads(t *testing.T) {
	tests := []struct {
		name        string
		imageURL    string
		status      int
		body        []byte
		maxBytes    int64
		wantRequest bool
	}{
		{name: "cross origin", imageURL: "https://other.test/image.jpg"},
		{name: "HTTP error", imageURL: "/image", status: http.StatusServiceUnavailable, wantRequest: true},
		{name: "invalid image", imageURL: "/image", status: http.StatusOK, body: []byte("not an image"), wantRequest: true},
		{name: "too large", imageURL: "/image", status: http.StatusOK, body: testPNG, maxBytes: 8, wantRequest: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, err := storage.New(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				writer.WriteHeader(test.status)
				_, _ = writer.Write(test.body)
			}))
			defer server.Close()
			repository := &recordingBingRepository{}
			metadata := &stubMetadataClient{images: []Image{{StartDate: "20260904", URL: test.imageURL}}}
			service := newArchiveService(t, metadata, repository, store, server.Client(), server.URL)
			if test.maxBytes > 0 {
				service.maxDownloadBytes = test.maxBytes
			}
			report, syncErr := service.Sync(context.Background())
			if syncErr == nil || report.Failed != 1 || report.Synchronized != 0 || len(repository.records) != 0 ||
				(requests.Load() > 0) != test.wantRequest {
				t.Fatalf("report = %#v, requests = %d, records = %#v, error = %v", report, requests.Load(), repository.records, syncErr)
			}
			temporary, err := os.ReadDir(filepath.Join(store.Root(), "tmp"))
			if err != nil || len(temporary) != 0 {
				t.Fatalf("temporary files = %#v, error = %v", temporary, err)
			}
		})
	}
}

func TestServiceRollsBackNewFileWhenDatabaseWriteFails(t *testing.T) {
	store, err := storage.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write(testPNG)
	}))
	defer server.Close()
	metadata := &stubMetadataClient{images: []Image{{StartDate: "20260904", URL: "/image"}}}
	repository := &recordingBingRepository{upsertErr: errors.New("database unavailable")}
	service := newArchiveService(t, metadata, repository, store, server.Client(), server.URL)
	service.random = bytes.NewReader(make([]byte, 16))
	report, syncErr := service.Sync(context.Background())
	if syncErr == nil || report.Failed != 1 {
		t.Fatalf("report = %#v, error = %v", report, syncErr)
	}
	yearEntries, err := os.ReadDir(filepath.Join(store.Root(), "download", "bing", "2026"))
	if err != nil || len(yearEntries) != 0 {
		t.Fatalf("Bing files = %#v, error = %v", yearEntries, err)
	}
}

func TestServiceContinuesAfterIndependentFailureAndSkipsDuplicateMetadata(t *testing.T) {
	store, err := storage.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write(testPNG)
	}))
	defer server.Close()
	good := Image{StartDate: "20260904", URL: "/good"}
	metadata := &stubMetadataClient{images: []Image{
		{StartDate: "20260904", URL: "https://other.test/blocked"}, good, good,
	}}
	repository := &recordingBingRepository{}
	service := newArchiveService(t, metadata, repository, store, server.Client(), server.URL)
	service.random = bytes.NewReader(make([]byte, 32))
	report, syncErr := service.Sync(context.Background())
	want := SyncReport{Fetched: 3, Synchronized: 1, Downloaded: 1, Failed: 1, Duplicates: 1}
	if syncErr == nil || report != want || len(repository.records) != 1 || repository.records[0].URL != good.URL {
		t.Fatalf("report = %#v, records = %#v, error = %v", report, repository.records, syncErr)
	}
}

func TestServiceSerializesConcurrentIdempotentSynchronization(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	db, err := database.Open(ctx, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store, err := storage.New(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	var downloads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		downloads.Add(1)
		_, _ = writer.Write(testPNG)
	}))
	defer server.Close()
	metadata := &stubMetadataClient{images: []Image{{StartDate: "20260904", URL: "/image"}}}
	service := newArchiveService(t, metadata, repository.NewBingImageRepository(db), store, server.Client(), server.URL)
	service.random = bytes.NewReader(make([]byte, 16))
	start := make(chan struct{})
	results := make(chan SyncReport, 2)
	errorsChannel := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			report, err := service.Sync(ctx)
			results <- report
			errorsChannel <- err
		}()
	}
	close(start)
	first, second := <-results, <-results
	firstErr, secondErr := <-errorsChannel, <-errorsChannel
	if firstErr != nil || secondErr != nil || downloads.Load() != 1 ||
		first.Downloaded+second.Downloaded != 1 || first.Reused+second.Reused != 1 {
		t.Fatalf("reports = %#v, %#v; downloads = %d; errors = %v, %v", first, second, downloads.Load(), firstErr, secondErr)
	}
}

func TestServiceDownloadTimeoutAndOptions(t *testing.T) {
	store, err := storage.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	metadata := &stubMetadataClient{images: []Image{{URL: "/image"}}}
	repository := &recordingBingRepository{}
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		<-request.Context().Done()
		return nil, request.Context().Err()
	})}
	service := newArchiveService(t, metadata, repository, store, httpClient, "https://example.test")
	service.downloadTimeout = 5 * time.Millisecond
	report, syncErr := service.Sync(context.Background())
	if !errors.Is(syncErr, context.DeadlineExceeded) || report.Failed != 1 {
		t.Fatalf("report = %#v, error = %v", report, syncErr)
	}
	for _, options := range []ServiceOptions{
		{DownloadBaseURL: "file:///image"}, {DownloadBaseURL: "https://user@example.test"},
		{DownloadTimeout: -time.Second}, {MaxDownloadBytes: -1}, {MaxDownloadBytes: MaxDownloadBytesLimit + 1},
	} {
		if _, err := NewService(metadata, repository, store, options); err == nil {
			t.Errorf("options %#v succeeded", options)
		}
	}
}

func newArchiveService(t *testing.T, metadata MetadataClient, images ImageRepository, store *storage.Store, httpClient *http.Client, base string) *Service {
	t.Helper()
	service, err := NewService(metadata, images, store, ServiceOptions{
		DownloadBaseURL: base, HTTPClient: httpClient, DownloadTimeout: time.Second,
		MaxDownloadBytes: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

type stubMetadataClient struct {
	mutex  sync.Mutex
	images []Image
	err    error
	index  int
	count  int
}

func (client *stubMetadataClient) Fetch(_ context.Context, index, count int) ([]Image, error) {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	client.index, client.count = index, count
	return append([]Image(nil), client.images...), client.err
}

type recordingBingRepository struct {
	mutex     sync.Mutex
	records   []repository.BingImage
	upsertErr error
}

type failingUpdateBingRepository struct {
	existing repository.BingImage
}

func (repo *failingUpdateBingRepository) GetByURL(context.Context, string) (repository.BingImage, error) {
	return repo.existing, nil
}

func (repo *failingUpdateBingRepository) ListWithFiles(context.Context) ([]repository.BingImage, error) {
	return []repository.BingImage{repo.existing}, nil
}

func (*failingUpdateBingRepository) GetByHash(context.Context, string) (repository.BingImage, error) {
	return repository.BingImage{}, repository.ErrNotFound
}

func (*failingUpdateBingRepository) Upsert(context.Context, repository.BingImage) error {
	return errors.New("database unavailable")
}

func (*recordingBingRepository) GetByURL(context.Context, string) (repository.BingImage, error) {
	return repository.BingImage{}, repository.ErrNotFound
}

func (*recordingBingRepository) GetByHash(context.Context, string) (repository.BingImage, error) {
	return repository.BingImage{}, repository.ErrNotFound
}

func (*recordingBingRepository) ListWithFiles(context.Context) ([]repository.BingImage, error) {
	return nil, nil
}

func (repository *recordingBingRepository) Upsert(_ context.Context, image repository.BingImage) error {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	if repository.upsertErr != nil {
		return repository.upsertErr
	}
	repository.records = append(repository.records, image)
	return nil
}
