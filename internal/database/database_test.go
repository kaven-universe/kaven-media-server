package database

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenRejectsIncompatibleHistoryWithoutChangingDatabase(t *testing.T) {
	for _, test := range []struct {
		name       string
		versions   []int
		historySQL string
		wantError  string
	}{
		{name: "newer schema", versions: []int{1, 999}},
		{name: "unknown schema without initial migration", versions: []int{999}},
		{name: "zero version", versions: []int{0}},
		{name: "negative version", versions: []int{-1}},
		{name: "duplicate history", historySQL: "CREATE TABLE schema_migrations(version INTEGER); INSERT INTO schema_migrations VALUES (1), (1)"},
		{name: "missing version column", historySQL: "CREATE TABLE schema_migrations(other TEXT)", wantError: "read migration history"},
		{name: "nonnumeric version", historySQL: "CREATE TABLE schema_migrations(version TEXT); INSERT INTO schema_migrations VALUES ('invalid')", wantError: "read migration version"},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			file := filepath.Join(directory, "kaven-media.db")
			db, err := sql.Open("sqlite", file)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { db.Close() })
			historySQL := test.historySQL
			if historySQL == "" {
				historySQL = migrationTable
			}
			if _, err := db.Exec(historySQL); err != nil {
				t.Fatal(err)
			}
			for _, version := range test.versions {
				if _, err := db.Exec("INSERT INTO schema_migrations VALUES (?, '2026-09-07T00:00:00.000Z')", version); err != nil {
					t.Fatal(err)
				}
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			opened, err := Open(context.Background(), directory)
			if opened != nil {
				opened.Close()
				t.Fatal("incompatible database opened")
			}
			wantError := test.wantError
			if wantError == "" {
				wantError = "incompatible with this executable"
			}
			if err == nil || !strings.Contains(err.Error(), wantError) {
				t.Fatalf("error = %v, want actionable schema incompatibility", err)
			}
			after, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatal("rejected open changed database bytes")
			}
			for _, suffix := range []string{"-wal", "-shm", "-journal"} {
				if _, err := os.Stat(file + suffix); !os.IsNotExist(err) {
					t.Fatalf("unexpected journal %s: %v", suffix, err)
				}
			}
		})
	}
}

func TestOpenAppliesPendingMigrationFromEmptyHistory(t *testing.T) {
	directory := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(directory, "kaven-media.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(migrationTable); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(context.Background(), directory)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	assertDatabaseShape(t, db)
	assertMigrationCount(t, db, 1)
}

func TestOpenAppliesMigrationsOnce(t *testing.T) {
	dataDir := t.TempDir()
	ctx := context.Background()

	db, err := Open(ctx, dataDir)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	assertDatabaseShape(t, db)
	assertMigrationCount(t, db, 1)
	if err := db.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}

	db, err = Open(ctx, dataDir)
	if err != nil {
		t.Fatalf("reopen database: %v", err)
	}
	defer db.Close()
	assertMigrationCount(t, db, 1)
}

func assertDatabaseShape(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, table := range []string{
		"images",
		"image_cache",
		"access_records",
		"bing_images",
		"download_records",
	} {
		var exists bool
		err := db.QueryRow("SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = ?)", table).Scan(&exists)
		if err != nil {
			t.Fatalf("check table %q: %v", table, err)
		}
		if !exists {
			t.Errorf("table %q does not exist", table)
		}
	}
}

func assertMigrationCount(t *testing.T, db *sql.DB, expected int) {
	t.Helper()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&count); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if count != expected {
		t.Fatalf("migration count = %d, want %d", count, expected)
	}
}

func TestMigrationVersion(t *testing.T) {
	tests := []struct {
		name    string
		version int
		wantErr bool
	}{
		{name: "001_initial.sql", version: 1},
		{name: "12_add-index.sql", version: 12},
		{name: "initial.sql", wantErr: true},
		{name: "000_initial.sql", wantErr: true},
		{name: "001_initial.txt", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			version, err := migrationVersion(test.name)
			if test.wantErr {
				if err == nil {
					t.Fatalf("migrationVersion(%q) succeeded, want error", test.name)
				}
				return
			}
			if err != nil {
				t.Fatalf("migrationVersion(%q): %v", test.name, err)
			}
			if version != test.version {
				t.Fatalf("migrationVersion(%q) = %d, want %d", test.name, version, test.version)
			}
		})
	}
}

func TestOpenConfiguresEveryConnection(t *testing.T) {
	db, err := Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(4)

	connections := make([]*sql.Conn, 0, 4)
	defer func() {
		for _, connection := range connections {
			connection.Close()
		}
	}()

	for i := 0; i < 4; i++ {
		connection, err := db.Conn(context.Background())
		if err != nil {
			t.Fatalf("acquire connection %d: %v", i, err)
		}
		connections = append(connections, connection)

		var foreignKeys, busyTimeout int
		if err := connection.QueryRowContext(context.Background(), "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
			t.Fatalf("read foreign_keys on connection %d: %v", i, err)
		}
		if err := connection.QueryRowContext(context.Background(), "PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
			t.Fatalf("read busy_timeout on connection %d: %v", i, err)
		}
		if foreignKeys != 1 {
			t.Errorf("connection %d foreign_keys = %d, want 1", i, foreignKeys)
		}
		if busyTimeout != 5000 {
			t.Errorf("connection %d busy_timeout = %d, want 5000", i, busyTimeout)
		}
	}
}
