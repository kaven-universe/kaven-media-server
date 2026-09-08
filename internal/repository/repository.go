package repository

import (
	"errors"
	"fmt"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

var (
	ErrNotFound = errors.New("repository record not found")
	ErrConflict = errors.New("repository record conflicts with existing data")
)

const timestampLayout = "2006-01-02T15:04:05.000Z"

func formatTimestamp(value time.Time) string {
	return value.UTC().Format(timestampLayout)
}

func parseTimestamp(field, value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse %s timestamp %q: %w", field, value, err)
	}
	return parsed.UTC(), nil
}

func writeError(operation string, err error) error {
	var sqliteErr *sqlite.Error
	if errors.As(err, &sqliteErr) {
		switch sqliteErr.Code() {
		case sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY, sqlite3.SQLITE_CONSTRAINT_UNIQUE:
			return fmt.Errorf("%s: %w: %w", operation, ErrConflict, err)
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func notFoundError(operation string, err error) error {
	if errors.Is(err, ErrNotFound) {
		return fmt.Errorf("%s: %w", operation, ErrNotFound)
	}
	return fmt.Errorf("%s: %w", operation, err)
}
