package httpapi_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"kaven.xyz/kaven/kaven-media-server/internal/config"
	"kaven.xyz/kaven/kaven-media-server/internal/httpapi"
)

type applicationSettingsStore struct {
	settings config.RuntimeSettings
	updates  int
}

func (store *applicationSettingsStore) Get(context.Context) (config.RuntimeSettings, error) {
	return store.settings, nil
}
func (store *applicationSettingsStore) Update(_ context.Context, settings config.RuntimeSettings) error {
	store.settings = settings
	store.updates++
	return nil
}

func TestApplicationSettingsHandlerValidatesAndRestarts(t *testing.T) {
	store := &applicationSettingsStore{settings: config.DefaultRuntimeSettings(t.TempDir())}
	restarts := 0
	handler := httpapi.NewApplicationSettingsHandler(store, nil, func() { restarts++ })
	bad := httptest.NewRequest(http.MethodPut, "/api/v1/admin/settings", bytes.NewBufferString(`{"publicUploads":true}`))
	bad.Header.Set("Content-Type", "application/json")
	badResponse := httptest.NewRecorder()
	handler.ServeHTTP(badResponse, bad)
	if badResponse.Code != http.StatusBadRequest || store.updates != 0 {
		t.Fatalf("bad update = %d, updates = %d", badResponse.Code, store.updates)
	}

	body := `{"uploadDirectory":"media/uploads","downloadDirectory":"media/downloads","publicUploads":false,"maxFileCount":25,"maxImageFileSize":10485760,"maxHFSFileSize":20971520,"rememberDurationDays":60,"bingSyncEnabled":false,"bingSyncIntervalHours":12,"allowedDomainNames":["example.com"],"hfsRoots":[{"name":"files","path":"hfs/files","public":true}]}`
	request := httptest.NewRequest(http.MethodPut, "/api/v1/admin/settings", bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || store.updates != 1 || restarts != 1 || store.settings.PublicUploads ||
		store.settings.BingSyncEnabled || store.settings.BingSyncIntervalHours != 12 ||
		store.settings.UploadDirectory != "media/uploads" || store.settings.DownloadDirectory != "media/downloads" {
		t.Fatalf("update = %d, updates = %d, restarts = %d, settings = %+v", response.Code, store.updates, restarts, store.settings)
	}
}
