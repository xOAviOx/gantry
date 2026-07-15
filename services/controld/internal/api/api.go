// Package api holds controld's chi router, middleware, and HTTP handlers.
package api

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/avishuklacode/gantry/services/controld/internal/config"
)

// Server carries handler dependencies (injected; no package-level state).
type Server struct {
	Logger *slog.Logger
	Pool   *pgxpool.Pool
	Cfg    config.Config
}

// NewRouter wires middleware and routes. Public: healthz, login, webhooks (M3).
// Everything else requires the admin token (bearer or session cookie).
func NewRouter(s *Server) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Use(requestLogger(s.Logger))

	r.Route("/api", func(r chi.Router) {
		r.Get("/healthz", s.handleHealthz)
		r.Post("/login", s.handleLogin)

		r.Group(func(r chi.Router) {
			r.Use(s.authRequired)

			r.Get("/projects", s.handleListProjects)
			r.Post("/projects", s.handleCreateProject)
			r.Get("/projects/{id}", s.handleGetProject)
			r.Post("/projects/{id}/deploy", s.handleDeployProject)

			r.Get("/deployments/{id}", s.handleGetDeployment)
			r.Get("/deployments/{id}/logs", s.handleGetLogs)
		})
	})

	return r
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// --- helpers ---

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func readJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}
