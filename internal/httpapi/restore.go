package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"net/http"

	"kaven.xyz/kaven/kaven-media-server/internal/uirestore"
)

const restoreMultipartAllowance int64 = 512 << 20

type RestoreHandler struct {
	dataDir  string
	maxBytes int64
	ready    func()
	active   chan struct{}
}

type RestoreResponse struct {
	Files      int   `json:"files"`
	Bytes      int64 `json:"bytes"`
	Restarting bool  `json:"restarting"`
}

func NewRestoreHandler(dataDir string, maxBytes int64, ready func()) *RestoreHandler {
	return &RestoreHandler{
		dataDir: dataDir, maxBytes: maxBytes, ready: ready, active: make(chan struct{}, 1),
	}
}

func (handler *RestoreHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	if handler.maxBytes <= 0 || handler.ready == nil {
		writeJSONResponse(writer, http.StatusServiceUnavailable, map[string]string{"error": "restore is unavailable"})
		return
	}
	select {
	case handler.active <- struct{}{}:
		defer func() { <-handler.active }()
	default:
		writeJSONResponse(writer, http.StatusConflict, map[string]string{"error": "another restore is in progress"})
		return
	}
	requestLimit := handler.maxBytes
	if requestLimit <= math.MaxInt64-restoreMultipartAllowance {
		requestLimit += restoreMultipartAllowance
	}
	request.Body = http.MaxBytesReader(writer, request.Body, requestLimit)
	reader, err := request.MultipartReader()
	if err != nil {
		writeJSONResponse(writer, http.StatusBadRequest, map[string]string{"error": "select a backup directory"})
		return
	}
	report, err := uirestore.Prepare(request.Context(), handler.dataDir, reader, handler.maxBytes)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			status = http.StatusRequestTimeout
		}
		slog.Warn("reject restore upload", "error", err)
		writeJSONResponse(writer, status, map[string]string{"error": err.Error()})
		return
	}
	writeJSONResponse(writer, http.StatusAccepted, RestoreResponse{
		Files: report.Files, Bytes: report.Bytes, Restarting: true,
	})
	if flusher, ok := writer.(http.Flusher); ok {
		flusher.Flush()
	}
	handler.ready()
}
