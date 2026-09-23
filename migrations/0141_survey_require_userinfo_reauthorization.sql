-- Owner: Survey. Forward-only authorization policy cutover.
-- Keep session records, answer data and identity ownership unchanged. Active
-- pre-policy sessions have no proof of a userinfo UnionID read, so revoke them
-- once. New sessions are issued by the strict userinfo adapter after deployment.
UPDATE survey_identity_sessions
SET revoked_at = CURRENT_TIMESTAMP
WHERE revoked_at IS NULL AND expires_at > CURRENT_TIMESTAMP;
