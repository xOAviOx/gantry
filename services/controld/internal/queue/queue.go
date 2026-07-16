// Package queue is the Postgres-backed job queue (SPEC.md §7). M1 implements
// enqueue + FOR UPDATE SKIP LOCKED claim + complete/fail with backoff. Advisory-
// lock serialization, supersession, heartbeats and the reaper arrive in M3.
package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Job is a claimed unit of work.
type Job struct {
	ID              int64
	Kind            string
	Payload         json.RawMessage
	Status          string
	Priority        int
	Attempts        int
	MaxAttempts     int
	CancelRequested bool
}

// EnqueueOpts are optional knobs for Enqueue.
type EnqueueOpts struct {
	DedupeKey string // unique; second insert with same key is a no-op (webhook dedupe)
	Priority  int
	RunAfter  time.Time
}

func marshalPayload(v any) (json.RawMessage, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}
	return raw, nil
}

// Enqueue inserts a queued job. Returns the new job id, or 0 if a dedupe_key
// collision meant nothing was inserted.
func Enqueue(ctx context.Context, pool *pgxpool.Pool, kind string, payload any, opts EnqueueOpts) (int64, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return 0, fmt.Errorf("marshal payload: %w", err)
	}

	runAfter := opts.RunAfter
	if runAfter.IsZero() {
		runAfter = time.Now()
	}
	var dedupe *string
	if opts.DedupeKey != "" {
		dedupe = &opts.DedupeKey
	}

	const sql = `
		INSERT INTO jobs (kind, payload, priority, run_after, dedupe_key)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (dedupe_key) DO NOTHING
		RETURNING id`
	var id int64
	err = pool.QueryRow(ctx, sql, kind, raw, opts.Priority, runAfter, dedupe).Scan(&id)
	if err == pgx.ErrNoRows {
		return 0, nil // deduped
	}
	if err != nil {
		return 0, fmt.Errorf("enqueue: %w", err)
	}
	return id, nil
}

// Claim atomically grabs the highest-priority ready job for this worker.
// Returns (nil, nil) when the queue is empty.
func Claim(ctx context.Context, pool *pgxpool.Pool, workerID string) (*Job, error) {
	const sql = `
		UPDATE jobs SET status='running', locked_by=$1, locked_at=now(), attempts=attempts+1
		WHERE id = (
			SELECT id FROM jobs
			WHERE status='queued' AND run_after <= now()
			ORDER BY priority DESC, id
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		RETURNING id, kind, payload, status, priority, attempts, max_attempts, cancel_requested`

	var j Job
	err := pool.QueryRow(ctx, sql, workerID).Scan(
		&j.ID, &j.Kind, &j.Payload, &j.Status, &j.Priority, &j.Attempts, &j.MaxAttempts, &j.CancelRequested)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("claim: %w", err)
	}
	return &j, nil
}

// Complete marks a job done.
func Complete(ctx context.Context, pool *pgxpool.Pool, id int64) error {
	_, err := pool.Exec(ctx, `UPDATE jobs SET status='done' WHERE id=$1`, id)
	return err
}

// Fail either requeues with exponential backoff (attempts left) or marks failed.
func Fail(ctx context.Context, pool *pgxpool.Pool, j *Job, cause error) error {
	if j.Attempts < j.MaxAttempts {
		backoff := time.Duration(1<<uint(j.Attempts)) * 5 * time.Second // 5s,10s,20s...
		_, err := pool.Exec(ctx,
			`UPDATE jobs SET status='queued', locked_by=NULL, locked_at=NULL, run_after=now()+$2 WHERE id=$1`,
			j.ID, backoff)
		return err
	}
	_, err := pool.Exec(ctx, `UPDATE jobs SET status='failed' WHERE id=$1`, j.ID)
	return err
}
