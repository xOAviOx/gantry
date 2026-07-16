package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ImageTagsToKeep returns the set of image tags GC must retain: for each project,
// the images of its keepN most recent deployments, plus any live deployment's
// image (SPEC.md §14). Everything else that is gantry-labeled can be pruned.
func ImageTagsToKeep(ctx context.Context, pool *pgxpool.Pool, keepN int) (map[string]bool, error) {
	if keepN < 1 {
		keepN = 1
	}
	const q = `
		WITH ranked AS (
			SELECT project_id, image_tag, status,
			       row_number() OVER (PARTITION BY project_id ORDER BY created_at DESC) AS rn
			FROM deployments
			WHERE image_tag <> ''
		)
		SELECT DISTINCT image_tag FROM ranked WHERE rn <= $1 OR status = 'live'`
	rows, err := pool.Query(ctx, q, keepN)
	if err != nil {
		return nil, fmt.Errorf("image tags to keep: %w", err)
	}
	defer rows.Close()

	keep := make(map[string]bool)
	for rows.Next() {
		var tag string
		if err := rows.Scan(&tag); err != nil {
			return nil, err
		}
		keep[tag] = true
	}
	return keep, rows.Err()
}

// PurgeOldLogs deletes log lines older than the retention window, returning the
// number of rows removed.
func PurgeOldLogs(ctx context.Context, pool *pgxpool.Pool, olderThan time.Duration) (int64, error) {
	tag, err := pool.Exec(ctx,
		`DELETE FROM log_lines WHERE ts < now() - make_interval(secs => $1)`,
		olderThan.Seconds())
	if err != nil {
		return 0, fmt.Errorf("purge old logs: %w", err)
	}
	return tag.RowsAffected(), nil
}
