-- Owner: Segment/Audience. Preserve immutable first-paid evidence for the
-- qualified member from refresh staging through its append-only entered event.
-- Existing events and staged batches remain unknown; no historical time is inferred.
ALTER TABLE segment_audience_snapshot_members
    ADD COLUMN first_paid_at TIMESTAMPTZ;

ALTER TABLE segment_audience_member_events
    ADD COLUMN first_paid_at TIMESTAMPTZ;

ALTER TABLE segment_audience_refresh_batches
    ADD COLUMN member_fact_digest BYTEA NOT NULL DEFAULT decode(repeat('00', 32), 'hex')
    CHECK (octet_length(member_fact_digest) = 32);

ALTER TABLE segment_audience_refresh_batches
    ALTER COLUMN member_fact_digest DROP DEFAULT;
