package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// InsertLogBatch bulk-inserts log lines via COPY.
func InsertLogBatch(ctx context.Context, pool *pgxpool.Pool, deploymentID string, lines []LogLine) error {
	if len(lines) == 0 {
		return nil
	}
	_, err := pool.CopyFrom(ctx,
		pgx.Identifier{"log_lines"},
		[]string{"deployment_id", "seq", "stream", "line", "ts"},
		pgx.CopyFromSlice(len(lines), func(i int) ([]any, error) {
			l := lines[i]
			return []any{deploymentID, l.Seq, l.Stream, l.Line, l.TS}, nil
		}),
	)
	if err != nil {
		return fmt.Errorf("insert log batch: %w", err)
	}
	return nil
}

// ListLogLines returns lines for a deployment with seq greater than afterSeq.
func ListLogLines(ctx context.Context, q DBTX, deploymentID string, afterSeq int64) ([]LogLine, error) {
	const sql = `SELECT seq, stream, line, ts FROM log_lines
		WHERE deployment_id = $1 AND seq > $2 ORDER BY seq`
	rows, err := q.Query(ctx, sql, deploymentID, afterSeq)
	if err != nil {
		return nil, fmt.Errorf("list log lines: %w", err)
	}
	defer rows.Close()

	var out []LogLine
	for rows.Next() {
		var l LogLine
		if err := rows.Scan(&l.Seq, &l.Stream, &l.Line, &l.TS); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// MaxLogSeq returns the highest seq persisted for a deployment (0 if none).
func MaxLogSeq(ctx context.Context, q DBTX, deploymentID string) (int64, error) {
	var seq int64
	err := q.QueryRow(ctx, `SELECT COALESCE(MAX(seq), 0) FROM log_lines WHERE deployment_id = $1`, deploymentID).Scan(&seq)
	if err != nil {
		return 0, fmt.Errorf("max log seq: %w", err)
	}
	return seq, nil
}
