package repository

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"kaven.xyz/kaven/kaven-media-server/internal/database"
)

func TestAdminCredentialRepositoryCreatesOnlyOnce(t *testing.T) {
	db, err := database.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	repository := NewAdminCredentialRepository(db)
	if _, exists, err := repository.Get(context.Background()); err != nil || exists {
		t.Fatalf("initial credential exists = %v, error = %v", exists, err)
	}
	hash := []byte("stored-password-hash")
	if err := repository.Create(context.Background(), "owner", hash); err != nil {
		t.Fatal(err)
	}
	credential, exists, err := repository.Get(context.Background())
	if err != nil || !exists || credential.Username != "owner" || !bytes.Equal(credential.PasswordHash, hash) {
		t.Fatalf("credential = %+v, exists = %v, error = %v", credential, exists, err)
	}
	if err := repository.Create(context.Background(), "other", []byte("replacement")); !errors.Is(err, ErrAdminAlreadyInitialized) {
		t.Fatalf("second create error = %v", err)
	}
	sessionToken := bytes.Repeat([]byte{7}, 32)
	credentialKey := bytes.Repeat([]byte{8}, 32)
	if created, err := NewAdminSessionRepository(db, 128).Create(
		context.Background(), sessionToken, credentialKey, time.Now().UTC(), time.Now().UTC().Add(24*time.Hour),
	); err != nil || !created {
		t.Fatalf("create session before password update = %v, %v", created, err)
	}
	newHash := []byte("updated-password-hash")
	if err := repository.UpdatePassword(context.Background(), newHash); err != nil {
		t.Fatal(err)
	}
	credential, _, err = repository.Get(context.Background())
	if err != nil || !bytes.Equal(credential.PasswordHash, newHash) {
		t.Fatalf("updated credential = %+v, error = %v", credential, err)
	}
	if _, exists, err := NewAdminSessionRepository(db, 128).Get(context.Background(), sessionToken, credentialKey, time.Now().UTC()); err != nil || exists {
		t.Fatalf("session after password update exists = %v, error = %v", exists, err)
	}
}

func TestAdminCredentialRepositoryCannotUpdateMissingCredential(t *testing.T) {
	db, err := database.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	err = NewAdminCredentialRepository(db).UpdatePassword(context.Background(), []byte("hash"))
	if !errors.Is(err, ErrAdminNotInitialized) {
		t.Fatalf("update missing credential error = %v", err)
	}
}
