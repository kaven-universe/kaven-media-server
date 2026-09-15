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
}
