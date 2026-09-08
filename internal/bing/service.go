package bing

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"time"

	"kaven.xyz/kaven/kaven-media-server/internal/imageproc"
	"kaven.xyz/kaven/kaven-media-server/internal/repository"
	"kaven.xyz/kaven/kaven-media-server/internal/storage"
)

const (
	DefaultDownloadBaseURL  = "https://cn.bing.com"
	DefaultDownloadTimeout  = 30 * time.Second
	DefaultMaxDownloadBytes = int64(50 * 1024 * 1024)
	MaxDownloadBytesLimit   = int64(200 * 1024 * 1024)
)

var archiveExtensions = []string{".jpg", ".png", ".gif", ".webp", ".tiff", ".avif", ".heif", ".svg"}

type MetadataClient interface {
	Fetch(context.Context, int, int) ([]Image, error)
}

type ImageRepository interface {
	GetByURL(context.Context, string) (repository.BingImage, error)
	Upsert(context.Context, repository.BingImage) error
}

type ServiceOptions struct {
	DownloadBaseURL  string
	HTTPClient       *http.Client
	DownloadTimeout  time.Duration
	MaxDownloadBytes int64
}

type Service struct {
	metadata         MetadataClient
	images           ImageRepository
	store            *storage.Store
	downloadBase     *url.URL
	httpClient       *http.Client
	downloadTimeout  time.Duration
	maxDownloadBytes int64
	now              func() time.Time
	random           io.Reader
	syncToken        chan struct{}
}

type SyncReport struct {
	Fetched      int
	Synchronized int
	Downloaded   int
	Reused       int
	Failed       int
	Duplicates   int
}

type syncedFile struct {
	relative   string
	created    bool
	downloaded bool
}

func NewService(metadata MetadataClient, images ImageRepository, store *storage.Store, options ServiceOptions) (*Service, error) {
	if metadata == nil || images == nil || store == nil {
		return nil, errors.New("create Bing archive service: metadata client, image repository, and storage are required")
	}
	base := options.DownloadBaseURL
	if base == "" {
		base = DefaultDownloadBaseURL
	}
	parsedBase, err := url.Parse(base)
	if err != nil || parsedBase.Host == "" || (parsedBase.Scheme != "http" && parsedBase.Scheme != "https") ||
		parsedBase.User != nil || parsedBase.Fragment != "" {
		return nil, errors.New("create Bing archive service: download base must be an absolute HTTP(S) URL without credentials or a fragment")
	}
	downloadTimeout := options.DownloadTimeout
	if downloadTimeout == 0 {
		downloadTimeout = DefaultDownloadTimeout
	}
	if downloadTimeout < 0 {
		return nil, errors.New("create Bing archive service: download timeout must be positive")
	}
	maxDownloadBytes := options.MaxDownloadBytes
	if maxDownloadBytes == 0 {
		maxDownloadBytes = DefaultMaxDownloadBytes
	}
	if maxDownloadBytes < 1 || maxDownloadBytes > MaxDownloadBytesLimit {
		return nil, fmt.Errorf("create Bing archive service: maximum download must be between 1 and %d bytes", MaxDownloadBytesLimit)
	}
	httpClient := options.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	return &Service{
		metadata: metadata, images: images, store: store, downloadBase: parsedBase,
		httpClient: httpClient, downloadTimeout: downloadTimeout, maxDownloadBytes: maxDownloadBytes,
		now: time.Now, random: rand.Reader, syncToken: make(chan struct{}, 1),
	}, nil
}

func (service *Service) Sync(ctx context.Context) (SyncReport, error) {
	select {
	case service.syncToken <- struct{}{}:
		defer func() { <-service.syncToken }()
	case <-ctx.Done():
		return SyncReport{}, ctx.Err()
	}
	images, err := service.metadata.Fetch(ctx, 0, MaxArchiveImages)
	if err != nil {
		return SyncReport{}, fmt.Errorf("synchronize Bing archive: fetch metadata: %w", err)
	}
	if len(images) > MaxArchiveImages {
		return SyncReport{}, fmt.Errorf("synchronize Bing archive: metadata contains more than %d images", MaxArchiveImages)
	}
	report := SyncReport{Fetched: len(images)}
	seen := make(map[string]struct{}, len(images))
	var result error
	for _, image := range images {
		if _, duplicate := seen[image.URL]; duplicate {
			report.Duplicates++
			continue
		}
		seen[image.URL] = struct{}{}
		downloaded, err := service.syncImage(ctx, image)
		if err != nil {
			report.Failed++
			result = errors.Join(result, fmt.Errorf("synchronize Bing image %q: %w", image.URL, err))
			if ctx.Err() != nil {
				break
			}
			continue
		}
		report.Synchronized++
		if downloaded {
			report.Downloaded++
		} else {
			report.Reused++
		}
	}
	return report, result
}

