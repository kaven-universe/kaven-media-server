// Package uirestore prepares private server-side backups and applies them while
// the application is stopped.
package uirestore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"kaven.xyz/kaven/kaven-media-server/internal/backup"
	"kaven.xyz/kaven/kaven-media-server/internal/config"
	"kaven.xyz/kaven/kaven-media-server/internal/database"
	"kaven.xyz/kaven/kaven-media-server/internal/datalock"
	"kaven.xyz/kaven/kaven-media-server/internal/repository"
)

const (
	markerName = ".kaven-restore.json"
	workPrefix = ".kaven-restore-"
)

var managedNames = []string{
	"kaven-media.db", "kaven-media.db-wal", "kaven-media.db-shm", "kaven-media.db-journal",
}

type state struct {
	Version int    `json:"version"`
	Work    string `json:"work"`
}

// PrepareDirectory validates a backup already present on the server and records
// a pending data swap without copying it through the HTTP request body.
func PrepareDirectory(ctx context.Context, activeDB *sql.DB, dataDir, snapshot string, maxBytes int64, roots []config.HFSRoot) (backup.Report, error) {
	if activeDB == nil {
		return backup.Report{}, errors.New("active database is required")
	}
	if maxBytes <= 0 {
		return backup.Report{}, errors.New("positive restore byte limit is required")
	}
	resolvedData, err := filepath.Abs(dataDir)
	if err != nil {
		return backup.Report{}, fmt.Errorf("resolve restore data directory: %w", err)
	}
	resolvedSnapshot, err := filepath.Abs(snapshot)
	if err != nil {
		return backup.Report{}, fmt.Errorf("resolve backup directory: %w", err)
	}
	return prepareDirectory(ctx, activeDB, resolvedData, resolvedSnapshot, maxBytes, "private backup", roots)
}

// ReadSnapshotSettings validates a private snapshot in an owned temporary
// directory and returns its database-backed application settings without
// modifying the snapshot or active database.
func ReadSnapshotSettings(ctx context.Context, dataDir, snapshot string, maxBytes int64) (config.RuntimeSettings, error) {
	if maxBytes <= 0 {
		return config.RuntimeSettings{}, errors.New("positive restore byte limit is required")
	}
	temporaryRoot := filepath.Join(dataDir, "tmp")
	work, err := os.MkdirTemp(temporaryRoot, ".backup-settings-")
	if err != nil {
		return config.RuntimeSettings{}, fmt.Errorf("create backup settings workspace: %w", err)
	}
	defer os.RemoveAll(work)
	candidate := filepath.Join(work, "data")
	if _, err := backup.Restore(ctx, snapshot, candidate, maxBytes); err != nil {
		return config.RuntimeSettings{}, fmt.Errorf("validate backup settings: %w", err)
	}
	db, err := database.Open(ctx, candidate)
	if err != nil {
		return config.RuntimeSettings{}, fmt.Errorf("open backup settings database: %w", err)
	}
	settings, readErr := repository.NewAdminSettingsRepository(db).Get(ctx)
	closeErr := db.Close()
	if readErr != nil {
		return config.RuntimeSettings{}, fmt.Errorf("read backup settings: %w", readErr)
	}
	if closeErr != nil {
		return config.RuntimeSettings{}, fmt.Errorf("close backup settings database: %w", closeErr)
	}
	return settings, nil
}

func prepareDirectory(ctx context.Context, activeDB *sql.DB, dataDir, snapshot string, maxBytes int64, description string, roots []config.HFSRoot) (backup.Report, error) {
	if _, err := os.Lstat(filepath.Join(dataDir, markerName)); !errors.Is(err, os.ErrNotExist) {
		return backup.Report{}, errors.New("another restore is already pending")
	}
	work, err := os.MkdirTemp(dataDir, workPrefix)
	if err != nil {
		return backup.Report{}, fmt.Errorf("create restore work directory: %w", err)
	}
	keepWork := false
	defer func() {
		if !keepWork {
			_ = os.RemoveAll(work)
		}
	}()
	candidate := filepath.Join(work, "candidate")
	report, err := backup.Restore(ctx, snapshot, candidate, maxBytes)
	if err != nil {
		return backup.Report{}, fmt.Errorf("validate %s: %w", description, err)
	}
	if err := configureCandidate(ctx, activeDB, candidate, roots); err != nil {
		return backup.Report{}, fmt.Errorf("configure restored database: %w", err)
	}
	pending := state{Version: 1, Work: filepath.Base(work)}
	if err := writeState(dataDir, pending); err != nil {
		return backup.Report{}, err
	}
	keepWork = true
	return report, nil
}

