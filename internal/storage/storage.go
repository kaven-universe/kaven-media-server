package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
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

const (
	UploadRootDirectory  = "upload"
	BingArchiveDirectory = "download/bing"
)

type Store struct {
	root                 string
	tempDir              string
	uploadDirectory      string
	bingArchiveDirectory string
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
		root:                 filepath.Clean(absolute),
		tempDir:              filepath.Join(filepath.Clean(absolute), "tmp"),
		uploadDirectory:      UploadRootDirectory,
		bingArchiveDirectory: BingArchiveDirectory,
	}
	return store, nil
}

func (store *Store) Root() string {
	return store.root
}

func (store *Store) ConfigureMediaDirectories(uploadDirectory, downloadDirectory string) error {
	if _, err := pathSegments(uploadDirectory); err != nil {
		return fmt.Errorf("configure upload directory: %w", err)
	}
	if _, err := pathSegments(downloadDirectory); err != nil {
		return fmt.Errorf("configure download directory: %w", err)
	}
	store.uploadDirectory = uploadDirectory
	store.bingArchiveDirectory = path.Join(downloadDirectory, "bing")
	return nil
}

func (store *Store) UploadDirectory() string { return store.uploadDirectory }

func (store *Store) BingArchiveDirectory() string { return store.bingArchiveDirectory }

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

// ResolveStoredFileReference resolves a media path stored relative to its
// configured media directory. Cache references retain their data-root-relative
// layout because the cache directory is fixed.
func (store *Store) ResolveStoredFileReference(reference, kind string) (string, error) {
	if strings.Contains(reference, `\`) {
		return "", fmt.Errorf("resolve stored file reference %q: %w: path must use forward slashes", reference, ErrInvalidPath)
	}
	if _, err := pathSegments(reference); err != nil {
		return "", fmt.Errorf("resolve stored file reference %q: %w", reference, err)
	}
	normalized := reference
	if kind == "image" || kind == "bing_image" {
		prefix := store.uploadDirectory
		if kind == "bing_image" {
			prefix = store.bingArchiveDirectory
		}
		storageReference := path.Join(prefix, normalized)
		return store.ResolveExisting(storageReference)
	}
	if kind != "image_cache" {
		return "", fmt.Errorf("resolve stored file reference %q: %w: unknown kind", reference, ErrInvalidPath)
	}
	if normalized == "cache" || strings.HasPrefix(normalized, "cache/") {
		return store.ResolveExisting(normalized)
	}
	return "", fmt.Errorf("resolve stored file reference %q: %w: unexpected %s path", reference, ErrInvalidPath, kind)
}

// ResolveAbsoluteFileReference resolves an existing absolute path after
// checking that none of its components are symlinks.
func ResolveAbsoluteFileReference(reference string) (string, error) {
	return resolveAbsoluteFileReference(reference)
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

// CommitAbsolute publishes a staged file to an absolute destination without
// replacing an existing entry. If the staging and destination directories are
// on different filesystems, it first copies to a temporary file beside the
// destination and then uses a hard link for the final no-overwrite operation.
func (file *StagedFile) CommitAbsolute(destination string, permission os.FileMode) (string, error) {
	file.mutex.Lock()
	defer file.mutex.Unlock()
	if file.finalized {
		return "", ErrAlreadyFinalized
	}
	if strings.TrimSpace(destination) == "" || !filepath.IsAbs(destination) {
		return "", fmt.Errorf("commit staged file: %w: destination must be absolute", ErrInvalidPath)
	}
	destination = filepath.Clean(destination)
	parent, err := ResolveAbsoluteFileReference(filepath.Dir(destination))
	if err != nil {
		return "", fmt.Errorf("commit staged file: resolve parent: %w", err)
	}
	if filepath.Clean(filepath.Join(parent, filepath.Base(destination))) != destination {
		return "", fmt.Errorf("commit staged file: %w: invalid destination", ErrInvalidPath)
	}
	if _, err := os.Lstat(destination); err == nil {
		return "", fmt.Errorf("commit staged file %q: %w", destination, ErrExists)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("commit staged file %q: %w", destination, err)
	}
	if err := os.Chmod(file.path, permission.Perm()); err != nil {
		return "", fmt.Errorf("commit staged file: set permissions: %w", err)
	}
	if err := os.Link(file.path, destination); err == nil {
		file.finalized = true
		_ = os.Remove(file.path)
		return destination, nil
	} else if errors.Is(err, os.ErrExist) {
		return "", fmt.Errorf("commit staged file %q: %w", destination, ErrExists)
	}

	temporary, err := os.CreateTemp(parent, ".kaven-upload-*")
	if err != nil {
		return "", fmt.Errorf("commit staged file: create destination temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	source, err := os.Open(file.path)
	if err != nil {
		_ = temporary.Close()
		return "", fmt.Errorf("commit staged file: reopen staged file: %w", err)
	}
	_, copyErr := io.Copy(temporary, source)
	closeSourceErr := source.Close()
	if copyErr != nil {
		_ = temporary.Close()
		return "", fmt.Errorf("commit staged file: copy to destination filesystem: %w", copyErr)
	}
	if closeSourceErr != nil {
		_ = temporary.Close()
		return "", fmt.Errorf("commit staged file: close staged file: %w", closeSourceErr)
	}
	if err := temporary.Chmod(permission.Perm()); err != nil {
		_ = temporary.Close()
		return "", fmt.Errorf("commit staged file: set destination permissions: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return "", fmt.Errorf("commit staged file: sync destination temporary file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return "", fmt.Errorf("commit staged file: close destination temporary file: %w", err)
	}
	if err := os.Link(temporaryPath, destination); err != nil {
		if errors.Is(err, os.ErrExist) {
			return "", fmt.Errorf("commit staged file %q: %w", destination, ErrExists)
		}
		return "", fmt.Errorf("commit staged file %q: %w", destination, err)
	}
	_ = os.Remove(temporaryPath)
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
