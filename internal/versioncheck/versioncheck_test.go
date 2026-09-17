package versioncheck

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCompareSemanticVersions(t *testing.T) {
	tests := []struct {
		left, right string
		want        int
	}{
		{left: "v1.0.0-rc.2", right: "v1.0.0-rc.10", want: -1},
		{left: "1.0.0-rc.10", right: "1.0.0", want: -1},
		{left: "v1.0.0", right: "1.0.0", want: 0},
		{left: "2.0.0", right: "1.99.99", want: 1},
	}
	for _, test := range tests {
		got, err := Compare(test.left, test.right)
		if err != nil || got != test.want {
			t.Errorf("Compare(%q, %q) = %d, %v; want %d", test.left, test.right, got, err, test.want)
		}
	}
	for _, invalid := range []string{"dev", "1.0", "01.0.0", "1.0.0-", "1.0.0-rc.01"} {
		if _, err := Compare(invalid, "1.0.0"); err == nil {
			t.Errorf("Compare(%q, ...) accepted an invalid version", invalid)
		}
	}
}

func TestCheckerSelectsNewestSemanticRelease(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("User-Agent") != "kaven-media-server" || request.Header.Get("Accept") == "" {
			t.Error("release request headers are missing")
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`[
			{"tag_name":"v1.0.0-rc.9","html_url":"https://github.com/kaven-universe/kaven-media-server/releases/tag/v1.0.0-rc.9","published_at":"2026-09-01T00:00:00Z"},
			{"tag_name":"v1.0.0-rc.10","html_url":"https://github.com/kaven-universe/kaven-media-server/releases/tag/v1.0.0-rc.10","published_at":"2026-09-02T00:00:00Z"},
			{"tag_name":"v9.0.0","html_url":"https://example.com/untrusted","published_at":"2026-09-03T00:00:00Z"},
			{"tag_name":"v2.0.0","html_url":"https://github.com/kaven-universe/kaven-media-server/releases/tag/v2.0.0","draft":true,"published_at":"2026-09-04T00:00:00Z"}
		]`))
	}))
	defer server.Close()
	checker := New(server.Client())
	checker.endpoint = server.URL
	release, err := checker.Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if release.Version != "v1.0.0-rc.10" || release.URL == "" || release.PublishedAt.IsZero() {
		t.Fatalf("release = %#v", release)
	}
}

func TestCheckerRejectsBadResponses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		http.Error(writer, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	checker := New(server.Client())
	checker.endpoint = server.URL
	if _, err := checker.Latest(context.Background()); err == nil {
		t.Fatal("expected release request failure")
	}
}
