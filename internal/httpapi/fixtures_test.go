package httpapi

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const legacyFixtureRoot = "testdata/legacy"

type contractFixture struct {
	Name     string           `json:"name"`
	Request  contractRequest  `json:"request"`
	Response contractResponse `json:"response"`
}

type contractRequest struct {
	Method         string `json:"method"`
	Path           string `json:"path"`
	Authentication string `json:"authentication"`
	MultipartField string `json:"multipartField,omitempty"`
}

type contractResponse struct {
	Status      int    `json:"status"`
	ContentType string `json:"contentType,omitempty"`
	Body        string `json:"body,omitempty"`
}

type assetFixture struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	MediaType string `json:"mediaType"`
	Size      int64  `json:"size"`
	SHA1      string `json:"sha1"`
}

func TestLegacyContractFixtures(t *testing.T) {
	var contracts []contractFixture
	decodeFixture(t, "contracts.json", &contracts)
	if len(contracts) == 0 {
		t.Fatal("contract fixture list is empty")
	}

	names := make(map[string]bool, len(contracts))
	referencedResponses := make(map[string]bool)
	for _, contract := range contracts {
		t.Run(contract.Name, func(t *testing.T) {
			if contract.Name == "" {
				t.Fatal("contract name is empty")
			}
			if names[contract.Name] {
				t.Fatalf("duplicate contract name %q", contract.Name)
			}
			names[contract.Name] = true

			if contract.Request.Method == "" || !strings.HasPrefix(contract.Request.Path, "/") {
				t.Fatalf("invalid request: %#v", contract.Request)
			}
			switch contract.Request.Authentication {
			case "none", "digest", "referer-policy", "per-root":
			default:
				t.Fatalf("unknown authentication policy %q", contract.Request.Authentication)
			}
			if contract.Response.Status < 100 || contract.Response.Status > 599 {
				t.Fatalf("invalid response status %d", contract.Response.Status)
			}
			if contract.Response.ContentType != "" {
				if _, _, err := mime.ParseMediaType(contract.Response.ContentType); err != nil {
					t.Fatalf("parse content type: %v", err)
				}
			}
			if contract.Response.Body == "" {
				if contract.Response.ContentType != "" {
					t.Fatal("bodyless response unexpectedly declares a content type")
				}
				return
			}

			body := readRelativeFixture(t, contract.Response.Body)
			if strings.HasPrefix(contract.Response.ContentType, "application/json") && !json.Valid(body) {
				t.Fatalf("response body %q is not valid JSON", contract.Response.Body)
			}
			if strings.HasPrefix(filepath.ToSlash(contract.Response.Body), "responses/") {
				referencedResponses[filepath.Clean(contract.Response.Body)] = true
			}
		})
	}

	entries, err := os.ReadDir(filepath.Join(legacyFixtureRoot, "responses"))
	if err != nil {
		t.Fatalf("read response fixtures: %v", err)
	}
	for _, entry := range entries {
		path := filepath.Join("responses", entry.Name())
		if !entry.IsDir() && !referencedResponses[path] {
			t.Errorf("response fixture %q is not referenced by a contract", path)
		}
	}
}

func TestLegacyAssetFixtures(t *testing.T) {
	var assets []assetFixture
	decodeFixture(t, "assets.json", &assets)
	if len(assets) == 0 {
		t.Fatal("asset fixture list is empty")
	}

	names := make(map[string]bool, len(assets))
	for _, asset := range assets {
		t.Run(asset.Name, func(t *testing.T) {
			if asset.Name == "" || names[asset.Name] {
				t.Fatalf("empty or duplicate asset name %q", asset.Name)
			}
			names[asset.Name] = true

			if _, _, err := mime.ParseMediaType(asset.MediaType); err != nil {
				t.Fatalf("parse media type: %v", err)
			}
			contents := readRelativeFixture(t, asset.Path)
			if int64(len(contents)) != asset.Size {
				t.Fatalf("size = %d, want %d", len(contents), asset.Size)
			}
			digest := sha1.Sum(contents)
			if got := hex.EncodeToString(digest[:]); got != asset.SHA1 {
				t.Fatalf("SHA-1 = %q, want %q", got, asset.SHA1)
			}
		})
	}
}

func decodeFixture(t *testing.T, name string, target any) {
	t.Helper()
	contents := readRelativeFixture(t, name)
	decoder := json.NewDecoder(strings.NewReader(string(contents)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
}

func readRelativeFixture(t *testing.T, name string) []byte {
	t.Helper()
	clean := filepath.Clean(name)
	if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		t.Fatalf("invalid fixture path %q", name)
	}
	contents, err := os.ReadFile(filepath.Join(legacyFixtureRoot, clean))
	if err != nil {
		t.Fatalf("read fixture %q: %v", name, err)
	}
	return contents
}
