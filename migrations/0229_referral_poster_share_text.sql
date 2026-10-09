-- Referral owns both the published artwork and its optional sharing copy.
-- Existing artwork, description and publication receipts are preserved.
ALTER TABLE referral_campaign_posters ADD COLUMN share_text TEXT NOT NULL DEFAULT '';
