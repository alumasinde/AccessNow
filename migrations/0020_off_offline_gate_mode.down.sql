-- 0020 module / offline gate mode — view first, then child tables, parent last.

DROP VIEW IF EXISTS v_device_sync_health;
DROP TABLE IF EXISTS sync_conflicts;
DROP TABLE IF EXISTS offline_cache_manifests;
DROP TABLE IF EXISTS device_sync_batches;
