package repository

import (
	"context"
	"testing"

	"kaven.xyz/kaven/kaven-media-server/internal/config"
	"kaven.xyz/kaven/kaven-media-server/internal/database"
)

func TestAdminSettingsRepository(t *testing.T) {
	db, err := database.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	repository := NewAdminSettingsRepository(db)
	days, err := repository.RememberDurationDays(context.Background())
	if err != nil || days != DefaultRememberDurationDays {
		t.Fatalf("default remember duration = %d, error = %v", days, err)
	}
	if err := repository.SetRememberDurationDays(context.Background(), 90); err != nil {
		t.Fatal(err)
	}
	days, err = repository.RememberDurationDays(context.Background())
	if err != nil || days != 90 {
		t.Fatalf("updated remember duration = %d, error = %v", days, err)
	}
	if err := repository.SetRememberDurationDays(context.Background(), 0); err == nil {
		t.Fatal("invalid remember duration succeeded")
	}
}

func TestAdminSettingsRepositoryRoundTripsApplicationSettings(t *testing.T) {
	db, err := database.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	repository := NewAdminSettingsRepository(db)
	settings, err := repository.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !settings.PublicUploads || settings.MaxFileCount != 100 || settings.RememberDurationDays != 30 ||
		!settings.BingSyncEnabled || settings.BingSyncIntervalHours != 24 || len(settings.HFSRoots) != 0 ||
		settings.UploadDirectory != "upload" || settings.DownloadDirectory != "download" {
		t.Fatalf("unexpected defaults: %+v", settings)
	}
	settings.PublicUploads = false
	settings.MaxFileCount = 12
	settings.BingSyncEnabled = false
	settings.BingSyncIntervalHours = 6
	settings.UploadDirectory = "media/uploads"
	settings.DownloadDirectory = "media/downloads"
	settings.AllowedDomainNames = []string{"example.com"}
	settings.HFSRoots = []config.HFSRoot{{Name: "public", Path: "hfs/public", Public: true}}
	if err := repository.Update(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	stored, err := repository.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stored.PublicUploads || stored.MaxFileCount != 12 || stored.BingSyncEnabled ||
		stored.BingSyncIntervalHours != 6 || stored.UploadDirectory != "media/uploads" ||
		stored.DownloadDirectory != "media/downloads" || len(stored.AllowedDomainNames) != 1 || stored.HFSRoots[0].Name != "public" {
		t.Fatalf("stored settings: %+v", stored)
	}
}
