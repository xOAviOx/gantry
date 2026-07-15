// Package docker wraps the official Docker SDK client and centralizes the
// gantry label scheme. Every resource controld creates carries these labels, and
// every list/prune filters by them — we never touch unlabeled resources (§5).
package docker

import (
	"context"
	"fmt"

	"github.com/docker/docker/client"
)

const (
	LabelManaged    = "dev.gantry.managed"
	LabelProject    = "dev.gantry.project"
	LabelDeployment = "dev.gantry.deployment"
	ManagedValue    = "true"
)

// Client embeds the SDK client so callers can use the full API plus our helpers.
type Client struct {
	*client.Client
}

// New builds a client from the environment (Windows named pipe by default on
// Docker Desktop) with API-version negotiation, and verifies with a Ping.
func New(ctx context.Context, host string) (*Client, error) {
	opts := []client.Opt{client.FromEnv, client.WithAPIVersionNegotiation()}
	if host != "" {
		opts = append(opts, client.WithHost(host))
	}
	c, err := client.NewClientWithOpts(opts...)
	if err != nil {
		return nil, fmt.Errorf("docker client: %w", err)
	}
	if _, err := c.Ping(ctx); err != nil {
		return nil, fmt.Errorf("docker ping: %w", err)
	}
	return &Client{Client: c}, nil
}

// Labels returns the standard label set for a resource belonging to a deployment.
func Labels(slug, deploymentID string) map[string]string {
	return map[string]string{
		LabelManaged:    ManagedValue,
		LabelProject:    slug,
		LabelDeployment: deploymentID,
	}
}
