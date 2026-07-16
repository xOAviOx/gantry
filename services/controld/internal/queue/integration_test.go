//go:build integration

// Integration tests for the Postgres-backed queue (SPEC.md §7). Run with:
//
//	make it        # loads DATABASE_URL from deploy/.env
//	DATABASE_URL=... go test -tags=integration ./services/controld/internal/queue/
//
// They assume an otherwise-idle queue (controld not running) and clean up after
// themselves. Each test scopes its writes to a throwaway project or a unique job
// kind so it won't disturb real data.
package queue

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set; skipping queue integration test")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// newTestProject inserts a throwaway project and registers cleanup that removes
// its jobs (jobs aren't FK'd to projects) and the project itself (which cascades
// to its deployments).
func newTestProject(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	ctx := context.Background()
	slug := fmt.Sprintf("it-%d", time.Now().UnixNano())
	var id string
	err := pool.QueryRow(ctx, `
		INSERT INTO projects (name, slug, repo_url, port) VALUES ($1,$1,'/tmp/x',3000)
		RETURNING id::text`, slug).Scan(&id)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM jobs WHERE payload->>'project_id' = $1`, id)
		_, _ = pool.Exec(c, `DELETE FROM projects WHERE id = $1`, id)
	})
	return id
}

func newTestDeployment(t *testing.T, pool *pgxpool.Pool, projectID string) string {
	t.Helper()
	var id string
	err := pool.QueryRow(context.Background(), `
		INSERT INTO deployments (project_id, trigger, status) VALUES ($1,'manual','queued')
		RETURNING id::text`, projectID).Scan(&id)
	if err != nil {
		t.Fatalf("create deployment: %v", err)
	}
	return id
}

func jobStatus(t *testing.T, pool *pgxpool.Pool, id int64) (status string, cancelReq bool) {
	t.Helper()
	if err := pool.QueryRow(context.Background(),
		`SELECT status, cancel_requested FROM jobs WHERE id=$1`, id).Scan(&status, &cancelReq); err != nil {
		t.Fatalf("read job %d: %v", id, err)
	}
	return
}

func deploymentStatus(t *testing.T, pool *pgxpool.Pool, id string) string {
	t.Helper()
	var s string
	if err := pool.QueryRow(context.Background(),
		`SELECT status FROM deployments WHERE id=$1`, id).Scan(&s); err != nil {
		t.Fatalf("read deployment %s: %v", id, err)
	}
	return s
}

// Many workers claiming the same queue must each get a distinct job — no job is
// ever handed out twice (FOR UPDATE SKIP LOCKED).
func TestConcurrentClaimNoDoubleClaim(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	kind := fmt.Sprintf("it_claim_%d", time.Now().UnixNano())
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM jobs WHERE kind=$1`, kind) })

	const n = 25
	mine := map[int64]bool{}
	for i := 0; i < n; i++ {
		var id int64
		if err := pool.QueryRow(ctx,
			`INSERT INTO jobs (kind, payload) VALUES ($1, '{}'::jsonb) RETURNING id`, kind).Scan(&id); err != nil {
			t.Fatal(err)
		}
		mine[id] = true
	}

	var claimedMine int64
	var mu sync.Mutex
	counts := map[int64]int{}

	var wg sync.WaitGroup
	for g := 0; g < 6; g++ {
		wg.Add(1)
		go func(wid int) {
			defer wg.Done()
			id := fmt.Sprintf("itw%d", wid)
			for atomic.LoadInt64(&claimedMine) < n {
				j, err := Claim(ctx, pool, id)
				if err != nil {
					t.Errorf("claim: %v", err)
					return
				}
				if j == nil {
					time.Sleep(5 * time.Millisecond)
					continue
				}
				if !mine[j.ID] {
					// A stray job from elsewhere; put it back and move on.
					_, _ = pool.Exec(ctx, `UPDATE jobs SET status='queued', locked_by=NULL, locked_at=NULL, attempts=attempts-1 WHERE id=$1`, j.ID)
					continue
				}
				mu.Lock()
				counts[j.ID]++
				mu.Unlock()
				atomic.AddInt64(&claimedMine, 1)
			}
		}(g)
	}
	wg.Wait()

	if len(counts) != n {
		t.Fatalf("claimed %d distinct jobs, want %d", len(counts), n)
	}
	for id, c := range counts {
		if c != 1 {
			t.Fatalf("job %d claimed %d times, want exactly 1", id, c)
		}
	}
}

