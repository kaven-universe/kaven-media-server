package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"kaven.xyz/kaven/kaven-media-server/internal/auth"
	"kaven.xyz/kaven/kaven-media-server/internal/backup"
	"kaven.xyz/kaven/kaven-media-server/internal/backupstore"
	"kaven.xyz/kaven/kaven-media-server/internal/bing"
	"kaven.xyz/kaven/kaven-media-server/internal/clientip"
	"kaven.xyz/kaven/kaven-media-server/internal/config"
	"kaven.xyz/kaven/kaven-media-server/internal/database"
	"kaven.xyz/kaven/kaven-media-server/internal/datalock"
	"kaven.xyz/kaven/kaven-media-server/internal/hfs"
	"kaven.xyz/kaven/kaven-media-server/internal/hfsdownload"
	"kaven.xyz/kaven/kaven-media-server/internal/httpapi"
	"kaven.xyz/kaven/kaven-media-server/internal/imageaccess"
	"kaven.xyz/kaven/kaven-media-server/internal/imagecache"
	"kaven.xyz/kaven/kaven-media-server/internal/imagehost"
	"kaven.xyz/kaven/kaven-media-server/internal/imageproc"
	"kaven.xyz/kaven/kaven-media-server/internal/imageupload"
	"kaven.xyz/kaven/kaven-media-server/internal/integrityrepair"
	"kaven.xyz/kaven/kaven-media-server/internal/referer"
	"kaven.xyz/kaven/kaven-media-server/internal/repository"
	"kaven.xyz/kaven/kaven-media-server/internal/scheduler"
	"kaven.xyz/kaven/kaven-media-server/internal/storage"
	"kaven.xyz/kaven/kaven-media-server/internal/versioncheck"
	"kaven.xyz/kaven/kaven-media-server/internal/webui"
)

const bingSyncJobName = "Bing archive synchronization"

var ErrRestartReady = errors.New("configuration or validated restore is ready to apply")

