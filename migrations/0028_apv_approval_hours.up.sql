-- 0028 module / approval workflow routing — a flow that only matches after hours
--
-- Value bands, departments, sites and pass types already route a flow. What the schema
-- could not express is the tenant's other common rule: the same goods pass that a store
-- clerk signs off at noon needs the security manager at 2am. This adds the time dimension
-- to the match. The window itself is not stored here - it belongs to the site, in
-- sites.settings under business_hours, because a night shift in Mombasa is not a night
-- shift in Nairobi.
--
-- Re-runnable: IF NOT EXISTS on the column, and the constraint is dropped before it is
-- added, which is how MariaDB makes an ADD CONSTRAINT idempotent.

ALTER TABLE approval_flows
    ADD COLUMN IF NOT EXISTS outside_hours_only TINYINT(1) NOT NULL DEFAULT 0
        COMMENT '1 = this flow only matches a request raised outside the site''s business hours';

ALTER TABLE approval_flows
    DROP CONSTRAINT IF EXISTS chk_approval_flows_outside_hours;

ALTER TABLE approval_flows
    ADD CONSTRAINT chk_approval_flows_outside_hours CHECK (outside_hours_only IN (0, 1));
