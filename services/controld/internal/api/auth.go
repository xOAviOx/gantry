package api

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

const sessionCookie = "gantry_session"

// handleLogin exchanges the admin token for an httpOnly session cookie.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token string `json:"token"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	if !s.tokenValid(body.Token) {
		writeErr(w, http.StatusUnauthorized, "invalid token")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    s.Cfg.AdminToken,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   7 * 24 * 3600,
	})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// authRequired gates a route group on the admin token (bearer or session cookie).
func (s *Server) authRequired(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok := bearerToken(r)
		if tok == "" {
			if c, err := r.Cookie(sessionCookie); err == nil {
				tok = c.Value
			}
		}
		if !s.tokenValid(tok) {
			writeErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) tokenValid(tok string) bool {
	if s.Cfg.AdminToken == "" || tok == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(tok), []byte(s.Cfg.AdminToken)) == 1
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if after, ok := strings.CutPrefix(h, "Bearer "); ok {
		return strings.TrimSpace(after)
	}
	return ""
}
