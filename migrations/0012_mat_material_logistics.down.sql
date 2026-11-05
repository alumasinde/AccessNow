-- 0012 module / material & asset logistics — view first, then child tables, parent last.

DROP VIEW IF EXISTS v_material_returns_outstanding;
DROP TABLE IF EXISTS material_item_events;
DROP TABLE IF EXISTS material_items;
DROP TABLE IF EXISTS material_passes;
