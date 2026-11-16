// Command server is the Threads Toolkit web backend:
// public JSON API for the web app, internal API for the scraper worker,
// job queue in Postgres and xlsx exports in S3.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	// Embed the IANA timezone database: the distroless image has none, and daily schedules need it
	_ "time/tzdata"

	"github.com/yeldosmanap/threads-toolkit/backend/internal/analysis"
	"github.com/yeldosmanap/threads-toolkit/backend/internal/api"
	"github.com/yeldosmanap/threads-toolkit/backend/internal/config"
	"github.com/yeldosmanap/threads-toolkit/backend/internal/db"
	"github.com/yeldosmanap/threads-toolkit/backend/internal/pipeline"
	"github.com/yeldosmanap/threads-toolkit/backend/internal/storage"
	"github.com/yeldosmanap/threads-toolkit/backend/internal/store"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	st := store.New(pool)

	files, err := storage.NewS3(ctx, storage.Options{
		Endpoint: cfg.S3Endpoint, Region: cfg.S3Region,
		Bucket: cfg.S3Bucket, AccessKey: cfg.S3AccessKey, SecretKey: cfg.S3SecretKey, PathStyle: cfg.S3PathStyle,
	})
	if err != nil {
		return err
	}
	if err := files.EnsureBucket(ctx); err != nil {
		return err
	}

	go maintenance(ctx, st, cfg, log)
	go scheduler(ctx, st, cfg, log)

	analysisEnabled := cfg.AnthropicAPIKey != ""
	if analysisEnabled {
		clf, err := analysis.NewClassifier(analysis.Options{APIKey: cfg.AnthropicAPIKey, Model: cfg.AnalysisModel, Effort: cfg.AnalysisEffort})
		if err != nil {
			return err
		}
		analyzer := &pipeline.Analyzer{Store: st, Files: files, Classifier: clf, Log: log, Concurrency: cfg.AnalysisConcurrency,
			CommentsPerRun: cfg.CommentsPerRun, CommentsPerPost: cfg.CommentsPerPost}
		go analyzer.Run(ctx)
		log.Info("post analysis enabled", "model", cfg.AnalysisModel, "effort", cfg.AnalysisEffort)
	} else {
		log.Warn("ANTHROPIC_API_KEY is not set: project runs will not be analyzed")
	}

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           api.New(cfg, st, files, log, analysisEnabled).Routes(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       2 * time.Minute, // worker uploads results in one request
		WriteTimeout:      2 * time.Minute,
		IdleTimeout:       2 * time.Minute,
	}
	errCh := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", cfg.ListenAddr)
		if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

// maintenance requeues jobs from crashed workers and removes expired sessions.
func maintenance(ctx context.Context, st *store.Store, cfg config.Config, log *slog.Logger) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		requeued, failed, err := st.RecoverStaleJobs(ctx, cfg.JobStaleAfter, cfg.JobMaxAttempts)
		if err != nil && ctx.Err() == nil {
			log.Error("recover stale jobs", "err", err)
		} else if requeued+failed > 0 {
			log.Warn("recovered stale jobs", "requeued", requeued, "failed", failed)
		}
		if _, err := st.DeleteExpiredSessions(ctx); err != nil && ctx.Err() == nil {
			log.Error("delete expired sessions", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// scheduler turns due recurring searches into queued jobs.
func scheduler(ctx context.Context, st *store.Store, cfg config.Config, log *slog.Logger) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	limits := store.ScheduleLimits{ActiveJobs: cfg.QuotaActiveJobs, DailyJobs: cfg.QuotaDailyJobs}
	for {
		res, err := st.RunDueSchedules(ctx, time.Now(), limits)
		if err != nil && ctx.Err() == nil {
			log.Error("run due schedules", "err", err)
		} else if res.Started+res.Skipped > 0 {
			log.Info("schedules", "started", res.Started, "skipped", res.Skipped)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
