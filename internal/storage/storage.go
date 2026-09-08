package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var (
	ErrInvalidPath      = errors.New("invalid storage path")
	ErrPathEscape       = errors.New("storage path escapes root")
	ErrNotFound         = errors.New("storage path not found")
	ErrExists           = errors.New("storage path already exists")
	ErrTooLarge         = errors.New("storage input exceeds byte limit")
	ErrInvalidLimit     = errors.New("storage byte limit must be positive")
	ErrAlreadyFinalized = errors.New("staged file already finalized")
)

type Store struct {
	root    string
	tempDir string
}

type WriteResult struct {
	Path string
	Size int64
}

type ValidateFunc func(path string, size int64) error

func New(root string) (*Store, error) {
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("create storage: %w: empty root", ErrInvalidPath)
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("create storage: resolve root: %w", err)
	}
	if err := os.MkdirAll(absolute, 0o755); err != nil {
		return nil, fmt.Errorf("create storage root: %w", err)
	}
	store, err := Open(absolute)
	if err != nil {
		return nil, err
	}
	if err := store.MkdirAll("tmp", 0o700); err != nil {
		return nil, fmt.Errorf("create storage temporary directory: %w", err)
	}
	return store, nil
}

// Open validates an existing storage root without creating or changing files.
func Open(root string) (*Store, error) {
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("open storage: %w: empty root", ErrInvalidPath)
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("open storage: resolve root: %w", err)
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return nil, fmt.Errorf("open storage: inspect root: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("open storage: %w: root is a symlink", ErrPathEscape)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("open storage: %w: root is not a directory", ErrInvalidPath)
	}

	store := &Store{
		root:    filepath.Clean(absolute),
		tempDir: filepath.Join(filepath.Clean(absolute), "tmp"),
	}
	return store, nil
}

func (store *Store) Root() string {
	return store.root
}

// Resolve validates a relative, platform-neutral path and rejects symlinks in
// every existing descendant of the storage root.
func (store *Store) Resolve(relative string) (string, error) {
	segments, err := pathSegments(relative)
	if err != nil {
		return "", err
	}

	current := store.root
	for index, segment := range segments {
		current = filepath.Join(current, segment)
		info, err := os.Lstat(current)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				for _, remaining := range segments[index+1:] {
					current = filepath.Join(current, remaining)
				}
				return current, nil
			}
			return "", fmt.Errorf("resolve storage path %q: %w", relative, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("resolve storage path %q: %w: symlink component %q", relative, ErrPathEscape, segment)
		}
		if index < len(segments)-1 && !info.IsDir() {
			return "", fmt.Errorf("resolve storage path %q: %w: component %q is not a directory", relative, ErrInvalidPath, segment)
		}
	}
	return current, nil
}

func (store *Store) ResolveExisting(relative string) (string, error) {
	resolved, err := store.Resolve(relative)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(resolved); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("resolve existing storage path %q: %w", relative, ErrNotFound)
		}
		return "", fmt.Errorf("resolve existing storage path %q: %w", relative, err)
	}
	return resolved, nil
}

