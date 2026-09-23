-- Existing OpenID-only sessions must reauthorize; no historical proof is inferred.
ALTER TABLE payment_sessions ADD COLUMN unionid_verified boolean NOT NULL DEFAULT false;
