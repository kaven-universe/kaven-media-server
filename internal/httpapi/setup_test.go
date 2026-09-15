package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"kaven.xyz/kaven/kaven-media-server/internal/auth"
	"kaven.xyz/kaven/kaven-media-server/internal/config"
	"kaven.xyz/kaven/kaven-media-server/internal/httpapi"
	"kaven.xyz/kaven/kaven-media-server/internal/repository"
)

type credentialCreator struct {
	username string
	hash     []byte
	settings config.RuntimeSettings
	err      error
}

func (creator *credentialCreator) Initialize(_ context.Context, username string, passwordHash []byte, settings config.RuntimeSettings) error {
	creator.username = username
	creator.hash = append([]byte(nil), passwordHash...)
	creator.settings = settings
	return creator.err
}

func TestSetupCreatesUsableAdministratorCredential(t *testing.T) {
	creator := &credentialCreator{}
	ready := false
	handler := httpapi.NewSetupHandler(false, creator, config.DefaultRuntimeSettings(t.TempDir()), nil, func() { ready = true })
	request := httptest.NewRequest(http.MethodPost, "/api/v1/setup", bytes.NewBufferString(
		`{"username":"owner","password":"a-secure-password-for-testing"}`,
	))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || !ready || creator.username != "owner" || !creator.settings.PublicUploads || creator.settings.MaxFileCount != 100 {
		t.Fatalf("setup response = %d, ready = %v, username = %q", response.Code, ready, creator.username)
	}
	authenticator, err := auth.NewFromPasswordHash(creator.username, creator.hash, auth.Options{})
	if err != nil {
		t.Fatalf("load stored password hash: %v", err)
	}
	loginBody, _ := json.Marshal(map[string]string{"username": "owner", "password": "a-secure-password-for-testing"})
	login := httptest.NewRequest(http.MethodPost, "/api/v1/admin/login", bytes.NewReader(loginBody))
	login.Header.Set("Content-Type", "application/json")
	loginResponse := httptest.NewRecorder()
	authenticator.LoginHandler().ServeHTTP(loginResponse, login)
	if loginResponse.Code != http.StatusOK {
		t.Fatalf("stored credential login = %d: %s", loginResponse.Code, loginResponse.Body.String())
	}
}

func TestSetupRejectsUnsafeOrRepeatedInitialization(t *testing.T) {
	tests := []struct {
		name        string
		initialized bool
		body        string
		origin      string
		creator     *credentialCreator
		want        int
	}{
		{name: "already initialized", initialized: true, body: `{}`, creator: &credentialCreator{}, want: http.StatusConflict},
		{name: "cross origin", body: `{}`, origin: "https://attacker.example", creator: &credentialCreator{}, want: http.StatusForbidden},
		{name: "short password", body: `{"username":"admin","password":"short"}`, creator: &credentialCreator{}, want: http.StatusBadRequest},
		{name: "atomic conflict", body: `{"username":"admin","password":"a-secure-password-for-testing"}`, creator: &credentialCreator{err: repository.ErrAdminAlreadyInitialized}, want: http.StatusConflict},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := httpapi.NewSetupHandler(test.initialized, test.creator, config.DefaultRuntimeSettings(t.TempDir()), nil, nil)
			request := httptest.NewRequest(http.MethodPost, "/api/v1/setup", bytes.NewBufferString(test.body))
			request.Header.Set("Content-Type", "application/json")
			if test.origin != "" {
				request.Header.Set("Origin", test.origin)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.want {
				t.Fatalf("response = %d, want %d: %s", response.Code, test.want, response.Body.String())
			}
		})
	}
}

func TestSetupStatus(t *testing.T) {
	defaults := config.DefaultRuntimeSettings(t.TempDir())
	for _, initialized := range []bool{false, true} {
		response := httptest.NewRecorder()
		httpapi.NewSetupStatusHandler(initialized, defaults).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/setup/status", nil))
		var status struct {
			Initialized bool                    `json:"initialized"`
			Defaults    *config.RuntimeSettings `json:"defaults"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &status); err != nil {
			t.Fatalf("decode status response: %v", err)
		}
		if response.Code != http.StatusOK || status.Initialized != initialized {
			t.Fatalf("status response = %d %q", response.Code, response.Body.String())
		}
		if initialized && status.Defaults != nil {
			t.Fatalf("initialized status exposed defaults: %s", response.Body.String())
		}
		if !initialized && (status.Defaults == nil || len(status.Defaults.HFSRoots) != 1 || status.Defaults.HFSRoots[0].Path != defaults.HFSRoots[0].Path) {
			t.Fatalf("uninitialized status defaults = %#v, want %#v", status.Defaults, defaults)
		}
	}
}
