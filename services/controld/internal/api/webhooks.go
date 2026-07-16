package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/avishuklacode/gantry/services/controld/internal/queue"
	"github.com/avishuklacode/gantry/services/controld/internal/store"
)

// githubPushPayload is the subset of GitHub's push event we act on.
type githubPushPayload struct {
	Ref        string `json:"ref"` // refs/heads/<branch>
	After      string `json:"after"`
	Repository struct {
		FullName string `json:"full_name"`
		CloneURL string `json:"clone_url"`
		HTMLURL  string `json:"html_url"`
		SSHURL   string `json:"ssh_url"`
	} `json:"repository"`
}

// handleGitHubWebhook receives GitHub push events (SPEC.md §15). It has no auth
// middleware — authenticity is the HMAC signature. Verified pushes on a project's
// configured branch enqueue a deploy; replays (same delivery id) are deduped and
// it answers 202 fast, doing the work in the queue.
func (s *Server) handleGitHubWebhook(w http.ResponseWriter, r *http.Request) {
	if s.Cfg.WebhookSecret == "" {
		writeErr(w, http.StatusServiceUnavailable, "webhook secret not configured")
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "read body")
		return
	}
	if !validSignature(s.Cfg.WebhookSecret, body, r.Header.Get("X-Hub-Signature-256")) {
		writeErr(w, http.StatusUnauthorized, "invalid signature")
		return
	}

	switch r.Header.Get("X-GitHub-Event") {
	case "ping":
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	case "push":
		// handled below
	default:
		writeJSON(w, http.StatusAccepted, map[string]string{"ignored": r.Header.Get("X-GitHub-Event")})
		return
	}

	// Dedupe the whole delivery before doing any work (SPEC.md §15).
	delivery := r.Header.Get("X-GitHub-Delivery")
	if delivery != "" {
		fresh, err := store.RecordDelivery(r.Context(), s.Pool, delivery)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !fresh {
			writeJSON(w, http.StatusAccepted, map[string]bool{"deduped": true})
			return
		}
	}

	var p githubPushPayload
	if err := json.Unmarshal(body, &p); err != nil {
		writeErr(w, http.StatusBadRequest, "bad payload")
		return
	}
	branch := strings.TrimPrefix(p.Ref, "refs/heads/")

	projects, err := store.ListProjects(r.Context(), s.Pool)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	queued := 0
	for _, proj := range projects {
		if proj.Branch != branch || !repoMatches(proj.RepoURL, p) {
			continue
		}
		dep, err := store.CreateDeployment(r.Context(), s.Pool, store.Deployment{
			ProjectID: proj.ID,
			CommitSHA: p.After,
			Trigger:   store.TriggerWebhook,
			Status:    store.StatusQueued,
		})
		if err != nil {
			s.Logger.Error("webhook create deployment", "project", proj.ID, "err", err)
			continue
		}
		dedupe := ""
		if delivery != "" {
			dedupe = delivery + ":" + proj.ID
		}
		if _, err := queue.EnqueueDeploy(r.Context(), s.Pool, queue.DeployJob{
			DeploymentID: dep.ID,
			ProjectID:    proj.ID,
		}, queue.EnqueueOpts{DedupeKey: dedupe}); err != nil {
			s.Logger.Error("webhook enqueue", "project", proj.ID, "err", err)
			continue
		}
		queued++
	}

	s.Logger.Info("github webhook", "branch", branch, "queued", queued, "delivery", delivery)
	writeJSON(w, http.StatusAccepted, map[string]any{"queued": queued, "branch": branch})
}

// handleCancelDeployment cooperatively cancels a deployment's in-flight job.
func (s *Server) handleCancelDeployment(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := store.GetDeployment(r.Context(), s.Pool, id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "deployment not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	ok, err := queue.RequestCancel(r.Context(), s.Pool, id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeErr(w, http.StatusConflict, "no active deploy to cancel")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"canceling": true})
}

// validSignature verifies GitHub's HMAC-SHA256 body signature in constant time.
func validSignature(secret string, body []byte, header string) bool {
	const prefix = "sha256="
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	want, err := hex.DecodeString(strings.TrimPrefix(header, prefix))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(mac.Sum(nil), want)
}

// repoMatches reports whether a project's repo_url refers to the pushed repo,
// tolerating the various URL forms GitHub sends (clone/html/ssh/full_name).
func repoMatches(projectRepoURL string, p githubPushPayload) bool {
	target := normalizeRepo(projectRepoURL)
	if target == "" {
		return false
	}
	for _, cand := range []string{p.Repository.CloneURL, p.Repository.HTMLURL, p.Repository.SSHURL, "github.com/" + p.Repository.FullName} {
		if cand != "" && normalizeRepo(cand) == target {
			return true
		}
	}
	return false
}

// normalizeRepo reduces a git URL to a comparable "host/owner/repo" form.
func normalizeRepo(u string) string {
	s := strings.ToLower(strings.TrimSpace(u))
	if s == "" {
		return ""
	}
	s = strings.TrimPrefix(s, "git+")
	if strings.HasPrefix(s, "git@") { // scp-like: git@github.com:owner/repo.git
		s = strings.Replace(strings.TrimPrefix(s, "git@"), ":", "/", 1)
	} else {
		for _, sch := range []string{"https://", "http://", "ssh://", "git://"} {
			s = strings.TrimPrefix(s, sch)
		}
		if at := strings.Index(s, "@"); at != -1 {
			if slash := strings.Index(s, "/"); slash == -1 || at < slash {
				s = s[at+1:] // strip user@host
			}
		}
	}
	s = strings.TrimSuffix(s, "/")
	s = strings.TrimSuffix(s, ".git")
	return s
}
