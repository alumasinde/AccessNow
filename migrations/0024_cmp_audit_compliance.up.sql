-- =============================================================================
-- 0024 module / audit & compliance — retention, legal holds, chain verification
-- Core always writes the basic audit row. This module adds the tools around it:
-- a retention policy per data category, legal holds that stop a purge, the purge
-- run ledger, and chain verification against the hash columns in audit_logs.
-- The Auditor role is read only and comes from the RBAC seed in 0026.
-- Re-runnable: CREATE TABLE IF NOT EXISTS only.
-- =============================================================================

CREATE TABLE IF NOT EXISTS retention_policies (
    id                      BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    tenant_id               BIGINT UNSIGNED NOT NULL,
    data_category           VARCHAR(48)     NOT NULL,
    retention_days          SMALLINT UNSIGNED NOT NULL,
    purge_action            VARCHAR(16)     NOT NULL DEFAULT 'null' COMMENT 'null blanks the column, anonymise replaces it with a stable hash',
    scope                   JSON            NULL COMMENT 'sites, modules or pass types the policy narrows to',
    notify_before_days      SMALLINT UNSIGNED NULL COMMENT 'warn the data owner before a purge lands',
    last_run_at             DATETIME(6)     NULL,
    is_active               TINYINT(1)      NOT NULL DEFAULT 1,
    updated_by              BIGINT UNSIGNED NULL,
    created_at              DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at              DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY uk_retention_policies (tenant_id, data_category),
    KEY idx_retention_policies_due (is_active, last_run_at),
    CONSTRAINT fk_retention_policies_tenant FOREIGN KEY (tenant_id) REFERENCES tenants (id) ON DELETE CASCADE,
    CONSTRAINT fk_retention_policies_updater FOREIGN KEY (updated_by) REFERENCES users (id) ON DELETE SET NULL,
    CONSTRAINT chk_retention_policies_category CHECK (data_category IN ('visitor_identity', 'visitor_photo', 'pass_subject_document', 'movement_detail', 'movement_note', 'attachment', 'notification_body', 'notification_address', 'webhook_payload', 'scan_raw_payload', 'blocklist_value', 'audit_log', 'report_file')),
    CONSTRAINT chk_retention_policies_action CHECK (purge_action IN ('null', 'anonymise', 'delete', 'archive')),
    CONSTRAINT chk_retention_policies_days CHECK (retention_days >= 0),
    CONSTRAINT chk_retention_policies_active CHECK (is_active IN (0, 1))
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci ROW_FORMAT = DYNAMIC;

CREATE TABLE IF NOT EXISTS legal_holds (
    id                      BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    tenant_id               BIGINT UNSIGNED NOT NULL,
    entity_type             VARCHAR(48)     NOT NULL,
    entity_id               BIGINT UNSIGNED NOT NULL,
    pass_id                 BIGINT UNSIGNED NULL,
    case_ref                VARCHAR(128)    NOT NULL,
    reason                  VARCHAR(1024)   NOT NULL,
    status                  VARCHAR(16)     NOT NULL DEFAULT 'active',
    placed_by_user_id       BIGINT UNSIGNED NULL,
    placed_at               DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    expires_at              DATETIME(6)     NULL,
    reviewed_at             DATETIME(6)     NULL,
    released_by_user_id     BIGINT UNSIGNED NULL,
    released_at             DATETIME(6)     NULL,
    release_reason          VARCHAR(1024)   NULL,
    created_at              DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at              DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    KEY idx_legal_holds_entity (tenant_id, entity_type, entity_id, status),
    KEY idx_legal_holds_pass (pass_id),
    KEY idx_legal_holds_expiry (status, expires_at),
    CONSTRAINT fk_legal_holds_tenant FOREIGN KEY (tenant_id) REFERENCES tenants (id) ON DELETE CASCADE,
    CONSTRAINT fk_legal_holds_pass FOREIGN KEY (pass_id) REFERENCES passes (id) ON DELETE SET NULL,
    CONSTRAINT fk_legal_holds_placer FOREIGN KEY (placed_by_user_id) REFERENCES users (id) ON DELETE SET NULL,
    CONSTRAINT fk_legal_holds_releaser FOREIGN KEY (released_by_user_id) REFERENCES users (id) ON DELETE SET NULL,
    CONSTRAINT chk_legal_holds_status CHECK (status IN ('active', 'released', 'expired')),
    CONSTRAINT chk_legal_holds_window CHECK (expires_at IS NULL OR expires_at > placed_at)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci ROW_FORMAT = DYNAMIC;

CREATE TABLE IF NOT EXISTS data_purge_runs (
    id                      BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    tenant_id               BIGINT UNSIGNED NOT NULL,
    retention_policy_id     BIGINT UNSIGNED NULL,
    data_category           VARCHAR(48)     NOT NULL,
    purge_action            VARCHAR(16)     NOT NULL,
    is_dry_run              TINYINT(1)      NOT NULL DEFAULT 1,
    status                  VARCHAR(16)     NOT NULL DEFAULT 'queued',
    rows_scanned            BIGINT UNSIGNED NOT NULL DEFAULT 0,
    rows_purged             BIGINT UNSIGNED NOT NULL DEFAULT 0,
    rows_skipped_on_hold    BIGINT UNSIGNED NOT NULL DEFAULT 0,
    cutoff_at               DATETIME(6)     NULL COMMENT 'anything older than this was eligible',
    error_message           VARCHAR(512)    NULL,
    started_at              DATETIME(6)     NULL,
    finished_at             DATETIME(6)     NULL,
    requested_by_user_id    BIGINT UNSIGNED NULL,
    created_at              DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    KEY idx_data_purge_runs_tenant (tenant_id, data_category, created_at),
    KEY idx_data_purge_runs_status (status, created_at),
    CONSTRAINT fk_data_purge_runs_tenant FOREIGN KEY (tenant_id) REFERENCES tenants (id) ON DELETE CASCADE,
    CONSTRAINT fk_data_purge_runs_policy FOREIGN KEY (retention_policy_id) REFERENCES retention_policies (id) ON DELETE SET NULL,
    CONSTRAINT fk_data_purge_runs_requester FOREIGN KEY (requested_by_user_id) REFERENCES users (id) ON DELETE SET NULL,
    CONSTRAINT chk_data_purge_runs_category CHECK (data_category IN ('visitor_identity', 'visitor_photo', 'pass_subject_document', 'movement_detail', 'movement_note', 'attachment', 'notification_body', 'notification_address', 'webhook_payload', 'scan_raw_payload', 'blocklist_value', 'audit_log', 'report_file')),
    CONSTRAINT chk_data_purge_runs_action CHECK (purge_action IN ('null', 'anonymise', 'delete', 'archive')),
    CONSTRAINT chk_data_purge_runs_status CHECK (status IN ('queued', 'running', 'succeeded', 'partial', 'failed')),
    CONSTRAINT chk_data_purge_runs_dry CHECK (is_dry_run IN (0, 1))
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci ROW_FORMAT = DYNAMIC;

CREATE TABLE IF NOT EXISTS audit_verifications (
    id                      BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    tenant_id               BIGINT UNSIGNED NOT NULL,
    stream_key              VARCHAR(32)     NOT NULL DEFAULT 'audit',
    from_seq                BIGINT UNSIGNED NOT NULL DEFAULT 0,
    to_seq                  BIGINT UNSIGNED NOT NULL DEFAULT 0,
    rows_checked            BIGINT UNSIGNED NOT NULL DEFAULT 0,
    outcome                 VARCHAR(16)     NOT NULL DEFAULT 'pending',
    first_mismatch_seq      BIGINT UNSIGNED NULL,
    first_mismatch_hash     CHAR(64)        NULL,
    tip_hash                CHAR(64)        NULL,
    duration_ms             INT UNSIGNED    NULL,
    checked_by_user_id      BIGINT UNSIGNED NULL,
    checked_at              DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    note                    VARCHAR(512)    NULL,
    created_at              DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    KEY idx_audit_verifications_tenant (tenant_id, checked_at),
    KEY idx_audit_verifications_outcome (tenant_id, outcome, checked_at),
    CONSTRAINT fk_audit_verifications_tenant FOREIGN KEY (tenant_id) REFERENCES tenants (id) ON DELETE CASCADE,
    CONSTRAINT fk_audit_verifications_checker FOREIGN KEY (checked_by_user_id) REFERENCES users (id) ON DELETE SET NULL,
    CONSTRAINT chk_audit_verifications_outcome CHECK (outcome IN ('pending', 'verified', 'mismatch', 'incomplete')),
    CONSTRAINT chk_audit_verifications_range CHECK (to_seq >= from_seq)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci ROW_FORMAT = DYNAMIC;

-- Everything a retention job may touch, in one place, so the purge walker has a
-- single query per category instead of knowledge of every module table.
CREATE OR REPLACE VIEW v_purge_candidates AS
SELECT  'visitor_identity' AS data_category, v.tenant_id, 'visitors' AS entity_type, v.id AS entity_id,
        v.id_number_purged_at AS purged_at, v.created_at AS record_created_at
FROM    visitors v
WHERE   v.id_number IS NOT NULL AND v.id_number_purged_at IS NULL
UNION ALL
SELECT  'visitor_photo', v.tenant_id, 'visitors', v.id, v.photo_purged_at, v.created_at
FROM    visitors v
WHERE   v.photo_path IS NOT NULL AND v.photo_purged_at IS NULL
UNION ALL
SELECT  'pass_subject_document', p.tenant_id, 'passes', p.id, p.subject_document_purged_at, p.created_at
FROM    passes p
WHERE   p.subject_document_no IS NOT NULL AND p.subject_document_purged_at IS NULL
UNION ALL
SELECT  'attachment', a.tenant_id, 'pass_attachments', a.id, a.purged_at, a.created_at
FROM    pass_attachments a
WHERE   a.purged_at IS NULL
UNION ALL
SELECT  'notification_address', n.tenant_id, 'notifications', n.id, n.to_address_purged_at, n.created_at
FROM    notifications n
WHERE   n.to_address IS NOT NULL AND n.to_address_purged_at IS NULL;
