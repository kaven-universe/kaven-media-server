package database

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
)

// validateMigrationHistory allows a fresh database or an ordered prefix of
// known migrations. It must run before journal configuration or schema writes.
func validateMigrationHistory(ctx context.Context, db *sql.DB) error {
	var exists bool
	if err := db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE name = 'schema_migrations')").Scan(&exists); err != nil {
		return fmt.Errorf("find migration history: %w", err)
	}
	if !exists {
		return nil
	}
	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("read embedded migrations: %w", err)
	}
	versions := make([]int, 0, len(entries))
	for _, entry := range entries {
		version, err := migrationVersion(entry.Name())
		if err != nil {
			return err
		}
		versions = append(versions, version)
	}
	sort.Ints(versions)
	rows, err := db.QueryContext(ctx, "SELECT version FROM schema_migrations ORDER BY version")
	if err != nil {
		return fmt.Errorf("read migration history: %w", err)
	}
	defer rows.Close()
	index := 0
	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			return fmt.Errorf("read migration version: %w", err)
		}
		if index >= len(versions) || version != versions[index] {
			return fmt.Errorf("schema migration %d is incompatible with this executable; use the matching server version or restore its pre-upgrade backup", version)
		}
		index++
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("scan migration history: %w", err)
	}
	return nil
}

// ValidateSchema requires exactly this executable's migrations without
// applying changes. Backups should be restored with the matching server version
// before a separate, deliberate upgrade.
func ValidateSchema(ctx context.Context, db *sql.DB) error {
	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		return err
	}
	expected := make(map[int]bool, len(entries))
	for _, entry := range entries {
		version, err := migrationVersion(entry.Name())
		if err != nil {
			return err
		}
		expected[version] = true
	}
	rows, err := db.QueryContext(ctx, "SELECT version FROM schema_migrations")
	if err != nil {
		return fmt.Errorf("read backup schema versions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			return err
		}
		if !expected[version] {
			return fmt.Errorf("backup schema version %d does not match this executable", version)
		}
		delete(expected, version)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(expected) != 0 {
		return fmt.Errorf("backup schema is missing %d required migration(s)", len(expected))
	}
	return nil
}
