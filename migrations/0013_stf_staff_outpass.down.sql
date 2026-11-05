-- 0013 module / staff outpass — view first, then child tables, parent last.

DROP VIEW IF EXISTS v_staff_out_of_site;
DROP TABLE IF EXISTS outpasses;
DROP TABLE IF EXISTS outpass_reasons;