func Run(ctx context.Context, cfg config.Config) (resultErr error) {
	lock, err := datalock.Acquire(cfg.DataDir)
	if err != nil {
		return err
	}
	defer lock.Close()
	store, err := storage.New(cfg.DataDir)
	if err != nil {
		return err
	}
	db, err := database.Open(ctx, cfg.DataDir)
	if err != nil {
		return err
	}
	defer db.Close()

	processor, err := imageproc.NewDefaultProcessor(config.DefaultImageWorkers)
	if err != nil {
		return err
	}
	defer processor.Close()
	adminCredentials := repository.NewAdminCredentialRepository(db)
	adminSettings := repository.NewAdminSettingsRepository(db)
	runtimeSettings, err := adminSettings.Get(ctx)
	if err != nil {
		return err
	}
	if err := config.ValidateRuntimeSettings(runtimeSettings); err != nil {
		return fmt.Errorf("validate stored application settings: %w", err)
	}
	if cfg.ConfigureLogging != nil {
		if err := cfg.ConfigureLogging(runtimeSettings.LocalLogEnabled, runtimeSettings.LogMaxFileSize, runtimeSettings.LogMaxBackups); err != nil {
			return fmt.Errorf("configure application logging: %w", err)
		}
	}
	cfg.ApplyRuntimeSettings(runtimeSettings)
	if err := store.ConfigureMediaDirectories(cfg.UploadDirectory, cfg.DownloadDirectory); err != nil {
		return err
	}
	for _, directory := range []string{store.UploadDirectory(), "cache", store.BingArchiveDirectory()} {
		if err := store.MkdirAll(directory, 0o755); err != nil {
			return fmt.Errorf("create %s directory: %w", directory, err)
		}
	}
	authenticator, err := loadAuthenticator(
		ctx, cfg.Admin, adminCredentials,
		repository.NewAdminSessionRepository(db, auth.DefaultMaxSessions), time.Duration(cfg.RememberDurationDays)*24*time.Hour,
	)
	if err != nil {
		return err
	}
	accessRecorder, err := imageaccess.NewRecorder(
		repository.NewAccessRecordRepository(db), config.DefaultAccessQueueSize, config.DefaultAccessWriteTimeout,
	)
	if err != nil {
		return err
	}
	defer func() {
		drainCtx, cancel := context.WithTimeout(context.Background(), config.DefaultAccessDrainTimeout)
		defer cancel()
		if err := accessRecorder.Close(drainCtx); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("drain image access records: %w", err))
		}
	}()
	downloadRecorder, err := hfsdownload.NewRecorder(
		repository.NewDownloadRecordRepository(db), config.DefaultDownloadQueueSize, config.DefaultDownloadWriteTimeout,
	)
	if err != nil {
		return err
	}
	defer func() {
		drainCtx, cancel := context.WithTimeout(context.Background(), config.DefaultDownloadDrainTimeout)
		defer cancel()
		if err := downloadRecorder.Close(drainCtx); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("drain HFS download records: %w", err))
		}
	}()
	metadataClient, err := bing.NewClient(bing.Options{})
	if err != nil {
		return err
	}
	bingArchive, err := bing.NewService(
		metadataClient, repository.NewBingImageRepository(db), store, bing.ServiceOptions{},
	)
	if err != nil {
		return err
	}
	integrityRepairer := integrityrepair.NewService(store, repository.NewImageRepository(db), bingArchive, cfg.MaxImageFileSize)
	scheduledJobs, err := scheduler.New(scheduler.Job{
		Name:     bingSyncJobName,
		Interval: time.Duration(cfg.BingSyncIntervalHours) * time.Hour,
		Disabled: !cfg.BingSyncEnabled,
		Run: func(ctx context.Context) error {
			report, err := bingArchive.Sync(ctx)
			if ctx.Err() == nil {
				slog.Info("Bing archive synchronization finished",
					"fetched", report.Fetched, "synchronized", report.Synchronized,
					"downloaded", report.Downloaded, "reused", report.Reused,
					"failed", report.Failed, "duplicates", report.Duplicates, "renamed", report.Migrated,
				)
			}
			return err
		},
	})
	if err != nil {
		return err
	}
	scheduleCtx, stopSchedule := context.WithCancel(ctx)
	scheduleResult := make(chan error, 1)
	go func() { scheduleResult <- scheduledJobs.Run(scheduleCtx) }()
	defer func() {
		stopSchedule()
		if err := <-scheduleResult; err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("stop scheduled jobs: %w", err))
		}
	}()

	restartReady := make(chan struct{}, 1)
	handler, err := routesWithRuntimeServices(db, cfg, store, processor, accessRecorder, downloadRecorder, authenticator, scheduledJobs, func() {
		select {
		case restartReady <- struct{}{}:
		default:
		}
	}, integrityRepairer)
	if err != nil {
		return err
	}
	server := &http.Server{
		Addr:              cfg.Listen,
		Handler:           requestLogger(handler),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	result := make(chan error, 1)
	go func() {
		slog.Info("Kaven Media Server started", "listen", cfg.Listen, "data", cfg.DataDir)
		result <- server.ListenAndServe()
	}()

	select {
	case err := <-result:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	case <-restartReady:
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("stop server for internal restart: %w", err)
		}
		return ErrRestartReady
	}
}

func loadAuthenticator(ctx context.Context, configured config.AdminCredentials, stored *repository.AdminCredentialRepository, sessions auth.PersistentSessionStore, rememberDuration time.Duration) (*auth.Authenticator, error) {
	options := auth.Options{SecureCookie: configured.SecureCookie, SessionStore: sessions, RememberDuration: rememberDuration}
	if configured.Enabled {
		authenticator, err := auth.NewWithOptions(configured.Username, configured.Password, options)
		clear(configured.Password)
		return authenticator, err
	}
	credential, exists, err := stored.Get(ctx)
	if err != nil || !exists {
		return nil, err
	}
	authenticator, err := auth.NewFromPasswordHash(credential.Username, credential.PasswordHash, options)
	clear(credential.PasswordHash)
	return authenticator, err
}

