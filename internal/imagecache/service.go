package imagecache

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"kaven.xyz/kaven/kaven-media-server/internal/imageproc"
	"kaven.xyz/kaven/kaven-media-server/internal/repository"
	"kaven.xyz/kaven/kaven-media-server/internal/storage"
)

type CacheRepository interface {
	Create(context.Context, repository.ImageCache) error
	GetByOriginalURL(context.Context, string) (repository.ImageCache, error)
	DeleteByOriginalURL(context.Context, string) error
}

type Transformer interface {
	Transform(context.Context, string, imageproc.Options) (imageproc.Result, error)
}

type Service struct {
	store       *storage.Store
	caches      CacheRepository
	transformer Transformer
	maxBytes    int64
	now         func() time.Time

	mutex   sync.Mutex
	flights map[string]*flight
}

type flight struct {
	done   chan struct{}
	result imageproc.Result
	err    error
}

func NewService(store *storage.Store, caches CacheRepository, transformer Transformer, maxBytes int64) (*Service, error) {
	if store == nil || caches == nil || transformer == nil {
		return nil, errors.New("create image cache: dependencies are required")
	}
	if maxBytes < 1 {
		return nil, errors.New("create image cache: maximum size must be positive")
	}
	return &Service{
		store: store, caches: caches, transformer: transformer, maxBytes: maxBytes,
		now: time.Now, flights: make(map[string]*flight),
	}, nil
}

func (service *Service) Transform(ctx context.Context, filePath string, image repository.Image, options imageproc.Options) (imageproc.Result, error) {
	key := CanonicalKey(image, options)
	if result, found, err := service.load(ctx, key, image.ID); found || err != nil {
		return result, err
	}
	return service.do(ctx, key, func() (imageproc.Result, error) {
		if result, found, err := service.load(ctx, key, image.ID); found || err != nil {
			return result, err
		}
		return service.create(ctx, key, filePath, image, options)
	})
}

func CanonicalKey(image repository.Image, options imageproc.Options) string {
	query := make(url.Values)
	if options.Width > 0 {
		query.Set("width", strconv.Itoa(options.Width))
	}
	if options.Height > 0 {
		query.Set("height", strconv.Itoa(options.Height))
	}
	if options.Quality > 0 {
		query.Set("quality", strconv.Itoa(options.Quality))
	}
	return "/image/" + image.ID + "?" + query.Encode()
}

func (service *Service) load(ctx context.Context, key, imageID string) (imageproc.Result, bool, error) {
	cache, err := service.caches.GetByOriginalURL(ctx, key)
	if errors.Is(err, repository.ErrNotFound) {
		return imageproc.Result{}, false, nil
	}
	if err != nil {
		return imageproc.Result{}, false, fmt.Errorf("look up image cache: %w", err)
	}
	if cache.ImageID != imageID || !managedRelative(cache) {
		if err := service.invalidate(ctx, cache); err != nil {
			return imageproc.Result{}, false, err
		}
		return imageproc.Result{}, false, nil
	}

	result, err := service.read(cache)
	if err == nil {
		return result, true, nil
	}
	if errors.Is(err, storage.ErrNotFound) || errors.Is(err, storage.ErrTooLarge) || errors.Is(err, imageproc.ErrUnsupportedFormat) {
		if invalidateErr := service.invalidate(ctx, cache); invalidateErr != nil {
			return imageproc.Result{}, false, errors.Join(err, invalidateErr)
		}
		return imageproc.Result{}, false, nil
	}
	return imageproc.Result{}, false, err
}

func (service *Service) read(cache repository.ImageCache) (imageproc.Result, error) {
	filePath, err := service.store.ResolveExisting(path.Join(cache.Folder, cache.Name))
	if err != nil {
		return imageproc.Result{}, err
	}
	file, err := os.Open(filePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return imageproc.Result{}, storage.ErrNotFound
		}
		return imageproc.Result{}, fmt.Errorf("open image cache: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return imageproc.Result{}, fmt.Errorf("inspect image cache: %w", err)
	}
	data, err := io.ReadAll(io.LimitReader(file, service.maxBytes+1))
	if err != nil {
		return imageproc.Result{}, fmt.Errorf("read image cache: %w", err)
	}
	if int64(len(data)) > service.maxBytes {
		return imageproc.Result{}, storage.ErrTooLarge
	}
	format, err := imageproc.Detect(data)
	if err != nil {
		return imageproc.Result{}, err
	}
	return imageproc.Result{Bytes: data, MIMEType: format.MIMEType}, nil
}

