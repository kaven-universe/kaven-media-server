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
	"kaven.xyz/kaven/kaven-media-server/internal/bing"
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
	"kaven.xyz/kaven/kaven-media-server/internal/referer"
	"kaven.xyz/kaven/kaven-media-server/internal/repository"
	"kaven.xyz/kaven/kaven-media-server/internal/scheduler"
	"kaven.xyz/kaven/kaven-media-server/internal/storage"
	"kaven.xyz/kaven/kaven-media-server/internal/webui"
)

const bingSyncJobName = "Bing archive synchronization"

var ErrRestoreReady = errors.New("validated restore is ready to apply")

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
	for _, directory := range []string{"images", "cache", "bing", "hfs"} {
		if err := store.MkdirAll(directory, 0o755); err != nil {
			return fmt.Errorf("create %s directory: %w", directory, err)
		}
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
	var authenticator *auth.Authenticator
	if cfg.Admin.Enabled {
		password := append([]byte(nil), cfg.Admin.Password...)
		authenticator, err = auth.New(cfg.Admin.Username, password)
		for index := range password {
			password[index] = 0
		}
		cfg.Admin.Password = nil
		if err != nil {
			return err
		}
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
	scheduledJobs, err := scheduler.New(scheduler.Job{
		Name: bingSyncJobName, Interval: config.DefaultBingSyncInterval,
		Run: func(ctx context.Context) error {
			report, err := bingArchive.Sync(ctx)
			if ctx.Err() == nil {
				slog.Info("Bing archive synchronization finished",
					"fetched", report.Fetched, "synchronized", report.Synchronized,
					"downloaded", report.Downloaded, "reused", report.Reused,
					"failed", report.Failed, "duplicates", report.Duplicates,
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

	restoreReady := make(chan struct{}, 1)
	handler, err := routesWithJobsAndRestore(db, cfg, store, processor, accessRecorder, downloadRecorder, authenticator, scheduledJobs, func() {
		select {
		case restoreReady <- struct{}{}:
		default:
		}
	})
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
	case <-restoreReady:
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("stop server for restore: %w", err)
		}
		return ErrRestoreReady
	}
}

func routes(db *sql.DB, cfg config.Config, store *storage.Store, processor *imageproc.Processor, accessRecorder *imageaccess.Recorder, downloadRecorder *hfsdownload.Recorder, authenticator *auth.Authenticator) (http.Handler, error) {
	return routesWithJobs(db, cfg, store, processor, accessRecorder, downloadRecorder, authenticator, nil)
}

func routesWithJobs(db *sql.DB, cfg config.Config, store *storage.Store, processor *imageproc.Processor, accessRecorder *imageaccess.Recorder, downloadRecorder *hfsdownload.Recorder, authenticator *auth.Authenticator, jobs httpapi.JobTrigger) (http.Handler, error) {
	return routesWithJobsAndRestore(db, cfg, store, processor, accessRecorder, downloadRecorder, authenticator, jobs, nil)
}

func routesWithJobsAndRestore(db *sql.DB, cfg config.Config, store *storage.Store, processor *imageproc.Processor, accessRecorder *imageaccess.Recorder, downloadRecorder *hfsdownload.Recorder, authenticator *auth.Authenticator, jobs httpapi.JobTrigger, restoreReady func()) (http.Handler, error) {
	refererPolicy, err := referer.New(cfg.AllowedDomainNames)
	if err != nil {
		return nil, fmt.Errorf("configure image referer policy: %w", err)
	}
	mux := http.NewServeMux()
	configuredRoots := cfg.HFSRoots
	if configuredRoots == nil {
		configuredRoots = config.DefaultHFSRoots()
	}
	hfsRoots, err := hfs.NewRegistry(store, configuredRoots)
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
				MaxFileCount:     config.DefaultMaxFileCount,
				MaxImageFileSize: config.DefaultMaxImageFileSize,
				MaxHFSFileSize:   config.DefaultMaxHFSFileSize,
			},
		})
	})
	imageRepository := repository.NewImageRepository(db)
	imageLookup := imagehost.NewLookupService(imageRepository)
	cacheService, err := imagecache.NewService(
		store, repository.NewImageCacheRepository(db), processor, config.DefaultMaxImageFileSize,
	)
	if err != nil {
		return nil, err
	}
	mux.Handle("GET /image/bing/random", refererPolicy.Protect(httpapi.NewBingImageHandler(repository.NewBingImageRepository(db), store)))
	mux.Handle("GET /image/{id}", refererPolicy.Protect(httpapi.NewImageHandler(imageLookup, store, cacheService, accessRecorder)))
	uploadService := imageupload.NewService(store, imageRepository)
	var uploadHandler http.Handler = httpapi.NewUploadHandler(
		store, uploadService, cfg.PublicUploads || authenticator != nil,
		config.DefaultMaxFileCount, config.DefaultMaxImageFileSize,
	)
	if !cfg.PublicUploads && authenticator != nil {
		uploadHandler = authenticator.Protect(uploadHandler)
	}
	mux.Handle("POST /images/upload", uploadHandler)
	if authenticator != nil {
		mux.Handle("GET /images", authenticator.Protect(httpapi.NewImagesHandler(imageRepository)))
		if restoreReady != nil {
			mux.Handle("POST /api/v1/admin/restore", authenticator.Protect(httpapi.NewRestoreHandler(
				cfg.DataDir, backup.DefaultMaxBytes, restoreReady,
			)))
		}
		if jobs != nil {
			bingSyncHandler, err := httpapi.NewBingSyncHandler(jobs, bingSyncJobName)
			if err != nil {
				return nil, err
			}
			protectedBingSync := authenticator.Protect(bingSyncHandler)
			mux.Handle("GET /api/sync-bing-images-from-db", protectedBingSync)
			mux.Handle("GET /api/sync-bing-images-from-dir", protectedBingSync)
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
				hfsRoots, root, store, config.DefaultMaxFileCount, config.DefaultMaxHFSFileSize,
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
	return httpapi.GuardHFSPaths(mux), nil
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
