-- =============================================================================
-- 0034 down — the ledger's notice goes back to having no wording, and the two
-- reads go back to sorting without an index.
--
-- The catalogue row itself stays: 0025 seeded it, a tenant route may already point at it,
-- and the name it was given is the one that row had before this file re-worded it.
-- =============================================================================

DELETE FROM notification_templates
 WHERE tenant_id IS NULL
   AND event_key = 'sync.conflicts_found';

UPDATE notification_events
   SET name                    = 'Offline batch replayed with conflicts',
       default_channels        = JSON_ARRAY('in_app'),
       default_recipient_types = JSON_ARRAY('gate_supervisor'),
       is_opt_out              = 1
 WHERE event_key = 'sync.conflicts_found';

ALTER TABLE offline_cache_manifests
    DROP INDEX IF EXISTS idx_offline_cache_live;

ALTER TABLE device_sync_batches
    DROP INDEX IF EXISTS idx_device_sync_batches_feed;
