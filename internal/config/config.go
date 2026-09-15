package config

import (
	"fmt"
	"io/fs"
	"os"
	"strings"
	"time"

	"kaven.xyz/kaven/kaven-media-server/internal/referer"
)

const (
	DefaultPublicUploads         = true
	DefaultMaxFileCount          = 100
	DefaultMaxImageFileSize      = int64(100 * 1024 * 1024)
	DefaultMaxHFSFileSize        = int64(1000 * 1024 * 1024)
	DefaultImageWorkers          = 4
	DefaultAccessQueueSize       = 256
	DefaultAccessWriteTimeout    = 2 * time.Second
	DefaultAccessDrainTimeout    = 5 * time.Second
	DefaultDownloadQueueSize     = 256
	DefaultDownloadWriteTimeout  = 2 * time.Second
	DefaultDownloadDrainTimeout  = 5 * time.Second
	DefaultBingSyncIntervalHours = 24
	MaxBingSyncIntervalHours     = 24 * 365
	DefaultUploadDirectory       = "upload"
	DefaultDownloadDirectory     = "download"
)

type Config struct {
	Listen                string
	DataDir               string
	UploadDirectory       string
	DownloadDirectory     string
	PublicUploads         bool
	MaxFileCount          int
	MaxImageFileSize      int64
	MaxHFSFileSize        int64
	RememberDurationDays  int
	BingSyncEnabled       bool
	BingSyncIntervalHours int
	AllowedDomainNames    []string
	HFSRoots              []HFSRoot
	Admin                 AdminCredentials
}

type RuntimeSettings struct {
	UploadDirectory       string    `json:"uploadDirectory"`
	DownloadDirectory     string    `json:"downloadDirectory"`
	PublicUploads         bool      `json:"publicUploads"`
	MaxFileCount          int       `json:"maxFileCount"`
	MaxImageFileSize      int64     `json:"maxImageFileSize"`
	MaxHFSFileSize        int64     `json:"maxHFSFileSize"`
	RememberDurationDays  int       `json:"rememberDurationDays"`
	BingSyncEnabled       bool      `json:"bingSyncEnabled"`
	BingSyncIntervalHours int       `json:"bingSyncIntervalHours"`
	AllowedDomainNames    []string  `json:"allowedDomainNames"`
	HFSRoots              []HFSRoot `json:"hfsRoots"`
}

func (configuration *Config) ApplyRuntimeSettings(settings RuntimeSettings) {
	configuration.UploadDirectory = settings.UploadDirectory
	configuration.DownloadDirectory = settings.DownloadDirectory
	configuration.PublicUploads = settings.PublicUploads
	configuration.MaxFileCount = settings.MaxFileCount
	configuration.MaxImageFileSize = settings.MaxImageFileSize
	configuration.MaxHFSFileSize = settings.MaxHFSFileSize
	configuration.RememberDurationDays = settings.RememberDurationDays
	configuration.BingSyncEnabled = settings.BingSyncEnabled
	configuration.BingSyncIntervalHours = settings.BingSyncIntervalHours
	configuration.AllowedDomainNames = settings.AllowedDomainNames
	configuration.HFSRoots = settings.HFSRoots
}

func DefaultRuntimeSettings(dataDir string) RuntimeSettings {
	return RuntimeSettings{
		UploadDirectory: DefaultUploadDirectory, DownloadDirectory: DefaultDownloadDirectory,
		PublicUploads: true, MaxFileCount: DefaultMaxFileCount,
		MaxImageFileSize: DefaultMaxImageFileSize, MaxHFSFileSize: DefaultMaxHFSFileSize,
		RememberDurationDays: 30, BingSyncEnabled: true,
		BingSyncIntervalHours: DefaultBingSyncIntervalHours,
		AllowedDomainNames:    []string{}, HFSRoots: DefaultHFSRoots(),
	}
}

func ValidateRuntimeSettings(settings RuntimeSettings) error {
	if err := ValidateMediaDirectories(settings.UploadDirectory, settings.DownloadDirectory); err != nil {
		return err
	}
	if settings.AllowedDomainNames == nil {
		return fmt.Errorf("allowedDomainNames must be an array")
	}
	if settings.HFSRoots == nil {
		return fmt.Errorf("hfsRoots must be an array")
	}
	if settings.MaxFileCount < 1 || settings.MaxFileCount > 1000 {
		return fmt.Errorf("maxFileCount must be between 1 and 1000")
	}
	if settings.MaxImageFileSize < 1024*1024 || settings.MaxImageFileSize > 1024*1024*1024 {
		return fmt.Errorf("maxImageFileSize must be between 1 MiB and 1 GiB")
	}
	if settings.MaxHFSFileSize < 1024*1024 || settings.MaxHFSFileSize > 1024*1024*1024*1024 {
		return fmt.Errorf("maxHFSFileSize must be between 1 MiB and 1 TiB")
	}
	if settings.RememberDurationDays < 1 || settings.RememberDurationDays > 365 {
		return fmt.Errorf("rememberDurationDays must be between 1 and 365")
	}
	if settings.BingSyncIntervalHours < 1 || settings.BingSyncIntervalHours > MaxBingSyncIntervalHours {
		return fmt.Errorf("bingSyncIntervalHours must be between 1 and %d", MaxBingSyncIntervalHours)
	}
	if len(settings.HFSRoots) > 64 {
		return fmt.Errorf("hfsRoots supports at most 64 roots")
	}
	if _, err := referer.New(settings.AllowedDomainNames); err != nil {
		return fmt.Errorf("allowedDomainNames: %w", err)
	}
	return nil
}

func ValidateMediaDirectories(uploadDirectory, downloadDirectory string) error {
	if err := validateMediaDirectory("uploadDirectory", uploadDirectory); err != nil {
		return err
	}
	if err := validateMediaDirectory("downloadDirectory", downloadDirectory); err != nil {
		return err
	}
	if directoriesOverlap(uploadDirectory, downloadDirectory) {
		return fmt.Errorf("uploadDirectory and downloadDirectory must not overlap")
	}
	return nil
}

func validateMediaDirectory(name, value string) error {
	if value == "" || value == "." || !fs.ValidPath(value) || strings.ContainsAny(value, `\\:`) {
		return fmt.Errorf("%s must be a canonical relative data path", name)
	}
	first := strings.SplitN(value, "/", 2)[0]
	if first == "cache" || first == "tmp" || first == "backup" || strings.HasPrefix(first, ".kaven-") {
		return fmt.Errorf("%s uses reserved data directory %q", name, first)
	}
	return nil
}

func directoriesOverlap(left, right string) bool {
	return left == right || strings.HasPrefix(left, right+"/") || strings.HasPrefix(right, left+"/")
}

func Env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
