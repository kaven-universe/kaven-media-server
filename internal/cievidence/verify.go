package cievidence

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"kaven.xyz/kaven/kaven-media-server/internal/buildinfo"
)

const (
	SchemaVersion        = 1
	MaxEvidenceFileBytes = 1 << 20
	MaxChecksumBytes     = 64 << 10
)

var (
	evidenceFiles       = []string{"container-amd64.json", "container-arm64.json", "manifest.json", "quality.json"}
	imageVersionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?$`)
)

type identity struct {
	SchemaVersion int    `json:"schemaVersion"`
	Repository    string `json:"repository"`
	Revision      string `json:"revision"`
	RunID         string `json:"runId"`
	RunAttempt    string `json:"runAttempt"`
}

type manifest struct {
	identity
	RunURL        string   `json:"runUrl"`
	RequiredFiles []string `json:"requiredFiles"`
}

type runner struct {
	OS           string `json:"os"`
	Architecture string `json:"architecture"`
}

type qualityEvidence struct {
	identity
	Runner    runner            `json:"runner"`
	GoVersion string            `json:"goVersion"`
	Checks    map[string]string `json:"checks"`
}

type imageEvidence struct {
	ID       string         `json:"id"`
	Platform string         `json:"platform"`
	Revision string         `json:"revision"`
	Version  buildinfo.Info `json:"version"`
}

type containerEvidence struct {
	identity
	TargetArchitecture string            `json:"targetArchitecture"`
	Runner             runner            `json:"runner"`
	Image              imageEvidence     `json:"image"`
	Checks             map[string]string `json:"checks"`
}

type Report struct {
	Accepted      bool     `json:"accepted"`
	Repository    string   `json:"repository"`
	Revision      string   `json:"revision"`
	RunID         string   `json:"runId"`
	RunAttempt    string   `json:"runAttempt"`
	RunURL        string   `json:"runUrl"`
	ImageVersion  string   `json:"imageVersion"`
	Architectures []string `json:"architectures"`
}

func Verify(directory, expectedRevision string) (Report, error) {
	expectedRevision = strings.TrimSpace(expectedRevision)
	if !fixedHex(expectedRevision, 40) {
		return Report{}, errors.New("expected revision must be a 40-character hexadecimal commit ID")
	}
	info, err := os.Stat(directory)
	if err != nil {
		return Report{}, fmt.Errorf("inspect CI evidence directory: %w", err)
	}
	if !info.IsDir() {
		return Report{}, errors.New("CI evidence path is not a directory")
	}
	checksums, err := readChecksums(directory)
	if err != nil {
		return Report{}, err
	}
	for _, name := range evidenceFiles {
		expected, exists := checksums[name]
		if !exists {
			return Report{}, fmt.Errorf("CI evidence checksum is missing %s", name)
		}
		actual, err := hashRegularFile(directory, name, MaxEvidenceFileBytes)
		if err != nil {
			return Report{}, err
		}
		if actual != expected {
			return Report{}, fmt.Errorf("CI evidence checksum mismatch for %s", name)
		}
	}

	var bundle manifest
	if err := readJSON(directory, "manifest.json", &bundle); err != nil {
		return Report{}, err
	}
	if err := validateIdentity(bundle.identity, expectedRevision); err != nil {
		return Report{}, fmt.Errorf("validate CI evidence manifest: %w", err)
	}
	if !sameNames(bundle.RequiredFiles, []string{"quality.json", "container-amd64.json", "container-arm64.json"}) {
		return Report{}, errors.New("CI evidence manifest has invalid required files")
	}
	if err := validateRunURL(bundle.RunURL, bundle.Repository, bundle.RunID); err != nil {
		return Report{}, err
	}

	var quality qualityEvidence
	if err := readJSON(directory, "quality.json", &quality); err != nil {
		return Report{}, err
	}
	if err := validateMatchingIdentity(quality.identity, bundle.identity); err != nil {
		return Report{}, fmt.Errorf("validate quality evidence: %w", err)
	}
	if quality.Runner.OS != "Linux" || quality.Runner.Architecture != "X64" || quality.GoVersion == "" {
		return Report{}, errors.New("quality evidence has incomplete runner or Go identity")
	}
	if !passedChecks(quality.Checks, []string{"format", "tests", "race", "vet"}) {
		return Report{}, errors.New("quality evidence does not record every required check as passed")
	}

	var imageVersion string
	for _, architecture := range []string{"amd64", "arm64"} {
		var container containerEvidence
		name := "container-" + architecture + ".json"
		if err := readJSON(directory, name, &container); err != nil {
			return Report{}, err
		}
		if err := validateMatchingIdentity(container.identity, bundle.identity); err != nil {
			return Report{}, fmt.Errorf("validate %s evidence: %w", architecture, err)
		}
		if container.TargetArchitecture != architecture || container.Image.Platform != "linux/"+architecture {
			return Report{}, fmt.Errorf("%s evidence has the wrong target platform", architecture)
		}
		wantRunnerArchitecture := map[string]string{"amd64": "X64", "arm64": "ARM64"}[architecture]
		if container.Runner.OS != "Linux" || container.Runner.Architecture != wantRunnerArchitecture {
			return Report{}, fmt.Errorf("%s evidence was not produced by the expected native Linux runner", architecture)
		}
		if !strings.HasPrefix(container.Image.ID, "sha256:") || !fixedHex(strings.TrimPrefix(container.Image.ID, "sha256:"), sha256.Size*2) {
			return Report{}, fmt.Errorf("%s evidence has an invalid image ID", architecture)
		}
		if container.Image.Revision != bundle.Revision || container.Image.Version.Revision != bundle.Revision || container.Image.Version.Modified {
			return Report{}, fmt.Errorf("%s image identity does not match the evidence revision", architecture)
		}
		if !validImageVersion(container.Image.Version.Version) || container.Image.Version.GoVersion == "" {
			return Report{}, fmt.Errorf("%s image version identity is invalid", architecture)
		}
		if imageVersion == "" {
			imageVersion = container.Image.Version.Version
		} else if container.Image.Version.Version != imageVersion {
			return Report{}, errors.New("container image versions do not match")
		}
		if !passedChecks(container.Checks, []string{"build", "runtimeCodecs", "architecture", "identity", "httpSmoke"}) {
			return Report{}, fmt.Errorf("%s evidence does not record every required check as passed", architecture)
		}
	}

	return Report{
		Accepted: true, Repository: bundle.Repository, Revision: bundle.Revision,
		RunID: bundle.RunID, RunAttempt: bundle.RunAttempt, RunURL: bundle.RunURL,
		ImageVersion: imageVersion, Architectures: []string{"amd64", "arm64"},
	}, nil
}

func validImageVersion(value string) bool {
	return value == "ci" || imageVersionPattern.MatchString(value)
}

func (report Report) WriteJSON(writer io.Writer) error {
	if err := json.NewEncoder(writer).Encode(report); err != nil {
		return fmt.Errorf("write CI evidence verification report: %w", err)
	}
	return nil
}

func readChecksums(directory string) (map[string]string, error) {
	contents, err := readRegularFile(directory, "SHA256SUMS", MaxChecksumBytes)
	if err != nil {
		return nil, err
	}
	checksums := make(map[string]string, len(evidenceFiles))
	for _, line := range strings.Split(strings.TrimSpace(string(contents)), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) != 2 || !fixedHex(fields[0], sha256.Size*2) {
			return nil, errors.New("CI evidence contains an invalid checksum line")
		}
		name := strings.TrimPrefix(fields[1], "*")
		if !contains(evidenceFiles, name) {
			return nil, errors.New("CI evidence checksum names an unexpected file")
		}
		if _, duplicate := checksums[name]; duplicate {
			return nil, errors.New("CI evidence contains a duplicate checksum")
		}
		checksums[name] = strings.ToLower(fields[0])
	}
	if len(checksums) != len(evidenceFiles) {
		return nil, errors.New("CI evidence checksum inventory is incomplete")
	}
	return checksums, nil
}

func readJSON(directory, name string, destination any) error {
	contents, err := readRegularFile(directory, name, MaxEvidenceFileBytes)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode CI evidence %s: %w", name, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("decode CI evidence %s: trailing JSON data", name)
	}
	return nil
}

func readRegularFile(directory, name string, limit int64) ([]byte, error) {
	path := filepath.Join(directory, name)
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect CI evidence %s: %w", name, err)
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, fmt.Errorf("CI evidence %s is not a bounded regular file", name)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read CI evidence %s: %w", name, err)
	}
	return contents, nil
}

func hashRegularFile(directory, name string, limit int64) (string, error) {
	contents, err := readRegularFile(directory, name, limit)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(contents)
	return hex.EncodeToString(sum[:]), nil
}

func validateIdentity(value identity, expectedRevision string) error {
	if value.SchemaVersion != SchemaVersion || !validRepository(value.Repository) {
		return errors.New("invalid schema version or repository")
	}
	if value.Revision != expectedRevision {
		return errors.New("revision does not match the proposed commit")
	}
	if _, err := positiveInteger(value.RunID); err != nil {
		return errors.New("invalid run ID")
	}
	if _, err := positiveInteger(value.RunAttempt); err != nil {
		return errors.New("invalid run attempt")
	}
	return nil
}

func validRepository(value string) bool {
	parts := strings.Split(value, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return false
	}
	for _, part := range parts {
		if len(part) > 100 || part == "." || part == ".." {
			return false
		}
		for _, character := range part {
			if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
				(character >= '0' && character <= '9') || strings.ContainsRune("-_.", character) {
				continue
			}
			return false
		}
	}
	return true
}

func validateMatchingIdentity(value, expected identity) error {
	if value != expected {
		return errors.New("repository, revision, run, or schema identity does not match manifest")
	}
	return nil
}

func validateRunURL(raw, repository, runID string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("CI evidence manifest has an invalid run URL")
	}
	wantSuffix := "/" + repository + "/actions/runs/" + runID
	if !strings.HasSuffix(strings.TrimSuffix(parsed.Path, "/"), wantSuffix) {
		return errors.New("CI evidence run URL does not match its repository and run ID")
	}
	return nil
}

func passedChecks(actual map[string]string, required []string) bool {
	if len(actual) != len(required) {
		return false
	}
	for _, name := range required {
		if actual[name] != "passed" {
			return false
		}
	}
	return true
}

func sameNames(actual, expected []string) bool {
	if len(actual) != len(expected) {
		return false
	}
	seen := make(map[string]bool, len(actual))
	for _, name := range actual {
		if !contains(expected, name) || seen[name] {
			return false
		}
		seen[name] = true
	}
	return true
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func fixedHex(value string, length int) bool {
	if len(value) != length {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func positiveInteger(value string) (uint64, error) {
	result, err := strconv.ParseUint(value, 10, 64)
	if err != nil || result == 0 {
		return 0, errors.New("value is not a positive integer")
	}
	return result, nil
}
