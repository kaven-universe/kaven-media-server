package hfs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"kaven.xyz/kaven/kaven-media-server/internal/config"
	"kaven.xyz/kaven/kaven-media-server/internal/storage"
)

const (
	maxRootNameLength          = 64
	DefaultMaxDirectoryEntries = 10_000
	DefaultMaxMutations        = 2
)

var (
	ErrUnsupportedEntry  = errors.New("unsupported HFS entry")
	ErrDirectoryTooLarge = errors.New("HFS directory has too many entries")
	ErrNotDirectory      = errors.New("HFS path is not a directory")
	ErrRollbackFailed    = errors.New("HFS upload rollback failed")
	ErrReadOnly          = errors.New("HFS root is read-only")
)

type UploadFile struct {
	Name   string
	Staged *storage.StagedFile
}

type Root struct {
	Name     string
	Path     string
	Public   bool
	ReadOnly bool
}

func (root Root) AllowsRead(admin bool) bool {
	return root.Public || admin
}

func (root Root) AllowsWrite(admin bool) bool {
	return admin && !root.ReadOnly
}

type Entry struct {
	Name         string
	IsDirectory  bool
	Size         int64
	LastModified time.Time
}

type Registry struct {
	store     *storage.Store
	roots     []Root
	byName    map[string]Root
	mutations chan struct{}
}

func NewRegistry(store *storage.Store, configured []config.HFSRoot) (*Registry, error) {
	if store == nil {
		return nil, errors.New("create HFS root registry: storage is required")
	}
	roots := make([]Root, 0, len(configured))
	byName := make(map[string]Root, len(configured))
	for index, candidate := range configured {
		root, err := validateRoot(candidate)
		if err != nil {
			return nil, fmt.Errorf("create HFS root registry: root %d: %w", index, err)
		}
		for _, existing := range roots {
			if strings.EqualFold(existing.Name, root.Name) {
				return nil, fmt.Errorf("create HFS root registry: duplicate root name %q", root.Name)
			}
			if pathsOverlap(existing.Path, root.Path) {
				return nil, fmt.Errorf("create HFS root registry: root paths %q and %q overlap", existing.Path, root.Path)
			}
		}
		roots = append(roots, root)
		byName[root.Name] = root
	}
	for _, root := range roots {
		if root.ReadOnly {
			resolved, err := store.ResolveReadOnlyFileReference(root.Path, "hfs")
			if err != nil {
				return nil, fmt.Errorf("open read-only HFS root %q: %w", root.Name, err)
			}
			info, err := os.Stat(resolved)
			if err != nil || !info.IsDir() {
				return nil, fmt.Errorf("open read-only HFS root %q: path is not a directory", root.Name)
			}
			continue
		}
		if err := store.MkdirAll(root.Path, 0o755); err != nil {
			return nil, fmt.Errorf("create HFS root %q: %w", root.Name, err)
		}
	}
	return &Registry{
		store: store, roots: roots, byName: byName, mutations: make(chan struct{}, DefaultMaxMutations),
	}, nil
}

func (registry *Registry) Lookup(name string) (Root, bool) {
	root, found := registry.byName[name]
	return root, found
}

func (registry *Registry) Roots() []Root {
	return append([]Root(nil), registry.roots...)
}

