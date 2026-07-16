package queue

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Reap requeues or fails jobs whose worker died mid-flight: a job still marked
// running whose lock hasn't been refreshed within staleAfter (SPEC.md §7). Jobs
// with retries left go back to queued; exhausted ones are failed. Jobs being
// cooperatively canceled are left alone. Returns how many were requeued/failed.
func Reap(ctx context.Context, pool *pgxpool.Pool, staleAfter time.Duration) (requeued, failed int64, err error) {
	secs := staleAfter.Seconds()

	rtag, err := pool.Exec(ctx, `
		UPDATE jobs SET status='queued', locked_by=NULL, locked_at=NULL, run_after=now()
		WHERE status='running' AND cancel_requested=false
		  AND locked_at < now() - make_interval(secs => $1)
		  AND attempts < max_attempts`, secs)
	if err != nil {
		return 0, 0, fmt.Errorf("reaper requeue: %w", err)
	}

	ftag, err := pool.Exec(ctx, `
		UPDATE jobs SET status='failed', locked_by=NULL, locked_at=NULL
		WHERE status='running'
		  AND locked_at < now() - make_interval(secs => $1)
		  AND attempts >= max_attempts`, secs)
	if err != nil {
		return rtag.RowsAffected(), 0, fmt.Errorf("reaper fail: %w", err)
	}

	return rtag.RowsAffected(), ftag.RowsAffected(), nil
}

// RunReaper sweeps the queue every interval until ctx is canceled.
func RunReaper(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger, interval, staleAfter time.Duration) {
	log.Info("reaper starting", "interval", interval, "stale_after", staleAfter)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			requeued, failed, err := Reap(ctx, pool, staleAfter)
			if err != nil {
				if ctx.Err() == nil {
					log.Error("reaper sweep failed", "err", err)
				}
				continue
			}
			if requeued > 0 || failed > 0 {
				log.Warn("reaper swept stale jobs", "requeued", requeued, "failed", failed, "reconciler", true)
			}
		}
	}
}
