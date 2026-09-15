package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

type AccessRecordRepository struct {
	db *sql.DB
}

func NewAccessRecordRepository(db *sql.DB) *AccessRecordRepository {
	return &AccessRecordRepository{db: db}
}

func (repository *AccessRecordRepository) Create(ctx context.Context, record AccessRecord) error {
	_, err := repository.db.ExecContext(ctx, `
		INSERT INTO access_records (
			id, image_id, ip, original_url, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?)`,
		record.ID, record.ImageID, record.IP, record.OriginalURL,
		formatTimestamp(record.CreatedAt), formatTimestamp(record.UpdatedAt),
	)
	if err != nil {
		return writeError("create access record", err)
	}
	return nil
}

func (repository *AccessRecordRepository) CountByImageID(ctx context.Context, imageID string) (int64, error) {
	var count int64
	if err := repository.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM access_records WHERE image_id = ?", imageID).Scan(&count); err != nil {
		return 0, fmt.Errorf("count access records by image ID: %w", err)
	}
	return count, nil
}

func (repository *AccessRecordRepository) GetByID(ctx context.Context, id string) (AccessRecord, error) {
	var record AccessRecord
	var createdAt, updatedAt string
	err := repository.db.QueryRowContext(ctx, `
		SELECT id, image_id, ip, original_url, created_at, updated_at
		FROM access_records WHERE id = ?`, id).Scan(
		&record.ID, &record.ImageID, &record.IP, &record.OriginalURL, &createdAt, &updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return AccessRecord{}, fmt.Errorf("get access record by ID: %w", ErrNotFound)
	}
	if err != nil {
		return AccessRecord{}, fmt.Errorf("get access record by ID: %w", err)
	}
	if record.CreatedAt, err = parseTimestamp("access record created_at", createdAt); err != nil {
		return AccessRecord{}, err
	}
	if record.UpdatedAt, err = parseTimestamp("access record updated_at", updatedAt); err != nil {
		return AccessRecord{}, err
	}
	return record, nil
}
