package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"kaven.xyz/kaven/kaven-media-server/internal/config"
)

const DefaultRememberDurationDays = 30

type AdminSettingsRepository struct {
	db *sql.DB
}

func NewAdminSettingsRepository(db *sql.DB) *AdminSettingsRepository {
	return &AdminSettingsRepository{db: db}
}

func (repository *AdminSettingsRepository) RememberDurationDays(ctx context.Context) (int, error) {
	var days int
	if err := repository.db.QueryRowContext(ctx,
		"SELECT remember_duration_days FROM admin_settings WHERE id = 1",
	).Scan(&days); err != nil {
		return 0, fmt.Errorf("read administrator session settings: %w", err)
	}
	return days, nil
}

func (repository *AdminSettingsRepository) SetRememberDurationDays(ctx context.Context, days int) error {
	result, err := repository.db.ExecContext(ctx,
		"UPDATE admin_settings SET remember_duration_days = ? WHERE id = 1", days,
	)
	if err != nil {
		return fmt.Errorf("update administrator session settings: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect administrator session settings update: %w", err)
	}
	if updated != 1 {
		return errors.New("administrator session settings are unavailable")
	}
	return nil
}

func (repository *AdminSettingsRepository) Get(ctx context.Context) (config.RuntimeSettings, error) {
	var settings config.RuntimeSettings
	var domainsJSON, rootsJSON string
	err := repository.db.QueryRowContext(ctx, `
		SELECT public_uploads, max_file_count, max_image_file_size, max_hfs_file_size,
		       remember_duration_days, bing_sync_enabled, bing_sync_interval_hours,
		       allowed_domain_names, hfs_roots, upload_directory, download_directory
		FROM admin_settings WHERE id = 1
	`).Scan(&settings.PublicUploads, &settings.MaxFileCount, &settings.MaxImageFileSize,
		&settings.MaxHFSFileSize, &settings.RememberDurationDays, &settings.BingSyncEnabled,
		&settings.BingSyncIntervalHours, &domainsJSON, &rootsJSON,
		&settings.UploadDirectory, &settings.DownloadDirectory)
	if err != nil {
		return config.RuntimeSettings{}, fmt.Errorf("read application settings: %w", err)
	}
	if err := json.Unmarshal([]byte(domainsJSON), &settings.AllowedDomainNames); err != nil {
		return config.RuntimeSettings{}, fmt.Errorf("decode allowed domains: %w", err)
	}
	if err := json.Unmarshal([]byte(rootsJSON), &settings.HFSRoots); err != nil {
		return config.RuntimeSettings{}, fmt.Errorf("decode HFS roots: %w", err)
	}
	return settings, nil
}

func (repository *AdminSettingsRepository) Update(ctx context.Context, settings config.RuntimeSettings) error {
	domainsJSON, err := json.Marshal(settings.AllowedDomainNames)
	if err != nil {
		return fmt.Errorf("encode allowed domains: %w", err)
	}
	rootsJSON, err := json.Marshal(settings.HFSRoots)
	if err != nil {
		return fmt.Errorf("encode HFS roots: %w", err)
	}
	result, err := repository.db.ExecContext(ctx, `
		UPDATE admin_settings SET public_uploads = ?, max_file_count = ?, max_image_file_size = ?,
		max_hfs_file_size = ?, remember_duration_days = ?, bing_sync_enabled = ?,
		bing_sync_interval_hours = ?, allowed_domain_names = ?, hfs_roots = ?,
		upload_directory = ?, download_directory = ? WHERE id = 1
	`, settings.PublicUploads, settings.MaxFileCount, settings.MaxImageFileSize, settings.MaxHFSFileSize,
		settings.RememberDurationDays, settings.BingSyncEnabled, settings.BingSyncIntervalHours,
		string(domainsJSON), string(rootsJSON), settings.UploadDirectory, settings.DownloadDirectory)
	if err != nil {
		return fmt.Errorf("update application settings: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect application settings update: %w", err)
	}
	if updated != 1 {
		return errors.New("application settings are unavailable")
	}
	return nil
}
