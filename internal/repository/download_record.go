package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

type DownloadRecordRepository struct {
	db *sql.DB
}

func NewDownloadRecordRepository(db *sql.DB) *DownloadRecordRepository {
	return &DownloadRecordRepository{db: db}
}

func (repository *DownloadRecordRepository) Create(ctx context.Context, record DownloadRecord) error {
	_, err := repository.db.ExecContext(ctx, `
		INSERT INTO download_records (
			id, file, ip, original_url, user_agent, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		record.ID, record.File, record.IP, record.OriginalURL, record.UserAgent,
		formatTimestamp(record.CreatedAt), formatTimestamp(record.UpdatedAt),
	)
	if err != nil {
		return writeError("create download record", err)
	}
	return nil
}

func (repository *DownloadRecordRepository) GetByID(ctx context.Context, id string) (DownloadRecord, error) {
	var record DownloadRecord
	var userAgent sql.NullString
	var createdAt, updatedAt string
	err := repository.db.QueryRowContext(ctx, `
		SELECT id, file, ip, original_url, user_agent, created_at, updated_at
		FROM download_records WHERE id = ?`, id).Scan(
		&record.ID, &record.File, &record.IP, &record.OriginalURL, &userAgent, &createdAt, &updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return DownloadRecord{}, fmt.Errorf("get download record by ID: %w", ErrNotFound)
	}
	if err != nil {
		return DownloadRecord{}, fmt.Errorf("get download record by ID: %w", err)
	}
	record.UserAgent = stringPointer(userAgent)
	if record.CreatedAt, err = parseTimestamp("download record created_at", createdAt); err != nil {
		return DownloadRecord{}, err
	}
	if record.UpdatedAt, err = parseTimestamp("download record updated_at", updatedAt); err != nil {
		return DownloadRecord{}, err
	}
	return record, nil
}
