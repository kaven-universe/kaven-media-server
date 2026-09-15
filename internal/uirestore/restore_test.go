package uirestore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"kaven.xyz/kaven/kaven-media-server/internal/backup"
	"kaven.xyz/kaven/kaven-media-server/internal/config"
	"kaven.xyz/kaven/kaven-media-server/internal/database"
	"kaven.xyz/kaven/kaven-media-server/internal/repository"
	"kaven.xyz/kaven/kaven-media-server/internal/storage"
)

func TestPrepareDirectoryUsesServerSideBackup(t *testing.T) {
	dataDir := t.TempDir()
	initializeData(t, dataDir)
	original := filepath.Join(dataDir, "hfs", "original.txt")
	if err := os.WriteFile(original, []byte("snapshot"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(t.TempDir(), "snapshot")
	if _, err := backup.Create(context.Background(), dataDir, snapshot, backup.DefaultMaxBytes); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(original, []byte("active"), 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(context.Background(), dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareDirectory(context.Background(), db, dataDir, snapshot, backup.DefaultMaxBytes, config.DefaultHFSRoots()); err != nil {
		t.Fatalf("prepare server backup: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if content, _ := os.ReadFile(original); string(content) != "active" {
		t.Fatalf("active content changed before restart: %q", content)
	}
	if applied, err := ApplyPending(dataDir); err != nil || !applied {
		t.Fatalf("apply server backup = %t, %v", applied, err)
	}
	if content, _ := os.ReadFile(original); string(content) != "active" {
		t.Fatalf("database-only restore changed managed content = %q", content)
	}
}

func TestPrepareDirectoryPreservesAdministratorAndRestoresSettings(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	initializeData(t, dataDir)
	db, err := database.Open(ctx, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	localSettings := config.DefaultRuntimeSettings(dataDir)
	localSettings.PublicUploads = false
	localSettings.AllowedDomainNames = []string{"local.example"}
	localSettings.HFSRoots = []config.HFSRoot{
		{Name: "uploaded", Path: "hfs/uploaded"},
	}
	credential := repository.NewAdminCredentialRepository(db)
	if err := credential.Initialize(ctx, "local-admin", []byte("local-password-hash"), localSettings); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	tokenHash := make([]byte, 32)
	credentialKey := make([]byte, 32)
	tokenHash[0], credentialKey[0] = 1, 2
	sessions := repository.NewAdminSessionRepository(db, 128)
	if created, err := sessions.Create(ctx, tokenHash, credentialKey, now, now.Add(time.Hour)); err != nil || !created {
		t.Fatalf("create session = %t, %v", created, err)
	}

	snapshotData := t.TempDir()
	initializeData(t, snapshotData)
	snapshotDB, err := database.Open(ctx, snapshotData)
	if err != nil {
		t.Fatal(err)
	}
	snapshotSettings := config.DefaultRuntimeSettings(dataDir)
	snapshotSettings.AllowedDomainNames = []string{"snapshot.example"}
	snapshotSettings.HFSRoots = []config.HFSRoot{
		{Name: "uploaded", Path: "hfs/uploaded"},
		{Name: "bing", Path: "hfs/bing"},
		{Name: "blog", Path: "hfs/blog", Public: true},
		{Name: "pub", Path: "hfs/pub", Public: true},
	}
	if err := repository.NewAdminSettingsRepository(snapshotDB).Update(ctx, snapshotSettings); err != nil {
		t.Fatal(err)
	}
	if err := snapshotDB.Close(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"uploaded", "bing", "blog", "pub"} {
		if err := os.MkdirAll(filepath.Join(snapshotData, "hfs", name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(snapshotData, "hfs", "blog", "from-snapshot.txt"), []byte("snapshot"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(t.TempDir(), "snapshot")
	if _, err := backup.Create(ctx, snapshotData, snapshot, backup.DefaultMaxBytes); err != nil {
		t.Fatal(err)
	}
	inspectedSettings, err := ReadSnapshotSettings(ctx, dataDir, snapshot, backup.DefaultMaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	if len(inspectedSettings.HFSRoots) != 4 || inspectedSettings.HFSRoots[2].Name != "blog" || !inspectedSettings.HFSRoots[3].Public {
		t.Fatalf("inspected snapshot settings = %+v", inspectedSettings)
	}
	activeFile := filepath.Join(dataDir, "hfs", "active.txt")
	if err := os.WriteFile(activeFile, []byte("active"), 0o600); err != nil {
		t.Fatal(err)
	}
	chosenRoots := []config.HFSRoot{
		{Name: "restored", Path: "hfs/restored"},
		{Name: "external", Path: filepath.Join(t.TempDir(), "external"), ReadOnly: true},
	}
	if err := os.MkdirAll(chosenRoots[1].Path, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareDirectory(ctx, db, dataDir, snapshot, backup.DefaultMaxBytes, chosenRoots); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if applied, err := ApplyPending(dataDir); err != nil || !applied {
		t.Fatalf("apply server backup = %t, %v", applied, err)
	}

	restoredDB, err := database.Open(ctx, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer restoredDB.Close()
	restoredCredential, exists, err := repository.NewAdminCredentialRepository(restoredDB).Get(ctx)
	if err != nil || !exists || restoredCredential.Username != "local-admin" || string(restoredCredential.PasswordHash) != "local-password-hash" {
		t.Fatalf("restored credential = %+v, %t, %v", restoredCredential, exists, err)
	}
	restoredSettings, err := repository.NewAdminSettingsRepository(restoredDB).Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !restoredSettings.PublicUploads || len(restoredSettings.HFSRoots) != 2 ||
		restoredSettings.HFSRoots[0].Name != "restored" || !restoredSettings.HFSRoots[1].ReadOnly ||
		len(restoredSettings.AllowedDomainNames) != 1 || restoredSettings.AllowedDomainNames[0] != "snapshot.example" {
		t.Fatalf("restored settings = %+v", restoredSettings)
	}
	if _, exists, err := repository.NewAdminSessionRepository(restoredDB, 128).Get(ctx, tokenHash, credentialKey, now); err != nil || !exists {
		t.Fatalf("restored session exists = %t, %v", exists, err)
	}
	if content, err := os.ReadFile(activeFile); err != nil || string(content) != "active" {
		t.Fatalf("active managed file = %q, %v", content, err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "hfs", "blog", "from-snapshot.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("database-only restore copied snapshot file: %v", err)
	}
}

func initializeData(t *testing.T, dataDir string) {
	t.Helper()
	store, err := storage.New(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, directory := range []string{"upload", "cache", "download/bing", "hfs"} {
		if err := store.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	db, err := database.Open(context.Background(), dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}
