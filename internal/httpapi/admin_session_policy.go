package httpapi

import (
	"context"
	"encoding/json"
	"mime"
	"net/http"
	"time"

	"kaven.xyz/kaven/kaven-media-server/internal/auth"
)

const maxSessionPolicyBody int64 = 4 * 1024

type AdminSessionSettings interface {
	RememberDurationDays(ctx context.Context) (int, error)
	SetRememberDurationDays(ctx context.Context, days int) error
}

type sessionPolicyRequest struct {
	RememberDurationDays int `json:"rememberDurationDays"`
}

func NewAdminSessionPolicyStatusHandler(settings AdminSessionSettings) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")
		days, err := settings.RememberDurationDays(request.Context())
		if err != nil {
			writeSetupError(writer, http.StatusInternalServerError, "session settings are unavailable")
			return
		}
		writeSetupJSON(writer, http.StatusOK, sessionPolicyRequest{RememberDurationDays: days})
	})
}

func NewAdminSessionPolicyHandler(authenticator *auth.Authenticator, settings AdminSessionSettings) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")
		mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
		if err != nil || mediaType != "application/json" {
			writeSetupError(writer, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
			return
		}
		request.Body = http.MaxBytesReader(writer, request.Body, maxSessionPolicyBody)
		decoder := json.NewDecoder(request.Body)
		decoder.DisallowUnknownFields()
		var policy sessionPolicyRequest
		if err := decoder.Decode(&policy); err != nil || !jsonEnds(decoder) {
			writeSetupError(writer, http.StatusBadRequest, "invalid session settings request")
			return
		}
		if policy.RememberDurationDays < 1 || policy.RememberDurationDays > 365 {
			writeSetupError(writer, http.StatusBadRequest, "rememberDurationDays must be between 1 and 365")
			return
		}
		if err := settings.SetRememberDurationDays(request.Context(), policy.RememberDurationDays); err != nil {
			writeSetupError(writer, http.StatusInternalServerError, "session settings could not be saved")
			return
		}
		if err := authenticator.SetRememberDuration(time.Duration(policy.RememberDurationDays) * 24 * time.Hour); err != nil {
			writeSetupError(writer, http.StatusInternalServerError, "session settings could not be applied")
			return
		}
		writeSetupJSON(writer, http.StatusOK, policy)
	})
}
