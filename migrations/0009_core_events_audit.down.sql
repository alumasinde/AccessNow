-- 0009 core / events & audit — view first, then child tables, parent last.

DROP VIEW IF EXISTS v_recent_gate_decisions;
DROP TABLE IF EXISTS audit_chain_tips;
DROP TABLE IF EXISTS audit_logs;
DROP TABLE IF EXISTS domain_events;
