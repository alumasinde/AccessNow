-- 0027 core / pass numbering
-- Destructive: dropping these tables discards every tenant's number formats and the
-- counters, so the next pass issued after a re-apply starts from the beginning again.
-- Already issued passes keep their pass_no, which lives on the passes row.

DROP TABLE IF EXISTS pass_number_counters;
DROP TABLE IF EXISTS pass_number_formats;
