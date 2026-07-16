package deploy

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"

	"github.com/avishuklacode/gantry/services/controld/internal/docker"
	"github.com/avishuklacode/gantry/services/controld/internal/queue"
	"github.com/avishuklacode/gantry/services/controld/internal/store"
)

// These MUST match what RenderAndLoad renders, so drift detection looks for the
// right hostnames in Caddy's live config.
const (
	dashboardHost = "paas.localhost"
	appsSuffix    = "apps.localhost"
)

// Reconciler drives actual Docker/Caddy state toward the desired state in the DB
// on a fixed interval (SPEC.md §13): recreate missing live containers, reap
// orphaned gantry containers, and repair Caddy on config drift. Every healing
// action is logged at warn with reconciler=true.
type Reconciler struct {
	orch *Orchestrator
	dc   *docker.Client
	log  *slog.Logger
}

func NewReconciler(orch *Orchestrator, dc *docker.Client) *Reconciler {
	return &Reconciler{orch: orch, dc: dc, log: orch.log}
}

// Run reconciles once per interval until ctx is canceled.
func (rc *Reconciler) Run(ctx context.Context, interval time.Duration) {
	rc.log.Info("reconciler starting", "interval", interval)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			rc.Reconcile(ctx)
		}
	}
}

type actualContainer struct {
	name  string
	state string // "running", "exited", ...
}

// Reconcile runs one full sweep. It is resilient: a failure in one pass does not
// skip the others.
func (rc *Reconciler) Reconcile(ctx context.Context) {
	actual, err := rc.appContainers(ctx)
	if err != nil {
		rc.log.Error("reconciler: list containers", "err", err)
	}

	// Pass 1: recreate live deployments whose container is missing or not running.
	healed := 0
	if live, err := store.ListLiveForReconcile(ctx, rc.orch.pool); err != nil {
		rc.log.Error("reconciler: list live", "err", err)
	} else {
		for _, app := range live {
			c, ok := actual[app.DeploymentID]
			if ok && c.state == "running" {
				continue
			}
			if rc.heal(ctx, app, ok) {
				healed++
			}
		}
	}

	// Pass 2: reap gantry app-containers no live/in-flight deployment owns.
	if protected, err := store.ListProtectedDeploymentIDs(ctx, rc.orch.pool); err != nil {
		rc.log.Error("reconciler: list protected", "err", err)
	} else {
		for depID, c := range actual {
			if protected[depID] {
				continue
			}
			rc.log.Warn("reconciler reaping orphan container", "container", c.name, "deployment", depID, "reconciler", true)
			if err := rc.orch.deployer.RemoveContainer(ctx, c.name); err != nil {
				rc.log.Warn("reconciler: remove orphan failed", "container", c.name, "err", err)
			}
		}
	}

	// Pass 3: repair Caddy on drift (or after a heal, to refresh its upstreams).
	rc.reconcileCaddy(ctx, healed > 0)
}

// heal recreates one live deployment's container (serialized against deploys for
// the same project via the advisory lock). existed indicates a stale/stopped
// container was found (vs. wholly missing).
func (rc *Reconciler) heal(ctx context.Context, app store.LiveApp, existed bool) bool {
	lock, got, err := queue.AcquireProjectLock(ctx, rc.orch.pool, app.ProjectID)
	if err != nil {
		rc.log.Warn("reconciler: lock error, skipping heal", "slug", app.Slug, "err", err)
		return false
	}
	if !got {
		// A deploy is in flight for this project; it will settle the state.
		return false
	}
	defer lock.Release()

	rc.log.Warn("reconciler recreating live container",
		"slug", app.Slug, "deployment", app.DeploymentID, "stale", existed, "reconciler", true)

	sink := reconcileSink{log: rc.log}
	env, err := rc.orch.projectEnv(ctx, app.ProjectID)
	if err != nil {
		rc.log.Error("reconciler: decrypt env", "slug", app.Slug, "err", err)
		return false
	}
	start, err := rc.orch.deployer.CreateAndStart(ctx, StartRequest{
		Slug:         app.Slug,
		DeploymentID: app.DeploymentID,
		ImageTag:     app.ImageTag,
		Port:         app.Port,
		Env:          env,
	}, sink)
	if err != nil {
		rc.log.Error("reconciler: recreate container", "slug", app.Slug, "err", err)
		return false
	}
	if err := rc.orch.deployer.HealthCheck(ctx, start.HostPort, app.HealthPath, sink); err != nil {
		rc.log.Warn("reconciler: recreated container unhealthy, removing (will retry)", "slug", app.Slug, "err", err, "reconciler", true)
		_ = rc.orch.deployer.RemoveContainer(context.WithoutCancel(ctx), start.ContainerName)
		return false
	}
	if err := store.SetDeploymentRuntime(ctx, rc.orch.pool, app.DeploymentID, start.ContainerName, start.HostPort); err != nil {
		rc.log.Error("reconciler: record runtime", "slug", app.Slug, "err", err)
	}
	rc.log.Warn("reconciler healed live container", "slug", app.Slug, "reconciler", true)
	return true
}

