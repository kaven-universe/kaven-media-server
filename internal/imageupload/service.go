package imageupload

import (
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"
	"unicode"

	"kaven.xyz/kaven/kaven-media-server/internal/imageproc"
	"kaven.xyz/kaven/kaven-media-server/internal/repository"
	"kaven.xyz/kaven/kaven-media-server/internal/storage"
)

var ErrInvalidUUID = errors.New("invalid image UUID")

type ImageRepository interface {
	Create(context.Context, repository.Image) error
	GetBySHA1(context.Context, string) (repository.Image, error)
}

type Service struct {
	store  *storage.Store
	images ImageRepository
	now    func() time.Time
	random io.Reader
}

type Input struct {
	File          *storage.StagedFile
	OriginalName  string
	RequestedUUID string
	UploadIP      string
}

type Result struct {
	ID        string
	UUID      string
	Name      string
	SHA1      string
	Duplicate bool
}

func NewService(store *storage.Store, images ImageRepository) *Service {
	return &Service{store: store, images: images, now: time.Now, random: rand.Reader}
}

func (service *Service) Save(ctx context.Context, input Input) (Result, error) {
	if input.File == nil {
		return Result{}, errors.New("save image: staged file is required")
	}
	defer input.File.Abort()

	uuid, err := service.imageUUID(input.RequestedUUID)
	if err != nil {
		return Result{}, err
	}
	originalName := safeDisplayName(input.OriginalName, "upload")
	result := Result{UUID: uuid, Name: originalName}

	format, err := imageproc.DetectFile(input.File.Path())
	if err != nil {
		return result, err
	}
	digest, err := checksum(ctx, input.File.Path())
	if err != nil {
		return result, err
	}
	result.SHA1 = digest

	existing, err := service.images.GetBySHA1(ctx, digest)
	if err == nil {
		result.ID = existing.ID
		result.Duplicate = true
		return result, nil
	}
	if !errors.Is(err, repository.ErrNotFound) {
		return Result{}, fmt.Errorf("check image duplicate: %w", err)
	}

	now := service.now().UTC()
	folder := path.Join("images", now.Format("2006"), now.Format("01"))
	if err := service.store.MkdirAll(folder, 0o755); err != nil {
		return Result{}, fmt.Errorf("create image folder: %w", err)
	}
	storedName := uuid + format.Extension
	relative := path.Join(folder, storedName)
	if _, err := input.File.Commit(relative, 0o640); err != nil {
		return Result{}, fmt.Errorf("publish image: %w", err)
	}

	image := repository.Image{
		ID: uuid, UUID: uuid, SHA1: digest, Folder: folder, Name: storedName,
		OriginalName: originalName, MIMEType: format.MIMEType, Size: input.File.Size(),
		UploadDate: now, UploadIP: input.UploadIP, CreatedAt: now, UpdatedAt: now,
	}
	if err := service.images.Create(ctx, image); err != nil {
		if removeErr := service.store.RemoveFile(relative); removeErr != nil {
			return Result{}, errors.Join(fmt.Errorf("create image record: %w", err), fmt.Errorf("roll back image file: %w", removeErr))
		}
		if errors.Is(err, repository.ErrConflict) {
			existing, lookupErr := service.images.GetBySHA1(ctx, digest)
			if lookupErr == nil {
				result.ID = existing.ID
				result.Duplicate = true
				return result, nil
			}
		}
		return Result{}, fmt.Errorf("create image record: %w", err)
	}

	result.ID = image.ID
	return result, nil
}

func (service *Service) imageUUID(requested string) (string, error) {
	if requested != "" {
		normalized := strings.ToLower(strings.ReplaceAll(requested, "-", ""))
		if len(normalized) != 32 {
			return "", ErrInvalidUUID
		}
		if _, err := hex.DecodeString(normalized); err != nil {
			return "", ErrInvalidUUID
		}
		return normalized, nil
	}

	value := make([]byte, 16)
	if _, err := io.ReadFull(service.random, value); err != nil {
		return "", fmt.Errorf("generate image UUID: %w", err)
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return hex.EncodeToString(value), nil
}

func checksum(ctx context.Context, filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("open staged image for checksum: %w", err)
	}
	defer file.Close()

	hash := sha1.New()
	buffer := make([]byte, 64*1024)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		count, readErr := file.Read(buffer)
		if count > 0 {
			if _, err := hash.Write(buffer[:count]); err != nil {
				return "", fmt.Errorf("hash staged image: %w", err)
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return "", fmt.Errorf("read staged image for checksum: %w", readErr)
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func safeDisplayName(value, fallback string) string {
	value = strings.ReplaceAll(value, `\`, "/")
	value = path.Base(value)
	value = strings.TrimSpace(strings.Map(func(character rune) rune {
		if unicode.IsControl(character) {
			return -1
		}
		return character
	}, value))
	if value == "" || value == "." || value == ".." {
		return fallback
	}
	return value
}
