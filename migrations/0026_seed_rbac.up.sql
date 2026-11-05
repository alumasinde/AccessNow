-- =============================================================================
-- 0026 seed / RBAC — the permission catalogue and the system role templates
-- permissions and role_templates are global. A tenant gets real roles copied
-- from the templates during provisioning, so roles stay tenant scoped and every
-- foreign key in 0004 remains enforceable.
-- Re-runnable: INSERT ... ON DUPLICATE KEY UPDATE, grants are set-based.
-- =============================================================================

INSERT INTO permissions
    (permission_key, name, module_key, action)
VALUES
    ('core.tenant.view',              'View tenant settings',            'core',               'view'),
    ('core.tenant.settings_update',   'Change tenant settings',          'core',               'update'),
    ('core.site.view',                'View sites',                      'core',               'view'),
    ('core.site.manage',              'Create and edit sites',           'core',               'administer'),
    ('core.gate.view',                'View gates',                      'core',               'view'),
    ('core.gate.manage',              'Create and edit gates',           'core',               'administer'),
    ('core.device.manage',            'Register and revoke gate devices', 'core',              'administer'),
    ('core.user.view',                'View people',                     'core',               'view'),
    ('core.user.invite',              'Invite users',                    'core',               'create'),
    ('core.user.manage',              'Edit users',                      'core',               'update'),
    ('core.role.manage',              'Grant and revoke roles',          'core',               'administer'),
    ('core.department.manage',        'Manage departments',              'core',               'administer'),
    ('core.staff.manage',             'Manage the staff register',       'core',               'update'),
    ('core.audit.view',               'View the audit trail',            'core',               'view'),
    ('core.pass.view',                'View the pass register',          'core',               'view'),
    ('visit.view',                    'View visits',                     'visitor_management', 'view'),
    ('visit.create',                  'Create a walk-in or booked visit','visitor_management', 'create'),
    ('visit.update',                  'Edit a visit',                    'visitor_management', 'update'),
    ('visit.approve',                 'Approve or reject a visit',       'visitor_management', 'approve'),
    ('visit.respond',                 'Reply as the host to a walk-in',  'visitor_management', 'approve'),
    ('visit.cancel',                  'Cancel a visit',                  'visitor_management', 'update'),
    ('visitor.view',                  'View visitor records',            'visitor_management', 'view'),
    ('visitor.create',                'Register a visitor',              'visitor_management', 'create'),
    ('visitor.update',                'Edit a visitor record',           'visitor_management', 'update'),
    ('visitor.merge',                 'Merge duplicate visitors',        'visitor_management', 'update'),
    ('visitor_company.view',          'View visitor companies',          'visitor_management', 'view'),
    ('visitor_company.manage',        'Manage visitor companies',        'visitor_management', 'administer'),
    ('preauth.send',                  'Send a pre-registration invite',  'visitor_management', 'create'),
    ('vehicle.view',                  'View vehicles and vehicle passes','vehicle_tracking',   'view'),
    ('vehicle.manage',                'Manage the fleet register',       'vehicle_tracking',   'administer'),
    ('vehicle_pass.create',           'Log a vehicle in',                'vehicle_tracking',   'create'),
    ('vehicle_pass.approve',          'Approve a vehicle pass',          'vehicle_tracking',   'approve'),
    ('material.view',                 'View goods passes and lines',     'material_logistics', 'view'),
    ('material.create',               'Create a goods pass',             'material_logistics', 'create'),
    ('material.approve',              'Approve outward goods',           'material_logistics', 'approve'),
    ('material.release',              'Release goods at the gate',       'material_logistics', 'update'),
    ('material.verify_return',        'Verify a return at the gate',     'material_logistics', 'update'),
    ('material.record_exception',     'Record a goods exception',        'material_logistics', 'update'),
    ('material.close',                'Close a goods pass',              'material_logistics', 'update'),
    ('outpass.view',                  'View outpasses',                  'staff_outpass',      'view'),
    ('outpass.create',                'Request an outpass',              'staff_outpass',      'create'),
    ('outpass.approve',               'Approve an outpass',              'staff_outpass',      'approve'),
    ('outpass.close',                 'Close an outpass',                'staff_outpass',      'update'),
    ('outpass.manage_reasons',        'Manage outpass reasons',          'staff_outpass',      'administer'),
    ('contractor.view',               'View contractors',                'contractor_access',  'view'),
    ('contractor.manage',             'Manage contractors and firms',    'contractor_access',  'administer'),
    ('contractor_document.upload',    'Upload contractor documents',     'contractor_access',  'create'),
    ('contractor_document.verify',    'Verify a contractor document',    'contractor_access',  'approve'),
    ('contractor_pass.create',        'Issue a contractor pass',         'contractor_access',  'create'),
    ('contractor_pass.approve',       'Approve a contractor pass',       'contractor_access',  'approve'),
    ('contractor_pass.revoke',        'Revoke a contractor pass',        'contractor_access',  'update'),
    ('scan.perform',                  'Validate a token at a gate',      'qr_scanning',        'create'),
    ('scan.view_log',                 'View scan attempts',              'qr_scanning',        'view'),
    ('token.issue',                   'Issue a pass token',              'qr_scanning',        'create'),
    ('token.revoke',                  'Revoke a pass token',             'qr_scanning',        'update'),
    ('approval.view',                 'View approval queues',            'approval_workflow',  'view'),
    ('approval.decide',               'Approve or reject in a queue',    'approval_workflow',  'approve'),
    ('approval.delegate',             'Delegate approvals',              'approval_workflow',  'update'),
    ('approval_flow.manage',          'Design approval flows',           'approval_workflow',  'administer'),
    ('rule.view',                     'View rules',                      'rules_policy',       'view'),
    ('rule.manage',                   'Create and edit rules',           'rules_policy',       'administer'),
    ('rule.override',                 'Override a rule at the gate',     'rules_policy',       'update'),
    ('blocklist.view',                'View the lists',                  'blocklist_watchlist','view'),
    ('blocklist.manage',              'Add and remove listings',         'blocklist_watchlist','administer'),
    ('blocklist.review_hit',          'Resolve a hit',                   'blocklist_watchlist','update'),
    ('notification.view',             'View in-app notices',             'notifications',      'view'),
    ('notification.configure',        'Set channels and recipients',     'notifications',      'administer'),
    ('notification.resend',           'Resend a notification',           'notifications',      'update'),
    ('sync.view',                     'View device sync status',         'offline_gate',       'view'),
    ('sync.replay',                   'Replay a sync batch',             'offline_gate',       'update'),
    ('sync.resolve_conflict',         'Resolve a sync conflict',         'offline_gate',       'update'),
    ('rollcall.view',                 'View who is on site',             'emergency_rollcall', 'view'),
    ('rollcall.start',                'Start a roll-call incident',      'emergency_rollcall', 'create'),
    ('rollcall.mark',                 'Mark a person accounted for',     'emergency_rollcall', 'update'),
    ('rollcall.close',                'Close a roll-call incident',      'emergency_rollcall', 'update'),
    ('report.view',                   'Run a report',                    'reports_exports',    'view'),
    ('report.export',                 'Export a report',                 'reports_exports',    'view'),
    ('report.schedule_manage',        'Manage scheduled exports',        'reports_exports',    'administer'),
    ('audit.view',                    'Read the compliance trail',       'audit_compliance',   'view'),
    ('audit.verify',                  'Verify the hash chain',           'audit_compliance',   'view'),
    ('retention.manage',              'Set retention policies',          'audit_compliance',   'administer'),
    ('legal_hold.manage',             'Place and release legal holds',   'audit_compliance',   'administer'),
    ('api_key.manage',                'Issue API keys',                  'integrations_api',   'administer'),
    ('webhook.manage',                'Manage webhook endpoints',        'integrations_api',   'administer'),
    ('external_system.manage',        'Configure HR and ERP links',      'integrations_api',   'administer'),
    ('actuator.trigger',              'Send a boom gate or turnstile command','integrations_api','create')