func routes(db *sql.DB, cfg config.Config, store *storage.Store, processor *imageproc.Processor, accessRecorder *imageaccess.Recorder, downloadRecorder *hfsdownload.Recorder, authenticator *auth.Authenticator) (http.Handler, error) {
	return routesWithJobs(db, cfg, store, processor, accessRecorder, downloadRecorder, authenticator, nil)
}

func routesWithJobs(db *sql.DB, cfg config.Config, store *storage.Store, processor *imageproc.Processor, accessRecorder *imageaccess.Recorder, downloadRecorder *hfsdownload.Recorder, authenticator *auth.Authenticator, jobs httpapi.JobTrigger) (http.Handler, error) {
	return routesWithJobsAndRestore(db, cfg, store, processor, accessRecorder, downloadRecorder, authenticator, jobs, nil)
}

func routesWithJobsAndRestore(db *sql.DB, cfg config.Config, store *storage.Store, processor *imageproc.Processor, accessRecorder *imageaccess.Recorder, downloadRecorder *hfsdownload.Recorder, authenticator *auth.Authenticator, jobs httpapi.JobTrigger, restartReady func()) (http.Handler, error) {
	return routesWithRuntimeServices(db, cfg, store, processor, accessRecorder, downloadRecorder, authenticator, jobs, restartReady, nil)
}

