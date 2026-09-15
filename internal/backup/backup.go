// Package backup creates and restores offline, checksummed SQLite snapshots.
package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"kaven.xyz/kaven/kaven-media-server/internal/buildinfo"
	"kaven.xyz/kaven/kaven-media-server/internal/config"
	"kaven.xyz/kaven/kaven-media-server/internal/database"
	"kaven.xyz/kaven/kaven-media-server/internal/datalock"
	"kaven.xyz/kaven/kaven-media-server/internal/diskspace"
	"kaven.xyz/kaven/kaven-media-server/internal/storage"
)

const (
	DefaultMaxBytes int64 = 1 << 40
	MaxEntries            = 100000
	maxManifest           = 16 << 20
	dbName                = "kaven-media.db"
	currentManifest       = 3
	capacityReserve int64 = 64 << 20
)

type Entry struct {
	Path      string    `json:"path"`
	Directory bool      `json:"directory,omitempty"`
	Size      int64     `json:"size,omitempty"`
	SHA256    string    `json:"sha256,omitempty"`
	Modified  time.Time `json:"modified"`
}

type Manifest struct {
	Version  int             `json:"version"`
	Created  time.Time       `json:"created"`
	Producer *buildinfo.Info `json:"producer,omitempty"`
	Entries  manifestEntries `json:"entries"`
}

type manifestEntries []Entry

func (entries *manifestEntries) UnmarshalJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	token, err := decoder.Token()
	if err != nil || token != json.Delim('[') {
		return errors.New("manifest entries must be an array")
	}
	*entries = nil
	for decoder.More() {
		if len(*entries) >= MaxEntries {
			return errors.New("manifest exceeds 100000 entries")
		}
		var entry Entry
		if err := decoder.Decode(&entry); err != nil {
			return err
		}
		*entries = append(*entries, entry)
	}
	_, err = decoder.Token()
	return err
}

type Report struct {
	Path            string          `json:"path"`
	Files           int             `json:"files"`
	Bytes           int64           `json:"bytes"`
	ManifestVersion int             `json:"manifestVersion"`
	Created         time.Time       `json:"created"`
	Producer        *buildinfo.Info `json:"producer,omitempty"`
}

// Create requires a stopped server and publishes only a validated snapshot.
// Source SQLite files are never opened by SQLite or changed by this operation.
func Create(ctx context.Context, source, destination string, maxBytes int64) (Report, error) {
	return execute(ctx, source, destination, maxBytes, false, diskspace.Available)
}

// Restore validates a snapshot and publishes it at a previously absent path.
func Restore(ctx context.Context, source, destination string, maxBytes int64) (Report, error) {
	return execute(ctx, source, destination, maxBytes, true, diskspace.Available)
}

type capacityReader func(string) (uint64, error)

