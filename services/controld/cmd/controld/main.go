// Command controld is the single Gantry control-plane binary (SPEC.md §2).
//
// Usage:
//
//	controld            run the server (auto-migrates on boot)
//	controld migrate    apply migrations and exit
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/avishuklacode/gantry/services/controld/internal/api"
	"github.com/avishuklacode/gantry/services/controld/internal/build"
	"github.com/avishuklacode/gantry/services/controld/internal/config"
	"github.com/avishuklacode/gantry/services/controld/internal/deploy"
	"github.com/avishuklacode/gantry/services/controld/internal/docker"
	"github.com/avishuklacode/gantry/services/controld/internal/logs"
	"github.com/avishuklacode/gantry/services/controld/internal/proxy"
	"github.com/avishuklacode/gantry/services/controld/internal/queue"
	"github.com/avishuklacode/gantry/services/controld/internal/secret"
	"github.com/avishuklacode/gantry/services/controld/internal/store"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logger := newLogger(cfg.LogLevel)
	slog.SetDefault(logger)

	// Subcommand: migrate then exit.
	if len(os.Args) > 1 && os.Args[1] == "migrate" {
		logger.Info("applying migrations")
		if err := store.Migrate(cfg.DatabaseURL); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
		logger.Info("migrations up to date")
		return nil
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := store.Migrate(cfg.DatabaseURL); err != nil {
		return fmt.Errorf("migrate on boot: %w", err)
	}

	pool, err := store.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	dc, err := docker.New(ctx, cfg.DockerHost)
	if err != nil {
		return fmt.Errorf("docker: %w", err)
	}
	defer dc.Close()

	// Wire the deploy pipeline.
	caddy := proxy.NewClient(cfg.CaddyAdmin)
	hub := logs.NewHub(pool, logger)
	builder := build.New(logger, cfg.BuildTimeout)
	deployer := deploy.NewDeployer(dc, cfg, logger)

	box, err := secret.New(cfg.MasterKeyB64)
	if err != nil {
		logger.Warn("env-var encryption disabled (set GANTRY_MASTER_KEY)", "err", err)
		box = nil
	}
	orch := deploy.NewOrchestrator(pool, builder, deployer, caddy, hub, box, cfg, logger)

	// Initial Caddy render + load (retry for slow container boot).
	bootLoadCaddy(ctx, logger, orch)

	// Queue + workers.
	host, _ := os.Hostname()
	workers := queue.NewPool(pool, logger, cfg.Workers, host)
	workers.Heartbeat = cfg.Heartbeat
	workers.LockRetryDelay = cfg.LockRetryDelay
	workers.CancelPoll = cfg.CancelPoll
	// Serialize build_deploy jobs per project via a per-project advisory lock.
	workers.SerializeKey = func(j *queue.Job) string {
		if j.Kind != "build_deploy" {
			return ""
		}
		var dj queue.DeployJob
		if err := json.Unmarshal(j.Payload, &dj); err != nil {
			return ""
		}
		return dj.ProjectID
	}
	workers.Register("build_deploy", func(ctx context.Context, j *queue.Job) error {
		var dj queue.DeployJob
		if err := json.Unmarshal(j.Payload, &dj); err != nil {
			return fmt.Errorf("bad build_deploy payload: %w", err)
		}
		// The deployment records its own success/failure/supersede; the pool sets
		// the job's terminal status (done/failed/canceled/superseded) from the
		// context cause, so a pipeline error here is only logged.
		if err := orch.Run(ctx, dj.DeploymentID, deploy.RunOpts{SkipBuild: dj.SkipBuild}); err != nil {
			logger.Error("deploy pipeline error", "deployment", dj.DeploymentID, "err", err)
		}
		return nil
	})

	workersDone := make(chan struct{})
	go func() {
		workers.Run(ctx)
		close(workersDone)
	}()

	// Reaper: requeue/fail jobs orphaned by a crashed worker (SPEC.md §7).
	go queue.RunReaper(ctx, pool, logger, cfg.ReaperInterval, cfg.JobStaleAfter)

	// HTTP server.
	srv := &http.Server{
		Addr:    cfg.ControldAddr,
		Handler: api.NewRouter(&api.Server{Logger: logger, Pool: pool, Cfg: cfg, Hub: hub, Secrets: box}),
	}
	errCh := make(chan error, 1)
	go func() {
		logger.Info("controld listening", "addr", cfg.ControldAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
		logger.Info("shutdown signal received; draining")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown", "err", err)
	}
	<-workersDone
	logger.Info("bye")
	return nil
}

func bootLoadCaddy(ctx context.Context, logger *slog.Logger, orch *deploy.Orchestrator) {
	for i := 0; i < 15; i++ {
		if err := orch.RenderAndLoad(ctx); err != nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
			continue
		}
		logger.Info("caddy config loaded on boot")
		return
	}
	logger.Error("initial caddy load failed after retries; reconciler will heal (M5)")
}

func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: lvl}))
}
