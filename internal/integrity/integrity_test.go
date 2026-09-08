package integrity

import (
	"bytes"
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kaven.xyz/kaven/kaven-media-server/internal/database"
	"kaven.xyz/kaven/kaven-media-server/internal/repository"
	"kaven.xyz/kaven/kaven-media-server/internal/storage"
)

func TestCheckHealthyStorage(t *testing.T) {
	ctx := context.Background()
	dataDir, db := prepareCheck(t)
	timestamp := checkTime()

	image := repository.Image{
		ID: "image-1", UUID: "uuid-1", SHA1: imageSHA1("original"), Folder: "images", Name: "original.jpg",
		OriginalName: "original.jpg", MIMEType: "image/jpeg", Size: 8, UploadDate: timestamp,
		UploadIP: "127.0.0.1", CreatedAt: timestamp, UpdatedAt: timestamp,
	}
	if err := repository.NewImageRepository(db).Create(ctx, image); err != nil {
		t.Fatalf("create image: %v", err)
	}
	cache := repository.ImageCache{
		ID: "cache-1", ImageID: image.ID, OriginalURL: "/image/sha1-1?width=1",
		Folder: "cache", Name: "derived.jpg", CreatedAt: timestamp, UpdatedAt: timestamp,
	}
	if err := repository.NewImageCacheRepository(db).Create(ctx, cache); err != nil {
		t.Fatalf("create image cache: %v", err)
	}
	bingFile := "bing/wallpaper.jpg"
	bing := repository.BingImage{
		ID: "bing-1", URL: "https://example.test/wallpaper.jpg", File: &bingFile,
		CreatedAt: timestamp, UpdatedAt: timestamp,
	}
	if err := repository.NewBingImageRepository(db).Upsert(ctx, bing); err != nil {
		t.Fatalf("create Bing image: %v", err)
	}
	access := repository.AccessRecord{
		ID: "access-1", ImageID: image.ID, IP: "127.0.0.1", OriginalURL: "/image/sha1-1",
		CreatedAt: timestamp, UpdatedAt: timestamp,
	}
	if err := repository.NewAccessRecordRepository(db).Create(ctx, access); err != nil {
		t.Fatalf("create access record: %v", err)
	}
	download := repository.DownloadRecord{
		ID: "download-1", File: "hfs/example.txt", IP: "127.0.0.1", OriginalURL: "/hfs/example.txt",
		CreatedAt: timestamp, UpdatedAt: timestamp,
	}
	if err := repository.NewDownloadRecordRepository(db).Create(ctx, download); err != nil {
		t.Fatalf("create download record: %v", err)
	}

	writeCheckFile(t, dataDir, "images/original.jpg", "original")
	writeCheckFile(t, dataDir, "cache/derived.jpg", "derived")
	writeCheckFile(t, dataDir, "bing/wallpaper.jpg", "wallpaper")

	report, err := Check(ctx, db, dataDir)
	if err != nil {
		t.Fatalf("check storage: %v", err)
	}
	if !report.Healthy() {
		t.Fatalf("healthy storage reported issues: %#v", report.Issues)
	}
	if report.ReferencesChecked != 3 || report.FilesChecked != 3 {
		t.Fatalf("report counts = references %d, files %d", report.ReferencesChecked, report.FilesChecked)
	}
	if report.ChecksumsChecked != 1 || report.ChecksumBytes != 8 {
		t.Fatalf("checksum counts = files %d, bytes %d", report.ChecksumsChecked, report.ChecksumBytes)
	}
	if len(report.SchemaVersions) != 1 || report.SchemaVersions[0] != 1 {
		t.Fatalf("schema versions = %v", report.SchemaVersions)
	}
	wantTableCounts := TableCounts{Images: 1, ImageCaches: 1, AccessRecords: 1, BingImages: 1, DownloadRecords: 1}
	if report.TableCounts != wantTableCounts {
		t.Fatalf("table counts = %#v, want %#v", report.TableCounts, wantTableCounts)
	}
	var output bytes.Buffer
	if err := report.WriteText(&output); err != nil {
		t.Fatalf("write report: %v", err)
	}
	if got, want := output.String(), "references checked: 3\nmanaged files checked: 3\nimage checksums checked: 1\nimage bytes checked: 8\nstatus: ok\n"; got != want {
		t.Fatalf("report output = %q, want %q", got, want)
	}
}

