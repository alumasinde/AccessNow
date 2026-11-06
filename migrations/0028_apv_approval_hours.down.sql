-- 0028 module / approval workflow routing
-- Only the added column and its constraint. Flows that were saved with the flag set lose
-- their after-hours rule, so re-applying 0028 does not restore a tenant's routing; it
-- restores the column the routing was stored in.

ALTER TABLE approval_flows
    DROP CONSTRAINT IF EXISTS chk_approval_flows_outside_hours;

ALTER TABLE approval_flows
    DROP COLUMN IF EXISTS outside_hours_only;