// reconcileCaddy reloads Caddy if its live config is missing any expected route,
// or unconditionally when force is set (e.g. a container was just recreated).
func (rc *Reconciler) reconcileCaddy(ctx context.Context, force bool) {
	upstreams, err := store.ListLiveAppUpstreams(ctx, rc.orch.pool)
	if err != nil {
		rc.log.Error("reconciler: list upstreams", "err", err)
		return
	}
	markers := []string{dashboardHost}
	for _, u := range upstreams {
		markers = append(markers, u.Slug+"."+appsSuffix, u.Dial)
	}

	current, err := rc.orch.caddy.GetConfig(ctx)
	if err != nil {
		rc.log.Warn("reconciler: caddy config unreadable, reloading", "err", err, "reconciler", true)
		if err := rc.orch.RenderAndLoad(ctx); err != nil {
			rc.log.Error("reconciler: caddy reload failed", "err", err)
		}
		return
	}
	if force || !hasAllMarkers(current, markers) {
		rc.log.Warn("reconciler repairing caddy config", "force", force, "routes", len(upstreams), "reconciler", true)
		if err := rc.orch.RenderAndLoad(ctx); err != nil {
			rc.log.Error("reconciler: caddy reload failed", "err", err)
		}
	}
}

// appContainers lists gantry app containers (those carrying a deployment label),
// keyed by deployment id. Infra containers (postgres/caddy) are managed-labeled
// too but have no deployment label, so they are never included — and never reaped.
func (rc *Reconciler) appContainers(ctx context.Context) (map[string]actualContainer, error) {
	list, err := rc.dc.ContainerList(ctx, container.ListOptions{
		All:     true,
		Filters: filters.NewArgs(filters.Arg("label", docker.LabelManaged+"="+docker.ManagedValue)),
	})
	if err != nil {
		return nil, err
	}
	out := make(map[string]actualContainer, len(list))
	for _, c := range list {
		depID := c.Labels[docker.LabelDeployment]
		if depID == "" {
			continue // infra or unlabeled — leave it alone
		}
		name := ""
		if len(c.Names) > 0 {
			name = strings.TrimPrefix(c.Names[0], "/")
		}
		out[depID] = actualContainer{name: name, state: c.State}
	}
	return out, nil
}

// hasAllMarkers reports whether every expected host/dial appears in Caddy's live
// config bytes — a cheap, robust drift check (a wiped config drops all of them).
func hasAllMarkers(config []byte, markers []string) bool {
	for _, m := range markers {
		if !bytes.Contains(config, []byte(m)) {
			return false
		}
	}
	return true
}

// reconcileSink routes container lifecycle log lines to slog (tagged reconciler)
// instead of a deployment's persisted log, since healing is not a user deploy.
type reconcileSink struct{ log *slog.Logger }

func (s reconcileSink) Line(stream, text string) {
	s.log.Info("reconcile", "stream", stream, "line", text, "reconciler", true)
}
func (s reconcileSink) System(text string) {
	s.log.Info("reconcile", "line", text, "reconciler", true)
}
func (s reconcileSink) StreamWriter(string) io.Writer { return io.Discard }
