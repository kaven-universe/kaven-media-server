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

func (repository *DownloadRecordRepository) Count(ctx context.Context) (int64, error) {
	var count int64
	if err := repository.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM download_records").Scan(&count); err != nil {
		return 0, fmt.Errorf("count download records: %w", err)
	}
	return count, nil
}

func (repository *DownloadRecordRepository) List(ctx context.Context, limit, offset int) ([]DownloadRecord, error) {
	rows, err := repository.db.QueryContext(ctx, `
		SELECT id, file, ip, original_url, user_agent, created_at, updated_at
		FROM download_records ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list download records: %w", err)
	}
	defer rows.Close()
	records := make([]DownloadRecord, 0)
	for rows.Next() {
		record, err := scanDownloadRecord(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list download records: %w", err)
	}
	return records, nil
}

func (repository *DownloadRecordRepository) GetByID(ctx context.Context, id string) (DownloadRecord, error) {
	return scanDownloadRecord(repository.db.QueryRowContext(ctx, `
		SELECT id, file, ip, original_url, user_agent, created_at, updated_at
		FROM download_records WHERE id = ?`, id))
}

func scanDownloadRecord(scanner rowScanner) (DownloadRecord, error) {
	var record DownloadRecord
	var userAgent sql.NullString
	var createdAt, updatedAt string
	err := scanner.Scan(&record.ID, &record.File, &record.IP, &record.OriginalURL, &userAgent, &createdAt, &updatedAt)
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
