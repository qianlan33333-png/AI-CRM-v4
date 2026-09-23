-- Owner: outbound. Existing encrypted legacy tasks retain their exact protocol.
ALTER TABLE outbound_commerce_push_intents ADD COLUMN payload_mode text NOT NULL DEFAULT 'legacy' CHECK(payload_mode IN ('legacy','custom_fields_v1'));
