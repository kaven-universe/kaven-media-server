package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"kaven.xyz/kaven/kaven-media-server/internal/config"
)

var ErrAdminAlreadyInitialized = errors.New("administrator is already initialized")
var ErrAdminNotInitialized = errors.New("administrator is not initialized")

type AdminCredential struct {
	Username     string
	PasswordHash []byte
}

type AdminCredentialRepository struct {
	db *sql.DB
}

func NewAdminCredentialRepository(db *sql.DB) *AdminCredentialRepository {
	return &AdminCredentialRepository{db: db}
}

func (repository *AdminCredentialRepository) Get(ctx context.Context) (AdminCredential, bool, error) {
	var credential AdminCredential
	err := repository.db.QueryRowContext(ctx,
		"SELECT username, password_hash FROM admin_credentials WHERE id = 1",
	).Scan(&credential.Username, &credential.PasswordHash)
	if errors.Is(err, sql.ErrNoRows) {
		return AdminCredential{}, false, nil
	}
	if err != nil {
		return AdminCredential{}, false, fmt.Errorf("read administrator credential: %w", err)
	}
	return credential, true, nil
}

func (repository *AdminCredentialRepository) Create(ctx context.Context, username string, passwordHash []byte) error {
	result, err := repository.db.ExecContext(ctx, `
		INSERT INTO admin_credentials(id, username, password_hash, created_at, updated_at)
		SELECT 1, ?, ?, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
		WHERE NOT EXISTS (SELECT 1 FROM admin_credentials WHERE id = 1)
	`, username, passwordHash)
	if err != nil {
		return fmt.Errorf("create administrator credential: %w", err)
	}
	created, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect administrator credential creation: %w", err)
	}
	if created != 1 {
		return ErrAdminAlreadyInitialized
	}
	return nil
}

func (repository *AdminCredentialRepository) Initialize(ctx context.Context, username string, passwordHash []byte, settings config.RuntimeSettings) error {
	domainsJSON, err := json.Marshal(settings.AllowedDomainNames)
	if err != nil {
		return fmt.Errorf("encode allowed domains: %w", err)
	}
	rootsJSON, err := json.Marshal(settings.HFSRoots)
	if err != nil {
		return fmt.Errorf("encode HFS roots: %w", err)
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin server initialization: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		INSERT INTO admin_credentials(id, username, password_hash, created_at, updated_at)
		SELECT 1, ?, ?, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
		WHERE NOT EXISTS (SELECT 1 FROM admin_credentials WHERE id = 1)
	`, username, passwordHash)
	if err != nil {
		return fmt.Errorf("create administrator credential: %w", err)
	}
	created, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect administrator credential creation: %w", err)
	}
	if created != 1 {
		return ErrAdminAlreadyInitialized
	}
	_, err = tx.ExecContext(ctx, `
		UPDATE admin_settings SET public_uploads = ?, max_file_count = ?, max_image_file_size = ?,
		max_hfs_file_size = ?, remember_duration_days = ?, bing_sync_enabled = ?,
		bing_sync_interval_hours = ?, allowed_domain_names = ?, hfs_roots = ? WHERE id = 1
	`, settings.PublicUploads, settings.MaxFileCount, settings.MaxImageFileSize, settings.MaxHFSFileSize,
		settings.RememberDurationDays, settings.BingSyncEnabled, settings.BingSyncIntervalHours,
		string(domainsJSON), string(rootsJSON))
	if err != nil {
		return fmt.Errorf("save initial application settings: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit server initialization: %w", err)
	}
	return nil
}

func (repository *AdminCredentialRepository) UpdatePassword(ctx context.Context, passwordHash []byte) error {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin administrator password update: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		UPDATE admin_credentials
		SET password_hash = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
		WHERE id = 1
	`, passwordHash)
	if err != nil {
		return fmt.Errorf("update administrator password: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect administrator password update: %w", err)
	}
	if updated != 1 {
		return ErrAdminNotInitialized
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM admin_sessions"); err != nil {
		return fmt.Errorf("revoke administrator sessions: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit administrator password update: %w", err)
	}
	return nil
}
