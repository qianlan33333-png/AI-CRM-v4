-- Owner: referral. Preserve legacy invitation history without inventing an
-- issuer identity. New financial use requires an actual verified issue action.
ALTER TABLE referral_invitations
  ADD COLUMN issuer_identity_id BIGINT NOT NULL DEFAULT 0,
  ADD COLUMN issuer_app_id TEXT NOT NULL DEFAULT '',
  ADD COLUMN issuer_app_scope TEXT NOT NULL DEFAULT '',
  ADD COLUMN issuer_channel TEXT NOT NULL DEFAULT '',
  ADD CONSTRAINT referral_invitation_issuer_shape CHECK (
    (issuer_identity_id=0 AND issuer_app_id='' AND issuer_app_scope='' AND issuer_channel='')
    OR
    (issuer_identity_id>0 AND issuer_app_id=btrim(issuer_app_id) AND char_length(issuer_app_id) BETWEEN 1 AND 160
      AND issuer_app_scope=btrim(issuer_app_scope) AND char_length(issuer_app_scope) BETWEEN 1 AND 240
      AND issuer_channel IN ('mini_program','h5_official_account'))
  );
