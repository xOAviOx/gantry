package api

import (
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

	if _, err := queue.Enqueue(r.Context(), s.Pool, "build_deploy",
		map[string]any{"deployment_id": dep.ID}, queue.EnqueueOpts{}); err != nil {
		writeErr(w, http.StatusInternalServerError, "enqueue: "+err.Error())
		return
	}

	writeJSON(w, http.StatusAccepted, dep)
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
