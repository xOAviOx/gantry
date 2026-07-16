package queue

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Cancellation causes. A worker's job context is torn down with one of these as
// its cause so the deploy pipeline can pick the right terminal status
// (superseded vs canceled) — see orchestrator fail handling and SPEC.md §7/§8.
var (
	ErrSuperseded = errors.New("superseded by a newer deploy")
	ErrCanceled   = errors.New("canceled")
)

// Cancel reasons persisted in jobs.cancel_reason.
const (
	ReasonSuperseded = "superseded"
	ReasonCanceled   = "canceled"
)

// DeployJob is the payload of a build_deploy job. project_id is carried so the
// queue can serialize and supersede per project without touching the payload's
// deployment to look it up.
type DeployJob struct {
	DeploymentID string `json:"deployment_id"`
	ProjectID    string `json:"project_id"`
	SkipBuild    bool   `json:"skip_build"`
}

// EnqueueDeploy supersedes a project's in-flight build_deploy work and enqueues a
// new one, atomically (SPEC.md §7: "newest push always wins"). Any *queued*
// build_deploy jobs for the project are marked superseded (and their deployments
// with them); a *running* one is flagged cancel_requested so its worker stops.
// Returns the new job id, or 0 if a dedupe_key collision meant nothing was added.
func EnqueueDeploy(ctx context.Context, pool *pgxpool.Pool, dj DeployJob, opts EnqueueOpts) (int64, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Supersede queued jobs for this project and their (still-queued) deployments.
	if _, err := tx.Exec(ctx, `
		WITH bumped AS (
			UPDATE jobs SET status='superseded'
			WHERE kind='build_deploy' AND status='queued' AND payload->>'project_id'=$1
			RETURNING payload->>'deployment_id' AS deployment_id
		)
		UPDATE deployments SET status='superseded', finished_at=now()
		WHERE id::text IN (SELECT deployment_id FROM bumped)
		  AND status NOT IN ('live','retired')`, dj.ProjectID); err != nil {
		return 0, fmt.Errorf("supersede queued: %w", err)
	}

	// Ask any running job for this project to cancel (cooperative; worker polls).
	if _, err := tx.Exec(ctx, `
		UPDATE jobs SET cancel_requested=true, cancel_reason=$2
		WHERE kind='build_deploy' AND status='running'
		  AND payload->>'project_id'=$1 AND cancel_requested=false`,
		dj.ProjectID, ReasonSuperseded); err != nil {
		return 0, fmt.Errorf("mark running cancel: %w", err)
	}

	raw, err := marshalPayload(dj)
	if err != nil {
		return 0, err
	}
	runAfter := opts.RunAfter
	if runAfter.IsZero() {
		runAfter = time.Now()
	}
	var dedupe *string
	if opts.DedupeKey != "" {
		dedupe = &opts.DedupeKey
	}

	var id int64
	err = tx.QueryRow(ctx, `
		INSERT INTO jobs (kind, payload, priority, run_after, dedupe_key)
		VALUES ('build_deploy', $1, $2, $3, $4)
		ON CONFLICT (dedupe_key) DO NOTHING
		RETURNING id`, raw, opts.Priority, runAfter, dedupe).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		// Deduped: still commit the supersession side effects.
		if cErr := tx.Commit(ctx); cErr != nil {
			return 0, cErr
		}
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("insert build_deploy: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit enqueue deploy: %w", err)
	}
	return id, nil
}

// ProjectLock is a held per-project advisory lock backed by a dedicated
// connection. Release must be called (typically via defer) to unlock and return
// the connection to the pool.
type ProjectLock struct {
	conn *pgxpool.Conn
	key  string
}

