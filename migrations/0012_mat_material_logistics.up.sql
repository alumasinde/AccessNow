-- =============================================================================
-- 0012 module / material & asset logistics — RGP, NRGP and inward declarations
-- Outward needs approval before it may leave. Line items carry quantities so a
-- partial return works. Returnable lines get an expected return date, and the
-- nightly job in the tenant timezone marks them overdue and alerts the issuer.
-- An inward personal item declared at the gate must be verified at exit, and the
-- exception path (item left behind, taken by someone else) is an item event.
-- Re-runnable: CREATE TABLE IF NOT EXISTS only.
-- =============================================================================

CREATE TABLE IF NOT EXISTS material_passes (
    id                      BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    tenant_id               BIGINT UNSIGNED NOT NULL,
    pass_id                 BIGINT UNSIGNED NOT NULL,
    kind                    VARCHAR(12)     NOT NULL,
    return_type             VARCHAR(8)      NOT NULL,
    item_category           VARCHAR(64)     NULL COMMENT 'asset, consumable, spare, luggage, sample',
    declared_by_name        VARCHAR(255)    NULL,
    declared_by_staff_id    BIGINT UNSIGNED NULL,
    carrier_name            VARCHAR(255)    NULL,
    carrier_phone           VARCHAR(32)     NULL,
    driver_name             VARCHAR(255)    NULL,
    vehicle_plate           VARCHAR(32)     NULL,
    waybill_ref             VARCHAR(64)     NULL,
    invoice_ref             VARCHAR(64)     NULL,
    po_ref                  VARCHAR(64)     NULL,
    origin_reference        VARCHAR(255)    NULL,
    destination_reference   VARCHAR(255)    NULL,
    destination_contact     VARCHAR(255)    NULL,
    gate_verification_required TINYINT(1)   NOT NULL DEFAULT 1,
    seal_no                 VARCHAR(64)     NULL,
    expected_return_at      DATETIME(6)     NULL,
    released_at             DATETIME(6)     NULL,
    released_gate_id        BIGINT UNSIGNED NULL,
    closed_at               DATETIME(6)     NULL,
    verified_at             DATETIME(6)     NULL,
    verified_by_user_id     BIGINT UNSIGNED NULL,
    status                  VARCHAR(24)     NOT NULL DEFAULT 'pending',
    notes                   TEXT            NULL,
    metadata                JSON            NULL,
    created_at              DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at              DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY uk_material_passes_pass (pass_id),
    UNIQUE KEY uk_material_passes_id_tenant (id, tenant_id),
    KEY idx_material_passes_tenant (tenant_id, kind, return_type, status),
    KEY idx_material_passes_return (tenant_id, status, expected_return_at),
    KEY idx_material_passes_waybill (tenant_id, waybill_ref),
    CONSTRAINT fk_material_passes_pass FOREIGN KEY (pass_id, tenant_id) REFERENCES passes (id, tenant_id) ON DELETE CASCADE,
    CONSTRAINT fk_material_passes_declarer FOREIGN KEY (declared_by_staff_id) REFERENCES staff (id) ON DELETE SET NULL,
    CONSTRAINT fk_material_passes_verifier FOREIGN KEY (verified_by_user_id) REFERENCES users (id) ON DELETE SET NULL,
    CONSTRAINT fk_material_passes_gate FOREIGN KEY (released_gate_id) REFERENCES gates (id) ON DELETE SET NULL,
    CONSTRAINT chk_material_passes_kind CHECK (kind IN ('inward', 'outward')),
    CONSTRAINT chk_material_passes_return_type CHECK (return_type IN ('rgp', 'nrgp')),
    CONSTRAINT chk_material_passes_status CHECK (status IN ('pending', 'approved', 'rejected', 'released', 'partially_returned', 'returned', 'closed', 'overdue', 'disputed')),
    CONSTRAINT chk_material_passes_nrgp CHECK (return_type <> 'nrgp' OR expected_return_at IS NULL),
    CONSTRAINT chk_material_passes_verify CHECK (gate_verification_required IN (0, 1))
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci ROW_FORMAT = DYNAMIC;

CREATE TABLE IF NOT EXISTS material_items (
    id                      BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    tenant_id               BIGINT UNSIGNED NOT NULL,
    material_pass_id        BIGINT UNSIGNED NOT NULL,
    line_no                 SMALLINT UNSIGNED NOT NULL,
    sku                     VARCHAR(64)     NULL,
    asset_tag               VARCHAR(64)     NULL,
    serial_no               VARCHAR(128)    NULL,
    item_name               VARCHAR(255)    NOT NULL,
    description             VARCHAR(512)    NULL,
    category                VARCHAR(64)     NULL,
    unit                    VARCHAR(16)     NOT NULL DEFAULT 'pcs',
    quantity_declared       DECIMAL(14, 3)  NOT NULL DEFAULT 0.000,
    quantity_in             DECIMAL(14, 3)  NOT NULL DEFAULT 0.000 COMMENT 'inward declarations',
    quantity_out            DECIMAL(14, 3)  NOT NULL DEFAULT 0.000 COMMENT 'actually released through the gate',
    quantity_returned       DECIMAL(14, 3)  NOT NULL DEFAULT 0.000,
    unit_value              DECIMAL(14, 2)  NULL,
    line_value              DECIMAL(16, 2)  NULL,
    currency                CHAR(3)         NULL,
    returnable              TINYINT(1)      NOT NULL DEFAULT 0,
    expected_return_at      DATETIME(6)     NULL,
    returned_at             DATETIME(6)     NULL,
    overdue_since           DATETIME(6)     NULL,
    status                  VARCHAR(24)     NOT NULL DEFAULT 'pending',
    notes                   VARCHAR(512)    NULL,
    created_at              DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at              DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY uk_material_items_line (material_pass_id, line_no),
    UNIQUE KEY uk_material_items_id_tenant (id, tenant_id),
    KEY idx_material_items_status (tenant_id, status, expected_return_at),
    KEY idx_material_items_sku (tenant_id, sku),
    KEY idx_material_items_asset_tag (tenant_id, asset_tag),
    CONSTRAINT fk_material_items_pass FOREIGN KEY (material_pass_id, tenant_id) REFERENCES material_passes (id, tenant_id) ON DELETE CASCADE,
    CONSTRAINT chk_material_items_returnable CHECK (returnable IN (0, 1)),
    CONSTRAINT chk_material_items_status CHECK (status IN ('pending', 'declared', 'released', 'partially_returned', 'returned', 'overdue', 'waived', 'lost')),
    CONSTRAINT chk_material_items_quantities CHECK (quantity_declared >= 0 AND quantity_in >= 0 AND quantity_out >= 0 AND quantity_returned >= 0)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci ROW_FORMAT = DYNAMIC;

CREATE TABLE IF NOT EXISTS material_item_events (
    id                      BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    tenant_id               BIGINT UNSIGNED NOT NULL,
    material_pass_id        BIGINT UNSIGNED NOT NULL,
    material_item_id        BIGINT UNSIGNED NOT NULL,
    event                   VARCHAR(24)     NOT NULL,
    quantity                DECIMAL(14, 3)  NOT NULL DEFAULT 0.000,
    occurred_at             DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    gate_id                 BIGINT UNSIGNED NULL,
    movement_id             BIGINT UNSIGNED NULL,
    actor_user_id           BIGINT UNSIGNED NULL,
    reason_code             VARCHAR(32)     NULL COMMENT 'left_behind, taken_by_other, damaged, not_returned, miscount',
    reason_note             VARCHAR(512)    NULL,
    counterparty_name       VARCHAR(255)    NULL COMMENT 'who took the item when someone else carried it out',
    counterparty_staff_id   BIGINT UNSIGNED NULL,
    attachment_path         VARCHAR(512)    NULL,
    created_at              DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    KEY idx_material_item_events_item (material_item_id, occurred_at),
    KEY idx_material_item_events_pass (material_pass_id, occurred_at),
    KEY idx_material_item_events_tenant (tenant_id, event, occurred_at),
    CONSTRAINT fk_material_item_events_pass FOREIGN KEY (material_pass_id, tenant_id) REFERENCES material_passes (id, tenant_id) ON DELETE CASCADE,
    CONSTRAINT fk_material_item_events_item FOREIGN KEY (material_item_id, tenant_id) REFERENCES material_items (id, tenant_id) ON DELETE CASCADE,
    CONSTRAINT fk_material_item_events_gate FOREIGN KEY (gate_id) REFERENCES gates (id) ON DELETE SET NULL,
    CONSTRAINT fk_material_item_events_movement FOREIGN KEY (movement_id) REFERENCES gate_movements (id) ON DELETE SET NULL,
    CONSTRAINT fk_material_item_events_actor FOREIGN KEY (actor_user_id) REFERENCES users (id) ON DELETE SET NULL,
    CONSTRAINT fk_material_item_events_counterparty FOREIGN KEY (counterparty_staff_id) REFERENCES staff (id) ON DELETE SET NULL,
    CONSTRAINT chk_material_item_events_event CHECK (event IN ('declared', 'entry', 'exit', 'verified', 'partial_return', 'returned', 'exception', 'waived')),
    CONSTRAINT chk_material_item_events_quantity CHECK (quantity >= 0)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci ROW_FORMAT = DYNAMIC;

-- Drives the dashboard overdue alert and the nightly item.overdue event.
CREATE OR REPLACE VIEW v_material_returns_outstanding AS
SELECT  mi.tenant_id,
        mi.material_pass_id,
        mp.pass_id,
        p.pass_no,
        mi.id AS material_item_id,
        mi.item_name,
        mi.serial_no,
        mi.quantity_out - mi.quantity_returned AS quantity_outstanding,
        mi.expected_return_at,
        mi.overdue_since,
        mi.status,
        mp.declared_by_staff_id,
        mp.declared_by_name
FROM    material_items mi
JOIN    material_passes mp          ON mp.id = mi.material_pass_id
JOIN    passes p                    ON p.id = mp.pass_id
WHERE   mi.returnable = 1
    AND mi.status IN ('released', 'partially_returned', 'overdue')
    AND mi.quantity_returned < mi.quantity_out;
