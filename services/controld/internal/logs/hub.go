// Package logs is the build/deploy log pipeline: a hub hands out per-deployment
// writers that assign sequence numbers, batch-persist to log_lines, and fan out
// to live SSE subscribers (SPEC.md §10). The hub also carries a second, lighter
// fan-out for deployment status changes so the pipeline UI can update live.
package logs

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/avishuklacode/gantry/services/controld/internal/store"
)

// Buffer sizes for live subscribers. Logs get a deep buffer (a burst of build
// output must not drop a well-behaved client); on overflow the subscriber is
// dropped and told via a gap event. Status changes are infrequent, so a small
// buffer is plenty.
const (
	LogSubBuffer    = 1000
	statusSubBuffer = 16
)

// Sink accepts log lines for a single deployment.
type Sink interface {
	Line(stream, text string)
	System(text string)
	StreamWriter(stream string) io.Writer
}

// Hub creates per-deployment writers backed by one Postgres pool and owns the
// in-process pub/sub used by the SSE endpoints.
type Hub struct {
	pool *pgxpool.Pool
	log  *slog.Logger

	mu    sync.Mutex
	logBk map[string]*logEntry                 // per-deployment live log fan-out
	evtBk map[string]*broker[store.Deployment] // per-deployment status fan-out
}

// logEntry couples a log broker with a count of active writers, so the broker is
// only garbage-collected once nothing is producing to it and nobody is reading.
type logEntry struct {
	b       *broker[store.LogLine]
	writers int
}

func NewHub(pool *pgxpool.Pool, log *slog.Logger) *Hub {
	return &Hub{
		pool:  pool,
		log:   log,
		logBk: make(map[string]*logEntry),
		evtBk: make(map[string]*broker[store.Deployment]),
	}
}

// Open returns a Writer for a deployment, seeding its sequence counter from any
// lines already persisted (so resumed/retried runs don't collide) and attaching
// it to the deployment's live log broker.
func (h *Hub) Open(ctx context.Context, deploymentID string) *Writer {
	start, err := store.MaxLogSeq(ctx, h.pool, deploymentID)
	if err != nil {
		h.log.Warn("could not read max log seq; starting at 0", "deployment", deploymentID, "err", err)
		start = 0
	}

	h.mu.Lock()
	e := h.logBk[deploymentID]
	if e == nil {
		e = &logEntry{b: newBroker[store.LogLine]()}
		h.logBk[deploymentID] = e
	}
	e.writers++
	bk := e.b
	h.mu.Unlock()

	w := &Writer{
		hub:    h,
		broker: bk,
		pool:   h.pool,
		log:    h.log,
		depID:  deploymentID,
		seq:    start,
		sig:    make(chan struct{}, 1),
		done:   make(chan struct{}),
	}
	w.wg.Add(1)
	go w.loop()
	return w
}

// closeWriter is called when a Writer is done; it decrements the writer count and
// GCs the log broker if nothing is producing or consuming.
func (h *Hub) closeWriter(deploymentID string) {
	h.mu.Lock()
	if e := h.logBk[deploymentID]; e != nil {
		e.writers--
		if e.writers <= 0 && e.b.len() == 0 {
			delete(h.logBk, deploymentID)
		}
	}
	h.mu.Unlock()
}

// LogSub is a live subscription to one deployment's log stream.
type LogSub struct {
	C       <-chan store.LogLine
	dropped <-chan struct{}
	closeFn func()
}

// Dropped is closed if the subscriber overflowed (SPEC.md §10: emit a gap event
// and disconnect).
func (s *LogSub) Dropped() <-chan struct{} { return s.dropped }

// Close unsubscribes. Safe to call once (typically via defer).
func (s *LogSub) Close() { s.closeFn() }

// SubscribeLogs attaches a live subscriber to a deployment's log stream. Callers
// should subscribe *before* reading the persisted backlog and then drop any
// replayed line whose seq they already sent, so no line is missed or duplicated.
func (h *Hub) SubscribeLogs(deploymentID string) *LogSub {
	h.mu.Lock()
	e := h.logBk[deploymentID]
	if e == nil {
		e = &logEntry{b: newBroker[store.LogLine]()}
		h.logBk[deploymentID] = e
	}
	bk := e.b
	h.mu.Unlock()

	s := bk.subscribe(LogSubBuffer)
	return &LogSub{
		C:       s.C,
		dropped: s.dropped,
		closeFn: func() {
			bk.unsubscribe(s)
			h.mu.Lock()
			if e := h.logBk[deploymentID]; e != nil && e.writers <= 0 && e.b.len() == 0 {
				delete(h.logBk, deploymentID)
			}
			h.mu.Unlock()
		},
	}
}

