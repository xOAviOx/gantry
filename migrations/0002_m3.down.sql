DROP INDEX IF EXISTS jobs_project_active_idx;
ALTER TABLE jobs DROP COLUMN IF EXISTS cancel_reason;
