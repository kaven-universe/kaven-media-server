// Package uirestore prepares browser-uploaded backups and applies them while
// the application is stopped.
package uirestore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime/multipart"
	"os"
	"path"
	"path/filepath"
	"strings"

	"kaven.xyz/kaven/kaven-media-server/internal/backup"
	"kaven.xyz/kaven/kaven-media-server/internal/datalock"
)

const (
	markerName      = ".kaven-restore.json"
	workPrefix      = ".kaven-restore-"
	maxManifestSize = 16 << 20
)

var managedNames = []string{
	"images", "cache", "bing", "hfs", "tmp",
	"kaven-media.db", "kaven-media.db-wal", "kaven-media.db-shm", "kaven-media.db-journal",
}

type state struct {
	Version int    `json:"version"`
	Work    string `json:"work"`
}

// Prepare receives files whose multipart field names are snapshot-relative
// paths. It validates the complete snapshot and records a pending data swap.
func Prepare(ctx context.Context, dataDir string, reader *multipart.Reader, maxBytes int64) (backup.Report, error) {
	if reader == nil || maxBytes <= 0 {
		return backup.Report{}, errors.New("restore upload reader and positive byte limit are required")
	}
	dataDir, err := filepath.Abs(dataDir)
	if err != nil {
		return backup.Report{}, fmt.Errorf("resolve restore data directory: %w", err)
	}
	if _, err := os.Lstat(filepath.Join(dataDir, markerName)); !errors.Is(err, os.ErrNotExist) {
		return backup.Report{}, errors.New("another restore is already pending")
	}
	uploadRoot, err := os.MkdirTemp(filepath.Join(dataDir, "tmp"), ".restore-upload-")
	if err != nil {
		return backup.Report{}, fmt.Errorf("create restore upload directory: %w", err)
	}
	defer os.RemoveAll(uploadRoot)

	files, total, err := receive(ctx, uploadRoot, reader, maxBytes)
	if err != nil {
		return backup.Report{}, err
	}
	if files == 0 || total == 0 {
		return backup.Report{}, errors.New("restore upload contains no files")
	}
	if err := materializeManifestDirectories(uploadRoot); err != nil {
		return backup.Report{}, err
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
	report, err := backup.Restore(ctx, uploadRoot, candidate, maxBytes)
	if err != nil {
		return backup.Report{}, fmt.Errorf("validate uploaded backup: %w", err)
	}
	pending := state{Version: 1, Work: filepath.Base(work)}
	if err := writeState(dataDir, pending); err != nil {
		return backup.Report{}, err
	}
	keepWork = true
	return report, nil
}

func receive(ctx context.Context, root string, reader *multipart.Reader, maxBytes int64) (int, int64, error) {
	var files int
	var total int64
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			return files, total, nil
		}
		if err != nil {
			return 0, 0, fmt.Errorf("read restore upload: %w", err)
		}
		name := part.FormName()
		if part.FileName() == "" || !validUploadPath(name) {
			part.Close()
			return 0, 0, fmt.Errorf("invalid restore upload path %q", name)
		}
		files++
		if files > backup.MaxEntries+1 {
			part.Close()
			return 0, 0, errors.New("restore upload has too many files")
		}
		if err := writePart(ctx, root, name, part, maxBytes-total, &total); err != nil {
			part.Close()
			return 0, 0, err
		}
		if err := part.Close(); err != nil {
			return 0, 0, fmt.Errorf("close restore upload part: %w", err)
		}
	}
}

func validUploadPath(name string) bool {
	if !fs.ValidPath(name) || name == "." || len(name) > 4096 || strings.ContainsAny(name, "\\:\"<>|?*") {
		return false
	}
	return name == "manifest.json" || strings.HasPrefix(name, "data/")
}

func writePart(ctx context.Context, root, name string, source io.Reader, remaining int64, total *int64) error {
	if remaining <= 0 {
		return errors.New("restore upload exceeds byte limit")
	}
	target := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return fmt.Errorf("create restore upload directory: %w", err)
	}
	file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create restore upload file %q: %w", name, err)
	}
	limited := &io.LimitedReader{R: &contextReader{ctx: ctx, reader: source}, N: remaining + 1}
	written, copyErr := io.Copy(file, limited)
	*total += written
	closeErr := file.Close()
	if copyErr != nil {
		return fmt.Errorf("write restore upload file %q: %w", name, copyErr)
	}
	if written > remaining {
		return errors.New("restore upload exceeds byte limit")
	}
	if closeErr != nil {
		return fmt.Errorf("close restore upload file %q: %w", name, closeErr)
	}
	return nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader *contextReader) Read(buffer []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.reader.Read(buffer)
}

func materializeManifestDirectories(root string) error {
	encoded, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	if err != nil {
		return fmt.Errorf("read uploaded backup manifest: %w", err)
	}
	if len(encoded) > maxManifestSize {
		return errors.New("uploaded backup manifest exceeds 16 MiB")
	}
	var manifest backup.Manifest
	decoder := json.NewDecoder(strings.NewReader(string(encoded)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return fmt.Errorf("decode uploaded backup manifest: %w", err)
	}
	for _, entry := range manifest.Entries {
		if !entry.Directory {
			continue
		}
		name := path.Join("data", entry.Path)
		if !validUploadPath(name) {
			return fmt.Errorf("invalid restore manifest directory %q", entry.Path)
		}
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(name)), 0o700); err != nil {
			return fmt.Errorf("create restore manifest directory %q: %w", entry.Path, err)
		}
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
