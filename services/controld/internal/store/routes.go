package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/avishuklacode/gantry/services/controld/internal/proxy"
)

// ListLiveAppUpstreams returns one upstream per project whose current deployment
// is live, for the Caddy renderer. M0 returns an empty set (no deployments yet).
func ListLiveAppUpstreams(ctx context.Context, pool *pgxpool.Pool) ([]proxy.AppUpstream, error) {
	const q = `
		SELECT p.slug, d.container_name, p.port
		FROM deployments d
		JOIN projects p ON p.id = d.project_id
		WHERE d.status = 'live' AND d.container_name <> ''
		ORDER BY p.slug`

	rows, err := pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("query live upstreams: %w", err)
	}
	defer rows.Close()

	var out []proxy.AppUpstream
	for rows.Next() {
		var slug, container string
		var port int
		if err := rows.Scan(&slug, &container, &port); err != nil {
			return nil, fmt.Errorf("scan upstream: %w", err)
		}
		out = append(out, proxy.AppUpstream{
			Slug: slug,
			Dial: fmt.Sprintf("%s:%d", container, port),
		})
	}
	return out, rows.Err()
}