type administratorState struct {
	credential *adminCredential
	sessions   []adminSession
}

type adminCredential struct {
	username     string
	passwordHash []byte
	createdAt    string
	updatedAt    string
}

type adminSession struct {
	tokenHash     []byte
	credentialKey []byte
	createdAt     string
	expiresAt     string
}

func configureCandidate(ctx context.Context, activeDB *sql.DB, candidateDataDir string, roots []config.HFSRoot) error {
	state, err := readAdministratorState(ctx, activeDB)
	if err != nil {
		return err
	}
	candidateDB, err := database.Open(ctx, candidateDataDir)
	if err != nil {
		return fmt.Errorf("open restored database: %w", err)
	}
	if err := writeAdministratorState(ctx, candidateDB, state); err != nil {
		_ = candidateDB.Close()
		return err
	}
	settingsRepository := repository.NewAdminSettingsRepository(candidateDB)
	settings, err := settingsRepository.Get(ctx)
	if err != nil {
		_ = candidateDB.Close()
		return fmt.Errorf("read restored application settings: %w", err)
	}
	settings.HFSRoots = roots
	if err := config.ValidateRuntimeSettings(settings); err != nil {
		_ = candidateDB.Close()
		return fmt.Errorf("validate restored application settings: %w", err)
	}
	if err := settingsRepository.Update(ctx, settings); err != nil {
		_ = candidateDB.Close()
		return fmt.Errorf("write restored directory mappings: %w", err)
	}
	if err := candidateDB.Close(); err != nil {
		return fmt.Errorf("close restored database: %w", err)
	}
	return nil
}

