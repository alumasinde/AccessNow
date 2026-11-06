-- =============================================================================
-- 0032 up — make the notification catalogue describe the events the code really
-- publishes.
--
-- The outbox is the source of truth for this: every key written by a module was
-- compared against notification_events, which found the vocabulary drifting in both
-- directions. The gate reaction publishes visit.checked_in / visit.checked_out while
-- the catalogue offers visit.arrived / visit.departed, and the desk's cancel, the gate's
-- verification of an inward load and Core's second-review decision publish keys the
-- catalogue never heard of. Those four events left the outbox as no_subscriber, which is
-- indistinguishable from "nobody wanted to know" when it is really "the words were never
-- written down".
--
-- Two keys are RENAMED rather than retired and replaced, because the FKs from
-- notification_routes and notification_templates are ON UPDATE CASCADE: a tenant that
-- already routed visit.arrived keeps its route through the rename and gets no dead row.
--
-- The retired rows are the ones whose story another event already tells. A visitor pass
-- decision reaches people as pass.approved / pass.rejected (Core, addressed to the
-- requester), and the host's "please approve" is approval.requested - announcing both
-- visit.approved and pass.approved is two notices for one click. material.exception and
-- material.item_overdue are what 0031's material.disputed and material.overdue replaced.
--
-- Deliberately still not notices: pass.issued (a draft nobody has decided on),
-- pass.completed (every module says its own goodbye: visit.checked_out, material.returned,
-- outpass.returned), movement.recorded (a scan is data), and the blocklist register's
-- entry_added / entry_revoked / hit_resolved, which are audited rather than announced.
-- =============================================================================

-- 1. Rename the two arrival words to what the gate reaction emits.

UPDATE notification_events
   SET event_key               = 'visit.checked_in',
       name                    = 'An expected visitor arrived at the gate',
       module_key              = 'visitor_management',
       default_channels        = JSON_ARRAY('in_app'),
       default_recipient_types = JSON_ARRAY('host'),
       is_opt_out              = 1,
       status                  = 'active'
 WHERE event_key = 'visit.arrived';

UPDATE notification_events
   SET event_key               = 'visit.checked_out',
       name                    = 'A visitor has left the site',
       module_key              = 'visitor_management',
       default_channels        = JSON_ARRAY('in_app'),
       default_recipient_types = JSON_ARRAY('host'),
       is_opt_out              = 1,
       status                  = 'active'
 WHERE event_key = 'visit.departed';

-- 2. Retire the rows no code writes and another event already covers. Inactive rather
--    than deleted: a tenant route or a saved template survives, it is just not offered.

UPDATE notification_events
   SET status = 'inactive'
 WHERE event_key IN ('visit.approved', 'visit.rejected', 'visit.host_approval_needed',
                     'material.exception', 'material.item_overdue');

-- 3. The three published keys the catalogue never had.

INSERT INTO notification_events
    (event_key, name, module_key, default_channels, default_recipient_types, is_opt_out)
VALUES
    ('visit.cancelled',     'A visit was called off before it happened',  'visitor_management', JSON_ARRAY('in_app'), JSON_ARRAY('host'),      1),
    ('material.verified',   'Inward goods were checked at the gate and closed', 'material_logistics', JSON_ARRAY('in_app'), JSON_ARRAY('requester'), 0),
    ('pass.pending_review', 'A pass needs a second decision before it works',   'core',           JSON_ARRAY('in_app'), JSON_ARRAY('approver'),  1)
ON DUPLICATE KEY UPDATE
    name                    = VALUES(name),
    module_key              = VALUES(module_key),
    default_channels        = VALUES(default_channels),
    default_recipient_types = VALUES(default_recipient_types),
    is_opt_out              = VALUES(is_opt_out),
    status                  = 'active';

-- 4. System wording for all five keys, including the two renamed ones, which arrived with
--    catalogue rows and no template at all.

INSERT INTO notification_templates
    (tenant_id, event_key, channel, locale, subject, body, short_body, is_html, variables)
VALUES
    (NULL, 'visit.checked_in', 'in_app', 'en', NULL,
     '{{subject_name}} has arrived for {{purpose}}. Pass {{pass_no}} is on site.',
     NULL, 0, JSON_ARRAY('subject_name', 'purpose', 'pass_no')),
    (NULL, 'visit.checked_out', 'in_app', 'en', NULL,
     '{{subject_name}} has left. Visit {{pass_no}} is closed.',
     NULL, 0, JSON_ARRAY('subject_name', 'pass_no')),
    (NULL, 'visit.cancelled', 'in_app', 'en', NULL,
     '{{subject_name}}''s visit {{pass_no}} was cancelled. {{note}}',
     NULL, 0, JSON_ARRAY('subject_name', 'pass_no', 'note')),
    (NULL, 'material.verified', 'in_app', 'en', NULL,
     'The goods on {{pass_no}} were checked at the gate and the pass is closed. Declared by {{declared_by}}.',
     NULL, 0, JSON_ARRAY('pass_no', 'declared_by')),
    (NULL, 'pass.pending_review', 'in_app', 'en', NULL,
     'Pass {{pass_no}} is waiting for a second decision. {{note}}',
     NULL, 0, JSON_ARRAY('pass_no', 'note'))
ON DUPLICATE KEY UPDATE
    body      = VALUES(body),
    variables = VALUES(variables);

-- 5. The two screening alarms were seeded with a catalogue row and no wording at all, so
--    every notice was rendered from the generic fallback - and the fallback uses the
--    catalogue name, which says "checked in" even when the person was walking out. These
--    bodies name no direction, so one row serves an arrival and a departure.

UPDATE notification_events
   SET name = 'A watchlisted person or vehicle presented at a gate'
 WHERE event_key = 'blocklist.watched_hit';

INSERT INTO notification_templates
    (tenant_id, event_key, channel, locale, subject, body, short_body, is_html, variables)
VALUES
    (NULL, 'blocklist.watched_hit', 'in_app', 'en', NULL,
     '{{subject_name}} passed a gate while listed on the watchlist. Reason on file: {{reason}}',
     NULL, 0, JSON_ARRAY('subject_name', 'reason')),
    (NULL, 'blocklist.denied_entry', 'in_app', 'en', NULL,
     '{{subject_name}} was refused at the gate. Reason on file: {{reason}}',
     NULL, 0, JSON_ARRAY('subject_name', 'reason'))
ON DUPLICATE KEY UPDATE
    body      = VALUES(body),
    variables = VALUES(variables);
