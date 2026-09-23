-- Owner: internal/media. Content package definitions and their immutable
-- material snapshots are local Media facts. This migration deliberately owns
-- no customer, Group Ops execution, Provider, or outbound-effect table.
CREATE TABLE media_content_packages (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
    content_text TEXT NOT NULL DEFAULT '' CHECK (length(content_text) <= 10000),
    enabled BOOLEAN NOT NULL DEFAULT true,
    version BIGINT NOT NULL CHECK (version > 0),
    created_by BIGINT NOT NULL CHECK (created_by > 0),
    updated_by BIGINT NOT NULL CHECK (updated_by > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);

CREATE TABLE media_content_package_versions (
    package_id BIGINT NOT NULL REFERENCES media_content_packages(id) ON DELETE RESTRICT,
    version BIGINT NOT NULL CHECK (version > 0),
    name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
    content_text TEXT NOT NULL CHECK (length(content_text) <= 10000),
    enabled BOOLEAN NOT NULL,
    created_by BIGINT NOT NULL CHECK (created_by > 0),
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (package_id, version)
);

CREATE TABLE media_content_package_version_refs (
    package_id BIGINT NOT NULL,
    version BIGINT NOT NULL,
    position INTEGER NOT NULL CHECK (position >= 0),
    material_kind TEXT NOT NULL CHECK (material_kind IN ('image','attachment','miniprogram','group_invite')),
    material_id BIGINT NOT NULL CHECK (material_id > 0),
    source_digest TEXT NOT NULL CHECK (source_digest ~ '^sha256:[0-9a-f]{64}$'),
    PRIMARY KEY (package_id, version, position),
    UNIQUE (package_id, version, material_kind, material_id),
    FOREIGN KEY (package_id, version) REFERENCES media_content_package_versions(package_id, version) ON DELETE RESTRICT
);

CREATE TABLE media_content_delivery_receipts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    operation TEXT NOT NULL CHECK (operation IN ('create','update','bind','upload_initiate','upload_part','upload_complete')),
    actor_admin_user_id BIGINT NOT NULL CHECK (actor_admin_user_id > 0),
    key_digest BYTEA NOT NULL CHECK (octet_length(key_digest) = 32),
    payload_digest BYTEA NOT NULL CHECK (octet_length(payload_digest) = 32),
    result_snapshot JSONB,
    created_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    UNIQUE (operation, actor_admin_user_id, key_digest),
    CONSTRAINT media_content_delivery_receipt_completion CHECK ((completed_at IS NULL) = (result_snapshot IS NULL))
);

CREATE TABLE media_content_delivery_bindings (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    campaign_code TEXT NOT NULL CHECK (length(campaign_code) BETWEEN 1 AND 200),
    plan_id TEXT NOT NULL CHECK (length(plan_id) BETWEEN 1 AND 200),
    package_id BIGINT NOT NULL REFERENCES media_content_packages(id) ON DELETE RESTRICT,
    group_invite_id BIGINT NOT NULL REFERENCES media_group_invites(id) ON DELETE RESTRICT,
    version BIGINT NOT NULL CHECK (version > 0),
    created_by BIGINT NOT NULL CHECK (created_by > 0),
    updated_by BIGINT NOT NULL CHECK (updated_by > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (campaign_code, plan_id)
);

-- Provider preparation is a Media-owned receipt/lease fact.  It is written
-- only after an approved preparation adapter has received a Provider proof;
-- Group Ops never writes this table and workers never resolve local media IDs
-- against it.  Invite links intentionally have no row here because their
-- captured title/url/description are already provider-ready local facts.
CREATE TABLE media_group_ops_preparation_receipts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    actor_admin_user_id BIGINT NOT NULL CHECK (actor_admin_user_id > 0),
    idempotency_key_digest BYTEA NOT NULL CHECK (octet_length(idempotency_key_digest) = 32),
    command_digest BYTEA NOT NULL CHECK (octet_length(command_digest) = 32),
    item_count INTEGER NOT NULL CHECK (item_count >= 0 AND item_count <= 9),
    required_through TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (actor_admin_user_id, idempotency_key_digest)
);

CREATE TABLE media_group_ops_preparation_items (
    preparation_receipt_id BIGINT NOT NULL REFERENCES media_group_ops_preparation_receipts(id) ON DELETE CASCADE,
    position INTEGER NOT NULL CHECK (position >= 0 AND position <= 8),
    material_kind TEXT NOT NULL CHECK (material_kind IN ('image','attachment','miniprogram')),
    material_id BIGINT NOT NULL CHECK (material_id > 0),
    source_digest TEXT NOT NULL CHECK (source_digest ~ '^sha256:[0-9a-f]{64}$'),
    receipt_digest TEXT NOT NULL CHECK (receipt_digest ~ '^sha256:[0-9a-f]{64}$'),
    ready_until TIMESTAMPTZ NOT NULL,
    attachment JSONB NOT NULL CHECK (jsonb_typeof(attachment) = 'object'),
    PRIMARY KEY (preparation_receipt_id, position)
);

CREATE INDEX media_group_ops_preparation_lookup_idx
    ON media_group_ops_preparation_items(material_kind, material_id, source_digest, ready_until DESC, preparation_receipt_id DESC);

CREATE INDEX media_content_package_versions_list_idx ON media_content_package_versions(package_id, version DESC);
CREATE INDEX media_content_delivery_bindings_package_idx ON media_content_delivery_bindings(package_id);
