package httpapi

import (
	"context"
	"log/slog"
	"net/http"

	"kaven.xyz/kaven/kaven-media-server/internal/buildinfo"
	"kaven.xyz/kaven/kaven-media-server/internal/versioncheck"
)

type ReleaseChecker interface {
	Latest(context.Context) (versioncheck.Release, error)
}

type VersionStatusResponse struct {
	Current         buildinfo.Info        `json:"current"`
	Latest          *versioncheck.Release `json:"latest,omitempty"`
	UpdateAvailable *bool                 `json:"updateAvailable,omitempty"`
}

type VersionHandler struct {
	checker ReleaseChecker
}

func NewVersionHandler(checker ReleaseChecker) *VersionHandler {
	return &VersionHandler{checker: checker}
}

func (handler *VersionHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	response := VersionStatusResponse{Current: buildinfo.Current()}
	if !request.URL.Query().Has("check") {
		writeJSONResponse(writer, http.StatusOK, response)
		return
	}
	if handler.checker == nil {
		writeJSONResponse(writer, http.StatusServiceUnavailable, map[string]string{"error": "version checking is unavailable"})
		return
	}
	latest, err := handler.checker.Latest(request.Context())
	if err != nil {
		slog.Warn("check latest application version", "error", err)
		writeJSONResponse(writer, http.StatusBadGateway, map[string]string{"error": "could not check the latest version"})
		return
	}
	response.Latest = &latest
	comparison, err := versioncheck.Compare(response.Current.Version, latest.Version)
	if err == nil {
		available := comparison < 0
		response.UpdateAvailable = &available
	}
	writeJSONResponse(writer, http.StatusOK, response)
}
