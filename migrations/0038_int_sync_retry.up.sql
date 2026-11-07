-- =============================================================================
-- 0038 module / integrations & API — sync job retry schedule
-- A failed run is put back in the queue with a next_attempt_at instead of
-- waiting for the system's next frequency interval. attempts is the count of
-- runs already started, so the worker can stop at the ladder's end.
-- =============================================================================

ALTER TABLE sync_jobs
    ADD COLUMN attempts SMALLINT UNSIGNED NOT NULL DEFAULT 0 AFTER status,
    ADD COLUMN next_attempt_at DATETIME(6) NULL AFTER attempts,
    ADD KEY idx_sync_jobs_due (status, next_attempt_at);
