-- =============================================================================
-- 0034 module / offline_gate — the notice the ledger sends, and the two reads
-- the desk and the device make on every pull.
--
-- 1. THE NOTICE. sync.conflicts_found was seeded by 0025 with a name and a recipient role
--    and has never had a publisher - until the offline ledger, which is exactly the thing it
--    was describing: a tablet that kept working while the link was down has now handed in a
--    queue the server disagrees with in places. It gets wording here because a supervisor
--    reading "Offline batch replayed with conflicts" learns nothing, while one reading which
--    device, how many rows and how many of them are argued about knows what to open.
--
--    in_app only, as 0025 chose. A batch that argued about half its rows is a queue of work
--    for the desk, not an emergency, and the same device can produce one every time it loses
--    signal.
--
-- 2. THE READS. The desk pages batches newest first across a whole tenant, and the only
--    index on that table starts at gate_device_id, so the tenant-wide sort had nothing to
--    walk. idx_device_sync_batches_feed is the sort and the cursor together, in the shape
--    every other feed in this schema has after 0031.
--
--    The other one is hotter: every device pull asks "what does this tablet hold at version
--    N", and uk_offline_cache_manifest cannot answer it without reading the device's whole
--    history of caches, because pass_id sits between the two columns the question names.
--    idx_offline_cache_live is the question's own shape, including superseded_at, which is
--    how the read says "the live set" rather than "the newest epoch, then throw rows away".
--
-- Re-runnable: ADD INDEX IF NOT EXISTS, and INSERT ... ON DUPLICATE KEY UPDATE.
-- =============================================================================

ALTER TABLE device_sync_batches
    ADD INDEX IF NOT EXISTS idx_device_sync_batches_feed (tenant_id, started_at, id);

ALTER TABLE offline_cache_manifests
    ADD INDEX IF NOT EXISTS idx_offline_cache_live (gate_device_id, cache_version, superseded_at);

INSERT INTO notification_events
    (event_key, name, module_key, default_channels, default_recipient_types, is_opt_out)
VALUES
    ('sync.conflicts_found', 'A device handed back a queue the server disagreed with', 'offline_gate',
     JSON_ARRAY('in_app'), JSON_ARRAY('gate_supervisor'), 1)
ON DUPLICATE KEY UPDATE
    name                    = VALUES(name),
    module_key              = VALUES(module_key),
    default_channels        = VALUES(default_channels),
    default_recipient_types = VALUES(default_recipient_types),
    is_opt_out              = VALUES(is_opt_out),
    status                  = 'active';

-- The counts are the sentence: {{record_count}} is what the device sent and {{conflict_count}}
-- is what the two sides could not agree about, and a supervisor decides from the gap between
-- them whether this is one bad tablet or a shift that has to be gone through row by row.
-- {{client_batch_id}} is the device's own name for the queue, which is the only name the
-- tablet will repeat, so it is the one worth reading out loud.
INSERT INTO notification_templates
    (tenant_id, event_key, channel, locale, subject, body, short_body, is_html, variables)
VALUES
    (NULL, 'sync.conflicts_found', 'in_app', 'en', NULL,
     '{{device_label}} handed in {{record_count}} buffered scans, and {{conflict_count}} of them are argued about. Batch {{client_batch_id}}.',
     '{{conflict_count}} conflicts in {{device_label}}''s batch {{client_batch_id}}.', 0,
     JSON_ARRAY('device_label', 'record_count', 'conflict_count', 'client_batch_id'))
ON DUPLICATE KEY UPDATE
    body       = VALUES(body),
    short_body = VALUES(short_body),
    variables  = VALUES(variables);
