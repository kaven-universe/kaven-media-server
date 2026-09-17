package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"kaven.xyz/kaven/kaven-media-server/internal/buildinfo"
	"kaven.xyz/kaven/kaven-media-server/internal/versioncheck"
)

type testReleaseChecker struct {
	release versioncheck.Release
	err     error
	calls   int
}

func (checker *testReleaseChecker) Latest(context.Context) (versioncheck.Release, error) {
	checker.calls++
	return checker.release, checker.err
}

func TestVersionHandlerShowsCurrentVersionWithoutExternalCheck(t *testing.T) {
	originalVersion, originalRevision := buildinfo.Version, buildinfo.Revision
	t.Cleanup(func() { buildinfo.Version, buildinfo.Revision = originalVersion, originalRevision })
	buildinfo.Version, buildinfo.Revision = "1.0.0-rc.1", "abcdef"
	checker := &testReleaseChecker{}
	response := httptest.NewRecorder()
	NewVersionHandler(checker).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/admin/version", nil))
	if response.Code != http.StatusOK || checker.calls != 0 {
		t.Fatalf("status = %d, checker calls = %d", response.Code, checker.calls)
	}
	var status VersionStatusResponse
	if err := json.Unmarshal(response.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.Current.Version != "1.0.0-rc.1" || status.Latest != nil || status.UpdateAvailable != nil {
		t.Fatalf("status = %#v", status)
	}
}

func TestVersionHandlerChecksForUpdate(t *testing.T) {
	originalVersion := buildinfo.Version
	t.Cleanup(func() { buildinfo.Version = originalVersion })
	buildinfo.Version = "1.0.0-rc.1"
	checker := &testReleaseChecker{release: versioncheck.Release{
		Version: "v1.0.0-rc.2", URL: "https://github.com/kaven-universe/kaven-media-server/releases/tag/v1.0.0-rc.2",
		PublishedAt: time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC),
	}}
	response := httptest.NewRecorder()
	NewVersionHandler(checker).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/admin/version?check", nil))
	var status VersionStatusResponse
	if err := json.Unmarshal(response.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || checker.calls != 1 || status.Latest == nil || status.UpdateAvailable == nil || !*status.UpdateAvailable {
		t.Fatalf("response = %d %#v, checker calls = %d", response.Code, status, checker.calls)
	}
}

func TestVersionHandlerReportsCheckFailure(t *testing.T) {
	response := httptest.NewRecorder()
	checker := &testReleaseChecker{err: errors.New("network unavailable")}
	NewVersionHandler(checker).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/admin/version?check", nil))
	if response.Code != http.StatusBadGateway || checker.calls != 1 {
		t.Fatalf("status = %d, checker calls = %d", response.Code, checker.calls)
	}
}
