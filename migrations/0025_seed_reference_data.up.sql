-- =============================================================================
-- 0025 seed / reference data — the catalogue every tenant draws from
-- Insert only, never truncate: re-running refreshes names and defaults and
-- leaves tenant data untouched. Keys are stable, names are display text.
-- Re-runnable: INSERT ... ON DUPLICATE KEY UPDATE on each unique key.
-- =============================================================================

INSERT INTO modules
    (module_key, name, summary, is_core, default_enabled, sort_order, status)
VALUES
    ('core',                    'Core',                       'Tenants, login and roles, sites and gates, module flags, basic audit', 1, 1,   0, 'available'),
    ('visitor_management',      'Visitor Management',         'Pre-registered and walk-in visitors, host approval, check-in and check-out', 0, 1,  10, 'available'),
    ('vehicle_tracking',        'Vehicle Tracking',           'Vehicles in and out by plate, driver and purpose, with dwell limits', 0, 1,  20, 'available'),
    ('material_logistics',      'Material and Asset Logistics','Inward and outward goods with returnable and non-returnable passes', 0, 1,  30, 'available'),
    ('staff_outpass',           'Staff Outpass',              'Employees stepping out during working hours', 0, 0,  40, 'available'),
    ('contractor_access',       'Contractor and Recurring Access','Long passes for contractors, suppliers and regular drivers', 0, 0,  50, 'available'),
    ('qr_scanning',             'QR and Hardware Scanning',   'Gate validation by QR, ID reader, ANPR camera and USB scanner', 0, 0,  60, 'available'),
    ('approval_workflow',       'Approval Workflow',          'Multi-level routing for any pass type', 0, 0,  70, 'available'),
    ('rules_policy',            'Rules and Policy',           'Automatic restrictions on requests and gate scans', 0, 0,  80, 'available'),
    ('blocklist_watchlist',     'Blacklist and Watchlist',    'Banned people and vehicles blocked, watched ones quietly flagged', 0, 0,  90, 'available'),
    ('notifications',           'Notifications',              'Email, SMS, WhatsApp and in-app notices with retries', 0, 1, 100, 'available'),
    ('offline_gate',            'Offline Gate Mode',          'Gates keep working when the link drops, then sync', 0, 0, 110, 'available'),
    ('emergency_rollcall',      'Emergency Roll-Call',        'Who is on site right now and who is still unaccounted', 0, 0, 120, 'available'),
    ('reports_exports',         'Reports and Scheduled Exports','On-demand analytics and automatic digests', 0, 1, 130, 'available'),
    ('audit_compliance',        'Audit and Compliance',       'Hash-chained history, retention rules and auto-purge', 0, 0, 140, 'available'),
    ('integrations_api',        'Integrations and API',       'API keys, webhooks, HR and ERP sync, boom gate triggers', 0, 0, 150, 'available')
ON DUPLICATE KEY UPDATE
    name = VALUES(name),
    summary = VALUES(summary),
    is_core = VALUES(is_core),
    default_enabled = VALUES(default_enabled),
    sort_order = VALUES(sort_order),
    status = VALUES(status);

INSERT INTO pass_categories
    (category_key, name, module_key, description, sort_order)
VALUES
    ('visitor',    'Visitor pass',     'visitor_management', 'People visiting staff or the public areas', 10),
    ('staff',      'Staff pass',       'staff_outpass',      'Employees leaving and returning during a shift', 20),
    ('vehicle',    'Vehicle pass',     'vehicle_tracking',   'Cars, trucks and trailers by plate', 30),
    ('material',   'Goods pass',       'material_logistics', 'Inward and outward goods, assets and luggage', 40),
    ('contractor', 'Contractor pass',  'contractor_access',  'Long access for contractors, suppliers and regular drivers', 50),
    ('service',    'Service visit',    'contractor_access',  'Short maintenance or repair attendance', 60),
    ('emergency',  'Emergency access', 'emergency_rollcall', 'Responders and muster-point access', 70)
ON DUPLICATE KEY UPDATE
    name = VALUES(name),
    module_key = VALUES(module_key),
    description = VALUES(description),
    sort_order = VALUES(sort_order);

INSERT INTO pass_types
    (type_key, name, module_key, category_key, requires_approval, requires_host, requires_vehicle, requires_items, requires_document_check, default_validity_hours, default_max_entries, default_dwell_limit_minutes, sort_order)
