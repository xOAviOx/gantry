package api

import (
	"context"
	"errors"
	"net/http"
	"regexp"

	"github.com/go-chi/chi/v5"

	"github.com/avishuklacode/gantry/services/controld/internal/queue"
	"github.com/avishuklacode/gantry/services/controld/internal/store"
)

var slugRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

type createProjectReq struct {
	Name           string `json:"name"`
	Slug           string `json:"slug"`
	RepoURL        string `json:"repo_url"`
	Branch         string `json:"branch"`
	DockerfilePath string `json:"dockerfile_path"`
	Port           int    `json:"port"`
	HealthPath     string `json:"health_path"`
}

func (s *Server) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	var req createProjectReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	if req.Name == "" || req.Slug == "" || req.RepoURL == "" {
		writeErr(w, http.StatusBadRequest, "name, slug, and repo_url are required")
		return
	}
	if !slugRe.MatchString(req.Slug) {
		writeErr(w, http.StatusBadRequest, "slug must be lowercase alphanumeric with hyphens")
		return
	}
	if req.Port < 1 || req.Port > 65535 {
		writeErr(w, http.StatusBadRequest, "port must be 1-65535")
		return
	}

	p := store.Project{
		Name:           req.Name,
		Slug:           req.Slug,
		RepoURL:        req.RepoURL,
		Branch:         orDefault(req.Branch, "main"),
		DockerfilePath: orDefault(req.DockerfilePath, "Dockerfile"),
		Port:           req.Port,
		HealthPath:     orDefault(req.HealthPath, "/"),
	}
	created, err := store.CreateProject(r.Context(), s.Pool, p)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "create project: "+err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) handleListProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := store.ListProjectsWithStatus(r.Context(), s.Pool)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if projects == nil {
		projects = []store.ProjectWithStatus{}
	}
	writeJSON(w, http.StatusOK, projects)
}

func (s *Server) handleGetProject(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	proj, err := store.GetProject(r.Context(), s.Pool, id)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "project not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	deps, err := store.ListDeploymentsByProject(r.Context(), s.Pool, id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if deps == nil {
		deps = []store.Deployment{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"project": proj, "deployments": deps})
}

type deployReq struct {
	SHA string `json:"sha"`
}

func (s *Server) handleDeployProject(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	proj, err := store.GetProject(r.Context(), s.Pool, id)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "project not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	var req deployReq
	_ = readJSON(r, &req) // body optional

	dep, err := store.CreateDeployment(r.Context(), s.Pool, store.Deployment{
		ProjectID: proj.ID,
		CommitSHA: req.SHA,
		Trigger:   store.TriggerManual,
		Status:    store.StatusQueued,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "create deployment: "+err.Error())
		return
	}

	// Newest deploy wins: this supersedes any queued build for the project and
	// asks a running one to stop (SPEC.md §7).
	if _, err := queue.EnqueueDeploy(r.Context(), s.Pool, queue.DeployJob{
		DeploymentID: dep.ID,
		ProjectID:    proj.ID,
	}, queue.EnqueueOpts{}); err != nil {
		writeErr(w, http.StatusInternalServerError, "enqueue: "+err.Error())
		return
	}

	writeJSON(w, http.StatusAccepted, dep)
}

// handleRollback redeploys a previous deployment's retained image (skip-build),
// running the same blue/green path so it's still zero-downtime (SPEC.md §8).
func (s *Server) handleRollback(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	proj, err := store.GetProject(r.Context(), s.Pool, id)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "project not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	var req struct {
		DeploymentID string `json:"deployment_id"`
	}
	if err := readJSON(r, &req); err != nil || req.DeploymentID == "" {
		writeErr(w, http.StatusBadRequest, "deployment_id required")
		return
	}
	target, err := store.GetDeployment(r.Context(), s.Pool, req.DeploymentID)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "deployment not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if target.ProjectID != proj.ID {
		writeErr(w, http.StatusBadRequest, "deployment does not belong to this project")
		return
	}
	if target.ImageTag == "" {
		writeErr(w, http.StatusUnprocessableEntity, "target deployment has no built image to roll back to")
		return
	}

	dep, err := s.enqueueSkipBuild(r.Context(), proj, target, store.TriggerRollback)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, dep)
}

// handleEnvRestart redeploys the current live image (skip-build) so newly-saved
// env vars take effect without a rebuild (SPEC.md §8).
func (s *Server) handleEnvRestart(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	proj, err := store.GetProject(r.Context(), s.Pool, id)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "project not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	live, err := store.GetLiveDeployment(r.Context(), s.Pool, proj.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if live == nil || live.ImageTag == "" {
		writeErr(w, http.StatusConflict, "no live deployment to restart")
		return
	}
	dep, err := s.enqueueSkipBuild(r.Context(), proj, *live, store.TriggerEnvRestart)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, dep)
}

// enqueueSkipBuild creates a deployment that reuses src's image and enqueues a
// supersession-aware skip-build deploy (shared by rollback + env/restart).
func (s *Server) enqueueSkipBuild(ctx context.Context, proj store.Project, src store.Deployment, trigger string) (store.Deployment, error) {
	dep, err := store.CreateDeployment(ctx, s.Pool, store.Deployment{
		ProjectID:     proj.ID,
		CommitSHA:     src.CommitSHA,
		CommitMessage: src.CommitMessage,
		Trigger:       trigger,
		Status:        store.StatusQueued,
		ImageTag:      src.ImageTag,
	})
	if err != nil {
		return store.Deployment{}, err
	}
	if _, err := queue.EnqueueDeploy(ctx, s.Pool, queue.DeployJob{
		DeploymentID: dep.ID,
		ProjectID:    proj.ID,
		SkipBuild:    true,
	}, queue.EnqueueOpts{}); err != nil {
		return store.Deployment{}, err
	}
	return dep, nil
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
