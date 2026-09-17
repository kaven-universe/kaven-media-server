package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime"
	"net/http"

	"kaven.xyz/kaven/kaven-media-server/internal/auth"
	"kaven.xyz/kaven/kaven-media-server/internal/config"
	"kaven.xyz/kaven/kaven-media-server/internal/repository"
)

const maxPasswordChangeBody int64 = 8 * 1024

type AdminPasswordUpdater interface {
	UpdatePassword(ctx context.Context, passwordHash []byte) error
}

type passwordChangeRequest struct {
	CurrentPassword string `json:"currentPassword"`
	NewPassword     string `json:"newPassword"`
}

func NewAdminPasswordStatusHandler(username string, managedExternally bool) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")
		writeSetupJSON(writer, http.StatusOK, map[string]any{
			"username": username, "managedExternally": managedExternally,
		})
	})
}

func NewAdminPasswordHandler(authenticator *auth.Authenticator, credentials AdminPasswordUpdater, managedExternally bool, ready func()) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")
		if managedExternally {
			writeSetupError(writer, http.StatusConflict, "administrator password is managed by the deployment environment")
			return
		}
		mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
		if err != nil || mediaType != "application/json" {
			writeSetupError(writer, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
			return
		}
		request.Body = http.MaxBytesReader(writer, request.Body, maxPasswordChangeBody)
		decoder := json.NewDecoder(request.Body)
		decoder.DisallowUnknownFields()
		var change passwordChangeRequest
		if err := decoder.Decode(&change); err != nil || !jsonEnds(decoder) {
			writeSetupError(writer, http.StatusBadRequest, "invalid password change request")
			return
		}
		currentPassword := []byte(change.CurrentPassword)
		newPassword := []byte(change.NewPassword)
		change.CurrentPassword = ""
		change.NewPassword = ""
		defer clear(currentPassword)
		defer clear(newPassword)
		if err := config.ValidateAdminPassword(newPassword); err != nil {
			writeSetupError(writer, http.StatusBadRequest, err.Error())
			return
		}
		if bytes.Equal(currentPassword, newPassword) {
			writeSetupError(writer, http.StatusBadRequest, "new password must differ from the current password")
			return
		}
		valid, err := authenticator.VerifyPassword(currentPassword)
		if errors.Is(err, auth.ErrLoginRateLimited) {
			writeSetupError(writer, http.StatusTooManyRequests, "password verification is busy")
			return
		}
		if err != nil {
			writeSetupError(writer, http.StatusInternalServerError, "password change failed")
			return
		}
		if !valid {
			writeSetupError(writer, http.StatusUnauthorized, "current password is incorrect")
			return
		}
		passwordHash, err := auth.HashPassword(newPassword)
		if err != nil {
			writeSetupError(writer, http.StatusInternalServerError, "password change failed")
			return
		}
		defer clear(passwordHash)
		if err := credentials.UpdatePassword(request.Context(), passwordHash); err != nil {
			if errors.Is(err, repository.ErrAdminNotInitialized) {
				writeSetupError(writer, http.StatusConflict, "administrator password is not stored by this server")
				return
			}
			writeSetupError(writer, http.StatusInternalServerError, "password change failed")
			return
		}
		authenticator.RevokeAllSessions()
		writeSetupJSON(writer, http.StatusOK, map[string]bool{"changed": true})
		if ready != nil {
			ready()
		}
	})
}
