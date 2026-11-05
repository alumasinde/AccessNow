-- 0016 module / approval workflow — view first, then child tables, parent last.

DROP VIEW IF EXISTS v_approval_inbox;
DROP TABLE IF EXISTS approval_delegations;
DROP TABLE IF EXISTS approval_request_steps;
DROP TABLE IF EXISTS approval_requests;
DROP TABLE IF EXISTS approval_flow_steps;
DROP TABLE IF EXISTS approval_flows;
