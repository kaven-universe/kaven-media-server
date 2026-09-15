package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"kaven.xyz/kaven/kaven-media-server/internal/auth"
	"kaven.xyz/kaven/kaven-media-server/internal/httpapi"
)

type passwordUpdater struct {
	hash []byte
	err  error
}

func (updater *passwordUpdater) UpdatePassword(_ context.Context, passwordHash []byte) error {
	updater.hash = append([]byte(nil), passwordHash...)
	return updater.err
}

func TestAdminPasswordChangeReplacesCredentialAndSessions(t *testing.T) {
	const oldPassword = "old password for password test"
	const newPassword = "new password for password test"
	authenticator, err := auth.New("admin", []byte(oldPassword))
	if err != nil {
		t.Fatal(err)
	}
	cookie := loginCookie(t, authenticator, oldPassword)
	updater := &passwordUpdater{}
	ready := false
	handler := authenticator.Protect(httpapi.NewAdminPasswordHandler(authenticator, updater, false, func() { ready = true }))
	body, _ := json.Marshal(map[string]string{"currentPassword": oldPassword, "newPassword": newPassword})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/password", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !ready {
		t.Fatalf("password change = %d %q, ready = %v", response.Code, response.Body.String(), ready)
	}

	sessionRequest := httptest.NewRequest(http.MethodGet, "/api/v1/admin/session", nil)
	sessionRequest.AddCookie(cookie)
	sessionResponse := httptest.NewRecorder()
	authenticator.SessionHandler().ServeHTTP(sessionResponse, sessionRequest)
	if sessionResponse.Code != http.StatusOK || sessionResponse.Body.String() != "{\"authenticated\":false}\n" {
		t.Fatalf("old session after password change = %d %q", sessionResponse.Code, sessionResponse.Body.String())
	}

	reloaded, err := auth.NewFromPasswordHash("admin", updater.hash, auth.Options{})
	if err != nil {
		t.Fatal(err)
	}
	_ = loginCookie(t, reloaded, newPassword)
}

func TestAdminPasswordChangeRejectsInvalidRequests(t *testing.T) {
	const password = "current password for testing"
	authenticator, err := auth.New("admin", []byte(password))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name              string
		managedExternally bool
		current           string
		newPassword       string
		want              int
	}{
		{name: "external credential", managedExternally: true, current: password, newPassword: "replacement password for testing", want: http.StatusConflict},
		{name: "incorrect current password", current: "incorrect password for testing", newPassword: "replacement password for testing", want: http.StatusUnauthorized},
		{name: "same password", current: password, newPassword: password, want: http.StatusBadRequest},
		{name: "short new password", current: password, newPassword: "short", want: http.StatusBadRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body, _ := json.Marshal(map[string]string{"currentPassword": test.current, "newPassword": test.newPassword})
			request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/password", bytes.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			httpapi.NewAdminPasswordHandler(authenticator, &passwordUpdater{}, test.managedExternally, nil).ServeHTTP(response, request)
			if response.Code != test.want {
				t.Fatalf("response = %d, want %d: %s", response.Code, test.want, response.Body.String())
			}
		})
	}
}

func TestAdminPasswordStatus(t *testing.T) {
	response := httptest.NewRecorder()
	httpapi.NewAdminPasswordStatusHandler("owner", true).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/admin/password", nil))
	if response.Code != http.StatusOK || response.Body.String() != "{\"managedExternally\":true,\"username\":\"owner\"}\n" {
		t.Fatalf("password status = %d %q", response.Code, response.Body.String())
	}
}

func loginCookie(t *testing.T, authenticator *auth.Authenticator, password string) *http.Cookie {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"username": "admin", "password": password})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/login", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	authenticator.LoginHandler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("login = %d: %s", response.Code, response.Body.String())
	}
	return response.Result().Cookies()[0]
}
