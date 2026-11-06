-- =============================================================================
-- 0030 module / notifications — reverse
--
-- Drops the catalogue rows and system templates this migration added. Deleting a
-- notification_events row cascades to its templates and to any tenant routes built on it,
-- which is the only honest reversal: a route for an event the system never publishes cannot
-- be left configured. Templates for keys that 0025 already had (material.approval_needed,
-- outpass.overdue and the rest) are removed by name here, since their catalogue row stays.
-- =============================================================================

DELETE FROM notification_templates
WHERE tenant_id IS NULL
  AND event_key IN (
    'approval.step.assigned', 'approval.decided', 'approval.rejected', 'approval.returned',
    'approval.cancelled', 'approval.reset', 'approval.delegated',
    'approval.escalated',
    'pass.approved', 'pass.rejected', 'pass.cancelled', 'pass.expired',
    'visit.created', 'approval.requested',
    'material.declared', 'material.received', 'material.returned', 'material.approval_needed',
    'material.released',
    'outpass.requested', 'outpass.departed', 'outpass.returned',
    'outpass.approval_needed', 'outpass.overdue', 'outpass.unreturned',
    'vehicle.logged', 'vehicle.approval_needed', 'vehicle.entered', 'vehicle.overstaying'
  );

DELETE FROM notification_events
WHERE event_key IN (
    'approval.step.assigned', 'approval.decided', 'approval.rejected', 'approval.returned',
    'approval.cancelled', 'approval.reset', 'approval.delegated',
    'pass.approved', 'pass.rejected', 'pass.cancelled', 'pass.expired',
    'visit.created', 'approval.requested',
    'material.declared', 'material.received', 'material.returned',
    'outpass.requested', 'outpass.departed', 'outpass.returned',
    'vehicle.logged', 'vehicle.approval_needed', 'vehicle.entered'
  );

UPDATE notification_events SET status = 'active' WHERE event_key = 'approval.awaiting_me';
