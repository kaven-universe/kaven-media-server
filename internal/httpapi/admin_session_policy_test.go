package httpapi_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"kaven.xyz/kaven/kaven-media-server/internal/auth"
	"kaven.xyz/kaven/kaven-media-server/internal/httpapi"
)

type sessionSettings struct {
	days   int
	getErr error
	setErr error
}

func (settings *sessionSettings) RememberDurationDays(context.Context) (int, error) {
	return settings.days, settings.getErr
}

func (settings *sessionSettings) SetRememberDurationDays(_ context.Context, days int) error {
	if settings.setErr != nil {
		return settings.setErr
	}
	settings.days = days
	return nil
}

func TestAdminSessionPolicyReadAndUpdate(t *testing.T) {
	authenticator, err := auth.New("admin", []byte("password long enough for testing"))
	if err != nil {
		t.Fatal(err)
	}
	settings := &sessionSettings{days: 30}
	status := httptest.NewRecorder()
	httpapi.NewAdminSessionPolicyStatusHandler(settings).ServeHTTP(status, httptest.NewRequest(http.MethodGet, "/", nil))
	if status.Code != http.StatusOK || status.Body.String() != "{\"rememberDurationDays\":30}\n" {
		t.Fatalf("policy status = %d %q", status.Code, status.Body.String())
	}

	request := httptest.NewRequest(http.MethodPut, "/", bytes.NewBufferString(`{"rememberDurationDays":7}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	httpapi.NewAdminSessionPolicyHandler(authenticator, settings).ServeHTTP(response, request)
	if response.Code != http.StatusOK || settings.days != 7 || authenticator.RememberDuration() != 7*24*time.Hour {
		t.Fatalf("policy update = %d %q, days = %d, duration = %s", response.Code, response.Body.String(), settings.days, authenticator.RememberDuration())
	}
}

func TestAdminSessionPolicyRejectsInvalidOrFailedUpdates(t *testing.T) {
	authenticator, _ := auth.New("admin", []byte("password long enough for testing"))
	for _, test := range []struct {
		name     string
		body     string
		settings *sessionSettings
		want     int
	}{
		{name: "zero", body: `{"rememberDurationDays":0}`, settings: &sessionSettings{}, want: http.StatusBadRequest},
		{name: "too long", body: `{"rememberDurationDays":366}`, settings: &sessionSettings{}, want: http.StatusBadRequest},
		{name: "unknown field", body: `{"rememberDurationDays":30,"other":true}`, settings: &sessionSettings{}, want: http.StatusBadRequest},
		{name: "storage failure", body: `{"rememberDurationDays":30}`, settings: &sessionSettings{setErr: errors.New("unavailable")}, want: http.StatusInternalServerError},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPut, "/", bytes.NewBufferString(test.body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			httpapi.NewAdminSessionPolicyHandler(authenticator, test.settings).ServeHTTP(response, request)
			if response.Code != test.want {
				t.Fatalf("response = %d, want %d: %s", response.Code, test.want, response.Body.String())
			}
		})
	}
}
