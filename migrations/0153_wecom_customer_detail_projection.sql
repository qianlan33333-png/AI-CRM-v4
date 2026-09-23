-- Owner: internal/wecom. A remark belongs to a provider follow-user
-- observation, never to OneID or the Customer directory projection. NULL
-- means pre-projection history; '' is an explicit Provider fact.
ALTER TABLE wecom_customer_owner_observations
  ADD COLUMN remark TEXT CHECK (
    remark IS NULL OR char_length(remark) <= 2000
  );
