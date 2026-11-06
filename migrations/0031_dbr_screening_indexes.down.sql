-- =============================================================================
-- 0031 down — every index dropped, every key retargeted back to the bare parent id,
-- line_value returned to a plain column nobody writes, and the four new notices made
-- inactive rather than deleted: a tenant that already saved a route against one keeps the
-- row and simply stops being offered it.
--
-- The old single-column FKs are only re-added after the rows have been checked, because
-- a cross-tenant id that 0031 made impossible is exactly what the older key would accept
-- again. Rolling this back re-opens that hole on purpose, which is why the data has to be
-- clean before the constraint goes back on.
-- =============================================================================

UPDATE notification_events SET status = 'inactive'
    WHERE event_key IN ('visit.no_show', 'visit.expired', 'material.overdue', 'material.disputed');

DELETE FROM notification_templates
    WHERE tenant_id IS NULL
      AND event_key IN ('visit.no_show', 'visit.expired', 'material.overdue', 'material.disputed');

ALTER TABLE visitors
    ADD FULLTEXT INDEX ft_visitors_name (first_name, last_name);

ALTER TABLE visit_preauthorizations
    DROP INDEX IF EXISTS uk_visit_preauth_code;

ALTER TABLE visit_preauthorizations
    ADD UNIQUE INDEX IF NOT EXISTS uk_visit_preauth_code (code);

ALTER TABLE material_items
    MODIFY COLUMN line_value DECIMAL(16, 2) NULL;

-- 3. Back to the bare parent ids.

ALTER TABLE blocklist_entries
    DROP FOREIGN KEY IF EXISTS fk_blocklist_incident;

ALTER TABLE blocklist_entries
    DROP INDEX IF EXISTS idx_blocklist_incident;

ALTER TABLE blocklist_entries
    ADD CONSTRAINT fk_blocklist_incident FOREIGN KEY (incident_pass_id)
        REFERENCES passes (id) ON DELETE SET NULL;

ALTER TABLE visit_preauthorizations
    DROP FOREIGN KEY IF EXISTS fk_visit_preauth_pass;

ALTER TABLE visit_preauthorizations
    DROP INDEX IF EXISTS idx_visit_preauth_pass;

ALTER TABLE visit_preauthorizations
    ADD CONSTRAINT fk_visit_preauth_pass FOREIGN KEY (pass_id)
        REFERENCES passes (id) ON DELETE SET NULL;

ALTER TABLE visit_preauthorizations
    DROP FOREIGN KEY IF EXISTS fk_visit_preauth_visitor;

ALTER TABLE visit_preauthorizations
    DROP INDEX IF EXISTS idx_visit_preauth_visitor;

ALTER TABLE visit_preauthorizations
    ADD CONSTRAINT fk_visit_preauth_visitor FOREIGN KEY (visitor_id)
        REFERENCES visitors (id) ON DELETE SET NULL;

ALTER TABLE visits
    DROP FOREIGN KEY IF EXISTS fk_visits_company;

ALTER TABLE visits
    DROP INDEX IF EXISTS idx_visits_company;

ALTER TABLE visits
    ADD INDEX IF NOT EXISTS idx_visits_company (visitor_company_id);

ALTER TABLE visits
    ADD CONSTRAINT fk_visits_company FOREIGN KEY (visitor_company_id)
        REFERENCES visitor_companies (id) ON DELETE SET NULL;

ALTER TABLE visitors
    DROP FOREIGN KEY IF EXISTS fk_visitors_company;

ALTER TABLE visitors
    DROP INDEX IF EXISTS idx_visitors_company;

ALTER TABLE visitors
    ADD INDEX IF NOT EXISTS idx_visitors_company (visitor_company_id);

ALTER TABLE visitors
    ADD CONSTRAINT fk_visitors_company FOREIGN KEY (visitor_company_id)
        REFERENCES visitor_companies (id) ON DELETE SET NULL;

-- 2 and 1. The feeds and the screening path.

ALTER TABLE vehicle_passes  DROP INDEX IF EXISTS idx_vehicle_passes_created;
ALTER TABLE vehicles        DROP INDEX IF EXISTS idx_vehicles_created;
ALTER TABLE material_passes DROP INDEX IF EXISTS idx_material_passes_created;
ALTER TABLE outpasses       DROP INDEX IF EXISTS idx_outpasses_created;
ALTER TABLE visits          DROP INDEX IF EXISTS idx_visits_created;
ALTER TABLE blocklist_hits  DROP INDEX IF EXISTS idx_blocklist_hits_feed;
ALTER TABLE blocklist_entries DROP INDEX IF EXISTS idx_blocklist_added;
ALTER TABLE blocklist_entries DROP INDEX IF EXISTS idx_blocklist_screen;