func (registry *Registry) AcquireMutation(ctx context.Context) (func(), error) {
	select {
	case registry.mutations <- struct{}{}:
		return func() { <-registry.mutations }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (registry *Registry) List() ([]Entry, error) {
	entries := make([]Entry, 0, len(registry.roots))
	for _, root := range registry.roots {
		file, info, err := registry.Open(root, nil)
		if errors.Is(err, storage.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("list HFS root %q: %w", root.Name, err)
		}
		_ = file.Close()
		if !info.IsDir() {
			return nil, fmt.Errorf("list HFS root %q: path is not a directory", root.Name)
		}
		entries = append(entries, Entry{Name: root.Name, IsDirectory: true, LastModified: info.ModTime().UTC()})
	}
	return entries, nil
}

func (registry *Registry) Open(root Root, segments []string) (*os.File, os.FileInfo, error) {
	relative, err := registry.relativePath(root, segments)
	if err != nil {
		return nil, nil, fmt.Errorf("open HFS path: %w", err)
	}
	path, err := registry.resolve(root, relative)
	if err != nil {
		return nil, nil, fmt.Errorf("open HFS path: %w", err)
	}
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, fmt.Errorf("open HFS path: %w", storage.ErrNotFound)
		}
		return nil, nil, fmt.Errorf("open HFS path: %w", err)
	}
	closeOnError := func(openErr error) (*os.File, os.FileInfo, error) {
		_ = file.Close()
		return nil, nil, openErr
	}
	info, err := file.Stat()
	if err != nil {
		return closeOnError(fmt.Errorf("inspect open HFS path: %w", err))
	}
	verifiedPath, err := registry.resolve(root, relative)
	if err != nil {
		return closeOnError(fmt.Errorf("verify open HFS path: %w", err))
	}
	verifiedInfo, err := os.Lstat(verifiedPath)
	if err != nil {
		return closeOnError(fmt.Errorf("verify open HFS path: %w", err))
	}
	if !os.SameFile(info, verifiedInfo) {
		return closeOnError(errors.New("verify open HFS path: file changed while opening"))
	}
	if !info.IsDir() && !info.Mode().IsRegular() {
		return closeOnError(fmt.Errorf("open HFS path: %w", ErrUnsupportedEntry))
	}
	return file, info, nil
}

func (registry *Registry) CreateDirectory(root Root, segments []string) error {
	if root.ReadOnly {
		return ErrReadOnly
	}
	if len(segments) == 0 {
		return storage.ErrExists
	}
	if err := registry.requireDirectory(root, segments[:len(segments)-1]); err != nil {
		return err
	}
	relative, err := registry.relativePath(root, segments)
	if err != nil {
		return err
	}
	path, err := registry.store.Resolve(relative)
	if err != nil {
		return err
	}
	if err := os.Mkdir(path, 0o755); err != nil {
		switch {
		case errors.Is(err, os.ErrExist):
			return storage.ErrExists
		case errors.Is(err, os.ErrNotExist):
			return storage.ErrNotFound
		default:
			return fmt.Errorf("create HFS directory: %w", err)
		}
	}
	return nil
}

func (registry *Registry) CommitFiles(root Root, directorySegments []string, files []UploadFile) error {
	if root.ReadOnly {
		return ErrReadOnly
	}
	if len(files) == 0 {
		return errors.New("commit HFS files: no files")
	}
	if err := registry.requireDirectory(root, directorySegments); err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(files))
	for _, file := range files {
		if file.Staged == nil {
			return errors.New("commit HFS files: staged file is required")
		}
		if err := ValidateSegment(file.Name); err != nil {
			return fmt.Errorf("commit HFS file name: %w", err)
		}
		key := strings.ToLower(file.Name)
		if _, duplicate := seen[key]; duplicate {
			return storage.ErrExists
		}
		seen[key] = struct{}{}
	}

	committed := make([]string, 0, len(files))
	for _, file := range files {
		segments := append(append([]string(nil), directorySegments...), file.Name)
		relative, err := registry.relativePath(root, segments)
		if err == nil {
			_, err = file.Staged.Commit(relative, 0o640)
		}
		if err != nil {
			if rollbackErr := registry.rollbackFiles(committed); rollbackErr != nil {
				return errors.Join(ErrRollbackFailed, err, rollbackErr)
			}
			return err
		}
		committed = append(committed, relative)
	}
	return nil
}

func (registry *Registry) rollbackFiles(relativePaths []string) error {
	var result error
	for index := len(relativePaths) - 1; index >= 0; index-- {
		if err := registry.store.RemoveFile(relativePaths[index]); err != nil {
			result = errors.Join(result, fmt.Errorf("roll back HFS file: %w", err))
		}
	}
	return result
}

func (registry *Registry) requireDirectory(root Root, segments []string) error {
	directory, info, err := registry.Open(root, segments)
	if err != nil {
		return err
	}
	_ = directory.Close()
	if !info.IsDir() {
		return ErrNotDirectory
	}
	return nil
}