VALUES
    ('visitor_walk_in',           'Walk-in visitor',        'visitor_management', 'visitor',    1, 1, 0, 0, 0,   8,    1,  480, 10),
    ('visitor_pre_registered',    'Pre-registered visitor', 'visitor_management', 'visitor',    1, 1, 0, 0, 0,  168,    1,  480, 20),
    ('vehicle_delivery',          'Delivery vehicle',       'vehicle_tracking',   'vehicle',    1, 0, 1, 0, 0,    4,    1,  120, 30),
    ('vehicle_fleet',             'Fleet vehicle',          'vehicle_tracking',   'vehicle',    0, 0, 1, 0, 0,   24,    2,  600, 40),
    ('vehicle_visitor',           'Visitor vehicle',        'vehicle_tracking',   'vehicle',    1, 1, 1, 0, 0,    8,    2,  240, 50),
    ('material_outward_rgp',      'Returnable goods pass',  'material_logistics', 'material',   1, 0, 0, 1, 0,  336,    1, NULL, 60),
    ('material_outward_nrgp',     'Non-returnable goods pass','material_logistics','material',  1, 0, 0, 1, 0,   24,    1, NULL, 70),
    ('material_inward',           'Inward goods declaration','material_logistics','material',   0, 0, 0, 1, 0,   24,    2, NULL, 80),
    ('material_personal_item',    'Personal item declaration','material_logistics','material',   0, 0, 0, 1, 0,   24,    2, NULL, 90),
    ('staff_outpass',             'Staff outpass',          'staff_outpass',      'staff',      1, 0, 0, 0, 0,    4,    2,   60, 100),
    ('contractor_recurring',      'Contractor recurring pass','contractor_access','contractor', 1, 0, 0, 0, 1,  720, NULL, NULL, 110),
    ('contractor_day',            'Contractor day pass',    'contractor_access',  'contractor', 1, 0, 0, 0, 1,   12,    2,  600, 120),
    ('service_visit',             'Service engineer visit', 'contractor_access',  'service',    1, 1, 1, 1, 1,    8,    1,  240, 130),
    ('emergency_access',          'Emergency responder access','emergency_rollcall','emergency', 0, 0, 0, 0, 0,    1, NULL,   60, 140)
ON DUPLICATE KEY UPDATE
    name = VALUES(name),
    module_key = VALUES(module_key),
    category_key = VALUES(category_key),
    requires_approval = VALUES(requires_approval),
    requires_host = VALUES(requires_host),
    requires_vehicle = VALUES(requires_vehicle),
    requires_items = VALUES(requires_items),
    requires_document_check = VALUES(requires_document_check),
    default_validity_hours = VALUES(default_validity_hours),
    default_max_entries = VALUES(default_max_entries),
    default_dwell_limit_minutes = VALUES(default_dwell_limit_minutes),
    sort_order = VALUES(sort_order);

INSERT INTO document_types
    (type_key, name, holder_type, applies_to_module, is_mandatory, validity_months, requires_verification, reminder_days_before)
VALUES
    ('national_id',               'National identity card', 'contractor', 'contractor_access',        1, NULL, 1,  0),
    ('work_permit',               'Work permit',            'contractor', 'contractor_access',        1,   24, 1, 60),
    ('police_clearance',          'Police clearance',       'contractor', 'contractor_access',        1,   12, 1, 30),
    ('safety_induction',          'Site safety induction',  'contractor', 'contractor_access',        1,    6, 1, 14),
    ('drivers_licence',           'Driving licence',        'vehicle',    'vehicle_tracking',         1,   60, 1, 60),
    ('vehicle_insurance',         'Vehicle insurance',      'vehicle',    'vehicle_tracking',         1,   12, 1, 30),
    ('vehicle_fitness',           'Fitness to drive certificate','vehicle','vehicle_tracking',        1,   12, 1, 30),
    ('hazmat_permit',             'Hazardous materials permit','vehicle',  'material_logistics',       1,   12, 1, 30),
    ('public_liability_insurance','Public liability insurance','organization','contractor_access',     1,   12, 1, 30)
ON DUPLICATE KEY UPDATE
    name = VALUES(name),
    holder_type = VALUES(holder_type),
    applies_to_module = VALUES(applies_to_module),
    is_mandatory = VALUES(is_mandatory),
    validity_months = VALUES(validity_months),
    requires_verification = VALUES(requires_verification),
    reminder_days_before = VALUES(reminder_days_before);

INSERT INTO outpass_reasons
    (reason_key, name, default_duration_minutes, requires_manager, requires_return_scan, sort_order)
VALUES
    ('personal_errand',    'Personal errand',            30, 0, 1, 10),
    ('medical_appointment','Medical appointment',        90, 0, 1, 20),
    ('family_emergency',   'Family emergency',          120, 1, 1, 30),
    ('official_business',  'Official business off site',180, 0, 1, 40),
    ('government_office',  'Government office',         120, 1, 1, 50),
    ('transport_delay',    'Transport delay',            60, 0, 0, 60),
    ('other',              'Other, reason required',     30, 1, 1, 70)
