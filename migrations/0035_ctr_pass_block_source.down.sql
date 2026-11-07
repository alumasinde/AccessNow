-- 0035 module / contractor — drop the block source, guarded so a second run is a no-op.

SET @sql = IF(
    (SELECT COUNT(*) FROM information_schema.COLUMNS
      WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'contractor_passes'
        AND COLUMN_NAME = 'blocked_source') = 1,
    'ALTER TABLE contractor_passes DROP COLUMN blocked_source',
    'SELECT ''already removed''');
PREPARE s FROM @sql;
EXECUTE s;
DEALLOCATE PREPARE s;