// StatusSub is a live subscription to one deployment's status changes.
type StatusSub struct {
	C       <-chan store.Deployment
	dropped <-chan struct{}
	closeFn func()
}

func (s *StatusSub) Dropped() <-chan struct{} { return s.dropped }
func (s *StatusSub) Close()                   { s.closeFn() }

// SubscribeStatus attaches a live subscriber to a deployment's status stream.
func (h *Hub) SubscribeStatus(deploymentID string) *StatusSub {
	h.mu.Lock()
	bk := h.evtBk[deploymentID]
	if bk == nil {
		bk = newBroker[store.Deployment]()
		h.evtBk[deploymentID] = bk
	}
	h.mu.Unlock()

	s := bk.subscribe(statusSubBuffer)
	return &StatusSub{
		C:       s.C,
		dropped: s.dropped,
		closeFn: func() {
			bk.unsubscribe(s)
			h.mu.Lock()
			if b := h.evtBk[deploymentID]; b != nil && b.len() == 0 {
				delete(h.evtBk, deploymentID)
			}
			h.mu.Unlock()
		},
	}
}

// PublishStatus fans a deployment's current state out to any live status
// subscribers. It is a no-op when nobody is watching.
func (h *Hub) PublishStatus(deploymentID string, dep store.Deployment) {
	h.mu.Lock()
	bk := h.evtBk[deploymentID]
	h.mu.Unlock()
	if bk != nil {
		bk.publish(dep)
	}
}

// Writer buffers log lines for one deployment: it flushes them to Postgres in
// batches (100 lines or every 200ms, SPEC.md §10) and simultaneously fans each
// line out to live subscribers.
type Writer struct {
	hub    *Hub
	broker *broker[store.LogLine]
	pool   *pgxpool.Pool
	log    *slog.Logger
	depID  string

	mu  sync.Mutex
	seq int64
	buf []store.LogLine

	sig  chan struct{}
	done chan struct{}
	wg   sync.WaitGroup
	once sync.Once
}

const batchSize = 100

// Line records one log line on the given stream (stdout|stderr|system|build). The
// line is assigned its sequence number, queued for batch persistence, and
// published to live subscribers under the same lock so subscribers observe lines
// in strict seq order.
func (w *Writer) Line(stream, text string) {
	w.mu.Lock()
	w.seq++
	ll := store.LogLine{Seq: w.seq, Stream: stream, Line: text, TS: time.Now()}
	w.buf = append(w.buf, ll)
	if w.broker != nil {
		w.broker.publish(ll)
	}
	big := len(w.buf) >= batchSize
	w.mu.Unlock()

	if big {
		select {
		case w.sig <- struct{}{}:
		default:
		}
	}
}

// System records a controld-generated status line.
func (w *Writer) System(text string) { w.Line("system", text) }

// StreamWriter returns an io.Writer that splits input into lines on the stream.
func (w *Writer) StreamWriter(stream string) io.Writer {
	return &streamWriter{w: w, stream: stream}
}

// Close flushes remaining lines, stops the background writer, and detaches from
// the hub's live broker.
func (w *Writer) Close() {
	w.once.Do(func() { close(w.done) })
	w.wg.Wait()
	if w.hub != nil {
		w.hub.closeWriter(w.depID)
	}
}

func (w *Writer) loop() {
	defer w.wg.Done()
	t := time.NewTicker(200 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-w.done:
			w.flush()
			return
		case <-w.sig:
			w.flush()
		case <-t.C:
			w.flush()
		}
	}
}

func (w *Writer) flush() {
	w.mu.Lock()
	if len(w.buf) == 0 {
		w.mu.Unlock()
		return
	}
	batch := w.buf
	w.buf = nil
	w.mu.Unlock()

	// Use a detached context so a canceled deploy still persists its final logs.
	if err := store.InsertLogBatch(context.Background(), w.pool, w.depID, batch); err != nil {
		w.log.Error("log flush failed", "deployment", w.depID, "lines", len(batch), "err", err)
	}
}

// streamWriter turns a byte stream (subprocess stdout/stderr, docker logs) into
// discrete lines. Each streamWriter owns its own partial-line buffer, so using
// separate ones for stdout/stderr is race-free.
type streamWriter struct {
	w      *Writer
	stream string
	buf    []byte
}

func (s *streamWriter) Write(p []byte) (int, error) {
	s.buf = append(s.buf, p...)
	for {
		i := bytes.IndexByte(s.buf, '\n')
		if i < 0 {
			break
		}
		line := strings.TrimRight(string(s.buf[:i]), "\r")
		s.w.Line(s.stream, line)
		s.buf = s.buf[i+1:]
	}
	return len(p), nil
}
