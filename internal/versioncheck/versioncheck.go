// Package versioncheck reads and compares published application releases.
package versioncheck

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultEndpoint  = "https://api.github.com/repos/kaven-universe/kaven-media-server/releases?per_page=20"
	maxResponseBytes = 256 * 1024
)

type Release struct {
	Version     string    `json:"version"`
	URL         string    `json:"url"`
	PublishedAt time.Time `json:"publishedAt"`
}

type Checker struct {
	client   *http.Client
	endpoint string
}

func New(client *http.Client) *Checker {
	if client == nil {
		client = &http.Client{
			Timeout: 10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}
	return &Checker{client: client, endpoint: DefaultEndpoint}
}

func (checker *Checker) Latest(ctx context.Context) (Release, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, checker.endpoint, nil)
	if err != nil {
		return Release{}, fmt.Errorf("create release request: %w", err)
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "kaven-media-server")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	response, err := checker.client.Do(request)
	if err != nil {
		return Release{}, fmt.Errorf("request releases: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxResponseBytes))
		return Release{}, fmt.Errorf("request releases: unexpected HTTP status %d", response.StatusCode)
	}

	var payload []struct {
		TagName     string    `json:"tag_name"`
		HTMLURL     string    `json:"html_url"`
		PublishedAt time.Time `json:"published_at"`
		Draft       bool      `json:"draft"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, maxResponseBytes+1))
	if err := decoder.Decode(&payload); err != nil {
		return Release{}, fmt.Errorf("decode releases: %w", err)
	}
	if len(payload) > 100 {
		return Release{}, errors.New("decode releases: too many entries")
	}

	var latest Release
	for _, item := range payload {
		if item.Draft || !validReleaseURL(item.HTMLURL) {
			continue
		}
		version, err := parse(item.TagName)
		if err != nil {
			continue
		}
		if latest.Version == "" {
			latest = Release{Version: item.TagName, URL: item.HTMLURL, PublishedAt: item.PublishedAt}
			continue
		}
		currentLatest, _ := parse(latest.Version)
		if compare(version, currentLatest) > 0 {
			latest = Release{Version: item.TagName, URL: item.HTMLURL, PublishedAt: item.PublishedAt}
		}
	}
	if latest.Version == "" {
		return Release{}, errors.New("no semantic release found")
	}
	return latest, nil
}

// Compare compares semantic versions and returns -1, 0, or 1.
func Compare(left, right string) (int, error) {
	a, err := parse(left)
	if err != nil {
		return 0, err
	}
	b, err := parse(right)
	if err != nil {
		return 0, err
	}
	return compare(a, b), nil
}

type semanticVersion struct {
	major, minor, patch uint64
	prerelease          []string
}

func parse(value string) (semanticVersion, error) {
	value = strings.TrimPrefix(strings.TrimSpace(value), "v")
	value = strings.SplitN(value, "+", 2)[0]
	core, prerelease, hasPrerelease := strings.Cut(value, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return semanticVersion{}, fmt.Errorf("invalid semantic version %q", value)
	}
	numbers := make([]uint64, 3)
	for index, part := range parts {
		if part == "" || (len(part) > 1 && part[0] == '0') {
			return semanticVersion{}, fmt.Errorf("invalid semantic version %q", value)
		}
		number, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return semanticVersion{}, fmt.Errorf("invalid semantic version %q", value)
		}
		numbers[index] = number
	}
	result := semanticVersion{major: numbers[0], minor: numbers[1], patch: numbers[2]}
	if !hasPrerelease {
		return result, nil
	}
	result.prerelease = strings.Split(prerelease, ".")
	for _, identifier := range result.prerelease {
		if identifier == "" {
			return semanticVersion{}, fmt.Errorf("invalid semantic version %q", value)
		}
		numeric := true
		for _, character := range identifier {
			if (character < '0' || character > '9') && (character < 'A' || character > 'Z') && (character < 'a' || character > 'z') && character != '-' {
				return semanticVersion{}, fmt.Errorf("invalid semantic version %q", value)
			}
			if character < '0' || character > '9' {
				numeric = false
			}
		}
		if numeric && len(identifier) > 1 && identifier[0] == '0' {
			return semanticVersion{}, fmt.Errorf("invalid semantic version %q", value)
		}
	}
	return result, nil
}

func compare(left, right semanticVersion) int {
	for _, pair := range [][2]uint64{{left.major, right.major}, {left.minor, right.minor}, {left.patch, right.patch}} {
		if pair[0] < pair[1] {
			return -1
		}
		if pair[0] > pair[1] {
			return 1
		}
	}
	if len(left.prerelease) == 0 && len(right.prerelease) == 0 {
		return 0
	}
	if len(left.prerelease) == 0 {
		return 1
	}
	if len(right.prerelease) == 0 {
		return -1
	}
	for index := 0; index < len(left.prerelease) && index < len(right.prerelease); index++ {
		a, b := left.prerelease[index], right.prerelease[index]
		if a == b {
			continue
		}
		aNumber, aErr := strconv.ParseUint(a, 10, 64)
		bNumber, bErr := strconv.ParseUint(b, 10, 64)
		switch {
		case aErr == nil && bErr == nil:
			if aNumber < bNumber {
				return -1
			}
			return 1
		case aErr == nil:
			return -1
		case bErr == nil:
			return 1
		case a < b:
			return -1
		default:
			return 1
		}
	}
	if len(left.prerelease) < len(right.prerelease) {
		return -1
	}
	if len(left.prerelease) > len(right.prerelease) {
		return 1
	}
	return 0
}

func validReleaseURL(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme == "https" && parsed.Host == "github.com" && strings.HasPrefix(parsed.Path, "/kaven-universe/kaven-media-server/releases/")
}
