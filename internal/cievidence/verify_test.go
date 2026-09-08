package cievidence

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kaven.xyz/kaven/kaven-media-server/internal/buildinfo"
)

const fixtureRevision = "0123456789abcdef0123456789abcdef01234567"

func TestVerifyAcceptsCompleteNativeEvidence(t *testing.T) {
	directory := writeFixture(t)
	report, err := Verify(directory, fixtureRevision)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Accepted || report.Repository != "owner/repository" || report.Revision != fixtureRevision || report.RunID != "123" || report.RunAttempt != "2" {
		t.Fatalf("verification report = %#v", report)
	}
	if strings.Join(report.Architectures, ",") != "amd64,arm64" {
		t.Fatalf("architectures = %v", report.Architectures)
	}
}

func TestVerifyRejectsTamperedEvidence(t *testing.T) {
	directory := writeFixture(t)
	path := filepath.Join(directory, "container-arm64.json")
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(contents, ' '), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(directory, fixtureRevision); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("tampered evidence error = %v", err)
	}
}

func TestVerifyRejectsWrongCandidateAndFailedChecks(t *testing.T) {
	directory := writeFixture(t)
	if _, err := Verify(directory, strings.Repeat("a", 40)); err == nil || !strings.Contains(err.Error(), "proposed commit") {
		t.Fatalf("candidate mismatch error = %v", err)
	}

	var quality qualityEvidence
	readFixtureJSON(t, directory, "quality.json", &quality)
	quality.Checks["race"] = "failed"
	writeFixtureJSON(t, directory, "quality.json", quality)
	writeChecksums(t, directory)
	if _, err := Verify(directory, fixtureRevision); err == nil || !strings.Contains(err.Error(), "required check") {
		t.Fatalf("failed check error = %v", err)
	}
}

func TestVerifyRequiresNativeArchitectureEvidence(t *testing.T) {
	directory := writeFixture(t)
	var container containerEvidence
	readFixtureJSON(t, directory, "container-arm64.json", &container)
	container.Runner.Architecture = "X64"
	writeFixtureJSON(t, directory, "container-arm64.json", container)
	writeChecksums(t, directory)
	if _, err := Verify(directory, fixtureRevision); err == nil || !strings.Contains(err.Error(), "native Linux runner") {
		t.Fatalf("non-native runner error = %v", err)
	}
}

func writeFixture(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	id := identity{
		SchemaVersion: SchemaVersion, Repository: "owner/repository", Revision: fixtureRevision,
		RunID: "123", RunAttempt: "2",
	}
	writeFixtureJSON(t, directory, "manifest.json", manifest{
		identity: id, RunURL: "https://github.example/owner/repository/actions/runs/123",
		RequiredFiles: []string{"quality.json", "container-amd64.json", "container-arm64.json"},
	})
	writeFixtureJSON(t, directory, "quality.json", qualityEvidence{
		identity: id, Runner: runner{OS: "Linux", Architecture: "X64"}, GoVersion: "go1.25.0",
		Checks: map[string]string{"format": "passed", "tests": "passed", "race": "passed", "vet": "passed"},
	})
	for _, architecture := range []string{"amd64", "arm64"} {
		writeFixtureJSON(t, directory, "container-"+architecture+".json", containerEvidence{
			identity: id, TargetArchitecture: architecture,
			Runner: runner{OS: "Linux", Architecture: map[string]string{"amd64": "X64", "arm64": "ARM64"}[architecture]},
			Image: imageEvidence{
				ID: "sha256:" + strings.Repeat("a", 64), Platform: "linux/" + architecture, Revision: fixtureRevision,
				Version: buildinfo.Info{Version: "ci", Revision: fixtureRevision, GoVersion: "go1.25.0"},
			},
			Checks: map[string]string{
				"build": "passed", "runtimeCodecs": "passed", "architecture": "passed", "identity": "passed", "httpSmoke": "passed",
			},
		})
	}
	writeChecksums(t, directory)
	return directory
}

func writeFixtureJSON(t *testing.T, directory, name string, value any) {
	t.Helper()
	contents, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, name), append(contents, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readFixtureJSON(t *testing.T, directory, name string, destination any) {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join(directory, name))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(contents, destination); err != nil {
		t.Fatal(err)
	}
}

func writeChecksums(t *testing.T, directory string) {
	t.Helper()
	var output strings.Builder
	for _, name := range evidenceFiles {
		contents, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(contents)
		fmt.Fprintf(&output, "%s  %s\n", hex.EncodeToString(sum[:]), name)
	}
	if err := os.WriteFile(filepath.Join(directory, "SHA256SUMS"), []byte(output.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}
