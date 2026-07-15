package queue

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
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
}

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
	log.Info("job claimed")

	err := func() (err error) {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("panic in handler: %v", r)
			}
		}()
		return h(ctx, job)
	}()

	if err != nil {
		log.Error("job failed", "err", err)
		if ferr := Fail(context.WithoutCancel(ctx), p.pool, job, err); ferr != nil {
			log.Error("marking job failed also failed", "err", ferr)
		}
		return
	}
	if cerr := Complete(context.WithoutCancel(ctx), p.pool, job.ID); cerr != nil {
		log.Error("completing job failed", "err", cerr)
		return
	}
	log.Info("job done")
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