ON DUPLICATE KEY UPDATE
    name = VALUES(name),
    module_key = VALUES(module_key),
    action = VALUES(action),
    status = 'active';

INSERT INTO role_templates
    (role_key, name, description, does_gate_work, is_default, is_self_service, sort_order)
VALUES
    ('tenant_admin',        'Tenant administrator', 'Everything inside the tenant except platform operations', 0, 1, 0,  10),
    ('site_manager',        'Site manager',         'Runs a site: gates, staff, rules and reports',            0, 1, 0,  20),
    ('gate_supervisor',     'Gate supervisor',      'Oversees gates, overrides rules and answers alerts',      1, 1, 0,  30),
    ('guard',               'Gate guard',           'Works one gate, records movements, cannot override',      1, 1, 0,  40),
    ('host',                'Host',                 'Replies when a visitor asks for them',                    0, 1, 1,  50),
    ('requester',           'Requester',            'Raises visits, outpasses and goods requests',             0, 1, 1,  60),
    ('approver',            'Approver',             'Decides in the approval queue',                           0, 1, 1,  70),
    ('contractor_manager',  'Contractor manager',   'Onboards contractors and checks their documents',         0, 1, 0,  80),
    ('auditor',             'Auditor',              'Read only, plus hash chain verification',                 0, 1, 0,  90),
    ('report_viewer',       'Report viewer',        'Runs and exports reports only',                           0, 1, 0, 100),
    ('integrations_admin',  'Integrations admin',   'API keys, webhooks, sync and actuators',                  0, 1, 0, 110)
