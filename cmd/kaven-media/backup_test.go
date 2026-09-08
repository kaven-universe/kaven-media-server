package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"kaven.xyz/kaven/kaven-media-server/internal/backup"
	"kaven.xyz/kaven/kaven-media-server/internal/database"
	"kaven.xyz/kaven/kaven-media-server/internal/datalock"
)

func TestBackupRestoreCommands(t *testing.T) {
	parent := t.TempDir()
	source := filepath.Join(parent, "source")
	for _, name := range []string{"images", "cache", "bing", "hfs", "tmp"} {
		if err := os.MkdirAll(filepath.Join(source, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	db, err := database.Open(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KAVEN_DATA_DIR", source)
	t.Setenv("KAVEN_HFS_ROOTS", `[{"name":"uploaded","path":"hfs/uploaded"}]`)
	snapshot := filepath.Join(parent, "snapshot")
	restored := filepath.Join(parent, "restored")
	var output bytes.Buffer
	if err := runBackup(context.Background(), "backup", []string{"--output", snapshot}, &output); err != nil {
		t.Fatal(err)
	}
	var report backup.Report
	if err := json.Unmarshal(output.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Path != snapshot || report.Files != 1 || report.ManifestVersion != 2 || report.Producer == nil || report.Producer.GoVersion == "" {
		t.Fatalf("report = %+v", report)
	}
	output.Reset()
	if err := runBackup(context.Background(), "restore", []string{"--input", snapshot, "--data-dir", restored}, &output); err != nil {
		t.Fatal(err)
	}
	var restoreReport backup.Report
	if err := json.Unmarshal(output.Bytes(), &restoreReport); err != nil {
		t.Fatal(err)
	}
	if restoreReport.ManifestVersion != 2 || restoreReport.Producer == nil || restoreReport.Producer.Revision != report.Producer.Revision {
		t.Fatalf("restore report = %+v", restoreReport)
	}
	output.Reset()
	if err := runCheck(context.Background(), restored, &output, false); err != nil {
		t.Fatal(err)
	}
	lock, err := datalock.Acquire(source)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	output.Reset()
	if err := runBackup(context.Background(), "backup", []string{"--output", filepath.Join(parent, "busy-backup")}, &output); !errors.Is(err, datalock.ErrBusy) {
		t.Fatalf("backup ignored process lock: %v", err)
	}
	if output.Len() != 0 {
		t.Fatal("failed backup wrote success report")
	}
	if err := runCheck(context.Background(), source, &output, false); !errors.Is(err, datalock.ErrBusy) {
		t.Fatalf("check ignored process lock: %v", err)
	}
}

func TestBackupCommandRejectsInvalidArguments(t *testing.T) {
	for _, command := range []string{"backup", "restore"} {
		for _, args := range [][]string{nil, {"--unknown"}, {"extra"}, {"--max-bytes", "oops"}} {
			if err := runBackup(context.Background(), command, args, &bytes.Buffer{}); err == nil {
				t.Fatalf("accepted %s %v", command, args)
			}
		}
	}
	t.Setenv("KAVEN_HFS_ROOTS", `[{"name":"legacy","path":"C:/legacy","readOnly":true}]`)
	if err := runBackup(context.Background(), "backup", []string{"--output", filepath.Join(t.TempDir(), "backup")}, &bytes.Buffer{}); err == nil {
		t.Fatal("accepted external HFS root")
	}
}
