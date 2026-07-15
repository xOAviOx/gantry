package store

import "time"

// Project is a registered repo + subdomain (SPEC.md §6).
type Project struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	Slug           string    `json:"slug"`
	RepoURL        string    `json:"repo_url"`
	Branch         string    `json:"branch"`
	DockerfilePath string    `json:"dockerfile_path"`
	Port           int       `json:"port"`
	HealthPath     string    `json:"health_path"`
	CreatedAt      time.Time `json:"created_at"`
}

// Deployment is one build+deploy attempt for a project.
type Deployment struct {
	ID            string     `json:"id"`
	ProjectID     string     `json:"project_id"`
	CommitSHA     string     `json:"commit_sha"`
	CommitMessage string     `json:"commit_message"`
	Trigger       string     `json:"trigger"`
	Status        string     `json:"status"`
	ImageTag      string     `json:"image_tag"`
	ContainerName string     `json:"container_name"`
	HostPort      *int       `json:"host_port"`
	Error         string     `json:"error"`
	CreatedAt     time.Time  `json:"created_at"`
	StartedAt     *time.Time `json:"started_at"`
	FinishedAt    *time.Time `json:"finished_at"`
}

// ProjectWithStatus decorates a project with its current live/last deploy info
// for the dashboard list.
type ProjectWithStatus struct {
	Project
	LiveStatus       string     `json:"live_status"`        // status of the most recent deployment
	LiveDeploymentID *string    `json:"live_deployment_id"` // id of the currently-live deployment, if any
	LastDeployAt     *time.Time `json:"last_deploy_at"`
}

// LogLine is a single persisted build/deploy log line.
type LogLine struct {
	Seq    int64     `json:"seq"`
	Stream string    `json:"stream"`
	Line   string    `json:"line"`
	TS     time.Time `json:"ts"`
}

// Deploy trigger + status constants (SPEC.md §8).
const (
	TriggerManual     = "manual"
	TriggerWebhook    = "webhook"
	TriggerRollback   = "rollback"
	TriggerEnvRestart = "env_restart"

	StatusQueued       = "queued"
	StatusCloning      = "cloning"
	StatusBuilding     = "building"
	StatusStarting     = "starting"
	StatusChecking     = "checking"
	StatusRouting      = "routing"
	StatusLive         = "live"
	StatusRetired      = "retired"
	StatusBuildFailed  = "build_failed"
	StatusDeployFailed = "deploy_failed"
	StatusSuperseded   = "superseded"
	StatusCanceled     = "canceled"
)