func readAdministratorState(ctx context.Context, db *sql.DB) (administratorState, error) {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return administratorState{}, fmt.Errorf("begin local administrator snapshot: %w", err)
	}
	defer tx.Rollback()
	var state administratorState
	var credential adminCredential
	err = tx.QueryRowContext(ctx, `
		SELECT username, password_hash, created_at, updated_at
		FROM admin_credentials WHERE id = 1
	`).Scan(&credential.username, &credential.passwordHash, &credential.createdAt, &credential.updatedAt)
	if err == nil {
		state.credential = &credential
	} else if !errors.Is(err, sql.ErrNoRows) {
		return administratorState{}, fmt.Errorf("read local administrator credential: %w", err)
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT token_hash, credential_key, created_at, expires_at
		FROM admin_sessions ORDER BY created_at, token_hash
	`)
	if err != nil {
		return administratorState{}, fmt.Errorf("read local administrator sessions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var session adminSession
		if err := rows.Scan(&session.tokenHash, &session.credentialKey, &session.createdAt, &session.expiresAt); err != nil {
			return administratorState{}, fmt.Errorf("read local administrator session: %w", err)
		}
		state.sessions = append(state.sessions, session)
	}
	if err := rows.Err(); err != nil {
		return administratorState{}, fmt.Errorf("read local administrator sessions: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return administratorState{}, fmt.Errorf("finish local administrator snapshot: %w", err)
	}
	return state, nil
}

func writeAdministratorState(ctx context.Context, db *sql.DB, state administratorState) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin restored configuration update: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM admin_sessions"); err != nil {
		return fmt.Errorf("replace restored administrator sessions: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM admin_credentials"); err != nil {
		return fmt.Errorf("replace restored administrator credential: %w", err)
	}
	if state.credential != nil {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO admin_credentials(id, username, password_hash, created_at, updated_at)
			VALUES (1, ?, ?, ?, ?)
		`, state.credential.username, state.credential.passwordHash,
			state.credential.createdAt, state.credential.updatedAt); err != nil {
			return fmt.Errorf("write restored administrator credential: %w", err)
		}
	}
	for _, session := range state.sessions {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO admin_sessions(token_hash, credential_key, created_at, expires_at)
			VALUES (?, ?, ?, ?)
		`, session.tokenHash, session.credentialKey, session.createdAt, session.expiresAt); err != nil {
			return fmt.Errorf("write restored administrator session: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit restored configuration update: %w", err)
	}
	return nil
}

// ApplyPending completes a prepared restore before the server opens its data.
func ApplyPending(dataDir string) (bool, error) {
	dataDir, err := filepath.Abs(dataDir)
	if err != nil {
		return false, fmt.Errorf("resolve restore data directory: %w", err)
	}
	pending, err := readState(dataDir)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	lock, err := datalock.Acquire(dataDir)
	if err != nil {
		return false, fmt.Errorf("lock data directory for restore: %w", err)
	}
	defer lock.Close()
	if pending.Version != 1 || !validWorkName(pending.Work) {
		return false, errors.New("invalid pending restore state")
	}
	work := filepath.Join(dataDir, pending.Work)
	rollback := filepath.Join(work, "rollback")
	candidate := filepath.Join(work, "candidate")
	if _, err := os.Lstat(work); errors.Is(err, os.ErrNotExist) {
		if err := os.Remove(filepath.Join(dataDir, markerName)); err != nil {
			return false, fmt.Errorf("remove completed restore marker: %w", err)
		}
		return true, nil
	} else if err != nil {
		return false, fmt.Errorf("inspect restore work directory: %w", err)
	}
	oldMoved := filepath.Join(work, "old-moved")
	if _, err := os.Lstat(oldMoved); errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(rollback, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return false, fmt.Errorf("create restore rollback directory: %w", err)
		}
		for _, name := range managedNames {
			if err := moveIfPresent(filepath.Join(dataDir, name), filepath.Join(rollback, name)); err != nil {
				return false, err
			}
		}
		if err := writePhase(oldMoved); err != nil {
			return false, err
		}
	} else if err != nil {
		return false, fmt.Errorf("inspect restore phase: %w", err)
	}
	newMoved := filepath.Join(work, "new-moved")
	if _, err := os.Lstat(newMoved); errors.Is(err, os.ErrNotExist) {
		for _, name := range managedNames {
			if err := moveIfPresent(filepath.Join(candidate, name), filepath.Join(dataDir, name)); err != nil {
				return false, err
			}
		}
		if err := writePhase(newMoved); err != nil {
			return false, err
		}
	} else if err != nil {
		return false, fmt.Errorf("inspect restore phase: %w", err)
	}
	if err := os.RemoveAll(work); err != nil {
		return false, fmt.Errorf("remove completed restore work directory: %w", err)
	}
	if err := os.Remove(filepath.Join(dataDir, markerName)); err != nil {
		return false, fmt.Errorf("remove completed restore marker: %w", err)
	}
	return true, nil
}

func writePhase(name string) error {
	file, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("record restore phase: %w", err)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("sync restore phase: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close restore phase: %w", err)
	}
	return nil
}

func moveIfPresent(source, destination string) error {
	_, sourceErr := os.Lstat(source)
	_, destinationErr := os.Lstat(destination)
	if errors.Is(sourceErr, os.ErrNotExist) && destinationErr == nil {
		return nil
	}
	if errors.Is(sourceErr, os.ErrNotExist) && errors.Is(destinationErr, os.ErrNotExist) {
		return nil
	}
	if sourceErr != nil {
		return fmt.Errorf("inspect restore source %q: %w", filepath.Base(source), sourceErr)
	}
	if !errors.Is(destinationErr, os.ErrNotExist) {
		return fmt.Errorf("restore destination already exists: %s", filepath.Base(destination))
	}
	if err := os.Rename(source, destination); err != nil {
		return fmt.Errorf("move restore entry %q: %w", filepath.Base(source), err)
	}
	return nil
}

func validWorkName(name string) bool {
	return filepath.Base(name) == name && strings.HasPrefix(name, workPrefix) && len(name) > len(workPrefix)
}

func readState(dataDir string) (state, error) {
	encoded, err := os.ReadFile(filepath.Join(dataDir, markerName))
	if err != nil {
		return state{}, err
	}
	var pending state
	decoder := json.NewDecoder(strings.NewReader(string(encoded)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&pending); err != nil {
		return state{}, fmt.Errorf("decode pending restore state: %w", err)
	}
	return pending, nil
}

func writeState(dataDir string, pending state) error {
	encoded, err := json.Marshal(pending)
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(dataDir, ".restore-state-")
	if err != nil {
		return fmt.Errorf("create pending restore state: %w", err)
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(encoded); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryName, filepath.Join(dataDir, markerName)); err != nil {
		return fmt.Errorf("publish pending restore state: %w", err)
	}
	return nil
}