func execute(ctx context.Context, source, destination string, maxBytes int64, restore bool, available capacityReader) (report Report, resultErr error) {
	if maxBytes <= 0 {
		return Report{}, errors.New("backup byte limit must be positive")
	}
	if err := ctx.Err(); err != nil {
		return Report{}, err
	}
	source, destination, err := locations(source, destination, restore)
	if err != nil {
		return Report{}, err
	}
	if !restore {
		lock, err := datalock.Acquire(source)
		if err != nil {
			return Report{}, err
		}
		defer lock.Close()
	}
	var manifest Manifest
	dataSource := source
	if restore {
		manifest, err = readManifest(source, maxBytes)
		if err != nil {
			return Report{}, err
		}
		if err := validateRestoreIdentity(manifest, buildinfo.Current()); err != nil {
			return Report{}, err
		}
		dataSource = filepath.Join(source, "data")
		info, err := os.Lstat(dataSource)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return Report{}, errors.New("backup data must be a directory, not a symlink")
		}
	}
	var entries []Entry
	if restore {
		entries, err = databaseEntries(manifest)
	} else {
		entries, err = databaseInventory(ctx, dataSource, true, maxBytes)
	}
	if err != nil {
		return Report{}, err
	}
	if restore {
		if err := verifyDatabaseInventory(dataSource, entries, true); err != nil {
			return Report{}, err
		}
	}
	parent := filepath.Dir(destination)
	operation := "backup"
	if restore {
		operation = "restore"
	}
	required, err := requiredCapacity(entries)
	if err != nil {
		return Report{}, err
	}
	if available == nil {
		return Report{}, errors.New("backup capacity reader is required")
	}
	free, err := available(parent)
	if err != nil {
		return Report{}, fmt.Errorf("preflight %s capacity: %w", operation, err)
	}
	if uint64(required) > free {
		return Report{}, fmt.Errorf("preflight %s capacity: need %d available bytes, filesystem has %d", operation, required, free)
	}
	stage, err := os.MkdirTemp(parent, ".kaven-stage-")
	if err != nil {
		return Report{}, fmt.Errorf("create backup staging directory: %w", err)
	}
	// stage is a server-generated direct child of the validated destination
	// parent; cleanup never uses manifest or source paths.
	defer func() {
		if err := os.RemoveAll(stage); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("clean staging directory %s: %w", stage, err))
		}
	}()
	dataStage := stage
	if !restore {
		dataStage = filepath.Join(stage, "data")
		if err := os.Mkdir(dataStage, 0o700); err != nil {
			return Report{}, err
		}
	}
	if err := copyEntries(ctx, dataSource, dataStage, entries, restore); err != nil {
		return Report{}, err
	}
	if err := validateDatabase(ctx, dataStage); err != nil {
		return Report{}, err
	}
	uploadDirectory, downloadDirectory := config.DefaultUploadDirectory, config.DefaultDownloadDirectory
	if restore {
		uploadDirectory, downloadDirectory, err = mediaDirectories(ctx, dataStage)
		if err != nil {
			return Report{}, err
		}
	}
	bingArchiveDirectory := path.Join(downloadDirectory, "bing")
	if !restore {
		entries, err = databaseInventory(ctx, dataStage, false, maxBytes)
		if err != nil {
			return Report{}, err
		}
		for i := range entries {
			if entries[i].Directory {
				continue
			}
			entries[i].SHA256, err = checksum(ctx, dataStage, entries[i])
			if err != nil {
				return Report{}, err
			}
		}
		producer := buildinfo.Current()
		manifest = Manifest{Version: currentManifest, Created: time.Now().UTC(), Producer: &producer, Entries: entries}
		encoded, err := json.MarshalIndent(manifest, "", "  ")
		if err != nil {
			return Report{}, err
		}
		if len(encoded) > maxManifest {
			return Report{}, errors.New("backup manifest exceeds 16 MiB")
		}
		if err := writeFileSync(filepath.Join(stage, "manifest.json"), encoded); err != nil {
			return Report{}, err
		}
	} else {
		// A database-only restore creates empty managed mount points for CLI use.
		// Web restore applies only the database and leaves active directories in place.
		for _, directory := range []string{uploadDirectory, "cache", bingArchiveDirectory, "tmp"} {
			if err := os.MkdirAll(filepath.Join(stage, filepath.FromSlash(directory)), 0o700); err != nil {
				return Report{}, err
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return Report{}, err
	}
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Directory {
			if err := syncDirectory(filepath.Join(dataStage, filepath.FromSlash(entries[i].Path))); err != nil {
				return Report{}, err
			}
		}
	}
	if restore {
		for _, directory := range []string{uploadDirectory, "cache", bingArchiveDirectory, downloadDirectory, "tmp"} {
			if err := syncDirectory(filepath.Join(stage, filepath.FromSlash(directory))); err != nil {
				return Report{}, err
			}
		}
	}
	if err := syncDirectory(dataStage); err != nil {
		return Report{}, err
	}
	if err := syncDirectory(stage); err != nil {
		return Report{}, err
	}
	if err := publish(stage, destination); err != nil {
		return Report{}, fmt.Errorf("publish %s: %w", destination, err)
	}
	if err := syncDirectory(parent); err != nil {
		return Report{}, fmt.Errorf("published %s but could not sync parent directory: %w", destination, err)
	}
	report = Report{Path: destination, ManifestVersion: manifest.Version, Created: manifest.Created, Producer: manifest.Producer}
	for _, entry := range entries {
		if !entry.Directory {
			report.Files++
			report.Bytes += entry.Size
		}
	}
	return report, nil
}

func mediaDirectories(ctx context.Context, directory string) (string, string, error) {
	databasePath := filepath.ToSlash(filepath.Join(directory, dbName))
	if !strings.HasPrefix(databasePath, "/") {
		databasePath = "/" + databasePath
	}
	u := url.URL{Scheme: "file", Path: databasePath}
	db, err := sql.Open("sqlite", u.String()+"?mode=ro")
	if err != nil {
		return "", "", err
	}
	defer db.Close()
	uploadDirectory, downloadDirectory := config.DefaultUploadDirectory, config.DefaultDownloadDirectory
	err = db.QueryRowContext(ctx, "SELECT upload_directory, download_directory FROM admin_settings WHERE id = 1").Scan(&uploadDirectory, &downloadDirectory)
	if err != nil {
		if strings.Contains(err.Error(), "no such column") {
			return uploadDirectory, downloadDirectory, nil
		}
		return "", "", fmt.Errorf("read restored media directory settings: %w", err)
	}
	if err := config.ValidateMediaDirectories(uploadDirectory, downloadDirectory); err != nil {
		return "", "", fmt.Errorf("validate restored media directory settings: %w", err)
	}
	return uploadDirectory, downloadDirectory, nil
}

