package hfsdownload

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"kaven.xyz/kaven/kaven-media-server/internal/repository"
)

const (
	MaxFilePathLength    = 8 * 1024
	MaxOriginalURLLength = 8 * 1024
	MaxIPAddressLength   = 128
	MaxUserAgentLength   = 1024
)

type Repository interface {
	Create(context.Context, repository.DownloadRecord) error
}

type Recorder struct {
	repository   Repository
	writeTimeout time.Duration
	now          func() time.Time
	random       io.Reader
	events       chan event
	done         chan struct{}
	cancel       context.CancelFunc

	mutex   sync.RWMutex
	closed  bool
	dropped atomic.Uint64
}

type event struct {
	file        string
	ip          string
	originalURL string
	userAgent   string
	timestamp   time.Time
}

func NewRecorder(repository Repository, capacity int, writeTimeout time.Duration) (*Recorder, error) {
	if repository == nil {
		return nil, errors.New("create HFS download recorder: repository is required")
	}
	if capacity < 1 {
		return nil, errors.New("create HFS download recorder: capacity must be positive")
	}
	if writeTimeout <= 0 {
		return nil, errors.New("create HFS download recorder: write timeout must be positive")
	}
	ctx, cancel := context.WithCancel(context.Background())
	recorder := &Recorder{
		repository: repository, writeTimeout: writeTimeout, now: time.Now, random: rand.Reader,
		events: make(chan event, capacity), done: make(chan struct{}), cancel: cancel,
	}
	go recorder.run(ctx)
	return recorder, nil
}

func (recorder *Recorder) Record(file, originalURL, ip, userAgent string) bool {
	if file == "" || len(file) > MaxFilePathLength || originalURL == "" || len(originalURL) > MaxOriginalURLLength ||
		ip == "" || len(ip) > MaxIPAddressLength || len(userAgent) > MaxUserAgentLength {
		recorder.dropped.Add(1)
		return false
	}
	event := event{file: file, originalURL: originalURL, ip: ip, userAgent: userAgent, timestamp: recorder.now().UTC()}
	recorder.mutex.RLock()
	defer recorder.mutex.RUnlock()
	if recorder.closed {
		recorder.dropped.Add(1)
		return false
	}
	select {
	case recorder.events <- event:
		return true
	default:
		recorder.dropped.Add(1)
		return false
	}
}

func (recorder *Recorder) Dropped() uint64 {
	return recorder.dropped.Load()
}

func (recorder *Recorder) Close(ctx context.Context) error {
	recorder.mutex.Lock()
	if !recorder.closed {
		recorder.closed = true
		close(recorder.events)
	}
	recorder.mutex.Unlock()
	select {
	case <-recorder.done:
		recorder.cancel()
		return nil
	case <-ctx.Done():
		recorder.cancel()
		return ctx.Err()
	}
}

func (recorder *Recorder) run(ctx context.Context) {
	defer close(recorder.done)
	for event := range recorder.events {
		if ctx.Err() != nil {
			return
		}
		if err := recorder.persist(ctx, event); err != nil && ctx.Err() == nil {
			slog.Warn("record HFS download", "file", event.file, "error", err)
		}
	}
}

func (recorder *Recorder) persist(parent context.Context, event event) error {
	id, err := recorder.newID()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(parent, recorder.writeTimeout)
	defer cancel()
	var userAgent *string
	if event.userAgent != "" {
		userAgent = &event.userAgent
	}
	return recorder.repository.Create(ctx, repository.DownloadRecord{
		ID: id, File: event.file, IP: event.ip, OriginalURL: event.originalURL, UserAgent: userAgent,
		CreatedAt: event.timestamp, UpdatedAt: event.timestamp,
	})
}

func (recorder *Recorder) newID() (string, error) {
	value := make([]byte, 16)
	if _, err := io.ReadFull(recorder.random, value); err != nil {
		return "", fmt.Errorf("generate HFS download ID: %w", err)
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return hex.EncodeToString(value), nil
}
