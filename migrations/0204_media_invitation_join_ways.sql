-- A public invitation owns one stable WeCom join-way configuration. The
-- configuration is updated in place when the active group changes.
CREATE TABLE media_invitation_join_ways (
 invite_id BIGINT PRIMARY KEY REFERENCES media_invitation_plans(invite_id),
 source_digest TEXT NOT NULL UNIQUE,
 effect_id TEXT UNIQUE CHECK(effect_id ~ '^eer_[1-9][0-9]*$'),
 state TEXT NOT NULL DEFAULT 'accepted',
 config_id TEXT NOT NULL DEFAULT '',
 qr_code TEXT NOT NULL DEFAULT '',
 chat_ids JSONB NOT NULL CHECK(jsonb_typeof(chat_ids)='array')
);
