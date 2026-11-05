-- =============================================================================
-- 0016 module / approval workflow — multi-level routing for any pass type
-- A flow is matched by pass type, department and declared value. Steps run
-- sequential or parallel, an approver who does not act inside the SLA is
-- escalated, and a delegation made before the request arrived is honoured.
-- Standalone default: a single approver, normally the host or the requester's
-- manager, which the engine models as a one-step flow.
-- Re-runnable: CREATE TABLE IF NOT EXISTS only.
-- =============================================================================

CREATE TABLE IF NOT EXISTS approval_flows (
    id                      BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    tenant_id               BIGINT UNSIGNED NOT NULL,
    name                    VARCHAR(255)    NOT NULL,
    pass_type_id            SMALLINT UNSIGNED NULL COMMENT 'NULL = any type',
    category_key            VARCHAR(64)     NULL COMMENT 'NULL = any category',
    department_id           BIGINT UNSIGNED NULL COMMENT 'NULL = any department',
    site_id                 BIGINT UNSIGNED NULL,
    min_value               DECIMAL(14, 2)  NULL,
    max_value               DECIMAL(14, 2)  NULL,
    currency                CHAR(3)         NULL,
    priority                SMALLINT        NOT NULL DEFAULT 100 COMMENT 'lowest wins when several flows match',
    is_default              TINYINT(1)      NOT NULL DEFAULT 0 COMMENT 'fallback when nothing else matches',
    status                  VARCHAR(16)     NOT NULL DEFAULT 'active',
    description             VARCHAR(512)    NULL,
    created_by              BIGINT UNSIGNED NULL,
    created_at              DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at              DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    deleted_at              DATETIME(6)     NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_approval_flows_id_tenant (id, tenant_id),
    KEY idx_approval_flows_match (tenant_id, status, pass_type_id, priority),
    KEY idx_approval_flows_value (tenant_id, min_value, max_value),
    CONSTRAINT fk_approval_flows_tenant FOREIGN KEY (tenant_id) REFERENCES tenants (id) ON DELETE CASCADE,
    CONSTRAINT fk_approval_flows_pass_type FOREIGN KEY (pass_type_id) REFERENCES pass_types (id) ON DELETE RESTRICT,
    CONSTRAINT fk_approval_flows_category FOREIGN KEY (category_key) REFERENCES pass_categories (category_key) ON DELETE SET NULL ON UPDATE CASCADE,
    CONSTRAINT fk_approval_flows_department FOREIGN KEY (department_id) REFERENCES departments (id) ON DELETE SET NULL,
    CONSTRAINT fk_approval_flows_site FOREIGN KEY (site_id) REFERENCES sites (id) ON DELETE SET NULL,
    CONSTRAINT fk_approval_flows_creator FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE SET NULL,
    CONSTRAINT chk_approval_flows_value CHECK (max_value IS NULL OR min_value IS NULL OR max_value >= min_value),
    CONSTRAINT chk_approval_flows_status CHECK (status IN ('active', 'inactive')),
    CONSTRAINT chk_approval_flows_default CHECK (is_default IN (0, 1))
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci ROW_FORMAT = DYNAMIC;

CREATE TABLE IF NOT EXISTS approval_flow_steps (
    id                      BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    tenant_id               BIGINT UNSIGNED NOT NULL,
    approval_flow_id        BIGINT UNSIGNED NOT NULL,
    step_no                 SMALLINT UNSIGNED NOT NULL,
    name                    VARCHAR(128)    NOT NULL,
    approver_type           VARCHAR(24)     NOT NULL COMMENT 'role, user, supervisor, host, department_head, requester_manager',
    approver_role_key       VARCHAR(64)     NULL,
    approver_user_id        BIGINT UNSIGNED NULL,
    mode                    VARCHAR(16)     NOT NULL DEFAULT 'sequential' COMMENT 'parallel steps at the same step_no all run',
    requires_all            TINYINT(1)      NOT NULL DEFAULT 0 COMMENT 'parallel step needs every assignee, not any one',
    sla_hours               SMALLINT UNSIGNED NULL,
    escalate_after_hours    SMALLINT UNSIGNED NULL,
    escalate_to_role_key    VARCHAR(64)     NULL,
    escalate_to_user_id     BIGINT UNSIGNED NULL,
    allow_self_approval     TINYINT(1)      NOT NULL DEFAULT 0,
    require_comment         TINYINT(1)      NOT NULL DEFAULT 0,
    memo                    VARCHAR(512)    NULL,
    is_active               TINYINT(1)      NOT NULL DEFAULT 1,
    created_at              DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at              DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY uk_approval_flow_steps_no (approval_flow_id, step_no, name),
    KEY idx_approval_flow_steps_flow (approval_flow_id, is_active, step_no),
    CONSTRAINT fk_approval_flow_steps_flow FOREIGN KEY (approval_flow_id, tenant_id) REFERENCES approval_flows (id, tenant_id) ON DELETE CASCADE,
    CONSTRAINT fk_approval_flow_steps_user FOREIGN KEY (approver_user_id) REFERENCES users (id) ON DELETE SET NULL,
    CONSTRAINT fk_approval_flow_steps_esc_user FOREIGN KEY (escalate_to_user_id) REFERENCES users (id) ON DELETE SET NULL,
    CONSTRAINT chk_approval_flow_steps_type CHECK (approver_type IN ('role', 'user', 'supervisor', 'host', 'department_head', 'requester_manager')),
    CONSTRAINT chk_approval_flow_steps_mode CHECK (mode IN ('sequential', 'parallel', 'any_one')),
    CONSTRAINT chk_approval_flow_steps_flags CHECK (requires_all IN (0, 1) AND allow_self_approval IN (0, 1) AND require_comment IN (0, 1) AND is_active IN (0, 1)),
    CONSTRAINT chk_approval_flow_steps_target CHECK (approver_type <> 'role' OR approver_role_key IS NOT NULL)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci ROW_FORMAT = DYNAMIC;

CREATE TABLE IF NOT EXISTS approval_requests (
    id                      BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    tenant_id               BIGINT UNSIGNED NOT NULL,
    pass_id                 BIGINT UNSIGNED NOT NULL,
    approval_flow_id        BIGINT UNSIGNED NULL COMMENT 'NULL = the standalone single approver default',
    flow_snapshot           JSON            NULL COMMENT 'steps as they were when the request started, so a later flow edit does not rewrite history',
    status                  VARCHAR(24)     NOT NULL DEFAULT 'pending',
    current_step_no         SMALLINT UNSIGNED NOT NULL DEFAULT 1,
    total_steps             SMALLINT UNSIGNED NOT NULL DEFAULT 1,
    value_amount            DECIMAL(14, 2)  NULL,
    currency                CHAR(3)         NULL,
    started_at              DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    deadline_at             DATETIME(6)     NULL,
    completed_at            DATETIME(6)     NULL,
    decided_by_user_id      BIGINT UNSIGNED NULL,
    decision_note           VARCHAR(512)    NULL,
    escalation_count        SMALLINT UNSIGNED NOT NULL DEFAULT 0,
    created_at              DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at              DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    open_pass_key           VARCHAR(32)     GENERATED ALWAYS AS (IF(completed_at IS NULL, CONCAT('pass-', pass_id), NULL)) STORED COMMENT 'one live request per pass',
    PRIMARY KEY (id),
    UNIQUE KEY uk_approval_requests_open (open_pass_key),
    UNIQUE KEY uk_approval_requests_id_tenant (id, tenant_id),
    KEY idx_approval_requests_pass (pass_id, started_at),
    KEY idx_approval_requests_status (tenant_id, status, deadline_at),
    KEY idx_approval_requests_flow (approval_flow_id),
    CONSTRAINT fk_approval_requests_pass FOREIGN KEY (pass_id, tenant_id) REFERENCES passes (id, tenant_id) ON DELETE CASCADE,
    CONSTRAINT fk_approval_requests_flow FOREIGN KEY (approval_flow_id) REFERENCES approval_flows (id) ON DELETE SET NULL,
    CONSTRAINT fk_approval_requests_decider FOREIGN KEY (decided_by_user_id) REFERENCES users (id) ON DELETE SET NULL,
    CONSTRAINT chk_approval_requests_status CHECK (status IN ('not_required', 'pending', 'in_progress', 'approved', 'rejected', 'cancelled', 'expired', 'escalated'))
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci ROW_FORMAT = DYNAMIC;

CREATE TABLE IF NOT EXISTS approval_request_steps (
    id                      BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    tenant_id               BIGINT UNSIGNED NOT NULL,
    approval_request_id     BIGINT UNSIGNED NOT NULL,
    step_no                 SMALLINT UNSIGNED NOT NULL,
    name                    VARCHAR(128)    NULL,
    assignee_key            VARCHAR(96)     NOT NULL COMMENT 'user:17 or role:shift_supervisor, makes the row unique per step',
    assignee_user_id        BIGINT UNSIGNED NULL,
    approver_role_key       VARCHAR(64)     NULL,
    assignee_name_snapshot  VARCHAR(255)    NULL,
    status                  VARCHAR(24)     NOT NULL DEFAULT 'pending',
    mode                    VARCHAR(16)     NOT NULL DEFAULT 'sequential',
    due_at                  DATETIME(6)     NULL,
    assigned_at             DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    notified_at             DATETIME(6)     NULL,
    viewed_at               DATETIME(6)     NULL,
    acted_at                DATETIME(6)     NULL,
    acted_by_user_id        BIGINT UNSIGNED NULL,
    comment                 VARCHAR(1024)   NULL,
    delegated_from_user_id  BIGINT UNSIGNED NULL,
    delegation_id           BIGINT UNSIGNED NULL,
    escalated_from_step_no  SMALLINT UNSIGNED NULL,
    created_at              DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at              DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY uk_approval_request_steps_assignee (approval_request_id, step_no, assignee_key),
    KEY idx_approval_request_steps_inbox (tenant_id, assignee_user_id, status, due_at),
    KEY idx_approval_request_steps_request (approval_request_id, step_no, status),
    KEY idx_approval_request_steps_role (tenant_id, approver_role_key, status),
    CONSTRAINT fk_approval_request_steps_request FOREIGN KEY (approval_request_id, tenant_id) REFERENCES approval_requests (id, tenant_id) ON DELETE CASCADE,
    CONSTRAINT fk_approval_request_steps_assignee FOREIGN KEY (assignee_user_id) REFERENCES users (id) ON DELETE SET NULL,
    CONSTRAINT fk_approval_request_steps_actor FOREIGN KEY (acted_by_user_id) REFERENCES users (id) ON DELETE SET NULL,
    CONSTRAINT fk_approval_request_steps_delegator FOREIGN KEY (delegated_from_user_id) REFERENCES users (id) ON DELETE SET NULL,
    CONSTRAINT chk_approval_request_steps_status CHECK (status IN ('pending', 'approved', 'rejected', 'skipped', 'delegated', 'expired', 'recalled', 'auto_approved')),
    CONSTRAINT chk_approval_request_steps_mode CHECK (mode IN ('sequential', 'parallel', 'any_one'))
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci ROW_FORMAT = DYNAMIC;

CREATE TABLE IF NOT EXISTS approval_delegations (
    id                      BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    tenant_id               BIGINT UNSIGNED NOT NULL,
    from_user_id            BIGINT UNSIGNED NOT NULL,
    to_user_id              BIGINT UNSIGNED NOT NULL,
    approval_flow_id        BIGINT UNSIGNED NULL COMMENT 'NULL = every flow of the delegator',
    pass_type_id            SMALLINT UNSIGNED NULL,
    department_id           BIGINT UNSIGNED NULL,
    starts_at               DATETIME(6)     NOT NULL,
    ends_at                 DATETIME(6)     NOT NULL,
    reason                  VARCHAR(512)    NULL,
    status                  VARCHAR(16)     NOT NULL DEFAULT 'active',
    created_by              BIGINT UNSIGNED NULL,
    created_at              DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at              DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    KEY idx_approval_delegations_from (tenant_id, from_user_id, status, starts_at),
    KEY idx_approval_delegations_to (tenant_id, to_user_id, status, ends_at),
    CONSTRAINT fk_approval_delegations_from FOREIGN KEY (from_user_id) REFERENCES users (id) ON DELETE CASCADE,
    CONSTRAINT fk_approval_delegations_to FOREIGN KEY (to_user_id) REFERENCES users (id) ON DELETE CASCADE,
    CONSTRAINT fk_approval_delegations_flow FOREIGN KEY (approval_flow_id) REFERENCES approval_flows (id) ON DELETE CASCADE,
    CONSTRAINT fk_approval_delegations_pass_type FOREIGN KEY (pass_type_id) REFERENCES pass_types (id) ON DELETE SET NULL,
    CONSTRAINT fk_approval_delegations_department FOREIGN KEY (department_id) REFERENCES departments (id) ON DELETE SET NULL,
    CONSTRAINT fk_approval_delegations_creator FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE SET NULL,
    CONSTRAINT chk_approval_delegations_window CHECK (ends_at > starts_at),
    CONSTRAINT chk_approval_delegations_people CHECK (from_user_id <> to_user_id),
    CONSTRAINT chk_approval_delegations_status CHECK (status IN ('active', 'expired', 'cancelled'))
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci ROW_FORMAT = DYNAMIC;

-- The approver inbox: what is waiting on me right now.
CREATE OR REPLACE VIEW v_approval_inbox AS
SELECT  ars.tenant_id,
        ars.assignee_user_id,
        ars.approver_role_key,
        ars.id AS request_step_id,
        ars.step_no,
        ars.name AS step_name,
        ars.status AS step_status,
        ars.due_at,
        ar.id AS approval_request_id,
        ar.status AS request_status,
        ar.pass_id,
        p.pass_no,
        p.title,
        p.value_amount,
        p.source_module_key,
        pt.type_key AS pass_type_key
FROM    approval_request_steps ars
JOIN    approval_requests ar        ON ar.id = ars.approval_request_id
JOIN    passes p                    ON p.id = ar.pass_id
JOIN    pass_types pt               ON pt.id = p.pass_type_id
WHERE   ars.status = 'pending'
    AND ar.status IN ('pending', 'in_progress', 'escalated');
