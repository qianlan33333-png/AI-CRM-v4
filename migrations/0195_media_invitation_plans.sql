-- Owner: media. Existing material IDs and links remain valid.
CREATE TABLE media_invitation_plans (
 invite_id BIGINT PRIMARY KEY REFERENCES media_group_invites(id),
 public_token TEXT NOT NULL UNIQUE CHECK(public_token ~ '^[0-9a-f]{48}$'),
 mode TEXT NOT NULL CHECK(mode IN ('single','sequence')),
 threshold INTEGER,
 state TEXT NOT NULL DEFAULT 'preparing' CHECK(state IN ('preparing','active','paused','full','stale')),
 current_chat_id TEXT NOT NULL DEFAULT '',
 bindings JSONB NOT NULL CHECK(jsonb_typeof(bindings)='array'),
 CHECK((mode='single' AND threshold IS NULL) OR (mode='sequence' AND threshold BETWEEN 1 AND 200 AND threshold IS NOT NULL))
);
CREATE TABLE media_invitation_codes (
 chat_id TEXT PRIMARY KEY,
 source_digest TEXT NOT NULL UNIQUE,
 effect_id TEXT UNIQUE CHECK(effect_id ~ '^eer_[1-9][0-9]*$'),
 state TEXT NOT NULL DEFAULT 'accepted',
 config_id TEXT NOT NULL DEFAULT '',
 qr_code TEXT NOT NULL DEFAULT ''
);
CREATE TABLE media_invitation_changes (
 id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 invite_id BIGINT NOT NULL REFERENCES media_invitation_plans(invite_id),
 operation TEXT NOT NULL,
 actor_id BIGINT,
 from_chat TEXT NOT NULL DEFAULT '',
 to_chat TEXT NOT NULL DEFAULT '',
 at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
