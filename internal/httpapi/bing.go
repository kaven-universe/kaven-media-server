package httpapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path"
	"strings"

	"kaven.xyz/kaven/kaven-media-server/internal/imageproc"
	"kaven.xyz/kaven/kaven-media-server/internal/repository"
	"kaven.xyz/kaven/kaven-media-server/internal/storage"
)

const maxRandomBingAttempts = 20

type BingImageRepository interface {
	RandomWithFile(context.Context) (repository.BingImage, error)
}

type BingImageHandler struct {
	images BingImageRepository
	store  *storage.Store
}

func NewBingImageHandler(images BingImageRepository, store *storage.Store) *BingImageHandler {
	return &BingImageHandler{images: images, store: store}
}

func (handler *BingImageHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	for range maxRandomBingAttempts {
		image, err := handler.images.RandomWithFile(request.Context())
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				writer.WriteHeader(http.StatusNotFound)
				return
			}
			if request.Context().Err() != nil {
				return
			}
			slog.Error("select random Bing image", "error", err)
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
		file, info, format, err := handler.open(image)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
				continue
			}
			if errors.Is(err, imageproc.ErrUnsupportedFormat) {
				slog.Warn("reject invalid archived Bing image", "image", image.ID, "error", err)
				writer.WriteHeader(http.StatusUnsupportedMediaType)
				return
			}
			slog.Error("open archived Bing image", "image", image.ID, "error", err)
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
		defer file.Close()
		writer.Header().Set("Content-Type", format.MIMEType)
		writer.Header().Set("Cache-Control", "public, max-age=0")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
		http.ServeContent(writer, request, path.Base(strings.ReplaceAll(*image.File, `\`, "/")), info.ModTime(), file)
		return
	}
	writer.WriteHeader(http.StatusNotFound)
}

func (handler *BingImageHandler) open(image repository.BingImage) (*os.File, os.FileInfo, imageproc.Format, error) {
	if image.File == nil {
		return nil, nil, imageproc.Format{}, storage.ErrNotFound
	}
	reference := *image.File
	resolved, err := handler.store.ResolveManagedFileReference(reference, "bing")
	if err != nil {
		return nil, nil, imageproc.Format{}, err
	}
	file, err := os.Open(resolved)
	if err != nil {
		return nil, nil, imageproc.Format{}, err
	}
	closeOnError := func(resultErr error) (*os.File, os.FileInfo, imageproc.Format, error) {
		_ = file.Close()
		return nil, nil, imageproc.Format{}, resultErr
	}
	info, err := file.Stat()
	if err != nil {
		return closeOnError(fmt.Errorf("inspect Bing image: %w", err))
	}
	if !info.Mode().IsRegular() {
		return closeOnError(errors.New("inspect Bing image: not a regular file"))
	}
	verifiedPath, err := handler.store.ResolveManagedFileReference(reference, "bing")
	if err != nil {
		return closeOnError(fmt.Errorf("verify Bing image path: %w", err))
	}
	verifiedInfo, err := os.Lstat(verifiedPath)
	if err != nil {
		return closeOnError(fmt.Errorf("verify Bing image path: %w", err))
	}
	if !os.SameFile(info, verifiedInfo) {
		return closeOnError(errors.New("verify Bing image path: file changed while opening"))
	}
	header := make([]byte, 4096)
	read, err := file.ReadAt(header, 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return closeOnError(fmt.Errorf("read Bing image header: %w", err))
	}
	format, err := imageproc.Detect(header[:read])
	if err != nil {
		return closeOnError(err)
	}
	return file, info, format, nil
}
