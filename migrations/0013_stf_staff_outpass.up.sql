-- =============================================================================
-- 0013 module / staff outpass — employees stepping out during working hours
-- Employee requests, supervisor approves, guard scans the exit. The return scan
-- is optional per tenant, and anyone not back by end of shift is flagged.
-- Duration limits hang off the reason, so a 20 minute pharmacy run and a two
-- hour errand are judged differently.
-- Re-runnable: CREATE TABLE IF NOT EXISTS only.
-- =============================================================================

CREATE TABLE IF NOT EXISTS outpass_reasons (
    id                      SMALLINT UNSIGNED NOT NULL AUTO_INCREMENT,
    reason_key              VARCHAR(64)     NOT NULL,
    name                    VARCHAR(128)    NOT NULL,
    default_duration_minutes INT UNSIGNED   NULL,
    requires_manager        TINYINT(1)      NOT NULL DEFAULT 0,
    requires_return_scan    TINYINT(1)      NOT NULL DEFAULT 1,
    sort_order              SMALLINT        NOT NULL DEFAULT 100,
    status                  VARCHAR(16)     NOT NULL DEFAULT 'active',
    created_at              DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at              DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY uk_outpass_reasons_key (reason_key),
    CONSTRAINT chk_outpass_reasons_flags CHECK (requires_manager IN (0, 1) AND requires_return_scan IN (0, 1)),
    CONSTRAINT chk_outpass_reasons_status CHECK (status IN ('active', 'inactive'))
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci ROW_FORMAT = DYNAMIC;

CREATE TABLE IF NOT EXISTS outpasses (
    id                      BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    tenant_id               BIGINT UNSIGNED NOT NULL,
    pass_id                 BIGINT UNSIGNED NOT NULL,
    staff_id                BIGINT UNSIGNED NOT NULL,
    department_id           BIGINT UNSIGNED NULL,
    site_id                 BIGINT UNSIGNED NOT NULL,
    reason_key              VARCHAR(64)     NOT NULL,
    reason_note             VARCHAR(512)    NULL,
    destination             VARCHAR(255)    NULL,
    vehicle_plate           VARCHAR(32)     NULL,
    is_company_vehicle      TINYINT(1)      NOT NULL DEFAULT 0,
    requested_at            DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    approved_at             DATETIME(6)     NULL,
    approved_by_user_id     BIGINT UNSIGNED NULL,
    rejection_reason        VARCHAR(512)    NULL,
    expected_duration_minutes INT UNSIGNED  NULL COMMENT 'snapshot of the reason limit at request time',
    expected_return_at      DATETIME(6)     NULL,
    exited_at               DATETIME(6)     NULL,
    exit_gate_id            BIGINT UNSIGNED NULL,
    exit_movement_id        BIGINT UNSIGNED NULL,
    return_scan_required    TINYINT(1)      NOT NULL DEFAULT 1,
    returned_at             DATETIME(6)     NULL,
    return_gate_id          BIGINT UNSIGNED NULL,
    return_movement_id      BIGINT UNSIGNED NULL,
    actual_duration_minutes INT UNSIGNED    NULL,
    is_over_duration        TINYINT(1)      NOT NULL DEFAULT 0,
    end_of_shift_at         DATETIME(6)     NULL COMMENT 'the unreturned-by-end-of-shift marker compares against this',
    is_unreturned           TINYINT(1)      NOT NULL DEFAULT 0,
    status                  VARCHAR(24)     NOT NULL DEFAULT 'pending',
    notes                   TEXT            NULL,
    created_at              DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at              DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY uk_outpasses_pass (pass_id),
    KEY idx_outpasses_staff (tenant_id, staff_id, requested_at),
    KEY idx_outpasses_out (tenant_id, status, expected_return_at),
    KEY idx_outpasses_unreturned (tenant_id, is_unreturned, status),
    KEY idx_outpasses_department (tenant_id, department_id, requested_at),
    CONSTRAINT fk_outpasses_pass FOREIGN KEY (pass_id, tenant_id) REFERENCES passes (id, tenant_id) ON DELETE CASCADE,
    CONSTRAINT fk_outpasses_staff FOREIGN KEY (staff_id, tenant_id) REFERENCES staff (id, tenant_id) ON DELETE RESTRICT,
    CONSTRAINT fk_outpasses_department FOREIGN KEY (department_id) REFERENCES departments (id) ON DELETE SET NULL,
    CONSTRAINT fk_outpasses_site FOREIGN KEY (site_id, tenant_id) REFERENCES sites (id, tenant_id) ON DELETE RESTRICT,
    CONSTRAINT fk_outpasses_reason FOREIGN KEY (reason_key) REFERENCES outpass_reasons (reason_key) ON DELETE RESTRICT ON UPDATE CASCADE,
    CONSTRAINT fk_outpasses_approver FOREIGN KEY (approved_by_user_id) REFERENCES users (id) ON DELETE SET NULL,
    CONSTRAINT fk_outpasses_exit_gate FOREIGN KEY (exit_gate_id) REFERENCES gates (id) ON DELETE SET NULL,
    CONSTRAINT fk_outpasses_return_gate FOREIGN KEY (return_gate_id) REFERENCES gates (id) ON DELETE SET NULL,
    CONSTRAINT fk_outpasses_exit_movement FOREIGN KEY (exit_movement_id) REFERENCES gate_movements (id) ON DELETE SET NULL,
    CONSTRAINT fk_outpasses_return_movement FOREIGN KEY (return_movement_id) REFERENCES gate_movements (id) ON DELETE SET NULL,
    CONSTRAINT chk_outpasses_status CHECK (status IN ('pending', 'approved', 'rejected', 'cancelled', 'out', 'returned', 'overdue', 'unreturned')),
    CONSTRAINT chk_outpasses_flags CHECK (is_company_vehicle IN (0, 1) AND return_scan_required IN (0, 1) AND is_over_duration IN (0, 1) AND is_unreturned IN (0, 1))
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci ROW_FORMAT = DYNAMIC;

-- "Who is out right now and who has not come back"
CREATE OR REPLACE VIEW v_staff_out_of_site AS
SELECT  o.tenant_id,
        o.id AS outpass_id,
        o.pass_id,
        p.pass_no,
        o.staff_id,
        s.first_name,
        s.last_name,
        s.employee_no,
        o.department_id,
        o.reason_key,
        o.expected_return_at,
        o.exited_at,
        TIMESTAMPDIFF(MINUTE, o.exited_at, UTC_TIMESTAMP()) AS minutes_out,
        o.status
FROM    outpasses o
JOIN    passes p              ON p.id = o.pass_id
JOIN    staff s               ON s.id = o.staff_id
WHERE   o.status IN ('out', 'overdue', 'unreturned');
