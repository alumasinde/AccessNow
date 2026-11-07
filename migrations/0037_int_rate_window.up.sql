-- =============================================================================
-- 0037 module / integrations & API — shared machine rate window
-- One row per credential. Every API instance spends and reads the same row, so
-- a key's budget is the key's, not each process's own copy of it.
-- Re-runnable: CREATE TABLE IF NOT EXISTS only.
-- =============================================================================

CREATE TABLE IF NOT EXISTS api_key_rate_windows (
    api_key_id              BIGINT UNSIGNED NOT NULL,
    window_start            DATETIME(6)     NOT NULL,
    hits                    INT UNSIGNED    NOT NULL DEFAULT 0,
    updated_at              DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    PRIMARY KEY (api_key_id),
    CONSTRAINT fk_api_key_rate_windows_key FOREIGN KEY (api_key_id) REFERENCES api_keys (id) ON DELETE CASCADE
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci ROW_FORMAT = DYNAMIC;
