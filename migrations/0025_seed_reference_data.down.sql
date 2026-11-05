-- 0025 seed / reference data.
-- Seeds are additive, so a rollback only removes rows created by this file.
-- The catalogues themselves belong to the table-creating migrations, so the down
-- deletes the seeded rows and leaves the tables standing.

DELETE FROM report_definitions
WHERE definition_key IN ('gate_movements_daily', 'visitor_log', 'host_activity', 'vehicle_dwell_times',
                         'material_outstanding', 'material_exceptions', 'outpass_register', 'contractor_expiry',
                         'blocklist_hits', 'approval_latency', 'gate_utilisation', 'guard_activity', 'audit_summary');

DELETE FROM notification_events
WHERE event_key IN ('visit.host_approval_needed', 'visit.approved', 'visit.rejected', 'visit.arrived',
                    'visit.departed', 'visit.expired', 'preauth.invitation_sent', 'vehicle.overstaying',
                    'vehicle.exited', 'material.approval_needed', 'material.released', 'material.item_overdue',
                    'material.exception', 'outpass.approval_needed', 'outpass.overdue', 'outpass.unreturned',
                    'contractor.document_expiring', 'contractor.pass_blocked', 'blocklist.watched_hit',
                    'blocklist.denied_entry', 'approval.awaiting_me', 'approval.escalated', 'gate.override_used',
                    'shift.handover_due', 'rollcall.incident_started', 'rollcall.still_unaccounted',
                    'report.ready', 'sync.conflicts_found', 'device.offline');

DELETE FROM outpass_reasons
WHERE reason_key IN ('personal_errand', 'medical_appointment', 'family_emergency', 'official_business',
                     'government_office', 'transport_delay', 'other');

DELETE FROM document_types
WHERE type_key IN ('national_id', 'work_permit', 'police_clearance', 'safety_induction', 'drivers_licence',
                   'vehicle_insurance', 'vehicle_fitness', 'hazmat_permit', 'public_liability_insurance');

DELETE FROM pass_types
WHERE type_key IN ('visitor_walk_in', 'visitor_pre_registered', 'vehicle_delivery', 'vehicle_fleet',
                   'vehicle_visitor', 'material_outward_rgp', 'material_outward_nrgp', 'material_inward',
                   'material_personal_item', 'staff_outpass', 'contractor_recurring', 'contractor_day',
                   'service_visit', 'emergency_access');

DELETE FROM pass_categories
WHERE category_key IN ('visitor', 'staff', 'vehicle', 'material', 'contractor', 'service', 'emergency');

DELETE FROM modules
WHERE module_key IN ('core', 'visitor_management', 'vehicle_tracking', 'material_logistics', 'staff_outpass',
                     'contractor_access', 'qr_scanning', 'approval_workflow', 'rules_policy', 'blocklist_watchlist',
                     'notifications', 'offline_gate', 'emergency_rollcall', 'reports_exports', 'audit_compliance',
                     'integrations_api');
