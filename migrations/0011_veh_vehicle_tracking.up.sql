-- =============================================================================
-- 0011 module / vehicle tracking — registered fleet and one-off plates
-- plate_normalized is the comparison key: uppercase, no spaces or separators.
-- The app normalises before insert, e.g. "KCA 123M" and "kca-123 m" match.
-- Owner references stay untyped integers on purpose: this module must not take a
-- foreign key into the visitor or contractor modules, or it could not run alone.
-- Re-runnable: CREATE TABLE IF NOT EXISTS only.
-- =============================================================================

CREATE TABLE IF NOT EXISTS vehicles (
    id                      BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    tenant_id               BIGINT UNSIGNED NOT NULL,
    plate_normalized        VARCHAR(32)     NOT NULL,
    plate_display           VARCHAR(32)     NOT NULL,
    country_code            CHAR(2)         NULL,
    make                    VARCHAR(64)     NULL,
    model                   VARCHAR(64)     NULL,
    color                   VARCHAR(32)     NULL,
    vehicle_class           VARCHAR(24)     NOT NULL DEFAULT 'car',
    body_type               VARCHAR(32)     NULL,
    is_fleet                TINYINT(1)      NOT NULL DEFAULT 0 COMMENT 'registered fleet skips the details form',
    owner_type              VARCHAR(16)     NOT NULL DEFAULT 'unknown',
    owner_user_id           BIGINT UNSIGNED NULL,
    owner_staff_id          BIGINT UNSIGNED NULL,
    owner_label             VARCHAR(255)    NULL COMMENT 'company or firm name when there is no record',
    owner_ref_id            BIGINT UNSIGNED NULL COMMENT 'cross-module id, resolved only when that module is on',
    insurance_expiry        DATE            NULL,
    fitness_expiry          DATE            NULL,
    permit_required         TINYINT(1)      NOT NULL DEFAULT 0,
    default_dwell_limit_minutes INT UNSIGNED NULL,
    status                  VARCHAR(16)     NOT NULL DEFAULT 'active',
    notes                   TEXT            NULL,
    external_ref            VARCHAR(128)    NULL,
    created_by              BIGINT UNSIGNED NULL,
    created_at              DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at              DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    deleted_at              DATETIME(6)     NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_vehicles_plate (tenant_id, plate_normalized),
    UNIQUE KEY uk_vehicles_external_ref (tenant_id, external_ref),
    UNIQUE KEY uk_vehicles_id_tenant (id, tenant_id),
    KEY idx_vehicles_owner (tenant_id, owner_type, owner_ref_id),
    KEY idx_vehicles_fleet (tenant_id, is_fleet, status),
    KEY idx_vehicles_expiry (tenant_id, insurance_expiry),
    CONSTRAINT fk_vehicles_tenant FOREIGN KEY (tenant_id) REFERENCES tenants (id) ON DELETE CASCADE,
    CONSTRAINT fk_vehicles_owner_user FOREIGN KEY (owner_user_id) REFERENCES users (id) ON DELETE SET NULL,
    CONSTRAINT fk_vehicles_owner_staff FOREIGN KEY (owner_staff_id) REFERENCES staff (id) ON DELETE SET NULL,
    CONSTRAINT fk_vehicles_creator FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE SET NULL,
    CONSTRAINT chk_vehicles_class CHECK (vehicle_class IN ('car', 'motorcycle', 'van', 'pickup', 'truck', 'bus', 'trailer', 'heavy_machinery', 'bicycle', 'other')),
    CONSTRAINT chk_vehicles_owner_type CHECK (owner_type IN ('staff', 'contractor', 'company', 'visitor', 'tenant', 'unknown')),
    CONSTRAINT chk_vehicles_status CHECK (status IN ('active', 'blocked', 'archived')),
    CONSTRAINT chk_vehicles_fleet CHECK (is_fleet IN (0, 1) AND permit_required IN (0, 1))
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci ROW_FORMAT = DYNAMIC;

CREATE TABLE IF NOT EXISTS vehicle_passes (
    id                      BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    tenant_id               BIGINT UNSIGNED NOT NULL,
    pass_id                 BIGINT UNSIGNED NOT NULL,
    vehicle_id              BIGINT UNSIGNED NULL,
    plate_normalized        VARCHAR(32)     NOT NULL,
    plate_display           VARCHAR(32)     NULL,
    is_fleet_snapshot       TINYINT(1)      NOT NULL DEFAULT 0,
    driver_name             VARCHAR(255)    NULL,
    driver_phone            VARCHAR(32)     NULL,
    driver_id_type          VARCHAR(24)     NULL,
    driver_id_number        VARCHAR(64)     NULL,
    driver_id_purged_at     DATETIME(6)     NULL,
    driver_staff_id         BIGINT UNSIGNED NULL,
    escort_name             VARCHAR(128)    NULL,
    purpose                 VARCHAR(512)    NULL,
    cargo_description       VARCHAR(512)    NULL,
    container_no            VARCHAR(64)     NULL,
    seal_no                 VARCHAR(64)     NULL,
    expected_entry_at       DATETIME(6)     NULL,
    expected_exit_at        DATETIME(6)     NULL,
    entered_at              DATETIME(6)     NULL,
    exited_at               DATETIME(6)     NULL,
    entry_gate_id           BIGINT UNSIGNED NULL,
    exit_gate_id            BIGINT UNSIGNED NULL,
    dwell_limit_minutes     INT UNSIGNED    NULL COMMENT 'threshold snapshot taken from the tenant or vehicle rule',
    is_overstaying          TINYINT(1)      NOT NULL DEFAULT 0,
    overstay_flagged_at     DATETIME(6)     NULL,
    actual_dwell_minutes    INT UNSIGNED    NULL,
    status                  VARCHAR(24)     NOT NULL DEFAULT 'pending',
    notes                   TEXT            NULL,
    created_at              DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at              DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY uk_vehicle_passes_pass (pass_id),
    KEY idx_vehicle_passes_plate (tenant_id, plate_normalized, status),
    KEY idx_vehicle_passes_vehicle (vehicle_id),
    KEY idx_vehicle_passes_on_site (tenant_id, is_overstaying, entered_at),
    CONSTRAINT fk_vehicle_passes_pass FOREIGN KEY (pass_id, tenant_id) REFERENCES passes (id, tenant_id) ON DELETE CASCADE,
    CONSTRAINT fk_vehicle_passes_vehicle FOREIGN KEY (vehicle_id, tenant_id) REFERENCES vehicles (id, tenant_id) ON DELETE RESTRICT,
    CONSTRAINT fk_vehicle_passes_driver_staff FOREIGN KEY (driver_staff_id) REFERENCES staff (id) ON DELETE SET NULL,
    CONSTRAINT fk_vehicle_passes_entry_gate FOREIGN KEY (entry_gate_id) REFERENCES gates (id) ON DELETE SET NULL,
    CONSTRAINT fk_vehicle_passes_exit_gate FOREIGN KEY (exit_gate_id) REFERENCES gates (id) ON DELETE SET NULL,
    CONSTRAINT chk_vehicle_passes_status CHECK (status IN ('pending', 'approved', 'rejected', 'cancelled', 'on_site', 'completed', 'overdue', 'expired')),
    CONSTRAINT chk_vehicle_passes_id_type CHECK (driver_id_type IS NULL OR driver_id_type IN ('national_id', 'passport', 'drivers_licence', 'work_permit', 'other')),
    CONSTRAINT chk_vehicle_passes_flags CHECK (is_fleet_snapshot IN (0, 1) AND is_overstaying IN (0, 1))
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci ROW_FORMAT = DYNAMIC;

-- The roll-call reads this. The flag is computed, not read from the column: is_overstaying
-- is what a scan or a sweep stamps for the record, and a vehicle already past the limit it
-- was given has to show on the roster before anybody stamps it.
CREATE OR REPLACE VIEW v_vehicles_on_site AS
SELECT  vp.id,
        vp.tenant_id,
        vp.pass_id,
        p.pass_no,
        vp.vehicle_id,
        vp.plate_normalized,
        vp.plate_display,
        vp.driver_name,
        vp.purpose,
        vp.entered_at,
        vp.dwell_limit_minutes,
        IF(vp.dwell_limit_minutes IS NOT NULL
           AND TIMESTAMPDIFF(MINUTE, vp.entered_at, UTC_TIMESTAMP()) >= vp.dwell_limit_minutes,
           1, 0) AS is_overstaying,
        TIMESTAMPDIFF(MINUTE, vp.entered_at, UTC_TIMESTAMP()) AS current_dwell_minutes
FROM    vehicle_passes vp
JOIN    passes p              ON p.id = vp.pass_id
WHERE   vp.status IN ('on_site', 'overdue');
