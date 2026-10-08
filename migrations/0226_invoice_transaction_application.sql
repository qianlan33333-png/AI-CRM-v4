-- New applications use the frozen WeChat transaction as the Provider and
-- customer-visible application number. Existing test/audit rows are retained.
ALTER TABLE invoice_applications DROP CONSTRAINT invoice_applications_id_check;
ALTER TABLE invoice_applications ADD CONSTRAINT invoice_applications_id_check
 CHECK(length(id) BETWEEN 1 AND 32 AND id ~ '^[A-Za-z0-9_|*-]+$');
