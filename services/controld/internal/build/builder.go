package build

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/avishuklacode/gantry/services/controld/internal/docker"
	"github.com/avishuklacode/gantry/services/controld/internal/logs"
)

// Request describes what to build.
type Request struct {
	RepoURL        string
	Branch         string
	SHA            string
	DockerfilePath string
	Slug           string
	DeploymentID   string
}

// Result is a successful build.
type Result struct {
	ImageTag      string
	SHA           string
	CommitMessage string
}

// Builder clones/copies a repo and builds an OCI image via `docker build`
// (BuildKit). It is the concrete implementation behind an intentionally small
// surface so an SDK/session-based builder could replace it later (D14).
type Builder struct {
	log     *slog.Logger
	timeout time.Duration
}

func New(log *slog.Logger, timeout time.Duration) *Builder {
	if timeout <= 0 {
		timeout = 15 * time.Minute
	}
	return &Builder{log: log, timeout: timeout}
}

// ImageTag is the deterministic tag for a deployment's image.
func ImageTag(slug, deploymentID string) string {
	return fmt.Sprintf("gantry/%s:d-%s", slug, deploy8(deploymentID))
}

func deploy8(id string) string {
	s := strings.ReplaceAll(id, "-", "")
	if len(s) > 8 {
		s = s[:8]
	}
	return s
}

// Build fetches the source, then builds and tags the image. onBuilding is called
// once the source is ready and the image build is about to start, so the caller
// can move the deployment from "cloning" to "building".
func (b *Builder) Build(ctx context.Context, req Request, sink logs.Sink, onBuilding func()) (Result, error) {
	tmp, err := os.MkdirTemp("", "gantry-build-")
	if err != nil {
		return Result{}, fmt.Errorf("mktemp: %w", err)
	}
	defer os.RemoveAll(tmp)

	ctxDir := filepath.Join(tmp, "ctx")
	if err := os.MkdirAll(ctxDir, 0o755); err != nil {
		return Result{}, err
	}

	sha, msg, err := fetch(ctx, req, ctxDir, sink)
	if err != nil {
		return Result{}, err
	}

	if onBuilding != nil {
		onBuilding()
	}

	imageTag := ImageTag(req.Slug, req.DeploymentID)
	if err := b.dockerBuild(ctx, ctxDir, req, imageTag, sink); err != nil {
		return Result{}, err
	}

	return Result{ImageTag: imageTag, SHA: sha, CommitMessage: msg}, nil
}

func (b *Builder) dockerBuild(ctx context.Context, ctxDir string, req Request, imageTag string, sink logs.Sink) error {
	bctx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()

	dfPath := req.DockerfilePath
	if dfPath == "" {
		dfPath = "Dockerfile"
	}

	args := []string{
		"build",
		"-f", filepath.Join(ctxDir, dfPath),
		"-t", imageTag,
		"--label", docker.LabelManaged + "=" + docker.ManagedValue,
		"--label", docker.LabelProject + "=" + req.Slug,
		"--label", docker.LabelDeployment + "=" + req.DeploymentID,
		"--progress=plain",
		ctxDir,
	}

	sink.System("docker build -t " + imageTag)
	cmd := exec.CommandContext(bctx, "docker", args...)
	cmd.Env = append(os.Environ(), "DOCKER_BUILDKIT=1")
	cmd.Stdout = sink.StreamWriter("build")
	cmd.Stderr = sink.StreamWriter("build")

	if err := cmd.Run(); err != nil {
		if bctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("build timed out after %s", b.timeout)
		}
		if ctx.Err() != nil {
			return fmt.Errorf("build canceled: %w", ctx.Err())
		}
		return fmt.Errorf("docker build failed: %w", err)
	}
	sink.System("build complete: " + imageTag)
	return nil
}
