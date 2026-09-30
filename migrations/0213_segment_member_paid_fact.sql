-- Owner: Segment/Audience. Preserve each opt-in customer's latest qualified
-- current-paid order from refresh staging through the immutable member event.
-- Existing snapshots/events remain unknown; no historical order is inferred.

ALTER TABLE segment_audience_snapshot_members
    ADD COLUMN paid_at TIMESTAMPTZ,
    ADD COLUMN paid_order_id BIGINT,
    ADD CONSTRAINT segment_audience_snapshot_members_paid_fact_check CHECK (
        (paid_at IS NULL AND paid_order_id IS NULL) OR
        (paid_at IS NOT NULL AND paid_order_id > 0)
    );

ALTER TABLE segment_audience_member_events
    ADD COLUMN event_kind TEXT NOT NULL DEFAULT 'audience.member_entered.v1',
    ADD COLUMN paid_at TIMESTAMPTZ,
    ADD COLUMN paid_order_id BIGINT;

ALTER TABLE segment_audience_member_events
    ALTER COLUMN event_kind DROP DEFAULT;

ALTER TABLE segment_audience_member_events
    DROP CONSTRAINT segment_audience_member_events_event_id_check;

ALTER TABLE segment_audience_member_events
    ADD CONSTRAINT segment_audience_member_events_kind_fact_check CHECK (
        (event_kind = 'audience.member_entered.v1' AND
            event_id = 'audmem_' || snapshot_id::text || '_' || customer_id::text AND
            ((paid_at IS NULL AND paid_order_id IS NULL) OR (paid_at IS NOT NULL AND paid_order_id > 0))) OR
        (event_kind = 'audience.member_paid_qualified.v1' AND
            event_id = 'audpay_' || snapshot_id::text || '_' || customer_id::text || '_' || paid_order_id::text AND
            paid_at IS NOT NULL AND paid_order_id > 0)
    );

ALTER TABLE segment_audience_refresh_batches
    ADD COLUMN member_fact_digest BYTEA NOT NULL DEFAULT decode(repeat('00', 32), 'hex')
    CHECK (octet_length(member_fact_digest) = 32);

ALTER TABLE segment_audience_refresh_batches
    ALTER COLUMN member_fact_digest DROP DEFAULT;
