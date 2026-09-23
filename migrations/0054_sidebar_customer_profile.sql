-- Owner: internal/customer.
-- Sidebar profile updates are local Customer facts. The receipt is kept with
-- the profile CAS while platform audit_events and outbox_events are appended
-- in the same PostgreSQL Unit of Work.
CREATE TABLE customer_sidebar_profile_receipts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    key_digest BYTEA NOT NULL UNIQUE CHECK (octet_length(key_digest)=32),
    payload_digest BYTEA NOT NULL CHECK (octet_length(payload_digest)=32),
    customer_id BIGINT NOT NULL REFERENCES customers(id) ON DELETE RESTRICT,
    employee_digest BYTEA NOT NULL CHECK (octet_length(employee_digest)=32),
    outcome TEXT NOT NULL CHECK (outcome IN ('updated','version_conflict')),
    result_snapshot JSONB NOT NULL CHECK (jsonb_typeof(result_snapshot)='object'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX customer_sidebar_profile_receipts_customer_idx
    ON customer_sidebar_profile_receipts(customer_id, created_at DESC);
