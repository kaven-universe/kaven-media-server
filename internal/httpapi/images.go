package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"kaven.xyz/kaven/kaven-media-server/internal/repository"
)

type imageLister interface {
	List(context.Context, int) ([]repository.Image, error)
}

type ImagesHandler struct {
	images imageLister
}

func NewImagesHandler(images imageLister) *ImagesHandler {
	return &ImagesHandler{images: images}
}

func (handler *ImagesHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	images, err := handler.images.List(request.Context(), 100)
	if err != nil {
		slog.Error("list images", "error", err)
		writer.WriteHeader(http.StatusInternalServerError)
		return
	}
	metadata := make([]ImageMetadata, 0, len(images))
	for _, image := range images {
		metadata = append(metadata, imageMetadata(image))
	}
	// Encode before committing headers so corrupt metadata cannot produce a
	// successful, partially written JSON response.
	body, err := json.Marshal(metadata)
	if err != nil {
		slog.Error("encode image list", "error", err)
		writer.WriteHeader(http.StatusInternalServerError)
		return
	}
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(http.StatusOK)
	if request.Method != http.MethodHead {
		if _, err := writer.Write(body); err != nil {
			slog.Error("write image list", "error", err)
		}
	}
}
