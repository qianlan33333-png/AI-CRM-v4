-- Owner: WeCom. Nullable provider/callback evidence; no historical timestamps
-- are inferred from local observation or row creation time. Forward-only.
ALTER TABLE wecom_customer_owner_observations
    ADD COLUMN followed_at TIMESTAMPTZ;

ALTER TABLE wecom_follow_relationships
    ADD COLUMN followed_at TIMESTAMPTZ;
