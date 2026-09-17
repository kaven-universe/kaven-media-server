// Package applog configures rotating persistent application logging.
package applog

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"kaven.xyz/kaven/kaven-media-server/internal/storage"
)

const (
	DirectoryName     = "logs"
	Filename          = "kaven-media.log"
	DefaultMaxBytes   = int64(10 * 1024 * 1024)
	DefaultMaxBackups = 5
	MaximumBackups    = 100
	archiveTimeLayout = "20060102T150405.000000000Z"
)

type Options struct {
	MaxBytes   int64
	MaxBackups int
}

// Writer appends to the active log and rotates it before a write would exceed
// the configured size. Writes are serialized because slog handlers may run
// concurrently.
type Writer struct {
	mu         sync.Mutex
	filename   string
	file       *os.File
	size       int64
	maxBytes   int64
	maxBackups int
	enabled    bool
	closed     bool
}

// New returns a text logger that writes identical records to the supplied
// console and to the persistent data-directory log.
func New(dataDir string, console io.Writer, options Options) (*slog.Logger, *Writer, error) {
	if console == nil {
		return nil, nil, errors.New("create application logger: console writer is required")
	}
	if options.MaxBytes == 0 {
		options.MaxBytes = DefaultMaxBytes
	}
	if options.MaxBackups == 0 {
		options.MaxBackups = DefaultMaxBackups
	}
	if options.MaxBytes < 1 || options.MaxBackups < 1 || options.MaxBackups > MaximumBackups {
		return nil, nil, fmt.Errorf("create application logger: size must be positive and backups must be between 1 and %d", MaximumBackups)
	}

	store, err := storage.New(dataDir)
	if err != nil {
		return nil, nil, fmt.Errorf("create application logger: %w", err)
	}
	if err := store.MkdirAll(DirectoryName, 0o750); err != nil {
		return nil, nil, fmt.Errorf("create application log directory: %w", err)
	}
	filename, err := store.Resolve(path.Join(DirectoryName, Filename))
	if err != nil {
		return nil, nil, fmt.Errorf("resolve application log: %w", err)
	}
	writer := &Writer{filename: filename, maxBytes: options.MaxBytes, maxBackups: options.MaxBackups, enabled: true}
	if err := writer.open(); err != nil {
		return nil, nil, err
	}
	handler := slog.NewTextHandler(io.MultiWriter(console, writer), &slog.HandlerOptions{
		ReplaceAttr: func(_ []string, attribute slog.Attr) slog.Attr {
			if attribute.Key == slog.TimeKey {
				attribute.Value = slog.TimeValue(attribute.Value.Time().UTC())
			}
			return attribute
		},
	})
	return slog.New(handler), writer, nil
}

func (writer *Writer) Path() string { return writer.filename }

// Configure changes persistent file logging without replacing the process-wide
// slog logger. The console side of the logger is independent of this writer.
func (writer *Writer) Configure(enabled bool, maxBytes int64, maxBackups int) error {
	if maxBytes < 1 || maxBackups < 0 || maxBackups > MaximumBackups {
		return fmt.Errorf("configure application log: size must be positive and backups must be between 0 and %d", MaximumBackups)
	}
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.closed {
		return errors.New("configure application log: writer is closed")
	}
	writer.maxBytes = maxBytes
	writer.maxBackups = maxBackups
	if maxBackups > 0 {
		if err := writer.pruneArchives(maxBackups); err != nil {
			return fmt.Errorf("configure application log: %w", err)
		}
	}
	if !enabled {
		writer.enabled = false
		return writer.closeFile()
	}
	if writer.file == nil {
		if err := writer.open(); err != nil {
			writer.enabled = false
			return err
		}
	}
	writer.enabled = true
	return nil
}

func (writer *Writer) Write(value []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.closed {
		return 0, errors.New("write application log: writer is closed")
	}
	if !writer.enabled {
		return len(value), nil
	}
	if writer.file == nil {
		return 0, errors.New("write application log: file is unavailable")
	}
	if writer.size > 0 && writer.size+int64(len(value)) > writer.maxBytes {
		if err := writer.rotate(); err != nil {
			return 0, err
		}
	}
	written, err := writer.file.Write(value)
	writer.size += int64(written)
	if err != nil {
		return written, fmt.Errorf("write application log: %w", err)
	}
	return written, nil
}