func (service *Service) create(ctx context.Context, key, filePath string, image repository.Image, options imageproc.Options) (imageproc.Result, error) {
	result, err := service.transformer.Transform(ctx, filePath, options)
	if err != nil {
		return imageproc.Result{}, err
	}
	if int64(len(result.Bytes)) > service.maxBytes {
		return imageproc.Result{}, storage.ErrTooLarge
	}
	format, err := imageproc.Detect(result.Bytes)
	if err != nil {
		return imageproc.Result{}, fmt.Errorf("validate transformed image: %w", err)
	}
	result.MIMEType = format.MIMEType

	digestBytes := sha256.Sum256([]byte(key))
	digest := hex.EncodeToString(digestBytes[:])
	folder := path.Join("cache", digest[:2])
	name := digest + format.Extension
	if err := service.store.MkdirAll(folder, 0o755); err != nil {
		return imageproc.Result{}, fmt.Errorf("create image cache directory: %w", err)
	}
	relative := path.Join(folder, name)
	published := false
	if _, err := service.store.WriteAtomic(ctx, relative, bytes.NewReader(result.Bytes), service.maxBytes, 0o640, nil); err != nil {
		return imageproc.Result{}, fmt.Errorf("publish image cache: %w", err)
	}
	published = true

	now := service.now().UTC()
	cache := repository.ImageCache{
		ID: digest, ImageID: image.ID, OriginalURL: key, Folder: folder, Name: name,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := service.caches.Create(ctx, cache); err != nil {
		if published {
			removeErr := service.store.RemoveFile(relative)
			if removeErr != nil && !errors.Is(removeErr, storage.ErrNotFound) {
				return imageproc.Result{}, errors.Join(fmt.Errorf("create image cache record: %w", err), fmt.Errorf("roll back image cache: %w", removeErr))
			}
		}
		return imageproc.Result{}, fmt.Errorf("create image cache record: %w", err)
	}
	return result, nil
}

func (service *Service) invalidate(ctx context.Context, cache repository.ImageCache) error {
	if err := service.caches.DeleteByOriginalURL(ctx, cache.OriginalURL); err != nil && !errors.Is(err, repository.ErrNotFound) {
		return fmt.Errorf("delete stale image cache record: %w", err)
	}
	if !managedRelative(cache) {
		return nil
	}
	err := service.store.RemoveFile(path.Join(cache.Folder, cache.Name))
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return fmt.Errorf("delete stale image cache file: %w", err)
	}
	return nil
}

func managedRelative(cache repository.ImageCache) bool {
	folder := strings.ReplaceAll(cache.Folder, `\`, "/")
	name := strings.ReplaceAll(cache.Name, `\`, "/")
	return path.Clean(folder) == folder && strings.HasPrefix(folder, "cache/") &&
		name == cache.Name && path.Base(name) == name && name != "." && name != ".."
}

func (service *Service) do(ctx context.Context, key string, operation func() (imageproc.Result, error)) (imageproc.Result, error) {
	service.mutex.Lock()
	if existing := service.flights[key]; existing != nil {
		service.mutex.Unlock()
		select {
		case <-existing.done:
			return existing.result, existing.err
		case <-ctx.Done():
			return imageproc.Result{}, ctx.Err()
		}
	}
	current := &flight{done: make(chan struct{})}
	service.flights[key] = current
	service.mutex.Unlock()

	current.result, current.err = operation()
	service.mutex.Lock()
	delete(service.flights, key)
	close(current.done)
	service.mutex.Unlock()
	return current.result, current.err
}
