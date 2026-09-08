package imagehost

import (
	"context"
	"errors"
	"strings"
	"testing"

	"kaven.xyz/kaven/kaven-media-server/internal/repository"
)

func TestParseIdentifier(t *testing.T) {
	tests := []struct {
		name  string
		value string
		kind  IdentifierKind
		want  string
	}{
		{name: "ObjectId", value: "ABCDEF0123456789ABCDEF01", kind: IdentifierObjectID, want: "abcdef0123456789abcdef01"},
		{name: "UUID", value: "ABCDEF0123456789ABCDEF0123456789", kind: IdentifierUUID, want: "abcdef0123456789abcdef0123456789"},
		{name: "hyphenated UUID", value: "ABCDEF01-2345-6789-ABCD-EF0123456789", kind: IdentifierUUID, want: "abcdef01-2345-6789-abcd-ef0123456789"},
		{name: "SHA-1", value: "ABCDEF0123456789ABCDEF0123456789ABCDEF01", kind: IdentifierSHA1, want: "abcdef0123456789abcdef0123456789abcdef01"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			identifier, err := ParseIdentifier(test.value)
			if err != nil {
				t.Fatalf("ParseIdentifier: %v", err)
			}
			if identifier.Kind != test.kind || identifier.Value != test.want || identifier.Original != test.value {
				t.Fatalf("identifier = %#v, want kind %d value %q", identifier, test.kind, test.want)
			}
		})
	}
}

func TestParseIdentifierRejectsMalformedValues(t *testing.T) {
	for _, value := range []string{
		"", "short", " abcdef0123456789abcdef01", "abcdef0123456789abcdef01 ",
		"ghijkl0123456789abcdef01", "abcdef0123456789abcdef0-",
		"abcdef0123456789abcdef0123456789abcd", "abcdef012345-6789-abcdef0123456789",
		"abcdef0123456789abcdef0123456789abcdef0123",
	} {
		t.Run(value, func(t *testing.T) {
			if _, err := ParseIdentifier(value); !errors.Is(err, ErrInvalidIdentifier) {
				t.Fatalf("ParseIdentifier(%q) error = %v, want ErrInvalidIdentifier", value, err)
			}
		})
	}
}

func TestLookupPreservesExactCaseAndFallsBackToLowercase(t *testing.T) {
	want := repository.Image{ID: "found"}
	fake := &lookupRepository{image: want, missing: map[string]bool{}}
	service := NewLookupService(fake)

	uppercase := "ABCDEF01-2345-6789-ABCD-EF0123456789"
	if image, err := service.Find(context.Background(), uppercase); err != nil || image.ID != want.ID {
		t.Fatalf("exact uppercase lookup = %#v, %v", image, err)
	}
	if got := fake.values; len(got) != 1 || got[0] != uppercase {
		t.Fatalf("exact lookup values = %v", got)
	}

	fake.values = nil
	fake.missing[uppercase] = true
	if image, err := service.Find(context.Background(), uppercase); err != nil || image.ID != want.ID {
		t.Fatalf("lowercase fallback lookup = %#v, %v", image, err)
	}
	if got := fake.values; len(got) != 2 || got[0] != uppercase || got[1] != strings.ToLower(uppercase) {
		t.Fatalf("fallback lookup values = %v", got)
	}
}

func TestLookupDispatchesByIdentifierKind(t *testing.T) {
	want := repository.Image{ID: "found"}
	repository := &lookupRepository{image: want}
	service := NewLookupService(repository)
	tests := []struct {
		value string
		call  string
	}{
		{value: "abcdef0123456789abcdef01", call: "id"},
		{value: "abcdef0123456789abcdef0123456789", call: "uuid"},
		{value: "abcdef0123456789abcdef0123456789abcdef01", call: "sha1"},
	}
	for _, test := range tests {
		repository.call = ""
		got, err := service.Find(context.Background(), test.value)
		if err != nil {
			t.Fatalf("Find(%q): %v", test.value, err)
		}
		if got.ID != want.ID || repository.call != test.call {
			t.Fatalf("Find(%q) = %#v via %q", test.value, got, repository.call)
		}
	}
}

type lookupRepository struct {
	image   repository.Image
	call    string
	err     error
	values  []string
	missing map[string]bool
}

func (fake *lookupRepository) result(value string) (repository.Image, error) {
	fake.values = append(fake.values, value)
	if fake.missing[value] {
		return repository.Image{}, repository.ErrNotFound
	}
	return fake.image, fake.err
}

func (fake *lookupRepository) GetByID(_ context.Context, value string) (repository.Image, error) {
	fake.call = "id"
	return fake.result(value)
}

func (fake *lookupRepository) GetByUUID(_ context.Context, value string) (repository.Image, error) {
	fake.call = "uuid"
	return fake.result(value)
}

func (fake *lookupRepository) GetBySHA1(_ context.Context, value string) (repository.Image, error) {
	fake.call = "sha1"
	return fake.result(value)
}