// ResolveManagedFileReference resolves an existing file below a required
// managed-storage prefix.
func (store *Store) ResolveManagedFileReference(reference, managedPrefix string) (string, error) {
	native := strings.ReplaceAll(reference, `\`, string(filepath.Separator))
	if filepath.IsAbs(native) || filepath.VolumeName(native) != "" {
		return "", fmt.Errorf("resolve managed file reference %q: %w", reference, ErrInvalidPath)
	}
	normalized := strings.ReplaceAll(reference, `\`, "/")
	if normalized != managedPrefix && !strings.HasPrefix(normalized, managedPrefix+"/") {
		return "", fmt.Errorf("resolve managed file reference %q: %w: expected %s path", reference, ErrInvalidPath, managedPrefix)
	}
	return store.ResolveExisting(normalized)
}

// ResolveReadOnlyFileReference accepts either a managed path below the required
// prefix or an explicit absolute read-only path. Absolute paths must not contain
// symlink components.
func (store *Store) ResolveReadOnlyFileReference(reference, managedPrefix string) (string, error) {
	native := strings.ReplaceAll(reference, `\`, string(filepath.Separator))
	if filepath.IsAbs(native) || filepath.VolumeName(native) != "" {
		return resolveAbsoluteFileReference(native)
	}
	return store.ResolveManagedFileReference(reference, managedPrefix)
}

func resolveAbsoluteFileReference(reference string) (string, error) {
	if strings.TrimSpace(reference) == "" || strings.IndexByte(reference, 0) >= 0 || !filepath.IsAbs(reference) {
		return "", fmt.Errorf("resolve absolute file reference: %w", ErrInvalidPath)
	}
	absolute := filepath.Clean(reference)
	volume := filepath.VolumeName(absolute)
	anchor := string(filepath.Separator)
	if volume != "" {
		anchor = volume + string(filepath.Separator)
	}
	relative, err := filepath.Rel(anchor, absolute)
	if err != nil || relative == "." {
		return "", fmt.Errorf("resolve absolute file reference %q: %w", reference, ErrInvalidPath)
	}
	current := anchor
	for _, segment := range strings.Split(relative, string(filepath.Separator)) {
		if segment == "" || segment == "." || segment == ".." {
			return "", fmt.Errorf("resolve absolute file reference %q: %w", reference, ErrInvalidPath)
		}
		current = filepath.Join(current, segment)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("resolve absolute file reference %q: %w", reference, ErrNotFound)
		}
		if err != nil {
			return "", fmt.Errorf("resolve absolute file reference %q: %w", reference, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("resolve absolute file reference %q: %w: symlink component", reference, ErrPathEscape)
		}
	}
	return absolute, nil
}

func (store *Store) RemoveFile(relative string) error {
	resolved, err := store.Resolve(relative)
	if err != nil {
		return err
	}
	info, err := os.Lstat(resolved)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove storage file %q: %w", relative, ErrNotFound)
		}
		return fmt.Errorf("remove storage file %q: %w", relative, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("remove storage file %q: %w: not a regular file", relative, ErrInvalidPath)
	}
	if err := os.Remove(resolved); err != nil {
		return fmt.Errorf("remove storage file %q: %w", relative, err)
	}
	return nil
}

func (store *Store) MkdirAll(relative string, permission os.FileMode) error {
	segments, err := pathSegments(relative)
	if err != nil {
		return err
	}
	current := store.root
	for _, segment := range segments {
		current = filepath.Join(current, segment)
		if err := os.Mkdir(current, permission); err != nil && !errors.Is(err, os.ErrExist) {
			return fmt.Errorf("create storage directory %q: %w", relative, err)
		}
		info, err := os.Lstat(current)
		if err != nil {
			return fmt.Errorf("inspect storage directory %q: %w", relative, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("create storage directory %q: %w: symlink component %q", relative, ErrPathEscape, segment)
		}
		if !info.IsDir() {
			return fmt.Errorf("create storage directory %q: %w: component %q is not a directory", relative, ErrInvalidPath, segment)
		}
	}
	return nil
}

func (store *Store) Stage(ctx context.Context, source io.Reader, maxBytes int64) (*StagedFile, error) {
	const maxInt64 = int64(^uint64(0) >> 1)
	if maxBytes <= 0 || maxBytes == maxInt64 {
		return nil, ErrInvalidLimit
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := store.ResolveExisting("tmp"); err != nil {
		return nil, fmt.Errorf("stage file: %w", err)
	}

	file, err := os.CreateTemp(store.tempDir, ".upload-*")
	if err != nil {
		return nil, fmt.Errorf("stage file: create temporary file: %w", err)
	}
	path := file.Name()
	cleanup := func() {
		file.Close()
		os.Remove(path)
	}

	limited := &io.LimitedReader{R: contextReader{ctx: ctx, reader: source}, N: maxBytes + 1}
	size, err := io.Copy(file, limited)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("stage file: copy input: %w", err)
	}
	if size > maxBytes {
		cleanup()
		return nil, ErrTooLarge
	}
	if err := file.Sync(); err != nil {
		cleanup()
		return nil, fmt.Errorf("stage file: sync temporary file: %w", err)
	}
	if err := file.Close(); err != nil {
		os.Remove(path)
		return nil, fmt.Errorf("stage file: close temporary file: %w", err)
	}
	return &StagedFile{store: store, path: path, size: size}, nil
}

func (store *Store) WriteAtomic(ctx context.Context, relative string, source io.Reader, maxBytes int64, permission os.FileMode, validate ValidateFunc) (WriteResult, error) {
	staged, err := store.Stage(ctx, source, maxBytes)
	if err != nil {
		return WriteResult{}, err
	}
	defer staged.Abort()

	if validate != nil {
		if err := validate(staged.Path(), staged.Size()); err != nil {
			return WriteResult{}, fmt.Errorf("validate staged file: %w", err)
		}
	}
	path, err := staged.Commit(relative, permission)
	if err != nil {
		return WriteResult{}, err
	}
	return WriteResult{Path: path, Size: staged.Size()}, nil
}

func pathSegments(relative string) ([]string, error) {
	if strings.TrimSpace(relative) == "" || strings.IndexByte(relative, 0) >= 0 {
		return nil, fmt.Errorf("%w: empty path or NUL byte", ErrInvalidPath)
	}
	normalized := strings.ReplaceAll(relative, `\`, "/")
	if strings.HasPrefix(normalized, "/") || isWindowsAbsolute(normalized) || filepath.IsAbs(relative) || filepath.VolumeName(relative) != "" {
		return nil, fmt.Errorf("%w: absolute path %q", ErrPathEscape, relative)
	}
	segments := strings.Split(normalized, "/")
	for _, segment := range segments {
		if segment == ".." {
			return nil, fmt.Errorf("%w: traversal in %q", ErrPathEscape, relative)
		}
		if segment == "" || segment == "." || strings.Contains(segment, ":") {
			return nil, fmt.Errorf("%w: non-canonical component in %q", ErrInvalidPath, relative)
		}
	}
	return segments, nil
}

func isWindowsAbsolute(path string) bool {
	return len(path) >= 3 && ((path[0] >= 'a' && path[0] <= 'z') || (path[0] >= 'A' && path[0] <= 'Z')) && path[1] == ':' && path[2] == '/'
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader contextReader) Read(buffer []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.reader.Read(buffer)
}

type StagedFile struct {
	store *Store
	path  string
	size  int64

	mutex     sync.Mutex
	finalized bool
}

func (file *StagedFile) Path() string {
	return file.path
}

func (file *StagedFile) Size() int64 {
	return file.size
}

// Commit atomically publishes a complete staged file without replacing an
// existing destination. Hard-link publication provides portable no-overwrite
// semantics on the supported local-filesystem deployment model.
func (file *StagedFile) Commit(relative string, permission os.FileMode) (string, error) {
	file.mutex.Lock()
	defer file.mutex.Unlock()
	if file.finalized {
		return "", ErrAlreadyFinalized
	}

	destination, err := file.store.Resolve(relative)
	if err != nil {
		return "", err
	}
	parentRelative := filepath.ToSlash(filepath.Dir(strings.ReplaceAll(relative, `\`, "/")))
	if parentRelative != "." {
		parent, err := file.store.ResolveExisting(parentRelative)
		if err != nil {
			return "", fmt.Errorf("commit staged file: resolve parent: %w", err)
		}
		info, err := os.Stat(parent)
		if err != nil || !info.IsDir() {
			return "", fmt.Errorf("commit staged file: destination parent is not a directory: %w", ErrInvalidPath)
		}
	}
	if err := os.Chmod(file.path, permission.Perm()); err != nil {
		return "", fmt.Errorf("commit staged file: set permissions: %w", err)
	}
	if err := os.Link(file.path, destination); err != nil {
		if errors.Is(err, os.ErrExist) {
			return "", fmt.Errorf("commit staged file %q: %w", relative, ErrExists)
		}
		return "", fmt.Errorf("commit staged file %q: %w", relative, err)
	}

	file.finalized = true
	_ = os.Remove(file.path)
	return destination, nil
}

func (file *StagedFile) Abort() error {
	file.mutex.Lock()
	defer file.mutex.Unlock()
	if file.finalized {
		return nil
	}
	file.finalized = true
	if err := os.Remove(file.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("abort staged file: %w", err)
	}
	return nil
}
