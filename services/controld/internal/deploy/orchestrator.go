package deploy

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/avishuklacode/gantry/services/controld/internal/build"
	"github.com/avishuklacode/gantry/services/controld/internal/config"
	"github.com/avishuklacode/gantry/services/controld/internal/logs"
	"github.com/avishuklacode/gantry/services/controld/internal/proxy"
	"github.com/avishuklacode/gantry/services/controld/internal/store"
)

// Orchestrator drives a single deployment through the state machine (SPEC.md §8),
// recording status transitions and streaming logs as it goes.
type Orchestrator struct {
	pool     *pgxpool.Pool
	builder  *build.Builder
	deployer *Deployer
	caddy    *proxy.Client
	hub      *logs.Hub
	cfg      config.Config
	log      *slog.Logger
}

func NewOrchestrator(pool *pgxpool.Pool, builder *build.Builder, deployer *Deployer, caddy *proxy.Client, hub *logs.Hub, cfg config.Config, log *slog.Logger) *Orchestrator {
	return &Orchestrator{pool: pool, builder: builder, deployer: deployer, caddy: caddy, hub: hub, cfg: cfg, log: log}
}

// RunOpts controls a deployment run.
type RunOpts struct {
	SkipBuild bool // rollback / env-restart reuse an existing image
}

// Run executes the pipeline for a deployment. It records the outcome on the
// deployment row; the returned error is for the caller's logs only (M1 does not
// retry deploy jobs).
func (o *Orchestrator) Run(ctx context.Context, deploymentID string, opts RunOpts) error {
	dep, err := store.GetDeployment(ctx, o.pool, deploymentID)
	if err != nil {
		return fmt.Errorf("load deployment: %w", err)
	}
	proj, err := store.GetProject(ctx, o.pool, dep.ProjectID)
	if err != nil {
		return fmt.Errorf("load project: %w", err)
	}

	w := o.hub.Open(ctx, deploymentID)
	defer w.Close()
	w.System(fmt.Sprintf("=== deploy %s · project %s · trigger %s ===", short(deploymentID), proj.Slug, dep.Trigger))

	fail := func(status, msg string) error {
		w.System("FAILED (" + status + "): " + msg)
		if err := store.FinishDeployment(context.WithoutCancel(ctx), o.pool, deploymentID, status, msg); err != nil {
			o.log.Error("record failure", "deployment", deploymentID, "err", err)
		}
		o.emit(ctx, deploymentID)
		return errors.New(msg)
	}

	_ = store.MarkDeploymentStarted(ctx, o.pool, deploymentID, store.StatusCloning)
	o.emit(ctx, deploymentID)

	imageTag := dep.ImageTag
	if !opts.SkipBuild {
		res, err := o.builder.Build(ctx, build.Request{
			RepoURL:        proj.RepoURL,
			Branch:         proj.Branch,
			SHA:            dep.CommitSHA,
			DockerfilePath: proj.DockerfilePath,
			Slug:           proj.Slug,
			DeploymentID:   deploymentID,
		}, w, func() {
			_ = store.SetDeploymentStatus(ctx, o.pool, deploymentID, store.StatusBuilding)
			o.emit(ctx, deploymentID)
		})
		if err != nil {
			return fail(store.StatusBuildFailed, "build: "+err.Error())
		}
		imageTag = res.ImageTag
		if err := store.SetDeploymentBuild(ctx, o.pool, deploymentID, res.SHA, res.CommitMessage, res.ImageTag); err != nil {
			o.log.Error("record build", "deployment", deploymentID, "err", err)
		}
		o.emit(ctx, deploymentID)
	} else {
		if imageTag == "" {
			return fail(store.StatusDeployFailed, "skip_build set but deployment has no image_tag")
		}
		w.System("skip build; reusing image " + imageTag)
	}

	// TODO(M4): decrypt project env vars here.
	env := map[string]string{}

	_ = store.SetDeploymentStatus(ctx, o.pool, deploymentID, store.StatusStarting)
	o.emit(ctx, deploymentID)
	start, err := o.deployer.CreateAndStart(ctx, StartRequest{
		Slug:         proj.Slug,
		DeploymentID: deploymentID,
		ImageTag:     imageTag,
		Port:         proj.Port,
		Env:          env,
	}, w)
	if err != nil {
		return fail(store.StatusDeployFailed, "start: "+err.Error())
	}
	if err := store.SetDeploymentRuntime(ctx, o.pool, deploymentID, start.ContainerName, start.HostPort); err != nil {
		o.log.Error("record runtime", "deployment", deploymentID, "err", err)
	}

	_ = store.SetDeploymentStatus(ctx, o.pool, deploymentID, store.StatusChecking)
	o.emit(ctx, deploymentID)
	if err := o.deployer.HealthCheck(ctx, start.HostPort, proj.HealthPath, w); err != nil {
		_ = o.deployer.RemoveContainer(context.WithoutCancel(ctx), start.ContainerName)
		return fail(store.StatusDeployFailed, "health check: "+err.Error())
	}

	// Route: promote in DB (single live per project), then re-render Caddy.
	_ = store.SetDeploymentStatus(ctx, o.pool, deploymentID, store.StatusRouting)
	o.emit(ctx, deploymentID)
	old, err := store.PromoteToLive(ctx, o.pool, dep.ProjectID, deploymentID)
	if err != nil {
		_ = o.deployer.RemoveContainer(context.WithoutCancel(ctx), start.ContainerName)
		return fail(store.StatusDeployFailed, "promote: "+err.Error())
	}
	o.emit(ctx, deploymentID)
	if err := o.RenderAndLoad(ctx); err != nil {
		// The deployment is live in the DB; the reconciler (M5) re-renders on drift.
		w.System("WARN caddy load failed (will reconcile): " + err.Error())
		o.log.Error("caddy load after deploy", "deployment", deploymentID, "err", err)
	} else {
		w.System(fmt.Sprintf("routed %s.apps.localhost -> %s:%d", proj.Slug, start.ContainerName, proj.Port))
	}

	// Retire previous live container(s) after a short drain.
	for _, name := range old {
		if err := o.deployer.Retire(ctx, name, 10*time.Second, w); err != nil {
			o.log.Warn("retire old container", "container", name, "err", err)
		}
	}

	w.System(fmt.Sprintf("=== LIVE at http://%s.apps.localhost/ ===", proj.Slug))
	o.log.Info("deployment live", "deployment", deploymentID, "slug", proj.Slug, "container", start.ContainerName)
	return nil
}

