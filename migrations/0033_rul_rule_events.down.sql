-- =============================================================================
-- 0033 down — the rulebook's two notices go back to not existing.
--
-- rule.matched is deleted, which takes its system wording with it through the FK cascade
-- from notification_templates. gate.override_used keeps its catalogue row, because 0025
-- seeded it and a tenant route may already point at it; its name goes back to what 0025
-- wrote and its system wording is removed, which is what put it on the generic fallback.
--
-- Nothing else changes: the rulebook's tables came from 0017 and are untouched here.
-- =============================================================================

DELETE FROM notification_templates
 WHERE tenant_id IS NULL
   AND event_key IN ('rule.matched', 'gate.override_used');

DELETE FROM notification_events
 WHERE event_key = 'rule.matched';

UPDATE notification_events
   SET name                    = 'A supervisor overrode a gate rule',
       default_channels        = JSON_ARRAY('in_app'),
       default_recipient_types = JSON_ARRAY('gate_supervisor'),
       is_opt_out              = 1
 WHERE event_key = 'gate.override_used';
