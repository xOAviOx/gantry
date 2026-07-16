package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// LiveApp is the desired runtime state of one project's live deployment — enough
// for the reconciler to recreate its container if it has gone missing.
type LiveApp struct {
	ProjectID     string
	Slug          string
	DeploymentID  string
	ImageTag      string
	ContainerName string
	Port          int
	HealthPath    string
}

// ListLiveForReconcile returns every project's currently-live deployment.
func ListLiveForReconcile(ctx context.Context, pool *pgxpool.Pool) ([]LiveApp, error) {
	const q = `
		SELECT p.id::text, p.slug, d.id::text, d.image_tag, d.container_name, p.port, p.health_path
		FROM deployments d
		JOIN projects p ON p.id = d.project_id
		WHERE d.status = 'live' AND d.image_tag <> ''
		ORDER BY p.slug`
	rows, err := pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list live for reconcile: %w", err)
	}
	defer rows.Close()

	var out []LiveApp
	for rows.Next() {
		var a LiveApp
		if err := rows.Scan(&a.ProjectID, &a.Slug, &a.DeploymentID, &a.ImageTag, &a.ContainerName, &a.Port, &a.HealthPath); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ListProtectedDeploymentIDs returns the ids of deployments that still legitimately
// own a container — the live one plus any in-flight (non-terminal) deploy. The
// reconciler removes only gantry containers whose deployment id is NOT in this set,
// so it never reaps a container that a running deploy is mid-way through creating.
func ListProtectedDeploymentIDs(ctx context.Context, pool *pgxpool.Pool) (map[string]bool, error) {
	const q = `
		SELECT id::text FROM deployments
		WHERE status NOT IN ('retired','superseded','canceled','build_failed','deploy_failed')`
	rows, err := pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list protected deployments: %w", err)
	}
	defer rows.Close()

	out := make(map[string]bool)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}
