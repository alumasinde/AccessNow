-- 0038 module / integrations & API — sync job retry schedule.

ALTER TABLE sync_jobs
    DROP KEY idx_sync_jobs_due,
    DROP COLUMN next_attempt_at,
    DROP COLUMN attempts;
