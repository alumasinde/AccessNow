-- 0026 seed / RBAC — maps first, then the rows they point at.

DELETE FROM role_template_permissions;

DELETE FROM role_templates
WHERE role_key IN ('tenant_admin', 'site_manager', 'gate_supervisor', 'guard', 'host', 'requester',
                   'approver', 'contractor_manager', 'auditor', 'report_viewer', 'integrations_admin');

DELETE FROM permissions
WHERE module_key IN ('core', 'visitor_management', 'vehicle_tracking', 'material_logistics', 'staff_outpass',
                     'contractor_access', 'qr_scanning', 'approval_workflow', 'rules_policy',
                     'blocklist_watchlist', 'notifications', 'offline_gate', 'emergency_rollcall',
                     'reports_exports', 'audit_compliance', 'integrations_api');
