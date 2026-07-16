-- M3: queue hardening. Record why a job was canceled so the worker can set the
-- right terminal deployment status (superseded vs canceled) when its context is
-- torn down cooperatively (SPEC.md §7).
ALTER TABLE jobs ADD COLUMN cancel_reason text NOT NULL DEFAULT '';

-- Speeds up per-project supersession lookups (find active build_deploy jobs for
-- a project when a newer push/deploy arrives).
CREATE INDEX jobs_project_active_idx
    ON jobs ((payload->>'project_id'))
    WHERE kind = 'build_deploy' AND status IN ('queued', 'running');
