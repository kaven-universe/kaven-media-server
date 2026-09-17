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
	publicPath := createTestRoot(t, store, "hfs/public")
	privatePath := createTestRoot(t, store, "hfs/private")
	registry, err := NewRegistry(store, []config.HFSRoot{
		{Name: "public", Path: publicPath, Public: true},
		{Name: "private", Path: privatePath},
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
	sharedPath := createTestRoot(t, store, "hfs/shared")
	registry, err := NewRegistry(store, []config.HFSRoot{{Name: "shared", Path: sharedPath, Public: true}})
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
	sharedPath := createTestRoot(t, store, "hfs/shared")
	registry, err := NewRegistry(store, []config.HFSRoot{{Name: "shared", Path: sharedPath, Public: true}})
	if err != nil {
		t.Fatal(err)
	}
	root, _ := registry.Lookup("shared")
	for _, segment := range []string{"", ".", "..", ".kaven-upload-incomplete", `..\\escape`, "bad:name", "line\nbreak", string([]byte{0xff})} {
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
	sharedPath := createTestRoot(t, store, "hfs/shared")
	registry, err := NewRegistry(store, []config.HFSRoot{{Name: "shared", Path: sharedPath, Public: true}})
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
	sharedPath := createTestRoot(t, store, "hfs/shared")
	registry, err := NewRegistry(store, []config.HFSRoot{{Name: "shared", Path: sharedPath}})
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
	sharedPath := createTestRoot(t, store, "hfs/shared")
	registry, err := NewRegistry(store, []config.HFSRoot{{Name: "shared", Path: sharedPath}})
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
	sharedPath := createTestRoot(t, store, "hfs/shared")
	registry, err := NewRegistry(store, []config.HFSRoot{{Name: "shared", Path: sharedPath}})
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
	sharedPath := createTestRoot(t, store, "hfs/shared")
	registry, err := NewRegistry(store, []config.HFSRoot{{Name: "shared", Path: sharedPath}})
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
		roots func(string) []config.HFSRoot
	}{
		{name: "invalid name", roots: func(dataDir string) []config.HFSRoot {
			return []config.HFSRoot{{Name: "bad/name", Path: dataDir}}
		}},
		{name: "relative traversal", roots: func(string) []config.HFSRoot {
			return []config.HFSRoot{{Name: "one", Path: "../one"}}
		}},
		{name: "non-canonical relative path", roots: func(string) []config.HFSRoot {
			return []config.HFSRoot{{Name: "one", Path: "one//nested"}}
		}},
		{name: "filesystem root", roots: func(dataDir string) []config.HFSRoot {
			return []config.HFSRoot{{Name: "one", Path: filepath.VolumeName(dataDir) + string(filepath.Separator)}}
		}},
		{name: "duplicate name", roots: func(dataDir string) []config.HFSRoot {
			return []config.HFSRoot{{Name: "One", Path: filepath.Join(dataDir, "one")}, {Name: "one", Path: filepath.Join(dataDir, "two")}}
		}},
		{name: "overlapping paths", roots: func(dataDir string) []config.HFSRoot {
			return []config.HFSRoot{{Name: "one", Path: filepath.Join(dataDir, "one")}, {Name: "two", Path: filepath.Join(dataDir, "one", "nested")}}
		}},
		{name: "private backup root", roots: func(dataDir string) []config.HFSRoot {
			return []config.HFSRoot{{Name: "backups", Path: filepath.Join(dataDir, "backup"), ReadOnly: true}}
		}},
		{name: "private temporary root", roots: func(dataDir string) []config.HFSRoot {
			return []config.HFSRoot{{Name: "temporary", Path: filepath.Join(dataDir, "tmp")}}
		}},
		{name: "private log root", roots: func(dataDir string) []config.HFSRoot {
			return []config.HFSRoot{{Name: "logs", Path: filepath.Join(dataDir, "logs"), ReadOnly: true}}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dataDir := t.TempDir()
			store, err := storage.New(dataDir)
			if err != nil {
				t.Fatal(err)
			}
			for _, directory := range []string{"one/nested", "two", "backup", "logs"} {
				if err := store.MkdirAll(directory, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := NewRegistry(store, test.roots(dataDir)); err == nil {
				t.Fatal("unsafe roots succeeded")
			}
		})
	}
}

func TestRegistryMapsVirtualRootToAnyAbsoluteDirectory(t *testing.T) {
	dataDir := t.TempDir()
	store, err := storage.New(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MkdirAll("upload/201810", 0o755); err != nil {
		t.Fatal(err)
	}
	absoluteRoot := filepath.Join(dataDir, "upload")
	registry, err := NewRegistry(store, []config.HFSRoot{{Name: "uploaded", Path: absoluteRoot}})
	if err != nil {
		t.Fatal(err)
	}
	root, found := registry.Lookup("uploaded")
	if !found || root.Path != absoluteRoot || !root.AllowsWrite(true) {
		t.Fatalf("virtual root = %+v, found = %v", root, found)
	}
	if err := registry.CreateDirectory(root, []string{"new"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ResolveExisting("upload/new"); err != nil {
		t.Fatalf("mapped directory was not created: %v", err)
	}
}

func TestRegistryMapsRelativeRootWithinDataDirectory(t *testing.T) {
	dataDir := t.TempDir()
	store, err := storage.New(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MkdirAll("shared/files", 0o755); err != nil {
		t.Fatal(err)
	}
	registry, err := NewRegistry(store, []config.HFSRoot{{Name: "shared", Path: "shared"}})
	if err != nil {
		t.Fatal(err)
	}
	root, found := registry.Lookup("shared")
	if !found || root.Path != filepath.Join(dataDir, "shared") {
		t.Fatalf("relative virtual root = %+v, found = %v", root, found)
	}
	if err := registry.CreateDirectory(root, []string{"new"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ResolveExisting("shared/new"); err != nil {
		t.Fatalf("relative mapped directory was not created: %v", err)
	}
}

func TestReadOnlyPolicyIsIndependentOfAbsoluteRootLocation(t *testing.T) {
	dataDir := t.TempDir()
	store, err := storage.New(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	createTestRoot(t, store, "managed")
	external := t.TempDir()
	registry, err := NewRegistry(store, []config.HFSRoot{
		{Name: "managed", Path: "managed", ReadOnly: true},
		{Name: "external", Path: external},
	})
	if err != nil {
		t.Fatal(err)
	}
	managedRoot, _ := registry.Lookup("managed")
	externalRoot, _ := registry.Lookup("external")
	if managedRoot.AllowsWrite(true) || !externalRoot.AllowsWrite(true) {
		t.Fatalf("root policies = managed %+v, external %+v", managedRoot, externalRoot)
	}
	if err := registry.CreateDirectory(managedRoot, []string{"blocked"}); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("read-only creation error = %v", err)
	}
	if err := registry.CreateDirectory(externalRoot, []string{"allowed"}); err != nil {
		t.Fatalf("writable external creation: %v", err)
	}
}

func TestRuntimeRegistrySkipsMissingRootsRegardlessOfWritePolicy(t *testing.T) {
	dataDir := t.TempDir()
	store, err := storage.New(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(t.TempDir(), "missing")
	unavailable := make([]config.HFSRoot, 0, 3)
	uploaded := createTestRoot(t, store, "upload")
	registry, err := NewRegistryWithOptions(store, []config.HFSRoot{
		{Name: "uploaded", Path: uploaded},
		{Name: "writable", Path: missing},
		{Name: "read-only", Path: missing + "-readonly", ReadOnly: true},
		{Name: "relative", Path: "missing-relative"},
	}, RegistryOptions{
		SkipUnavailableRoots: true,
		OnUnavailableRoot: func(root config.HFSRoot, err error) {
			if !errors.Is(err, storage.ErrNotFound) {
				t.Errorf("unavailable error = %v, want storage.ErrNotFound", err)
			}
			unavailable = append(unavailable, root)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(unavailable) != 3 || unavailable[0].Name != "writable" || unavailable[1].Name != "read-only" || unavailable[2].Name != "relative" {
		t.Fatalf("unavailable roots = %+v", unavailable)
	}
	if _, found := registry.Lookup("writable"); found {
		t.Fatal("missing writable root remained active")
	}
	if _, found := registry.Lookup("read-only"); found {
		t.Fatal("missing read-only root remained active")
	}
	if _, found := registry.Lookup("relative"); found {
		t.Fatal("missing relative root remained active")
	}
	if _, found := registry.Lookup("uploaded"); !found {
		t.Fatal("available managed root was removed")
	}
	if _, err := NewRegistry(store, []config.HFSRoot{{Name: "external", Path: missing}}); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("strict registry error = %v, want storage.ErrNotFound", err)
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
	for name, rootPath := range map[string]string{
		"absolute": filepath.Join(dataDir, "hfs", "public"),
		"relative": "hfs/public",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := NewRegistry(store, []config.HFSRoot{{Name: "public", Path: rootPath, Public: true}})
			if !errors.Is(err, storage.ErrPathEscape) {
				t.Fatalf("error = %v, want path escape", err)
			}
		})
	}
}

func createTestRoot(t *testing.T, store *storage.Store, relative string) string {
	t.Helper()
	if err := store.MkdirAll(relative, 0o755); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(store.Root(), filepath.FromSlash(relative))
}
