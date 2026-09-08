package imageaccess

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"kaven.xyz/kaven/kaven-media-server/internal/repository"
)

func TestRecorderDrainsAcceptedEvents(t *testing.T) {
	repository := &recordingRepository{}
	recorder, err := NewRecorder(repository, 4, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	timestamp := time.Date(2026, time.September, 4, 1, 2, 3, 4_000_000, time.FixedZone("test", 8*60*60))
	recorder.now = func() time.Time { return timestamp }
	recorder.random = bytes.NewReader(bytes.Repeat([]byte{0xff}, 32))

	if !recorder.Record("image-1", "/image/id?width=10", "192.0.2.1") ||
		!recorder.Record("image-1", "/image/id?quality=80", "2001:db8::1") {
		t.Fatal("valid access event was rejected")
	}
	if err := recorder.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	records := repository.snapshot()
	if len(records) != 2 {
		t.Fatalf("records = %#v, want 2", records)
	}
	for _, record := range records {
		if len(record.ID) != 32 || record.ID[12] != '4' || record.ID[16] != 'b' {
			t.Errorf("ID = %q, want UUIDv4 hex", record.ID)
		}
		if !record.CreatedAt.Equal(timestamp) || record.CreatedAt.Location() != time.UTC || !record.UpdatedAt.Equal(record.CreatedAt) {
			t.Errorf("timestamps = %v, %v", record.CreatedAt, record.UpdatedAt)
		}
	}
}

func TestRecorderNeverBlocksWhenQueueIsFull(t *testing.T) {
	repository := &blockingRepository{started: make(chan struct{}), release: make(chan struct{})}
	recorder, err := NewRecorder(repository, 1, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !recorder.Record("image", "/image/first", "192.0.2.1") {
		t.Fatal("first event rejected")
	}
	<-repository.started
	if !recorder.Record("image", "/image/second", "192.0.2.1") {
		t.Fatal("buffered event rejected")
	}
	if recorder.Record("image", "/image/dropped", "192.0.2.1") {
		t.Fatal("event was accepted into a full queue")
	}
	if recorder.Dropped() != 1 {
		t.Fatalf("dropped = %d, want 1", recorder.Dropped())
	}
	close(repository.release)
	if err := recorder.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if repository.callCount() != 2 {
		t.Fatalf("repository calls = %d, want 2", repository.callCount())
	}
}

func TestRecorderRejectsUnboundedAndClosedEvents(t *testing.T) {
	repository := &recordingRepository{}
	recorder, err := NewRecorder(repository, 1, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if recorder.Record("image", strings.Repeat("x", MaxOriginalURLLength+1), "192.0.2.1") {
		t.Fatal("oversized URL accepted")
	}
	if recorder.Record("image", "/image/id", strings.Repeat("x", MaxIPAddressLength+1)) {
		t.Fatal("oversized IP accepted")
	}
	if err := recorder.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if recorder.Record("image", "/image/id", "192.0.2.1") {
		t.Fatal("event accepted after close")
	}
	if recorder.Dropped() != 3 || len(repository.snapshot()) != 0 {
		t.Fatalf("dropped = %d, records = %#v", recorder.Dropped(), repository.snapshot())
	}
}

func TestRecorderContinuesAfterRepositoryFailure(t *testing.T) {
	repository := &recordingRepository{failures: 1}
	recorder, err := NewRecorder(repository, 2, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	recorder.Record("image", "/image/first", "192.0.2.1")
	recorder.Record("image", "/image/second", "192.0.2.1")
	if err := recorder.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if repository.calls != 2 || len(repository.snapshot()) != 1 || repository.snapshot()[0].OriginalURL != "/image/second" {
		t.Fatalf("calls = %d, records = %#v", repository.calls, repository.snapshot())
	}
}

type recordingRepository struct {
	mutex    sync.Mutex
	records  []repository.AccessRecord
	calls    int
	failures int
}

func (fake *recordingRepository) Create(_ context.Context, record repository.AccessRecord) error {
	fake.mutex.Lock()
	defer fake.mutex.Unlock()
	fake.calls++
	if fake.failures > 0 {
		fake.failures--
		return errors.New("database unavailable")
	}
	fake.records = append(fake.records, record)
	return nil
}

func (fake *recordingRepository) snapshot() []repository.AccessRecord {
	fake.mutex.Lock()
	defer fake.mutex.Unlock()
	return append([]repository.AccessRecord(nil), fake.records...)
}

type blockingRepository struct {
	mutex   sync.Mutex
	calls   int
	started chan struct{}
	release chan struct{}
}

func (fake *blockingRepository) Create(ctx context.Context, _ repository.AccessRecord) error {
	fake.mutex.Lock()
	fake.calls++
	call := fake.calls
	fake.mutex.Unlock()
	if call == 1 {
		close(fake.started)
		select {
		case <-fake.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (fake *blockingRepository) callCount() int {
	fake.mutex.Lock()
	defer fake.mutex.Unlock()
	return fake.calls
}
