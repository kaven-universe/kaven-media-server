package integrityrepair

import (
	"context"
	"crypto/rand"
	"crypto/sha1" // SHA-1 matches the image lookup checksum stored by the application.
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"kaven.xyz/kaven/kaven-media-server/internal/bing"
	"kaven.xyz/kaven/kaven-media-server/internal/imageproc"
	"kaven.xyz/kaven/kaven-media-server/internal/repository"
	"kaven.xyz/kaven/kaven-media-server/internal/storage"
)

const maxRepairDetails = 100

type BingRepairer interface {
	RepairOrphanRecords(context.Context, []string) (bing.RepairReport, error)
}

type ImageRepository interface {
	Create(context.Context, repository.Image) error
	GetBySHA1(context.Context, string) (repository.Image, error)
	RepairFileReference(context.Context, repository.Image) error
}

// Service repairs only records that can be reconstructed from verified file
// content. It never deletes, renames, or moves managed files.
type Service struct {
	store    *storage.Store
	images   ImageRepository
	bing     BingRepairer
	maxBytes int64
	now      func() time.Time
	random   io.Reader
}

func NewService(store *storage.Store, images ImageRepository, bingRepairer BingRepairer, maxBytes int64) *Service {
	return &Service{store: store, images: images, bing: bingRepairer, maxBytes: maxBytes, now: time.Now, random: rand.Reader}
}

