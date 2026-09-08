package integrity

import (
	"context"
	"crypto/sha1" // SHA-1 is required only to verify legacy-compatible image records.
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"kaven.xyz/kaven/kaven-media-server/internal/buildinfo"
	"kaven.xyz/kaven/kaven-media-server/internal/config"
	"kaven.xyz/kaven/kaven-media-server/internal/storage"
)

const MaxReportedIssues = 1000

type IssueKind string

const (
	IssueDatabaseIntegrity IssueKind = "database_integrity"
	IssueForeignKey        IssueKind = "foreign_key"
	IssueInvalidReference  IssueKind = "invalid_reference"
	IssueMissingFile       IssueKind = "missing_file"
	IssueOrphanFile        IssueKind = "orphan_file"
	IssueUnsafeFile        IssueKind = "unsafe_file"
	IssueMissingDirectory  IssueKind = "missing_directory"
	IssueInvalidCache      IssueKind = "invalid_cache"
	IssueChecksumMismatch  IssueKind = "checksum_mismatch"
)

type Issue struct {
	Kind   IssueKind `json:"kind"`
	Path   string    `json:"path,omitempty"`
	Detail string    `json:"detail"`
}

type ExecutionMetadata struct {
	GeneratedAt time.Time      `json:"generatedAt"`
	Producer    buildinfo.Info `json:"producer"`
}

type TableCounts struct {
	Images          int64 `json:"images"`
	ImageCaches     int64 `json:"imageCaches"`
	AccessRecords   int64 `json:"accessRecords"`
	BingImages      int64 `json:"bingImages"`
	DownloadRecords int64 `json:"downloadRecords"`
}

type Report struct {
	ReferencesChecked int
	FilesChecked      int
	ChecksumsChecked  int64
	ChecksumBytes     int64
	SchemaVersions    []int
	TableCounts       TableCounts
	Issues            []Issue
	Execution         *ExecutionMetadata
	truncatedIssues   int
}

func (report Report) Healthy() bool {
	return report.IssueCount() == 0
}

func (report Report) IssueCount() int {
	return len(report.Issues) + report.truncatedIssues
}

func (report Report) TruncatedIssues() int {
	return report.truncatedIssues
}

func (report Report) WriteJSON(writer io.Writer) error {
	value := struct {
		Healthy           bool               `json:"healthy"`
		ReferencesChecked int                `json:"referencesChecked"`
		FilesChecked      int                `json:"filesChecked"`
		ChecksumsChecked  int64              `json:"checksumsChecked"`
		ChecksumBytes     int64              `json:"checksumBytes"`
		SchemaVersions    []int              `json:"schemaVersions"`
		TableCounts       TableCounts        `json:"tableCounts"`
		IssueCount        int                `json:"issueCount"`
		Issues            []Issue            `json:"issues"`
		TruncatedIssues   int                `json:"truncatedIssues"`
		Execution         *ExecutionMetadata `json:"execution,omitempty"`
	}{
		Healthy:           report.Healthy(),
		ReferencesChecked: report.ReferencesChecked,
		FilesChecked:      report.FilesChecked,
		ChecksumsChecked:  report.ChecksumsChecked,
		ChecksumBytes:     report.ChecksumBytes,
		SchemaVersions:    report.SchemaVersions,
		TableCounts:       report.TableCounts,
		IssueCount:        report.IssueCount(),
		Issues:            report.Issues,
		TruncatedIssues:   report.TruncatedIssues(),
		Execution:         report.Execution,
	}
	if value.Issues == nil {
		value.Issues = []Issue{}
	}
	if value.SchemaVersions == nil {
		value.SchemaVersions = []int{}
	}
	if err := json.NewEncoder(writer).Encode(value); err != nil {
		return fmt.Errorf("write integrity JSON report: %w", err)
	}
	return nil
}

