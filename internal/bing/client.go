package bing

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
	DefaultEndpoint       = "https://cn.bing.com/HPImageArchive.aspx"
	DefaultAttemptTimeout = 10 * time.Second
	DefaultMaxAttempts    = 3
	DefaultRetryDelay     = 250 * time.Millisecond
	MaxRetryDelay         = 5 * time.Second
	MaxAttemptsLimit      = 10
	MaxArchiveImages      = 100
	MaxResponseBytes      = 2 * 1024 * 1024
	MaxMetadataTextBytes  = 8 * 1024
	MaxHotspots           = 256
)

type Image struct {
	StartDate     string            `json:"startdate"`
	FullStartDate string            `json:"fullstartdate"`
	EndDate       string            `json:"enddate"`
	URL           string            `json:"url"`
	URLBase       string            `json:"urlbase"`
	Copyright     string            `json:"copyright"`
	CopyrightLink string            `json:"copyrightlink"`
	Quiz          string            `json:"quiz"`
	WP            bool              `json:"wp"`
	Hash          string            `json:"hsh"`
	Dark          int64             `json:"drk"`
	Top           int64             `json:"top"`
	Bottom        int64             `json:"bot"`
	Hotspots      []json.RawMessage `json:"hs"`
}

type Options struct {
	Endpoint       string
	HTTPClient     *http.Client
	AttemptTimeout time.Duration
	MaxAttempts    int
	RetryDelay     time.Duration
}

type Client struct {
	endpoint       *url.URL
	httpClient     *http.Client
	attemptTimeout time.Duration
	maxAttempts    int
	retryDelay     time.Duration
	now            func() time.Time
	sleep          func(context.Context, time.Duration) error
}

type StatusError struct {
	StatusCode int
	Body       string
}

func (err *StatusError) Error() string {
	if err.Body == "" {
		return fmt.Sprintf("Bing metadata returned HTTP %d", err.StatusCode)
	}
	return fmt.Sprintf("Bing metadata returned HTTP %d: %s", err.StatusCode, err.Body)
}

func NewClient(options Options) (*Client, error) {
	endpoint := options.Endpoint
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.Fragment != "" {
		return nil, errors.New("create Bing metadata client: endpoint must be an absolute HTTP(S) URL without credentials or a fragment")
	}
	attemptTimeout := options.AttemptTimeout
	if attemptTimeout == 0 {
		attemptTimeout = DefaultAttemptTimeout
	}
	if attemptTimeout < 0 {
		return nil, errors.New("create Bing metadata client: attempt timeout must be positive")
	}
	maxAttempts := options.MaxAttempts
	if maxAttempts == 0 {
		maxAttempts = DefaultMaxAttempts
	}
	if maxAttempts < 1 || maxAttempts > MaxAttemptsLimit {
		return nil, fmt.Errorf("create Bing metadata client: max attempts must be between 1 and %d", MaxAttemptsLimit)
	}
	retryDelay := options.RetryDelay
	if retryDelay == 0 {
		retryDelay = DefaultRetryDelay
	}
	if retryDelay < 0 {
		return nil, errors.New("create Bing metadata client: retry delay must not be negative")
	}
	httpClient := options.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	return &Client{
		endpoint: parsed, httpClient: httpClient, attemptTimeout: attemptTimeout,
		maxAttempts: maxAttempts, retryDelay: retryDelay, now: time.Now, sleep: sleepContext,
	}, nil
}

func (client *Client) Fetch(ctx context.Context, index, count int) ([]Image, error) {
	if index < 0 {
		return nil, errors.New("fetch Bing metadata: index must not be negative")
	}
	if count < 1 || count > MaxArchiveImages {
		return nil, fmt.Errorf("fetch Bing metadata: count must be between 1 and %d", MaxArchiveImages)
	}
	var lastErr error
	for attempt := 0; attempt < client.maxAttempts; attempt++ {
		attemptCtx, cancel := context.WithTimeout(ctx, client.attemptTimeout)
		images, retry, retryAfter, err := client.fetchAttempt(attemptCtx, index, count)
		cancel()
		if err == nil {
			return images, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			return nil, fmt.Errorf("fetch Bing metadata: %w", ctx.Err())
		}
		if !retry || attempt+1 == client.maxAttempts {
			return nil, fmt.Errorf("fetch Bing metadata after %d attempt(s): %w", attempt+1, lastErr)
		}
		if err := client.sleep(ctx, client.backoff(attempt, retryAfter)); err != nil {
			return nil, fmt.Errorf("fetch Bing metadata retry: %w", err)
		}
	}
	return nil, fmt.Errorf("fetch Bing metadata: %w", lastErr)
}