func (writer *Writer) Close() error {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.closed {
		return nil
	}
	writer.closed = true
	return writer.closeFile()
}

func (writer *Writer) closeFile() error {
	if writer.file == nil {
		return nil
	}
	err := writer.file.Close()
	writer.file = nil
	if err != nil {
		return fmt.Errorf("close application log: %w", err)
	}
	return nil
}

func (writer *Writer) open() error {
	file, err := os.OpenFile(writer.filename, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o640)
	if err != nil {
		return fmt.Errorf("open application log: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return fmt.Errorf("inspect application log: %w", err)
	}
	if !info.Mode().IsRegular() {
		file.Close()
		return errors.New("open application log: path is not a regular file")
	}
	writer.file = file
	writer.size = info.Size()
	return nil
}

func (writer *Writer) rotate() error {
	if err := writer.file.Close(); err != nil {
		return fmt.Errorf("rotate application log: close active file: %w", err)
	}
	writer.file = nil
	var rotateErr error
	archivePath, err := writer.nextArchivePath(time.Now().UTC())
	if err != nil {
		rotateErr = errors.Join(rotateErr, err)
	} else if err := os.Rename(writer.filename, archivePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		rotateErr = errors.Join(rotateErr, fmt.Errorf("archive active log: %w", err))
	}
	if err := writer.open(); err != nil {
		return errors.Join(rotateErr, err)
	}
	if writer.maxBackups > 0 {
		rotateErr = errors.Join(rotateErr, writer.pruneArchives(writer.maxBackups))
	}
	if rotateErr != nil {
		return fmt.Errorf("rotate application log: %w", rotateErr)
	}
	return nil
}

type archiveFile struct {
	path string
	time time.Time
}

func (writer *Writer) archives() ([]archiveFile, error) {
	entries, err := os.ReadDir(filepath.Dir(writer.filename))
	if err != nil {
		return nil, fmt.Errorf("inspect application log backups: %w", err)
	}
	base := strings.TrimSuffix(filepath.Base(writer.filename), filepath.Ext(writer.filename))
	prefix := base + "-"
	suffix := filepath.Ext(writer.filename)
	archives := make([]archiveFile, 0)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, suffix) {
			continue
		}
		stamp := strings.TrimSuffix(strings.TrimPrefix(name, prefix), suffix)
		rotatedAt, err := time.Parse(archiveTimeLayout, stamp)
		if err == nil {
			archives = append(archives, archiveFile{path: filepath.Join(filepath.Dir(writer.filename), name), time: rotatedAt})
		}
	}
	sort.Slice(archives, func(left, right int) bool { return archives[left].time.After(archives[right].time) })
	return archives, nil
}

func (writer *Writer) nextArchivePath(now time.Time) (string, error) {
	directory := filepath.Dir(writer.filename)
	base := strings.TrimSuffix(filepath.Base(writer.filename), filepath.Ext(writer.filename))
	extension := filepath.Ext(writer.filename)
	for offset := time.Duration(0); ; offset++ {
		candidate := filepath.Join(directory, base+"-"+now.Add(offset).UTC().Format(archiveTimeLayout)+extension)
		if _, err := os.Lstat(candidate); errors.Is(err, os.ErrNotExist) {
			return candidate, nil
		} else if err != nil {
			return "", fmt.Errorf("inspect application log archive path: %w", err)
		}
	}
}

func (writer *Writer) pruneArchives(limit int) error {
	archives, err := writer.archives()
	if err != nil {
		return err
	}
	if len(archives) <= limit {
		return nil
	}
	var pruneErr error
	for _, archive := range archives[limit:] {
		if err := os.Remove(archive.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			pruneErr = errors.Join(pruneErr, fmt.Errorf("remove old application log %q: %w", filepath.Base(archive.path), err))
		}
	}
	return pruneErr
}

var _ io.WriteCloser = (*Writer)(nil)
