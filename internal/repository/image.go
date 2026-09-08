package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

type ImageRepository struct {
	db *sql.DB
}

func NewImageRepository(db *sql.DB) *ImageRepository {
	return &ImageRepository{db: db}
}

func (repository *ImageRepository) Create(ctx context.Context, image Image) error {
	_, err := repository.db.ExecContext(ctx, `
		INSERT INTO images (
			id, uuid, sha1, folder, name, original_name, mime_type, size,
			upload_date, upload_ip, created_at, updated_at
		) VALUES (?, ?, NULLIF(?, ''), ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		image.ID, image.UUID, image.SHA1, image.Folder, image.Name,
		image.OriginalName, image.MIMEType, image.Size,
		formatTimestamp(image.UploadDate), image.UploadIP,
		formatTimestamp(image.CreatedAt), formatTimestamp(image.UpdatedAt),
	)
	if err != nil {
		return writeError("create image", err)
	}
	return nil
}

func (repository *ImageRepository) GetByID(ctx context.Context, id string) (Image, error) {
	return repository.get(ctx, "id", id)
}

func (repository *ImageRepository) GetByUUID(ctx context.Context, uuid string) (Image, error) {
	return repository.get(ctx, "uuid", uuid)
}

func (repository *ImageRepository) GetBySHA1(ctx context.Context, sha1 string) (Image, error) {
	return repository.get(ctx, "sha1", sha1)
}

func (repository *ImageRepository) get(ctx context.Context, field, value string) (Image, error) {
	var query string
	switch field {
	case "id", "uuid", "sha1":
		query = imageSelect + " WHERE " + field + " = ?"
	default:
		return Image{}, fmt.Errorf("get image: unsupported lookup field %q", field)
	}
	image, err := scanImage(repository.db.QueryRowContext(ctx, query, value))
	if err != nil {
		return Image{}, notFoundError("get image by "+field, err)
	}
	return image, nil
}

func (repository *ImageRepository) List(ctx context.Context, limit int) ([]Image, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	rows, err := repository.db.QueryContext(ctx, imageSelect+" ORDER BY created_at DESC, id DESC LIMIT ?", limit)
	if err != nil {
		return nil, fmt.Errorf("list images: %w", err)
	}
	defer rows.Close()

	images := make([]Image, 0)
	for rows.Next() {
		image, err := scanImage(rows)
		if err != nil {
			return nil, fmt.Errorf("list images: %w", err)
		}
		images = append(images, image)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list images: %w", err)
	}
	return images, nil
}

func (repository *ImageRepository) Delete(ctx context.Context, id string) error {
	result, err := repository.db.ExecContext(ctx, "DELETE FROM images WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete image: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete image: count affected rows: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("delete image: %w", ErrNotFound)
	}
	return nil
}

const imageSelect = `
	SELECT id, uuid, COALESCE(sha1, ''), folder, name, original_name,
		mime_type, size, upload_date, upload_ip, created_at, updated_at
	FROM images`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanImage(scanner rowScanner) (Image, error) {
	var image Image
	var uploadDate, createdAt, updatedAt string
	if err := scanner.Scan(
		&image.ID, &image.UUID, &image.SHA1, &image.Folder, &image.Name,
		&image.OriginalName, &image.MIMEType, &image.Size, &uploadDate,
		&image.UploadIP, &createdAt, &updatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Image{}, ErrNotFound
		}
		return Image{}, fmt.Errorf("scan image: %w", err)
	}

	var err error
	if image.UploadDate, err = parseTimestamp("image upload_date", uploadDate); err != nil {
		return Image{}, err
	}
	if image.CreatedAt, err = parseTimestamp("image created_at", createdAt); err != nil {
		return Image{}, err
	}
	if image.UpdatedAt, err = parseTimestamp("image updated_at", updatedAt); err != nil {
		return Image{}, err
	}
	return image, nil
}
