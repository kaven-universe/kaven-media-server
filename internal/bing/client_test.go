package bing

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestClientFetchesLegacyMetadataShape(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/HPImageArchive.aspx" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		if got := request.URL.Query(); got.Get("format") != "js" || got.Get("idx") != "2" || got.Get("n") != "3" || got.Get("uhd") != "1" {
			t.Errorf("query = %v", got)
		}
		if request.Header.Get("Accept") != "application/json" || request.Header.Get("User-Agent") != "Kaven-Media-Server" {
			t.Errorf("headers = %#v", request.Header)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"images":[{
			"startdate":"20260904","fullstartdate":"202609040700","enddate":"20260905",
			"url":"/th?id=OHR.Example.jpg&w=1920&h=1080","urlbase":"/th?id=OHR.Example",
			"copyright":"Example","copyrightlink":"https://example.test/copyright","quiz":"/quiz",
			"wp":true,"hsh":"hash","drk":1,"top":2,"bot":3,
			"hs":[{"desc":"hotspot"}]
		}]}`)
	}))
	defer server.Close()
	client := newTestClient(t, server.Client(), server.URL+"/HPImageArchive.aspx", 1)

	images, err := client.Fetch(context.Background(), 2, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 1 {
		t.Fatalf("images = %#v", images)
	}
	want := Image{
		StartDate: "20260904", FullStartDate: "202609040700", EndDate: "20260905",
		URL: "/th?id=OHR.Example.jpg&w=1920&h=1080", URLBase: "/th?id=OHR.Example",
		Copyright: "Example", CopyrightLink: "https://example.test/copyright", Quiz: "/quiz",
		WP: true, Hash: "hash", Dark: 1, Top: 2, Bottom: 3,
		Hotspots: []json.RawMessage{json.RawMessage(`{"desc":"hotspot"}`)},
	}
	if !reflect.DeepEqual(images[0], want) {
		t.Fatalf("image = %#v", images[0])
	}
}

func TestClientRetriesTransientStatusWithExponentialBackoff(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) < 3 {
			http.Error(writer, "temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(writer, `{"images":[]}`)
	}))
	defer server.Close()
	client := newTestClient(t, server.Client(), server.URL, 3)
	delays := make([]time.Duration, 0, 2)
	client.sleep = func(_ context.Context, delay time.Duration) error {
		delays = append(delays, delay)
		return nil
	}

	images, err := client.Fetch(context.Background(), 0, 100)
	if err != nil || len(images) != 0 || calls.Load() != 3 {
		t.Fatalf("images = %#v, calls = %d, error = %v", images, calls.Load(), err)
	}
	if !reflect.DeepEqual(delays, []time.Duration{time.Millisecond, 2 * time.Millisecond}) {
		t.Fatalf("retry delays = %v", delays)
	}
}

func TestClientBoundsRetryAfter(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			writer.Header().Set("Retry-After", "120")
			http.Error(writer, "slow down", http.StatusTooManyRequests)
			return
		}
		_, _ = io.WriteString(writer, `{"images":[]}`)
	}))
	defer server.Close()
	client := newTestClient(t, server.Client(), server.URL, 2)
	var delay time.Duration
	client.sleep = func(_ context.Context, value time.Duration) error {
		delay = value
		return nil
	}
	if _, err := client.Fetch(context.Background(), 0, 1); err != nil {
		t.Fatal(err)
	}
	if delay != MaxRetryDelay {
		t.Fatalf("retry delay = %v, want %v", delay, MaxRetryDelay)
	}
}

func TestClientDoesNotRetryPermanentResponses(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.Error(writer, "bad request", http.StatusBadRequest)
	}))
	defer server.Close()
	client := newTestClient(t, server.Client(), server.URL, 3)
	_, err := client.Fetch(context.Background(), 0, 1)
	var statusErr *StatusError
	if !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusBadRequest || statusErr.Body != "bad request" || calls.Load() != 1 {
		t.Fatalf("calls = %d, status error = %#v, error = %v", calls.Load(), statusErr, err)
	}
}

func TestClientRetriesAttemptTimeouts(t *testing.T) {
	var calls atomic.Int32
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		<-request.Context().Done()
		return nil, request.Context().Err()
	})}
	client := newTestClient(t, httpClient, "https://example.test/archive", 2)
	client.attemptTimeout = 5 * time.Millisecond
	client.sleep = func(context.Context, time.Duration) error { return nil }
	_, err := client.Fetch(context.Background(), 0, 1)
	if !errors.Is(err, context.DeadlineExceeded) || calls.Load() != 2 {
		t.Fatalf("calls = %d, error = %v", calls.Load(), err)
	}
}

func TestClientStopsRetryBackoffWhenCallerCancels(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.Error(writer, "temporarily unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	client := newTestClient(t, server.Client(), server.URL, 3)
	ctx, cancel := context.WithCancel(context.Background())
	client.sleep = func(ctx context.Context, _ time.Duration) error {
		cancel()
		return ctx.Err()
	}
	_, err := client.Fetch(ctx, 0, 1)
	if !errors.Is(err, context.Canceled) || calls.Load() != 1 {
		t.Fatalf("calls = %d, error = %v", calls.Load(), err)
	}
}

func TestClientRejectsUnboundedAndMalformedMetadata(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "oversized body", body: strings.Repeat("x", MaxResponseBytes+1)},
		{name: "malformed JSON", body: `{"images":[`},
		{name: "missing URL", body: `{"images":[{"hsh":"hash"}]}`},
		{name: "oversized field", body: `{"images":[{"url":"` + strings.Repeat("x", MaxMetadataTextBytes+1) + `"}]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				_, _ = io.WriteString(writer, test.body)
			}))
			defer server.Close()
			client := newTestClient(t, server.Client(), server.URL, 3)
			if _, err := client.Fetch(context.Background(), 0, 1); err == nil || calls.Load() != 1 {
				t.Fatalf("calls = %d, error = %v", calls.Load(), err)
			}
		})
	}
}

func TestClientValidatesOptionsAndFetchWindow(t *testing.T) {
	for _, options := range []Options{
		{Endpoint: "://bad"},
		{Endpoint: "file:///archive"},
		{Endpoint: "https://user@example.test/archive"},
		{Endpoint: "https://example.test/archive#fragment"},
		{AttemptTimeout: -time.Second},
		{MaxAttempts: -1},
		{MaxAttempts: MaxAttemptsLimit + 1},
		{RetryDelay: -time.Second},
	} {
		if _, err := NewClient(options); err == nil {
			t.Errorf("options %#v succeeded", options)
		}
	}
	client := newTestClient(t, &http.Client{}, "https://example.test/archive", 1)
	for _, window := range [][2]int{{-1, 1}, {0, 0}, {0, MaxArchiveImages + 1}} {
		if _, err := client.Fetch(context.Background(), window[0], window[1]); err == nil {
			t.Errorf("window %v succeeded", window)
		}
	}
}

func newTestClient(t *testing.T, httpClient *http.Client, endpoint string, attempts int) *Client {
	t.Helper()
	client, err := NewClient(Options{
		Endpoint: endpoint, HTTPClient: httpClient, AttemptTimeout: time.Second,
		MaxAttempts: attempts, RetryDelay: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}
