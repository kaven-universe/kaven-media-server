package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

type ImageCacheRepository struct {
	db *sql.DB
}

func NewImageCacheRepository(db *sql.DB) *ImageCacheRepository {
	return &ImageCacheRepository{db: db}
}

func (repository *ImageCacheRepository) Create(ctx context.Context, cache ImageCache) error {
	_, err := repository.db.ExecContext(ctx, `
		INSERT INTO image_cache (
			id, image_id, original_url, folder, name, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		cache.ID, cache.ImageID, cache.OriginalURL, cache.Folder, cache.Name,
		formatTimestamp(cache.CreatedAt), formatTimestamp(cache.UpdatedAt),
	)
	if err != nil {
		return writeError("create image cache", err)
	}
	return nil
}

func (repository *ImageCacheRepository) GetByOriginalURL(ctx context.Context, originalURL string) (ImageCache, error) {
	return repository.get(ctx, "original_url", originalURL)
}

func (repository *ImageCacheRepository) GetByID(ctx context.Context, id string) (ImageCache, error) {
	return repository.get(ctx, "id", id)
}

func (repository *ImageCacheRepository) get(ctx context.Context, field, value string) (ImageCache, error) {
	if field != "id" && field != "original_url" {
		return ImageCache{}, fmt.Errorf("get image cache: unsupported lookup field %q", field)
	}
	cache, err := scanImageCache(repository.db.QueryRowContext(ctx, imageCacheSelect+" WHERE "+field+" = ?", value))
	if err != nil {
		return ImageCache{}, notFoundError("get image cache by "+field, err)
	}
	return cache, nil
}

func (repository *ImageCacheRepository) DeleteByOriginalURL(ctx context.Context, originalURL string) error {
	result, err := repository.db.ExecContext(ctx, "DELETE FROM image_cache WHERE original_url = ?", originalURL)
	if err != nil {
		return fmt.Errorf("delete image cache by original URL: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete image cache by original URL: count affected rows: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("delete image cache by original URL: %w", ErrNotFound)
	}
	return nil
}

const imageCacheSelect = `
	SELECT id, image_id, original_url, folder, name, created_at, updated_at
	FROM image_cache`

func scanImageCache(scanner rowScanner) (ImageCache, error) {
	var cache ImageCache
	var createdAt, updatedAt string
	if err := scanner.Scan(
		&cache.ID, &cache.ImageID, &cache.OriginalURL, &cache.Folder,
		&cache.Name, &createdAt, &updatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ImageCache{}, ErrNotFound
		}
		return ImageCache{}, fmt.Errorf("scan image cache: %w", err)
	}
	var err error
	if cache.CreatedAt, err = parseTimestamp("image cache created_at", createdAt); err != nil {
		return ImageCache{}, err
	}
	if cache.UpdatedAt, err = parseTimestamp("image cache updated_at", updatedAt); err != nil {
		return ImageCache{}, err
	}
	return cache, nil
}
