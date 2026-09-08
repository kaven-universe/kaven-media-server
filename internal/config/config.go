package config

import (
	"fmt"
	"kaven.xyz/kaven/kaven-media-server/internal/referer"
	"os"
	"strconv"
	"time"
)

const (
	DefaultMaxFileCount         = 100
	DefaultMaxImageFileSize     = int64(100 * 1024 * 1024)
	DefaultMaxHFSFileSize       = int64(1000 * 1024 * 1024)
	DefaultImageWorkers         = 4
	DefaultAccessQueueSize      = 256
	DefaultAccessWriteTimeout   = 2 * time.Second
	DefaultAccessDrainTimeout   = 5 * time.Second
	DefaultDownloadQueueSize    = 256
	DefaultDownloadWriteTimeout = 2 * time.Second
	DefaultDownloadDrainTimeout = 5 * time.Second
	DefaultBingSyncInterval     = 24 * time.Hour
)

type Config struct {
	Listen             string
	DataDir            string
	PublicUploads      bool
	Admin              AdminCredentials
	HFSRoots           []HFSRoot
	AllowedDomainNames []string
}

func AllowedDomainNamesFromEnvironment() ([]string, error) {
	domains, err := referer.Parse(os.Getenv("KAVEN_ALLOWED_DOMAIN_NAMES"))
	if err != nil {
		return nil, fmt.Errorf("KAVEN_ALLOWED_DOMAIN_NAMES: %w", err)
	}
	return domains, nil
}

func EnvBool(name string, fallback bool) (bool, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("parse %s as boolean: %w", name, err)
	}
	return parsed, nil
}

func Env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
