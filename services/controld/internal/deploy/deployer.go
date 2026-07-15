// Package deploy runs the blue/green deploy pipeline: create a hardened
// container, publish an ephemeral health port, health-check it, then (via the
// orchestrator) flip the Caddy route and retire the old container (SPEC.md §8).
package deploy

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/strslice"
	"github.com/docker/go-connections/nat"

	"github.com/avishuklacode/gantry/services/controld/internal/config"
	"github.com/avishuklacode/gantry/services/controld/internal/docker"
	"github.com/avishuklacode/gantry/services/controld/internal/logs"
)

// Deployer performs container lifecycle operations for one app at a time.
type Deployer struct {
	dc  *docker.Client
	cfg config.Config
	log *slog.Logger
}

func NewDeployer(dc *docker.Client, cfg config.Config, log *slog.Logger) *Deployer {
	return &Deployer{dc: dc, cfg: cfg, log: log}
}

// StartRequest describes the container to run.
type StartRequest struct {
	Slug         string
	DeploymentID string
	ImageTag     string
	Port         int
	Env          map[string]string
}

// StartResult reports the running container and its ephemeral 127.0.0.1 port.
type StartResult struct {
	ContainerName string
	HostPort      int
}

// ContainerName is the deterministic name for a deployment's container.
func ContainerName(slug, deploymentID string) string {
	return fmt.Sprintf("gantry-%s-%s", slug, short(deploymentID))
}

// CreateAndStart creates a hardened container on gantry-apps, publishes the app
// port to an ephemeral 127.0.0.1 host port, starts it, and returns the port.
func (d *Deployer) CreateAndStart(ctx context.Context, req StartRequest, sink logs.Sink) (StartResult, error) {
	name := ContainerName(req.Slug, req.DeploymentID)
	port := nat.Port(fmt.Sprintf("%d/tcp", req.Port))

	// PORT is injected Heroku-style so the app listens where we expect.
	env := []string{fmt.Sprintf("PORT=%d", req.Port)}
	for k, v := range req.Env {
		if k == "PORT" {
			continue
		}
		env = append(env, k+"="+v)
	}

	pids := int64(256)
	cfg := &container.Config{
		Image:        req.ImageTag,
		Env:          env,
		ExposedPorts: nat.PortSet{port: struct{}{}},
		Labels:       docker.Labels(req.Slug, req.DeploymentID),
	}
	hostCfg := &container.HostConfig{
		PortBindings: nat.PortMap{port: []nat.PortBinding{{HostIP: "127.0.0.1", HostPort: ""}}},
		RestartPolicy: container.RestartPolicy{
			Name:              container.RestartPolicyOnFailure,
			MaximumRetryCount: 3,
		},
		CapDrop:     strslice.StrSlice{"ALL"},
		SecurityOpt: []string{"no-new-privileges"},
		LogConfig: container.LogConfig{
			Type:   "json-file",
			Config: map[string]string{"max-size": "10m", "max-file": "3"},
		},
		Resources: container.Resources{
			Memory:    512 * 1024 * 1024, // 512m
			NanoCPUs:  1_000_000_000,     // 1.0 CPU
			PidsLimit: &pids,
		},
	}
	netCfg := &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			d.cfg.AppsNetwork: {},
		},
	}

	sink.System(fmt.Sprintf("creating container %s (mem 512m, cpus 1.0, pids 256, cap-drop ALL)", name))

	// Remove any stale container with the same name (idempotency for reconcile).
	_ = d.stopAndRemove(context.WithoutCancel(ctx), name)

	created, err := d.dc.ContainerCreate(ctx, cfg, hostCfg, netCfg, nil, name)
	if err != nil {
		return StartResult{}, fmt.Errorf("container create: %w", err)
	}
	if err := d.dc.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		_ = d.stopAndRemove(context.WithoutCancel(ctx), name)
		return StartResult{}, fmt.Errorf("container start: %w", err)
	}

	// Docker Desktop populates the ephemeral host-port mapping a moment after
	// ContainerStart returns, so poll the inspect until it appears.
	hostPort, err := d.waitForHostPort(ctx, created.ID, name, port)
	if err != nil {
		return StartResult{}, err
	}

	sink.System(fmt.Sprintf("started %s, health port 127.0.0.1:%d", name, hostPort))
	return StartResult{ContainerName: name, HostPort: hostPort}, nil
}

func (d *Deployer) waitForHostPort(ctx context.Context, id, name string, port nat.Port) (int, error) {
	deadline := time.Now().Add(10 * time.Second)
	for {
		insp, err := d.dc.ContainerInspect(ctx, id)
		if err != nil {
			return 0, fmt.Errorf("inspect: %w", err)
		}
		if insp.State != nil && !insp.State.Running && insp.State.ExitCode != 0 {
			return 0, fmt.Errorf("container %s exited immediately (code %d)", name, insp.State.ExitCode)
		}
		if b := insp.NetworkSettings.Ports[port]; len(b) > 0 && b[0].HostPort != "" {
			hp, err := strconv.Atoi(b[0].HostPort)
			if err != nil {
				return 0, fmt.Errorf("parse host port %q: %w", b[0].HostPort, err)
			}
			return hp, nil
		}
		if time.Now().After(deadline) {
			return 0, fmt.Errorf("no published host port for %s after 10s", port)
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// HealthCheck polls the published port until a 2xx/3xx or the 60s budget expires.
func (d *Deployer) HealthCheck(ctx context.Context, hostPort int, healthPath string, sink logs.Sink) error {
	if healthPath == "" {
		healthPath = "/"
	}
	url := fmt.Sprintf("http://127.0.0.1:%d%s", hostPort, healthPath)
	client := &http.Client{Timeout: 3 * time.Second}
	deadline := time.Now().Add(60 * time.Second)
	sink.System("health check " + url)

	for attempt := 1; ; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		resp, err := client.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 400 {
				sink.System(fmt.Sprintf("healthy (HTTP %d) after %d attempt(s)", resp.StatusCode, attempt))
				return nil
			}
			sink.System(fmt.Sprintf("attempt %d: HTTP %d (not ready)", attempt, resp.StatusCode))
		} else {
			sink.System(fmt.Sprintf("attempt %d: %v", attempt, err))
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("not healthy within 60s")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// Retire drains for the given duration, then stops and removes the container.
func (d *Deployer) Retire(ctx context.Context, name string, drain time.Duration, sink logs.Sink) error {
	sink.System(fmt.Sprintf("draining old container %s for %s", name, drain))
	select {
	case <-ctx.Done():
	case <-time.After(drain):
	}
	return d.stopAndRemove(context.WithoutCancel(ctx), name)
}

// RemoveContainer stops and removes a container (cleanup on failure).
func (d *Deployer) RemoveContainer(ctx context.Context, name string) error {
	return d.stopAndRemove(ctx, name)
}

func (d *Deployer) stopAndRemove(ctx context.Context, name string) error {
	timeout := 10
	_ = d.dc.ContainerStop(ctx, name, container.StopOptions{Timeout: &timeout})
	return d.dc.ContainerRemove(ctx, name, container.RemoveOptions{Force: true})
}

func short(id string) string {
	s := ""
	for _, r := range id {
		if r != '-' {
			s += string(r)
		}
		if len(s) == 8 {
			break
		}
	}
	return s
}
