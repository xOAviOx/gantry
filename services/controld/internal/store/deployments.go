package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const deployCols = `id::text, project_id::text, commit_sha, commit_message, trigger, status,
	image_tag, container_name, host_port, error, created_at, started_at, finished_at`

func scanDeployment(row pgx.Row) (Deployment, error) {
	var d Deployment
	err := row.Scan(&d.ID, &d.ProjectID, &d.CommitSHA, &d.CommitMessage, &d.Trigger, &d.Status,
		&d.ImageTag, &d.ContainerName, &d.HostPort, &d.Error, &d.CreatedAt, &d.StartedAt, &d.FinishedAt)
	return d, err
}

// CreateDeployment inserts a deployment (typically status=queued).
func CreateDeployment(ctx context.Context, q DBTX, d Deployment) (Deployment, error) {
	const sql = `INSERT INTO deployments (project_id, commit_sha, commit_message, trigger, status, image_tag)
		VALUES ($1,$2,$3,$4,$5,$6)
		RETURNING ` + deployCols
	got, err := scanDeployment(q.QueryRow(ctx, sql, d.ProjectID, d.CommitSHA, d.CommitMessage, d.Trigger, d.Status, d.ImageTag))
	if err != nil {
		return Deployment{}, fmt.Errorf("create deployment: %w", err)
	}
	return got, nil
}

// GetDeployment returns one deployment by id, or ErrNotFound.
func GetDeployment(ctx context.Context, q DBTX, id string) (Deployment, error) {
	const sql = `SELECT ` + deployCols + ` FROM deployments WHERE id = $1`
	d, err := scanDeployment(q.QueryRow(ctx, sql, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Deployment{}, ErrNotFound
	}
	if err != nil {
		return Deployment{}, fmt.Errorf("get deployment: %w", err)
	}
	return d, nil
}

// ListDeploymentsByProject returns a project's deployments, newest first.
func ListDeploymentsByProject(ctx context.Context, q DBTX, projectID string) ([]Deployment, error) {
	const sql = `SELECT ` + deployCols + ` FROM deployments WHERE project_id = $1 ORDER BY created_at DESC`
	rows, err := q.Query(ctx, sql, projectID)
	if err != nil {
		return nil, fmt.Errorf("list deployments: %w", err)
	}
	defer rows.Close()

	var out []Deployment
	for rows.Next() {
		d, err := scanDeployment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// GetLiveDeployment returns the project's live deployment, or (nil, nil) if none.
func GetLiveDeployment(ctx context.Context, q DBTX, projectID string) (*Deployment, error) {
	const sql = `SELECT ` + deployCols + ` FROM deployments
		WHERE project_id = $1 AND status = 'live' ORDER BY created_at DESC LIMIT 1`
	d, err := scanDeployment(q.QueryRow(ctx, sql, projectID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get live deployment: %w", err)
	}
	return &d, nil
}

// SetDeploymentStatus updates just the status.
func SetDeploymentStatus(ctx context.Context, q DBTX, id, status string) error {
	_, err := q.Exec(ctx, `UPDATE deployments SET status = $2 WHERE id = $1`, id, status)
	return err
}

// MarkDeploymentStarted stamps started_at and sets the given status.
func MarkDeploymentStarted(ctx context.Context, q DBTX, id, status string) error {
	_, err := q.Exec(ctx, `UPDATE deployments SET status = $2, started_at = now() WHERE id = $1`, id, status)
	return err
}

// FinishDeployment stamps finished_at, sets a terminal status and error text.
func FinishDeployment(ctx context.Context, q DBTX, id, status, errMsg string) error {
	_, err := q.Exec(ctx, `UPDATE deployments SET status = $2, error = $3, finished_at = now() WHERE id = $1`, id, status, errMsg)
	return err
}

// SetDeploymentBuild records the resolved commit + built image.
func SetDeploymentBuild(ctx context.Context, q DBTX, id, sha, msg, imageTag string) error {
	_, err := q.Exec(ctx,
		`UPDATE deployments SET commit_sha = $2, commit_message = $3, image_tag = $4 WHERE id = $1`,
		id, sha, msg, imageTag)
	return err
}

// SetDeploymentRuntime records the running container name + ephemeral host port.
func SetDeploymentRuntime(ctx context.Context, q DBTX, id, containerName string, hostPort int) error {
	_, err := q.Exec(ctx,
		`UPDATE deployments SET container_name = $2, host_port = $3 WHERE id = $1`,
		id, containerName, hostPort)
	return err
}

// PromoteToLive atomically retires the project's current live deployment(s) and
// marks the given deployment live. Returns the retired container names so the
// caller can stop/remove them after draining.
func PromoteToLive(ctx context.Context, pool *pgxpool.Pool, projectID, deploymentID string) ([]string, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx,
		`SELECT container_name FROM deployments
		 WHERE project_id = $1 AND status = 'live' AND id <> $2 AND container_name <> ''`,
		projectID, deploymentID)
	if err != nil {
		return nil, fmt.Errorf("find old live: %w", err)
	}
	var old []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return nil, err
		}
		old = append(old, name)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if _, err := tx.Exec(ctx,
		`UPDATE deployments SET status = 'retired', finished_at = now()
		 WHERE project_id = $1 AND status = 'live' AND id <> $2`,
		projectID, deploymentID); err != nil {
		return nil, fmt.Errorf("retire old: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE deployments SET status = 'live', finished_at = now() WHERE id = $1`,
		deploymentID); err != nil {
		return nil, fmt.Errorf("promote: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit promote: %w", err)
	}
	return old, nil
}
