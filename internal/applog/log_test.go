package applog

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestLoggerWritesConsoleAndPersistentFile(t *testing.T) {
	dataDir := t.TempDir()
	var console bytes.Buffer
	logger, writer, err := New(dataDir, &console, Options{MaxBytes: 1024, MaxBackups: 2})
	if err != nil {
		t.Fatal(err)
	}
	logger.Info("persistent message", "value", 42)
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(filepath.Join(dataDir, DirectoryName, Filename))
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{"console": console.String(), "file": string(contents)} {
		if !strings.Contains(value, "msg=\"persistent message\"") || !strings.Contains(value, "value=42") {
			t.Fatalf("%s output = %q", name, value)
		}
	}
}

func TestWriterRotatesAndBoundsBackups(t *testing.T) {
	dataDir := t.TempDir()
	logger, writer, err := New(dataDir, &bytes.Buffer{}, Options{MaxBytes: 100, MaxBackups: 2})
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 12; index++ {
		logger.Info("rotation record", slog.Int("index", index), slog.String("padding", strings.Repeat("x", 32)))
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(dataDir, DirectoryName, Filename)
	if info, err := os.Stat(base); err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		t.Fatalf("active file %q = %#v, %v", base, info, err)
	}
	archives := archivePaths(t, dataDir)
	if len(archives) != 2 {
		t.Fatalf("archive count = %d, want 2: %v", len(archives), archives)
	}
	for _, filename := range archives {
		if info, err := os.Stat(filename); err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
			t.Fatalf("rotated file %q = %#v, %v", filename, info, err)
		}
		name := filepath.Base(filename)
		stamp := strings.TrimSuffix(strings.TrimPrefix(name, "kaven-media-"), ".log")
		if _, err := time.Parse(archiveTimeLayout, stamp); err != nil {
			t.Fatalf("archive filename %q does not contain a UTC timestamp: %v", name, err)
		}
	}
}

func TestWriterRetainsAllBackupsWhenConfiguredWithZero(t *testing.T) {
	dataDir := t.TempDir()
	logger, writer, err := New(dataDir, &bytes.Buffer{}, Options{MaxBytes: 100, MaxBackups: 2})
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Configure(true, 100, 0); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 8; index++ {
		logger.Info("unbounded rotation record", slog.Int("index", index), slog.String("padding", strings.Repeat("x", 32)))
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	archives := archivePaths(t, dataDir)
	if len(archives) < 3 {
		t.Fatalf("archive count = %d, want at least 3: %v", len(archives), archives)
	}
	for _, filename := range archives {
		if info, err := os.Stat(filename); err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
			t.Fatalf("retained file %q = %#v, %v", filename, info, err)
		}
	}
}

func TestWriterSerializesConcurrentRecords(t *testing.T) {
	dataDir := t.TempDir()
	logger, writer, err := New(dataDir, &bytes.Buffer{}, Options{MaxBytes: 1024 * 1024, MaxBackups: 1})
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	for index := 0; index < 100; index++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			logger.Info("concurrent record", "index", index)
		}(index)
	}
	group.Wait()
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(filepath.Join(dataDir, DirectoryName, Filename))
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Count(string(contents), "msg=\"concurrent record\""); lines != 100 {
		t.Fatalf("concurrent record count = %d", lines)
	}
}

func TestWriterCanDisableAndReenablePersistentOutput(t *testing.T) {
	dataDir := t.TempDir()
	var console bytes.Buffer
	logger, writer, err := New(dataDir, &console, Options{MaxBytes: 1024, MaxBackups: 2})
	if err != nil {
		t.Fatal(err)
	}
	logger.Info("before disable")
	staleBackup := filepath.Join(dataDir, DirectoryName, "kaven-media-20200101T000000.000000000Z.log")
	if err := os.WriteFile(staleBackup, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	newerBackup := filepath.Join(dataDir, DirectoryName, "kaven-media-20210101T000000.000000000Z.log")
	if err := os.WriteFile(newerBackup, []byte("newer"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writer.Configure(false, 2048, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(staleBackup); !os.IsNotExist(err) {
		t.Fatalf("stale backup was not pruned: %v", err)
	}
	if _, err := os.Stat(newerBackup); err != nil {
		t.Fatalf("newer backup was pruned: %v", err)
	}
	logger.Info("console only")
	if err := writer.Configure(true, 2048, 1); err != nil {
		t.Fatal(err)
	}
	logger.Info("after enable")
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(filepath.Join(dataDir, DirectoryName, Filename))
	if err != nil {
		t.Fatal(err)
	}
	fileOutput := string(contents)
	if strings.Contains(fileOutput, "console only") || !strings.Contains(fileOutput, "before disable") || !strings.Contains(fileOutput, "after enable") {
		t.Fatalf("file output = %q", fileOutput)
	}
	if !strings.Contains(console.String(), "console only") {
		t.Fatalf("console output = %q", console.String())
	}
}

func archivePaths(t *testing.T, dataDir string) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dataDir, DirectoryName, "kaven-media-*.log"))
	if err != nil {
		t.Fatal(err)
	}
	return paths
}

func TestLoggerRejectsSymlinkedLogDirectory(t *testing.T) {
	dataDir := t.TempDir()
	if err := os.Symlink(t.TempDir(), filepath.Join(dataDir, DirectoryName)); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, _, err := New(dataDir, &bytes.Buffer{}, Options{}); err == nil {
		t.Fatal("symlinked log directory was accepted")
	}
}