func TestCheckReportsConsistencyProblems(t *testing.T) {
	ctx := context.Background()
	dataDir, db := prepareCheck(t)
	timestamp := checkTime()
	images := repository.NewImageRepository(db)

	missing := repository.Image{
		ID: "missing-image", UUID: "missing-uuid", SHA1: strings.Repeat("a", 40), Folder: "images", Name: "missing.jpg",
		OriginalName: "missing.jpg", MIMEType: "image/jpeg", Size: 1, UploadDate: timestamp,
		UploadIP: "127.0.0.1", CreatedAt: timestamp, UpdatedAt: timestamp,
	}
	if err := images.Create(ctx, missing); err != nil {
		t.Fatalf("create missing image reference: %v", err)
	}
	invalid := missing
	invalid.ID, invalid.UUID, invalid.SHA1 = "invalid-image", "invalid-uuid", strings.Repeat("b", 40)
	invalid.Folder, invalid.Name = "..", "outside.jpg"
	if err := images.Create(ctx, invalid); err != nil {
		t.Fatalf("create invalid image reference: %v", err)
	}
	cache := repository.ImageCache{
		ID: "invalid-cache", ImageID: missing.ID, OriginalURL: "", Folder: "cache", Name: "invalid.jpg",
		CreatedAt: timestamp, UpdatedAt: timestamp,
	}
	if err := repository.NewImageCacheRepository(db).Create(ctx, cache); err != nil {
		t.Fatalf("create invalid cache: %v", err)
	}
	writeCheckFile(t, dataDir, "cache/invalid.jpg", "cache")
	writeCheckFile(t, dataDir, "images/orphan.jpg", "orphan")
	insertBrokenForeignKey(t, db, timestamp)

	report, err := Check(ctx, db, dataDir)
	if err != nil {
		t.Fatalf("check storage: %v", err)
	}
	if report.Healthy() {
		t.Fatal("unhealthy storage reported as healthy")
	}
	wantKinds := map[IssueKind]bool{
		IssueForeignKey:       false,
		IssueInvalidReference: false,
		IssueMissingFile:      false,
		IssueOrphanFile:       false,
		IssueInvalidCache:     false,
	}
	for _, issue := range report.Issues {
		if _, expected := wantKinds[issue.Kind]; expected {
			wantKinds[issue.Kind] = true
		}
	}
	for kind, found := range wantKinds {
		if !found {
			t.Errorf("report does not contain %s: %#v", kind, report.Issues)
		}
	}

	var output bytes.Buffer
	if err := report.WriteText(&output); err != nil {
		t.Fatalf("write report: %v", err)
	}
	if strings.Contains(output.String(), dataDir) {
		t.Fatalf("report leaks absolute data directory: %s", output.String())
	}
	for index := 1; index < len(report.Issues); index++ {
		previous, current := report.Issues[index-1], report.Issues[index]
		if previous.Kind > current.Kind || (previous.Kind == current.Kind && previous.Path > current.Path) {
			t.Fatalf("issues are not sorted: %#v", report.Issues)
		}
	}
}

func TestCheckReportsImageChecksumMismatch(t *testing.T) {
	ctx := context.Background()
	dataDir, db := prepareCheck(t)
	timestamp := checkTime()
	image := repository.Image{
		ID: "image-1", UUID: "uuid-1", SHA1: imageSHA1("expected"), Folder: "images", Name: "original.jpg",
		OriginalName: "original.jpg", MIMEType: "image/jpeg", Size: 8, UploadDate: timestamp,
		UploadIP: "127.0.0.1", CreatedAt: timestamp, UpdatedAt: timestamp,
	}
	if err := repository.NewImageRepository(db).Create(ctx, image); err != nil {
		t.Fatal(err)
	}
	writeCheckFile(t, dataDir, "images/original.jpg", "tampered")
	report, err := Check(ctx, db, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if report.Healthy() || report.ChecksumsChecked != 1 || report.ChecksumBytes != 8 {
		t.Fatalf("checksum report = %#v", report)
	}
	found := false
	for _, issue := range report.Issues {
		if issue.Kind == IssueChecksumMismatch && issue.Path == "images/original.jpg" {
			found = true
		}
	}
	if !found {
		t.Fatalf("checksum mismatch issue missing: %#v", report.Issues)
	}
}

func TestChecksumImageHonorsLimit(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "image.bin")
	if err := os.WriteFile(filename, []byte("12345"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, size, err := checksumImage(context.Background(), filename, 4); !errors.Is(err, storage.ErrTooLarge) || size != 5 {
		t.Fatalf("checksum size = %d, error = %v", size, err)
	}
}

func TestCheckReportsMissingManagedDirectories(t *testing.T) {
	dataDir := t.TempDir()
	db, err := database.Open(context.Background(), dataDir)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	report, err := Check(context.Background(), db, dataDir)
	if err != nil {
		t.Fatalf("check storage: %v", err)
	}
	if len(report.Issues) != 3 {
		t.Fatalf("issue count = %d, want 3: %#v", len(report.Issues), report.Issues)
	}
	for _, issue := range report.Issues {
		if issue.Kind != IssueMissingDirectory {
			t.Fatalf("issue = %#v, want missing directory", issue)
		}
	}
}

func prepareCheck(t *testing.T) (string, *sql.DB) {
	t.Helper()
	dataDir := t.TempDir()
	for _, directory := range []string{"images", "cache", "bing"} {
		if err := os.Mkdir(filepath.Join(dataDir, directory), 0o755); err != nil {
			t.Fatalf("create %s: %v", directory, err)
		}
	}
	db, err := database.Open(context.Background(), dataDir)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return dataDir, db
}

func writeCheckFile(t *testing.T, dataDir, relative, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dataDir, filepath.FromSlash(relative)), []byte(contents), 0o600); err != nil {
		t.Fatalf("write %s: %v", relative, err)
	}
}

