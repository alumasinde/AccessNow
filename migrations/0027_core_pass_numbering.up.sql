-- 0027 core / pass numbering — per tenant pass number formats and their counters
--
-- A tenant owns the shape of the reference printed on a badge. One format row per
-- scope (material_outward_rgp, visitor, vehicle, staff_outpass) and one counter row
-- per format per period. Allocation is a single atomic statement, so two guards at
-- two gates cannot be handed the same number.
--
-- Re-runnable: CREATE TABLE IF NOT EXISTS only.

CREATE TABLE IF NOT EXISTS pass_number_formats (
    id                  BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    tenant_id           BIGINT UNSIGNED NOT NULL,
    format_key          VARCHAR(64)     NOT NULL COMMENT 'scope this format issues for, e.g. material_outward_rgp',
    name                VARCHAR(128)    NOT NULL,
    pattern             VARCHAR(128)    NOT NULL COMMENT 'tokens: {PREFIX} {CATEGORY} {MODULE} {YYYY} {YY} {MM} {DD} {SEQ}',
    prefix              VARCHAR(16)     NULL COMMENT 'fills {PREFIX}, kept separate so it can be edited without touching the pattern',
    category_key        VARCHAR(64)     NULL COMMENT 'fills {CATEGORY}',
    module_key          VARCHAR(64)     NULL COMMENT 'fills {MODULE}',
    sequence_width      TINYINT UNSIGNED NOT NULL DEFAULT 4 COMMENT 'zero padding for {SEQ}',
    -- separator_char, not separator: SEPARATOR is a reserved word in MySQL and MariaDB.
    separator_char      VARCHAR(4)      NOT NULL DEFAULT '-',
    reset_period        VARCHAR(16)     NOT NULL DEFAULT 'yearly' COMMENT 'counter period, not the validity window',
    gap_required        TINYINT(1)      NOT NULL DEFAULT 0 COMMENT '1 when an auditor may count the numbers with no gaps',
    is_active           TINYINT(1)      NOT NULL DEFAULT 1,
    created_at          DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at          DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY uk_pass_number_formats_key (tenant_id, format_key),
    UNIQUE KEY uk_pass_number_formats_id_tenant (id, tenant_id),
    KEY idx_pass_number_formats_active (tenant_id, is_active, category_key),
    CONSTRAINT fk_pass_number_formats_tenant FOREIGN KEY (tenant_id) REFERENCES tenants (id) ON DELETE CASCADE,
    CONSTRAINT fk_pass_number_formats_category FOREIGN KEY (category_key) REFERENCES pass_categories (category_key) ON DELETE RESTRICT ON UPDATE CASCADE,
    CONSTRAINT fk_pass_number_formats_module FOREIGN KEY (module_key) REFERENCES modules (module_key) ON DELETE RESTRICT ON UPDATE CASCADE,
    CONSTRAINT chk_pass_number_formats_width CHECK (sequence_width BETWEEN 1 AND 9),
    CONSTRAINT chk_pass_number_formats_reset CHECK (reset_period IN ('none', 'daily', 'monthly', 'yearly')),
    CONSTRAINT chk_pass_number_formats_gap CHECK (gap_required IN (0, 1) AND is_active IN (0, 1))
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci ROW_FORMAT = DYNAMIC;

CREATE TABLE IF NOT EXISTS pass_number_counters (
    id                  BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    tenant_id           BIGINT UNSIGNED NOT NULL,
    format_id           BIGINT UNSIGNED NOT NULL,
    period_key          VARCHAR(8)      NOT NULL DEFAULT '' COMMENT '2026, 202611 or 20261105, empty when reset_period is none',
    last_value          BIGINT UNSIGNED NOT NULL DEFAULT 0,
    step                SMALLINT UNSIGNED NOT NULL DEFAULT 1,
    created_at          DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at          DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY uk_pass_number_counters_scope (format_id, period_key),
    UNIQUE KEY uk_pass_number_counters_id_tenant (id, tenant_id),
    KEY idx_pass_number_counters_tenant (tenant_id, format_id, period_key),
    CONSTRAINT fk_pass_number_counters_tenant FOREIGN KEY (tenant_id) REFERENCES tenants (id) ON DELETE CASCADE,
    CONSTRAINT fk_pass_number_counters_format FOREIGN KEY (format_id) REFERENCES pass_number_formats (id) ON DELETE CASCADE,
    CONSTRAINT chk_pass_number_counters_step CHECK (step BETWEEN 1 AND 1000)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci ROW_FORMAT = DYNAMIC;
