package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestResolve(t *testing.T) {
	store := newTestStore(t)
	if err := store.MkdirAll("images/2026", 0o755); err != nil {
		t.Fatalf("create directory: %v", err)
	}

	valid := []string{
		"images/file.jpg",
		"images/2026/file.jpg",
		`images\2026\file.jpg`,
		"文件/照片.jpg",
	}
	for _, value := range valid {
		t.Run("valid "+value, func(t *testing.T) {
			resolved, err := store.Resolve(value)
			if err != nil {
				t.Fatalf("Resolve(%q): %v", value, err)
			}
			relative, err := filepath.Rel(store.Root(), resolved)
			if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				t.Fatalf("resolved path %q escapes root %q", resolved, store.Root())
			}
		})
	}

	tests := []struct {
		path string
		err  error
	}{
		{path: "", err: ErrInvalidPath},
		{path: ".", err: ErrInvalidPath},
		{path: "images//file.jpg", err: ErrInvalidPath},
		{path: "images/./file.jpg", err: ErrInvalidPath},
		{path: "../secret", err: ErrPathEscape},
		{path: "images/../../secret", err: ErrPathEscape},
		{path: `images\..\secret`, err: ErrPathEscape},
		{path: "/absolute/path", err: ErrPathEscape},
		{path: `C:\absolute\path`, err: ErrPathEscape},
		{path: `\\server\share\file`, err: ErrPathEscape},
		{path: "file.txt:stream", err: ErrInvalidPath},
		{path: "file\x00.txt", err: ErrInvalidPath},
	}
	for _, test := range tests {
		t.Run("invalid "+test.path, func(t *testing.T) {
			if _, err := store.Resolve(test.path); !errors.Is(err, test.err) {
				t.Fatalf("Resolve(%q) error = %v, want %v", test.path, err, test.err)
			}
		})
	}
}

func TestOpenDoesNotCreateMissingRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing")
	if _, err := Open(root); err == nil {
		t.Fatal("Open succeeded for a missing root")
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Open changed missing root: %v", err)
	}
}

func TestResolveRejectsSymlinkDescendants(t *testing.T) {
	store := newTestStore(t)
	outside := t.TempDir()
	link := filepath.Join(store.Root(), "escape")
	if err := os.Symlink(outside, link); err != nil {
		if runtime.GOOS == "windows" || errors.Is(err, os.ErrPermission) {
			t.Skipf("symlink creation is unavailable: %v", err)
		}
		t.Fatalf("create symlink: %v", err)
	}
	if _, err := store.Resolve("escape/file.txt"); !errors.Is(err, ErrPathEscape) {
		t.Fatalf("Resolve through symlink error = %v, want ErrPathEscape", err)
	}
	if err := store.MkdirAll("escape/nested", 0o755); !errors.Is(err, ErrPathEscape) {
		t.Fatalf("MkdirAll through symlink error = %v, want ErrPathEscape", err)
	}
}

