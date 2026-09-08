package hfs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kaven.xyz/kaven/kaven-media-server/internal/config"
	"kaven.xyz/kaven/kaven-media-server/internal/storage"
)

func TestRegistryCreatesListsAndLooksUpRoots(t *testing.T) {
	store, err := storage.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	registry, err := NewRegistry(store, []config.HFSRoot{
		{Name: "public", Path: "hfs/public", Public: true},
		{Name: "private", Path: "hfs/private"},
	})
	if err != nil {
		t.Fatal(err)
	}
	modified := time.Date(2026, time.September, 4, 1, 2, 3, 4_000_000, time.UTC)
	for _, path := range []string{"hfs/public", "hfs/private"} {
		resolved, err := store.ResolveExisting(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(resolved, modified, modified); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := registry.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Name != "public" || !entries[0].LastModified.Equal(modified) || entries[1].Name != "private" {
		t.Fatalf("entries = %#v", entries)
	}
	public, found := registry.Lookup("public")
	if !found || !public.AllowsRead(false) || public.AllowsWrite(false) || !public.AllowsWrite(true) {
		t.Fatalf("public root policy = %#v, found = %t", public, found)
	}
	private, found := registry.Lookup("private")
	if !found || private.AllowsRead(false) || !private.AllowsRead(true) {
		t.Fatalf("private root policy = %#v, found = %t", private, found)
	}
}

func TestRegistryBoundsConcurrentMutations(t *testing.T) {
	store, err := storage.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	registry, err := NewRegistry(store, nil)
	if err != nil {
		t.Fatal(err)
	}
	first, err := registry.AcquireMutation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer first()
	second, err := registry.AcquireMutation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer second()
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if release, err := registry.AcquireMutation(cancelled); !errors.Is(err, context.Canceled) || release != nil {
		t.Fatalf("third acquisition returned release = %t, error = %v; want cancellation", release != nil, err)
	}
}

func TestRegistryOpensContainedFilesAndListsSafeEntries(t *testing.T) {
	store, err := storage.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	registry, err := NewRegistry(store, []config.HFSRoot{{Name: "shared", Path: "hfs/shared", Public: true}})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MkdirAll("hfs/shared/folder", 0o755); err != nil {
		t.Fatal(err)
	}
	filePath, err := store.Resolve("hfs/shared/hello world.txt")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filePath, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, _ := registry.Lookup("shared")
	directory, info, err := registry.Open(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()
	if !info.IsDir() {
		t.Fatal("root did not open as a directory")
	}
	entries, err := ReadDirectory(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Name != "folder" || !entries[0].IsDirectory ||
		entries[1].Name != "hello world.txt" || entries[1].IsDirectory || entries[1].Size != 5 {
		t.Fatalf("entries = %#v", entries)
	}
	file, info, err := registry.Open(root, []string{"hello world.txt"})
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if !info.Mode().IsRegular() {
		t.Fatal("file did not open as a regular file")
	}
}

func TestRegistryRejectsUnsafeSegmentsAndRequestedSymlinks(t *testing.T) {
	store, err := storage.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	registry, err := NewRegistry(store, []config.HFSRoot{{Name: "shared", Path: "hfs/shared", Public: true}})
	if err != nil {
		t.Fatal(err)
	}
	root, _ := registry.Lookup("shared")
	for _, segment := range []string{"", ".", "..", `..\\escape`, "bad:name", "line\nbreak", string([]byte{0xff})} {
		if _, _, err := registry.Open(root, []string{segment}); !errors.Is(err, storage.ErrInvalidPath) {
			t.Errorf("segment %q error = %v, want invalid path", segment, err)
		}
	}
	target := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(target, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	link, err := store.Resolve("hfs/shared/link.txt")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, _, err := registry.Open(root, []string{"link.txt"}); !errors.Is(err, storage.ErrPathEscape) {
		t.Fatalf("symlink error = %v, want path escape", err)
	}
	directory, _, err := registry.Open(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()
	entries, err := ReadDirectory(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.EqualFold(entry.Name, "link.txt") {
			t.Fatal("symlink was included in directory listing")
		}
	}
}

func TestReadDirectoryEnforcesEntryLimit(t *testing.T) {
	store, err := storage.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	registry, err := NewRegistry(store, []config.HFSRoot{{Name: "shared", Path: "hfs/shared", Public: true}})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"one", "two", "three"} {
		path, err := store.Resolve("hfs/shared/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	root, _ := registry.Lookup("shared")
	directory, _, err := registry.Open(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()
	if _, err := readDirectory(directory, 2); !errors.Is(err, ErrDirectoryTooLarge) {
		t.Fatalf("error = %v, want directory too large", err)
	}
}

func TestRegistryCreatesOneDirectoryWithoutOverwriting(t *testing.T) {
	store, err := storage.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	registry, err := NewRegistry(store, []config.HFSRoot{{Name: "shared", Path: "hfs/shared"}})
	if err != nil {
		t.Fatal(err)
	}
	root, _ := registry.Lookup("shared")
	if err := registry.CreateDirectory(root, []string{"folder"}); err != nil {
		t.Fatal(err)
	}
	path, err := store.ResolveExisting("hfs/shared/folder")
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		t.Fatalf("created path is not a directory: %v", err)
	}
	if err := registry.CreateDirectory(root, []string{"folder"}); !errors.Is(err, storage.ErrExists) {
		t.Fatalf("duplicate error = %v, want exists", err)
	}
	if err := registry.CreateDirectory(root, []string{"missing", "nested"}); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("missing parent error = %v, want not found", err)
	}
}

func TestRegistryCommitsFilesAndRollsBackBatchOnConflict(t *testing.T) {
	store, err := storage.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	registry, err := NewRegistry(store, []config.HFSRoot{{Name: "shared", Path: "hfs/shared"}})
	if err != nil {
		t.Fatal(err)
	}
	root, _ := registry.Lookup("shared")
	stage := func(contents string) *storage.StagedFile {
		file, err := store.Stage(context.Background(), strings.NewReader(contents), 100)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = file.Abort() })
		return file
	}
	if err := registry.CommitFiles(root, nil, []UploadFile{{Name: "one.txt", Staged: stage("one")}}); err != nil {
		t.Fatal(err)
	}
	one, err := os.ReadFile(filepath.Join(store.Root(), "hfs", "shared", "one.txt"))
	if err != nil || string(one) != "one" {
		t.Fatalf("stored file = %q, error = %v", one, err)
	}

	existing := filepath.Join(store.Root(), "hfs", "shared", "existing.txt")
	if err := os.WriteFile(existing, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	err = registry.CommitFiles(root, nil, []UploadFile{
		{Name: "created.txt", Staged: stage("created")},
		{Name: "existing.txt", Staged: stage("replacement")},
	})
	if !errors.Is(err, storage.ErrExists) {
		t.Fatalf("conflict error = %v, want exists", err)
	}
	if _, err := os.Stat(filepath.Join(store.Root(), "hfs", "shared", "created.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rolled-back file still exists: %v", err)
	}
	contents, err := os.ReadFile(existing)
	if err != nil || string(contents) != "original" {
		t.Fatalf("existing file = %q, error = %v", contents, err)
	}
}

func TestRegistryConcurrentCommitsPublishExactlyOneFile(t *testing.T) {
	store, err := storage.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	registry, err := NewRegistry(store, []config.HFSRoot{{Name: "shared", Path: "hfs/shared"}})
	if err != nil {
		t.Fatal(err)
	}
	root, _ := registry.Lookup("shared")
	type uploadCandidate struct {
		contents string
		staged   *storage.StagedFile
	}
	candidates := []uploadCandidate{{contents: "left"}, {contents: "right"}}
	for index := range candidates {
		candidates[index].staged, err = store.Stage(context.Background(), strings.NewReader(candidates[index].contents), 100)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = candidates[index].staged.Abort() })
	}
	type result struct {
		contents string
		err      error
	}
	start := make(chan struct{})
	results := make(chan result, len(candidates))
	for _, candidate := range candidates {
		go func(candidate uploadCandidate) {
			<-start
			results <- result{
				contents: candidate.contents,
				err:      registry.CommitFiles(root, nil, []UploadFile{{Name: "same.txt", Staged: candidate.staged}}),
			}
		}(candidate)
	}
	close(start)
	first, second := <-results, <-results
	if (first.err == nil) == (second.err == nil) {
		t.Fatalf("commit errors = %v, %v; want exactly one success", first.err, second.err)
	}
	failed, winner := first, second
	if first.err == nil {
		failed, winner = second, first
	}
	if !errors.Is(failed.err, storage.ErrExists) {
		t.Fatalf("losing commit error = %v, want exists", failed.err)
	}
	contents, err := os.ReadFile(filepath.Join(store.Root(), "hfs", "shared", "same.txt"))
	if err != nil || string(contents) != winner.contents {
		t.Fatalf("published file = %q, error = %v; want %q", contents, err, winner.contents)
	}
}

func TestRegistryMutationsRejectSymlinkedDirectory(t *testing.T) {
	store, err := storage.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	registry, err := NewRegistry(store, []config.HFSRoot{{Name: "shared", Path: "hfs/shared"}})
	if err != nil {
		t.Fatal(err)
	}
	root, _ := registry.Lookup("shared")
	outside := t.TempDir()
	link, err := store.Resolve("hfs/shared/escape")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := registry.CreateDirectory(root, []string{"escape", "directory"}); !errors.Is(err, storage.ErrPathEscape) {
		t.Fatalf("create through symlink error = %v, want path escape", err)
	}
	staged, err := store.Stage(context.Background(), strings.NewReader("outside"), 100)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = staged.Abort() })
	if err := registry.CommitFiles(root, []string{"escape"}, []UploadFile{{Name: "file.txt", Staged: staged}}); !errors.Is(err, storage.ErrPathEscape) {
		t.Fatalf("upload through symlink error = %v, want path escape", err)
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatalf("outside entries = %#v, error = %v", entries, err)
	}
}

func TestRegistryRejectsUnsafeOrAmbiguousRootsBeforeCreatingDirectories(t *testing.T) {
	tests := []struct {
		name  string
		roots []config.HFSRoot
	}{
		{name: "invalid name", roots: []config.HFSRoot{{Name: "bad/name", Path: "hfs/one"}}},
		{name: "traversal", roots: []config.HFSRoot{{Name: "one", Path: "hfs/../one"}}},
		{name: "outside HFS", roots: []config.HFSRoot{{Name: "one", Path: "images"}}},
		{name: "backslashes", roots: []config.HFSRoot{{Name: "one", Path: `hfs\one`}}},
		{name: "writable absolute", roots: []config.HFSRoot{{Name: "one", Path: filepath.Join(t.TempDir(), "root")}}},
		{name: "relative read-only", roots: []config.HFSRoot{{Name: "one", Path: "hfs/one", ReadOnly: true}}},
		{name: "duplicate name", roots: []config.HFSRoot{{Name: "One", Path: "hfs/one"}, {Name: "one", Path: "hfs/two"}}},
		{name: "overlapping paths", roots: []config.HFSRoot{{Name: "one", Path: "hfs/one"}, {Name: "two", Path: "hfs/one/nested"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dataDir := t.TempDir()
			store, err := storage.New(dataDir)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := NewRegistry(store, test.roots); err == nil {
				t.Fatal("unsafe roots succeeded")
			}
			if _, err := os.Stat(filepath.Join(dataDir, "hfs")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("validation created HFS directory: %v", err)
			}
		})
	}
}

func TestRegistryRejectsSymlinkedRootPath(t *testing.T) {
	dataDir := t.TempDir()
	store, err := storage.New(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	if err := os.Mkdir(filepath.Join(dataDir, "hfs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dataDir, "hfs", "public")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	_, err = NewRegistry(store, []config.HFSRoot{{Name: "public", Path: "hfs/public", Public: true}})
	if !errors.Is(err, storage.ErrPathEscape) {
		t.Fatalf("error = %v, want path escape", err)
	}
}