func (report Report) WriteText(writer io.Writer) error {
	if _, err := fmt.Fprintf(writer, "references checked: %d\nmanaged files checked: %d\nimage checksums checked: %d\nimage bytes checked: %d\n", report.ReferencesChecked, report.FilesChecked, report.ChecksumsChecked, report.ChecksumBytes); err != nil {
		return fmt.Errorf("write integrity summary: %w", err)
	}
	if report.Healthy() {
		if _, err := io.WriteString(writer, "status: ok\n"); err != nil {
			return fmt.Errorf("write integrity status: %w", err)
		}
		return nil
	}
	if _, err := fmt.Fprintf(writer, "status: %d issue(s)\n", report.IssueCount()); err != nil {
		return fmt.Errorf("write integrity status: %w", err)
	}
	for _, issue := range report.Issues {
		if issue.Path == "" {
			if _, err := fmt.Fprintf(writer, "- %s: %s\n", issue.Kind, issue.Detail); err != nil {
				return fmt.Errorf("write integrity issue: %w", err)
			}
			continue
		}
		if _, err := fmt.Fprintf(writer, "- %s %s: %s\n", issue.Kind, issue.Path, issue.Detail); err != nil {
			return fmt.Errorf("write integrity issue: %w", err)
		}
	}
	if truncated := report.TruncatedIssues(); truncated > 0 {
		if _, err := fmt.Fprintf(writer, "issues truncated: %d\n", truncated); err != nil {
			return fmt.Errorf("write integrity truncation count: %w", err)
		}
	}
	return nil
}

func Check(ctx context.Context, db *sql.DB, dataDir string) (Report, error) {
	store, err := storage.Open(dataDir)
	if err != nil {
		return Report{}, fmt.Errorf("check storage root: %w", err)
	}
	checker := checker{
		db:         db,
		store:      store,
		references: make(map[string]bool),
	}
	if err := checker.checkDatabase(ctx); err != nil {
		return Report{}, err
	}
	if err := checker.collectReferences(ctx); err != nil {
		return Report{}, err
	}
	if err := checker.checkImageChecksums(ctx); err != nil {
		return Report{}, err
	}
	if err := checker.checkCacheRecords(ctx); err != nil {
		return Report{}, err
	}
	if err := checker.scanManagedFiles(ctx); err != nil {
		return Report{}, err
	}
	sort.Slice(checker.report.Issues, func(i, j int) bool {
		left, right := checker.report.Issues[i], checker.report.Issues[j]
		if left.Kind != right.Kind {
			return left.Kind < right.Kind
		}
		if left.Path != right.Path {
			return left.Path < right.Path
		}
		return left.Detail < right.Detail
	})
	return checker.report, nil
}

type checker struct {
	db         *sql.DB
	store      *storage.Store
	references map[string]bool
	report     Report
}