func (service *Service) syncImage(ctx context.Context, metadata Image) (bool, error) {
	if metadata.URL == "" {
		return false, errors.New("Bing image URL is required")
	}
	now := service.now().UTC()
	record := service.record(metadata, now)
	existing, err := service.images.GetByURL(ctx, metadata.URL)
	switch {
	case err == nil:
		record.ID = existing.ID
		record.CreatedAt = existing.CreatedAt
		if existing.File != nil {
			usable, err := service.usableStoredFile(*existing.File)
			if err != nil {
				return false, err
			}
			if usable {
				record.File = existing.File
				if err := service.images.Upsert(ctx, record); err != nil {
					return false, fmt.Errorf("update Bing image record: %w", err)
				}
				return false, nil
			}
		}
	case errors.Is(err, repository.ErrNotFound):
		id, idErr := service.newID()
		if idErr != nil {
			return false, idErr
		}
		record.ID = id
	default:
		return false, fmt.Errorf("look up Bing image: %w", err)
	}

	file, err := service.obtainFile(ctx, metadata)
	if err != nil {
		return false, err
	}
	record.File = &file.relative
	if err := service.images.Upsert(ctx, record); err != nil {
		if file.created {
			if removeErr := service.store.RemoveFile(file.relative); removeErr != nil {
				return false, errors.Join(fmt.Errorf("upsert Bing image record: %w", err), fmt.Errorf("roll back Bing image file: %w", removeErr))
			}
		}
		return false, fmt.Errorf("upsert Bing image record: %w", err)
	}
	return file.downloaded, nil
}

