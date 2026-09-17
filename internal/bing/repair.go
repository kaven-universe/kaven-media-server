package bing

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"

	"kaven.xyz/kaven/kaven-media-server/internal/imageproc"
	"kaven.xyz/kaven/kaven-media-server/internal/repository"
)

const (
	repairMetadataPageSize = 8
	repairMetadataPages    = 2
	maxRepairDetails       = 100
)

type RepairDetail struct {
	Path   string `json:"path"`
	Result string `json:"result"`
}

type RepairDuplicate struct {
	Path              string `json:"path"`
	ExistingReference string `json:"existingReference"`
}

type RepairReport struct {
	Candidates       int               `json:"candidates"`
	Repaired         int               `json:"repaired"`
	AlreadyRecorded  int               `json:"alreadyRecorded"`
	Duplicates       int               `json:"duplicates"`
	Unmatched        int               `json:"unmatched"`
	Failed           int               `json:"failed"`
	Details          []RepairDetail    `json:"details"`
	DuplicateDetails []RepairDuplicate `json:"duplicateDetails"`
}

// RepairOrphanRecords conservatively reconstructs Bing database rows from
// orphan files whose final filename component contains a 32-character Bing
// metadata hash. It never deletes, renames, or replaces a media file.
func (service *Service) RepairOrphanRecords(ctx context.Context, candidates []string) (RepairReport, error) {
	select {
	case service.syncToken <- struct{}{}:
		defer func() { <-service.syncToken }()
	case <-ctx.Done():
		return RepairReport{}, ctx.Err()
	}

	report := RepairReport{Details: make([]RepairDetail, 0), DuplicateDetails: make([]RepairDuplicate, 0)}
	filtered := make([]repairCandidate, 0, len(candidates))
	seenPaths := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(strings.ReplaceAll(candidate, `\`, "/"))
		if _, duplicate := seenPaths[candidate]; duplicate {
			continue
		}
		seenPaths[candidate] = struct{}{}
		relative, hash, ok := service.repairCandidate(candidate)
		if !ok {
			continue
		}
		report.Candidates++
		filtered = append(filtered, repairCandidate{storagePath: candidate, relative: relative, hash: hash})
	}
	if len(filtered) == 0 {
		return report, nil
	}

	unresolved := make([]repairCandidate, 0, len(filtered))
	for _, candidate := range filtered {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		resolved, err := service.store.ResolveStoredFileReference(candidate.relative, "bing_image")
		if err != nil {
			report.failed(candidate.storagePath, "file is unavailable or unsafe: "+err.Error())
			continue
		}
		if _, err := imageproc.DetectFile(resolved); err != nil {
			report.failed(candidate.storagePath, "file is not a supported image: "+err.Error())
			continue
		}

		existing, err := service.images.GetByHash(ctx, candidate.hash)
		switch {
		case err == nil:
			service.repairExisting(ctx, existing, candidate, &report)
			continue
		case !errors.Is(err, repository.ErrNotFound):
			report.failed(candidate.storagePath, "could not query the existing Bing hash")
			continue
		}
		unresolved = append(unresolved, candidate)
	}
	if len(unresolved) == 0 {
		return report, nil
	}

	metadataByHash := make(map[string]Image)
	for page := 0; page < repairMetadataPages; page++ {
		images, err := service.metadata.Fetch(ctx, page*repairMetadataPageSize, repairMetadataPageSize)
		if err != nil {
			return report, fmt.Errorf("repair Bing records: fetch metadata page %d: %w", page+1, err)
		}
		for _, image := range images {
			hash := strings.ToLower(image.Hash)
			if validRepairHash(hash) {
				metadataByHash[hash] = image
			}
		}
	}

	for _, candidate := range unresolved {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		metadata, ok := metadataByHash[candidate.hash]
		if !ok {
			report.unmatched(candidate.storagePath, "no matching Bing metadata was available in the recent archive window")
			continue
		}
		now := service.now().UTC()
		record := service.record(metadata, now)
		if byURL, err := service.images.GetByURL(ctx, metadata.URL); err == nil {
			record.ID = byURL.ID
			record.CreatedAt = byURL.CreatedAt
			service.repairExisting(ctx, byURL, candidate, &report)
			continue
		} else if !errors.Is(err, repository.ErrNotFound) {
			report.failed(candidate.storagePath, "could not query the existing Bing URL")
			continue
		} else {
			record.ID, err = service.newID()
			if err != nil {
				report.failed(candidate.storagePath, err.Error())
				continue
			}
		}
		record.File = &candidate.relative
		if err := service.images.Upsert(ctx, record); err != nil {
			report.failed(candidate.storagePath, "could not save reconstructed Bing metadata: "+err.Error())
			continue
		}
		report.Repaired++
		report.add(candidate.storagePath, "database record reconstructed")
	}
	return report, nil
}

type repairCandidate struct {
	storagePath string
	relative    string
	hash        string
}

func (service *Service) repairCandidate(candidate string) (string, string, bool) {
	prefix := strings.TrimSuffix(service.store.BingArchiveDirectory(), "/") + "/"
	if !strings.HasPrefix(candidate, prefix) {
		return "", "", false
	}
	relative := strings.TrimPrefix(candidate, prefix)
	base := path.Base(relative)
	extension := path.Ext(base)
	stem := strings.TrimSuffix(base, extension)
	separator := strings.LastIndexByte(stem, '_')
	if separator < 0 {
		return "", "", false
	}
	hash := strings.ToLower(stem[separator+1:])
	if !validRepairHash(hash) {
		return "", "", false
	}
	return relative, hash, true
}

func validRepairHash(value string) bool {
	if len(value) != 32 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func (service *Service) repairExisting(ctx context.Context, existing repository.BingImage, candidate repairCandidate, report *RepairReport) {
	if existing.File != nil {
		usable, err := service.usableStoredFile(*existing.File)
		if err != nil {
			report.failed(candidate.storagePath, "existing database reference is unsafe: "+err.Error())
			return
		}
		if usable {
			if path.Clean(*existing.File) == path.Clean(candidate.relative) {
				report.AlreadyRecorded++
				report.add(candidate.storagePath, "the file already has an exact database reference")
			} else {
				report.Duplicates++
				report.addDuplicate(candidate.storagePath, path.Join(service.store.BingArchiveDirectory(), *existing.File))
				report.add(candidate.storagePath, "duplicate Bing file left unchanged because the hash already references another valid file")
			}
			return
		}
	}
	existing.File = &candidate.relative
	existing.UpdatedAt = service.now().UTC()
	if err := service.images.Upsert(ctx, existing); err != nil {
		report.failed(candidate.storagePath, "could not repair the existing Bing record: "+err.Error())
		return
	}
	report.Repaired++
	report.add(candidate.storagePath, "missing database file reference repaired")
}

func (report *RepairReport) unmatched(path, result string) {
	report.Unmatched++
	report.add(path, result)
}

func (report *RepairReport) failed(path, result string) {
	report.Failed++
	report.add(path, result)
}

func (report *RepairReport) add(path, result string) {
	if len(report.Details) < maxRepairDetails {
		report.Details = append(report.Details, RepairDetail{Path: path, Result: result})
	}
}

func (report *RepairReport) addDuplicate(candidate, existingReference string) {
	if len(report.DuplicateDetails) < maxRepairDetails {
		report.DuplicateDetails = append(report.DuplicateDetails, RepairDuplicate{Path: candidate, ExistingReference: existingReference})
	}
}
