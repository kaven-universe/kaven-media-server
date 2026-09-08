package imagecache

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"kaven.xyz/kaven/kaven-media-server/internal/database"
	"kaven.xyz/kaven/kaven-media-server/internal/imageproc"
	"kaven.xyz/kaven/kaven-media-server/internal/repository"
	"kaven.xyz/kaven/kaven-media-server/internal/storage"
)

var cachedPNG = append([]byte("\x89PNG\r\n\x1a\n"), []byte("derived")...)

func TestServiceCreatesAndReusesCanonicalCache(t *testing.T) {
	service, caches, image, transformer, store := cacheTestDependencies(t)
	options := imageproc.Options{Width: 80, Height: 60, Quality: 75}
	first, err := service.Transform(context.Background(), "source", image, options)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Transform(context.Background(), "source", image, options)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Bytes, cachedPNG) || !bytes.Equal(second.Bytes, cachedPNG) || second.MIMEType != "image/png" {
		t.Fatalf("cached results = %#v, %#v", first, second)
	}
	if transformer.callCount() != 1 {
		t.Fatalf("transform calls = %d, want 1", transformer.callCount())
	}
	key := CanonicalKey(image, options)
	if key != "/image/abcdef0123456789abcdef01?height=60&quality=75&width=80" {
		t.Fatalf("canonical key = %q", key)
	}
	cache, err := caches.GetByOriginalURL(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	cachePath, err := store.ResolveExisting(filepath.ToSlash(filepath.Join(cache.Folder, cache.Name)))
	if err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(cachePath); err != nil || !bytes.Equal(data, cachedPNG) {
		t.Fatalf("cache bytes = %q, error = %v", data, err)
	}
}

func TestServiceCoalescesConcurrentMisses(t *testing.T) {
	service, _, image, transformer, _ := cacheTestDependencies(t)
	transformer.started = make(chan struct{})
	transformer.release = make(chan struct{})

	const count = 20
	results := make([]imageproc.Result, count)
	errorsList := make([]error, count)
	var wait sync.WaitGroup
	for index := range count {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			results[index], errorsList[index] = service.Transform(
				context.Background(), "source", image, imageproc.Options{Width: 100},
			)
		}(index)
	}
	<-transformer.started
	close(transformer.release)
	wait.Wait()
	for index, err := range errorsList {
		if err != nil || !bytes.Equal(results[index].Bytes, cachedPNG) {
			t.Fatalf("result %d = %#v, error = %v", index, results[index], err)
		}
	}
	if transformer.callCount() != 1 {
		t.Fatalf("transform calls = %d, want 1", transformer.callCount())
	}
}

func TestServiceRepairsMissingCacheFile(t *testing.T) {
	service, caches, image, transformer, store := cacheTestDependencies(t)
	options := imageproc.Options{Quality: 80}
	if _, err := service.Transform(context.Background(), "source", image, options); err != nil {
		t.Fatal(err)
	}
	cache, err := caches.GetByOriginalURL(context.Background(), CanonicalKey(image, options))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RemoveFile(filepath.ToSlash(filepath.Join(cache.Folder, cache.Name))); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Transform(context.Background(), "source", image, options); err != nil {
		t.Fatal(err)
	}
	if transformer.callCount() != 2 {
		t.Fatalf("transform calls = %d, want 2", transformer.callCount())
	}
}

