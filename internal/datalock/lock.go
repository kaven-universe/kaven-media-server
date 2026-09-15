// Package datalock coordinates processes using a local data directory.
package datalock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const Filename = ".kaven-media.lock"

var ErrBusy = errors.New("data directory is in use; stop the server and other maintenance commands first")

// Acquire holds an OS lock until Close or process exit. The lock file must
// remain in place, including after Close, so waiters always lock the same inode.
func Acquire(directory string) (*os.File, error) {
	if directory == "" {
		return nil, errors.New("lock data directory: empty path")
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return nil, fmt.Errorf("create lock directory: %w", err)
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("lock data directory: invalid root %q", directory)
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	info, err = root.Lstat(Filename)
	if err == nil && !info.Mode().IsRegular() {
		return nil, errors.New("lock data directory: lock is not a regular file")
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	file, err := root.OpenFile(Filename, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open directory lock: %w", err)
	}
	if err := lock(file); err != nil {
		file.Close()
		return nil, fmt.Errorf("lock %s: %w", filepath.Clean(directory), err)
	}
	return file, nil
}
