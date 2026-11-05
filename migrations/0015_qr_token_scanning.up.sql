-- =============================================================================
-- 0015 module / QR & hardware scanning — issued tokens and the raw scan log
-- A random 128 bit token is generated on approval and only its SHA-256 is
-- stored. Every scanner, tablet, ANPR camera and USB reader posts to the same
-- validation endpoint, and scan_attempts records what that endpoint answered,
-- including the rejects that never became a movement row.
-- Module off: manual search and click check-in keep working, this table stays
-- empty and nothing else notices.
-- Re-runnable: CREATE TABLE IF NOT EXISTS only.
-- =============================================================================

CREATE TABLE IF NOT EXISTS pass_tokens (
    id                      BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    tenant_id               BIGINT UNSIGNED NOT NULL,
    pass_id                 BIGINT UNSIGNED NOT NULL,
    token_hash              CHAR(64)        NOT NULL COMMENT 'SHA-256 of the 128 bit secret, the secret itself is never stored',
    token_prefix            VARCHAR(12)     NOT NULL COMMENT 'first characters, enough for a support agent to ask "does it start with 7F3A"',
    format                  VARCHAR(20)     NOT NULL DEFAULT 'qr_text',
    issued_at               DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    expires_at              DATETIME(6)     NULL,
    revoked_at              DATETIME(6)     NULL,
    revoke_reason           VARCHAR(255)    NULL,
    max_uses                SMALLINT UNSIGNED NULL,
    use_count               INT UNSIGNED    NOT NULL DEFAULT 0,
    last_used_at            DATETIME(6)     NULL,
    last_gate_id            BIGINT UNSIGNED NULL,
    is_single_use           TINYINT(1)      NOT NULL DEFAULT 0,
    issued_by_user_id       BIGINT UNSIGNED NULL,
    delivery_channel        VARCHAR(16)     NULL COMMENT 'print, sms, email, app, badge, device',
    metadata                JSON            NULL,
    created_at              DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at              DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY uk_pass_tokens_hash (token_hash),
    KEY idx_pass_tokens_pass (pass_id, revoked_at),
    KEY idx_pass_tokens_prefix (tenant_id, token_prefix),
    KEY idx_pass_tokens_expiry (tenant_id, expires_at),
    CONSTRAINT fk_pass_tokens_pass FOREIGN KEY (pass_id, tenant_id) REFERENCES passes (id, tenant_id) ON DELETE CASCADE,
    CONSTRAINT fk_pass_tokens_last_gate FOREIGN KEY (last_gate_id) REFERENCES gates (id) ON DELETE SET NULL,
    CONSTRAINT fk_pass_tokens_issuer FOREIGN KEY (issued_by_user_id) REFERENCES users (id) ON DELETE SET NULL,
    CONSTRAINT chk_pass_tokens_format CHECK (format IN ('qr_text', 'qr_url', 'numeric', 'nfc_uid', 'rfid_card', 'barcode', 'anpr_plate')),
    CONSTRAINT chk_pass_tokens_delivery CHECK (delivery_channel IS NULL OR delivery_channel IN ('print', 'sms', 'email', 'app', 'badge', 'device', 'api')),
    CONSTRAINT chk_pass_tokens_single_use CHECK (is_single_use IN (0, 1))
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci ROW_FORMAT = DYNAMIC;

CREATE TABLE IF NOT EXISTS scan_attempts (
    id                      BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    tenant_id               BIGINT UNSIGNED NOT NULL,
    site_id                 BIGINT UNSIGNED NULL,
    gate_id                 BIGINT UNSIGNED NOT NULL,
    device_id               BIGINT UNSIGNED NULL,
    gate_session_id         BIGINT UNSIGNED NULL,
    input_kind              VARCHAR(20)     NOT NULL,
    token_hash              CHAR(64)        NULL COMMENT 'hash of whatever the device read, raw input is never stored',
    token_prefix            VARCHAR(12)     NULL,
    plate_read              VARCHAR(32)     NULL COMMENT 'ANPR result before any match',
    outcome                 VARCHAR(24)     NOT NULL,
    response_code           VARCHAR(32)     NULL COMMENT 'stable code the device speaks, e.g. WRONG_GATE',
    response_message        VARCHAR(255)    NULL,
    matched_pass_id         BIGINT UNSIGNED NULL,
    matched_token_id        BIGINT UNSIGNED NULL,
    matched_movement_id     BIGINT UNSIGNED NULL,
    direction_requested     VARCHAR(8)      NULL,
    latency_ms              INT UNSIGNED    NULL,
    occurred_at             DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    idempotency_key         CHAR(64)        NULL,
    raw_payload             JSON            NULL COMMENT 'device envelope, stripped of any personal data',
    created_at              DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY uk_scan_attempts_idempotency (tenant_id, idempotency_key),
    KEY idx_scan_attempts_gate (gate_id, occurred_at),
    KEY idx_scan_attempts_tenant (tenant_id, occurred_at),
    KEY idx_scan_attempts_outcome (tenant_id, outcome, occurred_at),
    KEY idx_scan_attempts_token (token_hash),
    KEY idx_scan_attempts_pass (matched_pass_id),
    KEY idx_scan_attempts_device (device_id, occurred_at),
    CONSTRAINT fk_scan_attempts_tenant FOREIGN KEY (tenant_id) REFERENCES tenants (id) ON DELETE CASCADE,
    CONSTRAINT fk_scan_attempts_gate FOREIGN KEY (gate_id, tenant_id) REFERENCES gates (id, tenant_id) ON DELETE RESTRICT,
    CONSTRAINT fk_scan_attempts_site FOREIGN KEY (site_id) REFERENCES sites (id) ON DELETE SET NULL,
    CONSTRAINT fk_scan_attempts_device FOREIGN KEY (device_id) REFERENCES gate_devices (id) ON DELETE SET NULL,
    CONSTRAINT fk_scan_attempts_session FOREIGN KEY (gate_session_id) REFERENCES gate_sessions (id) ON DELETE SET NULL,
    CONSTRAINT fk_scan_attempts_pass FOREIGN KEY (matched_pass_id) REFERENCES passes (id) ON DELETE SET NULL,
    CONSTRAINT fk_scan_attempts_token FOREIGN KEY (matched_token_id) REFERENCES pass_tokens (id) ON DELETE SET NULL,
    CONSTRAINT fk_scan_attempts_movement FOREIGN KEY (matched_movement_id) REFERENCES gate_movements (id) ON DELETE SET NULL,
    CONSTRAINT chk_scan_attempts_input CHECK (input_kind IN ('qr', 'barcode', 'rfid', 'nfc', 'plate', 'manual', 'face', 'other')),
    CONSTRAINT chk_scan_attempts_outcome CHECK (outcome IN ('granted', 'denied', 'warned', 'override', 'unknown_token', 'expired', 'revoked', 'wrong_gate', 'out_of_window', 'already_used', 'blocklist_hit', 'hardware_fault')),
    CONSTRAINT chk_scan_attempts_direction CHECK (direction_requested IS NULL OR direction_requested IN ('in', 'out', 'both'))
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci ROW_FORMAT = DYNAMIC;
