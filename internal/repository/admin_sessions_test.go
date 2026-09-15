package repository

import (
	"bytes"
	"context"
	"testing"
	"time"

	"kaven.xyz/kaven/kaven-media-server/internal/database"
)

func TestAdminSessionRepositoryPersistsExpiresAndEvicts(t *testing.T) {
	db, err := database.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	repository := NewAdminSessionRepository(db, 2)
	now := time.Date(2026, time.September, 9, 1, 2, 3, 0, time.UTC)
	credentialKey := bytes.Repeat([]byte{9}, 32)
	tokens := [][]byte{bytes.Repeat([]byte{1}, 32), bytes.Repeat([]byte{2}, 32), bytes.Repeat([]byte{3}, 32)}
	for index, token := range tokens {
		created, err := repository.Create(context.Background(), token, credentialKey, now.Add(time.Duration(index)*time.Second), now.Add(24*time.Hour))
		if err != nil || !created {
			t.Fatalf("create session %d = %v, %v", index, created, err)
		}
	}
	if _, exists, err := repository.Get(context.Background(), tokens[0], credentialKey, now); err != nil || exists {
		t.Fatalf("evicted session exists = %v, error = %v", exists, err)
	}
	expiresAt, exists, err := repository.Get(context.Background(), tokens[2], credentialKey, now)
	if err != nil || !exists || !expiresAt.Equal(now.Add(24*time.Hour)) {
		t.Fatalf("stored session expiry = %s, exists = %v, error = %v", expiresAt, exists, err)
	}
	if _, exists, err := repository.Get(context.Background(), tokens[2], bytes.Repeat([]byte{8}, 32), now); err != nil || exists {
		t.Fatalf("session with different credential key exists = %v, error = %v", exists, err)
	}
	created, err := repository.Create(context.Background(), tokens[2], credentialKey, now, now.Add(24*time.Hour))
	if err != nil || created {
		t.Fatalf("duplicate session = %v, error = %v", created, err)
	}
	if _, exists, err := repository.Get(context.Background(), tokens[2], credentialKey, now.Add(25*time.Hour)); err != nil || exists {
		t.Fatalf("expired session exists = %v, error = %v", exists, err)
	}
}