ON DUPLICATE KEY UPDATE
    name = VALUES(name),
    default_duration_minutes = VALUES(default_duration_minutes),
    requires_manager = VALUES(requires_manager),
    requires_return_scan = VALUES(requires_return_scan),
    sort_order = VALUES(sort_order);

INSERT INTO notification_events
    (event_key, name, module_key, default_channels, default_recipient_types, is_opt_out)
VALUES
    ('visit.host_approval_needed',  'A guard logged a walk-in and needs your reply', 'visitor_management', JSON_ARRAY('in_app', 'email'),               JSON_ARRAY('host'),                 1),
    ('visit.approved',              'Visit approved',                                'visitor_management', JSON_ARRAY('in_app', 'email'),               JSON_ARRAY('visitor', 'host'),    1),
    ('visit.rejected',              'Visit rejected',                                'visitor_management', JSON_ARRAY('in_app', 'email'),               JSON_ARRAY('visitor'),            1),
    ('visit.arrived',               'Visitor has arrived at the gate',               'visitor_management', JSON_ARRAY('in_app'),                        JSON_ARRAY('host'),                 1),
    ('visit.departed',              'Visitor has checked out',                       'visitor_management', JSON_ARRAY('in_app'),                        JSON_ARRAY('host'),                 0),
    ('visit.expired',               'Visit window expired without arrival',          'visitor_management', JSON_ARRAY('in_app'),                        JSON_ARRAY('host'),                 0),
    ('preauth.invitation_sent',     'Pre-registration invitation sent',              'visitor_management', JSON_ARRAY('email'),                         JSON_ARRAY('visitor'),              1),
    ('vehicle.overstaying',         'Vehicle is past its dwell limit',               'vehicle_tracking',   JSON_ARRAY('in_app'),                        JSON_ARRAY('gate_supervisor'),      1),
    ('vehicle.exited',              'Vehicle has exited',                            'vehicle_tracking',   JSON_ARRAY(),                                JSON_ARRAY('host'),                 0),
    ('material.approval_needed',    'Outward goods pass needs approval',             'material_logistics', JSON_ARRAY('in_app', 'email'),               JSON_ARRAY('approver'),             1),
    ('material.released',           'Goods released at the gate',                    'material_logistics', JSON_ARRAY('in_app'),                        JSON_ARRAY('requester'),            0),
    ('material.item_overdue',       'Returnable item is overdue',                    'material_logistics', JSON_ARRAY('in_app', 'email', 'sms'),        JSON_ARRAY('requester', 'department_head'), 1),
    ('material.exception',          'Goods exception recorded at the gate',          'material_logistics', JSON_ARRAY('in_app'),                        JSON_ARRAY('gate_supervisor'),      1),
    ('outpass.approval_needed',     'Staff outpass needs approval',                  'staff_outpass',      JSON_ARRAY('in_app'),                        JSON_ARRAY('approver'),             1),
    ('outpass.overdue',             'Employee has not returned from an outpass',     'staff_outpass',      JSON_ARRAY('in_app'),                        JSON_ARRAY('department_head'),      1),
    ('outpass.unreturned',          'Employee not back by end of shift',             'staff_outpass',      JSON_ARRAY('in_app', 'email', 'sms'),        JSON_ARRAY('gate_supervisor', 'department_head'), 1),
    ('contractor.document_expiring','Contractor document is about to expire',        'contractor_access',  JSON_ARRAY('in_app', 'email'),               JSON_ARRAY('contractor_manager'),   1),
    ('contractor.pass_blocked',     'Pass blocked by an expired document',           'contractor_access',  JSON_ARRAY('in_app'),                        JSON_ARRAY('contractor_manager'),   1),
    ('blocklist.watched_hit',       'Watchlisted person or vehicle checked in',      'blocklist_watchlist',JSON_ARRAY('in_app', 'sms'),                 JSON_ARRAY('gate_supervisor'),      1),
    ('blocklist.denied_entry',      'Entry denied at a gate',                        'blocklist_watchlist',JSON_ARRAY('in_app'),                        JSON_ARRAY('gate_supervisor'),      1),
    ('approval.awaiting_me',        'An approval is waiting on you',                 'approval_workflow',  JSON_ARRAY('in_app', 'email'),               JSON_ARRAY('approver'),             1),
    ('approval.escalated',          'An approval was escalated to you',              'approval_workflow',  JSON_ARRAY('in_app', 'email', 'sms'),        JSON_ARRAY('approver'),             1),
    ('gate.override_used',          'A supervisor overrode a gate rule',             'rules_policy',       JSON_ARRAY('in_app'),                        JSON_ARRAY('gate_supervisor'),      1),
    ('shift.handover_due',          'Gate shift handover outstanding',               'core',               JSON_ARRAY('in_app'),                        JSON_ARRAY('gate_supervisor'),      0),
    ('rollcall.incident_started',   'Roll-call started, confirm you are accounted for','emergency_rollcall',JSON_ARRAY('in_app', 'email', 'sms'),       JSON_ARRAY('gate_supervisor', 'tenant_admin'), 0),
    ('rollcall.still_unaccounted',  'People still unaccounted for',                  'emergency_rollcall', JSON_ARRAY('in_app', 'sms'),                 JSON_ARRAY('gate_supervisor', 'tenant_admin'), 0),
    ('report.ready',                'A scheduled report is ready',                   'reports_exports',    JSON_ARRAY('email'),                         JSON_ARRAY('tenant_admin'),         1),
    ('sync.conflicts_found',        'Offline batch replayed with conflicts',         'offline_gate',       JSON_ARRAY('in_app'),                        JSON_ARRAY('gate_supervisor'),      1),
    ('device.offline',              'A gate device has stopped reporting',           'offline_gate',       JSON_ARRAY('in_app'),                        JSON_ARRAY('site_supervisor'),      0)
