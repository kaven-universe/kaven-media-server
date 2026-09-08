package backup

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"kaven.xyz/kaven/kaven-media-server/internal/buildinfo"
	"kaven.xyz/kaven/kaven-media-server/internal/database"
	"kaven.xyz/kaven/kaven-media-server/internal/datalock"
	"kaven.xyz/kaven/kaven-media-server/internal/repository"
)

func fixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, name := range []string{"images", "cache", "bing", "hfs/empty", "tmp"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	db, err := database.Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	data := []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="1" height="1"/>`)
	sum := sha1.Sum(data)
	now := time.Date(2026, 9, 7, 1, 2, 3, 0, time.UTC)
	if err := repository.NewImageRepository(db).Create(context.Background(), repository.Image{
		ID: "0123456789abcdef01234567", UUID: "0123456789abcdef0123456789abcdef", SHA1: hex.EncodeToString(sum[:]),
		Folder: "images", Name: "pixel.svg", OriginalName: "pixel.svg", MIMEType: "image/svg+xml", Size: int64(len(data)),
		UploadDate: now, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string][]byte{"images/pixel.svg": data, "hfs/notes.txt": []byte("preserve these bytes\n"), "tmp/incomplete": []byte("skip me")} {
		if err := os.WriteFile(filepath.Join(root, name), value, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chtimes(filepath.Join(root, "hfs/notes.txt"), now, now); err != nil {
		t.Fatal(err)
	}
	// Leave a stable connection open to retain committed WAL content, as after
	// an interrupted process. Backup must copy it without modifying its bytes.
	return root
}

func TestRoundTripPreservesWALDataAndFiles(t *testing.T) {
	source := fixture(t)
	before := map[string][]byte{}
	for _, name := range []string{dbName, dbName + "-wal", dbName + "-shm"} {
		data, err := os.ReadFile(filepath.Join(source, name))
		if err != nil {
			t.Fatal(err)
		}
		before[name] = data
	}
	parent := t.TempDir()
	snapshot, restored := filepath.Join(parent, "snapshot"), filepath.Join(parent, "restored")
	report, err := Create(context.Background(), source, snapshot, DefaultMaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	if report.Files != 3 || report.ManifestVersion != 2 || report.Producer == nil || report.Producer.GoVersion == "" {
		t.Fatalf("backup report = %+v", report)
	}
	for name, want := range before {
		got, err := os.ReadFile(filepath.Join(source, name))
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("source %s changed: %v", name, err)
		}
	}
	for _, name := range []string{"data/tmp", "data/" + dbName + "-wal", "data/" + datalock.Filename} {
		if _, err := os.Lstat(filepath.Join(snapshot, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("excluded entry exists: %s", name)
		}
	}
	restoreReport, err := Restore(context.Background(), snapshot, restored, DefaultMaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	if restoreReport.ManifestVersion != 2 || restoreReport.Producer == nil || restoreReport.Producer.Revision != report.Producer.Revision {
		t.Fatalf("restore report = %+v", restoreReport)
	}
	for _, name := range []string{"images/pixel.svg", "hfs/notes.txt"} {
		want, _ := os.ReadFile(filepath.Join(source, name))
		got, err := os.ReadFile(filepath.Join(restored, name))
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("restored %s differs: %v", name, err)
		}
	}
	wantInfo, _ := os.Stat(filepath.Join(source, "hfs/notes.txt"))
	gotInfo, _ := os.Stat(filepath.Join(restored, "hfs/notes.txt"))
	if !wantInfo.ModTime().Equal(gotInfo.ModTime()) {
		t.Fatal("file modification time changed")
	}
	for _, name := range []string{"hfs/empty", "tmp"} {
		if info, err := os.Stat(filepath.Join(restored, name)); err != nil || !info.IsDir() {
			t.Fatalf("missing restored directory %s", name)
		}
	}
	db, err := database.Open(context.Background(), restored)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	images, err := repository.NewImageRepository(db).List(context.Background(), 100)
	if err != nil || len(images) != 1 || images[0].ID != "0123456789abcdef01234567" {
		t.Fatalf("restored rows = %+v, error %v", images, err)
	}
}

func TestBackupRejectsBusyDirectoryAndUnsafeDestinations(t *testing.T) {
	source := fixture(t)
	lock, err := datalock.Acquire(source)
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "backup")
	if _, err := Create(context.Background(), source, destination, DefaultMaxBytes); !errors.Is(err, datalock.ErrBusy) {
		t.Fatalf("busy backup = %v", err)
	}
	lock.Close()
	for _, target := range []string{source, filepath.Join(source, "backup"), t.TempDir()} {
		if _, err := Create(context.Background(), source, target, DefaultMaxBytes); err == nil {
			t.Fatalf("accepted destination %s", target)
		}
	}
	if _, err := Create(context.Background(), source, destination, 1); err == nil {
		t.Fatal("accepted tiny byte limit")
	}
	if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed backup left destination")
	}
	if _, err := Create(context.Background(), filepath.Join(t.TempDir(), "missing"), destination, DefaultMaxBytes); err == nil {
		t.Fatal("accepted missing source")
	}
}

func TestBackupAndRestoreRejectInsufficientCapacityBeforeStaging(t *testing.T) {
	parent := t.TempDir()
	source := fixture(t)
	snapshot := filepath.Join(parent, "snapshot")
	if _, err := Create(context.Background(), source, snapshot, DefaultMaxBytes); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name        string
		source      string
		destination string
		restore     bool
	}{
		{name: "backup", source: source, destination: filepath.Join(parent, "new-backup")},
		{name: "restore", source: snapshot, destination: filepath.Join(parent, "restored"), restore: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			called := false
			_, err := execute(context.Background(), test.source, test.destination, DefaultMaxBytes, test.restore, func(path string) (uint64, error) {
				called = true
				if path != parent {
					t.Fatalf("capacity path = %q, want %q", path, parent)
				}
				return uint64(capacityReserve - 1), nil
			})
			if !called || err == nil || !strings.Contains(err.Error(), "preflight "+test.name+" capacity") {
				t.Fatalf("capacity preflight = called %t, error %v", called, err)
			}
			if _, err := os.Stat(test.destination); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("capacity failure created destination: %v", err)
			}
			stages, _ := filepath.Glob(filepath.Join(parent, ".kaven-stage-*"))
			if len(stages) != 0 {
				t.Fatalf("capacity failure created staging directories: %v", stages)
			}
		})
	}
}

func TestRequiredCapacityIncludesFilesAndRejectsOverflow(t *testing.T) {
	required, err := requiredCapacity([]Entry{{Size: 123}, {Directory: true}, {Size: 456}})
	if err != nil || required != capacityReserve+579 {
		t.Fatalf("required capacity = %d, %v", required, err)
	}
	if _, err := requiredCapacity([]Entry{{Size: math.MaxInt64}}); err == nil {
		t.Fatal("capacity overflow was accepted")
	}
}

func TestRestoreRejectsCorruptionAndCleansStage(t *testing.T) {
	for _, mutation := range []string{"bytes", "missing", "extra", "traversal", "absolute", "duplicate", "version", "producer", "negative", "symlink"} {
		t.Run(mutation, func(t *testing.T) {
			parent := t.TempDir()
			snapshot := filepath.Join(parent, "snapshot")
			destination := filepath.Join(parent, "restored")
			if _, err := Create(context.Background(), fixture(t), snapshot, DefaultMaxBytes); err != nil {
				t.Fatal(err)
			}
			switch mutation {
			case "bytes":
				name := filepath.Join(snapshot, "data/hfs/notes.txt")
				data, _ := os.ReadFile(name)
				data[0] ^= 1
				if err := os.WriteFile(name, data, 0o600); err != nil {
					t.Fatal(err)
				}
			case "missing":
				if err := os.Remove(filepath.Join(snapshot, "data/images/pixel.svg")); err != nil {
					t.Fatal(err)
				}
			case "extra":
				if err := os.WriteFile(filepath.Join(snapshot, "data/hfs/extra"), []byte("extra"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				name := filepath.Join(snapshot, "data/hfs/notes.txt")
				if err := os.Remove(name); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(snapshot, "data/images/pixel.svg"), name); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			default:
				editManifest(t, snapshot, func(m *Manifest) {
					switch mutation {
					case "traversal":
						m.Entries[0].Path = "hfs/../../outside"
					case "absolute":
						m.Entries[0].Path = "C:/outside"
					case "duplicate":
						m.Entries = append(m.Entries, m.Entries[0])
					case "version":
						m.Version = 999
					case "producer":
						m.Producer = nil
					case "negative":
						m.Entries[0].Size = -1
					}
				})
			}
			if _, err := Restore(context.Background(), snapshot, destination, DefaultMaxBytes); err == nil {
				t.Fatal("accepted corrupt backup")
			}
			if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("failed restore published destination")
			}
			stages, _ := filepath.Glob(filepath.Join(parent, ".kaven-stage-*"))
			if len(stages) != 0 {
				t.Fatalf("stages remain: %v", stages)
			}
		})
	}
}

func TestRestoreEnforcesKnownProducerIdentityAndAcceptsFormatOne(t *testing.T) {
	originalVersion, originalRevision := buildinfo.Version, buildinfo.Revision
	t.Cleanup(func() {
		buildinfo.Version, buildinfo.Revision = originalVersion, originalRevision
	})
	buildinfo.Version, buildinfo.Revision = "v1.0.0", "aaaaaaaa"
	parent := t.TempDir()
	snapshot := filepath.Join(parent, "backup")
	if _, err := Create(context.Background(), fixture(t), snapshot, DefaultMaxBytes); err != nil {
		t.Fatal(err)
	}
	buildinfo.Version = "v1.0.1"
	if _, err := Restore(context.Background(), snapshot, filepath.Join(parent, "wrong-version"), DefaultMaxBytes); err == nil || !strings.Contains(err.Error(), "requires server version") {
		t.Fatalf("version mismatch = %v", err)
	}
	buildinfo.Version, buildinfo.Revision = "v1.0.0", "bbbbbbbb"
	if _, err := Restore(context.Background(), snapshot, filepath.Join(parent, "wrong-revision"), DefaultMaxBytes); err == nil || !strings.Contains(err.Error(), "requires server revision") {
		t.Fatalf("revision mismatch = %v", err)
	}

	editManifest(t, snapshot, func(manifest *Manifest) {
		manifest.Version = 1
		manifest.Producer = nil
	})
	if report, err := Restore(context.Background(), snapshot, filepath.Join(parent, "format-one"), DefaultMaxBytes); err != nil || report.ManifestVersion != 1 || report.Producer != nil {
		t.Fatalf("format one restore = %+v, %v", report, err)
	}
}

func TestRestoreDoesNotOverwriteAndHonorsCancellation(t *testing.T) {
	parent := t.TempDir()
	snapshot := filepath.Join(parent, "backup")
	if _, err := Create(context.Background(), fixture(t), snapshot, DefaultMaxBytes); err != nil {
		t.Fatal(err)
	}
	if _, err := Restore(context.Background(), snapshot, parent, DefaultMaxBytes); err == nil {
		t.Fatal("accepted existing destination")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Restore(ctx, snapshot, filepath.Join(parent, "restored"), DefaultMaxBytes); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel = %v", err)
	}
	if _, err := Restore(context.Background(), snapshot, filepath.Join(parent, "restored"), 1); err == nil {
		t.Fatal("ignored restore byte limit")
	}
}

func TestBackupRejectsExternalReferencesAndOrphans(t *testing.T) {
	for _, external := range []bool{false, true} {
		source := fixture(t)
		if external {
			db, err := database.Open(context.Background(), source)
			if err != nil {
				t.Fatal(err)
			}
			_, err = db.Exec("UPDATE images SET folder = ?", t.TempDir())
			db.Close()
			if err != nil {
				t.Fatal(err)
			}
		} else {
			if err := os.WriteFile(filepath.Join(source, "images/orphan"), []byte("orphan"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := Create(context.Background(), source, filepath.Join(t.TempDir(), "backup"), DefaultMaxBytes); err == nil {
			t.Fatal("accepted unhealthy backup")
		}
	}
}

func TestRestoreChecksSQLiteEvenWithMatchingManifest(t *testing.T) {
	parent := t.TempDir()
	snapshot := filepath.Join(parent, "backup")
	if _, err := Create(context.Background(), fixture(t), snapshot, DefaultMaxBytes); err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(snapshot, "data", dbName)
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	copy(data, "not a database")
	if err := os.WriteFile(name, data, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	editManifest(t, snapshot, func(m *Manifest) {
		for i := range m.Entries {
			if m.Entries[i].Path == dbName {
				m.Entries[i].SHA256 = hex.EncodeToString(sum[:])
			}
		}
	})
	if _, err := Restore(context.Background(), snapshot, filepath.Join(parent, "restored"), DefaultMaxBytes); err == nil {
		t.Fatal("accepted corrupt SQLite with matching checksum")
	}
}

func TestPublishNeverReplacesExistingDirectory(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "windows" {
		t.Skip("platform not supported")
	}
	parent := t.TempDir()
	stage, destination := filepath.Join(parent, "stage"), filepath.Join(parent, "existing")
	if err := os.Mkdir(stage, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(destination, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := publish(stage, destination); err == nil {
		t.Fatal("publication replaced an empty existing directory")
	}
	if _, err := os.Stat(stage); err != nil {
		t.Fatal("failed publication removed staging directory")
	}
}

func TestPortablePathValidation(t *testing.T) {
	for _, name := range []string{"../escape", "/absolute", "hfs/../escape", `hfs\escape`, "hfs/C:stream", "hfs/NUL.txt", "hfs/trailing.", "hfs/a\x00b", "hfs//empty"} {
		if validPath(name) {
			t.Errorf("accepted %q", name)
		}
	}
	for _, name := range []string{"hfs/hello world.txt", "images/2026/pixel.svg", "hfs/照片.png"} {
		if !validPath(name) {
			t.Errorf("rejected %q", name)
		}
	}
}

func editManifest(t *testing.T, snapshot string, edit func(*Manifest)) {
	t.Helper()
	name := filepath.Join(snapshot, "manifest.json")
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	edit(&manifest)
	data, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

type cancelAfterFirstCopy struct {
	context.Context
	parent string
}

func (ctx cancelAfterFirstCopy) Err() error {
	matches, _ := filepath.Glob(filepath.Join(ctx.parent, ".kaven-stage-*", "hfs", "notes.txt"))
	if len(matches) != 0 {
		return context.Canceled
	}
	return ctx.Context.Err()
}

func TestInterruptedRestoreCleansOwnedFilesAndCanRetry(t *testing.T) {
	parent := t.TempDir()
	snapshot := filepath.Join(parent, "backup")
	destination := filepath.Join(parent, "restored")
	if _, err := Create(context.Background(), fixture(t), snapshot, DefaultMaxBytes); err != nil {
		t.Fatal(err)
	}
	ctx := cancelAfterFirstCopy{context.Background(), parent}
	if _, err := Restore(ctx, snapshot, destination, DefaultMaxBytes); !errors.Is(err, context.Canceled) {
		t.Fatalf("interrupted restore = %v", err)
	}
	if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("interrupted restore published destination")
	}
	stages, _ := filepath.Glob(filepath.Join(parent, ".kaven-stage-*"))
	if len(stages) != 0 {
		t.Fatal("interrupted restore left staging files")
	}
	if _, err := Restore(context.Background(), snapshot, destination, DefaultMaxBytes); err != nil {
		t.Fatal(err)
	}
}

func TestBackupRequiresMatchingSchema(t *testing.T) {
	source := fixture(t)
	db, err := database.Open(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec("INSERT INTO schema_migrations(version, applied_at) VALUES (999, '2026-09-07T00:00:00.000Z')")
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Create(context.Background(), source, filepath.Join(t.TempDir(), "backup"), DefaultMaxBytes); err == nil {
		t.Fatal("accepted future schema")
	}
}

func TestManifestDecodingLimitsEntryAllocation(t *testing.T) {
	data := []byte(`[` + strings.Repeat(`{},`, MaxEntries) + `{}` + `]`)
	var entries manifestEntries
	if err := json.Unmarshal(data, &entries); err == nil {
		t.Fatal("accepted oversized entry array")
	}
	if len(entries) != MaxEntries {
		t.Fatalf("allocated %d entries", len(entries))
	}
}