func (service *Service) RepairOrphanRecords(ctx context.Context, candidates []string) (bing.RepairReport, error) {
	if service.store == nil || service.images == nil || service.bing == nil || service.maxBytes <= 0 {
		return bing.RepairReport{}, errors.New("repair integrity records: storage, image repository, Bing repairer, and image size limit are required")
	}
	report, err := service.bing.RepairOrphanRecords(ctx, candidates)
	if err != nil {
		return report, err
	}
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(strings.ReplaceAll(candidate, `\`, "/"))
		if _, duplicate := seen[candidate]; duplicate {
			continue
		}
		seen[candidate] = struct{}{}
		relative, ok := service.uploadCandidate(candidate)
		if !ok {
			continue
		}
		report.Candidates++
		service.repairUpload(ctx, candidate, relative, &report)
	}
	return report, nil
}

func (service *Service) uploadCandidate(candidate string) (string, bool) {
	prefix := strings.TrimSuffix(service.store.UploadDirectory(), "/") + "/"
	if !strings.HasPrefix(candidate, prefix) {
		return "", false
	}
	relative := strings.TrimPrefix(candidate, prefix)
	if relative == "" {
		return "", false
	}
	return relative, true
}

func (service *Service) repairUpload(ctx context.Context, storagePath, relative string, report *bing.RepairReport) {
	if err := ctx.Err(); err != nil {
		addFailure(report, storagePath, err.Error())
		return
	}
	resolved, err := service.store.ResolveExisting(storagePath)
	if err != nil {
		addFailure(report, storagePath, "file is unavailable or unsafe: "+err.Error())
		return
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		addFailure(report, storagePath, "file is no longer a regular file")
		return
	}
	if info.Size() > service.maxBytes {
		addFailure(report, storagePath, fmt.Sprintf("file exceeds the configured image size limit of %d bytes", service.maxBytes))
		return
	}
	format, err := imageproc.DetectFile(resolved)
	if err != nil {
		addFailure(report, storagePath, "file is not a supported image: "+err.Error())
		return
	}
	digest, size, err := checksumFile(ctx, resolved, service.maxBytes)
	if err != nil {
		addFailure(report, storagePath, err.Error())
		return
	}

	existing, err := service.images.GetBySHA1(ctx, digest)
	if err == nil {
		service.repairExistingUpload(ctx, existing, storagePath, relative, resolved, format.MIMEType, size, report)
		return
	}
	if !errors.Is(err, repository.ErrNotFound) {
		addFailure(report, storagePath, "could not query an existing image checksum")
		return
	}

	id, err := service.newID()
	if err != nil {
		addFailure(report, storagePath, err.Error())
		return
	}
	timestamp := info.ModTime().UTC()
	if timestamp.IsZero() {
		timestamp = service.now().UTC()
	}
	image := repository.Image{
		ID: id, UUID: id, SHA1: digest, Folder: storageFolder(relative), Name: path.Base(relative),
		OriginalName: path.Base(relative), MIMEType: format.MIMEType, Size: size,
		UploadDate: timestamp, UploadIP: "integrity-repair", CreatedAt: timestamp, UpdatedAt: timestamp,
	}
	if err := service.images.Create(ctx, image); err != nil {
		addFailure(report, storagePath, "could not create reconstructed image record: "+err.Error())
		return
	}
	report.Repaired++
	addDetail(report, storagePath, "image record reconstructed from verified file content; original legacy ID was not recoverable")
}

func (service *Service) repairExistingUpload(ctx context.Context, existing repository.Image, storagePath, relative, resolved, mimeType string, size int64, report *bing.RepairReport) {
	existingRelative := path.Join(existing.Folder, existing.Name)
	existingResolved, err := service.store.ResolveStoredFileReference(existingRelative, "image")
	if err == nil {
		if filepath.Clean(existingResolved) == filepath.Clean(resolved) {
			report.AlreadyRecorded++
			addDetail(report, storagePath, "the file already has an exact database reference")
		} else {
			report.Duplicates++
			addDuplicate(report, storagePath, path.Join(service.store.UploadDirectory(), existingRelative))
			addDetail(report, storagePath, "duplicate upload left unchanged because its checksum already references another valid file")
		}
		return
	}
	if !errors.Is(err, storage.ErrNotFound) {
		addFailure(report, storagePath, "existing image reference is unsafe: "+err.Error())
		return
	}
	existing.Folder = storageFolder(relative)
	existing.Name = path.Base(relative)
	existing.MIMEType = mimeType
	existing.Size = size
	existing.UpdatedAt = service.now().UTC()
	if err := service.images.RepairFileReference(ctx, existing); err != nil {
		addFailure(report, storagePath, "could not repair the existing image record: "+err.Error())
		return
	}
	report.Repaired++
	addDetail(report, storagePath, "missing image file reference repaired from matching checksum")
}

func (service *Service) newID() (string, error) {
	value := make([]byte, 16)
	if _, err := io.ReadFull(service.random, value); err != nil {
		return "", fmt.Errorf("generate repaired image UUID: %w", err)
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return hex.EncodeToString(value), nil
}

func checksumFile(ctx context.Context, filename string, maxBytes int64) (string, int64, error) {
	file, err := os.Open(filename)
	if err != nil {
		return "", 0, fmt.Errorf("open orphan image: %w", err)
	}
	defer file.Close()
	hash := sha1.New() // #nosec G401 -- the database lookup contract stores SHA-1.
	buffer := make([]byte, 64*1024)
	var size int64
	for {
		if err := ctx.Err(); err != nil {
			return "", size, err
		}
		count, readErr := file.Read(buffer)
		if count > 0 {
			size += int64(count)
			if size > maxBytes {
				return "", size, fmt.Errorf("orphan image exceeds the configured image size limit of %d bytes", maxBytes)
			}
			_, _ = hash.Write(buffer[:count])
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return "", size, fmt.Errorf("read orphan image: %w", readErr)
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}

func storageFolder(relative string) string {
	folder := path.Dir(relative)
	if folder == "." {
		return ""
	}
	return folder
}

func addFailure(report *bing.RepairReport, candidate, result string) {
	report.Failed++
	addDetail(report, candidate, result)
}

func addDetail(report *bing.RepairReport, candidate, result string) {
	if len(report.Details) < maxRepairDetails {
		report.Details = append(report.Details, bing.RepairDetail{Path: candidate, Result: result})
	}
}

func addDuplicate(report *bing.RepairReport, candidate, existingReference string) {
	if len(report.DuplicateDetails) < maxRepairDetails {
		report.DuplicateDetails = append(report.DuplicateDetails, bing.RepairDuplicate{Path: candidate, ExistingReference: existingReference})
	}
}
