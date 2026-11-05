-- =============================================================================
-- 0009 core / events & audit — the module firewall and the tamper-evident log
-- Modules never call each other. They write a domain_events row (transactional
-- outbox) and a worker delivers it; if the consuming module is off, the event
-- simply goes undelivered and the standalone default applies.
-- audit_logs carries the hash-chain columns from the start, so the compliance
-- module can verify a chain without ever rewriting this table.
-- Re-runnable: CREATE TABLE IF NOT EXISTS / CREATE OR REPLACE VIEW.
-- =============================================================================

CREATE TABLE IF NOT EXISTS domain_events (
    id                      BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    tenant_id               BIGINT UNSIGNED NOT NULL,
    event_key               VARCHAR(64)     NOT NULL COMMENT 'movement.recorded, pass.approved, item.overdue',
    aggregate_type          VARCHAR(48)     NOT NULL,
    aggregate_id            BIGINT UNSIGNED NULL,
    pass_id                 BIGINT UNSIGNED NULL,
    site_id                 BIGINT UNSIGNED NULL,
    gate_id                 BIGINT UNSIGNED NULL,
    actor_user_id           BIGINT UNSIGNED NULL,
    occurred_at             DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    dedupe_key              CHAR(64)        NULL COMMENT 'caller supplied, stops a replayed request emitting twice',
    payload                 JSON            NOT NULL,
    status                  VARCHAR(16)     NOT NULL DEFAULT 'pending',
    attempts                SMALLINT UNSIGNED NOT NULL DEFAULT 0,
    dispatched_at           DATETIME(6)     NULL,
    last_error              VARCHAR(512)    NULL,
    created_at              DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY uk_domain_events_dedupe (tenant_id, dedupe_key),
    KEY idx_domain_events_pending (status, occurred_at),
    KEY idx_domain_events_tenant (tenant_id, event_key, occurred_at),
    KEY idx_domain_events_pass (pass_id),
    KEY idx_domain_events_aggregate (aggregate_type, aggregate_id),
    CONSTRAINT fk_domain_events_tenant FOREIGN KEY (tenant_id) REFERENCES tenants (id) ON DELETE CASCADE,
    CONSTRAINT fk_domain_events_pass FOREIGN KEY (pass_id) REFERENCES passes (id) ON DELETE SET NULL,
    CONSTRAINT fk_domain_events_actor FOREIGN KEY (actor_user_id) REFERENCES users (id) ON DELETE SET NULL,
    CONSTRAINT chk_domain_events_status CHECK (status IN ('pending', 'dispatched', 'no_subscriber', 'failed', 'discarded'))
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci ROW_FORMAT = DYNAMIC;

CREATE TABLE IF NOT EXISTS audit_logs (
    id                      BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    tenant_id               BIGINT UNSIGNED NOT NULL,
    stream_key              VARCHAR(32)     NOT NULL DEFAULT 'audit',
    seq                     BIGINT UNSIGNED NOT NULL COMMENT 'per tenant chain position',
    actor_user_id           BIGINT UNSIGNED NULL,
    actor_type              VARCHAR(16)     NOT NULL DEFAULT 'user',
    actor_device_id         BIGINT UNSIGNED NULL,
    action                  VARCHAR(64)     NOT NULL COMMENT 'gate.switched, pass.override, role.granted',
    subject_type            VARCHAR(48)     NULL,
    subject_id              BIGINT UNSIGNED NULL,
    pass_id                 BIGINT UNSIGNED NULL,
    site_id                 BIGINT UNSIGNED NULL,
    gate_id                 BIGINT UNSIGNED NULL,
    gate_session_id         BIGINT UNSIGNED NULL,
    before_json             JSON            NULL,
    after_json              JSON            NULL,
    reason                  VARCHAR(512)    NULL,
    ip_address              VARCHAR(45)     NULL,
    user_agent              VARCHAR(255)    NULL,
    request_id              VARCHAR(64)     NULL,
    occurred_at             DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    prev_hash               CHAR(64)        NULL COMMENT 'row_hash of the previous seq, NULL at the chain head',
    row_hash                CHAR(64)        NULL COMMENT 'SHA-256 over the canonical row plus prev_hash',
    chain_alg               VARCHAR(16)     NOT NULL DEFAULT 'sha256',
    created_at              DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY uk_audit_logs_seq (tenant_id, stream_key, seq),
    KEY idx_audit_logs_time (tenant_id, occurred_at),
    KEY idx_audit_logs_action (tenant_id, action, occurred_at),
    KEY idx_audit_logs_subject (tenant_id, subject_type, subject_id),
    KEY idx_audit_logs_actor (tenant_id, actor_user_id, occurred_at),
    KEY idx_audit_logs_pass (pass_id),
    KEY idx_audit_logs_hash (row_hash),
    CONSTRAINT fk_audit_logs_tenant FOREIGN KEY (tenant_id) REFERENCES tenants (id) ON DELETE CASCADE,
    CONSTRAINT fk_audit_logs_actor FOREIGN KEY (actor_user_id) REFERENCES users (id) ON DELETE SET NULL,
    CONSTRAINT fk_audit_logs_device FOREIGN KEY (actor_device_id) REFERENCES gate_devices (id) ON DELETE SET NULL,
    CONSTRAINT fk_audit_logs_pass FOREIGN KEY (pass_id) REFERENCES passes (id) ON DELETE SET NULL,
    CONSTRAINT fk_audit_logs_gate FOREIGN KEY (gate_id) REFERENCES gates (id) ON DELETE SET NULL,
    CONSTRAINT fk_audit_logs_session FOREIGN KEY (gate_session_id) REFERENCES gate_sessions (id) ON DELETE SET NULL,
    CONSTRAINT chk_audit_logs_actor_type CHECK (actor_type IN ('user', 'device', 'api_key', 'service', 'scheduler', 'system'))
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci ROW_FORMAT = DYNAMIC;

CREATE TABLE IF NOT EXISTS audit_chain_tips (
    tenant_id               BIGINT UNSIGNED NOT NULL,
    stream_key              VARCHAR(32)     NOT NULL DEFAULT 'audit',
    last_seq                BIGINT UNSIGNED NOT NULL DEFAULT 0,
    last_hash               CHAR(64)        NULL,
    sealed_at               DATETIME(6)     NULL,
    updated_at              DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    PRIMARY KEY (tenant_id, stream_key),
    CONSTRAINT fk_audit_chain_tips_tenant FOREIGN KEY (tenant_id) REFERENCES tenants (id) ON DELETE CASCADE
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci ROW_FORMAT = DYNAMIC;

-- One row per decision the guard screen makes, so a red light is explainable
-- after the fact even when no pass row was created.
CREATE OR REPLACE VIEW v_recent_gate_decisions AS
SELECT  m.id AS movement_id,
        m.tenant_id,
        m.gate_id,
        g.name AS gate_name,
        m.pass_id,
        p.pass_no,
        pt.type_key AS pass_type_key,
        m.direction,
        m.result,
        m.reason_code,
        m.occurred_at,
        m.guard_user_id,
        m.gate_session_id
FROM    gate_movements m
JOIN    gates g               ON g.id = m.gate_id
JOIN    passes p              ON p.id = m.pass_id
JOIN    pass_types pt         ON pt.id = p.pass_type_id;
