-- Owner: outbound. Destination overrides retain runtime-owned signing policy.
CREATE TABLE outbound_commerce_push_endpoints (
 owner_kind text NOT NULL CHECK(owner_kind IN ('wechat_pay','service_period')),
 owner_id bigint NOT NULL CHECK(owner_id>0),
 configuration_reference text NOT NULL UNIQUE,
 template_reference text NOT NULL CHECK(length(template_reference) BETWEEN 1 AND 128),
 endpoint text NOT NULL CHECK(length(endpoint) BETWEEN 1 AND 4096),
 revision bigint NOT NULL DEFAULT 1 CHECK(revision>0),
 updated_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(owner_kind,owner_id)
);
