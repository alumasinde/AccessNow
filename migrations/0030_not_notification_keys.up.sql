-- =============================================================================
-- 0030 module / notifications — the catalogue made true, and system templates
--
-- 0019 built the tables and 0025 seeded a catalogue of event keys, but the catalogue
-- describes the system as it was designed rather than as it runs: approval.awaiting_me
-- has never been published, while approval.step.assigned - the event that actually says
-- "it is your turn" - had no row, so no tenant could route it and no template could exist
-- for it. This migration closes that gap for every key the shipped modules emit today,
-- and adds the message text.
--
-- Keys whose modules are not built yet (contractor, blocklist, rollcall, reports, offline
-- gate) are left exactly as 0025 seeded them: they will never fire, and they cost nothing.
--
-- Templates are written with tenant_id NULL, which is the "system default every tenant
-- inherits" that 0019 already defines; a tenant that wants different wording inserts its
-- own row for the same (event_key, channel, locale) and this one stops applying to it.
--
-- Re-runnable: INSERT ... ON DUPLICATE KEY UPDATE only, no ALTER TABLE.
-- =============================================================================

INSERT INTO notification_events
    (event_key, name, module_key, default_channels, default_recipient_types, is_opt_out)
VALUES
    -- The workflow engine. These are the keys the approval module publishes; see
    -- internal/modules/approval/{engine,decide,service}.go.
    ('approval.step.assigned',  'An approval is waiting on you',        'approval_workflow', JSON_ARRAY('in_app', 'email'), JSON_ARRAY('approver'),           1),
    ('approval.decided',        'Your approval was answered',           'approval_workflow', JSON_ARRAY('in_app'),          JSON_ARRAY('requester'),          1),
    ('approval.rejected',       'The pass you filed was rejected',      'approval_workflow', JSON_ARRAY('in_app', 'email'), JSON_ARRAY('requester'),          1),
    ('approval.returned',       'The pass you filed needs a correction','approval_workflow', JSON_ARRAY('in_app', 'email'), JSON_ARRAY('requester'),          1),
    ('approval.cancelled',      'A pass you were asked about was withdrawn', 'approval_workflow', JSON_ARRAY('in_app'),     JSON_ARRAY('approver'),           1),
    ('approval.reset',          'An approval was restarted on changed paper', 'approval_workflow', JSON_ARRAY('in_app'),    JSON_ARRAY('approver'),           1),
    ('approval.delegated',      'You are covering somebody\'s approvals', 'approval_workflow', JSON_ARRAY(),               JSON_ARRAY('approver'),           1),

    -- Core: the state a pass actually reached, whatever module filed it.
    ('pass.approved',           'Your pass is approved and works at the gate', 'core', JSON_ARRAY('in_app', 'email'), JSON_ARRAY('requester'), 1),
    ('pass.rejected',           'Your pass was rejected',                      'core', JSON_ARRAY('in_app', 'email'), JSON_ARRAY('requester'), 1),
    ('pass.cancelled',          'Your pass was cancelled',                     'core', JSON_ARRAY('in_app'),          JSON_ARRAY('requester'), 1),
    ('pass.expired',            'Your pass expired before it was used',        'core', JSON_ARRAY('in_app'),          JSON_ARRAY('requester'), 1),

    -- Visitor desk: a walk-in is only a pass once the host says yes.
    ('visit.created',           'A visitor was logged at the gate',            'visitor_management', JSON_ARRAY('in_app'),          JSON_ARRAY('host'),           1),
    ('approval.requested',      'A visitor needs your answer',                 'visitor_management', JSON_ARRAY('in_app', 'email'), JSON_ARRAY('host'),           1),

    -- Goods: what left, what came back, what is still owed.
    ('material.declared',       'Goods were declared outward',                 'material_logistics', JSON_ARRAY(),                  JSON_ARRAY('approver'),       1),
    ('material.received',       'Goods came back through the gate',            'material_logistics', JSON_ARRAY('in_app'),          JSON_ARRAY('requester'),      0),
    ('material.returned',       'A returnable item is back',                   'material_logistics', JSON_ARRAY('in_app'),          JSON_ARRAY('requester'),      0),

    -- Staff step-outs.
    ('outpass.requested',       'A staff step-out was filed',                  'staff_outpass', JSON_ARRAY(),                  JSON_ARRAY('approver'),       1),
    ('outpass.departed',        'An employee left the site',                   'staff_outpass', JSON_ARRAY('in_app'),          JSON_ARRAY('gate_supervisor'), 0),
    ('outpass.returned',        'An employee is back on site',                 'staff_outpass', JSON_ARRAY(),                  JSON_ARRAY('gate_supervisor'), 0),

    -- Vehicles.
    ('vehicle.logged',          'A vehicle pass was filed',                    'vehicle_tracking', JSON_ARRAY(),                  JSON_ARRAY('approver'),           1),
    ('vehicle.approval_needed', 'A vehicle pass needs approval',               'vehicle_tracking', JSON_ARRAY('in_app', 'email'), JSON_ARRAY('approver'),           1),
    ('vehicle.entered',         'A vehicle is on site',                        'vehicle_tracking', JSON_ARRAY(),                  JSON_ARRAY('gate_supervisor'),    0)
