-- Owner: internal/openplatform. Only allowlisted, identity-free JSON may be
-- written by its application; windows expire after 24 hours.
CREATE TABLE openplatform_customer_windows (
 id TEXT PRIMARY KEY CHECK (id ~ '^[0-9a-f]{32}$'),
 client_id TEXT NOT NULL,
 grant_digest TEXT NOT NULL CHECK (grant_digest ~ '^[0-9a-f]{64}$'),
 from_time TIMESTAMPTZ NOT NULL,
 to_time TIMESTAMPTZ NOT NULL,
 item_count INTEGER NOT NULL CHECK (item_count>=0),
 expires_at TIMESTAMPTZ NOT NULL,
 CHECK (from_time<to_time)
);
CREATE INDEX openplatform_customer_windows_expiry_idx ON openplatform_customer_windows(expires_at);
CREATE TABLE openplatform_customer_window_items (
 window_id TEXT NOT NULL REFERENCES openplatform_customer_windows(id) ON DELETE CASCADE,
 ordinal INTEGER NOT NULL CHECK (ordinal>=0),
 record_key TEXT NOT NULL,
 changed_at TIMESTAMPTZ NOT NULL,
 projection JSONB NOT NULL CHECK (jsonb_typeof(projection)='object'),
 PRIMARY KEY (window_id,ordinal),
 UNIQUE (window_id,record_key)
);