ON DUPLICATE KEY UPDATE
    name = VALUES(name),
    description = VALUES(description),
    does_gate_work = VALUES(does_gate_work),
    is_default = VALUES(is_default),
    is_self_service = VALUES(is_self_service),
    sort_order = VALUES(sort_order);

-- tenant_admin holds every active permission.
INSERT INTO role_template_permissions (template_role_id, permission_id)
SELECT rt.id, p.id
FROM role_templates rt
JOIN permissions p ON p.status = 'active'
WHERE rt.role_key = 'tenant_admin'
ON DUPLICATE KEY UPDATE created_at = CURRENT_TIMESTAMP(6);

INSERT INTO role_template_permissions (template_role_id, permission_id)
SELECT rt.id, p.id
FROM role_templates rt
JOIN permissions p ON p.permission_key IN (
    'core.tenant.view', 'core.site.view', 'core.site.manage', 'core.gate.view', 'core.gate.manage',
    'core.device.manage', 'core.user.view', 'core.user.invite', 'core.user.manage', 'core.department.manage',
    'core.staff.manage', 'core.audit.view', 'core.pass.view', 'visit.view', 'visit.create', 'visit.update', 'visit.approve',
    'visit.cancel', 'visitor.view', 'visitor.create', 'visitor.update', 'visitor_company.view',
    'preauth.send', 'vehicle.view', 'vehicle.manage', 'vehicle_pass.create', 'vehicle_pass.approve',
    'material.view', 'material.create', 'material.approve', 'material.release', 'material.verify_return',
    'material.record_exception', 'material.close', 'outpass.view', 'outpass.approve', 'outpass.close',
    'outpass.manage_reasons', 'contractor.view', 'contractor.manage', 'contractor_document.verify',
    'contractor_pass.create', 'contractor_pass.approve', 'contractor_pass.revoke', 'scan.perform',
    'scan.view_log', 'token.issue', 'token.revoke', 'approval.view', 'approval.decide', 'approval.delegate',
    'approval_flow.manage', 'rule.view', 'rule.manage', 'rule.override', 'blocklist.view', 'blocklist.manage',
    'blocklist.review_hit', 'notification.view', 'notification.configure', 'sync.view', 'sync.replay',
    'sync.resolve_conflict', 'rollcall.view', 'rollcall.start', 'rollcall.mark', 'rollcall.close',
    'report.view', 'report.export', 'report.schedule_manage')
WHERE rt.role_key = 'site_manager'
ON DUPLICATE KEY UPDATE created_at = CURRENT_TIMESTAMP(6);

INSERT INTO role_template_permissions (template_role_id, permission_id)
SELECT rt.id, p.id
FROM role_templates rt
JOIN permissions p ON p.permission_key IN (
    'core.tenant.view', 'core.site.view', 'core.gate.view', 'core.device.manage', 'core.user.view',
    'core.staff.manage', 'core.audit.view', 'core.pass.view', 'visit.view', 'visit.create', 'visit.approve', 'visitor.view',
    'visitor.create', 'vehicle.view', 'vehicle_pass.create', 'vehicle_pass.approve', 'material.view',
    'material.create', 'material.approve', 'material.release', 'material.verify_return',
    'material.record_exception', 'outpass.view', 'outpass.create', 'outpass.approve', 'outpass.close',
    'contractor.view', 'contractor_pass.create', 'contractor_pass.approve', 'scan.perform', 'scan.view_log',
    'token.issue', 'token.revoke', 'approval.view', 'approval.decide', 'rule.view', 'rule.override',
    'blocklist.view', 'blocklist.manage', 'blocklist.review_hit', 'notification.view', 'sync.view',
    'sync.replay', 'sync.resolve_conflict', 'rollcall.view', 'rollcall.start', 'rollcall.mark',
    'rollcall.close', 'report.view', 'report.export', 'actuator.trigger')
WHERE rt.role_key = 'gate_supervisor'
ON DUPLICATE KEY UPDATE created_at = CURRENT_TIMESTAMP(6);

INSERT INTO role_template_permissions (template_role_id, permission_id)
SELECT rt.id, p.id
FROM role_templates rt
JOIN permissions p ON p.permission_key IN (
    'core.site.view', 'core.gate.view', 'core.user.view', 'core.audit.view', 'core.pass.view', 'visit.view', 'visit.create',
    'visitor.view', 'visitor.create', 'vehicle.view', 'vehicle_pass.create', 'material.view',
    'material.create', 'material.release', 'material.verify_return', 'material.record_exception',
    'outpass.view', 'outpass.create', 'outpass.close', 'contractor.view', 'scan.perform', 'scan.view_log',
    'token.issue', 'notification.view', 'sync.view', 'rollcall.view', 'rollcall.mark', 'actuator.trigger')
