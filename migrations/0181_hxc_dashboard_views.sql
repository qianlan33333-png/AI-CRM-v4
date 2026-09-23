-- HXC owns view configuration; no customer or source identity is copied.
CREATE TABLE hxc_dashboard_views (
 id bigserial PRIMARY KEY,
 owner_kind text NOT NULL CHECK(owner_kind IN ('admin','staff')),
 owner_id bigint NOT NULL,
 name text NOT NULL CHECK(length(name) BETWEEN 1 AND 60),
 config jsonb NOT NULL,
 version bigint NOT NULL DEFAULT 1,
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX hxc_dashboard_views_owner ON hxc_dashboard_views(owner_kind,owner_id,id);
CREATE TABLE hxc_dashboard_view_receipts (
 actor_id bigint NOT NULL,
 key_digest bytea NOT NULL,
 payload_digest bytea NOT NULL,
 result jsonb NOT NULL,
 PRIMARY KEY(actor_id,key_digest)
);

-- Owner: internal/product. Immutable scope; revocation only after issue.
CREATE TABLE product_dashboard_shares (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 resource_id bigint NOT NULL,
 config jsonb NOT NULL,
 fields jsonb NOT NULL,
 mode text NOT NULL CHECK(mode IN ('metrics','details')),
 enabled boolean NOT NULL DEFAULT true,
 version bigint NOT NULL DEFAULT 1,
 token_digest bytea NOT NULL UNIQUE CHECK(octet_length(token_digest)=32)
);

-- Owner: internal/hxcdashboard. Immutable scope; revocation only after issue.
CREATE TABLE hxc_dashboard_shares (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 resource_id bigint NOT NULL,
 config jsonb NOT NULL,
 fields jsonb NOT NULL,
 mode text NOT NULL CHECK(mode IN ('metrics','details')),
 enabled boolean NOT NULL DEFAULT true,
 version bigint NOT NULL DEFAULT 1,
 token_digest bytea NOT NULL UNIQUE CHECK(octet_length(token_digest)=32)
);

-- Private per-owner signing key survives restarts and supports idempotent issue.
CREATE TABLE product_dashboard_share_key (
 singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),
 key_bytes bytea NOT NULL CHECK(octet_length(key_bytes)=32)
);
INSERT INTO product_dashboard_share_key(key_bytes)
VALUES(decode(replace(gen_random_uuid()::text,'-','') || replace(gen_random_uuid()::text,'-',''),'hex'));

-- Private per-owner signing key survives restarts and supports idempotent issue.
CREATE TABLE hxc_dashboard_share_key (
 singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),
 key_bytes bytea NOT NULL CHECK(octet_length(key_bytes)=32)
);
INSERT INTO hxc_dashboard_share_key(key_bytes)
VALUES(decode(replace(gen_random_uuid()::text,'-','') || replace(gen_random_uuid()::text,'-',''),'hex'));
