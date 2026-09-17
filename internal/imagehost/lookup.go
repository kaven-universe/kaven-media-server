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
	IdentifierID IdentifierKind = iota + 1
	IdentifierUUID
	IdentifierSHA1
)

type Identifier struct {
	Kind  IdentifierKind
	Value string
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
		kind = IdentifierID
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
	return Identifier{Kind: kind, Value: normalized}, nil
}

func (service *LookupService) Find(ctx context.Context, value string) (repository.Image, error) {
	identifier, err := ParseIdentifier(value)
	if err != nil {
		return repository.Image{}, err
	}

	var image repository.Image
	switch identifier.Kind {
	case IdentifierID:
		image, err = service.images.GetByID(ctx, identifier.Value)
	case IdentifierUUID:
		image, err = service.images.GetByUUID(ctx, identifier.Value)
	case IdentifierSHA1:
		image, err = service.images.GetBySHA1(ctx, identifier.Value)
	default:
		return repository.Image{}, ErrInvalidIdentifier
	}
	if err != nil {
		return repository.Image{}, fmt.Errorf("look up image: %w", err)
	}
	return image, nil
}