func (checker *checker) checkDatabase(ctx context.Context) error {
	versions, err := checker.db.QueryContext(ctx, "SELECT version FROM schema_migrations ORDER BY version")
	if err != nil {
		return fmt.Errorf("read SQLite schema versions: %w", err)
	}
	for versions.Next() {
		var version int
		if err := versions.Scan(&version); err != nil {
			versions.Close()
			return fmt.Errorf("scan SQLite schema version: %w", err)
		}
		checker.report.SchemaVersions = append(checker.report.SchemaVersions, version)
	}
	if err := versions.Close(); err != nil {
		return fmt.Errorf("close SQLite schema versions: %w", err)
	}
	if err := versions.Err(); err != nil {
		return fmt.Errorf("read SQLite schema versions: %w", err)
	}

	if err := checker.db.QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(*) FROM images),
			(SELECT COUNT(*) FROM image_cache),
			(SELECT COUNT(*) FROM access_records),
			(SELECT COUNT(*) FROM bing_images),
			(SELECT COUNT(*) FROM download_records)`).Scan(
		&checker.report.TableCounts.Images,
		&checker.report.TableCounts.ImageCaches,
		&checker.report.TableCounts.AccessRecords,
		&checker.report.TableCounts.BingImages,
		&checker.report.TableCounts.DownloadRecords,
	); err != nil {
		return fmt.Errorf("count SQLite destination rows: %w", err)
	}

	rows, err := checker.db.QueryContext(ctx, "PRAGMA integrity_check")
	if err != nil {
		return fmt.Errorf("run SQLite integrity check: %w", err)
	}
	for rows.Next() {
		var result string
		if err := rows.Scan(&result); err != nil {
			rows.Close()
			return fmt.Errorf("read SQLite integrity result: %w", err)
		}
		if result != "ok" {
			checker.add(IssueDatabaseIntegrity, "", result)
		}
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close SQLite integrity results: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read SQLite integrity results: %w", err)
	}

	foreignRows, err := checker.db.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return fmt.Errorf("run SQLite foreign-key check: %w", err)
	}
	defer foreignRows.Close()
	for foreignRows.Next() {
		var table, parent string
		var rowID sql.NullInt64
		var foreignKeyID int64
		if err := foreignRows.Scan(&table, &rowID, &parent, &foreignKeyID); err != nil {
			return fmt.Errorf("read SQLite foreign-key result: %w", err)
		}
		detail := fmt.Sprintf("table=%s parent=%s foreign_key=%d", table, parent, foreignKeyID)
		if rowID.Valid {
			detail += fmt.Sprintf(" rowid=%d", rowID.Int64)
		}
		checker.add(IssueForeignKey, "", detail)
	}
	if err := foreignRows.Err(); err != nil {
		return fmt.Errorf("read SQLite foreign-key results: %w", err)
	}
	return nil
}

func (checker *checker) collectReferences(ctx context.Context) error {
	queries := []struct {
		kind, prefix string
		query        string
	}{
		{kind: "image", prefix: "images", query: "SELECT id, folder || '/' || name FROM images"},
		{kind: "image cache", prefix: "cache", query: "SELECT id, folder || '/' || name FROM image_cache"},
		{kind: "Bing image", prefix: "bing", query: "SELECT id, file FROM bing_images WHERE file IS NOT NULL AND file <> ''"},
	}
	for _, item := range queries {
		rows, err := checker.db.QueryContext(ctx, item.query)
		if err != nil {
			return fmt.Errorf("query %s file references: %w", item.kind, err)
		}
		for rows.Next() {
			var id, relative string
			if err := rows.Scan(&id, &relative); err != nil {
				rows.Close()
				return fmt.Errorf("scan %s file reference: %w", item.kind, err)
			}
			checker.report.ReferencesChecked++
			resolved, err := checker.store.ResolveManagedFileReference(relative, item.prefix)
			if err != nil {
				if errors.Is(err, storage.ErrNotFound) {
					checker.add(IssueMissingFile, filepath.ToSlash(relative), fmt.Sprintf("referenced by %s %s", item.kind, id))
					continue
				}
				checker.add(IssueInvalidReference, filepath.ToSlash(relative), fmt.Sprintf("%s %s: %v", item.kind, id, err))
				continue
			}
			if pathWithin(checker.store.Root(), resolved) {
				checker.references[pathKey(resolved)] = true
			}
			info, err := os.Stat(resolved)
			if err != nil {
				if os.IsNotExist(err) {
					checker.add(IssueMissingFile, filepath.ToSlash(relative), fmt.Sprintf("referenced by %s %s", item.kind, id))
					continue
				}
				rows.Close()
				return fmt.Errorf("inspect referenced file %q: %w", relative, err)
			}
			if !info.Mode().IsRegular() {
				checker.add(IssueInvalidReference, filepath.ToSlash(relative), fmt.Sprintf("%s %s does not reference a regular file", item.kind, id))
			}
		}
		if err := rows.Close(); err != nil {
			return fmt.Errorf("close %s file references: %w", item.kind, err)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("read %s file references: %w", item.kind, err)
		}
	}
	return nil
}

func (checker *checker) checkCacheRecords(ctx context.Context) error {
	rows, err := checker.db.QueryContext(ctx, `
		SELECT image_cache.id, image_cache.original_url
		FROM image_cache
		LEFT JOIN images ON images.id = image_cache.image_id
		WHERE images.id IS NULL OR trim(image_cache.original_url) = ''`)
	if err != nil {
		return fmt.Errorf("query invalid image caches: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, originalURL string
		if err := rows.Scan(&id, &originalURL); err != nil {
			return fmt.Errorf("scan invalid image cache: %w", err)
		}
		detail := "referenced image is missing"
		if strings.TrimSpace(originalURL) == "" {
			detail = "original URL is empty"
		}
		checker.add(IssueInvalidCache, id, detail)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read invalid image caches: %w", err)
	}
	return nil
}

func (checker *checker) checkImageChecksums(ctx context.Context) error {
	rows, err := checker.db.QueryContext(ctx, "SELECT id, folder || '/' || name, COALESCE(sha1, ''), size FROM images")
	if err != nil {
		return fmt.Errorf("query image checksums: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, relative, expectedSHA1 string
		var expectedSize int64
		if err := rows.Scan(&id, &relative, &expectedSHA1, &expectedSize); err != nil {
			return fmt.Errorf("scan image checksum: %w", err)
		}
		if !validSHA1(expectedSHA1) || expectedSize < 0 || expectedSize > config.DefaultMaxImageFileSize {
			checker.add(IssueChecksumMismatch, filepath.ToSlash(relative), fmt.Sprintf("image %s has invalid checksum metadata", id))
			continue
		}
		resolved, err := checker.store.ResolveManagedFileReference(relative, "images")
		if err != nil {
			continue // Reference problems were recorded by collectReferences.
		}
		info, err := os.Lstat(resolved)
		if err != nil || !info.Mode().IsRegular() {
			continue // Missing and non-regular references were already recorded.
		}
		actualSHA1, actualSize, err := checksumImage(ctx, resolved, config.DefaultMaxImageFileSize)
		if err != nil {
			if errors.Is(err, storage.ErrTooLarge) {
				checker.add(IssueChecksumMismatch, filepath.ToSlash(relative), fmt.Sprintf("image %s exceeds the maximum image size", id))
				continue
			}
			return fmt.Errorf("checksum image file %q: %w", relative, err)
		}
		checker.report.ChecksumsChecked++
		if checker.report.ChecksumBytes > math.MaxInt64-actualSize {
			return errors.New("image checksum byte count overflow")
		}
		checker.report.ChecksumBytes += actualSize
		if actualSize != expectedSize || !strings.EqualFold(actualSHA1, expectedSHA1) {
			checker.add(IssueChecksumMismatch, filepath.ToSlash(relative), fmt.Sprintf("image %s does not match recorded size and SHA-1", id))
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read image checksums: %w", err)
	}
	if checker.report.ChecksumsChecked != checker.report.TableCounts.Images {
		checker.add(IssueChecksumMismatch, "", fmt.Sprintf(
			"verified %d of %d image checksums", checker.report.ChecksumsChecked, checker.report.TableCounts.Images,
		))
	}
	return nil
}

func checksumImage(ctx context.Context, filename string, limit int64) (string, int64, error) {
	if limit < 1 || limit == math.MaxInt64 {
		return "", 0, storage.ErrInvalidLimit
	}
	file, err := os.Open(filename)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	digest := sha1.New() // #nosec G401 -- required for legacy-compatible integrity verification.
	limited := &io.LimitedReader{R: contextReader{ctx: ctx, reader: file}, N: limit + 1}
	size, err := io.Copy(digest, limited)
	if err != nil {
		return "", size, err
	}
	if size > limit {
		return "", size, storage.ErrTooLarge
	}
	return hex.EncodeToString(digest.Sum(nil)), size, nil
}

func validSHA1(value string) bool {
	if len(value) != sha1.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader contextReader) Read(buffer []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.reader.Read(buffer)
}

func (checker *checker) scanManagedFiles(ctx context.Context) error {
	for _, directory := range []string{"images", "cache", "bing"} {
		if err := ctx.Err(); err != nil {
			return err
		}
		root, err := checker.store.Resolve(directory)
		if err != nil {
			checker.add(IssueUnsafeFile, directory, err.Error())
			continue
		}
		info, err := os.Lstat(root)
		if err != nil {
			if os.IsNotExist(err) {
				checker.add(IssueMissingDirectory, directory, "managed directory does not exist")
				continue
			}
			return fmt.Errorf("inspect managed directory %q: %w", directory, err)
		}
		if !info.IsDir() {
			checker.add(IssueUnsafeFile, directory, "managed path is not a directory")
			continue
		}
		if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if path == root || entry.IsDir() {
				return nil
			}
			relative, err := filepath.Rel(checker.store.Root(), path)
			if err != nil {
				return err
			}
			relative = filepath.ToSlash(relative)
			if entry.Type()&os.ModeSymlink != 0 {
				checker.add(IssueUnsafeFile, relative, "symlink in managed directory")
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				checker.add(IssueUnsafeFile, relative, "non-regular entry in managed directory")
				return nil
			}
			checker.report.FilesChecked++
			if !checker.references[pathKey(path)] {
				checker.add(IssueOrphanFile, relative, "file has no database reference")
			}
			return nil
		}); err != nil {
			return fmt.Errorf("scan managed directory %q: %w", directory, err)
		}
	}
	return nil
}

func (checker *checker) add(kind IssueKind, path, detail string) {
	if len(checker.report.Issues) >= MaxReportedIssues {
		checker.report.truncatedIssues++
		return
	}
	checker.report.Issues = append(checker.report.Issues, Issue{Kind: kind, Path: path, Detail: detail})
}

func pathKey(path string) string {
	clean := filepath.Clean(path)
	if runtime.GOOS == "windows" {
		return strings.ToLower(clean)
	}
	return clean
}

func pathWithin(root, candidate string) bool {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(candidate))
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}