func databaseEntries(manifest Manifest) ([]Entry, error) {
	for _, entry := range manifest.Entries {
		if entry.Path == dbName && !entry.Directory {
			return []Entry{entry}, nil
		}
	}
	return nil, errors.New("backup manifest does not contain the database")
}

func verifyDatabaseInventory(directory string, entries []Entry, strict bool) error {
	if len(entries) != 1 {
		return errors.New("backup must contain one database entry")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer root.Close()
	if strict {
		directory, err := root.Open(".")
		if err != nil {
			return fmt.Errorf("open backup data directory: %w", err)
		}
		children, err := directory.ReadDir(2)
		_ = directory.Close()
		if err != nil {
			return fmt.Errorf("inventory backup database: %w", err)
		}
		if len(children) != 1 || children[0].Name() != dbName {
			return errors.New("format 3 backup data must contain only the database")
		}
	}
	file, info, err := openRegular(root, dbName)
	if err != nil {
		return fmt.Errorf("inspect backup database: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close backup database after inspection: %w", err)
	}
	if info.Size() != entries[0].Size {
		return errors.New("backup database size does not match manifest")
	}
	return nil
}

func databaseInventory(ctx context.Context, directory string, raw bool, maxBytes int64) ([]Entry, error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	names := []string{dbName}
	if raw {
		names = append(names, dbName+"-wal", dbName+"-journal")
	}
	entries := make([]Entry, 0, len(names))
	var total int64
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		info, err := root.Lstat(name)
		if errors.Is(err, os.ErrNotExist) && name != dbName {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("inspect backup database file %s: %w", name, err)
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("backup database file is unsafe: %s", name)
		}
		if info.Size() < 0 || info.Size() > maxBytes-total {
			return nil, errors.New("backup exceeds byte limit")
		}
		total += info.Size()
		entries = append(entries, Entry{Path: name, Size: info.Size(), Modified: info.ModTime().UTC()})
	}
	return entries, nil
}

func requiredCapacity(entries []Entry) (int64, error) {
	required := capacityReserve
	for _, entry := range entries {
		if entry.Directory {
			continue
		}
		if entry.Size < 0 || required > math.MaxInt64-entry.Size {
			return 0, errors.New("backup capacity estimate exceeds supported size")
		}
		required += entry.Size
	}
	return required, nil
}

func locations(source, destination string, restore bool) (string, string, error) {
	if strings.TrimSpace(source) == "" || strings.TrimSpace(destination) == "" {
		return "", "", errors.New("source and destination paths are required")
	}
	source, err := filepath.Abs(source)
	if err != nil {
		return "", "", err
	}
	destination, err = filepath.Abs(destination)
	if err != nil {
		return "", "", err
	}
	// Reject symlink components before resolving root handles or performing any
	// SQLite operation (SQLite opens by filename, outside os.Root).
	for _, target := range []string{source, filepath.Dir(destination)} {
		for current := target; ; current = filepath.Dir(current) {
			info, err := os.Lstat(current)
			if err != nil {
				return "", "", err
			}
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return "", "", fmt.Errorf("unsafe directory %q", current)
			}
			if filepath.Dir(current) == current {
				break
			}
		}
	}
	if _, err := os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
		return "", "", fmt.Errorf("destination must not exist: %s", destination)
	}
	if within(source, destination) && !restore && isBackupRepositoryDestination(source, destination) {
		return source, destination, nil
	}
	if within(source, destination) || within(destination, source) {
		return "", "", errors.New("source and destination must not overlap")
	}
	return source, destination, nil
}

func isBackupRepositoryDestination(source, destination string) bool {
	relative, err := filepath.Rel(source, destination)
	if err != nil || filepath.IsAbs(relative) {
		return false
	}
	parts := strings.Split(filepath.Clean(relative), string(filepath.Separator))
	return len(parts) == 2 && parts[0] == "backup" && parts[1] != "." && parts[1] != ".."
}

