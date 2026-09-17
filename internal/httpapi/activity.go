package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"kaven.xyz/kaven/kaven-media-server/internal/repository"
)

const (
	defaultActivityPageSize = 25
	maxActivityPageSize     = 100
)

type AccessRecordLister interface {
	Count(context.Context) (int64, error)
	List(context.Context, int, int) ([]repository.AccessRecord, error)
}

type DownloadRecordLister interface {
	Count(context.Context) (int64, error)
	List(context.Context, int, int) ([]repository.DownloadRecord, error)
}

type ActivityHandler struct {
	accesses  AccessRecordLister
	downloads DownloadRecordLister
}

type ActivityEntry struct {
	ID          string    `json:"id"`
	Kind        string    `json:"kind"`
	Resource    string    `json:"resource"`
	ImageID     string    `json:"imageId,omitempty"`
	IP          string    `json:"ip"`
	OriginalURL string    `json:"originalUrl"`
	UserAgent   *string   `json:"userAgent,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
}

type ActivityResponse struct {
	Kind     string          `json:"kind"`
	Page     int             `json:"page"`
	PageSize int             `json:"pageSize"`
	Total    int64           `json:"total"`
	Records  []ActivityEntry `json:"records"`
}

func NewActivityHandler(accesses AccessRecordLister, downloads DownloadRecordLister) *ActivityHandler {
	return &ActivityHandler{accesses: accesses, downloads: downloads}
}

func (handler *ActivityHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	kind := request.URL.Query().Get("kind")
	if kind == "" {
		kind = "access"
	}
	page, err := positiveQueryInteger(request, "page", 1, 0)
	if err != nil {
		writeJSONResponse(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	pageSize, err := positiveQueryInteger(request, "pageSize", defaultActivityPageSize, maxActivityPageSize)
	if err != nil {
		writeJSONResponse(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if page > int(^uint(0)>>1)/pageSize {
		writeJSONResponse(writer, http.StatusBadRequest, map[string]string{"error": "page is too large"})
		return
	}
	offset := (page - 1) * pageSize

	response := ActivityResponse{Kind: kind, Page: page, PageSize: pageSize, Records: make([]ActivityEntry, 0)}
	switch kind {
	case "access":
		if handler.accesses == nil {
			handler.unavailable(writer)
			return
		}
		response.Total, err = handler.accesses.Count(request.Context())
		if err == nil {
			var records []repository.AccessRecord
			records, err = handler.accesses.List(request.Context(), pageSize, offset)
			for _, record := range records {
				response.Records = append(response.Records, ActivityEntry{
					ID: record.ID, Kind: kind, Resource: record.ImageID, ImageID: record.ImageID,
					IP: record.IP, OriginalURL: record.OriginalURL, CreatedAt: record.CreatedAt,
				})
			}
		}
	case "download":
		if handler.downloads == nil {
			handler.unavailable(writer)
			return
		}
		response.Total, err = handler.downloads.Count(request.Context())
		if err == nil {
			var records []repository.DownloadRecord
			records, err = handler.downloads.List(request.Context(), pageSize, offset)
			for _, record := range records {
				response.Records = append(response.Records, ActivityEntry{
					ID: record.ID, Kind: kind, Resource: record.File, IP: record.IP,
					OriginalURL: record.OriginalURL, UserAgent: record.UserAgent, CreatedAt: record.CreatedAt,
				})
			}
		}
	default:
		writeJSONResponse(writer, http.StatusBadRequest, map[string]string{"error": "kind must be access or download"})
		return
	}
	if err != nil {
		writeJSONResponse(writer, http.StatusInternalServerError, map[string]string{"error": "could not list activity records"})
		return
	}
	writeJSONResponse(writer, http.StatusOK, response)
}

func positiveQueryInteger(request *http.Request, name string, fallback, maximum int) (int, error) {
	raw := request.URL.Query().Get(name)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 || maximum > 0 && value > maximum {
		if maximum > 0 {
			return 0, fmt.Errorf("%s must be between 1 and %d", name, maximum)
		}
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return value, nil
}

func (handler *ActivityHandler) unavailable(writer http.ResponseWriter) {
	writeJSONResponse(writer, http.StatusServiceUnavailable, map[string]string{"error": "activity records are unavailable"})
}
