-- =============================================================================
-- 0031 database review — the index the gate asks for, the sorts that had none,
-- five foreign keys that could cross a tenant, and two columns that never told
-- the truth.
--
-- 1. SCREENING. Every barrier asks blocklist_entries the same question - this tenant,
--    this identifier, this hash - and the only index that contains match_hash,
--    uk_blocklist_match, has list_type in its second column. The question does not know
--    the list type, because the list type is what it is finding out, so the prefix breaks
--    there and MariaDB - which has no skip scan - reads the tenant's whole register.
--    idx_blocklist_screen is the shape the query actually has.
--
-- 2. FEEDS. Every desk list ends in ORDER BY <timestamp> DESC, id DESC and none of those
--    timestamps is in an index. At demo volume a filesort is invisible; at a visit per
--    person per day it is the reason the register takes three seconds to open. Each head
--    table gets (tenant_id, <timestamp>, id), which is the sort and the cursor together.
--
-- 3. TENANTS. Five foreign keys point at a parent's bare (id) while the child carries its
--    own tenant_id, so a row may name another tenant's company, pass or visitor and the
--    database calls it valid. Every parent already has the (id, tenant_id) unique key these
--    need, so this is a retarget, not a redesign.
--
-- 4. COLUMNS. material_items.line_value was selected and shown but never written, so every
--    line reported a null value; it is now the arithmetic it always claimed to be.
--    visitors.visit_count and last_visited_at are written by the check-in reaction in Go,
--    which is what makes idx_visitors_activity - dead until now - mean something.
--    visit_preauthorizations.code was unique across every tenant at once, so two tenants
--    could not ever issue the same six-character link; nothing reads that table yet, so
--    the key changes while it costs nothing.
--    ft_visitors_name is dropped: no query in the system uses MATCH(), and visitor search
--    is a prefix LIKE.
--
-- 5. VOCABULARY. Four statuses the schema allows and no job reached: a visit that never
--    happened stayed 'approved' forever, and goods past their return date stayed
--    'released'. The sweeps that write them are in Go; the notices they publish are
--    registered here so a tenant can route them on day one.
--
-- Re-runnable: every statement uses IF [NOT] EXISTS, the MariaDB form - except the five
-- ADD CONSTRAINT fk_ lines, because MariaDB has no IF NOT EXISTS for a foreign key. Those
-- five are the ones that change what the database will accept, so running this file twice
-- stops there rather than pretending it was harmless.
-- =============================================================================

-- 1. The screening index, and the register's own newest-first feed.

ALTER TABLE blocklist_entries
    ADD INDEX IF NOT EXISTS idx_blocklist_screen (tenant_id, match_type, match_hash);

ALTER TABLE blocklist_entries
    ADD INDEX IF NOT EXISTS idx_blocklist_added (tenant_id, added_at, id);

ALTER TABLE blocklist_hits
    ADD INDEX IF NOT EXISTS idx_blocklist_hits_feed (tenant_id, occurred_at, id);

-- 2. The five head tables the desk pages through.

ALTER TABLE visits
    ADD INDEX IF NOT EXISTS idx_visits_created (tenant_id, created_at, id);

ALTER TABLE outpasses
    ADD INDEX IF NOT EXISTS idx_outpasses_created (tenant_id, created_at, id);

ALTER TABLE material_passes
    ADD INDEX IF NOT EXISTS idx_material_passes_created (tenant_id, created_at, id);

ALTER TABLE vehicles
    ADD INDEX IF NOT EXISTS idx_vehicles_created (tenant_id, created_at, id);

ALTER TABLE vehicle_passes
    ADD INDEX IF NOT EXISTS idx_vehicle_passes_created (tenant_id, created_at, id);

-- 3. The five keys that could reach across a tenant. Each one is dropped before the index
--    that backed it, and the replacement index carries both columns - the new key needs an
--    index with its own columns as a prefix, and the old lookup keeps one.
--
--    The delete rule changes from SET NULL to RESTRICT, and it has to: MariaDB will only
--    build a composite ON DELETE SET NULL key when every referencing column is nullable,
--    and tenant_id never is. RESTRICT is also the kinder rule for this data. A company is
--    taken off with deleted_at, not deleted, so a hard delete is an admin purge that ought
--    to say so; and a ban citing an incident pass, or an invitation citing a visitor, that
--    silently loses its reference when the retention job finally runs would leave the desk
--    explaining a decision whose evidence is gone. Refusing the purge is the same choice
--    fk_outpasses_reason and fk_visits_visitor already make, and the tenant cascade over
--    that shape is proven: deleting a tenant still clears its visits and visitors together.

ALTER TABLE visitors
    DROP FOREIGN KEY IF EXISTS fk_visitors_company;

ALTER TABLE visitors
    DROP INDEX IF EXISTS idx_visitors_company;

ALTER TABLE visitors
    ADD INDEX IF NOT EXISTS idx_visitors_company (visitor_company_id, tenant_id);

ALTER TABLE visitors
    ADD CONSTRAINT fk_visitors_company FOREIGN KEY (visitor_company_id, tenant_id)
        REFERENCES visitor_companies (id, tenant_id) ON DELETE RESTRICT;

ALTER TABLE visits
    DROP FOREIGN KEY IF EXISTS fk_visits_company;

ALTER TABLE visits
    DROP INDEX IF EXISTS idx_visits_company;

ALTER TABLE visits
    ADD INDEX IF NOT EXISTS idx_visits_company (visitor_company_id, tenant_id);

