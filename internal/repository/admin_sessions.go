package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type AdminSessionRepository struct {
	db          *sql.DB
	maxSessions int
}

func NewAdminSessionRepository(db *sql.DB, maxSessions int) *AdminSessionRepository {
	return &AdminSessionRepository{db: db, maxSessions: maxSessions}
}

func (repository *AdminSessionRepository) Create(ctx context.Context, tokenHash, credentialKey []byte, createdAt, expiresAt time.Time) (bool, error) {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin administrator session creation: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM admin_sessions WHERE expires_at <= ?", formatTimestamp(createdAt)); err != nil {
		return false, fmt.Errorf("remove expired administrator sessions: %w", err)
	}
	var count int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM admin_sessions").Scan(&count); err != nil {
		return false, fmt.Errorf("count administrator sessions: %w", err)
	}
	if repository.maxSessions > 0 && count >= repository.maxSessions {
		if _, err := tx.ExecContext(ctx, `DELETE FROM admin_sessions WHERE token_hash = (
			SELECT token_hash FROM admin_sessions ORDER BY created_at, token_hash LIMIT 1
		)`); err != nil {
			return false, fmt.Errorf("evict administrator session: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO admin_sessions(token_hash, credential_key, created_at, expires_at) VALUES (?, ?, ?, ?)",
		tokenHash, credentialKey, formatTimestamp(createdAt), formatTimestamp(expiresAt),
	); err != nil {
		var exists bool
		if checkErr := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM admin_sessions WHERE token_hash = ?)", tokenHash).Scan(&exists); checkErr == nil && exists {
			return false, nil
		}
		return false, fmt.Errorf("create administrator session: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit administrator session creation: %w", err)
	}
	return true, nil
}

func (repository *AdminSessionRepository) Get(ctx context.Context, tokenHash, credentialKey []byte, now time.Time) (time.Time, bool, error) {
	var rawExpires string
	err := repository.db.QueryRowContext(ctx,
		"SELECT expires_at FROM admin_sessions WHERE token_hash = ? AND credential_key = ?", tokenHash, credentialKey,
	).Scan(&rawExpires)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("read administrator session: %w", err)
	}
	expiresAt, err := parseTimestamp("administrator session expiry", rawExpires)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("parse administrator session expiry: %w", err)
	}
	if !now.Before(expiresAt) {
		if err := repository.Delete(ctx, tokenHash); err != nil {
			return time.Time{}, false, err
		}
		return time.Time{}, false, nil
	}
	return expiresAt, true, nil
}

func (repository *AdminSessionRepository) Delete(ctx context.Context, tokenHash []byte) error {
	if _, err := repository.db.ExecContext(ctx, "DELETE FROM admin_sessions WHERE token_hash = ?", tokenHash); err != nil {
		return fmt.Errorf("delete administrator session: %w", err)
	}
	return nil
}
