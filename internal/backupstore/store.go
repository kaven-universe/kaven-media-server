// Package backupstore manages the private backup repository below the data directory.
package backupstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"kaven.xyz/kaven/kaven-media-server/internal/backup"
	"kaven.xyz/kaven/kaven-media-server/internal/buildinfo"
)

const (
	DirectoryName = "backup"
	requestName   = ".create-request.json"
	resultName    = ".create-result.json"
	maxManifest   = 16 << 20
	maxBackups    = 1000
)

var validName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

type Store struct {
	dataDir string
	root    string
}

type Entry struct {
	Name            string          `json:"name"`
	Files           int             `json:"files"`
	Bytes           int64           `json:"bytes"`
	ManifestVersion int             `json:"manifestVersion"`
	Created         time.Time       `json:"created"`
	Producer        *buildinfo.Info `json:"producer,omitempty"`
}

type Status struct {
	Backups   []Entry `json:"backups"`
	LastError string  `json:"lastError,omitempty"`
}

type createRequest struct {
	Version int    `json:"version"`
	Name    string `json:"name"`
}

type createResult struct {
	Error string `json:"error"`
}

func New(dataDir string) (*Store, error) {
	resolved, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, fmt.Errorf("resolve backup data directory: %w", err)
	}
	root := filepath.Join(resolved, DirectoryName)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create private backup directory: %w", err)
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("private backup path must be a directory, not a symlink")
	}
	return &Store{dataDir: resolved, root: root}, nil
}

func (store *Store) List() (Status, error) {
	children, err := store.readRootEntries()
	if err != nil {
		return Status{}, err
	}
	status := Status{Backups: make([]Entry, 0, len(children))}
	for _, child := range children {
		if strings.HasPrefix(child.Name(), ".") || !child.IsDir() || child.Type()&os.ModeSymlink != 0 {
			continue
		}
		entry, err := store.inspect(child.Name())
		if err == nil {
			status.Backups = append(status.Backups, entry)
		}
	}
	sort.Slice(status.Backups, func(i, j int) bool {
		return status.Backups[i].Created.After(status.Backups[j].Created)
	})
	var result createResult
	if err := readJSONFile(filepath.Join(store.root, resultName), 16<<10, &result); err == nil {
		status.LastError = result.Error
	} else if !errors.Is(err, os.ErrNotExist) {
		return Status{}, fmt.Errorf("read backup creation result: %w", err)
	}
	return status, nil
}

func (store *Store) Resolve(name string) (string, error) {
	if err := ValidateName(name); err != nil {
		return "", err
	}
	target := filepath.Join(store.root, name)
	info, err := os.Lstat(target)
	if errors.Is(err, os.ErrNotExist) {
		return "", errors.New("backup does not exist")
	}
	if err != nil {
		return "", fmt.Errorf("inspect backup: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("backup must be a directory, not a symlink")
	}
	return target, nil
}

func (store *Store) Queue(name string) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	children, err := store.readRootEntries()
	if err != nil {
		return err
	}
	for _, child := range children {
		if strings.EqualFold(child.Name(), name) {
			return errors.New("backup name already exists")
		}
	}
	if _, err := os.Lstat(filepath.Join(store.root, name)); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			return errors.New("backup name already exists")
		}
		return fmt.Errorf("inspect backup destination: %w", err)
	}
	encoded, err := json.Marshal(createRequest{Version: 1, Name: name})
	if err != nil {
		return err
	}
	request := filepath.Join(store.root, requestName)
	file, err := os.OpenFile(request, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return errors.New("another backup is already queued")
	}
	if err != nil {
		return fmt.Errorf("queue backup: %w", err)
	}
	if _, err = file.Write(encoded); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		_ = os.Remove(request)
		return fmt.Errorf("write backup request: %w", err)
	}
	if closeErr != nil {
		_ = os.Remove(request)
		return fmt.Errorf("close backup request: %w", closeErr)
	}
	_ = os.Remove(filepath.Join(store.root, resultName))
	return nil
}

func (store *Store) readRootEntries() ([]os.DirEntry, error) {
	directory, err := os.Open(store.root)
	if err != nil {
		return nil, fmt.Errorf("list private backups: %w", err)
	}
	defer directory.Close()
	children, err := directory.ReadDir(maxBackups + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("list private backups: %w", err)
	}
	if len(children) > maxBackups {
		return nil, errors.New("private backup directory contains more than 1000 entries")
	}
	return children, nil
}

// ApplyPending creates a queued snapshot while the server is stopped.
func (store *Store) ApplyPending(ctx context.Context, maxBytes int64) (bool, error) {
	requestPath := filepath.Join(store.root, requestName)
	var request createRequest
	if err := readJSONFile(requestPath, 16<<10, &request); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return store.finishFailedRequest(requestPath, fmt.Errorf("read queued backup: %w", err))
	}
	if request.Version != 1 {
		return store.finishFailedRequest(requestPath, errors.New("unsupported queued backup request"))
	}
	if err := ValidateName(request.Name); err != nil {
		return store.finishFailedRequest(requestPath, err)
	}
	destination := filepath.Join(store.root, request.Name)
	if _, err := os.Lstat(destination); errors.Is(err, os.ErrNotExist) {
		_, err = backup.Create(ctx, store.dataDir, destination, maxBytes)
		if err != nil {
			return store.finishFailedRequest(requestPath, err)
		}
	} else if err != nil {
		return false, fmt.Errorf("inspect queued backup destination: %w", err)
	}
	if err := os.Remove(requestPath); err != nil {
		return false, fmt.Errorf("complete queued backup: %w", err)
	}
	_ = os.Remove(filepath.Join(store.root, resultName))
	return true, nil
}

func (store *Store) finishFailedRequest(requestPath string, cause error) (bool, error) {
	if err := store.recordFailure(cause); err != nil {
		return false, errors.Join(cause, err)
	}
	if err := os.Remove(requestPath); err != nil {
		return false, errors.Join(cause, fmt.Errorf("remove failed backup request: %w", err))
	}
	return true, nil
}

func ValidateName(name string) error {
	if !validName.MatchString(name) || name == "." || name == ".." || strings.HasSuffix(name, ".") || strings.HasSuffix(name, " ") {
		return errors.New("backup name must use 1-128 letters, numbers, dots, underscores, or hyphens")
	}
	return nil
}

func (store *Store) inspect(name string) (Entry, error) {
	var manifest backup.Manifest
	if err := readJSONFile(filepath.Join(store.root, name, "manifest.json"), maxManifest, &manifest); err != nil {
		return Entry{}, err
	}
	entry := Entry{Name: name, ManifestVersion: manifest.Version, Created: manifest.Created, Producer: manifest.Producer}
	for _, item := range manifest.Entries {
		if !item.Directory {
			entry.Files++
			entry.Bytes += item.Size
		}
	}
	return entry, nil
}

func (store *Store) recordFailure(cause error) error {
	encoded, err := json.Marshal(createResult{Error: cause.Error()})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(store.root, resultName), encoded, 0o600)
}

func readJSONFile(name string, limit int64, destination any) error {
	file, err := os.Open(name)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if info.Size() > limit {
		return errors.New("JSON file exceeds size limit")
	}
	decoder := json.NewDecoder(io.LimitReader(file, limit+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON data")
	}
	return nil
}
