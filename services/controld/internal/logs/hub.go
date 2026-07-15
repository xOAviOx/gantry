// Package logs is the build/deploy log pipeline: a hub hands out per-deployment
// writers that assign sequence numbers, batch-persist to log_lines, and (from M2)
// fan out to live SSE subscribers.
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

// Sink accepts log lines for a single deployment.
type Sink interface {
	Line(stream, text string)
	System(text string)
	StreamWriter(stream string) io.Writer
}

// Hub creates per-deployment writers backed by one Postgres pool.
type Hub struct {
	pool *pgxpool.Pool
	log  *slog.Logger
}

func NewHub(pool *pgxpool.Pool, log *slog.Logger) *Hub {
	return &Hub{pool: pool, log: log}
}

// Open returns a Writer for a deployment, seeding its sequence counter from any
// lines already persisted (so resumed/retried runs don't collide).
func (h *Hub) Open(ctx context.Context, deploymentID string) *Writer {
	start, err := store.MaxLogSeq(ctx, h.pool, deploymentID)
	if err != nil {
		h.log.Warn("could not read max log seq; starting at 0", "deployment", deploymentID, "err", err)
		start = 0
	}
	w := &Writer{
		pool:  h.pool,
		log:   h.log,
		depID: deploymentID,
		seq:   start,
		sig:   make(chan struct{}, 1),
		done:  make(chan struct{}),
	}
	w.wg.Add(1)
	go w.loop()
	return w
}

// Writer buffers log lines for one deployment and flushes them in batches
// (100 lines or every 200ms), matching SPEC.md §10.
type Writer struct {
	pool  *pgxpool.Pool
	log   *slog.Logger
	depID string

	mu  sync.Mutex
	seq int64
	buf []store.LogLine

	sig  chan struct{}
	done chan struct{}
	wg   sync.WaitGroup
	once sync.Once
}

const batchSize = 100

// Line records one log line on the given stream (stdout|stderr|system|build).
func (w *Writer) Line(stream, text string) {
	w.mu.Lock()
	w.seq++
	w.buf = append(w.buf, store.LogLine{Seq: w.seq, Stream: stream, Line: text, TS: time.Now()})
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

// Close flushes remaining lines and stops the background writer.
func (w *Writer) Close() {
	w.once.Do(func() { close(w.done) })
	w.wg.Wait()
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
