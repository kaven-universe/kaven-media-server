package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"kaven.xyz/kaven/kaven-media-server/internal/auth"
	"kaven.xyz/kaven/kaven-media-server/internal/config"
	"kaven.xyz/kaven/kaven-media-server/internal/database"
	"kaven.xyz/kaven/kaven-media-server/internal/hfsdownload"
	"kaven.xyz/kaven/kaven-media-server/internal/imageaccess"
	"kaven.xyz/kaven/kaven-media-server/internal/imageproc"
	"kaven.xyz/kaven/kaven-media-server/internal/repository"
	"kaven.xyz/kaven/kaven-media-server/internal/storage"
)

func TestRoutes(t *testing.T) {
	dataDir := t.TempDir()
	db, err := database.Open(context.Background(), dataDir)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	store, err := storage.New(dataDir)
	if err != nil {
		t.Fatalf("create storage: %v", err)
	}
	processor, err := imageproc.NewDefaultProcessor(1)
	if err != nil {
		t.Fatalf("create image processor: %v", err)
	}
	t.Cleanup(processor.Close)
	accessRecorder := newTestAccessRecorder(t, db)
	handler, err := routes(db, config.Config{}, store, processor, accessRecorder, nil, nil)
	if err != nil {
		t.Fatalf("create routes: %v", err)
	}

	t.Run("health", func(t *testing.T) {
		response := serve(t, handler, http.MethodGet, "/healthz")
		if response.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
		}
		assertContentType(t, response, "application/json; charset=utf-8")
		assertJSON(t, response.Body.Bytes(), map[string]any{"status": "ok"})
	})

	t.Run("image referer policy", func(t *testing.T) {
		filtered, err := routes(db, config.Config{AllowedDomainNames: []string{"example.com"}}, store, processor, accessRecorder, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range []string{"/image/0123456789abcdef01234567", "/image/0123456789abcdef01234567?info", "/image/bing/random"} {
			for _, method := range []string{"GET", "HEAD"} {
				request := httptest.NewRequest(method, path, nil)
				request.Header.Set("Referer", "https://other.test/")
				response := httptest.NewRecorder()
				filtered.ServeHTTP(response, request)
				if response.Code != 403 {
					t.Fatalf("%s %s = %d", method, path, response.Code)
				}
				request.Header.Set("Referer", "https://example.com/")
				response = httptest.NewRecorder()
				filtered.ServeHTTP(response, request)
				if response.Code != 404 {
					t.Fatalf("allowed %s %s = %d, want missing-image 404", method, path, response.Code)
				}
			}
		}
		request := httptest.NewRequest("GET", "/healthz", nil)
		request.Header.Set("Referer", "https://other.test/")
		response := httptest.NewRecorder()
		filtered.ServeHTTP(response, request)
		if response.Code != 200 {
			t.Fatal("image policy restricted health route")
		}
	})

	t.Run("server info", func(t *testing.T) {
		response := serve(t, handler, http.MethodGet, "/server/info")
		if response.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
		}
		assertContentType(t, response, "application/json; charset=utf-8")
		assertJSON(t, response.Body.Bytes(), map[string]any{
			"upload": map[string]any{
				"maxFileCount":     float64(100),
				"maxImageFileSize": float64(100 * 1024 * 1024),
				"maxHfsFileSize":   float64(1000 * 1024 * 1024),
			},
		})
	})

	t.Run("first-run setup status", func(t *testing.T) {
		response := serve(t, handler, http.MethodGet, "/api/v1/setup/status")
		var status struct {
			Initialized bool                    `json:"initialized"`
			Defaults    *config.RuntimeSettings `json:"defaults"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &status); err != nil {
			t.Fatalf("decode setup status: %v", err)
		}
		if response.Code != http.StatusOK || status.Initialized || status.Defaults == nil || len(status.Defaults.HFSRoots) != 1 || status.Defaults.HFSRoots[0].Path != "upload" {
			t.Fatalf("setup status = %d %q", response.Code, response.Body.String())
		}
	})

	t.Run("embedded UI", func(t *testing.T) {
		response := serve(t, handler, http.MethodGet, "/")
		if response.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
		}
		if response.Body.Len() == 0 {
			t.Fatal("response body is empty")
		}
	})

	t.Run("unsupported API method", func(t *testing.T) {
		response := serve(t, handler, http.MethodPost, "/healthz")
		if response.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want %d", response.Code, http.StatusNotFound)
		}
	})

	t.Run("image access is persisted", func(t *testing.T) {
		timestamp := time.Date(2026, time.September, 4, 1, 2, 3, 0, time.UTC)
		image := repository.Image{
			ID: "abcdef0123456789abcdef01", UUID: "abcdef0123456789abcdef0123456789",
			SHA1: "abcdef0123456789abcdef0123456789abcdef01", Folder: "2026", Name: "source.png",
			OriginalName: "source.png", MIMEType: "image/png", Size: 8, UploadDate: timestamp,
			UploadIP: "127.0.0.1", CreatedAt: timestamp, UpdatedAt: timestamp,
		}
		if err := repository.NewImageRepository(db).Create(context.Background(), image); err != nil {
			t.Fatal(err)
		}
		if err := store.MkdirAll("upload/2026", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(store.Root(), "upload", "2026", image.Name), []byte("\x89PNG\r\n\x1a\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		response := serve(t, handler, http.MethodGet, "/image/"+image.ID)
		if response.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", response.Code)
		}
		if err := accessRecorder.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
		count, err := repository.NewAccessRecordRepository(db).CountByImageID(context.Background(), image.ID)
		if err != nil || count != 1 {
			t.Fatalf("access count = %d, error = %v", count, err)
		}
	})

	t.Run("random Bing image", func(t *testing.T) {
		timestamp := time.Date(2026, time.September, 4, 1, 2, 3, 0, time.UTC)
		file := "2026/wallpaper.jpg"
		url := "/th?id=OHR.Wallpaper_UHD.jpg"
		image := repository.BingImage{
			ID: "bing-image", URL: url, File: &file, CreatedAt: timestamp, UpdatedAt: timestamp,
		}
		if err := repository.NewBingImageRepository(db).Upsert(context.Background(), image); err != nil {
			t.Fatal(err)
		}
		if err := store.MkdirAll("download/bing/2026", 0o755); err != nil {
			t.Fatal(err)
		}
		contents := []byte("\xff\xd8\xffarchived-image")
		if err := os.WriteFile(filepath.Join(store.Root(), "download", "bing", filepath.FromSlash(file)), contents, 0o600); err != nil {
			t.Fatal(err)
		}
		response := serve(t, handler, http.MethodGet, "/image/bing/random")
		if response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), contents) {
			t.Fatalf("response = %d %q", response.Code, response.Body.Bytes())
		}
		assertContentType(t, response, "image/jpeg")
	})
}

func TestUIRestoreRouteRequiresConfiguredAdministrator(t *testing.T) {
	dataDir := t.TempDir()
	db, err := database.Open(context.Background(), dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := storage.New(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	processor, err := imageproc.NewDefaultProcessor(1)
	if err != nil {
		t.Fatal(err)
	}
	defer processor.Close()
	accessRecorder := newTestAccessRecorder(t, db)

	disabled, err := routesWithJobsAndRestore(db, config.Config{DataDir: dataDir}, store, processor, accessRecorder, nil, nil, nil, func() {})
	if err != nil {
		t.Fatal(err)
	}
	if response := serve(t, disabled, http.MethodPost, "/api/v1/admin/backups/restore"); response.Code != http.StatusNotFound {
		t.Fatalf("disabled restore status = %d, want 404", response.Code)
	}

	authenticator, err := auth.New("admin", []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	protected, err := routesWithJobsAndRestore(db, config.Config{DataDir: dataDir}, store, processor, accessRecorder, nil, authenticator, nil, func() {})
	if err != nil {
		t.Fatal(err)
	}
	response := serve(t, protected, http.MethodPost, "/api/v1/admin/backups/restore")
	assertRequiresAdmin(t, response)
	assertRequiresAdmin(t, serve(t, protected, http.MethodGet, "/api/v1/admin/backups/snapshot/mappings"))
	assertRequiresAdmin(t, serve(t, protected, http.MethodGet, "/api/v1/admin/data-directories"))
	assertRequiresAdmin(t, serve(t, protected, http.MethodPost, "/api/v1/admin/integrity/check"))
	if response := serve(t, protected, http.MethodPost, "/api/v1/admin/restore"); response.Code != http.StatusNotFound {
		t.Fatalf("removed upload restore status = %d, want 404", response.Code)
	}
}

func TestRunClearsCredentialsAfterCreatingVerifier(t *testing.T) {
	password := []byte("restart-secret")
	dataDir := t.TempDir()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve listen address: %v", err)
	}
	listenAddress := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("release listen address: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		result <- Run(ctx, config.Config{
			Listen: listenAddress, DataDir: dataDir,
			Admin: config.AdminCredentials{Enabled: true, Username: "admin", Password: password},
		})
	}()

	client := &http.Client{Timeout: 250 * time.Millisecond}
	deadline := time.Now().Add(5 * time.Second)
	for {
		response, requestErr := client.Get("http://" + listenAddress + "/healthz")
		if requestErr == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				break
			}
		}
		select {
		case runErr := <-result:
			t.Fatalf("Run stopped before becoming healthy: %v", runErr)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("server did not become healthy before timeout: %v", requestErr)
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancel()
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("run canceled server: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop after context cancellation")
	}
	if !bytes.Equal(password, make([]byte, len(password))) {
		t.Fatal("Run retained caller-owned plaintext credentials")
	}
}

func TestUploadAuthorizationPolicy(t *testing.T) {
	dataDir := t.TempDir()
	db, err := database.Open(context.Background(), dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store, err := storage.New(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	processor, err := imageproc.NewDefaultProcessor(1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(processor.Close)
	accessRecorder := newTestAccessRecorder(t, db)

	disabled, err := routes(db, config.Config{}, store, processor, accessRecorder, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if response := serve(t, disabled, http.MethodPost, "/images/upload"); response.Code != http.StatusForbidden {
		t.Fatalf("disabled upload status = %d, want 403", response.Code)
	}

	authenticator, err := auth.New("admin", []byte("correct horse battery staple"))
	if err != nil {
		t.Fatal(err)
	}
	protected, err := routes(db, config.Config{}, store, processor, accessRecorder, nil, authenticator)
	if err != nil {
		t.Fatal(err)
	}
	if response := serve(t, protected, http.MethodGet, "/api/v1/setup/status"); response.Code != http.StatusOK || response.Body.String() != "{\"initialized\":true}\n" {
		t.Fatalf("initialized setup status = %d %q", response.Code, response.Body.String())
	}
	if response := serve(t, protected, http.MethodPost, "/api/v1/setup"); response.Code != http.StatusConflict {
		t.Fatalf("repeated setup status = %d, want 409", response.Code)
	}
	challenge := serve(t, protected, http.MethodPost, "/images/upload")
	assertRequiresAdmin(t, challenge)

	public, err := routes(db, config.Config{PublicUploads: true}, store, processor, accessRecorder, nil, authenticator)
	if err != nil {
		t.Fatal(err)
	}
	publicCookie := loginAdmin(t, public)
	logout := httptest.NewRequest(http.MethodPost, "/api/v1/admin/logout", nil)
	logout.AddCookie(publicCookie)
	logoutResponse := httptest.NewRecorder()
	public.ServeHTTP(logoutResponse, logout)
	if logoutResponse.Code != http.StatusNoContent {
		t.Fatalf("public upload logout status = %d", logoutResponse.Code)
	}
	if response := serve(t, public, http.MethodPost, "/images/upload"); response.Code != http.StatusBadRequest {
		t.Fatalf("public upload after logout status = %d, want multipart validation 400", response.Code)
	}
	t.Run("image list authorization", func(t *testing.T) {
		if response := serve(t, disabled, http.MethodGet, "/images"); response.Code != http.StatusNotFound {
			t.Fatalf("disabled image list = %d", response.Code)
		}
		for _, handler := range []http.Handler{protected, public} {
			challenge := serve(t, handler, http.MethodGet, "/images")
			assertRequiresAdmin(t, challenge)
			cookie := loginAdmin(t, handler)
			request := httptest.NewRequest(http.MethodGet, "/images", nil)
			request.AddCookie(cookie)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusOK || response.Body.String() != "[]" {
				t.Fatalf("authenticated image list = %d %q", response.Code, response.Body.String())
			}
		}
	})
}

func TestAdminSessionRouteRequiresConfiguredAdministrator(t *testing.T) {
	dataDir := t.TempDir()
	db, err := database.Open(context.Background(), dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store, err := storage.New(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	processor, err := imageproc.NewDefaultProcessor(1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(processor.Close)
	accessRecorder := newTestAccessRecorder(t, db)

	disabled, err := routes(db, config.Config{}, store, processor, accessRecorder, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"/api/v1/admin/session", "/api/v1/admin/password", "/api/v1/admin/session-policy", "/api/v1/admin/settings", "/api/v1/admin/data-directories"} {
		if response := serve(t, disabled, http.MethodGet, target); response.Code != http.StatusNotFound {
			t.Fatalf("disabled admin route %s status = %d, want 404", target, response.Code)
		}
	}
	for _, target := range []string{"/api/v1/admin/login", "/api/v1/admin/logout", "/api/v1/admin/password"} {
		if response := serve(t, disabled, http.MethodPost, target); response.Code != http.StatusNotFound {
			t.Fatalf("disabled admin route %s status = %d, want 404", target, response.Code)
		}
	}

	authenticator, err := auth.New("admin", []byte("correct horse battery staple"))
	if err != nil {
		t.Fatal(err)
	}
	protected, err := routes(db, config.Config{}, store, processor, accessRecorder, nil, authenticator)
	if err != nil {
		t.Fatal(err)
	}
	response := serve(t, protected, http.MethodGet, "/api/v1/admin/session")
	if response.Code != http.StatusOK || response.Body.String() != "{\"authenticated\":false}\n" {
		t.Fatalf("anonymous session status = %d %q", response.Code, response.Body.String())
	}
	assertRequiresAdmin(t, serve(t, protected, http.MethodGet, "/api/v1/admin/session-policy"))
	assertRequiresAdmin(t, serve(t, protected, http.MethodGet, "/api/v1/admin/settings"))
	cookie := loginAdmin(t, protected)
	passwordStatus := httptest.NewRequest(http.MethodGet, "/api/v1/admin/password", nil)
	passwordStatus.AddCookie(cookie)
	passwordStatusResponse := httptest.NewRecorder()
	protected.ServeHTTP(passwordStatusResponse, passwordStatus)
	if passwordStatusResponse.Code != http.StatusOK || passwordStatusResponse.Body.String() != "{\"managedExternally\":false,\"username\":\"admin\"}\n" {
		t.Fatalf("password status = %d %q", passwordStatusResponse.Code, passwordStatusResponse.Body.String())
	}
	policyStatus := httptest.NewRequest(http.MethodGet, "/api/v1/admin/session-policy", nil)
	policyStatus.AddCookie(cookie)
	policyStatusResponse := httptest.NewRecorder()
	protected.ServeHTTP(policyStatusResponse, policyStatus)
	if policyStatusResponse.Code != http.StatusOK || policyStatusResponse.Body.String() != "{\"rememberDurationDays\":30}\n" {
		t.Fatalf("session policy status = %d %q", policyStatusResponse.Code, policyStatusResponse.Body.String())
	}
	settingsRequest := httptest.NewRequest(http.MethodGet, "/api/v1/admin/settings", nil)
	settingsRequest.AddCookie(cookie)
	settingsResponse := httptest.NewRecorder()
	protected.ServeHTTP(settingsResponse, settingsRequest)
	if settingsResponse.Code != http.StatusOK {
		t.Fatalf("settings status = %d %q", settingsResponse.Code, settingsResponse.Body.String())
	}
	directoriesRequest := httptest.NewRequest(http.MethodGet, "/api/v1/admin/data-directories", nil)
	directoriesRequest.AddCookie(cookie)
	directoriesResponse := httptest.NewRecorder()
	protected.ServeHTTP(directoriesResponse, directoriesRequest)
	if directoriesResponse.Code != http.StatusOK {
		t.Fatalf("data directories status = %d %q", directoriesResponse.Code, directoriesResponse.Body.String())
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/admin/session", nil)
	request.AddCookie(cookie)
	response = httptest.NewRecorder()
	protected.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "{\"authenticated\":true}\n" {
		t.Fatalf("authenticated session = %d %q", response.Code, response.Body.String())
	}
	logout := httptest.NewRequest(http.MethodPost, "/api/v1/admin/logout", nil)
	logout.AddCookie(cookie)
	response = httptest.NewRecorder()
	protected.ServeHTTP(response, logout)
	if response.Code != http.StatusNoContent {
		t.Fatalf("logout = %d %q", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/session", nil)
	request.AddCookie(cookie)
	response = httptest.NewRecorder()
	protected.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "{\"authenticated\":false}\n" {
		t.Fatalf("logged-out session = %d %q", response.Code, response.Body.String())
	}
}

func TestBingSyncRoutesRequireAdministrator(t *testing.T) {
	dataDir := t.TempDir()
	db, err := database.Open(context.Background(), dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store, err := storage.New(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	processor, err := imageproc.NewDefaultProcessor(1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(processor.Close)
	accessRecorder := newTestAccessRecorder(t, db)
	jobs := &testJobTrigger{queued: true}

	disabled, err := routesWithJobs(db, config.Config{}, store, processor, accessRecorder, nil, nil, jobs)
	if err != nil {
		t.Fatal(err)
	}
	if response := serve(t, disabled, http.MethodPost, "/api/v1/admin/bing/sync"); response.Code != http.StatusNotFound {
		t.Fatalf("disabled administrative route = %d, want 404", response.Code)
	}

	authenticator, err := auth.New("admin", []byte("correct horse battery staple"))
	if err != nil {
		t.Fatal(err)
	}
	protected, err := routesWithJobs(db, config.Config{}, store, processor, accessRecorder, nil, authenticator, jobs)
	if err != nil {
		t.Fatal(err)
	}
	assertRequiresAdmin(t, serve(t, protected, http.MethodPost, "/api/v1/admin/bing/sync"))
}

func TestHFSRootListingRequiresConfiguredAdministrator(t *testing.T) {
	dataDir := t.TempDir()
	db, err := database.Open(context.Background(), dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store, err := storage.New(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	processor, err := imageproc.NewDefaultProcessor(1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(processor.Close)
	accessRecorder := newTestAccessRecorder(t, db)

	disabled, err := routes(db, config.Config{}, store, processor, accessRecorder, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if response := serve(t, disabled, http.MethodGet, "/hfs"); response.Code != http.StatusNotFound {
		t.Fatalf("disabled HFS status = %d, want 404", response.Code)
	}

	authenticator, err := auth.New("admin", []byte("correct horse battery staple"))
	if err != nil {
		t.Fatal(err)
	}
	protected, err := routes(db, config.Config{}, store, processor, accessRecorder, nil, authenticator)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"/hfs", "/hfs/"} {
		response := serve(t, protected, http.MethodGet, target)
		assertRequiresAdmin(t, response)
	}
}

func TestRoutesSkipUnavailableHFSRoot(t *testing.T) {
	dataDir := t.TempDir()
	db, err := database.Open(context.Background(), dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store, err := storage.New(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	processor, err := imageproc.NewDefaultProcessor(1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(processor.Close)
	accessRecorder := newTestAccessRecorder(t, db)
	handler, err := routes(db, config.Config{
		DataDir: dataDir,
		HFSRoots: []config.HFSRoot{{
			Name: "external", Path: filepath.Join(t.TempDir(), "missing"), ReadOnly: true,
		}},
	}, store, processor, accessRecorder, nil, nil)
	if err != nil {
		t.Fatalf("create routes with unavailable external HFS root: %v", err)
	}
	if response := serve(t, handler, http.MethodGet, "/healthz"); response.Code != http.StatusOK {
		t.Fatalf("health response = %d: %s", response.Code, response.Body.String())
	}
	if response := serve(t, handler, http.MethodGet, "/hfs/external"); response.Code != http.StatusNotFound {
		t.Fatalf("unavailable root response = %d, want 404", response.Code)
	}
}

func TestHFSPathRoutesApplyPerRootReadPolicy(t *testing.T) {
	dataDir := t.TempDir()
	db, err := database.Open(context.Background(), dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store, err := storage.New(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	processor, err := imageproc.NewDefaultProcessor(1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(processor.Close)
	accessRecorder := newTestAccessRecorder(t, db)
	referenceRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(referenceRoot, "reference.txt"), []byte("reference"), 0o600); err != nil {
		t.Fatal(err)
	}
	configuration := config.Config{HFSRoots: []config.HFSRoot{
		{Name: "public", Path: createAppHFSRoot(t, store, "hfs/public"), Public: true},
		{Name: "private", Path: createAppHFSRoot(t, store, "hfs/private")},
		{Name: "reference", Path: referenceRoot, Public: true, ReadOnly: true},
	}}

	withoutAdmin, err := routes(db, configuration, store, processor, accessRecorder, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if response := serve(t, withoutAdmin, http.MethodGet, "/hfs/public"); response.Code != http.StatusOK || response.Body.String() != "[]\n" {
		t.Fatalf("public root = %d %q", response.Code, response.Body.String())
	}
	if response := serve(t, withoutAdmin, http.MethodGet, "/hfs/private"); response.Code != http.StatusNotFound {
		t.Fatalf("private root without admin = %d, want 404", response.Code)
	}
	if response := serve(t, withoutAdmin, http.MethodPost, "/hfs/public?mkdir"); response.Code != http.StatusNotFound {
		t.Fatalf("public root write without admin = %d, want 404", response.Code)
	}
	if response := serve(t, withoutAdmin, http.MethodGet, "/hfs/reference/reference.txt"); response.Code != http.StatusOK || response.Body.String() != "reference" {
		t.Fatalf("read-only referenced root = %d %q", response.Code, response.Body.String())
	}
	for _, target := range []string{"/hfs/public/%2e%2e/secret", "/hfs/public/a%2Fb", "/hfs/public/a//b", "/hfs%2Fpublic/%2e%2e"} {
		if response := serve(t, withoutAdmin, http.MethodGet, target); response.Code != http.StatusBadRequest {
			t.Fatalf("unsafe target %q = %d, want 400", target, response.Code)
		}
	}

	authenticator, err := auth.New("admin", []byte("correct horse battery staple"))
	if err != nil {
		t.Fatal(err)
	}
	withAdmin, err := routes(db, configuration, store, processor, accessRecorder, nil, authenticator)
	if err != nil {
		t.Fatal(err)
	}
	if response := serve(t, withAdmin, http.MethodGet, "/hfs/public"); response.Code != http.StatusOK {
		t.Fatalf("public root with admin configured = %d", response.Code)
	}
	response := serve(t, withAdmin, http.MethodGet, "/hfs/private")
	assertRequiresAdmin(t, response)
	for _, target := range []string{"/hfs/public/new?mkdir", "/hfs/private/new?mkdir"} {
		response := serve(t, withAdmin, http.MethodPost, target)
		assertRequiresAdmin(t, response)
	}
	if response := serve(t, withAdmin, http.MethodPost, "/hfs/reference/new?mkdir"); response.Code != http.StatusNotFound {
		t.Fatalf("read-only referenced write = %d, want 404", response.Code)
	}
}

func TestHFSDownloadIsPersistedAfterCompletedTransfer(t *testing.T) {
	dataDir := t.TempDir()
	db, err := database.Open(context.Background(), dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store, err := storage.New(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	processor, err := imageproc.NewDefaultProcessor(1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(processor.Close)
	accessRecorder := newTestAccessRecorder(t, db)
	downloadRecorder, err := hfsdownload.NewRecorder(repository.NewDownloadRecordRepository(db), 4, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	configuration := config.Config{HFSRoots: []config.HFSRoot{{Name: "public", Path: createAppHFSRoot(t, store, "hfs/public"), Public: true}}}
	handler, err := routes(db, configuration, store, processor, accessRecorder, downloadRecorder, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store.Root(), "hfs", "public", "archive.bin"), []byte("archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/hfs/public/archive.bin?download", nil)
	request.RemoteAddr = "192.0.2.20:1234"
	request.Header.Set("User-Agent", "Kaven integration test")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "archive" {
		t.Fatalf("response = %d %q", response.Code, response.Body.String())
	}
	if err := downloadRecorder.Close(context.Background()); err != nil {
		t.Fatal(err)
	}

	var file, originalURL, ip string
	var userAgent sql.NullString
	if err := db.QueryRowContext(context.Background(), `
		SELECT file, original_url, ip, user_agent FROM download_records
	`).Scan(&file, &originalURL, &ip, &userAgent); err != nil {
		t.Fatal(err)
	}
	if file != filepath.Join(configuration.HFSRoots[0].Path, "archive.bin") || originalURL != "/hfs/public/archive.bin?download" || ip != "192.0.2.20" ||
		!userAgent.Valid || userAgent.String != "Kaven integration test" {
		t.Fatalf("record = %q %q %q %#v", file, originalURL, ip, userAgent)
	}
}

func TestHealthReportsUnavailableDatabase(t *testing.T) {
	dataDir := t.TempDir()
	db, err := database.Open(context.Background(), dataDir)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	store, err := storage.New(dataDir)
	if err != nil {
		t.Fatalf("create storage: %v", err)
	}
	processor, err := imageproc.NewDefaultProcessor(1)
	if err != nil {
		t.Fatalf("create image processor: %v", err)
	}
	t.Cleanup(processor.Close)
	accessRecorder := newTestAccessRecorder(t, db)
	handler, err := routes(db, config.Config{}, store, processor, accessRecorder, nil, nil)
	if err != nil {
		t.Fatalf("create routes: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}

	response := serve(t, handler, http.MethodGet, "/healthz")
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
	if response.Body.String() != "database unavailable\n" {
		t.Fatalf("body = %q, want database unavailable", response.Body.String())
	}
}

func newTestAccessRecorder(t *testing.T, db *sql.DB) *imageaccess.Recorder {
	t.Helper()
	recorder, err := imageaccess.NewRecorder(repository.NewAccessRecordRepository(db), 4, time.Second)
	if err != nil {
		t.Fatalf("create access recorder: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := recorder.Close(ctx); err != nil {
			t.Errorf("close access recorder: %v", err)
		}
	})
	return recorder
}

func TestRunCreatesLayoutAndStopsOnCancellation(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- Run(ctx, config.Config{Listen: "127.0.0.1:0", DataDir: dataDir})
	}()

	databasePath := filepath.Join(dataDir, "kaven-media.db")
	probe, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatalf("open database probe: %v", err)
	}
	defer probe.Close()

	deadline := time.Now().Add(5 * time.Second)
	var rootsJSON string
	for {
		var migrations int
		err := probe.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&migrations)
		if err == nil && migrations == 1 {
			err = probe.QueryRow("SELECT hfs_roots FROM admin_settings WHERE id = 1").Scan(&rootsJSON)
			if err == nil {
				var roots []config.HFSRoot
				if json.Unmarshal([]byte(rootsJSON), &roots) == nil && len(roots) == 0 {
					if _, statErr := os.Stat(filepath.Join(dataDir, "cache")); statErr == nil {
						break
					}
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("server did not initialize before timeout: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}

	for _, directory := range []string{"cache", "download/bing", "tmp", "upload"} {
		info, err := os.Stat(filepath.Join(dataDir, filepath.FromSlash(directory)))
		if err != nil {
			t.Errorf("stat %s directory: %v", directory, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("%s is not a directory", directory)
		}
	}
	var roots []config.HFSRoot
	if err := json.Unmarshal([]byte(rootsJSON), &roots); err != nil {
		t.Fatal(err)
	}
	if len(roots) != 0 {
		t.Fatalf("stored HFS roots = %+v", roots)
	}

	cancel()
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop after context cancellation")
	}
}

func TestLoadAuthenticatorUsesPersistedSetupCredential(t *testing.T) {
	db, err := database.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	password := []byte("persisted password for testing")
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	credentials := repository.NewAdminCredentialRepository(db)
	if err := credentials.Create(context.Background(), "owner", hash); err != nil {
		t.Fatal(err)
	}
	authenticator, err := loadAuthenticator(
		context.Background(), config.AdminCredentials{}, credentials,
		repository.NewAdminSessionRepository(db, auth.DefaultMaxSessions), auth.DefaultRememberDuration,
	)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/login", bytes.NewBufferString(
		`{"username":"owner","password":"persisted password for testing"}`,
	))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	authenticator.LoginHandler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("persisted administrator login = %d: %s", response.Code, response.Body.String())
	}
}

func TestRememberedAdministratorSessionSurvivesAuthenticatorRestart(t *testing.T) {
	db, err := database.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	password := []byte("remembered password for testing")
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	credentials := repository.NewAdminCredentialRepository(db)
	if err := credentials.Create(context.Background(), "owner", hash); err != nil {
		t.Fatal(err)
	}
	sessions := repository.NewAdminSessionRepository(db, auth.DefaultMaxSessions)
	newAuthenticator := func() *auth.Authenticator {
		authenticator, err := loadAuthenticator(
			context.Background(), config.AdminCredentials{}, credentials, sessions, 7*24*time.Hour,
		)
		if err != nil {
			t.Fatal(err)
		}
		return authenticator
	}

	first := newAuthenticator()
	ephemeralBody := bytes.NewBufferString(`{"username":"owner","password":"remembered password for testing"}`)
	ephemeralLogin := httptest.NewRequest(http.MethodPost, "/api/v1/admin/login", ephemeralBody)
	ephemeralLogin.Header.Set("Content-Type", "application/json")
	ephemeralResponse := httptest.NewRecorder()
	first.LoginHandler().ServeHTTP(ephemeralResponse, ephemeralLogin)
	if ephemeralResponse.Code != http.StatusOK {
		t.Fatalf("ephemeral login = %d: %s", ephemeralResponse.Code, ephemeralResponse.Body.String())
	}
	ephemeralCookie := ephemeralResponse.Result().Cookies()[0]
	if ephemeralCookie.MaxAge != 0 || !ephemeralCookie.Expires.IsZero() {
		t.Fatalf("ephemeral cookie = %#v", ephemeralCookie)
	}
	ephemeralAfterRestart := httptest.NewRequest(http.MethodGet, "/api/v1/admin/session", nil)
	ephemeralAfterRestart.AddCookie(ephemeralCookie)
	ephemeralAfterRestartResponse := httptest.NewRecorder()
	newAuthenticator().SessionHandler().ServeHTTP(ephemeralAfterRestartResponse, ephemeralAfterRestart)
	if ephemeralAfterRestartResponse.Code != http.StatusOK || ephemeralAfterRestartResponse.Body.String() != "{\"authenticated\":false}\n" {
		t.Fatalf("ephemeral session after restart = %d %q", ephemeralAfterRestartResponse.Code, ephemeralAfterRestartResponse.Body.String())
	}

	body := bytes.NewBufferString(`{"username":"owner","password":"remembered password for testing","remember":true}`)
	login := httptest.NewRequest(http.MethodPost, "/api/v1/admin/login", body)
	login.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	first.LoginHandler().ServeHTTP(response, login)
	if response.Code != http.StatusOK {
		t.Fatalf("remembered login = %d: %s", response.Code, response.Body.String())
	}
	cookie := response.Result().Cookies()[0]
	if cookie.MaxAge < 7*24*60*60-2 || cookie.MaxAge > 7*24*60*60 || cookie.Expires.IsZero() {
		t.Fatalf("remembered cookie = %#v", cookie)
	}

	second := newAuthenticator()
	session := httptest.NewRequest(http.MethodGet, "/api/v1/admin/session", nil)
	session.AddCookie(cookie)
	sessionResponse := httptest.NewRecorder()
	second.SessionHandler().ServeHTTP(sessionResponse, session)
	if sessionResponse.Code != http.StatusOK {
		t.Fatalf("remembered session after restart = %d: %s", sessionResponse.Code, sessionResponse.Body.String())
	}

	logout := httptest.NewRequest(http.MethodPost, "/api/v1/admin/logout", nil)
	logout.AddCookie(cookie)
	second.LogoutHandler().ServeHTTP(httptest.NewRecorder(), logout)
	third := newAuthenticator()
	replayed := httptest.NewRequest(http.MethodGet, "/api/v1/admin/session", nil)
	replayed.AddCookie(cookie)
	replayedResponse := httptest.NewRecorder()
	third.SessionHandler().ServeHTTP(replayedResponse, replayed)
	if replayedResponse.Code != http.StatusOK || replayedResponse.Body.String() != "{\"authenticated\":false}\n" {
		t.Fatalf("logged-out persistent session after restart = %d %q", replayedResponse.Code, replayedResponse.Body.String())
	}
}

func serve(t *testing.T, handler http.Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, target, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func loginAdmin(t *testing.T, handler http.Handler) *http.Cookie {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/login", bytes.NewBufferString(
		`{"username":"admin","password":"correct horse battery staple"}`,
	))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("admin login = %d %q", response.Code, response.Body.String())
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != auth.CookieName {
		t.Fatalf("admin login cookies = %#v", cookies)
	}
	return cookies[0]
}

func assertRequiresAdmin(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	if response.Code != http.StatusUnauthorized || response.Header().Get("WWW-Authenticate") != "" {
		t.Fatalf("protected response = %d, challenge %q", response.Code, response.Header().Get("WWW-Authenticate"))
	}
}

func assertContentType(t *testing.T, response *httptest.ResponseRecorder, want string) {
	t.Helper()
	if got := response.Header().Get("Content-Type"); got != want {
		t.Fatalf("Content-Type = %q, want %q", got, want)
	}
}

func assertJSON(t *testing.T, body []byte, want map[string]any) {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode JSON %q: %v", body, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("JSON = %#v, want %#v", got, want)
	}
}

func createAppHFSRoot(t *testing.T, store *storage.Store, relative string) string {
	t.Helper()
	if err := store.MkdirAll(relative, 0o755); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(store.Root(), filepath.FromSlash(relative))
}

func TestWriteJSONTerminatesResponseWithNewline(t *testing.T) {
	response := httptest.NewRecorder()
	writeJSON(response, http.StatusAccepted, map[string]string{"status": "queued"})

	result := response.Result()
	defer result.Body.Close()
	body, err := io.ReadAll(result.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != "{\"status\":\"queued\"}\n" {
		t.Fatalf("body = %q", body)
	}
}

type testJobTrigger struct {
	queued bool
}

func (trigger *testJobTrigger) Trigger(string) (bool, error) {
	return trigger.queued, nil
}
