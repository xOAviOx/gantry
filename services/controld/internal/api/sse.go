package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/avishuklacode/gantry/services/controld/internal/store"
)

// heartbeat cadence for idle SSE connections (SPEC.md §10: comment every 15s).
const sseHeartbeat = 15 * time.Second

// handleStreamLogs streams a deployment's build/deploy logs over SSE
// (SPEC.md §10). On connect it replays the persisted backlog after Last-Event-ID
// then streams live lines; each event's id is the line's seq, so the browser's
// native EventSource reconnect resumes seamlessly. A slow client that overflows
// its buffer is sent a `gap` event and disconnected.
func (s *Server) handleStreamLogs(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	if _, err := store.GetDeployment(r.Context(), s.Pool, id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "deployment not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	lastID := lastEventID(r)

	// Subscribe BEFORE reading the backlog so a line produced in the gap between
	// the DB read and going live is not lost; overlap is deduped by seq below.
	sub := s.Hub.SubscribeLogs(id)
	defer sub.Close()

	sseHeaders(w)
	flusher.Flush()

	ctx := r.Context()

	// 1) Replay persisted backlog after lastID.
	maxSeq := lastID
	backlog, err := store.ListLogLines(ctx, s.Pool, id, lastID)
	if err != nil {
		s.Logger.Error("sse log backlog", "deployment", id, "err", err)
	}
	for _, ll := range backlog {
		if writeLogEvent(w, ll) != nil {
			return
		}
		maxSeq = ll.Seq
	}
	flusher.Flush()

	// 2) Stream live, skipping anything already covered by the backlog.
	ping := time.NewTicker(sseHeartbeat)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-sub.Dropped():
			_ = writeRaw(w, "event: gap\ndata: {\"reason\":\"overflow\"}\n\n")
			flusher.Flush()
			return
		case ll := <-sub.C:
			if ll.Seq <= maxSeq {
				continue
			}
			if writeLogEvent(w, ll) != nil {
				return
			}
			maxSeq = ll.Seq
			flusher.Flush()
		case <-ping.C:
			if writeRaw(w, ": ping\n\n") != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// handleStreamEvents streams a deployment's status transitions over SSE so the
// pipeline UI updates live (SPEC.md §11). It sends the current state immediately
// on connect, so a reconnect always re-syncs.
func (s *Server) handleStreamEvents(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}

	// Subscribe first so we don't miss a transition between the snapshot read and
	// going live (status is monotonic; a rare duplicate is harmless).
	sub := s.Hub.SubscribeStatus(id)
	defer sub.Close()

	dep, err := store.GetDeployment(r.Context(), s.Pool, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "deployment not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	sseHeaders(w)
	flusher.Flush()

	if writeStatusEvent(w, dep) != nil {
		return
	}
	flusher.Flush()

	ctx := r.Context()
	ping := time.NewTicker(sseHeartbeat)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-sub.Dropped():
			// Status overflow is unlikely; the client reconnects and re-syncs.
			return
		case d := <-sub.C:
			if writeStatusEvent(w, d) != nil {
				return
			}
			flusher.Flush()
		case <-ping.C:
			if writeRaw(w, ": ping\n\n") != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// --- SSE wire helpers ---

func sseHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no") // defensive: disable proxy buffering
	w.WriteHeader(http.StatusOK)
}

func writeLogEvent(w http.ResponseWriter, ll store.LogLine) error {
	data, err := json.Marshal(ll)
	if err != nil {
		return err
	}
	return writeRaw(w, "id: "+strconv.FormatInt(ll.Seq, 10)+"\nevent: log\ndata: "+string(data)+"\n\n")
}

func writeStatusEvent(w http.ResponseWriter, dep store.Deployment) error {
	data, err := json.Marshal(dep)
	if err != nil {
		return err
	}
	return writeRaw(w, "event: status\ndata: "+string(data)+"\n\n")
}

func writeRaw(w http.ResponseWriter, s string) error {
	_, err := io.WriteString(w, s)
	return err
}

// lastEventID resolves the resume cursor: the browser sends Last-Event-ID on
// reconnect; a manual client may pass ?last_event_id= (or the legacy ?after=).
func lastEventID(r *http.Request) int64 {
	if v := r.Header.Get("Last-Event-ID"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	q := r.URL.Query()
	for _, key := range []string{"last_event_id", "after"} {
		if v := q.Get(key); v != "" {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				return n
			}
		}
	}
	return 0
}
