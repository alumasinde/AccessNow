-- =============================================================================
-- 0008 core / movements — the append-only gate log every module writes to
-- An entry scan stays open until a matching exit closes it, which is what gives
-- dwell time, overstay detection and the emergency roll-call list.
-- Rows are never updated by hand; the closing pointer and the sync markers are
-- the only mutable columns.
-- Re-runnable: CREATE TABLE IF NOT EXISTS only.
-- =============================================================================

CREATE TABLE IF NOT EXISTS gate_movements (
    id                      BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    uuid                    CHAR(36)        NOT NULL,
    tenant_id               BIGINT UNSIGNED NOT NULL,
    site_id                 BIGINT UNSIGNED NOT NULL,
    gate_id                 BIGINT UNSIGNED NOT NULL,
    pass_id                 BIGINT UNSIGNED NOT NULL,
    gate_session_id         BIGINT UNSIGNED NULL COMMENT 'who was on duty when this was recorded',
    guard_user_id           BIGINT UNSIGNED NULL,
    device_id               BIGINT UNSIGNED NULL,
    direction               VARCHAR(8)      NOT NULL,
    result                  VARCHAR(16)     NOT NULL DEFAULT 'granted',
    occurred_at             DATETIME(6)     NOT NULL COMMENT 'server receive time, always UTC',
    local_time              DATETIME(6)     NULL COMMENT 'site wall clock at the moment of the scan',
    source                  VARCHAR(20)     NOT NULL DEFAULT 'scan',
    token_id                BIGINT UNSIGNED NULL COMMENT 'pass_tokens row, added in 0015',
    subject_name            VARCHAR(255)    NULL COMMENT 'snapshot, so history survives a retention purge',
    subject_document_no     VARCHAR(64)     NULL,
    plate                   VARCHAR(32)     NULL,
    close_of_movement_id    BIGINT UNSIGNED NULL COMMENT 'this exit row closes that entry row',
    dwell_minutes           INT UNSIGNED    NULL COMMENT 'filled when a closing row lands',
    override_id             BIGINT UNSIGNED NULL,
    reason_code             VARCHAR(32)     NULL,
    reason_note             VARCHAR(512)    NULL,
    idempotency_key         CHAR(64)        NULL COMMENT 'device generated, makes a replayed offline batch a no-op',
    device_occurred_at      DATETIME(6)     NULL COMMENT 'clock on the device when the scan happened',
    sync_batch_id           BIGINT UNSIGNED NULL COMMENT 'filled by the offline sync import, 0020',
    is_late_sync            TINYINT(1)      NOT NULL DEFAULT 0,
    clock_skew_seconds      INT             NULL,
    metadata                JSON            NULL,
    created_at              DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY uk_gate_movements_uuid (uuid),
    UNIQUE KEY uk_gate_movements_id_tenant (id, tenant_id),
    UNIQUE KEY uk_gate_movements_idempotency (tenant_id, idempotency_key),
    KEY idx_gate_movements_open (tenant_id, direction, result, occurred_at),
    KEY idx_gate_movements_close_of (close_of_movement_id),
    KEY idx_gate_movements_pass (pass_id, occurred_at),
    KEY idx_gate_movements_token (token_id),
    KEY idx_gate_movements_gate_time (gate_id, occurred_at),
    KEY idx_gate_movements_session (gate_session_id, occurred_at),
    KEY idx_gate_movements_guard (tenant_id, guard_user_id, occurred_at),
    KEY idx_gate_movements_site_time (tenant_id, site_id, occurred_at),
    KEY idx_gate_movements_plate (tenant_id, plate, occurred_at),
    KEY idx_gate_movements_sync (sync_batch_id),
    CONSTRAINT fk_gate_movements_tenant FOREIGN KEY (tenant_id) REFERENCES tenants (id) ON DELETE CASCADE,
    CONSTRAINT fk_gate_movements_site FOREIGN KEY (site_id, tenant_id) REFERENCES sites (id, tenant_id) ON DELETE RESTRICT,
    CONSTRAINT fk_gate_movements_gate FOREIGN KEY (gate_id, tenant_id) REFERENCES gates (id, tenant_id) ON DELETE RESTRICT,
    CONSTRAINT fk_gate_movements_pass FOREIGN KEY (pass_id, tenant_id) REFERENCES passes (id, tenant_id) ON DELETE RESTRICT,
    CONSTRAINT fk_gate_movements_session FOREIGN KEY (gate_session_id) REFERENCES gate_sessions (id) ON DELETE SET NULL,
    CONSTRAINT fk_gate_movements_guard FOREIGN KEY (guard_user_id) REFERENCES users (id) ON DELETE SET NULL,
    CONSTRAINT fk_gate_movements_device FOREIGN KEY (device_id) REFERENCES gate_devices (id) ON DELETE SET NULL,
    CONSTRAINT fk_gate_movements_close FOREIGN KEY (close_of_movement_id) REFERENCES gate_movements (id) ON DELETE SET NULL,
    CONSTRAINT fk_gate_movements_override FOREIGN KEY (override_id) REFERENCES gate_overrides (id) ON DELETE SET NULL,
    CONSTRAINT chk_gate_movements_direction CHECK (direction IN ('in', 'out', 'none')),
    CONSTRAINT chk_gate_movements_result CHECK (result IN ('granted', 'denied', 'warned', 'override', 'voided')),
    CONSTRAINT chk_gate_movements_source CHECK (source IN ('scan', 'manual', 'offline_sync', 'api', 'import', 'camera', 'system')),
    CONSTRAINT chk_gate_movements_late CHECK (is_late_sync IN (0, 1))
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci ROW_FORMAT = DYNAMIC;

CREATE TABLE IF NOT EXISTS movement_notes (
    id                      BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    tenant_id               BIGINT UNSIGNED NOT NULL,
    movement_id             BIGINT UNSIGNED NOT NULL,
    author_user_id          BIGINT UNSIGNED NULL,
    kind                    VARCHAR(24)     NOT NULL DEFAULT 'note',
    body                    TEXT            NOT NULL,
    created_at              DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    KEY idx_movement_notes_movement (movement_id, created_at),
    CONSTRAINT fk_movement_notes_movement FOREIGN KEY (movement_id, tenant_id) REFERENCES gate_movements (id, tenant_id) ON DELETE CASCADE,
    CONSTRAINT fk_movement_notes_author FOREIGN KEY (author_user_id) REFERENCES users (id) ON DELETE SET NULL,
    CONSTRAINT chk_movement_notes_kind CHECK (kind IN ('note', 'exception', 'handover', 'incident', 'verification'))
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci ROW_FORMAT = DYNAMIC;

-- Guards the roll-call and "who is on site" dashboards: an entry row with no
-- closing row and no denial is an open presence. The close pointer lives on the
-- exit row, so an entry is open when no later row references it.
CREATE OR REPLACE VIEW v_open_movements AS
SELECT  m.id,
        m.uuid,
        m.tenant_id,
        m.site_id,
        m.gate_id,
        m.pass_id,
        m.gate_session_id,
        m.guard_user_id,
        m.direction,
        m.result,
        m.source,
        m.occurred_at,
        m.local_time,
        m.subject_name,
        m.plate,
        TIMESTAMPDIFF(MINUTE, m.occurred_at, UTC_TIMESTAMP()) AS minutes_on_site
FROM    gate_movements m
WHERE   m.direction = 'in'
    AND m.result IN ('granted', 'warned', 'override')
    AND NOT EXISTS (
            SELECT 1
            FROM   gate_movements c
            WHERE  c.close_of_movement_id = m.id
        );