// Enqueuing a newer deploy supersedes a queued one (job + deployment) and asks a
// running one to cancel — "newest push always wins."
func TestEnqueueDeploySupersedes(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	proj := newTestProject(t, pool)

	d1 := newTestDeployment(t, pool, proj)
	j1, err := EnqueueDeploy(ctx, pool, DeployJob{DeploymentID: d1, ProjectID: proj}, EnqueueOpts{})
	if err != nil {
		t.Fatal(err)
	}

	d2 := newTestDeployment(t, pool, proj)
	j2, err := EnqueueDeploy(ctx, pool, DeployJob{DeploymentID: d2, ProjectID: proj}, EnqueueOpts{})
	if err != nil {
		t.Fatal(err)
	}

	if s, _ := jobStatus(t, pool, j1); s != "superseded" {
		t.Fatalf("j1 status = %q, want superseded", s)
	}
	if s := deploymentStatus(t, pool, d1); s != "superseded" {
		t.Fatalf("d1 status = %q, want superseded", s)
	}
	if s, _ := jobStatus(t, pool, j2); s != "queued" {
		t.Fatalf("j2 status = %q, want queued", s)
	}

	// Simulate j2 now running, then a third deploy: j2 must be flagged to cancel.
	if _, err := pool.Exec(ctx, `UPDATE jobs SET status='running', locked_at=now() WHERE id=$1`, j2); err != nil {
		t.Fatal(err)
	}
	d3 := newTestDeployment(t, pool, proj)
	if _, err := EnqueueDeploy(ctx, pool, DeployJob{DeploymentID: d3, ProjectID: proj}, EnqueueOpts{}); err != nil {
		t.Fatal(err)
	}
	if s, cancel := jobStatus(t, pool, j2); !cancel || s != "running" {
		t.Fatalf("j2 status=%q cancel_requested=%v, want running + cancel_requested", s, cancel)
	}
}

// The reaper requeues stale running jobs with retries left, fails exhausted ones,
// leaves fresh jobs alone, and never resurrects a job being canceled.
func TestReaperRequeuesStale(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	kind := fmt.Sprintf("it_reap_%d", time.Now().UnixNano())
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM jobs WHERE kind=$1`, kind) })

	ins := func(status string, staleSecs, attempts, maxAttempts int, cancel bool) int64 {
		var id int64
		err := pool.QueryRow(ctx, `
			INSERT INTO jobs (kind, payload, status, attempts, max_attempts, cancel_requested, locked_at)
			VALUES ($1,'{}'::jsonb,$2,$3,$4,$5, now() - make_interval(secs => $6))
			RETURNING id`, kind, status, attempts, maxAttempts, cancel, staleSecs).Scan(&id)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}

	staleRetryable := ins("running", 120, 1, 2, false) // → requeued
	staleExhausted := ins("running", 120, 2, 2, false) // → failed
	fresh := ins("running", 5, 0, 2, false)            // → untouched
	staleCanceling := ins("running", 120, 0, 2, true)  // → untouched (being canceled)

	requeued, failed, err := Reap(ctx, pool, 60*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if requeued < 1 || failed < 1 {
		t.Fatalf("reap counts requeued=%d failed=%d, want >=1 each", requeued, failed)
	}

	if s, _ := jobStatus(t, pool, staleRetryable); s != "queued" {
		t.Fatalf("stale retryable = %q, want queued", s)
	}
	if s, _ := jobStatus(t, pool, staleExhausted); s != "failed" {
		t.Fatalf("stale exhausted = %q, want failed", s)
	}
	if s, _ := jobStatus(t, pool, fresh); s != "running" {
		t.Fatalf("fresh job = %q, want running (untouched)", s)
	}
	if s, _ := jobStatus(t, pool, staleCanceling); s != "running" {
		t.Fatalf("canceling job = %q, want running (untouched)", s)
	}
}
