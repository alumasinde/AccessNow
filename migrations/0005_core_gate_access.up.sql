-- =============================================================================
-- 0005 core / gate access & shifts — who may work which gate, and when
-- Picking a gate opens a gate session, which answers "who was on duty at the
-- Staff Gate at 2am". A user may hold only one open session at a time.
-- Re-runnable: CREATE TABLE IF NOT EXISTS only.
-- =============================================================================

CREATE TABLE IF NOT EXISTS gate_users (
    gate_id                 BIGINT UNSIGNED NOT NULL,
    tenant_id               BIGINT UNSIGNED NOT NULL,
    user_id                 BIGINT UNSIGNED NOT NULL,
    is_supervisor           TINYINT(1)      NOT NULL DEFAULT 0,
    assigned_by             BIGINT UNSIGNED NULL,
    assigned_at             DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    expires_at              DATETIME(6)     NULL,
    PRIMARY KEY (gate_id, user_id),
    KEY idx_gate_users_user (user_id, tenant_id),
    KEY idx_gate_users_tenant (tenant_id, gate_id),
    CONSTRAINT fk_gate_users_gate FOREIGN KEY (gate_id, tenant_id) REFERENCES gates (id, tenant_id) ON DELETE CASCADE,
    CONSTRAINT fk_gate_users_user FOREIGN KEY (user_id, tenant_id) REFERENCES users (id, tenant_id) ON DELETE CASCADE,
    CONSTRAINT fk_gate_users_assigner FOREIGN KEY (assigned_by) REFERENCES users (id) ON DELETE SET NULL,
    CONSTRAINT chk_gate_users_supervisor CHECK (is_supervisor IN (0, 1))
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci ROW_FORMAT = DYNAMIC;

CREATE TABLE IF NOT EXISTS gate_sessions (
    id                      BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    uuid                    CHAR(36)        NOT NULL,
    tenant_id               BIGINT UNSIGNED NOT NULL,
    site_id                 BIGINT UNSIGNED NOT NULL,
    user_id                 BIGINT UNSIGNED NOT NULL,
    gate_id                 BIGINT UNSIGNED NOT NULL,
    device_id               BIGINT UNSIGNED NULL,
    opened_at               DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    closed_at               DATETIME(6)     NULL,
    close_reason            VARCHAR(24)     NULL COMMENT 'switched_gate, logout, timeout, device_revoked, admin',
    handover_from_id        BIGINT UNSIGNED NULL COMMENT 'the session that ended just before this one',
    movements_count         INT UNSIGNED    NOT NULL DEFAULT 0,
    notes                   VARCHAR(255)    NULL,
    open_user_key           VARCHAR(32)     GENERATED ALWAYS AS (IF(closed_at IS NULL, CONCAT('open-', user_id), NULL)) STORED COMMENT 'NULL once closed, so the unique key allows many closed rows',
    created_at              DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at              DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY uk_gate_sessions_uuid (uuid),
    UNIQUE KEY uk_gate_sessions_one_open (open_user_key),
    KEY idx_gate_sessions_gate_open (gate_id, closed_at, opened_at),
    KEY idx_gate_sessions_user (tenant_id, user_id, opened_at),
    KEY idx_gate_sessions_device (device_id, closed_at),
    CONSTRAINT fk_gate_sessions_tenant FOREIGN KEY (tenant_id) REFERENCES tenants (id) ON DELETE CASCADE,
    CONSTRAINT fk_gate_sessions_site FOREIGN KEY (site_id, tenant_id) REFERENCES sites (id, tenant_id) ON DELETE CASCADE,
    CONSTRAINT fk_gate_sessions_user FOREIGN KEY (user_id, tenant_id) REFERENCES users (id, tenant_id) ON DELETE CASCADE,
    CONSTRAINT fk_gate_sessions_gate FOREIGN KEY (gate_id, tenant_id) REFERENCES gates (id, tenant_id) ON DELETE CASCADE,
    CONSTRAINT fk_gate_sessions_device FOREIGN KEY (device_id) REFERENCES gate_devices (id) ON DELETE SET NULL,
    CONSTRAINT fk_gate_sessions_handover FOREIGN KEY (handover_from_id) REFERENCES gate_sessions (id) ON DELETE SET NULL,
    CONSTRAINT chk_gate_sessions_close_reason CHECK (close_reason IS NULL OR close_reason IN ('switched_gate', 'logout', 'timeout', 'device_revoked', 'admin', 'previous_shift')),
    CONSTRAINT chk_gate_sessions_window CHECK (closed_at IS NULL OR closed_at >= opened_at)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci ROW_FORMAT = DYNAMIC;

CREATE TABLE IF NOT EXISTS gate_overrides (
    id                      BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    tenant_id               BIGINT UNSIGNED NOT NULL,
    gate_id                 BIGINT UNSIGNED NOT NULL,
    gate_session_id         BIGINT UNSIGNED NULL,
    user_id                 BIGINT UNSIGNED NOT NULL COMMENT 'the supervisor who allowed it',
    kind                    VARCHAR(24)     NOT NULL COMMENT 'closed_gate, out_of_hours, wrong_gate, category_mismatch, blocklist_watch',
    pass_id                 BIGINT UNSIGNED NULL,
    reason                  VARCHAR(512)    NOT NULL COMMENT 'mandatory, audited',
    occurred_at             DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    expires_at              DATETIME(6)     NULL COMMENT 'standing override window, NULL = single action',
    revoked_at              DATETIME(6)     NULL,
    created_at              DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    KEY idx_gate_overrides_gate (tenant_id, gate_id, occurred_at),
    KEY idx_gate_overrides_user (tenant_id, user_id, occurred_at),
    CONSTRAINT fk_gate_overrides_tenant FOREIGN KEY (tenant_id) REFERENCES tenants (id) ON DELETE CASCADE,
    CONSTRAINT fk_gate_overrides_gate FOREIGN KEY (gate_id, tenant_id) REFERENCES gates (id, tenant_id) ON DELETE CASCADE,
    CONSTRAINT fk_gate_overrides_session FOREIGN KEY (gate_session_id) REFERENCES gate_sessions (id) ON DELETE SET NULL,
    CONSTRAINT fk_gate_overrides_user FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT chk_gate_overrides_kind CHECK (kind IN ('closed_gate', 'out_of_hours', 'wrong_gate', 'category_mismatch', 'blocklist_watch', 'override_limit'))
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci ROW_FORMAT = DYNAMIC;
