-- Owner: outbound. Deferred references and private delivery evidence.
ALTER TABLE outbound_private_message_intents ADD COLUMN deferred_target_reference TEXT NOT NULL DEFAULT '';
ALTER TABLE outbound_private_message_intents DROP CONSTRAINT ck_outbound_private_message_refs;
ALTER TABLE outbound_private_message_intents ADD CONSTRAINT ck_outbound_private_message_refs CHECK (
 length(btrim(source_reference)) BETWEEN 1 AND 200 AND length(btrim(payload_reference)) BETWEEN 1 AND 200 AND
 ((customer_id > 0 AND staff_id > 0 AND deferred_target_reference='') OR
 (customer_id=0 AND staff_id=0 AND deferred_target_reference=payload_reference AND deferred_target_reference LIKE 'aiassistant:%')));
CREATE TABLE outbound_private_message_receipts (
 payload_reference TEXT PRIMARY KEY REFERENCES outbound_private_message_intents(source_reference),
 message_id TEXT NOT NULL DEFAULT '', sender_userid TEXT NOT NULL DEFAULT '', external_userid TEXT NOT NULL DEFAULT '',
 reason TEXT NOT NULL DEFAULT '', delivery_status INTEGER, sent_at TIMESTAMPTZ,
 observed_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
-- Raw provider identity values above are restricted business evidence, never effect envelopes or logs.
