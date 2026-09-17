package imageaccess

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
	MaxOriginalURLLength = 8 * 1024
	MaxIPAddressLength   = 128
)

type Repository interface {
	Create(context.Context, repository.AccessRecord) error
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
	imageID     string
	ip          string
	originalURL string
	timestamp   time.Time
}

func NewRecorder(repository Repository, capacity int, writeTimeout time.Duration) (*Recorder, error) {
	if repository == nil {
		return nil, errors.New("create image access recorder: repository is required")
	}
	if capacity < 1 {
		return nil, errors.New("create image access recorder: capacity must be positive")
	}
	if writeTimeout <= 0 {
		return nil, errors.New("create image access recorder: write timeout must be positive")
	}
	ctx, cancel := context.WithCancel(context.Background())
	recorder := &Recorder{
		repository: repository, writeTimeout: writeTimeout, now: time.Now, random: rand.Reader,
		events: make(chan event, capacity), done: make(chan struct{}), cancel: cancel,
	}
	go recorder.run(ctx)
	return recorder, nil
}

func (recorder *Recorder) Record(imageID, originalURL, ip string) bool {
	if imageID == "" || originalURL == "" || len(originalURL) > MaxOriginalURLLength || ip == "" || len(ip) > MaxIPAddressLength {
		recorder.dropped.Add(1)
		return false
	}
	event := event{imageID: imageID, originalURL: originalURL, ip: ip, timestamp: recorder.now().UTC()}
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
			slog.Warn("record image access", "image", event.imageID, "error", err)
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
	return recorder.repository.Create(ctx, repository.AccessRecord{
		ID: id, ImageID: event.imageID, IP: event.ip, OriginalURL: event.originalURL,
		CreatedAt: event.timestamp, UpdatedAt: event.timestamp,
	})
}

func (recorder *Recorder) newID() (string, error) {
	value := make([]byte, 16)
	if _, err := io.ReadFull(recorder.random, value); err != nil {
		return "", fmt.Errorf("generate image access ID: %w", err)
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return hex.EncodeToString(value), nil
}
