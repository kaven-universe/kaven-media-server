package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
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
			SHA1: "abcdef0123456789abcdef0123456789abcdef01", Folder: "images", Name: "source.png",
			OriginalName: "source.png", MIMEType: "image/png", Size: 8, UploadDate: timestamp,
			UploadIP: "127.0.0.1", CreatedAt: timestamp, UpdatedAt: timestamp,
		}
		if err := repository.NewImageRepository(db).Create(context.Background(), image); err != nil {
			t.Fatal(err)
		}
		if err := store.MkdirAll("images", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(store.Root(), "images", image.Name), []byte("\x89PNG\r\n\x1a\n"), 0o600); err != nil {
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
		file := "bing/2026/wallpaper.jpg"
		url := "/th?id=OHR.Wallpaper_UHD.jpg"
		image := repository.BingImage{
			ID: "bing-image", URL: url, File: &file, CreatedAt: timestamp, UpdatedAt: timestamp,
		}
		if err := repository.NewBingImageRepository(db).Upsert(context.Background(), image); err != nil {
			t.Fatal(err)
		}
		if err := store.MkdirAll("bing/2026", 0o755); err != nil {
			t.Fatal(err)
		}
		contents := []byte("\xff\xd8\xffarchived-image")
		if err := os.WriteFile(filepath.Join(store.Root(), filepath.FromSlash(file)), contents, 0o600); err != nil {
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
	if response := serve(t, disabled, http.MethodPost, "/api/v1/admin/restore"); response.Code != http.StatusNotFound {
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
	response := serve(t, protected, http.MethodPost, "/api/v1/admin/restore")
	if response.Code != http.StatusUnauthorized || response.Header().Get("WWW-Authenticate") == "" {
		t.Fatalf("protected restore response = %d, challenge %q", response.Code, response.Header().Get("WWW-Authenticate"))
	}
}

func TestRunPreservesCredentialsForInternalRestart(t *testing.T) {
	password := []byte("restart-secret")
	dataDir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- Run(ctx, config.Config{
			Listen: "127.0.0.1:0", DataDir: dataDir,
			Admin: config.AdminCredentials{Enabled: true, Username: "admin", Password: password},
		})
	}()
	time.Sleep(100 * time.Millisecond)
	cancel()
	err := <-result
	if err != nil {
		t.Fatalf("run canceled server: %v", err)
	}
	if string(password) != "restart-secret" {
		t.Fatal("Run cleared caller-owned credentials needed by internal restart")
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
	challenge := serve(t, protected, http.MethodPost, "/images/upload")
	if challenge.Code != http.StatusUnauthorized || !strings.Contains(challenge.Header().Get("WWW-Authenticate"), "algorithm=SHA-256") {
		t.Fatalf("protected upload = %d, challenge %q", challenge.Code, challenge.Header().Get("WWW-Authenticate"))
	}

	public, err := routes(db, config.Config{PublicUploads: true}, store, processor, accessRecorder, nil, authenticator)
	if err != nil {
		t.Fatal(err)
	}
	if response := serve(t, public, http.MethodPost, "/images/upload"); response.Code != http.StatusBadRequest {
		t.Fatalf("public upload status = %d, want multipart validation 400", response.Code)
	}
	t.Run("image list authorization", func(t *testing.T) {
		if response := serve(t, disabled, http.MethodGet, "/images"); response.Code != http.StatusNotFound {
			t.Fatalf("disabled image list = %d", response.Code)
		}
		for _, handler := range []http.Handler{protected, public} {
			challenge := serve(t, handler, http.MethodGet, "/images")
			if challenge.Code != http.StatusUnauthorized {
				t.Fatalf("anonymous image list = %d", challenge.Code)
			}
			fields := map[string]string{}
			for _, match := range regexp.MustCompile(`(\w+)="([^"]*)"`).FindAllStringSubmatch(challenge.Header().Get("WWW-Authenticate"), -1) {
				fields[match[1]] = match[2]
			}
			hash := func(value string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(value))) }
			digest := hash(hash("admin:"+auth.Realm+":correct horse battery staple") + ":" + fields["nonce"] + ":00000001:images-test:auth:" + hash("GET:/images"))
			request := httptest.NewRequest(http.MethodGet, "/images", nil)
			request.Header.Set("Authorization", fmt.Sprintf(`Digest username="admin", realm="%s", nonce="%s", uri="/images", response="%s", algorithm=SHA-256, qop=auth, nc=00000001, cnonce="images-test", opaque="%s"`, auth.Realm, fields["nonce"], digest, fields["opaque"]))
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusOK || response.Body.String() != "[]" {
				t.Fatalf("authenticated image list = %d %q", response.Code, response.Body.String())
			}
			response = httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("replayed image list = %d", response.Code)
			}
		}
	})
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
	if response := serve(t, disabled, http.MethodGet, "/api/sync-bing-images-from-db"); response.Code != http.StatusNotFound {
		t.Fatalf("disabled route = %d, want 404", response.Code)
	}

	authenticator, err := auth.New("admin", []byte("correct horse battery staple"))
	if err != nil {
		t.Fatal(err)
	}
	protected, err := routesWithJobs(db, config.Config{}, store, processor, accessRecorder, nil, authenticator, jobs)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"/api/sync-bing-images-from-db", "/api/sync-bing-images-from-dir"} {
		response := serve(t, protected, http.MethodGet, target)
		if response.Code != http.StatusUnauthorized || !strings.Contains(response.Header().Get("WWW-Authenticate"), "algorithm=SHA-256") {
			t.Fatalf("protected route %q = %d, challenge %q", target, response.Code, response.Header().Get("WWW-Authenticate"))
		}
	}
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
		if response.Code != http.StatusUnauthorized || !strings.Contains(response.Header().Get("WWW-Authenticate"), "algorithm=SHA-256") {
			t.Fatalf("protected HFS %s = %d, challenge %q", target, response.Code, response.Header().Get("WWW-Authenticate"))
		}
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
	if err := os.WriteFile(filepath.Join(referenceRoot, "legacy.txt"), []byte("legacy"), 0o600); err != nil {
		t.Fatal(err)
	}
	configuration := config.Config{HFSRoots: []config.HFSRoot{
		{Name: "public", Path: "hfs/public", Public: true},
		{Name: "private", Path: "hfs/private"},
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
	if response := serve(t, withoutAdmin, http.MethodGet, "/hfs/reference/legacy.txt"); response.Code != http.StatusOK || response.Body.String() != "legacy" {
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
	if response.Code != http.StatusUnauthorized || !strings.Contains(response.Header().Get("WWW-Authenticate"), "algorithm=SHA-256") {
		t.Fatalf("private root = %d, challenge %q", response.Code, response.Header().Get("WWW-Authenticate"))
	}
	for _, target := range []string{"/hfs/public/new?mkdir", "/hfs/private/new?mkdir"} {
		response := serve(t, withAdmin, http.MethodPost, target)
		if response.Code != http.StatusUnauthorized || !strings.Contains(response.Header().Get("WWW-Authenticate"), "algorithm=SHA-256") {
			t.Fatalf("protected write %q = %d, challenge %q", target, response.Code, response.Header().Get("WWW-Authenticate"))
		}
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
	configuration := config.Config{HFSRoots: []config.HFSRoot{{Name: "public", Path: "hfs/public", Public: true}}}
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
	if file != "hfs/public/archive.bin" || originalURL != "/hfs/public/archive.bin?download" || ip != "192.0.2.20" ||
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
	for {
		var migrations int
		err := probe.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&migrations)
		if err == nil && migrations == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("server did not initialize before timeout: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}

	for _, directory := range []string{"images", "cache", "bing", "hfs", "tmp"} {
		info, err := os.Stat(filepath.Join(dataDir, directory))
		if err != nil {
			t.Errorf("stat %s directory: %v", directory, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("%s is not a directory", directory)
		}
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

func serve(t *testing.T, handler http.Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, target, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
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
