package imagehost

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"kaven.xyz/kaven/kaven-media-server/internal/repository"
)

var ErrInvalidIdentifier = errors.New("invalid image identifier")

type IdentifierKind int

const (
	IdentifierObjectID IdentifierKind = iota + 1
	IdentifierUUID
	IdentifierSHA1
)

type Identifier struct {
	Kind     IdentifierKind
	Value    string
	Original string
}

type ImageRepository interface {
	GetByID(context.Context, string) (repository.Image, error)
	GetByUUID(context.Context, string) (repository.Image, error)
	GetBySHA1(context.Context, string) (repository.Image, error)
}

type LookupService struct {
	images ImageRepository
}

func NewLookupService(images ImageRepository) *LookupService {
	return &LookupService{images: images}
}

func ParseIdentifier(value string) (Identifier, error) {
	if value != strings.TrimSpace(value) {
		return Identifier{}, ErrInvalidIdentifier
	}
	normalized := strings.ToLower(value)
	var kind IdentifierKind
	switch len(normalized) {
	case 24:
		kind = IdentifierObjectID
	case 32:
		kind = IdentifierUUID
	case 36:
		if normalized[8] != '-' || normalized[13] != '-' || normalized[18] != '-' || normalized[23] != '-' {
			return Identifier{}, ErrInvalidIdentifier
		}
		kind = IdentifierUUID
	case 40:
		kind = IdentifierSHA1
	default:
		return Identifier{}, ErrInvalidIdentifier
	}
	hexValue := strings.ReplaceAll(normalized, "-", "")
	if _, err := hex.DecodeString(hexValue); err != nil {
		return Identifier{}, ErrInvalidIdentifier
	}
	return Identifier{Kind: kind, Value: normalized, Original: value}, nil
}

func (service *LookupService) Find(ctx context.Context, value string) (repository.Image, error) {
	identifier, err := ParseIdentifier(value)
	if err != nil {
		return repository.Image{}, err
	}

	var image repository.Image
	switch identifier.Kind {
	case IdentifierObjectID:
		image, err = lookupWithCaseFallback(ctx, identifier, service.images.GetByID)
	case IdentifierUUID:
		image, err = lookupWithCaseFallback(ctx, identifier, service.images.GetByUUID)
	case IdentifierSHA1:
		image, err = lookupWithCaseFallback(ctx, identifier, service.images.GetBySHA1)
	default:
		return repository.Image{}, ErrInvalidIdentifier
	}
	if err != nil {
		return repository.Image{}, fmt.Errorf("look up image: %w", err)
	}
	return image, nil
}

func lookupWithCaseFallback(ctx context.Context, identifier Identifier, lookup func(context.Context, string) (repository.Image, error)) (repository.Image, error) {
	image, err := lookup(ctx, identifier.Original)
	if errors.Is(err, repository.ErrNotFound) && identifier.Value != identifier.Original {
		return lookup(ctx, identifier.Value)
	}
	return image, err
}
