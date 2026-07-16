package api

import (
	"errors"
	"net/http"

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
