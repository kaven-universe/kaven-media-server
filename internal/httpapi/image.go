package httpapi

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path"
	"strconv"

	"kaven.xyz/kaven/kaven-media-server/internal/imagehost"
	"kaven.xyz/kaven/kaven-media-server/internal/imageproc"
	"kaven.xyz/kaven/kaven-media-server/internal/repository"
	"kaven.xyz/kaven/kaven-media-server/internal/storage"
)

type ImageHandler struct {
	lookup      *imagehost.LookupService
	store       *storage.Store
	transformer imageTransformer
	accesses    imageAccessRecorder
}

type imageTransformer interface {
	Transform(context.Context, string, repository.Image, imageproc.Options) (imageproc.Result, error)
}

type imageAccessRecorder interface {
	Record(imageID, originalURL, ip string) bool
}

func NewImageHandler(lookup *imagehost.LookupService, store *storage.Store, transformer imageTransformer, accesses imageAccessRecorder) *ImageHandler {
	return &ImageHandler{lookup: lookup, store: store, transformer: transformer, accesses: accesses}
}

func (handler *ImageHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	image, err := handler.lookup.Find(request.Context(), request.PathValue("id"))
	if err != nil {
		switch {
		case errors.Is(err, imagehost.ErrInvalidIdentifier):
			writer.WriteHeader(http.StatusBadRequest)
		case errors.Is(err, repository.ErrNotFound):
			writer.WriteHeader(http.StatusNotFound)
		default:
			slog.Error("look up image", "error", err)
			writer.WriteHeader(http.StatusInternalServerError)
		}
		return
	}

	if request.URL.Query().Has("json") {
		writeJSONResponse(writer, http.StatusOK, imageMetadata(image))
		return
	}
	requestTarget := request.RequestURI
	if requestTarget == "" {
		requestTarget = request.URL.RequestURI()
	}
	handler.accesses.Record(image.ID, requestTarget, requestIP(request))

	options, transformed, err := transformationOptions(request)
	if err != nil {
		writer.WriteHeader(http.StatusBadRequest)
		return
	}
	if transformed {
		handler.serveTransformed(writer, request, image, options)
		return
	}
	handler.serveOriginal(writer, request, image)
}

func (handler *ImageHandler) serveOriginal(writer http.ResponseWriter, request *http.Request, image repository.Image) {
	filePath, err := handler.resolveImagePath(image)
	if err != nil {
		handler.writeResolveError(writer, image.ID, err)
		return
	}
	file, err := os.Open(filePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		slog.Error("open image file", "image", image.ID, "error", err)
		writer.WriteHeader(http.StatusInternalServerError)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		slog.Error("inspect image file", "image", image.ID, "error", err)
		writer.WriteHeader(http.StatusInternalServerError)
		return
	}

	header := make([]byte, 4096)
	read, err := file.ReadAt(header, 0)
	if err != nil && !errors.Is(err, io.EOF) {
		slog.Error("read image header", "image", image.ID, "error", err)
		writer.WriteHeader(http.StatusInternalServerError)
		return
	}
	format, err := imageproc.Detect(header[:read])
	if err != nil {
		slog.Warn("reject invalid stored image", "image", image.ID, "error", err)
		writer.WriteHeader(http.StatusUnsupportedMediaType)
		return
	}

	writer.Header().Set("Content-Type", format.MIMEType)
	writer.Header().Set("Cache-Control", "public, max-age=0")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	if identifier, err := imagehost.ParseIdentifier(image.SHA1); err == nil && identifier.Kind == imagehost.IdentifierSHA1 {
		writer.Header().Set("ETag", `"`+identifier.Value+`"`)
	}
	http.ServeContent(writer, request, image.OriginalName, info.ModTime(), file)
}

func (handler *ImageHandler) serveTransformed(writer http.ResponseWriter, request *http.Request, image repository.Image, options imageproc.Options) {
	filePath, err := handler.resolveImagePath(image)
	if err != nil {
		handler.writeResolveError(writer, image.ID, err)
		return
	}
	result, err := handler.transformer.Transform(request.Context(), filePath, image, options)
	if err != nil {
		switch {
		case errors.Is(err, imageproc.ErrInvalid):
			writer.WriteHeader(http.StatusBadRequest)
		case errors.Is(err, imageproc.ErrUnsupported):
			writer.WriteHeader(http.StatusUnsupportedMediaType)
		case errors.Is(err, imageproc.ErrUnavailable):
			writer.WriteHeader(http.StatusNotImplemented)
		case request.Context().Err() != nil:
			return
		default:
			slog.Error("transform image", "image", image.ID, "error", err)
			writer.WriteHeader(http.StatusInternalServerError)
		}
		return
	}
	writer.Header().Set("Content-Type", result.MIMEType)
	writer.Header().Set("Content-Length", strconv.Itoa(len(result.Bytes)))
	writer.Header().Set("Cache-Control", "public, max-age=0")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	if request.Method != http.MethodHead {
		_, _ = writer.Write(result.Bytes)
	}
}

func (handler *ImageHandler) resolveImagePath(image repository.Image) (string, error) {
	reference := path.Join(image.Folder, image.Name)
	return handler.store.ResolveManagedFileReference(reference, "images")
}

func (handler *ImageHandler) writeResolveError(writer http.ResponseWriter, imageID string, err error) {
	if errors.Is(err, storage.ErrNotFound) {
		writer.WriteHeader(http.StatusNotFound)
		return
	}
	slog.Error("resolve image file", "image", imageID, "error", err)
	writer.WriteHeader(http.StatusInternalServerError)
}

func transformationOptions(request *http.Request) (imageproc.Options, bool, error) {
	query := request.URL.Query()
	var options imageproc.Options
	transformed := false
	for name, target := range map[string]*int{
		"width": &options.Width, "height": &options.Height, "quality": &options.Quality,
	} {
		values, present := query[name]
		if !present {
			continue
		}
		transformed = true
		if len(values) != 1 || values[0] == "" {
			return imageproc.Options{}, true, imageproc.ErrInvalid
		}
		value, err := strconv.Atoi(values[0])
		if err != nil || value < 1 {
			return imageproc.Options{}, true, imageproc.ErrInvalid
		}
		if (name == "quality" && value > 100) || (name != "quality" && value > imageproc.MaxDimension) {
			return imageproc.Options{}, true, imageproc.ErrInvalid
		}
		*target = value
	}
	return options, transformed, nil
}

func imageMetadata(image repository.Image) ImageMetadata {
	return ImageMetadata{
		ID: image.ID, Folder: image.Folder, Name: image.Name,
		OriginalName: image.OriginalName, Path: path.Join(image.Folder, image.Name),
		UploadDate: NewTimestamp(image.UploadDate), UUID: image.UUID,
		MIMEType: image.MIMEType, Size: image.Size, UploadIP: image.UploadIP,
		SHA1: image.SHA1, CreatedAt: NewTimestamp(image.CreatedAt),
		UpdatedAt: NewTimestamp(image.UpdatedAt), Version: 0,
	}
}