func (service *Service) obtainFile(ctx context.Context, metadata Image) (syncedFile, error) {
	folder := path.Join("bing", archiveYear(metadata))
	key := archiveKey(metadata.URL)
	for _, extension := range archiveExtensions {
		relative := path.Join(folder, key+extension)
		resolved, err := service.store.ResolveExisting(relative)
		if errors.Is(err, storage.ErrNotFound) {
			continue
		}
		if err != nil {
			return syncedFile{}, fmt.Errorf("inspect existing Bing image: %w", err)
		}
		if _, err := imageproc.DetectFile(resolved); err != nil {
			return syncedFile{}, fmt.Errorf("validate existing Bing image: %w", err)
		}
		return syncedFile{relative: relative}, nil
	}

	downloadURL, err := service.downloadURL(metadata.URL)
	if err != nil {
		return syncedFile{}, err
	}
	downloadCtx, cancel := context.WithTimeout(ctx, service.downloadTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(downloadCtx, http.MethodGet, downloadURL.String(), nil)
	if err != nil {
		return syncedFile{}, fmt.Errorf("create Bing image request: %w", err)
	}
	request.Header.Set("Accept", "image/*")
	request.Header.Set("User-Agent", "Kaven-Media-Server")
	httpClient := *service.httpClient
	previousRedirect := httpClient.CheckRedirect
	httpClient.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if request.URL.Scheme != service.downloadBase.Scheme || request.URL.Host != service.downloadBase.Host {
			return errors.New("Bing image redirect changed origin")
		}
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		if previousRedirect != nil {
			return previousRedirect(request, via)
		}
		return nil
	}
	response, err := httpClient.Do(request)
	if err != nil {
		return syncedFile{}, fmt.Errorf("download Bing image: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return syncedFile{}, fmt.Errorf("download Bing image: HTTP %d", response.StatusCode)
	}
	if response.ContentLength > service.maxDownloadBytes {
		return syncedFile{}, fmt.Errorf("download Bing image: content length exceeds %d bytes", service.maxDownloadBytes)
	}
	staged, err := service.store.Stage(downloadCtx, response.Body, service.maxDownloadBytes)
	if err != nil {
		return syncedFile{}, fmt.Errorf("stage Bing image: %w", err)
	}
	defer staged.Abort()
	format, err := imageproc.DetectFile(staged.Path())
	if err != nil {
		return syncedFile{}, fmt.Errorf("validate Bing image: %w", err)
	}
	if err := service.store.MkdirAll(folder, 0o755); err != nil {
		return syncedFile{}, fmt.Errorf("create Bing image folder: %w", err)
	}
	relative := path.Join(folder, key+format.Extension)
	if _, err := staged.Commit(relative, 0o640); err != nil {
		if errors.Is(err, storage.ErrExists) {
			resolved, resolveErr := service.store.ResolveExisting(relative)
			if resolveErr == nil {
				_, resolveErr = imageproc.DetectFile(resolved)
			}
			if resolveErr == nil {
				return syncedFile{relative: relative, downloaded: true}, nil
			}
		}
		return syncedFile{}, fmt.Errorf("publish Bing image: %w", err)
	}
	return syncedFile{relative: relative, created: true, downloaded: true}, nil
}

func (service *Service) usableStoredFile(relative string) (bool, error) {
	resolved, err := service.store.ResolveManagedFileReference(relative, "bing")
	if errors.Is(err, storage.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("resolve stored Bing image: %w", err)
	}
	if _, err := imageproc.DetectFile(resolved); err != nil {
		return false, fmt.Errorf("validate stored Bing image: %w", err)
	}
	return true, nil
}

func (service *Service) downloadURL(source string) (*url.URL, error) {
	if source == "" {
		return nil, errors.New("build Bing image URL: source URL is required")
	}
	parsed, err := url.Parse(source)
	if err != nil || parsed.User != nil || parsed.Fragment != "" {
		return nil, errors.New("build Bing image URL: invalid source URL")
	}
	resolved := service.downloadBase.ResolveReference(parsed)
	if resolved.Scheme != service.downloadBase.Scheme || resolved.Host != service.downloadBase.Host {
		return nil, errors.New("build Bing image URL: source changed origin")
	}
	query := resolved.Query()
	query.Del("w")
	query.Del("h")
	resolved.RawQuery = query.Encode()
	return resolved, nil
}

func (service *Service) record(metadata Image, now time.Time) repository.BingImage {
	return repository.BingImage{
		StartDate: optionalString(metadata.StartDate), FullStartDate: optionalString(metadata.FullStartDate),
		EndDate: optionalString(metadata.EndDate), URL: metadata.URL, URLBase: optionalString(metadata.URLBase),
		Copyright: optionalString(metadata.Copyright), CopyrightLink: optionalString(metadata.CopyrightLink),
		Quiz: optionalString(metadata.Quiz), WP: metadata.WP, Hash: optionalString(metadata.Hash),
		Dark: optionalInt(metadata.Dark), Top: optionalInt(metadata.Top), Bottom: optionalInt(metadata.Bottom),
		Hotspots: encodeHotspots(metadata.Hotspots), CreatedAt: now, UpdatedAt: now,
	}
}

func encodeHotspots(hotspots []json.RawMessage) []string {
	result := make([]string, 0, len(hotspots))
	for _, hotspot := range hotspots {
		var text string
		if json.Unmarshal(hotspot, &text) == nil {
			result = append(result, text)
			continue
		}
		var compact bytes.Buffer
		if json.Compact(&compact, hotspot) == nil {
			result = append(result, compact.String())
		}
	}
	return result
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func optionalInt(value int64) *int64 {
	return &value
}

func archiveYear(image Image) string {
	for _, value := range []struct {
		date   string
		length int
	}{{image.StartDate, 8}, {image.FullStartDate, 12}} {
		if len(value.date) != value.length {
			continue
		}
		valid := true
		for _, character := range value.date {
			if character < '0' || character > '9' {
				valid = false
				break
			}
		}
		if valid {
			return value.date[:4]
		}
	}
	return "unknown"
}

func archiveKey(sourceURL string) string {
	digest := sha256.Sum256([]byte(sourceURL))
	return hex.EncodeToString(digest[:])
}

func (service *Service) newID() (string, error) {
	value := make([]byte, 16)
	if _, err := io.ReadFull(service.random, value); err != nil {
		return "", fmt.Errorf("generate Bing image ID: %w", err)
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return hex.EncodeToString(value), nil
}
