// Package backup creates and restores offline, checksummed directory snapshots.
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
	"sort"
	"strings"
	"time"
	"unicode"

	"kaven.xyz/kaven/kaven-media-server/internal/buildinfo"
	"kaven.xyz/kaven/kaven-media-server/internal/database"
	"kaven.xyz/kaven/kaven-media-server/internal/datalock"
	"kaven.xyz/kaven/kaven-media-server/internal/diskspace"
	"kaven.xyz/kaven/kaven-media-server/internal/integrity"
)

const (
	DefaultMaxBytes int64 = 1 << 40
	MaxEntries            = 100000
	maxManifest           = 16 << 20
	dbName                = "kaven-media.db"
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
	source, destination, err := locations(source, destination)
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
	entries, err := inventory(ctx, dataSource, !restore, maxBytes)
	if err != nil {
		return Report{}, err
	}
	if restore {
		if len(entries) != len(manifest.Entries) {
			return Report{}, errors.New("backup file inventory does not match manifest")
		}
		for i := range entries {
			if entries[i].Path != manifest.Entries[i].Path || entries[i].Directory != manifest.Entries[i].Directory || entries[i].Size != manifest.Entries[i].Size {
				return Report{}, fmt.Errorf("backup inventory mismatch at %q", entries[i].Path)
			}
		}
		entries = manifest.Entries
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
	if !restore {
		entries, err = inventory(ctx, dataStage, false, maxBytes)
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
		manifest = Manifest{Version: 2, Created: time.Now().UTC(), Producer: &producer, Entries: entries}
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
		// Temporary uploads are deliberately absent from the snapshot.
		if err := os.Mkdir(filepath.Join(stage, "tmp"), 0o700); err != nil {
			return Report{}, err
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
		if err := syncDirectory(filepath.Join(stage, "tmp")); err != nil {
			return Report{}, err
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

func locations(source, destination string) (string, string, error) {
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
	if within(source, destination) || within(destination, source) {
		return "", "", errors.New("source and destination must not overlap")
	}
	return source, destination, nil
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
	if first == "images" || first == "cache" || first == "bing" || first == "hfs" {
		return true
	}
	return name == dbName || raw && (name == dbName+"-wal" || name == dbName+"-journal")
}

func inventory(ctx context.Context, directory string, raw bool, maxBytes int64) ([]Entry, error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	for _, name := range []string{"images", "cache", "bing", "hfs"} {
		info, err := root.Lstat(name)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("required managed directory is missing or unsafe: %s", name)
		}
	}
	var entries []Entry
	var total int64
	err = walkBounded(root, ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if name == "." {
			return nil
		}
		if raw && (name == "tmp" || name == datalock.Filename || name == dbName+"-shm") {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if !validPath(name) || !allowed(name, raw) {
			return fmt.Errorf("unsupported backup path %q", name)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("backup rejects symlinks and special files: %q", name)
		}
		if len(entries) >= MaxEntries {
			return errors.New("backup exceeds 100000 entries")
		}
		size := int64(0)
		if !info.IsDir() {
			size = info.Size()
			if size < 0 || size > maxBytes-total {
				return errors.New("backup exceeds byte limit")
			}
			total += size
		}
		entries = append(entries, Entry{Path: name, Directory: info.IsDir(), Size: size, Modified: info.ModTime().UTC()})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("inventory backup: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return entries, nil
}

// Read directories in bounded batches rather than loading an arbitrarily large
// directory listing before applying the entry limit.
func walkBounded(root *os.Root, directory string, visit fs.WalkDirFunc) error {
	file, err := root.Open(directory)
	if err != nil {
		return err
	}
	defer file.Close()
	for {
		batch, err := file.ReadDir(256)
		if err != nil && err != io.EOF {
			return err
		}
		for _, entry := range batch {
			name := path.Join(directory, entry.Name())
			visitErr := visit(name, entry, nil)
			if visitErr == fs.SkipDir && entry.IsDir() {
				continue
			}
			if visitErr != nil {
				return visitErr
			}
			if entry.IsDir() {
				if err := walkBounded(root, name, visit); err != nil {
					return err
				}
			}
		}
		if err == io.EOF {
			return nil
		}
	}
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
	if (manifest.Version != 1 && manifest.Version != 2) || manifest.Created.IsZero() || len(manifest.Entries) == 0 || len(manifest.Entries) > MaxEntries {
		return Manifest{}, errors.New("unsupported or invalid backup manifest")
	}
	if manifest.Version == 1 && manifest.Producer != nil {
		return Manifest{}, errors.New("format 1 backup manifest contains producer identity")
	}
	if manifest.Version == 2 && !validProducer(manifest.Producer) {
		return Manifest{}, errors.New("format 2 backup manifest has invalid producer identity")
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
	if manifest.Version == 1 || manifest.Producer == nil {
		return nil
	}
	producer := *manifest.Producer
	if knownBuildVersion(producer.Version) && producer.Version != current.Version {
		return fmt.Errorf("backup requires server version %q; current version is %q", producer.Version, current.Version)
	}
	if knownBuildRevision(producer.Revision) && producer.Revision != current.Revision {
		return fmt.Errorf("backup requires server revision %q; current revision is %q", producer.Revision, current.Revision)
	}
	return nil
}

func knownBuildVersion(value string) bool {
	return value != "" && value != "dev" && value != "unknown"
}

func knownBuildRevision(value string) bool {
	return value != "" && value != "unknown"
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
	// Reject external image/Bing references before invoking the checker, so a
	// crafted snapshot cannot cause filesystem inspection outside staged data.
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
			if !validPath(name) || !allowed(name, false) || name == dbName {
				rows.Close()
				return fmt.Errorf("backup requires managed file references, got %q", name)
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
	}
	report, err := integrity.Check(ctx, db, directory)
	if err != nil {
		return err
	}
	if !report.Healthy() {
		return fmt.Errorf("backup integrity check found %d issue(s): %s %s", len(report.Issues), report.Issues[0].Kind, report.Issues[0].Path)
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
