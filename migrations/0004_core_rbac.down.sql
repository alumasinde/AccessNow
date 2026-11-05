-- 0004 core / RBAC — child tables first, parent last.

DROP TABLE IF EXISTS permission_grants;
DROP TABLE IF EXISTS role_template_permissions;
DROP TABLE IF EXISTS role_templates;
DROP TABLE IF EXISTS user_roles;
DROP TABLE IF EXISTS role_permissions;
DROP TABLE IF EXISTS roles;
DROP TABLE IF EXISTS permissions;
