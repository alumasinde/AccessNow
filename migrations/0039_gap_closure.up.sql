-- =============================================================================
-- 0039 gap closure — stored report artifacts, pre-purge notice bookkeeping
-- A report run can now be downloaded after it was produced, and a retention
-- policy remembers when its owner was last warned. The notification catalogue
-- gains the event those warnings are published under.
-- =============================================================================

ALTER TABLE report_runs
    ADD COLUMN artifact LONGBLOB NULL AFTER checksum_sha256,
    ADD COLUMN artifact_content_type VARCHAR(128) NULL AFTER artifact;

ALTER TABLE retention_policies
    ADD COLUMN last_notified_at DATETIME(6) NULL AFTER last_run_at;

INSERT INTO notification_events
    (event_key, name, module_key, default_channels, default_recipient_types, is_opt_out)
VALUES
    ('compliance.purge_due', 'Retention purge is due', 'audit_compliance',
     JSON_ARRAY('in_app', 'email'), JSON_ARRAY('tenant_admin'), 1)
ON DUPLICATE KEY UPDATE
    name = VALUES(name),
    module_key = VALUES(module_key),
    default_channels = VALUES(default_channels),
    default_recipient_types = VALUES(default_recipient_types),
    status = 'active';
