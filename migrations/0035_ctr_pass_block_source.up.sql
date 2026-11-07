-- =============================================================================
-- 0035 module / contractor — where a block came from
-- An expired document blocks a pass on its own, and renewing the document is allowed
-- to lift exactly that block. A supervisor who stopped a pass by hand must not be
-- un-stopped by the nightly sweep, so the two need to be told apart on the row rather
-- than guessed from the wording of a reason.
-- Guarded ALTER: re-running against a database that already has the column does nothing.
-- =============================================================================

SET @sql = IF(
    (SELECT COUNT(*) FROM information_schema.COLUMNS
      WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'contractor_passes'
        AND COLUMN_NAME = 'blocked_source') = 0,
    'ALTER TABLE contractor_passes ADD COLUMN blocked_source VARCHAR(16) NULL COMMENT ''document or manual'' AFTER blocked_reason',
    'SELECT ''already applied''');
PREPARE s FROM @sql;
EXECUTE s;
DEALLOCATE PREPARE s;
