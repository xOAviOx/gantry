package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// ErrNotFound is returned when a lookup matches no row.
var ErrNotFound = errors.New("not found")

const projectCols = `id::text, name, slug, repo_url, branch, dockerfile_path, port, health_path, created_at`

func scanProject(row pgx.Row) (Project, error) {
	var p Project
	err := row.Scan(&p.ID, &p.Name, &p.Slug, &p.RepoURL, &p.Branch, &p.DockerfilePath, &p.Port, &p.HealthPath, &p.CreatedAt)
	return p, err
}

// CreateProject inserts a project and returns the stored row.
func CreateProject(ctx context.Context, q DBTX, p Project) (Project, error) {
	const sql = `INSERT INTO projects (name, slug, repo_url, branch, dockerfile_path, port, health_path)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		RETURNING ` + projectCols
	row := q.QueryRow(ctx, sql, p.Name, p.Slug, p.RepoURL, p.Branch, p.DockerfilePath, p.Port, p.HealthPath)
	got, err := scanProject(row)
	if err != nil {
		return Project{}, fmt.Errorf("create project: %w", err)
	}
	return got, nil
}

// GetProject returns one project by id, or ErrNotFound.
func GetProject(ctx context.Context, q DBTX, id string) (Project, error) {
	const sql = `SELECT ` + projectCols + ` FROM projects WHERE id = $1`
	p, err := scanProject(q.QueryRow(ctx, sql, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Project{}, ErrNotFound
	}
	if err != nil {
		return Project{}, fmt.Errorf("get project: %w", err)
	}
	return p, nil
}

// ListProjects returns all projects, newest first.
func ListProjects(ctx context.Context, q DBTX) ([]Project, error) {
	const sql = `SELECT ` + projectCols + ` FROM projects ORDER BY created_at DESC`
	rows, err := q.Query(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	defer rows.Close()

	var out []Project
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ListProjectsWithStatus returns every project decorated with its most recent
// deployment status, the currently-live deployment id (if any), and last deploy time.
func ListProjectsWithStatus(ctx context.Context, q DBTX) ([]ProjectWithStatus, error) {
	const sql = `
		SELECT ` + projectCols + `,
			COALESCE(latest.status, '') AS live_status,
			live.id::text                AS live_deployment_id,
			latest.created_at            AS last_deploy_at
		FROM projects p
		LEFT JOIN LATERAL (
			SELECT status, created_at FROM deployments d
			WHERE d.project_id = p.id ORDER BY created_at DESC LIMIT 1
		) latest ON true
		LEFT JOIN LATERAL (
			SELECT id FROM deployments d
			WHERE d.project_id = p.id AND d.status = 'live' ORDER BY created_at DESC LIMIT 1
		) live ON true
		ORDER BY p.created_at DESC`

	rows, err := q.Query(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("list projects with status: %w", err)
	}
	defer rows.Close()

	var out []ProjectWithStatus
	for rows.Next() {
		var pw ProjectWithStatus
		p := &pw.Project
		if err := rows.Scan(
			&p.ID, &p.Name, &p.Slug, &p.RepoURL, &p.Branch, &p.DockerfilePath, &p.Port, &p.HealthPath, &p.CreatedAt,
			&pw.LiveStatus, &pw.LiveDeploymentID, &pw.LastDeployAt,
		); err != nil {
			return nil, err
		}
		out = append(out, pw)
	}
	return out, rows.Err()
}
