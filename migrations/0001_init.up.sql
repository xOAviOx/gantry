-- Gantry initial schema (SPEC.md §6). gen_random_uuid() is core in Postgres 13+.

CREATE TABLE projects (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name            text NOT NULL,
    slug            text NOT NULL UNIQUE,          -- subdomain
    repo_url        text NOT NULL,                 -- https URL or absolute local path (dev)
    branch          text NOT NULL DEFAULT 'main',
    dockerfile_path text NOT NULL DEFAULT 'Dockerfile',
    port            int  NOT NULL,                 -- port the app listens on
    health_path     text NOT NULL DEFAULT '/',
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE deployments (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id      uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    commit_sha      text NOT NULL DEFAULT '',
    commit_message  text NOT NULL DEFAULT '',
    trigger         text NOT NULL,                 -- webhook | manual | rollback | env_restart
    status          text NOT NULL,                 -- see state machine (SPEC.md §8)
    image_tag       text NOT NULL DEFAULT '',
    container_name  text NOT NULL DEFAULT '',
    host_port       int,                           -- ephemeral 127.0.0.1 port for health checks
    error           text NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now(),
    started_at      timestamptz,
    finished_at     timestamptz
);
CREATE INDEX deployments_project_created_idx ON deployments (project_id, created_at DESC);
CREATE INDEX deployments_status_idx          ON deployments (status);

CREATE TABLE jobs (
    id               bigserial PRIMARY KEY,
    kind             text NOT NULL,
    payload          jsonb NOT NULL DEFAULT '{}'::jsonb,
    status           text NOT NULL DEFAULT 'queued', -- queued|running|done|failed|canceled|superseded
    priority         int  NOT NULL DEFAULT 0,
    attempts         int  NOT NULL DEFAULT 0,
    max_attempts     int  NOT NULL DEFAULT 2,
    run_after        timestamptz NOT NULL DEFAULT now(),
    dedupe_key       text UNIQUE,                    -- e.g. github delivery id
    cancel_requested boolean NOT NULL DEFAULT false,
    locked_by        text,
    locked_at        timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now()
);
-- Supports the FOR UPDATE SKIP LOCKED claim query (SPEC.md §7).
CREATE INDEX jobs_claim_idx ON jobs (status, run_after, priority DESC, id);

CREATE TABLE log_lines (
    deployment_id uuid   NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
    seq           bigint NOT NULL,
    stream        text   NOT NULL,                   -- stdout | stderr | system
    line          text   NOT NULL,
    ts            timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (deployment_id, seq)
);

CREATE TABLE env_vars (
    project_id uuid  NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    key        text  NOT NULL,
    value_enc  bytea NOT NULL,
    nonce      bytea NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (project_id, key)
);

CREATE TABLE webhook_deliveries (
    delivery_id text PRIMARY KEY,
    received_at timestamptz NOT NULL DEFAULT now()
);
