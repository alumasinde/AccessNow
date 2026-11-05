-- =============================================================================
-- 0004 core / RBAC — roles, permissions, role_permissions
-- permissions is a global catalogue. Roles are always tenant scoped, so every
-- foreign key stays valid. Default roles come from the system templates seeded
-- in 0026, which tenant provisioning copies into real roles at signup.
-- Re-runnable: CREATE TABLE IF NOT EXISTS only.
-- =============================================================================

CREATE TABLE IF NOT EXISTS permissions (
    id                      SMALLINT UNSIGNED NOT NULL AUTO_INCREMENT,
    permission_key          VARCHAR(96)     NOT NULL COMMENT 'module.action, e.g. material.approve',
    name                    VARCHAR(128)    NOT NULL,
    module_key              VARCHAR(64)     NOT NULL,
    action                  VARCHAR(48)     NOT NULL COMMENT 'view, create, update, approve, scan, override, export, administer',
    description             VARCHAR(255)    NULL,
    status                  VARCHAR(16)     NOT NULL DEFAULT 'active',
    created_at              DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at              DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY uk_permissions_key (permission_key),
    KEY idx_permissions_module (module_key, action),
    CONSTRAINT fk_permissions_module FOREIGN KEY (module_key) REFERENCES modules (module_key) ON DELETE RESTRICT ON UPDATE CASCADE,
    CONSTRAINT chk_permissions_status CHECK (status IN ('active', 'retired'))
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci ROW_FORMAT = DYNAMIC;

CREATE TABLE IF NOT EXISTS roles (
    id                      BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    tenant_id               BIGINT UNSIGNED NOT NULL,
    role_key                VARCHAR(64)     NOT NULL,
    name                    VARCHAR(128)    NOT NULL,
    description             VARCHAR(255)    NULL,
    template_role_key       VARCHAR(64)     NULL COMMENT 'system template this role was cloned from',
    is_system               TINYINT(1)      NOT NULL DEFAULT 0 COMMENT 'tenant may not delete or rename it',
    is_assignable           TINYINT(1)      NOT NULL DEFAULT 1,
    does_gate_work          TINYINT(1)      NOT NULL DEFAULT 0 COMMENT 'true means the login flow shows the gate picker',
    max_active_sessions     SMALLINT UNSIGNED NULL,
    sort_order              SMALLINT        NOT NULL DEFAULT 100,
    status                  VARCHAR(16)     NOT NULL DEFAULT 'active',
    created_at              DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at              DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    deleted_at              DATETIME(6)     NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_roles_key (tenant_id, role_key),
    UNIQUE KEY uk_roles_id_tenant (id, tenant_id),
    KEY idx_roles_gate_work (tenant_id, does_gate_work, status),
    CONSTRAINT fk_roles_tenant FOREIGN KEY (tenant_id) REFERENCES tenants (id) ON DELETE CASCADE,
    CONSTRAINT chk_roles_status CHECK (status IN ('active', 'inactive')),
    CONSTRAINT chk_roles_flags CHECK (is_system IN (0, 1) AND is_assignable IN (0, 1) AND does_gate_work IN (0, 1))
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci ROW_FORMAT = DYNAMIC;

CREATE TABLE IF NOT EXISTS role_permissions (
    role_id                 BIGINT UNSIGNED NOT NULL,
    tenant_id               BIGINT UNSIGNED NOT NULL,
    permission_id           SMALLINT UNSIGNED NOT NULL,
    created_at              DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (role_id, permission_id),
    KEY idx_role_permissions_permission (permission_id, role_id),
    KEY idx_role_permissions_tenant (tenant_id, role_id),
    CONSTRAINT fk_role_permissions_role FOREIGN KEY (role_id, tenant_id) REFERENCES roles (id, tenant_id) ON DELETE CASCADE,
    CONSTRAINT fk_role_permissions_permission FOREIGN KEY (permission_id) REFERENCES permissions (id) ON DELETE CASCADE
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci ROW_FORMAT = DYNAMIC;

CREATE TABLE IF NOT EXISTS user_roles (
    user_id                 BIGINT UNSIGNED NOT NULL,
    tenant_id               BIGINT UNSIGNED NOT NULL,
    role_id                 BIGINT UNSIGNED NOT NULL,
    site_id                 BIGINT UNSIGNED NULL COMMENT 'NULL = every site in the tenant',
    granted_by              BIGINT UNSIGNED NULL,
    granted_at              DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    expires_at              DATETIME(6)     NULL COMMENT 'temporary access, e.g. a cover shift',
    PRIMARY KEY (user_id, role_id),
    KEY idx_user_roles_role (role_id, tenant_id),
    KEY idx_user_roles_tenant (tenant_id, user_id),
    KEY idx_user_roles_site (site_id),
    CONSTRAINT fk_user_roles_user FOREIGN KEY (user_id, tenant_id) REFERENCES users (id, tenant_id) ON DELETE CASCADE,
    CONSTRAINT fk_user_roles_role FOREIGN KEY (role_id, tenant_id) REFERENCES roles (id, tenant_id) ON DELETE CASCADE,
    CONSTRAINT fk_user_roles_site FOREIGN KEY (site_id) REFERENCES sites (id) ON DELETE CASCADE,
    CONSTRAINT fk_user_roles_granter FOREIGN KEY (granted_by) REFERENCES users (id) ON DELETE SET NULL
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci ROW_FORMAT = DYNAMIC;

CREATE TABLE IF NOT EXISTS role_templates (
    id                      SMALLINT UNSIGNED NOT NULL AUTO_INCREMENT,
    role_key                VARCHAR(64)     NOT NULL,
    name                    VARCHAR(128)    NOT NULL,
    description             VARCHAR(255)    NULL,
    does_gate_work          TINYINT(1)      NOT NULL DEFAULT 0,
    is_default              TINYINT(1)      NOT NULL DEFAULT 0 COMMENT 'materialise for every new tenant',
    is_self_service         TINYINT(1)      NOT NULL DEFAULT 0 COMMENT 'a tenant may grant it to their own staff',
    sort_order              SMALLINT        NOT NULL DEFAULT 100,
    created_at              DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at              DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY uk_role_templates_key (role_key),
    CONSTRAINT chk_role_templates_flags CHECK (does_gate_work IN (0, 1) AND is_default IN (0, 1) AND is_self_service IN (0, 1))
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci ROW_FORMAT = DYNAMIC;

CREATE TABLE IF NOT EXISTS role_template_permissions (
    template_role_id        SMALLINT UNSIGNED NOT NULL,
    permission_id           SMALLINT UNSIGNED NOT NULL,
    created_at              DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (template_role_id, permission_id),
    KEY idx_rtp_permission (permission_id, template_role_id),
    CONSTRAINT fk_rtp_template FOREIGN KEY (template_role_id) REFERENCES role_templates (id) ON DELETE CASCADE,
    CONSTRAINT fk_rtp_permission FOREIGN KEY (permission_id) REFERENCES permissions (id) ON DELETE CASCADE
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci ROW_FORMAT = DYNAMIC;

CREATE TABLE IF NOT EXISTS permission_grants (
    id                      BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    tenant_id               BIGINT UNSIGNED NOT NULL,
    user_id                 BIGINT UNSIGNED NULL COMMENT 'NULL = grant applies to a whole role',
    role_id                 BIGINT UNSIGNED NULL,
    permission_id           SMALLINT UNSIGNED NOT NULL,
    effect                  VARCHAR(8)      NOT NULL DEFAULT 'allow' COMMENT 'allow adds, deny subtracts',
    scope_type              VARCHAR(16)     NOT NULL DEFAULT 'tenant',
    scope_id                BIGINT UNSIGNED NULL COMMENT 'site_id, gate_id or department_id per scope_type',
    reason                  VARCHAR(255)    NULL,
    created_by              BIGINT UNSIGNED NULL,
    created_at              DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    expires_at              DATETIME(6)     NULL,
    revoked_at              DATETIME(6)     NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_permission_grants (tenant_id, user_id, role_id, permission_id, effect, scope_type, scope_id),
    KEY idx_permission_grants_user (tenant_id, user_id),
    KEY idx_permission_grants_role (role_id),
    CONSTRAINT fk_permission_grants_tenant FOREIGN KEY (tenant_id) REFERENCES tenants (id) ON DELETE CASCADE,
    CONSTRAINT fk_permission_grants_user FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE,
    CONSTRAINT fk_permission_grants_role FOREIGN KEY (role_id) REFERENCES roles (id) ON DELETE CASCADE,
    CONSTRAINT fk_permission_grants_permission FOREIGN KEY (permission_id) REFERENCES permissions (id) ON DELETE CASCADE,
    CONSTRAINT chk_permission_grants_effect CHECK (effect IN ('allow', 'deny')),
    CONSTRAINT chk_permission_grants_scope CHECK (scope_type IN ('tenant', 'site', 'gate', 'department', 'module')),
    CONSTRAINT chk_permission_grants_target CHECK (user_id IS NOT NULL OR role_id IS NOT NULL),
    CONSTRAINT chk_permission_grants_scoped CHECK (scope_type = 'tenant' OR scope_id IS NOT NULL)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci ROW_FORMAT = DYNAMIC;
