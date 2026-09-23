-- Owner: internal/product.
-- Service-period member workspace configuration is Product-owned metadata. The
-- actual members and their remarks remain Order-owned and are accessed only
-- through Order ports. This additive migration never backfills a view or a
-- collaborator from unverified historical administration records.

CREATE TABLE product_service_period_member_views (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    product_id BIGINT NOT NULL REFERENCES products(id) ON DELETE RESTRICT,
    name TEXT NOT NULL CHECK (char_length(btrim(name)) BETWEEN 1 AND 60),
    position INTEGER NOT NULL DEFAULT 0 CHECK (position >= 0),
    is_default BOOLEAN NOT NULL DEFAULT FALSE,
    config_json JSONB NOT NULL CHECK (jsonb_typeof(config_json)='object' AND octet_length(config_json::text)<=32768),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version>0),
    created_by BIGINT NOT NULL REFERENCES admin_users(id) ON DELETE RESTRICT,
    updated_by BIGINT NOT NULL REFERENCES admin_users(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);
-- PostgreSQL permits an expression uniqueness rule only as an index, not as a
-- table constraint.  Names therefore remain case-insensitively unique.
CREATE UNIQUE INDEX product_service_period_member_views_name_ci_idx
    ON product_service_period_member_views(product_id, lower(name));
CREATE UNIQUE INDEX product_service_period_member_views_default_idx
    ON product_service_period_member_views(product_id) WHERE is_default;
CREATE INDEX product_service_period_member_views_position_idx
    ON product_service_period_member_views(product_id, position, id);

CREATE TABLE product_service_period_member_collaborators (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    product_id BIGINT NOT NULL REFERENCES products(id) ON DELETE RESTRICT,
    admin_user_id BIGINT NOT NULL REFERENCES admin_users(id) ON DELETE RESTRICT,
    permission TEXT NOT NULL CHECK (permission IN ('read','edit')),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version>0),
    created_by BIGINT NOT NULL REFERENCES admin_users(id) ON DELETE RESTRICT,
    updated_by BIGINT NOT NULL REFERENCES admin_users(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    UNIQUE(product_id, admin_user_id)
);
CREATE INDEX product_service_period_member_collaborators_user_idx
    ON product_service_period_member_collaborators(admin_user_id, product_id);

CREATE TABLE product_service_period_member_shares (
    product_id BIGINT PRIMARY KEY REFERENCES products(id) ON DELETE RESTRICT,
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    public_id TEXT NOT NULL DEFAULT '' CHECK (char_length(public_id)<=80),
    generation BIGINT NOT NULL DEFAULT 0 CHECK (generation>=0),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version>0),
    created_by BIGINT NOT NULL REFERENCES admin_users(id) ON DELETE RESTRICT,
    updated_by BIGINT NOT NULL REFERENCES admin_users(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    CHECK ((enabled AND char_length(public_id)>=24) OR (NOT enabled AND public_id=''))
);
CREATE UNIQUE INDEX product_service_period_member_shares_public_id_idx
    ON product_service_period_member_shares(public_id) WHERE public_id<>'';