ON DUPLICATE KEY UPDATE
    name                    = VALUES(name),
    module_key              = VALUES(module_key),
    default_channels        = VALUES(default_channels),
    default_recipient_types = VALUES(default_recipient_types),
    is_opt_out              = VALUES(is_opt_out);

-- The key 0025 hoped for is superseded by the one the engine really publishes. Inactive
-- rather than deleted: a tenant that already saved a route against it keeps the row, and
-- it stops being offered as a choice.
UPDATE notification_events SET status = 'inactive' WHERE event_key = 'approval.awaiting_me';

-- subject is NULL for in_app: an in-app notice is a line in a list, not an envelope.
-- Body wording is deliberately short - the same text carries the push-style inbox and the
-- top of an email, and {{placeholders}} are filled from the event payload plus the pass row.
INSERT INTO notification_templates
    (tenant_id, event_key, channel, locale, subject, body, short_body, is_html, variables)
VALUES
    (NULL, 'approval.step.assigned', 'in_app', 'en', NULL,
     '{{step_name}}: {{subject_name}} on {{pass_no}} is waiting for you.',
     NULL, 0, JSON_ARRAY('step_name', 'subject_name', 'pass_no')),
    (NULL, 'approval.step.assigned', 'email', 'en', 'You are asked to approve pass {{pass_no}}',
     'A pass is at your step.\n\n  Pass:     {{pass_no}} ({{status}})\n  Subject:  {{subject_name}}\n  Filed by: {{requester_name}}\n  Step:     {{step_name}}\n  Site:     {{site_name}}\n\nSign it at the approvals desk.',
     'Pass {{pass_no}} ({{subject_name}}) is waiting for your approval.', 0,
     JSON_ARRAY('pass_no', 'status', 'subject_name', 'requester_name', 'step_name', 'site_name')),

    (NULL, 'approval.escalated', 'in_app', 'en', NULL,
     'Escalated to you: {{subject_name}} on {{pass_no}} went unanswered.',
     '{{pass_no}} was escalated to you.', 0, JSON_ARRAY('subject_name', 'pass_no')),
    (NULL, 'approval.escalated', 'email', 'en', 'Escalated to you: pass {{pass_no}}',
     'Nobody answered this ask in time, so it is now yours.\n\n  Pass:    {{pass_no}}\n  Subject: {{subject_name}}\n  Step:    {{step_name}}\n\nPlease decide it today.',
     'Pass {{pass_no}} was escalated to you. Please decide it.', 0,
     JSON_ARRAY('pass_no', 'subject_name', 'step_name')),
    (NULL, 'approval.escalated', 'sms', 'en', NULL,
     'AccessNow: pass {{pass_no}} was escalated to you. Please approve or reject it.',
     'Pass {{pass_no}} escalated to you.', 0, JSON_ARRAY('pass_no')),

    (NULL, 'approval.decided', 'in_app', 'en', NULL,
     '{{requester_name}}: your {{pass_no}} is {{chain_status}}.',
     NULL, 0, JSON_ARRAY('requester_name', 'pass_no', 'chain_status')),

    (NULL, 'approval.rejected', 'in_app', 'en', NULL,
     'Rejected: {{pass_no}} ({{subject_name}}). {{comment}}',
     '{{pass_no}} was rejected.', 0, JSON_ARRAY('pass_no', 'subject_name', 'comment')),
    (NULL, 'approval.rejected', 'email', 'en', 'Rejected: pass {{pass_no}}',
     'The approval chain rejected this pass.\n\n  Pass:    {{pass_no}}\n  Subject: {{subject_name}}\n  Reason:  {{comment}}\n\nYou may file it again with the paper corrected.',
     'Pass {{pass_no}} was rejected: {{comment}}', 0, JSON_ARRAY('pass_no', 'subject_name', 'comment')),

    (NULL, 'approval.returned', 'in_app', 'en', NULL,
     'Back to you for a correction: {{pass_no}}. {{comment}}',
     '{{pass_no}} was returned for correction.', 0, JSON_ARRAY('pass_no', 'comment')),
    (NULL, 'approval.returned', 'email', 'en', 'Fix and refiling: pass {{pass_no}}',
     'An approver returned this pass rather than rejecting it.\n\n  Pass:    {{pass_no}}\n  Subject: {{subject_name}}\n  Asked:   {{comment}}\n\nCorrect it and it restarts the chain under today''s rules.',
     'Pass {{pass_no}} returned for correction: {{comment}}', 0,
     JSON_ARRAY('pass_no', 'subject_name', 'comment')),

    (NULL, 'approval.cancelled', 'in_app', 'en', NULL,
     'Withdrawn before you answered: {{pass_no}} ({{subject_name}}).',
     '{{pass_no}} was cancelled.', 0, JSON_ARRAY('pass_no', 'subject_name')),
    (NULL, 'approval.reset', 'in_app', 'en', NULL,
     'The paper on {{pass_no}} changed, so the chain restarted: {{reason}}',
     '{{pass_no}} restarted its approvals.', 0, JSON_ARRAY('pass_no', 'reason')),

    (NULL, 'pass.approved', 'in_app', 'en', NULL,
     'Approved: {{pass_no}} ({{subject_name}}) now works at the gate.',
     '{{pass_no}} is approved.', 0, JSON_ARRAY('pass_no', 'subject_name')),
    (NULL, 'pass.approved', 'email', 'en', 'Approved: {{pass_no}}',
     'Your pass is approved and valid at the barrier.\n\n  Pass:    {{pass_no}}\n  Kind:    {{status}}\n  Subject: {{subject_name}}\n  Valid until: {{valid_until}}\n\nBring the pass and the ID you filed.',
     'Pass {{pass_no}} is approved and works at the gate.', 0,
     JSON_ARRAY('pass_no', 'status', 'subject_name', 'valid_until')),

    (NULL, 'pass.rejected', 'in_app', 'en', NULL,
     'Rejected: {{pass_no}} ({{subject_name}}). {{decision_note}}',
     '{{pass_no}} was rejected.', 0, JSON_ARRAY('pass_no', 'subject_name', 'decision_note')),
    (NULL, 'pass.rejected', 'email', 'en', 'Rejected: {{pass_no}}',
     'Your pass was not approved.\n\n  Pass:    {{pass_no}}\n  Subject: {{subject_name}}\n  Note:    {{decision_note}}',
     'Pass {{pass_no}} was rejected.', 0, JSON_ARRAY('pass_no', 'subject_name', 'decision_note')),

    (NULL, 'pass.cancelled', 'in_app', 'en', NULL,
     'Cancelled: {{pass_no}} ({{subject_name}}).', '{{pass_no}} was cancelled.', 0,
     JSON_ARRAY('pass_no', 'subject_name')),
    (NULL, 'pass.expired', 'in_app', 'en', NULL,
     'Expired unused: {{pass_no}} ({{subject_name}}).', '{{pass_no}} expired.', 0,
     JSON_ARRAY('pass_no', 'subject_name')),

    (NULL, 'visit.created', 'in_app', 'en', NULL,
     '{{subject_name}} was logged at the gate for you. {{purpose}}',
     '{{subject_name}} is waiting at the gate.', 0, JSON_ARRAY('subject_name', 'purpose')),
    (NULL, 'approval.requested', 'in_app', 'en', NULL,
     '{{subject_name}} is at the gate and needs your answer. {{purpose}}',
     '{{subject_name}} needs your approval at the gate.', 0, JSON_ARRAY('subject_name', 'purpose')),
    (NULL, 'approval.requested', 'email', 'en', 'A visitor is waiting for you: {{subject_name}}',
     'A guard logged a walk-in and the pass cannot be issued until you answer.\n\n  Visitor: {{subject_name}}\n  Purpose: {{purpose}}\n  Pass:    {{pass_no}}\n\nApprove or refuse it from the desk.',
     '{{subject_name}} is at the gate for you. Please approve or refuse.', 0,
     JSON_ARRAY('subject_name', 'purpose', 'pass_no')),

    (NULL, 'material.approval_needed', 'in_app', 'en', NULL,
     '{{lines}} line(s) of goods want to leave on {{pass_no}}. {{value}}',
     'Goods on {{pass_no}} need approval.', 0, JSON_ARRAY('lines', 'pass_no', 'value')),
    (NULL, 'material.approval_needed', 'email', 'en', 'Goods need approval: {{pass_no}}',
     'Goods are waiting at the gate for an approver.\n\n  Pass:   {{pass_no}}\n  Lines:  {{lines}}\n  Value:  {{value}}\n  Filed:  {{requester_name}}',
     '{{lines}} goods line(s) on {{pass_no}} need approval.', 0,
     JSON_ARRAY('pass_no', 'lines', 'value', 'requester_name')),
    (NULL, 'material.released', 'in_app', 'en', NULL,
     'Released at the gate: {{pass_no}} ({{lines}} line(s)).',
     'Goods on {{pass_no}} went out.', 0, JSON_ARRAY('pass_no', 'lines')),
    (NULL, 'material.received', 'in_app', 'en', NULL,
     'Goods came back in on {{pass_no}}.', '{{pass_no}} goods received back.', 0, JSON_ARRAY('pass_no')),
    (NULL, 'material.returned', 'in_app', 'en', NULL,
     'The item on {{pass_no}} is back.', '{{pass_no}} item returned.', 0, JSON_ARRAY('pass_no')),

    (NULL, 'outpass.approval_needed', 'in_app', 'en', NULL,
     '{{employee_no}} wants to step out for {{expected_minutes}} minutes. {{destination}}',
     'Step-out for {{employee_no}} needs approval.', 0,
     JSON_ARRAY('employee_no', 'expected_minutes', 'destination')),
    (NULL, 'outpass.overdue', 'in_app', 'en', NULL,
     '{{employee_no}} is out past the time on {{pass_no}} ({{minutes_out}} minutes).',
     '{{employee_no}} is overdue back.', 0, JSON_ARRAY('employee_no', 'pass_no', 'minutes_out')),
    (NULL, 'outpass.unreturned', 'in_app', 'en', NULL,
     '{{employee_no}} never scanned back on {{pass_no}}. Close it out.',
     '{{employee_no}} did not return before shift end.', 0, JSON_ARRAY('employee_no', 'pass_no')),
    (NULL, 'outpass.unreturned', 'email', 'en', 'Not back by end of shift: {{employee_no}}',
     'An employee left and never came back through the gate.\n\n  Employee: {{employee_no}}\n  Name:     {{subject_name}}\n  Pass:     {{pass_no}}\n  Destination: {{destination}}\n\nThe outpass stays open until somebody closes it.',
     '{{employee_no}} ({{pass_no}}) did not return.', 0,
     JSON_ARRAY('employee_no', 'subject_name', 'pass_no', 'destination')),
    (NULL, 'outpass.unreturned', 'sms', 'en', NULL,
     'AccessNow: {{employee_no}} did not return from a step-out ({{pass_no}}).',
     '{{employee_no}} did not return.', 0, JSON_ARRAY('employee_no', 'pass_no')),
    (NULL, 'outpass.departed', 'in_app', 'en', NULL,
     '{{employee_no}} went out at the gate: {{destination}}',
     '{{employee_no}} departed the site.', 0, JSON_ARRAY('employee_no', 'destination')),

    (NULL, 'vehicle.approval_needed', 'in_app', 'en', NULL,
     'Vehicle {{plate}} ({{driver}}) wants in on {{pass_no}}.',
     'Vehicle {{plate}} needs approval.', 0, JSON_ARRAY('plate', 'driver', 'pass_no')),
    (NULL, 'vehicle.approval_needed', 'email', 'en', 'Vehicle pass needs approval: {{plate}}',
     'A vehicle pass is waiting for an approver.\n\n  Plate:   {{plate}}\n  Driver:  {{driver}}\n  Pass:    {{pass_no}}\n  Dwell:   {{dwell_limit_minutes}} minutes',
     'Vehicle {{plate}} on {{pass_no}} needs approval.', 0,
     JSON_ARRAY('plate', 'driver', 'pass_no', 'dwell_limit_minutes')),
    (NULL, 'vehicle.overstaying', 'in_app', 'en', NULL,
     '{{plate}} is past its dwell limit by {{dwell_minutes}} minutes.',
     '{{plate}} is overstaying.', 0, JSON_ARRAY('plate', 'dwell_minutes'))
ON DUPLICATE KEY UPDATE
    subject    = VALUES(subject),
    body       = VALUES(body),
    short_body = VALUES(short_body),
    variables  = VALUES(variables);