WHERE rt.role_key = 'guard'
ON DUPLICATE KEY UPDATE created_at = CURRENT_TIMESTAMP(6);

INSERT INTO role_template_permissions (template_role_id, permission_id)
SELECT rt.id, p.id
FROM role_templates rt
JOIN permissions p ON p.permission_key IN (
    'core.user.view', 'visit.view', 'visit.respond', 'visitor.view', 'outpass.view', 'material.view',
    'notification.view', 'report.view')
WHERE rt.role_key = 'host'
ON DUPLICATE KEY UPDATE created_at = CURRENT_TIMESTAMP(6);

INSERT INTO role_template_permissions (template_role_id, permission_id)
SELECT rt.id, p.id
FROM role_templates rt
JOIN permissions p ON p.permission_key IN (
    'visit.view', 'visit.create', 'visitor.view', 'vehicle.view', 'material.view', 'material.create',
    'outpass.view', 'outpass.create', 'contractor.view', 'approval.view', 'notification.view')
WHERE rt.role_key = 'requester'
ON DUPLICATE KEY UPDATE created_at = CURRENT_TIMESTAMP(6);

INSERT INTO role_template_permissions (template_role_id, permission_id)
SELECT rt.id, p.id
FROM role_templates rt
JOIN permissions p ON p.permission_key IN (
    'visit.view', 'visit.approve', 'vehicle.view', 'vehicle_pass.approve', 'material.view', 'material.approve',
    'outpass.view', 'outpass.approve', 'contractor.view', 'contractor_pass.approve', 'approval.view',
    'approval.decide', 'approval.delegate', 'rule.view', 'notification.view', 'report.view')
WHERE rt.role_key = 'approver'
ON DUPLICATE KEY UPDATE created_at = CURRENT_TIMESTAMP(6);

INSERT INTO role_template_permissions (template_role_id, permission_id)
SELECT rt.id, p.id
FROM role_templates rt
JOIN permissions p ON p.permission_key IN (
    'core.staff.manage', 'core.user.view', 'visit.view', 'visitor.view', 'vehicle.view', 'vehicle.manage',
    'contractor.view', 'contractor.manage', 'contractor_document.upload', 'contractor_document.verify',
    'contractor_pass.create', 'contractor_pass.approve', 'contractor_pass.revoke', 'rule.view',
    'blocklist.view', 'blocklist.manage', 'notification.view', 'report.view', 'report.export',
    'approval.view', 'approval.decide')
WHERE rt.role_key = 'contractor_manager'
ON DUPLICATE KEY UPDATE created_at = CURRENT_TIMESTAMP(6);

-- Auditor is the read-only template. Anything ending in a verb of change is
-- deliberately absent.
INSERT INTO role_template_permissions (template_role_id, permission_id)
SELECT rt.id, p.id
FROM role_templates rt
JOIN permissions p ON p.permission_key IN (
    'core.tenant.view', 'core.site.view', 'core.gate.view', 'core.user.view', 'core.audit.view', 'core.pass.view',
    'visit.view', 'visitor.view', 'visitor_company.view', 'vehicle.view', 'material.view', 'outpass.view',
    'contractor.view', 'scan.view_log', 'approval.view', 'rule.view', 'blocklist.view', 'notification.view',
    'sync.view', 'rollcall.view', 'report.view', 'report.export', 'audit.view', 'audit.verify')
WHERE rt.role_key = 'auditor'
ON DUPLICATE KEY UPDATE created_at = CURRENT_TIMESTAMP(6);

INSERT INTO role_template_permissions (template_role_id, permission_id)
SELECT rt.id, p.id
FROM role_templates rt
JOIN permissions p ON p.permission_key IN (
    'core.site.view', 'core.gate.view', 'report.view', 'report.export', 'notification.view', 'visit.view',
    'vehicle.view', 'material.view', 'outpass.view', 'contractor.view')
WHERE rt.role_key = 'report_viewer'
ON DUPLICATE KEY UPDATE created_at = CURRENT_TIMESTAMP(6);

INSERT INTO role_template_permissions (template_role_id, permission_id)
SELECT rt.id, p.id
FROM role_templates rt
JOIN permissions p ON p.permission_key IN (
    'core.site.view', 'core.gate.view', 'core.device.manage', 'scan.view_log', 'notification.configure',
    'notification.resend', 'sync.view', 'sync.replay', 'sync.resolve_conflict', 'api_key.manage',
    'webhook.manage', 'external_system.manage', 'actuator.trigger', 'report.view', 'report.export',
    'approval.view')
WHERE rt.role_key = 'integrations_admin'
ON DUPLICATE KEY UPDATE created_at = CURRENT_TIMESTAMP(6);
