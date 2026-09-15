package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"

	"kaven.xyz/kaven/kaven-media-server/internal/auth"
	"kaven.xyz/kaven/kaven-media-server/internal/config"
	"kaven.xyz/kaven/kaven-media-server/internal/repository"
)

const maxSetupBody int64 = 128 * 1024

type ServerInitializer interface {
	Initialize(ctx context.Context, username string, passwordHash []byte, settings config.RuntimeSettings) error
}

type setupRequest struct {
	Username string                  `json:"username"`
	Password string                  `json:"password"`
	Settings *config.RuntimeSettings `json:"settings,omitempty"`
}

type setupStatusResponse struct {
	Initialized bool                    `json:"initialized"`
	Defaults    *config.RuntimeSettings `json:"defaults,omitempty"`
}

func NewSetupStatusHandler(initialized bool, defaults config.RuntimeSettings) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")
		response := setupStatusResponse{Initialized: initialized}
		if !initialized {
			response.Defaults = &defaults
		}
		writeSetupJSON(writer, http.StatusOK, response)
	})
}

func NewSetupHandler(initialized bool, credentials ServerInitializer, defaults config.RuntimeSettings, validateSettings func(config.RuntimeSettings) error, ready func()) http.Handler {
	slot := make(chan struct{}, 1)
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")
		if initialized {
			writeSetupError(writer, http.StatusConflict, "server is already initialized")
			return
		}
		if !auth.IsSameOrigin(request) {
			writeSetupError(writer, http.StatusForbidden, "cross-origin request rejected")
			return
		}
		select {
		case slot <- struct{}{}:
			defer func() { <-slot }()
		default:
			writeSetupError(writer, http.StatusTooManyRequests, "initialization is already in progress")
			return
		}
		mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
		if err != nil || mediaType != "application/json" {
			writeSetupError(writer, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
			return
		}
		request.Body = http.MaxBytesReader(writer, request.Body, maxSetupBody)
		decoder := json.NewDecoder(request.Body)
		decoder.DisallowUnknownFields()
		var setup setupRequest
		if err := decoder.Decode(&setup); err != nil || !jsonEnds(decoder) {
			writeSetupError(writer, http.StatusBadRequest, "invalid initialization request")
			return
		}
		password := []byte(setup.Password)
		setup.Password = ""
		defer clear(password)
		if err := config.ValidateAdminUsername(setup.Username); err != nil {
			writeSetupError(writer, http.StatusBadRequest, err.Error())
			return
		}
		if err := config.ValidateAdminPassword(password); err != nil {
			writeSetupError(writer, http.StatusBadRequest, err.Error())
			return
		}
		settings := defaults
		if setup.Settings != nil {
			settings = *setup.Settings
		}
		if err := config.ValidateRuntimeSettings(settings); err != nil {
			writeSetupError(writer, http.StatusBadRequest, err.Error())
			return
		}
		if validateSettings != nil {
			if err := validateSettings(settings); err != nil {
				writeSetupError(writer, http.StatusBadRequest, err.Error())
				return
			}
		}
		passwordHash, err := auth.HashPassword(password)
		if err != nil {
			writeSetupError(writer, http.StatusInternalServerError, "initialization failed")
			return
		}
		defer clear(passwordHash)
		if err := credentials.Initialize(request.Context(), setup.Username, passwordHash, settings); err != nil {
			if errors.Is(err, repository.ErrAdminAlreadyInitialized) {
				writeSetupError(writer, http.StatusConflict, "server is already initialized")
				return
			}
			writeSetupError(writer, http.StatusInternalServerError, "initialization failed")
			return
		}
		writeSetupJSON(writer, http.StatusCreated, map[string]bool{"initialized": true})
		if ready != nil {
			ready()
		}
	})
}

func jsonEnds(decoder *json.Decoder) bool {
	var extra any
	return errors.Is(decoder.Decode(&extra), io.EOF)
}

func writeSetupError(writer http.ResponseWriter, status int, message string) {
	writeSetupJSON(writer, status, map[string]string{"error": message})
}

func writeSetupJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
