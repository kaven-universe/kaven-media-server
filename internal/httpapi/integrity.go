package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"kaven.xyz/kaven/kaven-media-server/internal/backupstore"
	"kaven.xyz/kaven/kaven-media-server/internal/bing"
	"kaven.xyz/kaven/kaven-media-server/internal/buildinfo"
	"kaven.xyz/kaven/kaven-media-server/internal/integrity"
	"kaven.xyz/kaven/kaven-media-server/internal/uirestore"
)

// IntegrityHandler runs a read-only integrity check against the active
// database and the managed files in the current data directory.
type IntegrityHandler struct {
	db       *sql.DB
	dataDir  string
	backups  *backupstore.Store
	maxBytes int64
	repairer IntegrityRepairer
	active   chan struct{}
}

type IntegrityRepairer interface {
	RepairOrphanRecords(context.Context, []string) (bing.RepairReport, error)
}

type integrityRepairRequest struct {
	BackupName string `json:"backupName"`
}

type IntegrityRepairResponse struct {
	BackupName string            `json:"backupName"`
	Repair     bing.RepairReport `json:"repair"`
	Integrity  integrity.Report  `json:"integrity"`
}

func NewIntegrityHandler(db *sql.DB, dataDir string) *IntegrityHandler {
	return &IntegrityHandler{db: db, dataDir: dataDir, active: make(chan struct{}, 1)}
}

func NewRepairableIntegrityHandler(db *sql.DB, dataDir string, backups *backupstore.Store, maxBytes int64, repairer IntegrityRepairer) *IntegrityHandler {
	return &IntegrityHandler{
		db: db, dataDir: dataDir, backups: backups, maxBytes: maxBytes,
		repairer: repairer, active: make(chan struct{}, 1),
	}
}

func (handler *IntegrityHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	if handler.db == nil || handler.dataDir == "" {
		writeJSONResponse(writer, http.StatusServiceUnavailable, map[string]string{"error": "storage integrity check is unavailable"})
		return
	}
	if !handler.begin() {
		writeJSONResponse(writer, http.StatusConflict, map[string]string{"error": "a storage integrity check is already active"})
		return
	}
	defer handler.end()

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

func (handler *IntegrityHandler) Repair(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	if handler.db == nil || handler.dataDir == "" || handler.backups == nil || handler.maxBytes <= 0 || handler.repairer == nil {
		writeJSONResponse(writer, http.StatusServiceUnavailable, map[string]string{"error": "storage integrity repair is unavailable"})
		return
	}
	if !handler.begin() {
		writeJSONResponse(writer, http.StatusConflict, map[string]string{"error": "a storage integrity operation is already active"})
		return
	}
	defer handler.end()

	var body integrityRepairRequest
	if err := decodeBackupStoreRequest(writer, request, &body, &body.BackupName); err != nil {
		writeJSONResponse(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	snapshot, err := handler.backups.Resolve(body.BackupName)
	if err != nil {
		writeJSONResponse(writer, http.StatusBadRequest, map[string]string{"error": "a valid database backup is required before repair: " + err.Error()})
		return
	}
	if _, err := uirestore.ReadSnapshotSettings(request.Context(), handler.dataDir, snapshot, handler.maxBytes); err != nil {
		writeJSONResponse(writer, http.StatusBadRequest, map[string]string{"error": "the required database backup is invalid: " + err.Error()})
		return
	}

	before, err := integrity.Check(request.Context(), handler.db, handler.dataDir)
	if err != nil {
		handler.repairError(writer, err, "could not inspect storage before repair")
		return
	}
	orphans := make([]string, 0)
	for _, issue := range before.Issues {
		if issue.Kind == integrity.IssueOrphanFile && issue.Path != "" {
			orphans = append(orphans, issue.Path)
		}
	}
	repairReport, err := handler.repairer.RepairOrphanRecords(request.Context(), orphans)
	if err != nil {
		handler.repairError(writer, err, "could not attempt storage repair")
		return
	}
	after, err := integrity.Check(request.Context(), handler.db, handler.dataDir)
	if err != nil {
		handler.repairError(writer, err, "storage repair completed but the follow-up check failed")
		return
	}
	after.Execution = &integrity.ExecutionMetadata{GeneratedAt: time.Now().UTC(), Producer: buildinfo.Current()}
	writeJSONResponse(writer, http.StatusOK, IntegrityRepairResponse{
		BackupName: body.BackupName, Repair: repairReport, Integrity: after,
	})
}

func (handler *IntegrityHandler) begin() bool {
	select {
	case handler.active <- struct{}{}:
		return true
	default:
		return false
	}
}

func (handler *IntegrityHandler) end() { <-handler.active }

func (handler *IntegrityHandler) repairError(writer http.ResponseWriter, err error, message string) {
	status := http.StatusInternalServerError
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		status = http.StatusRequestTimeout
	}
	slog.Error(message, "error", err)
	writeJSONResponse(writer, status, map[string]string{"error": message})
}
