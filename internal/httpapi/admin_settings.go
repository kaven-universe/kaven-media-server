package httpapi

import (
	"context"
	"encoding/json"
	"mime"
	"net/http"

	"kaven.xyz/kaven/kaven-media-server/internal/config"
)

const maxApplicationSettingsBody int64 = 128 * 1024

type ApplicationSettingsStore interface {
	Get(context.Context) (config.RuntimeSettings, error)
	Update(context.Context, config.RuntimeSettings) error
}

func NewApplicationSettingsHandler(store ApplicationSettingsStore, validate func(config.RuntimeSettings) error, ready func()) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")
		if request.Method == http.MethodGet {
			settings, err := store.Get(request.Context())
			if err != nil {
				writeSetupError(writer, http.StatusInternalServerError, "settings are unavailable")
				return
			}
			writeSetupJSON(writer, http.StatusOK, settings)
			return
		}
		mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
		if err != nil || mediaType != "application/json" {
			writeSetupError(writer, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
			return
		}
		request.Body = http.MaxBytesReader(writer, request.Body, maxApplicationSettingsBody)
		decoder := json.NewDecoder(request.Body)
		decoder.DisallowUnknownFields()
		var settings config.RuntimeSettings
		if err := decoder.Decode(&settings); err != nil || !jsonEnds(decoder) {
			writeSetupError(writer, http.StatusBadRequest, "invalid settings request")
			return
		}
		if err := config.ValidateRuntimeSettings(settings); err != nil {
			writeSetupError(writer, http.StatusBadRequest, err.Error())
			return
		}
		if validate != nil {
			if err := validate(settings); err != nil {
				writeSetupError(writer, http.StatusBadRequest, err.Error())
				return
			}
		}
		if err := store.Update(request.Context(), settings); err != nil {
			writeSetupError(writer, http.StatusInternalServerError, "settings could not be saved")
			return
		}
		writeSetupJSON(writer, http.StatusOK, settings)
		if ready != nil {
			ready()
		}
	})
}