func TestResolveExisting(t *testing.T) {
	store := newTestStore(t)
	if _, err := store.ResolveExisting("missing.txt"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing path error = %v, want ErrNotFound", err)
	}
	if err := os.WriteFile(filepath.Join(store.Root(), "exists.txt"), []byte("ok"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	resolved, err := store.ResolveExisting("exists.txt")
	if err != nil {
		t.Fatalf("resolve existing file: %v", err)
	}
	if resolved != filepath.Join(store.Root(), "exists.txt") {
		t.Fatalf("resolved path = %q", resolved)
	}
}

func TestResolveManagedFileReference(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MkdirAll("images", 0o755); err != nil {
		t.Fatal(err)
	}
	managed := filepath.Join(store.Root(), "images", "managed.jpg")
	if err := os.WriteFile(managed, []byte("managed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := store.ResolveManagedFileReference("images/managed.jpg", "images"); err != nil || got != managed {
		t.Fatalf("managed reference = %q, %v", got, err)
	}
	if _, err := store.ResolveManagedFileReference("bing/managed.jpg", "images"); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("wrong managed prefix error = %v", err)
	}

	external := filepath.Join(t.TempDir(), "external.jpg")
	if err := os.WriteFile(external, []byte("external"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ResolveManagedFileReference(external, "images"); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("absolute managed reference error = %v", err)
	}
}

func TestResolveReadOnlyFileReferenceSupportsAbsoluteFiles(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(t.TempDir(), "external.jpg")
	if err := os.WriteFile(external, []byte("external"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := store.ResolveReadOnlyFileReference(external, "hfs"); err != nil || got != external {
		t.Fatalf("absolute reference = %q, %v", got, err)
	}
	missing := filepath.Join(filepath.Dir(external), "missing.jpg")
	if _, err := store.ResolveReadOnlyFileReference(missing, "hfs"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing absolute reference error = %v", err)
	}

	link := filepath.Join(t.TempDir(), "external-link.jpg")
	if err := os.Symlink(external, link); err == nil {
		if _, err := store.ResolveReadOnlyFileReference(link, "hfs"); !errors.Is(err, ErrPathEscape) {
			t.Fatalf("absolute symlink error = %v", err)
		}
	}
}

func TestRemoveFile(t *testing.T) {
	store := newTestStore(t)
	path := filepath.Join(store.Root(), "remove.txt")
	if err := os.WriteFile(path, []byte("remove"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := store.RemoveFile("remove.txt"); err != nil {
		t.Fatalf("remove file: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("removed file still exists: %v", err)
	}
	if err := store.RemoveFile("remove.txt"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("remove missing error = %v, want ErrNotFound", err)
	}
	if err := store.RemoveFile("tmp"); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("remove directory error = %v, want ErrInvalidPath", err)
	}
}

func TestStageAndCommit(t *testing.T) {
	store := newTestStore(t)
	if err := store.MkdirAll("images/2026", 0o755); err != nil {
		t.Fatalf("create destination: %v", err)
	}
	staged, err := store.Stage(context.Background(), strings.NewReader("complete contents"), 100)
	if err != nil {
		t.Fatalf("stage file: %v", err)
	}
	if staged.Size() != int64(len("complete contents")) {
		t.Fatalf("staged size = %d", staged.Size())
	}
	if _, err := os.Stat(staged.Path()); err != nil {
		t.Fatalf("stat staged file: %v", err)
	}

	destination, err := staged.Commit("images/2026/result.txt", 0o640)
	if err != nil {
		t.Fatalf("commit file: %v", err)
	}
	contents, err := os.ReadFile(destination)
	if err != nil {
		t.Fatalf("read committed file: %v", err)
	}
	if string(contents) != "complete contents" {
		t.Fatalf("committed contents = %q", contents)
	}
	if _, err := os.Stat(staged.Path()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staged path remains after commit: %v", err)
	}
	if _, err := staged.Commit("images/2026/again.txt", 0o640); !errors.Is(err, ErrAlreadyFinalized) {
		t.Fatalf("second commit error = %v, want ErrAlreadyFinalized", err)
	}
	if err := staged.Abort(); err != nil {
		t.Fatalf("abort after commit: %v", err)
	}
}

func TestWriteAtomicValidatesAndDoesNotOverwrite(t *testing.T) {
	store := newTestStore(t)
	if err := store.MkdirAll("hfs/uploaded", 0o755); err != nil {
		t.Fatalf("create destination: %v", err)
	}
	validationCalled := false
	result, err := store.WriteAtomic(
		context.Background(),
		"hfs/uploaded/file.txt",
		strings.NewReader("new"),
		10,
		0o640,
		func(path string, size int64) error {
			validationCalled = true
			contents, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			if size != 3 || string(contents) != "new" {
				return errors.New("unexpected staged contents")
			}
			return nil
		},
	)
	if err != nil {
		t.Fatalf("write file: %v", err)
	}
	if !validationCalled || result.Size != 3 {
		t.Fatalf("validation called = %v, result = %#v", validationCalled, result)
	}

	_, err = store.WriteAtomic(context.Background(), "hfs/uploaded/file.txt", strings.NewReader("replacement"), 20, 0o640, nil)
	if !errors.Is(err, ErrExists) {
		t.Fatalf("overwrite error = %v, want ErrExists", err)
	}
	contents, err := os.ReadFile(result.Path)
	if err != nil {
		t.Fatalf("read destination: %v", err)
	}
	if string(contents) != "new" {
		t.Fatalf("existing file was replaced with %q", contents)
	}
	assertTemporaryDirectoryEmpty(t, store)
}

func TestWriteAtomicCleansUpFailures(t *testing.T) {
	tests := []struct {
		name     string
		ctx      func() context.Context
		source   io.Reader
		limit    int64
		validate ValidateFunc
		want     error
	}{
		{
			name: "too large", ctx: context.Background,
			source: strings.NewReader("12345"), limit: 4, want: ErrTooLarge,
		},
		{
			name: "invalid limit", ctx: context.Background,
			source: strings.NewReader("x"), limit: 0, want: ErrInvalidLimit,
		},
		{
			name: "source error", ctx: context.Background,
			source: errorReader{}, limit: 10, want: errFixture,
		},
		{
			name: "validation error", ctx: context.Background,
			source: strings.NewReader("valid bytes"), limit: 20,
			validate: func(string, int64) error { return errFixture }, want: errFixture,
		},
		{
			name: "canceled", ctx: canceledContext,
			source: strings.NewReader("x"), limit: 10, want: context.Canceled,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newTestStore(t)
			_, err := store.WriteAtomic(test.ctx(), "result.txt", test.source, test.limit, 0o600, test.validate)
			if !errors.Is(err, test.want) {
				t.Fatalf("WriteAtomic error = %v, want %v", err, test.want)
			}
			if _, err := os.Stat(filepath.Join(store.Root(), "result.txt")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("destination exists after failure: %v", err)
			}
			assertTemporaryDirectoryEmpty(t, store)
		})
	}
}

func TestConcurrentWritesPublishExactlyOnce(t *testing.T) {
	store := newTestStore(t)
	const writers = 8
	start := make(chan struct{})
	errorsChannel := make(chan error, writers)
	var wait sync.WaitGroup
	for index := 0; index < writers; index++ {
		wait.Add(1)
		go func(value byte) {
			defer wait.Done()
			<-start
			_, err := store.WriteAtomic(context.Background(), "winner.txt", bytes.NewReader([]byte{value}), 1, 0o600, nil)
			errorsChannel <- err
		}(byte(index))
	}
	close(start)
	wait.Wait()
	close(errorsChannel)

	successes, conflicts := 0, 0
	for err := range errorsChannel {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrExists):
			conflicts++
		default:
			t.Errorf("unexpected write error: %v", err)
		}
	}
	if successes != 1 || conflicts != writers-1 {
		t.Fatalf("successes = %d, conflicts = %d", successes, conflicts)
	}
	contents, err := os.ReadFile(filepath.Join(store.Root(), "winner.txt"))
	if err != nil || len(contents) != 1 || contents[0] >= writers {
		t.Fatalf("published contents = %v, error = %v", contents, err)
	}
	assertTemporaryDirectoryEmpty(t, store)
}

var errFixture = errors.New("fixture error")

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) {
	return 0, errFixture
}

func canceledContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	return store
}

func assertTemporaryDirectoryEmpty(t *testing.T, store *Store) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(store.Root(), "tmp"))
	if err != nil {
		t.Fatalf("read temporary directory: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("temporary directory contains %d entries", len(entries))
	}
}
