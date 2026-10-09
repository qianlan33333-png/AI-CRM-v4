-- Preserve published bytes and receipts. Legacy dimensions are read from the
-- frozen image header until a layout update writes their local metadata.
ALTER TABLE referral_operation_receipts DROP CONSTRAINT referral_operation_receipts_operation_check;
ALTER TABLE referral_operation_receipts ADD CONSTRAINT referral_operation_receipts_operation_check
    CHECK (operation IN ('join','issue_invitation','campaign_create','campaign_update','campaign_state','team_create','invite_reverse','invite_revoke','reward_record','campaign_close','campaign_posters','issue_product_activity_context','poster_layouts'));

ALTER TABLE referral_campaign_posters
    ADD COLUMN image_width INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN image_height INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN qr_layout JSONB,
    ADD CONSTRAINT referral_poster_dimensions CHECK (
        (image_width = 0 AND image_height = 0) OR (image_width > 0 AND image_height > 0)),
    ADD CONSTRAINT referral_poster_layout_object CHECK (
        qr_layout IS NULL OR jsonb_typeof(qr_layout) = 'object');
