package api

import (
	"errors"
	"net/http"
	"regexp"

	"github.com/go-chi/chi/v5"

	"github.com/avishuklacode/gantry/services/controld/internal/store"
)

// POSIX-ish env var name.
var envKeyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type putEnvReq struct {
	Set    map[string]string `json:"set"`
	Delete []string          `json:"delete"`
}

// handleListEnv returns a project's env-var keys and update times — never values
// (the UI is write-only; use reveal for a single value).
func (s *Server) handleListEnv(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	keys, err := store.ListEnvKeys(r.Context(), s.Pool, id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if keys == nil {
		keys = []store.EnvVarMeta{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": keys})
}

// handlePutEnv upserts and/or deletes env vars. It changes nothing about running
// containers by itself (SPEC.md §15) — a deploy or env/restart applies them.
func (s *Server) handlePutEnv(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if s.Secrets == nil {
		writeErr(w, http.StatusServiceUnavailable, "env encryption unavailable: set GANTRY_MASTER_KEY")
		return
	}
	if _, err := store.GetProject(r.Context(), s.Pool, id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "project not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	var req putEnvReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	for k := range req.Set {
		if !envKeyRe.MatchString(k) {
			writeErr(w, http.StatusBadRequest, "invalid env key: "+k)
			return
		}
	}

	for k, v := range req.Set {
		ct, nonce, err := s.Secrets.Encrypt(v)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "encrypt: "+err.Error())
			return
		}
		if err := store.UpsertEnvVar(r.Context(), s.Pool, id, k, ct, nonce); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	for _, k := range req.Delete {
		if err := store.DeleteEnvVar(r.Context(), s.Pool, id, k); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	// Audit the mutation by key only (never values).
	s.Logger.Info("env vars updated", "project", id, "set", keysOf(req.Set), "deleted", req.Delete)

	keys, err := store.ListEnvKeys(r.Context(), s.Pool, id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if keys == nil {
		keys = []store.EnvVarMeta{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": keys})
}

// handleRevealEnv decrypts and returns a single value. Reveals are explicit and
// audit-logged (SPEC.md §11).
func (s *Server) handleRevealEnv(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if s.Secrets == nil {
		writeErr(w, http.StatusServiceUnavailable, "env encryption unavailable: set GANTRY_MASTER_KEY")
		return
	}
	var req struct {
		Key string `json:"key"`
	}
	if err := readJSON(r, &req); err != nil || req.Key == "" {
		writeErr(w, http.StatusBadRequest, "key required")
		return
	}
	ct, nonce, err := store.GetEnvVar(r.Context(), s.Pool, id, req.Key)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "env var not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	val, err := s.Secrets.Decrypt(ct, nonce)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "decrypt failed")
		return
	}
	s.Logger.Warn("env var revealed", "project", id, "key", req.Key)
	writeJSON(w, http.StatusOK, map[string]string{"key": req.Key, "value": val})
}

func keysOf(m map[string]string) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
