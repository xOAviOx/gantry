package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/avishuklacode/gantry/services/controld/internal/store"
)

func (s *Server) handleGetDeployment(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	dep, err := store.GetDeployment(r.Context(), s.Pool, id)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "deployment not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, dep)
}

// handleGetLogs is the M1 polling endpoint: GET .../logs?after=<seq> returns log
// lines with seq greater than after. (M2 replaces this with SSE.)
func (s *Server) handleGetLogs(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)

	lines, err := store.ListLogLines(r.Context(), s.Pool, id, after)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if lines == nil {
		lines = []store.LogLine{}
	}
	next := after
	if n := len(lines); n > 0 {
		next = lines[n-1].Seq
	}
	writeJSON(w, http.StatusOK, map[string]any{"lines": lines, "next": next})
}
