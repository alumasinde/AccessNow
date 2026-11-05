-- 0024 module / audit & compliance — view first, then child tables, parent last.

DROP VIEW IF EXISTS v_purge_candidates;
DROP TABLE IF EXISTS audit_verifications;
DROP TABLE IF EXISTS data_purge_runs;
DROP TABLE IF EXISTS legal_holds;
DROP TABLE IF EXISTS retention_policies;