// emit publishes the deployment's current state to any live status subscribers
// (the /events SSE stream). It reloads via a detached context so the terminal
// live/failed event still goes out even if the run's ctx was canceled.
func (o *Orchestrator) emit(ctx context.Context, deploymentID string) {
	dep, err := store.GetDeployment(context.WithoutCancel(ctx), o.pool, deploymentID)
	if err != nil {
		o.log.Warn("status emit: reload failed", "deployment", deploymentID, "err", err)
		return
	}
	o.hub.PublishStatus(deploymentID, dep)
}

// RenderAndLoad renders the full desired Caddy config from DB state and pushes
// it. Reused on boot and after every route change.
func (o *Orchestrator) RenderAndLoad(ctx context.Context) error {
	apps, err := store.ListLiveAppUpstreams(ctx, o.pool)
	if err != nil {
		return err
	}
	blob, err := proxy.Render(proxy.RenderInput{
		DashboardHost: "paas.localhost",
		AppsSuffix:    "apps.localhost",
		HostInternal:  o.cfg.HostInternal,
		ControldPort:  o.cfg.ControldPort,
		WebPort:       o.cfg.WebPort,
		AdminListen:   "0.0.0.0:2019",
		AdminOrigins:  o.cfg.CaddyOrigins,
		Apps:          apps,
	})
	if err != nil {
		return err
	}
	return o.caddy.Load(ctx, blob)
}
