package queue

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Handler processes a claimed job. Returning an error triggers Fail (backoff or
// terminal). A deploy that "fails" the app records its outcome on the deployment
// row and returns nil here — the job itself succeeded.
type Handler func(ctx context.Context, j *Job) error

// Pool runs N workers that poll for jobs and dispatch to registered handlers.
type Pool struct {
	pool     *pgxpool.Pool
	log      *slog.Logger
	workers  int
	id       string
	handlers map[string]Handler

	// Heartbeat is how often a running job refreshes its lock (default 15s).
	Heartbeat time.Duration
	// LockRetryDelay is how long a job waits before retry when its project lock is
	// contended (default 10s).
	LockRetryDelay time.Duration
	// CancelPoll is how often a running job checks its cancel flag (default 2s).
	CancelPoll time.Duration
	// SerializeKey returns a per-job serialization key (e.g. project id) for jobs
	// that must not run concurrently; "" means no serialization. Optional.
	SerializeKey func(*Job) string
}

const (
	defaultHeartbeat  = 15 * time.Second
	defaultLockRetry  = 10 * time.Second
	defaultCancelPoll = 2 * time.Second
)

func NewPool(pool *pgxpool.Pool, log *slog.Logger, workers int, id string) *Pool {
	if workers < 1 {
		workers = 1
	}
	return &Pool{pool: pool, log: log, workers: workers, id: id, handlers: map[string]Handler{}}
}

// Register wires a handler for a job kind. Call before Run.
func (p *Pool) Register(kind string, h Handler) { p.handlers[kind] = h }

// Run starts the workers and blocks until ctx is canceled.
func (p *Pool) Run(ctx context.Context) {
	p.log.Info("queue workers starting", "workers", p.workers, "id", p.id)
	done := make(chan struct{})
	for i := 0; i < p.workers; i++ {
		wid := fmt.Sprintf("%s-w%d", p.id, i)
		go func() {
			p.worker(ctx, wid)
			done <- struct{}{}
		}()
	}
	for i := 0; i < p.workers; i++ {
		<-done
	}
	p.log.Info("queue workers stopped")
}

func (p *Pool) worker(ctx context.Context, wid string) {
	base := 500 * time.Millisecond
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		job, err := Claim(ctx, p.pool, wid)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			p.log.Error("claim failed", "worker", wid, "err", err)
			sleepJitter(ctx, base)
			continue
		}
		if job == nil {
			sleepJitter(ctx, base)
			continue
		}

		p.dispatch(ctx, wid, job)
	}
}

func (p *Pool) dispatch(ctx context.Context, wid string, job *Job) {
	h, ok := p.handlers[job.Kind]
	if !ok {
		p.log.Error("no handler for job kind", "kind", job.Kind, "job", job.ID)
		_ = Fail(ctx, p.pool, job, fmt.Errorf("no handler for kind %q", job.Kind))
		return
	}

	log := p.log.With("worker", wid, "job", job.ID, "kind", job.Kind, "attempt", job.Attempts)

	// Per-project serialization: hold an advisory lock for the job's duration so
	// two deploys for the same project never run at once (SPEC.md §7). On
	// contention, requeue after a delay (not counted as an attempt).
	if p.SerializeKey != nil {
		if key := p.SerializeKey(job); key != "" {
			lock, got, err := AcquireProjectLock(ctx, p.pool, key)
			if err != nil {
				log.Error("acquire project lock", "err", err)
				_ = RequeueForLock(context.WithoutCancel(ctx), p.pool, job, p.lockRetry())
				return
			}
			if !got {
				log.Info("project busy; requeued", "key", key)
				_ = RequeueForLock(context.WithoutCancel(ctx), p.pool, job, p.lockRetry())
				return
			}
			defer lock.Release()
		}
	}

	log.Info("job claimed")

	// Per-job context whose cancellation cause tells the pipeline why it stopped.
	jobCtx, cancelCause := context.WithCancelCause(ctx)
	defer cancelCause(nil)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go p.heartbeatLoop(jobCtx, job.ID, stop, &wg)
	go p.cancelLoop(jobCtx, job.ID, cancelCause, stop, &wg)

	err := runHandler(h, jobCtx, job)

	close(stop)
	wg.Wait()

	// Finalize with a detached context so shutdown can't strand the row.
	fctx := context.WithoutCancel(ctx)
	switch cause := context.Cause(jobCtx); {
	case errors.Is(cause, ErrSuperseded):
		if e := MarkStopped(fctx, p.pool, job.ID, "superseded"); e != nil {
			log.Error("mark superseded", "err", e)
		}
		log.Info("job superseded")
	case errors.Is(cause, ErrCanceled):
		if e := MarkStopped(fctx, p.pool, job.ID, "canceled"); e != nil {
			log.Error("mark canceled", "err", e)
		}
		log.Info("job canceled")
	case err != nil:
		log.Error("job failed", "err", err)
		if ferr := Fail(fctx, p.pool, job, err); ferr != nil {
			log.Error("marking job failed also failed", "err", ferr)
		}
	default:
		if cerr := Complete(fctx, p.pool, job.ID); cerr != nil {
			log.Error("completing job failed", "err", cerr)
			return
		}
		log.Info("job done")
	}
}

// heartbeatLoop refreshes the job's lock until the job ends or its context is
// canceled, so the reaper won't reclaim a job that's still making progress.
func (p *Pool) heartbeatLoop(ctx context.Context, id int64, stop <-chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	t := time.NewTicker(p.heartbeat())
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ctx.Done():
			return
		case <-t.C:
			if err := Heartbeat(ctx, p.pool, id); err != nil && ctx.Err() == nil {
				p.log.Warn("heartbeat failed", "job", id, "err", err)
			}
		}
	}
}

// cancelLoop watches for a cancel request and, on seeing one, cancels the job's
// context with the matching cause (superseded vs canceled).
func (p *Pool) cancelLoop(ctx context.Context, id int64, cancelCause context.CancelCauseFunc, stop <-chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	t := time.NewTicker(p.cancelPoll())
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ctx.Done():
			return
		case <-t.C:
			req, reason, err := PollCancel(ctx, p.pool, id)
			if err != nil {
				continue
			}
			if req {
				if reason == ReasonCanceled {
					cancelCause(ErrCanceled)
				} else {
					cancelCause(ErrSuperseded)
				}
				return
			}
		}
	}
}

func runHandler(h Handler, ctx context.Context, job *Job) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic in handler: %v", r)
		}
	}()
	return h(ctx, job)
}

func (p *Pool) heartbeat() time.Duration {
	if p.Heartbeat > 0 {
		return p.Heartbeat
	}
	return defaultHeartbeat
}

func (p *Pool) lockRetry() time.Duration {
	if p.LockRetryDelay > 0 {
		return p.LockRetryDelay
	}
	return defaultLockRetry
}

func (p *Pool) cancelPoll() time.Duration {
	if p.CancelPoll > 0 {
		return p.CancelPoll
	}
	return defaultCancelPoll
}

func sleepJitter(ctx context.Context, base time.Duration) {
	d := base + time.Duration(rand.Int63n(int64(base/2)+1))
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
