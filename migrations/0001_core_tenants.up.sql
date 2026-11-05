-- =============================================================================
-- 0001 core / tenants — tenant registry, sellable module catalogue, module flags
-- Re-runnable: CREATE TABLE IF NOT EXISTS only, no ALTER, no DROP-and-recreate.
-- =============================================================================

CREATE TABLE IF NOT EXISTS tenants (
    id                  BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    slug                VARCHAR(63)     NOT NULL COMMENT 'stable tenant identifier used by subdomain and API',
    name                VARCHAR(255)    NOT NULL,
    legal_name          VARCHAR(255)    NULL,
    timezone            VARCHAR(64)     NOT NULL DEFAULT 'Africa/Nairobi' COMMENT 'all business dates resolve in this zone',
    locale              VARCHAR(10)     NOT NULL DEFAULT 'en',
    currency_code       CHAR(3)         NOT NULL DEFAULT 'KES',
    status              VARCHAR(24)     NOT NULL DEFAULT 'active',
    contact_email       VARCHAR(255)    NULL,
    contact_phone       VARCHAR(32)     NULL,
    licence_start       DATE            NULL,
    licence_end         DATE            NULL,
    settings            JSON            NULL COMMENT 'tenant-wide policy, e.g. same_gate_exit_required, return_scan_required, cross_gate_allowed',
    created_at          DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at          DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    deleted_at          DATETIME(6)     NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_tenants_slug (slug),
    KEY idx_tenants_status (status, deleted_at),
    KEY idx_tenants_licence_end (licence_end),
    CONSTRAINT chk_tenants_status CHECK (status IN ('active', 'trial', 'suspended', 'archived'))
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci ROW_FORMAT = DYNAMIC;

CREATE TABLE IF NOT EXISTS modules (
    id                  SMALLINT UNSIGNED NOT NULL AUTO_INCREMENT,
    module_key          VARCHAR(64)     NOT NULL,
    name                VARCHAR(128)    NOT NULL,
    summary             VARCHAR(255)    NULL,
    is_core             TINYINT(1)      NOT NULL DEFAULT 0 COMMENT 'core is always on and is never sold',
    default_enabled     TINYINT(1)      NOT NULL DEFAULT 0,
    sort_order          SMALLINT        NOT NULL DEFAULT 100,
    status              VARCHAR(16)     NOT NULL DEFAULT 'available',
    created_at          DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at          DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY uk_modules_key (module_key),
    KEY idx_modules_status (status, sort_order),
    CONSTRAINT chk_modules_status CHECK (status IN ('available', 'beta', 'retired')),
    CONSTRAINT chk_modules_core CHECK (is_core IN (0, 1))
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci ROW_FORMAT = DYNAMIC;

CREATE TABLE IF NOT EXISTS tenant_modules (
    id                  BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    tenant_id           BIGINT UNSIGNED NOT NULL,
    module_key          VARCHAR(64)     NOT NULL,
    enabled             TINYINT(1)      NOT NULL DEFAULT 0,
    settings            JSON            NULL COMMENT 'per-tenant module overrides, e.g. dwell_limit_minutes, return_scan_required',
    activated_at        DATETIME(6)     NULL,
    deactivated_at      DATETIME(6)     NULL,
    created_at          DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at          DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY uk_tenant_modules (tenant_id, module_key),
    KEY idx_tenant_modules_enabled (module_key, enabled),
    CONSTRAINT fk_tenant_modules_tenant FOREIGN KEY (tenant_id) REFERENCES tenants (id) ON DELETE CASCADE,
    CONSTRAINT fk_tenant_modules_module FOREIGN KEY (module_key) REFERENCES modules (module_key) ON DELETE RESTRICT ON UPDATE CASCADE,
    CONSTRAINT chk_tenant_modules_enabled CHECK (enabled IN (0, 1)),
    CONSTRAINT chk_tenant_modules_window CHECK (deactivated_at IS NULL OR activated_at IS NULL OR deactivated_at >= activated_at)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci ROW_FORMAT = DYNAMIC;