func insertBrokenForeignKey(t *testing.T, db *sql.DB, timestamp time.Time) {
	t.Helper()
	connection, err := db.Conn(context.Background())
	if err != nil {
		t.Fatalf("acquire database connection: %v", err)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(context.Background(), "PRAGMA foreign_keys=OFF"); err != nil {
		t.Fatalf("disable foreign keys for fixture: %v", err)
	}
	formatted := timestamp.Format("2006-01-02T15:04:05.000Z")
	if _, err := connection.ExecContext(context.Background(), `
		INSERT INTO access_records (id, image_id, ip, original_url, created_at, updated_at)
		VALUES ('broken-access', 'does-not-exist', '127.0.0.1', '/image/missing', ?, ?)`, formatted, formatted); err != nil {
		t.Fatalf("insert broken foreign key: %v", err)
	}
}

func checkTime() time.Time {
	return time.Date(2026, time.September, 4, 1, 2, 3, 4_000_000, time.UTC)
}

func imageSHA1(contents string) string {
	digest := sha1.Sum([]byte(contents)) // #nosec G401 -- test fixture for legacy-compatible verification.
	return hex.EncodeToString(digest[:])
}

func TestCheckHonorsCanceledContext(t *testing.T) {
	dataDir, db := prepareCheck(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Check(ctx, db, dataDir); !errors.Is(err, context.Canceled) {
		t.Fatalf("Check error = %v, want context.Canceled", err)
	}
}

func TestReportBoundsIssuesAndWritesMachineReadableTotals(t *testing.T) {
	checker := checker{}
	for index := 0; index < MaxReportedIssues+7; index++ {
		checker.add(IssueOrphanFile, fmt.Sprintf("images/%04d.jpg", index), "file has no database reference")
	}
	report := checker.report
	if report.Healthy() || report.IssueCount() != MaxReportedIssues+7 || len(report.Issues) != MaxReportedIssues || report.TruncatedIssues() != 7 {
		t.Fatalf("bounded report = %#v", report)
	}
	var output bytes.Buffer
	if err := report.WriteText(&output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), fmt.Sprintf("status: %d issue(s)", MaxReportedIssues+7)) || !strings.Contains(output.String(), "issues truncated: 7") {
		t.Fatalf("text report = %q", output.String())
	}
	output.Reset()
	if err := report.WriteJSON(&output); err != nil {
		t.Fatal(err)
	}
	var value struct {
		Healthy         bool        `json:"healthy"`
		IssueCount      int         `json:"issueCount"`
		Issues          []Issue     `json:"issues"`
		TruncatedIssues int         `json:"truncatedIssues"`
		SchemaVersions  []int       `json:"schemaVersions"`
		TableCounts     TableCounts `json:"tableCounts"`
	}
	if err := json.Unmarshal(output.Bytes(), &value); err != nil {
		t.Fatal(err)
	}
	if value.Healthy || value.IssueCount != MaxReportedIssues+7 || len(value.Issues) != MaxReportedIssues || value.TruncatedIssues != 7 || value.SchemaVersions == nil || value.TableCounts != (TableCounts{}) {
		t.Fatalf("JSON report = %#v", value)
	}
}
