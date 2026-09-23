-- Owner: config. Keys are AEAD ciphertext; audit records contain no credentials.
CREATE TABLE config_ai_model (
 id BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (id),
 provider TEXT NOT NULL,
 model TEXT NOT NULL,
 key_ciphertext BYTEA NOT NULL,
 version BIGINT NOT NULL CHECK (version>0),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE config_ai_model_audits (
 id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 actor_id BIGINT NOT NULL,
 version BIGINT NOT NULL,
 occurred_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
