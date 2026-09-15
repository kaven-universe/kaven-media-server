package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"kaven.xyz/kaven/kaven-media-server/internal/backupstore"
	"kaven.xyz/kaven/kaven-media-server/internal/config"
	"kaven.xyz/kaven/kaven-media-server/internal/uirestore"
)

const backupStoreRequestLimit int64 = 16 << 10

type BackupStoreHandler struct {
	store    *backupstore.Store
	db       *sql.DB
	dataDir  string
	maxBytes int64
	ready    func()
	validate func([]config.HFSRoot) error
	active   chan struct{}
}

type backupCreateRequest struct {
	Name string `json:"name"`
}

type backupRestoreRequest struct {
	Name     string            `json:"name"`
	HFSRoots *[]config.HFSRoot `json:"hfsRoots"`
}

type RestoreResponse struct {
	Files      int   `json:"files"`
	Bytes      int64 `json:"bytes"`
	Restarting bool  `json:"restarting"`
}

type BackupMappingsResponse struct {
	UploadDirectory   string           `json:"uploadDirectory"`
	DownloadDirectory string           `json:"downloadDirectory"`
	HFSRoots          []config.HFSRoot `json:"hfsRoots"`
}

func NewBackupStoreHandler(store *backupstore.Store, db *sql.DB, dataDir string, maxBytes int64, ready func(), validate func([]config.HFSRoot) error) *BackupStoreHandler {
	return &BackupStoreHandler{store: store, db: db, dataDir: dataDir, maxBytes: maxBytes, ready: ready, validate: validate, active: make(chan struct{}, 1)}
}

func (handler *BackupStoreHandler) List(writer http.ResponseWriter, _ *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	if handler.store == nil {
		writeJSONResponse(writer, http.StatusServiceUnavailable, map[string]string{"error": "backup repository is unavailable"})
		return
	}
	status, err := handler.store.List()
	if err != nil {
		slog.Error("list private backups", "error", err)
		writeJSONResponse(writer, http.StatusInternalServerError, map[string]string{"error": "could not list backups"})
		return
	}
	writeJSONResponse(writer, http.StatusOK, status)
}

func (handler *BackupStoreHandler) Mappings(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	if handler.store == nil || handler.maxBytes <= 0 {
		writeJSONResponse(writer, http.StatusServiceUnavailable, map[string]string{"error": "backup mappings are unavailable"})
		return
	}
	name := strings.TrimSpace(request.PathValue("name"))
	if err := backupstore.ValidateName(name); err != nil {
		writeJSONResponse(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	snapshot, err := handler.store.Resolve(name)
	if err != nil {
		writeJSONResponse(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	settings, err := uirestore.ReadSnapshotSettings(request.Context(), handler.dataDir, snapshot, handler.maxBytes)
	if err != nil {
		writeJSONResponse(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSONResponse(writer, http.StatusOK, BackupMappingsResponse{
		UploadDirectory: settings.UploadDirectory, DownloadDirectory: settings.DownloadDirectory, HFSRoots: settings.HFSRoots,
	})
}

func (handler *BackupStoreHandler) Create(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	if handler.store == nil || handler.ready == nil {
		writeJSONResponse(writer, http.StatusServiceUnavailable, map[string]string{"error": "backup creation is unavailable"})
		return
	}
	if !handler.begin() {
		writeJSONResponse(writer, http.StatusConflict, map[string]string{"error": "another backup or restore is in progress"})
		return
	}
	release := true
	defer func() {
		if release {
			handler.end()
		}
	}()
	var body backupCreateRequest
	if err := decodeBackupStoreRequest(writer, request, &body, &body.Name); err != nil {
		writeJSONResponse(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := handler.store.Queue(body.Name); err != nil {
		writeJSONResponse(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSONResponse(writer, http.StatusAccepted, map[string]any{"name": body.Name, "restarting": true})
	flushResponse(writer)
	release = false
	handler.ready()
}

func (handler *BackupStoreHandler) Restore(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	if handler.store == nil || handler.db == nil || handler.ready == nil || handler.validate == nil || handler.maxBytes <= 0 {
		writeJSONResponse(writer, http.StatusServiceUnavailable, map[string]string{"error": "restore is unavailable"})
		return
	}
	if !handler.begin() {
		writeJSONResponse(writer, http.StatusConflict, map[string]string{"error": "another restore is in progress"})
		return
	}
	release := true
	defer func() {
		if release {
			handler.end()
		}
	}()
	var body backupRestoreRequest
	if err := decodeBackupStoreRequest(writer, request, &body, &body.Name); err != nil {
		writeJSONResponse(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if body.HFSRoots == nil {
		writeJSONResponse(writer, http.StatusBadRequest, map[string]string{"error": "restore directory mappings are required"})
		return
	}
	if len(*body.HFSRoots) > 64 {
		writeJSONResponse(writer, http.StatusBadRequest, map[string]string{"error": "hfsRoots supports at most 64 roots"})
		return
	}
	if err := handler.validate(*body.HFSRoots); err != nil {
		writeJSONResponse(writer, http.StatusBadRequest, map[string]string{"error": "invalid restore directory mappings: " + err.Error()})
		return
	}
	snapshot, err := handler.store.Resolve(body.Name)
	if err != nil {
		writeJSONResponse(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	report, err := uirestore.PrepareDirectory(request.Context(), handler.db, handler.dataDir, snapshot, handler.maxBytes, *body.HFSRoots)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			status = http.StatusRequestTimeout
		}
		writeJSONResponse(writer, status, map[string]string{"error": err.Error()})
		return
	}
	writeJSONResponse(writer, http.StatusAccepted, RestoreResponse{Files: report.Files, Bytes: report.Bytes, Restarting: true})
	flushResponse(writer)
	release = false
	handler.ready()
}

func (handler *BackupStoreHandler) begin() bool {
	select {
	case handler.active <- struct{}{}:
		return true
	default:
		return false
	}
}

func (handler *BackupStoreHandler) end() {
	<-handler.active
}

func decodeBackupStoreRequest(writer http.ResponseWriter, request *http.Request, destination any, name *string) error {
	request.Body = http.MaxBytesReader(writer, request.Body, backupStoreRequestLimit)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return errors.New("invalid backup request")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("invalid backup request")
	}
	*name = strings.TrimSpace(*name)
	return backupstore.ValidateName(*name)
}

func flushResponse(writer http.ResponseWriter) {
	if flusher, ok := writer.(http.Flusher); ok {
		flusher.Flush()
	}
}
