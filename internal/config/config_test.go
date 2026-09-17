package config

import (
	"strings"
	"testing"
)

func TestEnv(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		fallback string
		want     string
	}{
		{name: "uses configured value", value: "127.0.0.1:5558", fallback: ":5558", want: "127.0.0.1:5558"},
		{name: "uses fallback when unset", fallback: "./data", want: "./data"},
		{name: "uses fallback when empty", value: "", fallback: "./data", want: "./data"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			const name = "KAVEN_TEST_VALUE"
			t.Setenv(name, test.value)
			if got := Env(name, test.fallback); got != test.want {
				t.Fatalf("Env(%q, %q) = %q, want %q", name, test.fallback, got, test.want)
			}
		})
	}
}

func TestValidateMediaDirectories(t *testing.T) {
	for _, test := range []struct {
		upload, download string
		valid            bool
	}{
		{upload: "upload", download: "download", valid: true},
		{upload: "media/uploads", download: "media/downloads", valid: true},
		{upload: "/absolute", download: "download"},
		{upload: "upload", download: `C:\\download`},
		{upload: "media", download: "media/downloads"},
		{upload: "cache/uploads", download: "download"},
	} {
		err := ValidateMediaDirectories(test.upload, test.download)
		if test.valid && err != nil {
			t.Errorf("%q, %q rejected: %v", test.upload, test.download, err)
		}
		if !test.valid && err == nil {
			t.Errorf("%q, %q accepted", test.upload, test.download)
		}
	}
	if err := ValidateMediaDirectories("upload", "tmp/files"); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("reserved directory error = %v", err)
	}
	if err := ValidateMediaDirectories("logs/uploads", "download"); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("log directory error = %v", err)
	}
}

func TestValidateRuntimeSettingsLoggingLimits(t *testing.T) {
	settings := DefaultRuntimeSettings(t.TempDir())
	for _, test := range []struct {
		name       string
		maxBytes   int64
		maxBackups int
		valid      bool
	}{
		{name: "defaults", maxBytes: DefaultLogMaxFileSize, maxBackups: DefaultLogMaxBackups, valid: true},
		{name: "minimum", maxBytes: 1024 * 1024, maxBackups: 1, valid: true},
		{name: "size too small", maxBytes: 1024*1024 - 1, maxBackups: 1},
		{name: "size too large", maxBytes: MaxLogMaxFileSize + 1, maxBackups: 1},
		{name: "unlimited backups", maxBytes: DefaultLogMaxFileSize, maxBackups: 0, valid: true},
		{name: "negative backups", maxBytes: DefaultLogMaxFileSize, maxBackups: -1},
		{name: "too many backups", maxBytes: DefaultLogMaxFileSize, maxBackups: MaxLogMaxBackups + 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			settings.LogMaxFileSize = test.maxBytes
			settings.LogMaxBackups = test.maxBackups
			err := ValidateRuntimeSettings(settings)
			if test.valid && err != nil {
				t.Fatalf("settings rejected: %v", err)
			}
			if !test.valid && err == nil {
				t.Fatal("invalid settings accepted")
			}
		})
	}
}

func TestValidateRuntimeSettingsTrustedProxyCIDRs(t *testing.T) {
	for _, test := range []struct {
		name   string
		values []string
		valid  bool
	}{
		{name: "empty", values: []string{}, valid: true},
		{name: "IPv4 and IPv6", values: []string{"172.17.0.0/16", "2001:db8::/32"}, valid: true},
		{name: "plain address", values: []string{"172.17.0.1"}},
		{name: "noncanonical network", values: []string{"172.17.0.1/16"}},
		{name: "duplicate", values: []string{"172.17.0.0/16", "172.17.0.0/16"}},
		{name: "all IPv4", values: []string{"0.0.0.0/0"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			settings := DefaultRuntimeSettings(t.TempDir())
			settings.TrustedProxyCIDRs = test.values
			err := ValidateRuntimeSettings(settings)
			if test.valid && err != nil {
				t.Fatalf("settings rejected: %v", err)
			}
			if !test.valid && err == nil {
				t.Fatal("invalid settings accepted")
			}
		})
	}
}
