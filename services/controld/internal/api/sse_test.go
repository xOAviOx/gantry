package api

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/avishuklacode/gantry/services/controld/internal/store"
)

// lastEventID resolves the SSE resume cursor: the browser's Last-Event-ID header
// takes precedence, then ?last_event_id, then the legacy ?after; anything
// unparseable falls back to 0 (full replay).
func TestLastEventID(t *testing.T) {
	cases := []struct {
		name   string
		url    string
		header string
		want   int64
	}{
		{"default zero", "/logs", "", 0},
		{"header", "/logs", "42", 42},
		{"query last_event_id", "/logs?last_event_id=17", "", 17},
		{"query after legacy", "/logs?after=9", "", 9},
		{"header beats query", "/logs?last_event_id=1", "99", 99},
		{"garbage header ignored", "/logs?after=5", "not-a-number", 5},
		{"garbage everywhere", "/logs?after=xyz", "nope", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", c.url, nil)
			if c.header != "" {
				r.Header.Set("Last-Event-ID", c.header)
			}
			if got := lastEventID(r); got != c.want {
				t.Fatalf("lastEventID = %d, want %d", got, c.want)
			}
		})
	}
}

// A log event carries the seq as the SSE id (so the browser resumes from it) on a
// `log` event with a JSON payload.
func TestWriteLogEvent(t *testing.T) {
	rec := httptest.NewRecorder()
	ll := store.LogLine{Seq: 7, Stream: "build", Line: "step 3/9", TS: time.Unix(0, 0).UTC()}
	if err := writeLogEvent(rec, ll); err != nil {
		t.Fatal(err)
	}
	out := rec.Body.String()

	if !strings.HasPrefix(out, "id: 7\nevent: log\ndata: {") {
		t.Fatalf("bad framing/prefix: %q", out)
	}
	if !strings.HasSuffix(out, "}\n\n") {
		t.Fatalf("event must end with a blank line: %q", out)
	}

	// The data line must be valid JSON round-tripping the line.
	data := strings.TrimSuffix(strings.TrimPrefix(out, "id: 7\nevent: log\ndata: "), "\n\n")
	var back store.LogLine
	if err := json.Unmarshal([]byte(data), &back); err != nil {
		t.Fatalf("data is not valid json: %v (%q)", err, data)
	}
	if back.Seq != 7 || back.Stream != "build" || back.Line != "step 3/9" {
		t.Fatalf("round-trip mismatch: %+v", back)
	}
}

// A status event has no id (no resume semantics) and rides a `status` event.
func TestWriteStatusEvent(t *testing.T) {
	rec := httptest.NewRecorder()
	dep := store.Deployment{ID: "d1", Status: store.StatusBuilding}
	if err := writeStatusEvent(rec, dep); err != nil {
		t.Fatal(err)
	}
	out := rec.Body.String()
	if strings.Contains(out, "id:") {
		t.Fatalf("status events must not set an id: %q", out)
	}
	if !strings.HasPrefix(out, "event: status\ndata: {") || !strings.HasSuffix(out, "}\n\n") {
		t.Fatalf("bad status framing: %q", out)
	}
	if !strings.Contains(out, `"status":"building"`) {
		t.Fatalf("status payload missing: %q", out)
	}
}

func TestSSEHeaders(t *testing.T) {
	rec := httptest.NewRecorder()
	sseHeaders(rec)
	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Fatalf("Cache-Control = %q, want no-cache", cc)
	}
}
