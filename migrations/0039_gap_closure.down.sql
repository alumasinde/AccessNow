DELETE FROM notification_events WHERE event_key = 'compliance.purge_due';

ALTER TABLE retention_policies
    DROP COLUMN last_notified_at;

ALTER TABLE report_runs
    DROP COLUMN artifact_content_type,
    DROP COLUMN artifact;