func TestServiceRepairsInvalidCacheFile(t *testing.T) {
	service, caches, image, transformer, store := cacheTestDependencies(t)
	options := imageproc.Options{Height: 80}
	if _, err := service.Transform(context.Background(), "source", image, options); err != nil {
		t.Fatal(err)
	}
	cache, err := caches.GetByOriginalURL(context.Background(), CanonicalKey(image, options))
	if err != nil {
		t.Fatal(err)
	}
	filePath, err := store.ResolveExisting(filepath.ToSlash(filepath.Join(cache.Folder, cache.Name)))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filePath, []byte("not an image"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := service.Transform(context.Background(), "source", image, options)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(result.Bytes, cachedPNG) || transformer.callCount() != 2 {
		t.Fatalf("repaired result = %#v, transform calls = %d", result, transformer.callCount())
	}
}

func TestServiceRollsBackFileWhenRecordCreationFails(t *testing.T) {
	store, err := storage.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repositoryFailure := errors.New("database unavailable")
	service, err := NewService(store, &failingCacheRepository{err: repositoryFailure}, &fakeTransformer{result: imageproc.Result{Bytes: cachedPNG}}, 1024)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Transform(context.Background(), "source", repository.Image{ID: "image"}, imageproc.Options{Width: 10})
	if !errors.Is(err, repositoryFailure) {
		t.Fatalf("error = %v, want repository failure", err)
	}
	entries, err := os.ReadDir(filepath.Join(store.Root(), "cache"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		children, readErr := os.ReadDir(filepath.Join(store.Root(), "cache", entry.Name()))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if len(children) != 0 {
			t.Fatalf("rollback left files in %q: %#v", entry.Name(), children)
		}
	}
}

func TestManagedRelativeRejectsCachePathEscapes(t *testing.T) {
	tests := []struct {
		cache repository.ImageCache
		want  bool
	}{
		{cache: repository.ImageCache{Folder: "cache/ab", Name: "digest.png"}, want: true},
		{cache: repository.ImageCache{Folder: `cache\ab`, Name: "digest.png"}, want: true},
		{cache: repository.ImageCache{Folder: "cache/../images", Name: "source.png"}},
		{cache: repository.ImageCache{Folder: "images", Name: "source.png"}},
		{cache: repository.ImageCache{Folder: "cache/ab", Name: "../source.png"}},
		{cache: repository.ImageCache{Folder: "cache/ab", Name: `..\source.png`}},
	}
	for _, test := range tests {
		if got := managedRelative(test.cache); got != test.want {
			t.Errorf("managedRelative(%#v) = %t, want %t", test.cache, got, test.want)
		}
	}
}

type fakeTransformer struct {
	mutex   sync.Mutex
	calls   int
	result  imageproc.Result
	started chan struct{}
	release chan struct{}
}

func (transformer *fakeTransformer) Transform(context.Context, string, imageproc.Options) (imageproc.Result, error) {
	transformer.mutex.Lock()
	transformer.calls++
	started, release := transformer.started, transformer.release
	transformer.mutex.Unlock()
	if started != nil {
		select {
		case <-started:
		default:
			close(started)
		}
	}
	if release != nil {
		<-release
	}
	return transformer.result, nil
}

func (transformer *fakeTransformer) callCount() int {
	transformer.mutex.Lock()
	defer transformer.mutex.Unlock()
	return transformer.calls
}

type failingCacheRepository struct{ err error }

func (repository *failingCacheRepository) Create(context.Context, repository.ImageCache) error {
	return repository.err
}

func (*failingCacheRepository) GetByOriginalURL(context.Context, string) (repository.ImageCache, error) {
	return repository.ImageCache{}, repository.ErrNotFound
}

func (*failingCacheRepository) DeleteByOriginalURL(context.Context, string) error {
	return repository.ErrNotFound
}

func cacheTestDependencies(t *testing.T) (*Service, *repository.ImageCacheRepository, repository.Image, *fakeTransformer, *storage.Store) {
	t.Helper()
	dataDir := t.TempDir()
	store, err := storage.New(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(context.Background(), dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	now := time.Date(2026, time.September, 4, 1, 2, 3, 0, time.UTC)
	image := repository.Image{
		ID: "abcdef0123456789abcdef01", UUID: "abcdef0123456789abcdef0123456789",
		SHA1: "abcdef0123456789abcdef0123456789abcdef01", Folder: "images", Name: "source.png",
		OriginalName: "source.png", MIMEType: "image/png", Size: 10, UploadDate: now,
		UploadIP: "127.0.0.1", CreatedAt: now, UpdatedAt: now,
	}
	if err := repository.NewImageRepository(db).Create(context.Background(), image); err != nil {
		t.Fatal(err)
	}
	caches := repository.NewImageCacheRepository(db)
	transformer := &fakeTransformer{result: imageproc.Result{Bytes: cachedPNG, MIMEType: "wrong/type", Width: 80, Height: 60}}
	service, err := NewService(store, caches, transformer, 1024)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now }
	return service, caches, image, transformer, store
}
