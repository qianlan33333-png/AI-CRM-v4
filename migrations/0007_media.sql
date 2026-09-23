-- Owner: internal/media.  Media is local configuration/content only; it has no
-- customer, identity, provider, or outbound-effect columns.
CREATE TABLE media_blobs (
    digest TEXT PRIMARY KEY CHECK (digest ~ '^sha256:[0-9a-f]{64}$'),
    mime_type TEXT NOT NULL CHECK (mime_type IN ('image/png','image/jpeg','image/gif','application/pdf')),
    byte_size BIGINT NOT NULL CHECK (byte_size > 0 AND byte_size <= 10485760),
    content BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT media_blobs_size_matches_content CHECK (octet_length(content) = byte_size)
);

CREATE TABLE media_images (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    blob_digest TEXT NOT NULL REFERENCES media_blobs(digest),
    file_name TEXT NOT NULL CHECK (length(file_name) BETWEEN 1 AND 255),
    name TEXT NOT NULL CHECK (length(name) <= 200),
    description TEXT NOT NULL DEFAULT '' CHECK (length(description) <= 10000),
    tags TEXT NOT NULL DEFAULT '' CHECK (length(tags) <= 10000),
    category TEXT NOT NULL DEFAULT '' CHECK (length(category) <= 200),
    mime_type TEXT NOT NULL CHECK (mime_type IN ('image/png','image/jpeg','image/gif')),
    byte_size INTEGER NOT NULL CHECK (byte_size > 0 AND byte_size <= 10485760),
    width INTEGER NOT NULL CHECK (width > 0),
    height INTEGER NOT NULL CHECK (height > 0),
    enabled BOOLEAN NOT NULL DEFAULT true,
    created_by BIGINT NOT NULL CHECK (created_by > 0),
    updated_by BIGINT NOT NULL CHECK (updated_by > 0),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX media_images_list_idx ON media_images (enabled, updated_at DESC, id DESC);

CREATE TABLE media_attachments (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    blob_digest TEXT NOT NULL REFERENCES media_blobs(digest),
    file_name TEXT NOT NULL CHECK (length(file_name) BETWEEN 1 AND 255),
    name TEXT NOT NULL CHECK (length(name) <= 200),
    description TEXT NOT NULL DEFAULT '' CHECK (length(description) <= 10000),
    tags JSONB NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(tags) = 'array'),
    mime_type TEXT NOT NULL CHECK (mime_type = 'application/pdf'),
    byte_size BIGINT NOT NULL CHECK (byte_size > 0 AND byte_size <= 10485760),
    enabled BOOLEAN NOT NULL DEFAULT true,
    created_by BIGINT NOT NULL CHECK (created_by > 0),
    updated_by BIGINT NOT NULL CHECK (updated_by > 0),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX media_attachments_list_idx ON media_attachments (enabled, updated_at DESC, id DESC);

CREATE TABLE media_miniprograms (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
    app_id TEXT NOT NULL CHECK (length(app_id) BETWEEN 1 AND 128),
    page_path TEXT NOT NULL CHECK (length(page_path) BETWEEN 1 AND 2048),
    title TEXT NOT NULL CHECK (length(title) BETWEEN 1 AND 200),
    thumb_image_id BIGINT REFERENCES media_images(id),
    enabled BOOLEAN NOT NULL DEFAULT true,
    created_by BIGINT NOT NULL CHECK (created_by > 0),
    updated_by BIGINT NOT NULL CHECK (updated_by > 0),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX media_miniprograms_list_idx ON media_miniprograms (enabled, updated_at DESC, id DESC);

CREATE TABLE media_group_invites (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
    title TEXT NOT NULL CHECK (length(title) BETWEEN 1 AND 200),
    description TEXT NOT NULL DEFAULT '' CHECK (length(description) <= 10000),
    join_url TEXT NOT NULL CHECK (length(join_url) BETWEEN 1 AND 4096),
    cover_image_id BIGINT REFERENCES media_images(id),
    enabled BOOLEAN NOT NULL DEFAULT true,
    archived_at TIMESTAMPTZ,
    created_by BIGINT NOT NULL CHECK (created_by > 0),
    updated_by BIGINT NOT NULL CHECK (updated_by > 0),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);

-- A local reference ledger lets future owners protect media without granting
-- them access to Media tables. The Media HTTP API never accepts foreign IDs.
CREATE TABLE media_references (
    material_kind TEXT NOT NULL CHECK (material_kind IN ('image','attachment','miniprogram','group_invite')),
    material_id BIGINT NOT NULL,
    owner TEXT NOT NULL CHECK (length(owner) BETWEEN 1 AND 120),
    reference_digest TEXT NOT NULL CHECK (reference_digest ~ '^sha256:[0-9a-f]{64}$'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (material_kind, material_id, owner, reference_digest)
);

CREATE TABLE media_operation_receipts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    operation TEXT NOT NULL CHECK (length(operation) BETWEEN 1 AND 80),
    actor_admin_user_id BIGINT NOT NULL CHECK (actor_admin_user_id > 0),
    resource_kind TEXT NOT NULL CHECK (resource_kind IN ('image','attachment','miniprogram','group_invite','upload')),
    resource_id BIGINT,
    idempotency_key_digest TEXT NOT NULL CHECK (idempotency_key_digest ~ '^sha256:[0-9a-f]{64}$'),
    command_digest TEXT NOT NULL CHECK (command_digest ~ '^sha256:[0-9a-f]{64}$'),
    result JSONB NOT NULL DEFAULT '{}'::jsonb,
    completed_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (operation, actor_admin_user_id, idempotency_key_digest)
);

CREATE TABLE media_audit_events (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    event_type TEXT NOT NULL CHECK (event_type ~ '^media[.][a-z0-9_.]+$'),
    resource_kind TEXT NOT NULL,
    resource_id BIGINT NOT NULL,
    actor_admin_user_id BIGINT NOT NULL CHECK (actor_admin_user_id > 0),
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE media_outbox (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    event_type TEXT NOT NULL CHECK (event_type ~ '^media[.][a-z0-9_.]+$'),
    aggregate_kind TEXT NOT NULL,
    aggregate_id BIGINT NOT NULL,
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    published_at TIMESTAMPTZ
);

CREATE TABLE media_attachment_uploads (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    actor_admin_user_id BIGINT NOT NULL CHECK (actor_admin_user_id > 0),
    idempotency_key_digest TEXT NOT NULL CHECK (idempotency_key_digest ~ '^sha256:[0-9a-f]{64}$'),
    file_name TEXT NOT NULL,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    expected_size BIGINT NOT NULL CHECK (expected_size > 0 AND expected_size <= 10485760),
    expected_digest TEXT NOT NULL CHECK (expected_digest ~ '^sha256:[0-9a-f]{64}$'),
    enabled BOOLEAN NOT NULL DEFAULT true,
    expires_at TIMESTAMPTZ NOT NULL,
    completed_attachment_id BIGINT REFERENCES media_attachments(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE(actor_admin_user_id, idempotency_key_digest)
);
CREATE TABLE media_attachment_upload_parts (
    upload_id BIGINT NOT NULL REFERENCES media_attachment_uploads(id) ON DELETE CASCADE,
    part_number INTEGER NOT NULL CHECK (part_number > 0),
    digest TEXT NOT NULL CHECK (digest ~ '^sha256:[0-9a-f]{64}$'),
    content BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY(upload_id, part_number),
    CONSTRAINT media_attachment_upload_part_size CHECK (octet_length(content) > 0 AND octet_length(content) <= 1048576)
);