func (registry *Registry) relativePath(root Root, segments []string) (string, error) {
	registered, found := registry.Lookup(root.Name)
	if !found || registered != root {
		return "", errors.New("root is not registered")
	}
	for _, segment := range segments {
		if err := ValidateSegment(segment); err != nil {
			return "", err
		}
	}
	relative := root.Path
	if len(segments) > 0 {
		relative += "/" + strings.Join(segments, "/")
	}
	return relative, nil
}

func (registry *Registry) resolve(root Root, value string) (string, error) {
	if root.ReadOnly {
		return registry.store.ResolveReadOnlyFileReference(filepath.FromSlash(value), "hfs")
	}
	return registry.store.ResolveExisting(value)
}

func ReadDirectory(directory *os.File) ([]Entry, error) {
	return readDirectory(directory, DefaultMaxDirectoryEntries)
}

func readDirectory(directory *os.File, maxEntries int) ([]Entry, error) {
	if maxEntries <= 0 {
		return nil, ErrDirectoryTooLarge
	}
	children, err := directory.ReadDir(maxEntries + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("read HFS directory: %w", err)
	}
	if len(children) > maxEntries {
		return nil, ErrDirectoryTooLarge
	}
	entries := make([]Entry, 0, len(children))
	for _, child := range children {
		if err := ValidateSegment(child.Name()); err != nil || child.Type()&os.ModeSymlink != 0 {
			continue
		}
		info, err := child.Info()
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("inspect HFS directory entry: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
			continue
		}
		entry := Entry{Name: child.Name(), IsDirectory: info.IsDir(), LastModified: info.ModTime().UTC()}
		if info.Mode().IsRegular() {
			entry.Size = info.Size()
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func ValidateSegment(segment string) error {
	if segment == "" || segment == "." || segment == ".." || !utf8.ValidString(segment) {
		return storage.ErrInvalidPath
	}
	for _, character := range segment {
		if character == '/' || character == '\\' || character == ':' || unicode.IsControl(character) {
			return storage.ErrInvalidPath
		}
	}
	return nil
}

func validateRoot(candidate config.HFSRoot) (Root, error) {
	if len(candidate.Name) == 0 || len(candidate.Name) > maxRootNameLength || candidate.Name == "." || candidate.Name == ".." {
		return Root{}, fmt.Errorf("name must be 1-%d URL-safe characters", maxRootNameLength)
	}
	for _, character := range candidate.Name {
		if !isRootNameCharacter(character) {
			return Root{}, errors.New("name contains an invalid character")
		}
	}
	if candidate.ReadOnly && filepath.IsAbs(strings.ReplaceAll(candidate.Path, `\`, string(filepath.Separator))) {
		return Root{Name: candidate.Name, Path: filepath.Clean(candidate.Path), Public: candidate.Public, ReadOnly: true}, nil
	}
	if candidate.ReadOnly {
		return Root{}, errors.New("read-only path must be absolute")
	}
	if strings.Contains(candidate.Path, `\`) {
		return Root{}, errors.New("path must use forward slashes")
	}
	parts := strings.Split(candidate.Path, "/")
	if len(parts) < 2 || parts[0] != "hfs" {
		return Root{}, errors.New("path must be below hfs/")
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || strings.ContainsAny(part, ":\x00") {
			return Root{}, errors.New("path contains a non-canonical component")
		}
	}
	return Root{Name: candidate.Name, Path: candidate.Path, Public: candidate.Public}, nil
}

func isRootNameCharacter(character rune) bool {
	return character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
		character >= '0' && character <= '9' || strings.ContainsRune("._@-", character)
}

func pathsOverlap(left, right string) bool {
	left = strings.ToLower(filepath.ToSlash(filepath.Clean(left)))
	right = strings.ToLower(filepath.ToSlash(filepath.Clean(right)))
	return left == right || strings.HasPrefix(left, right+"/") || strings.HasPrefix(right, left+"/")
}
