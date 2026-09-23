-- Owner: internal/wecom. WelcomeCode is encrypted at the authenticated
-- callback boundary and exposed downstream only as an opaque one-time grant.
-- Forward-only; ciphertext is retained for audit and becomes unreadable after
-- expiry/consumption because runtime redemption is CAS guarded.
CREATE TABLE wecom_welcome_grants (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    callback_digest BYTEA NOT NULL UNIQUE CHECK(octet_length(callback_digest)=32),
    value_digest BYTEA NOT NULL CHECK(octet_length(value_digest)=32),
    ciphertext BYTEA NOT NULL CHECK(octet_length(ciphertext)>28 AND octet_length(ciphertext)<=8192),
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    consumer_effect_ref TEXT CHECK(consumer_effect_ref IS NULL OR consumer_effect_ref ~ '^eer_[1-9][0-9]*$'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CHECK(expires_at>created_at),
    CHECK((consumed_at IS NULL AND consumer_effect_ref IS NULL) OR (consumed_at IS NOT NULL AND consumer_effect_ref IS NOT NULL))
);
CREATE FUNCTION wecom_welcome_grant_guard() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
  IF TG_OP IN ('DELETE','TRUNCATE') OR OLD.consumed_at IS NOT NULL OR NEW.callback_digest IS DISTINCT FROM OLD.callback_digest OR NEW.value_digest IS DISTINCT FROM OLD.value_digest OR NEW.ciphertext IS DISTINCT FROM OLD.ciphertext OR NEW.expires_at IS DISTINCT FROM OLD.expires_at OR NEW.created_at IS DISTINCT FROM OLD.created_at OR NEW.consumed_at IS NULL OR NEW.consumer_effect_ref IS NULL THEN RAISE EXCEPTION 'welcome grant is immutable except one-time consumption'; END IF;
  RETURN NEW;
END; $$;
CREATE TRIGGER wecom_welcome_grants_guard BEFORE UPDATE OR DELETE ON wecom_welcome_grants FOR EACH ROW EXECUTE FUNCTION wecom_welcome_grant_guard();
CREATE TRIGGER wecom_welcome_grants_no_truncate BEFORE TRUNCATE ON wecom_welcome_grants FOR EACH STATEMENT EXECUTE FUNCTION wecom_welcome_grant_guard();
