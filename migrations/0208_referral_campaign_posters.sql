-- Referral owns the published snapshot. Admin Media remains private and is
-- read only when an authorized editor publishes a new poster set.
ALTER TABLE referral_operation_receipts DROP CONSTRAINT referral_operation_receipts_operation_check;
ALTER TABLE referral_operation_receipts ADD CONSTRAINT referral_operation_receipts_operation_check
    CHECK (operation IN ('join','issue_invitation','campaign_create','campaign_update','campaign_state','team_create','invite_reverse','invite_revoke','reward_record','campaign_close','campaign_posters'));

CREATE TABLE referral_campaign_posters (
    campaign_id BIGINT NOT NULL REFERENCES referral_campaigns(id) ON DELETE RESTRICT,
    slot SMALLINT NOT NULL CHECK (slot BETWEEN 1 AND 3),
    source_image_id BIGINT NOT NULL CHECK (source_image_id > 0),
    description TEXT NOT NULL CHECK (char_length(description) <= 80),
    media_type TEXT NOT NULL CHECK (media_type IN ('image/png', 'image/jpeg')),
    image_bytes BYTEA NOT NULL CHECK (octet_length(image_bytes) BETWEEN 1 AND 5242880),
    image_sha256 BYTEA NOT NULL CHECK (octet_length(image_sha256) = 32),
    published_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (campaign_id, slot)
);