func within(root, name string) bool {
	relative, err := filepath.Rel(root, name)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

func validPath(name string) bool {
	if !fs.ValidPath(name) || name == "." || len(name) > 4096 || strings.ContainsAny(name, "\\:\"<>|?*") {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return false
		}
	}
	for _, segment := range strings.Split(name, "/") {
		if strings.HasSuffix(segment, ".") || strings.HasSuffix(segment, " ") {
			return false
		}
		base := strings.ToUpper(strings.SplitN(segment, ".", 2)[0])
		if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" || len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '0' && base[3] <= '9' {
			return false
		}
	}
	return true
}

func allowed(name string, raw bool) bool {
	first := strings.SplitN(name, "/", 2)[0]
	if first == storage.UploadRootDirectory || first == "cache" || first == "download" || first == "hfs" {
		return true
	}
	return name == dbName || raw && (name == dbName+"-wal" || name == dbName+"-journal")
}

func openRegular(root *os.Root, name string) (*os.File, os.FileInfo, error) {
	for current := name; current != "."; current = path.Dir(current) {
		info, err := root.Lstat(current)
		if err != nil {
			return nil, nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, nil, fmt.Errorf("symlink in backup path %q", name)
		}
	}
	before, err := root.Lstat(name)
	if err != nil {
		return nil, nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("not a regular file: %s", name)
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, nil, err
	}
	info, err := file.Stat()
	if err != nil || !os.SameFile(before, info) {
		file.Close()
		return nil, nil, fmt.Errorf("file changed while opening %q", name)
	}
	return file, info, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func copyEntries(ctx context.Context, source, destination string, entries []Entry, verify bool) error {
	src, err := os.OpenRoot(source)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.OpenRoot(destination)
	if err != nil {
		return err
	}
	defer dst.Close()
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.Directory {
			if err := dst.Mkdir(entry.Path, 0o700); err != nil {
				return err
			}
			continue
		}
		if err := copyFile(ctx, src, dst, entry, verify); err != nil {
			return fmt.Errorf("copy %s: %w", entry.Path, err)
		}
	}
	return nil
}

func copyFile(ctx context.Context, src, dst *os.Root, entry Entry, verify bool) error {
	in, before, err := openRegular(src, entry.Path)
	if err != nil {
		return err
	}
	defer in.Close()
	if before.Size() != entry.Size {
		return errors.New("source size changed")
	}
	out, err := dst.OpenFile(entry.Path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer out.Close()
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(out, hash), contextReader{ctx, io.LimitReader(in, entry.Size+1)})
	if err != nil {
		return err
	}
	if n != entry.Size {
		return errors.New("source size changed while copying")
	}
	after, err := in.Stat()
	if err != nil {
		return err
	}
	if !after.ModTime().Equal(before.ModTime()) || after.Size() != before.Size() {
		return errors.New("source changed while copying")
	}
	if verify && hex.EncodeToString(hash.Sum(nil)) != entry.SHA256 {
		return errors.New("SHA-256 checksum mismatch")
	}
	if err := out.Sync(); err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return dst.Chtimes(entry.Path, entry.Modified, entry.Modified)
}

