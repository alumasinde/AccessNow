-- =============================================================================
-- 0032 down — the arrival words go back to arrived/departed, the five retired rows are
-- offered again, and the three keys the code publishes but 0032 invented rows for are
-- deleted. The FKs from notification_templates and notification_routes are ON DELETE
-- CASCADE, so dropping a catalogue row takes its system wording with it and a tenant that
-- re-routed one loses the route it could not have used.
-- =============================================================================

UPDATE notification_events
   SET event_key               = 'visit.arrived',
       name                    = 'Visitor has arrived at the gate',
       default_channels        = JSON_ARRAY('in_app'),
       default_recipient_types = JSON_ARRAY('host'),
       is_opt_out              = 1,
       status                  = 'active'
 WHERE event_key = 'visit.checked_in';

UPDATE notification_events
   SET event_key               = 'visit.departed',
       name                    = 'Visitor has checked out',
       default_channels        = JSON_ARRAY('in_app'),
       default_recipient_types = JSON_ARRAY('host'),
       is_opt_out              = 0,
       status                  = 'active'
 WHERE event_key = 'visit.checked_out';

UPDATE notification_events
   SET status = 'active'
 WHERE event_key IN ('visit.approved', 'visit.rejected', 'visit.host_approval_needed',
                     'material.exception', 'material.item_overdue');

DELETE FROM notification_events
 WHERE event_key IN ('visit.cancelled', 'material.verified', 'pass.pending_review');

-- The two screening alarms go back to having no system wording, which is what put them on
-- the generic fallback in the first place, and their catalogue name goes back with them.

DELETE FROM notification_templates
 WHERE tenant_id IS NULL
   AND event_key IN ('blocklist.watched_hit', 'blocklist.denied_entry');

UPDATE notification_events
   SET name = 'Watchlisted person or vehicle checked in'
 WHERE event_key = 'blocklist.watched_hit';
