package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"kaven.xyz/kaven/kaven-media-server/internal/buildinfo"
	"kaven.xyz/kaven/kaven-media-server/internal/integrity"
)

// IntegrityHandler runs a read-only integrity check against the active
// database and the managed files in the current data directory.
type IntegrityHandler struct {
	db      *sql.DB
	dataDir string
	active  chan struct{}
}

func NewIntegrityHandler(db *sql.DB, dataDir string) *IntegrityHandler {
	return &IntegrityHandler{db: db, dataDir: dataDir, active: make(chan struct{}, 1)}
}

func (handler *IntegrityHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	if handler.db == nil || handler.dataDir == "" {
		writeJSONResponse(writer, http.StatusServiceUnavailable, map[string]string{"error": "storage integrity check is unavailable"})
		return
	}
	select {
	case handler.active <- struct{}{}:
		defer func() { <-handler.active }()
	default:
		writeJSONResponse(writer, http.StatusConflict, map[string]string{"error": "a storage integrity check is already active"})
		return
	}

	report, err := integrity.Check(request.Context(), handler.db, handler.dataDir)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			status = http.StatusRequestTimeout
		}
		slog.Error("check active storage integrity", "error", err)
		writeJSONResponse(writer, status, map[string]string{"error": "could not check storage integrity"})
		return
	}
	report.Execution = &integrity.ExecutionMetadata{
		GeneratedAt: time.Now().UTC(),
		Producer:    buildinfo.Current(),
	}
	writer.Header().Set("Content-Type", "application/json")
	_ = report.WriteJSON(writer)
}