ON DUPLICATE KEY UPDATE
    name = VALUES(name),
    module_key = VALUES(module_key),
    default_channels = VALUES(default_channels),
    default_recipient_types = VALUES(default_recipient_types),
    is_opt_out = VALUES(is_opt_out);

INSERT INTO report_definitions
    (definition_key, name, module_key, description, period_grain, allowed_groupings, default_format, is_default, sort_order)
VALUES
    ('gate_movements_daily',     'Gate movements by day',          'core',               'Entries, exits and denies per gate and shift',                 'day',   JSON_ARRAY('gate', 'site', 'guard'),                  'pdf',  1,  10),
    ('visitor_log',              'Visitor register',               'visitor_management', 'Every visit with host, purpose and time on site',              'day',   JSON_ARRAY('host', 'gate', 'company'),                  'xlsx', 1,  20),
    ('host_activity',            'Host responsiveness',            'visitor_management', 'Approvals handled, average reply time, unanswered',            'week',  JSON_ARRAY('host', 'department'),                       'pdf',  0,  30),
    ('vehicle_dwell_times',      'Vehicle dwell times',            'vehicle_tracking',   'Time on site, overstays and repeat plates',                    'day',   JSON_ARRAY('gate', 'vehicle_class'),                    'xlsx', 1,  40),
    ('material_outstanding',     'Outstanding returnable goods',   'material_logistics', 'RGP lines not yet back, aged by overdue days',                 'day',   JSON_ARRAY('department', 'category'),                   'xlsx', 1,  50),
    ('material_exceptions',      'Goods exceptions',               'material_logistics', 'Items left behind or carried out by someone else',             'month', JSON_ARRAY('gate', 'reason'),                             'pdf',  0,  60),
    ('outpass_register',         'Staff outpass register',         'staff_outpass',      'Who left, why, for how long and who did not return',           'day',   JSON_ARRAY('department', 'staff', 'gate'),              'xlsx', 1,  70),
    ('contractor_expiry',        'Contractor documents expiring',  'contractor_access',  'Documents and passes due in the window',                       'week',  JSON_ARRAY('company', 'department'),                    'pdf',  1,  80),
    ('blocklist_hits',           'Blocklist and watchlist hits',   'blocklist_watchlist','Screening matches, denials and overrides',                     'day',   JSON_ARRAY('gate', 'list_type'),                        'pdf',  0,  90),
    ('approval_latency',         'Approval queue and latency',     'approval_workflow',  'Waiting, escalated and overdue approval steps',                'day',   JSON_ARRAY('approver', 'pass_type'),                    'pdf',  0, 100),
    ('gate_utilisation',         'Gate utilisation',               'core',               'Scan volume and idle time per gate and hour',                  'day',   JSON_ARRAY('gate', 'site'),                               'xlsx', 0, 110),
    ('guard_activity',           'Guard activity and shifts',      'core',               'Movements recorded per guard and per gate session',            'shift', JSON_ARRAY('guard', 'gate'),                              'xlsx', 0, 120),
    ('audit_summary',            'Audit and override summary',     'audit_compliance',   'Overrides, role changes and chain verification results',       'month', JSON_ARRAY('action', 'user'),                             'pdf',  0, 130)
ON DUPLICATE KEY UPDATE
    name = VALUES(name),
    module_key = VALUES(module_key),
    description = VALUES(description),
    period_grain = VALUES(period_grain),
    allowed_groupings = VALUES(allowed_groupings),
    default_format = VALUES(default_format),
    is_default = VALUES(is_default),
    sort_order = VALUES(sort_order);
