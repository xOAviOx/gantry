package api

import (
	"errors"
	"net/http"

	"github.com/avishuklacode/gantry/services/controld/internal/gc"
)

// handleDisk returns a `docker system df`-style summary for the dashboard widget.
func (s *Server) handleDisk(w http.ResponseWriter, r *http.Request) {
	if s.GC == nil {
		writeErr(w, http.StatusServiceUnavailable, "gc unavailable")
		return
	}
	rep, err := s.GC.Disk(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

// handleGC runs garbage collection on demand and returns what it reclaimed.
func (s *Server) handleGC(w http.ResponseWriter, r *http.Request) {
	if s.GC == nil {
		writeErr(w, http.StatusServiceUnavailable, "gc unavailable")
		return
	}
	rep, err := s.GC.Run(r.Context())
	if errors.Is(err, gc.ErrBusy) {
		writeErr(w, http.StatusConflict, "gc already in progress")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rep)
}
