-- =============================================================================
-- 0036 module / audit & compliance — archive receipts
-- The purge now offers four actions. delete removes an attachment row; archive
-- erases the value in place and files a receipt here, so the desk can prove a
-- record was archived rather than merely blanked. The receipt names the record
-- and hashes the value it erased: it never stores the value itself, because a
-- second copy of the personal data is what the retention policy exists to remove.
-- Re-runnable: CREATE TABLE IF NOT EXISTS only.
-- =============================================================================

CREATE TABLE IF NOT EXISTS data_purge_archives (
    id                      BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    tenant_id               BIGINT UNSIGNED NOT NULL,
    purge_run_id            BIGINT UNSIGNED NULL,
    data_category           VARCHAR(48)     NOT NULL,
    entity_type             VARCHAR(48)     NOT NULL,
    entity_id               BIGINT UNSIGNED NOT NULL,
    value_hash              CHAR(64)        NULL COMMENT 'SHA-256 of the erased value, NULL when the category carried none',
    archived_at             DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    created_at              DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    KEY idx_data_purge_archives_tenant (tenant_id, data_category, archived_at),
    KEY idx_data_purge_archives_run (purge_run_id),
    KEY idx_data_purge_archives_record (tenant_id, entity_type, entity_id),
    CONSTRAINT fk_data_purge_archives_tenant FOREIGN KEY (tenant_id) REFERENCES tenants (id) ON DELETE CASCADE,
    CONSTRAINT fk_data_purge_archives_run FOREIGN KEY (purge_run_id) REFERENCES data_purge_runs (id) ON DELETE SET NULL,
    CONSTRAINT chk_data_purge_archives_category CHECK (data_category IN ('visitor_identity', 'visitor_photo', 'pass_subject_document', 'movement_detail', 'movement_note', 'attachment', 'notification_body', 'notification_address', 'webhook_payload', 'scan_raw_payload', 'blocklist_value', 'audit_log', 'report_file'))
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci ROW_FORMAT = DYNAMIC;