func checksum(ctx context.Context, directory string, entry Entry) (string, error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return "", err
	}
	defer root.Close()
	file, _, err := openRegular(root, entry.Path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	n, err := io.Copy(hash, contextReader{ctx, io.LimitReader(file, entry.Size+1)})
	if err != nil {
		return "", err
	}
	if n != entry.Size {
		return "", errors.New("file changed during checksum")
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func readManifest(directory string, maxBytes int64) (Manifest, error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return Manifest{}, err
	}
	defer root.Close()
	file, info, err := openRegular(root, "manifest.json")
	if err != nil {
		return Manifest{}, err
	}
	defer file.Close()
	if info.Size() > maxManifest {
		return Manifest{}, errors.New("backup manifest exceeds 16 MiB")
	}
	decoder := json.NewDecoder(io.LimitReader(file, maxManifest+1))
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("read backup manifest: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return Manifest{}, errors.New("trailing backup manifest data")
	}
	if manifest.Version != currentManifest || manifest.Created.IsZero() || len(manifest.Entries) == 0 || len(manifest.Entries) > MaxEntries {
		return Manifest{}, errors.New("unsupported or invalid backup manifest")
	}
	if !validProducer(manifest.Producer) {
		return Manifest{}, fmt.Errorf("format %d backup manifest has invalid producer identity", manifest.Version)
	}
	previous := ""
	var total int64
	for _, entry := range manifest.Entries {
		if !validPath(entry.Path) || !allowed(entry.Path, false) || entry.Path <= previous || entry.Modified.IsZero() || entry.Size < 0 || entry.Size > maxBytes-total {
			return Manifest{}, fmt.Errorf("invalid manifest entry %q", entry.Path)
		}
		previous = entry.Path
		if entry.Directory {
			if entry.Size != 0 || entry.SHA256 != "" {
				return Manifest{}, errors.New("invalid directory entry")
			}
		} else {
			decoded, err := hex.DecodeString(entry.SHA256)
			if err != nil || len(decoded) != sha256.Size || entry.SHA256 != strings.ToLower(entry.SHA256) {
				return Manifest{}, errors.New("invalid checksum in manifest")
			}
			total += entry.Size
		}
	}
	if len(manifest.Entries) != 1 || manifest.Entries[0].Path != dbName || manifest.Entries[0].Directory {
		return Manifest{}, errors.New("format 3 backup must contain only the database")
	}
	return manifest, nil
}

func validProducer(producer *buildinfo.Info) bool {
	if producer == nil {
		return false
	}
	for _, value := range []string{producer.Version, producer.Revision, producer.GoVersion} {
		if strings.TrimSpace(value) == "" || len(value) > 256 {
			return false
		}
		for _, character := range value {
			if unicode.IsControl(character) {
				return false
			}
		}
	}
	return true
}

func validateRestoreIdentity(manifest Manifest, current buildinfo.Info) error {
	producer := *manifest.Producer
	if knownBuildVersion(producer.Version) && knownBuildVersion(current.Version) && producer.Version != current.Version {
		return fmt.Errorf("backup server version %q does not match current version %q", producer.Version, current.Version)
	}
	return nil
}

func knownBuildVersion(value string) bool {
	return value != "" && value != "dev" && value != "unknown"
}

func validateDatabase(ctx context.Context, directory string) error {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	file, _, err := openRegular(root, dbName)
	root.Close()
	if err != nil {
		return fmt.Errorf("validate backup database: %w", err)
	}
	file.Close()
	databasePath := filepath.ToSlash(filepath.Join(directory, dbName))
	if !strings.HasPrefix(databasePath, "/") {
		databasePath = "/" + databasePath
	}
	u := url.URL{Scheme: "file", Path: databasePath}
	db, err := sql.Open("sqlite", u.String()+"?mode=rw")
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(1)
	defer db.Close()
	if err := database.ValidateSchema(ctx, db); err != nil {
		return err
	}
	// Bound reference counts without opening referenced files. Directory bytes
	// are deliberately outside the database-only backup format.
	references := 0
	for _, query := range []string{"SELECT folder || '/' || name FROM images", "SELECT folder || '/' || name FROM image_cache", "SELECT file FROM bing_images WHERE file IS NOT NULL AND file <> ''"} {
		rows, err := db.QueryContext(ctx, query)
		if err != nil {
			return fmt.Errorf("inspect backup references: %w", err)
		}
		for rows.Next() {
			references++
			if references > MaxEntries {
				rows.Close()
				return errors.New("backup exceeds 100000 file references")
			}
			var name string
			if err := rows.Scan(&name); err != nil {
				rows.Close()
				return err
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
	}
	rows, err := db.QueryContext(ctx, "PRAGMA integrity_check")
	if err != nil {
		return fmt.Errorf("check backup database integrity: %w", err)
	}
	for rows.Next() {
		var result string
		if err := rows.Scan(&result); err != nil {
			rows.Close()
			return err
		}
		if result != "ok" {
			rows.Close()
			return fmt.Errorf("backup database integrity check: %s", result)
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	foreignRows, err := db.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return fmt.Errorf("check backup database foreign keys: %w", err)
	}
	if foreignRows.Next() {
		foreignRows.Close()
		return errors.New("backup database has foreign-key violations")
	}
	if err := foreignRows.Close(); err != nil {
		return err
	}
	// Recover/checkpoint copied sidecars only. Never run migrations or SQLite
	// writes on the source. A snapshot ends with one standalone database file.
	var mode string
	if err := db.QueryRowContext(ctx, "PRAGMA journal_mode=DELETE").Scan(&mode); err != nil {
		return fmt.Errorf("consolidate backup database: %w", err)
	}
	if mode != "delete" {
		return errors.New("backup database did not leave WAL mode")
	}
	if err := db.Close(); err != nil {
		return err
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Lstat(filepath.Join(directory, dbName+suffix)); !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("unexpected SQLite sidecar remains: %s", suffix)
		}
	}
	file, err = os.OpenFile(filepath.Join(directory, dbName), os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}

func writeFileSync(name string, data []byte) error {
	file, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	return file.Close()
}