ALTER TABLE visits
    ADD CONSTRAINT fk_visits_company FOREIGN KEY (visitor_company_id, tenant_id)
        REFERENCES visitor_companies (id, tenant_id) ON DELETE RESTRICT;

ALTER TABLE visit_preauthorizations
    DROP FOREIGN KEY IF EXISTS fk_visit_preauth_visitor;

ALTER TABLE visit_preauthorizations
    DROP INDEX IF EXISTS fk_visit_preauth_visitor;

ALTER TABLE visit_preauthorizations
    ADD INDEX IF NOT EXISTS idx_visit_preauth_visitor (visitor_id, tenant_id);

ALTER TABLE visit_preauthorizations
    ADD CONSTRAINT fk_visit_preauth_visitor FOREIGN KEY (visitor_id, tenant_id)
        REFERENCES visitors (id, tenant_id) ON DELETE RESTRICT;

ALTER TABLE visit_preauthorizations
    DROP FOREIGN KEY IF EXISTS fk_visit_preauth_pass;

ALTER TABLE visit_preauthorizations
    DROP INDEX IF EXISTS fk_visit_preauth_pass;

ALTER TABLE visit_preauthorizations
    ADD INDEX IF NOT EXISTS idx_visit_preauth_pass (pass_id, tenant_id);

ALTER TABLE visit_preauthorizations
    ADD CONSTRAINT fk_visit_preauth_pass FOREIGN KEY (pass_id, tenant_id)
        REFERENCES passes (id, tenant_id) ON DELETE RESTRICT;

ALTER TABLE blocklist_entries
    DROP FOREIGN KEY IF EXISTS fk_blocklist_incident;

ALTER TABLE blocklist_entries
    DROP INDEX IF EXISTS fk_blocklist_incident;

ALTER TABLE blocklist_entries
    ADD INDEX IF NOT EXISTS idx_blocklist_incident (incident_pass_id, tenant_id);

ALTER TABLE blocklist_entries
    ADD CONSTRAINT fk_blocklist_incident FOREIGN KEY (incident_pass_id, tenant_id)
        REFERENCES passes (id, tenant_id) ON DELETE RESTRICT;

-- 4. The columns.

-- A line is worth what it is: declared quantity at the unit price the desk typed. Nothing
-- ever wrote this column, so no existing value is being replaced.
ALTER TABLE material_items
    MODIFY COLUMN line_value DECIMAL(16, 2)
        GENERATED ALWAYS AS (ROUND(quantity_declared * unit_value, 2)) STORED
        COMMENT 'declared quantity at the declared unit price';

-- The invitation link is only unique inside the tenant that issued it.
ALTER TABLE visit_preauthorizations
    DROP INDEX IF EXISTS uk_visit_preauth_code;

ALTER TABLE visit_preauthorizations
    ADD UNIQUE INDEX IF NOT EXISTS uk_visit_preauth_code (tenant_id, code);

-- Nobody searches a visitor by name with MATCH(); the desk searches a prefix LIKE, and a
-- FULLTEXT index is four columns of engine nobody asks.
ALTER TABLE visitors
    DROP INDEX IF EXISTS ft_visitors_name;

-- 5. The notices the two new sweeps send. in_app only: an expired visit and an overdue
--    load of goods are both somebody's desk business, and the wording says who has to do
--    something about it.

INSERT INTO notification_events
    (event_key, name, module_key, default_channels, default_recipient_types, is_opt_out)
VALUES
    ('visit.no_show',   'A visitor was expected and never arrived',   'visitor_management', JSON_ARRAY('in_app'), JSON_ARRAY('host'), 1),
    ('visit.expired',   'A visit went past its window unused',        'visitor_management', JSON_ARRAY('in_app'), JSON_ARRAY('host'), 1),
    ('material.overdue','Goods are past the date they were due back', 'material_logistics', JSON_ARRAY('in_app'), JSON_ARRAY('requester'), 1),
    ('material.disputed','Somebody has disputed a goods declaration', 'material_logistics', JSON_ARRAY('in_app'), JSON_ARRAY('requester'), 1)
ON DUPLICATE KEY UPDATE
    name                    = VALUES(name),
    module_key              = VALUES(module_key),
    default_channels        = VALUES(default_channels),
    default_recipient_types = VALUES(default_recipient_types),
    is_opt_out              = VALUES(is_opt_out),
    -- The down migration parks these rows as inactive instead of deleting them, because a
    -- tenant may have routed one of them. Re-arming here is what makes down-then-up work.
    status                  = 'active';

INSERT INTO notification_templates
    (tenant_id, event_key, channel, locale, subject, body, short_body, is_html, variables)
VALUES
    (NULL, 'visit.no_show', 'in_app', 'en', NULL,
     '{{subject_name}} never arrived for {{pass_no}}. The visit is closed as a no-show.',
     NULL, 0, JSON_ARRAY('subject_name', 'pass_no')),
    (NULL, 'visit.expired', 'in_app', 'en', NULL,
     'Nobody used {{pass_no}} for {{subject_name}} before its window ended. The visit is closed.',
     NULL, 0, JSON_ARRAY('pass_no', 'subject_name')),
    (NULL, 'material.overdue', 'in_app', 'en', NULL,
     'Goods on {{pass_no}} are past the date they were due back. Declared by {{declared_by}}.',
     NULL, 0, JSON_ARRAY('pass_no', 'declared_by')),
    (NULL, 'material.disputed', 'in_app', 'en', NULL,
     '{{pass_no}} is disputed: {{reason}}',
     NULL, 0, JSON_ARRAY('pass_no', 'reason'))
ON DUPLICATE KEY UPDATE
    body      = VALUES(body),
    variables = VALUES(variables);