func (client *Client) fetchAttempt(ctx context.Context, index, count int) ([]Image, bool, time.Duration, error) {
	target := *client.endpoint
	query := target.Query()
	query.Set("format", "js")
	query.Set("idx", strconv.Itoa(index))
	query.Set("n", strconv.Itoa(count))
	query.Set("uhd", "1")
	target.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, false, 0, fmt.Errorf("create Bing metadata request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "Kaven-Media-Server")
	response, err := client.httpClient.Do(request)
	if err != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		return nil, true, 0, fmt.Errorf("send Bing metadata request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 513))
		text := strings.TrimSpace(string(body))
		if len(text) > 512 {
			text = text[:512]
		}
		return nil, retryableStatus(response.StatusCode), parseRetryAfter(response.Header.Get("Retry-After"), client.now()), &StatusError{
			StatusCode: response.StatusCode, Body: text,
		}
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, MaxResponseBytes+1))
	if err != nil {
		return nil, true, 0, fmt.Errorf("read Bing metadata response: %w", err)
	}
	if len(body) > MaxResponseBytes {
		return nil, false, 0, fmt.Errorf("read Bing metadata response: body exceeds %d bytes", MaxResponseBytes)
	}
	var archive struct {
		Images []Image `json:"images"`
	}
	if err := json.Unmarshal(body, &archive); err != nil {
		return nil, false, 0, fmt.Errorf("decode Bing metadata response: %w", err)
	}
	if err := validateImages(archive.Images); err != nil {
		return nil, false, 0, err
	}
	return archive.Images, false, 0, nil
}

func validateImages(images []Image) error {
	if len(images) > MaxArchiveImages {
		return fmt.Errorf("validate Bing metadata response: contains more than %d images", MaxArchiveImages)
	}
	for index, image := range images {
		if image.URL == "" {
			return fmt.Errorf("validate Bing metadata response: image %d has no URL", index)
		}
		for name, value := range map[string]string{
			"startdate": image.StartDate, "fullstartdate": image.FullStartDate, "enddate": image.EndDate,
			"url": image.URL, "urlbase": image.URLBase, "copyright": image.Copyright,
			"copyrightlink": image.CopyrightLink, "quiz": image.Quiz, "hsh": image.Hash,
		} {
			if len(value) > MaxMetadataTextBytes {
				return fmt.Errorf("validate Bing metadata response: image %d field %s exceeds %d bytes", index, name, MaxMetadataTextBytes)
			}
		}
		if len(image.Hotspots) > MaxHotspots {
			return fmt.Errorf("validate Bing metadata response: image %d has too many hotspots", index)
		}
		for hotspotIndex, hotspot := range image.Hotspots {
			if len(hotspot) > MaxMetadataTextBytes {
				return fmt.Errorf("validate Bing metadata response: image %d hotspot %d exceeds %d bytes", index, hotspotIndex, MaxMetadataTextBytes)
			}
		}
	}
	return nil
}

func retryableStatus(status int) bool {
	return status == http.StatusRequestTimeout || status == http.StatusTooEarly || status == http.StatusTooManyRequests ||
		status >= http.StatusInternalServerError && status <= 599
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds > 0 {
		if seconds >= int64(MaxRetryDelay/time.Second) {
			return MaxRetryDelay
		}
		return time.Duration(seconds) * time.Second
	}
	if retryAt, err := http.ParseTime(value); err == nil {
		return min(max(retryAt.Sub(now), 0), MaxRetryDelay)
	}
	return 0
}

func (client *Client) backoff(attempt int, retryAfter time.Duration) time.Duration {
	if retryAfter > 0 {
		return retryAfter
	}
	delay := client.retryDelay
	for range attempt {
		if delay >= MaxRetryDelay/2 {
			return MaxRetryDelay
		}
		delay *= 2
	}
	return min(delay, MaxRetryDelay)
}

func sleepContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
