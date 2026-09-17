package integrityrepair

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kaven.xyz/kaven/kaven-media-server/internal/bing"
	"kaven.xyz/kaven/kaven-media-server/internal/database"
	"kaven.xyz/kaven/kaven-media-server/internal/repository"
	"kaven.xyz/kaven/kaven-media-server/internal/storage"
)

var repairPNG = []byte("\x89PNG\r\n\x1a\nrepair fixture")

type stubBingRepairer struct{ report bing.RepairReport }

func (repairer stubBingRepairer) RepairOrphanRecords(context.Context, []string) (bing.RepairReport, error) {
	return repairer.report, nil
}

func TestRepairReconstructsUploadRecordWithoutChangingFile(t *testing.T) {
	ctx := context.Background()
	dataDir, store, images := prepareRepair(t)
	storagePath := "upload/201812/client/original.png"
	filename := writeRepairFile(t, dataDir, storagePath, repairPNG)
	before, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(store, images, stubBingRepairer{}, 1024)
	service.random = strings.NewReader(strings.Repeat("a", 16))

	report, err := service.RepairOrphanRecords(ctx, []string{storagePath})
	if err != nil || report.Candidates != 1 || report.Repaired != 1 || report.Duplicates != 0 || report.Failed != 0 {
		t.Fatalf("report = %#v, error = %v", report, err)
	}
	digest := repairSHA1(repairPNG)
	record, err := images.GetBySHA1(ctx, digest)
	if err != nil {
		t.Fatalf("get repaired image: %v", err)
	}
	if record.ID != "6161616161614161a161616161616161" || record.Folder != "201812/client" || record.Name != "original.png" || record.MIMEType != "image/png" || record.Size != int64(len(repairPNG)) || record.UploadIP != "integrity-repair" {
		t.Fatalf("record = %#v", record)
	}
	after, err := os.ReadFile(filename)
	if err != nil || string(after) != string(before) {
		t.Fatalf("file changed: %q, error = %v", after, err)
	}
}

func TestRepairReconstructsFileAtUploadRoot(t *testing.T) {
	ctx := context.Background()
	dataDir, store, images := prepareRepair(t)
	storagePath := "upload/root.png"
	writeRepairFile(t, dataDir, storagePath, repairPNG)
	service := NewService(store, images, stubBingRepairer{}, 1024)
	service.random = strings.NewReader(strings.Repeat("b", 16))

	report, err := service.RepairOrphanRecords(ctx, []string{storagePath})
	if err != nil || report.Repaired != 1 || report.Failed != 0 {
		t.Fatalf("report = %#v, error = %v", report, err)
	}
	record, err := images.GetBySHA1(ctx, repairSHA1(repairPNG))
	if err != nil {
		t.Fatalf("get repaired image: %v", err)
	}
	if record.Folder != "" || record.Name != "root.png" {
		t.Fatalf("record path = %q/%q", record.Folder, record.Name)
	}
}

func TestRepairRejectsUploadAboveConfiguredLimit(t *testing.T) {
	ctx := context.Background()
	dataDir, store, images := prepareRepair(t)
	storagePath := "upload/too-large.png"
	writeRepairFile(t, dataDir, storagePath, repairPNG)
	service := NewService(store, images, stubBingRepairer{}, int64(len(repairPNG)-1))

	report, err := service.RepairOrphanRecords(ctx, []string{storagePath})
	if err != nil || report.Repaired != 0 || report.Failed != 1 {
		t.Fatalf("report = %#v, error = %v", report, err)
	}
	if _, err := images.GetBySHA1(ctx, repairSHA1(repairPNG)); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("unexpected reconstructed record: %v", err)
	}
}

func TestRepairLeavesChecksumDuplicateAndRepairsMissingReference(t *testing.T) {
	ctx := context.Background()
	dataDir, store, images := prepareRepair(t)
	now := time.Date(2026, time.September, 16, 1, 2, 3, 0, time.UTC)
	digest := repairSHA1(repairPNG)
	record := repository.Image{
		ID: strings.Repeat("1", 32), UUID: strings.Repeat("1", 32), SHA1: digest,
		Folder: "2026/09", Name: "recorded.png", OriginalName: "original.png",
		MIMEType: "image/png", Size: int64(len(repairPNG)), UploadDate: now,
		UploadIP: "127.0.0.1", CreatedAt: now, UpdatedAt: now,
	}
	if err := images.Create(ctx, record); err != nil {
		t.Fatal(err)
	}
	writeRepairFile(t, dataDir, "upload/2026/09/recorded.png", repairPNG)
	duplicatePath := "upload/legacy/duplicate.png"
	writeRepairFile(t, dataDir, duplicatePath, repairPNG)
	service := NewService(store, images, stubBingRepairer{}, 1024)

	report, err := service.RepairOrphanRecords(ctx, []string{duplicatePath})
	if err != nil || report.Duplicates != 1 || report.Repaired != 0 || report.Failed != 0 {
		t.Fatalf("duplicate report = %#v, error = %v", report, err)
	}
	if len(report.DuplicateDetails) != 1 || report.DuplicateDetails[0].Path != duplicatePath || report.DuplicateDetails[0].ExistingReference != "upload/2026/09/recorded.png" {
		t.Fatalf("duplicate details = %#v", report.DuplicateDetails)
	}

	if err := os.Remove(filepath.Join(dataDir, filepath.FromSlash("upload/2026/09/recorded.png"))); err != nil {
		t.Fatal(err)
	}
	report, err = service.RepairOrphanRecords(ctx, []string{duplicatePath})
	if err != nil || report.Repaired != 1 || report.Duplicates != 0 || report.Failed != 0 {
		t.Fatalf("replacement report = %#v, error = %v", report, err)
	}
	repaired, err := images.GetBySHA1(ctx, digest)
	if err != nil || repaired.Folder != "legacy" || repaired.Name != "duplicate.png" {
		t.Fatalf("repaired record = %#v, error = %v", repaired, err)
	}
}

func prepareRepair(t *testing.T) (string, *storage.Store, *repository.ImageRepository) {
	t.Helper()
	dataDir := t.TempDir()
	db, err := database.Open(context.Background(), dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store, err := storage.New(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	return dataDir, store, repository.NewImageRepository(db)
}

func writeRepairFile(t *testing.T, dataDir, relative string, contents []byte) string {
	t.Helper()
	filename := filepath.Join(dataDir, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	return filename
}

func repairSHA1(contents []byte) string {
	digest := sha1.Sum(contents) // #nosec G401 -- matches the application database field.
	return hex.EncodeToString(digest[:])
}
