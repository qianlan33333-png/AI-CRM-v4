-- Invoice-owned recovery preserves receipts and historical audit while moving
-- an explicitly verified failed test to its canonical transaction reference.
ALTER TABLE invoice_operation_receipts DROP CONSTRAINT invoice_operation_receipts_application_id_fkey;
ALTER TABLE invoice_operation_receipts ADD CONSTRAINT invoice_operation_receipts_application_id_fkey
 FOREIGN KEY(application_id) REFERENCES invoice_applications(id) ON UPDATE CASCADE;
ALTER TABLE invoice_callback_receipts DROP CONSTRAINT invoice_callback_receipts_application_id_fkey;
ALTER TABLE invoice_callback_receipts ADD CONSTRAINT invoice_callback_receipts_application_id_fkey
 FOREIGN KEY(application_id) REFERENCES invoice_applications(id) ON UPDATE CASCADE;
ALTER TABLE invoice_audit_events DROP CONSTRAINT invoice_audit_events_application_id_fkey;
ALTER TABLE invoice_audit_events ADD CONSTRAINT invoice_audit_events_application_id_fkey
 FOREIGN KEY(application_id) REFERENCES invoice_applications(id) ON UPDATE CASCADE;
-- No buyer title or credential is copied into these attempt materials.
ALTER TABLE invoice_audit_events ADD COLUMN material JSONB NOT NULL DEFAULT '{}'::jsonb CHECK(jsonb_typeof(material)='object');