// AcquireProjectLock tries to take the project's advisory lock without blocking.
// ok=false means another worker holds it (the caller should requeue). The lock is
// session-scoped, so it is held for as long as the returned *ProjectLock lives.
func AcquireProjectLock(ctx context.Context, pool *pgxpool.Pool, projectID string) (*ProjectLock, bool, error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("acquire conn: %w", err)
	}
	key := "gantry:project:" + projectID
	var got bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtext($1))`, key).Scan(&got); err != nil {
		conn.Release()
		return nil, false, fmt.Errorf("try advisory lock: %w", err)
	}
	if !got {
		conn.Release()
		return nil, false, nil
	}
	return &ProjectLock{conn: conn, key: key}, true, nil
}

// Release unlocks the advisory lock and returns the connection. Safe once.
func (l *ProjectLock) Release() {
	if l == nil || l.conn == nil {
		return
	}
	// Use a short, detached context: unlock must run even if the job's ctx is done.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = l.conn.Exec(ctx, `SELECT pg_advisory_unlock(hashtext($1))`, l.key)
	l.conn.Release()
	l.conn = nil
}

// RequeueForLock returns a claimed job to the queue after N and undoes the claim's
// attempt increment — lock contention is not a real attempt (SPEC.md §7).
func RequeueForLock(ctx context.Context, pool *pgxpool.Pool, j *Job, delay time.Duration) error {
	_, err := pool.Exec(ctx, `
		UPDATE jobs SET status='queued', locked_by=NULL, locked_at=NULL,
			run_after=now()+$2, attempts=GREATEST(attempts-1, 0)
		WHERE id=$1`, j.ID, delay)
	return err
}

// Heartbeat refreshes a running job's lock so the reaper doesn't consider it stale.
func Heartbeat(ctx context.Context, pool *pgxpool.Pool, id int64) error {
	_, err := pool.Exec(ctx, `UPDATE jobs SET locked_at=now() WHERE id=$1 AND status='running'`, id)
	return err
}

// PollCancel reports whether a cancel has been requested for the job, and why.
func PollCancel(ctx context.Context, pool *pgxpool.Pool, id int64) (requested bool, reason string, err error) {
	err = pool.QueryRow(ctx, `SELECT cancel_requested, cancel_reason FROM jobs WHERE id=$1`, id).
		Scan(&requested, &reason)
	return requested, reason, err
}

// MarkStopped sets a terminal, non-retryable status on a job (canceled |
// superseded) and clears its lock.
func MarkStopped(ctx context.Context, pool *pgxpool.Pool, id int64, status string) error {
	_, err := pool.Exec(ctx, `UPDATE jobs SET status=$2, locked_by=NULL, locked_at=NULL WHERE id=$1`, id, status)
	return err
}

// RequestCancel cooperatively cancels a deployment's build_deploy job. A running
// job is flagged (its worker stops within the poll interval); a still-queued job
// is finalized immediately along with its deployment. Returns true if a matching
// active job was found.
func RequestCancel(ctx context.Context, pool *pgxpool.Pool, deploymentID string) (bool, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Queued: cancel outright (no worker will run it) and cancel the deployment.
	qtag, err := tx.Exec(ctx, `
		UPDATE jobs SET status='canceled', locked_by=NULL, locked_at=NULL,
			cancel_requested=true, cancel_reason=$2
		WHERE kind='build_deploy' AND status='queued' AND payload->>'deployment_id'=$1`,
		deploymentID, ReasonCanceled)
	if err != nil {
		return false, err
	}
	if qtag.RowsAffected() > 0 {
		if _, err := tx.Exec(ctx, `
			UPDATE deployments SET status='canceled', finished_at=now()
			WHERE id=$1 AND status NOT IN ('live','retired')`, deploymentID); err != nil {
			return false, err
		}
	}

	// Running: flag for cooperative cancel.
	rtag, err := tx.Exec(ctx, `
		UPDATE jobs SET cancel_requested=true, cancel_reason=$2
		WHERE kind='build_deploy' AND status='running' AND payload->>'deployment_id'=$1`,
		deploymentID, ReasonCanceled)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return qtag.RowsAffected()+rtag.RowsAffected() > 0, nil
}
