-- =============================================================================
-- 0020 module / offline gate mode — cached passes, buffered scans, replay-safe sync
-- A device pulls today's approved passes valid at its gate plus the blacklist,
-- keeps movements in a local queue, and posts them as one batch when the link
-- returns. The batch is identified by client_batch_id and gate_movements by
-- idempotency_key, so a half-finished upload replayed ten times still lands once.
-- On conflict the server keeps the pass status, keeps every movement row, and
-- marks the late ones. Module off: the gate simply requires a live connection.
-- Re-runnable: CREATE TABLE IF NOT EXISTS only.
-- =============================================================================

CREATE TABLE IF NOT EXISTS device_sync_batches (
    id                      BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    tenant_id               BIGINT UNSIGNED NOT NULL,
    gate_device_id          BIGINT UNSIGNED NOT NULL,
    gate_id                 BIGINT UNSIGNED NULL,
    client_batch_id         VARCHAR(64)     NOT NULL COMMENT 'device generated uuid, the replay guard',
    kind                    VARCHAR(16)     NOT NULL,
    direction               VARCHAR(8)      NOT NULL,
    status                  VARCHAR(16)     NOT NULL DEFAULT 'pending',
    started_at              DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    finished_at             DATETIME(6)     NULL,
    record_count            INT UNSIGNED    NOT NULL DEFAULT 0,
    accepted_count          INT UNSIGNED    NOT NULL DEFAULT 0,
    duplicate_count         INT UNSIGNED    NOT NULL DEFAULT 0,
    rejected_count          INT UNSIGNED    NOT NULL DEFAULT 0,
    conflict_count          INT UNSIGNED    NOT NULL DEFAULT 0,
    device_first_at         DATETIME(6)     NULL COMMENT 'earliest local timestamp in the batch',
    device_last_at          DATETIME(6)     NULL,
    cache_version_from      BIGINT UNSIGNED NULL COMMENT 'what the device held when it went offline',
    cache_version_to        BIGINT UNSIGNED NULL COMMENT 'what it should hold next',
    checksum_sha256         CHAR(64)        NULL,
    error_code              VARCHAR(48)     NULL,
    error_message           VARCHAR(512)    NULL,
    requested_by_user_id    BIGINT UNSIGNED NULL,
    created_at              DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at              DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY uk_device_sync_batches (tenant_id, gate_device_id, client_batch_id),
    KEY idx_device_sync_batches_status (status, started_at),
    KEY idx_device_sync_batches_gate (gate_id, started_at),
    CONSTRAINT fk_device_sync_batches_tenant FOREIGN KEY (tenant_id) REFERENCES tenants (id) ON DELETE CASCADE,
    CONSTRAINT fk_device_sync_batches_device FOREIGN KEY (gate_device_id) REFERENCES gate_devices (id) ON DELETE CASCADE,
    CONSTRAINT fk_device_sync_batches_gate FOREIGN KEY (gate_id) REFERENCES gates (id) ON DELETE SET NULL,
    CONSTRAINT fk_device_sync_batches_user FOREIGN KEY (requested_by_user_id) REFERENCES users (id) ON DELETE SET NULL,
    CONSTRAINT chk_device_sync_batches_kind CHECK (kind IN ('movements', 'blacklist', 'passes', 'full')),
    CONSTRAINT chk_device_sync_batches_direction CHECK (direction IN ('upload', 'download')),
    CONSTRAINT chk_device_sync_batches_status CHECK (status IN ('pending', 'processing', 'completed', 'partial', 'failed', 'superseded'))
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci ROW_FORMAT = DYNAMIC;

CREATE TABLE IF NOT EXISTS offline_cache_manifests (
    id                      BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    tenant_id               BIGINT UNSIGNED NOT NULL,
    gate_device_id          BIGINT UNSIGNED NOT NULL,
    gate_id                 BIGINT UNSIGNED NOT NULL,
    pass_id                 BIGINT UNSIGNED NOT NULL,
    token_hash              CHAR(64)        NULL COMMENT 'the secret the device must accept, hash only',
    reason                  VARCHAR(24)     NOT NULL DEFAULT 'valid_today',
    cache_version           BIGINT UNSIGNED NOT NULL DEFAULT 1,
    valid_from              DATETIME(6)     NOT NULL,
    valid_until             DATETIME(6)     NOT NULL,
    pushed_at               DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    superseded_at           DATETIME(6)     NULL,
    created_at              DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY uk_offline_cache_manifest (gate_device_id, pass_id, cache_version),
    KEY idx_offline_cache_validity (tenant_id, gate_id, valid_until),
    KEY idx_offline_cache_token (token_hash),
    CONSTRAINT fk_offline_cache_device FOREIGN KEY (gate_device_id) REFERENCES gate_devices (id) ON DELETE CASCADE,
    CONSTRAINT fk_offline_cache_gate FOREIGN KEY (gate_id) REFERENCES gates (id) ON DELETE CASCADE,
    CONSTRAINT fk_offline_cache_pass FOREIGN KEY (pass_id) REFERENCES passes (id) ON DELETE CASCADE,
    CONSTRAINT chk_offline_cache_reason CHECK (reason IN ('valid_today', 'valid_range', 'multi_entry', 'blacklist', 'watchlist')),
    CONSTRAINT chk_offline_cache_window CHECK (valid_until > valid_from)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci ROW_FORMAT = DYNAMIC;

CREATE TABLE IF NOT EXISTS sync_conflicts (
    id                      BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    tenant_id               BIGINT UNSIGNED NOT NULL,
    device_sync_batch_id    BIGINT UNSIGNED NOT NULL,
    gate_device_id          BIGINT UNSIGNED NOT NULL,
    conflict_type           VARCHAR(24)     NOT NULL,
    device_movement_uuid    CHAR(36)        NULL,
    movement_id             BIGINT UNSIGNED NULL COMMENT 'the server row that already existed',
    pass_id                 BIGINT UNSIGNED NULL,
    detail                  JSON            NULL COMMENT 'device row against server row, diffed',
    resolution              VARCHAR(32)     NOT NULL DEFAULT 'server_kept_status',
    resolution_note         VARCHAR(512)    NULL,
    detected_at             DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    resolved_by_user_id     BIGINT UNSIGNED NULL,
    resolved_at             DATETIME(6)     NULL,
    created_at              DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY uk_sync_conflicts_device_uuid (tenant_id, gate_device_id, device_movement_uuid),
    KEY idx_sync_conflicts_batch (device_sync_batch_id, conflict_type),
    KEY idx_sync_conflicts_open (tenant_id, resolved_at, detected_at),
    CONSTRAINT fk_sync_conflicts_batch FOREIGN KEY (device_sync_batch_id) REFERENCES device_sync_batches (id) ON DELETE CASCADE,
    CONSTRAINT fk_sync_conflicts_device FOREIGN KEY (gate_device_id) REFERENCES gate_devices (id) ON DELETE CASCADE,
    CONSTRAINT fk_sync_conflicts_movement FOREIGN KEY (movement_id) REFERENCES gate_movements (id) ON DELETE SET NULL,
    CONSTRAINT fk_sync_conflicts_pass FOREIGN KEY (pass_id) REFERENCES passes (id) ON DELETE SET NULL,
    CONSTRAINT fk_sync_conflicts_resolver FOREIGN KEY (resolved_by_user_id) REFERENCES users (id) ON DELETE SET NULL,
    CONSTRAINT chk_sync_conflicts_type CHECK (conflict_type IN ('duplicate', 'out_of_order', 'status_mismatch', 'clock_skew', 'unknown_pass', 'revoked_token', 'gate_mismatch', 'quota_exceeded')),
    CONSTRAINT chk_sync_conflicts_resolution CHECK (resolution IN ('server_kept_status', 'kept_both_records', 'marked_late', 'dropped_duplicate', 'rejected', 'manual_review'))
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci ROW_FORMAT = DYNAMIC;

-- What a device still owes the server, and how stale it is.
CREATE OR REPLACE VIEW v_device_sync_health AS
SELECT  gd.id AS gate_device_id,
        gd.tenant_id,
        gd.label,
        gd.gate_id,
        g.name AS gate_name,
        gd.sync_status,
        gd.last_seen_at,
        gd.last_sync_at,
        gd.cache_version,
        (SELECT COUNT(*) FROM device_sync_batches b WHERE b.gate_device_id = gd.id AND b.status IN ('pending', 'processing', 'partial', 'failed')) AS open_batches,
        (SELECT COUNT(*) FROM sync_conflicts c WHERE c.gate_device_id = gd.id AND c.resolved_at IS NULL) AS unresolved_conflicts
FROM    gate_devices gd
JOIN    gates g               ON g.id = gd.gate_id
WHERE   gd.is_active = 1;