func routesWithRuntimeServices(db *sql.DB, cfg config.Config, store *storage.Store, processor *imageproc.Processor, accessRecorder *imageaccess.Recorder, downloadRecorder *hfsdownload.Recorder, authenticator *auth.Authenticator, jobs httpapi.JobTrigger, restartReady func(), repairer httpapi.IntegrityRepairer) (http.Handler, error) {
	defaults := config.DefaultRuntimeSettings(store.Root())
	if cfg.UploadDirectory == "" {
		cfg.UploadDirectory = defaults.UploadDirectory
	}
	if cfg.DownloadDirectory == "" {
		cfg.DownloadDirectory = defaults.DownloadDirectory
	}
	if err := store.ConfigureMediaDirectories(cfg.UploadDirectory, cfg.DownloadDirectory); err != nil {
		return nil, err
	}
	if cfg.MaxFileCount == 0 {
		cfg.MaxFileCount = defaults.MaxFileCount
	}
	if cfg.MaxImageFileSize == 0 {
		cfg.MaxImageFileSize = defaults.MaxImageFileSize
	}
	if cfg.MaxHFSFileSize == 0 {
		cfg.MaxHFSFileSize = defaults.MaxHFSFileSize
	}
	if cfg.RememberDurationDays == 0 {
		cfg.RememberDurationDays = defaults.RememberDurationDays
	}
	if cfg.BingSyncIntervalHours == 0 {
		cfg.BingSyncIntervalHours = defaults.BingSyncIntervalHours
	}
	refererPolicy, err := referer.New(cfg.AllowedDomainNames)
	if err != nil {
		return nil, fmt.Errorf("configure image referer policy: %w", err)
	}
	clientIPs, err := clientip.New(cfg.TrustedProxyCIDRs)
	if err != nil {
		return nil, fmt.Errorf("configure trusted proxies: %w", err)
	}
	mux := http.NewServeMux()
	adminCredentials := repository.NewAdminCredentialRepository(db)
	adminSettings := repository.NewAdminSettingsRepository(db)
	_, persistedAdminExists, err := adminCredentials.Get(context.Background())
	if err != nil {
		return nil, err
	}
	initialized := authenticator != nil || persistedAdminExists
	mux.Handle("GET /api/v1/setup/status", httpapi.NewSetupStatusHandler(initialized, defaults))
	validateSettings := func(settings config.RuntimeSettings) error {
		_, err := hfs.NewRegistry(store, settings.HFSRoots)
		return err
	}
	mux.Handle("POST /api/v1/setup", httpapi.NewSetupHandler(initialized, adminCredentials, defaults, validateSettings, restartReady))
	configuredRoots := cfg.HFSRoots
	if configuredRoots == nil {
		if err := store.MkdirAll(store.UploadDirectory(), 0o755); err != nil {
			return nil, fmt.Errorf("create default HFS root: %w", err)
		}
		configuredRoots = config.DefaultHFSRootsForUploadDirectory(store.UploadDirectory())
	}
	hfsRoots, err := hfs.NewRegistryWithOptions(store, configuredRoots, hfs.RegistryOptions{
		SkipUnavailableRoots: true,
		OnUnavailableRoot: func(root config.HFSRoot, err error) {
			slog.Warn("skip unavailable HFS root", "name", root.Name, "path", root.Path, "error", err)
		},
	})
	if err != nil {
		return nil, err
	}
	privateBackups, err := backupstore.New(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := db.PingContext(r.Context()); err != nil {
			http.Error(w, "database unavailable", http.StatusServiceUnavailable)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /server/info", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, httpapi.ServerInfoResponse{
			Upload: httpapi.UploadLimits{
				MaxFileCount:     cfg.MaxFileCount,
				MaxImageFileSize: cfg.MaxImageFileSize,
				MaxHFSFileSize:   cfg.MaxHFSFileSize,
			},
		})
	})
	imageRepository := repository.NewImageRepository(db)
	imageLookup := imagehost.NewLookupService(imageRepository)
	cacheService, err := imagecache.NewService(
		store, repository.NewImageCacheRepository(db), processor, cfg.MaxImageFileSize,
	)
	if err != nil {
		return nil, err
	}
	mux.Handle("GET /image/bing/random", refererPolicy.Protect(httpapi.NewBingImageHandler(repository.NewBingImageRepository(db), store)))
	mux.Handle("GET /image/{id}", refererPolicy.Protect(httpapi.NewImageHandler(imageLookup, store, cacheService, accessRecorder)))
	uploadService := imageupload.NewService(store, imageRepository)
	var uploadHandler http.Handler = httpapi.NewUploadHandler(
		store, uploadService, cfg.PublicUploads || authenticator != nil,
		cfg.MaxFileCount, cfg.MaxImageFileSize,
	)
	if authenticator != nil && !cfg.PublicUploads {
		uploadHandler = authenticator.Protect(uploadHandler)
	}
	mux.Handle("POST /images/upload", uploadHandler)
	if authenticator != nil {
		mux.Handle("POST /api/v1/admin/login", authenticator.LoginHandler())
		mux.Handle("GET /api/v1/admin/session", authenticator.SessionHandler())
		mux.Handle("POST /api/v1/admin/logout", authenticator.LogoutHandler())
		mux.Handle("GET /api/v1/admin/password", authenticator.Protect(httpapi.NewAdminPasswordStatusHandler(
			authenticator.Username(), cfg.Admin.Enabled,
		)))
		mux.Handle("POST /api/v1/admin/password", authenticator.Protect(httpapi.NewAdminPasswordHandler(
			authenticator, adminCredentials, cfg.Admin.Enabled, restartReady,
		)))
		mux.Handle("GET /api/v1/admin/session-policy", authenticator.Protect(httpapi.NewAdminSessionPolicyStatusHandler(adminSettings)))
		mux.Handle("PUT /api/v1/admin/session-policy", authenticator.Protect(httpapi.NewAdminSessionPolicyHandler(authenticator, adminSettings)))
		mux.Handle("GET /api/v1/admin/version", authenticator.Protect(httpapi.NewVersionHandler(versioncheck.New(nil))))
		settingsHandler := authenticator.Protect(httpapi.NewApplicationSettingsHandler(adminSettings, validateSettings, restartReady))
		mux.Handle("GET /api/v1/admin/settings", settingsHandler)
		mux.Handle("PUT /api/v1/admin/settings", settingsHandler)
		mux.Handle("GET /api/v1/admin/data-directories", authenticator.Protect(httpapi.NewDataDirectoriesHandler(store.Root())))
		integrityHandler := httpapi.NewIntegrityHandler(db, store.Root())
		if restartReady != nil && repairer != nil {
			integrityHandler = httpapi.NewRepairableIntegrityHandler(db, store.Root(), privateBackups, backup.DefaultMaxBytes, repairer)
			mux.Handle("POST /api/v1/admin/integrity/repair", authenticator.Protect(http.HandlerFunc(integrityHandler.Repair)))
		}
		mux.Handle("POST /api/v1/admin/integrity/check", authenticator.Protect(integrityHandler))
		mux.Handle("GET /api/v1/admin/activity", authenticator.Protect(httpapi.NewActivityHandler(
			repository.NewAccessRecordRepository(db), repository.NewDownloadRecordRepository(db),
		)))
		mux.Handle("GET /images", authenticator.Protect(httpapi.NewImagesHandler(imageRepository)))
		if restartReady != nil {
			validateRestoreRoots := func(roots []config.HFSRoot) error {
				_, err := hfs.NewRegistry(store, roots)
				return err
			}
			backupHandler := httpapi.NewBackupStoreHandler(privateBackups, db, cfg.DataDir, backup.DefaultMaxBytes, restartReady, validateRestoreRoots)
			mux.Handle("GET /api/v1/admin/backups", authenticator.Protect(http.HandlerFunc(backupHandler.List)))
			mux.Handle("GET /api/v1/admin/backups/{name}/mappings", authenticator.Protect(http.HandlerFunc(backupHandler.Mappings)))
			mux.Handle("POST /api/v1/admin/backups", authenticator.Protect(http.HandlerFunc(backupHandler.Create)))
			mux.Handle("POST /api/v1/admin/backups/restore", authenticator.Protect(http.HandlerFunc(backupHandler.Restore)))
		}
		if jobs != nil {
			bingSyncHandler, err := httpapi.NewBingSyncHandler(jobs, bingSyncJobName)
			if err != nil {
				return nil, err
			}
			mux.Handle("POST /api/v1/admin/bing/sync", authenticator.Protect(bingSyncHandler))
		}
		hfsRootHandler := authenticator.Protect(httpapi.NewHFSRootHandler(hfsRoots))
		mux.Handle("GET /hfs", hfsRootHandler)
		mux.Handle("GET /hfs/{$}", hfsRootHandler)
	}
	for _, root := range hfsRoots.Roots() {
		var pathHandler http.Handler = httpapi.NewHFSPathHandler(hfsRoots, root, downloadRecorder)
		if !root.Public {
			if authenticator == nil {
				continue
			}
			pathHandler = authenticator.Protect(pathHandler)
		}
		prefix := "/hfs/" + root.Name
		mux.Handle("GET "+prefix, pathHandler)
		mux.Handle("GET "+prefix+"/", pathHandler)
		if authenticator != nil && root.AllowsWrite(true) {
			writeHandler := authenticator.Protect(httpapi.NewHFSWriteHandler(
				hfsRoots, root, store, cfg.MaxFileCount, cfg.MaxHFSFileSize,
			))
			mux.Handle("POST "+prefix, writeHandler)
			mux.Handle("POST "+prefix+"/", writeHandler)
		}
	}

	ui, err := webui.NewHandler()
	if err != nil {
		return nil, err
	}
	mux.Handle("/", ui)
	return clientIPs.Middleware(httpapi.GuardHFSPaths(mux)), nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		slog.Error("write response", "error", err)
	}
}

func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		next.ServeHTTP(w, r)
		slog.Info("request", "method", r.Method, "path", r.URL.Path, "duration", time.Since(started))
	})
}
