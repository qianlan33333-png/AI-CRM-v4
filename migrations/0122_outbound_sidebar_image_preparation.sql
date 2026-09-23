-- Owner: internal/outbound. Temporary image upload is distinct from sending.
CREATE TABLE outbound_sidebar_image_preparations (
 id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 image_id BIGINT NOT NULL CHECK(image_id>0),
 scope_digest TEXT NOT NULL,
 source_digest BYTEA NOT NULL CHECK(octet_length(source_digest)=32),
 content BYTEA NOT NULL CHECK(octet_length(content)>5 AND octet_length(content)<=2097152),
 file_name TEXT NOT NULL,
 media_type TEXT NOT NULL CHECK(media_type IN ('image/png','image/jpeg')),
 effect_id TEXT UNIQUE,
 state TEXT NOT NULL CHECK(state IN ('accepted','queued','executed','retryable_failed','outcome_unknown','final_failed')),
 media_id TEXT,
 ready_until TIMESTAMPTZ,
 created_at TIMESTAMPTZ NOT NULL,
 updated_at TIMESTAMPTZ NOT NULL,
 CHECK ((state='executed' AND media_id IS NOT NULL AND ready_until IS NOT NULL) OR state<>'executed')
);
CREATE INDEX outbound_sidebar_image_resource ON outbound_sidebar_image_preparations(scope_digest,image_id,id DESC);
CREATE TABLE outbound_sidebar_image_events (
 id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 preparation_id BIGINT NOT NULL REFERENCES outbound_sidebar_image_preparations(id),
 operation TEXT NOT NULL,
 evidence_digest TEXT NOT NULL,
 occurred_at TIMESTAMPTZ NOT NULL,
 UNIQUE(preparation_id,operation,evidence_digest)
);
CREATE TABLE outbound_sidebar_image_outbox (
 id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 preparation_id BIGINT NOT NULL REFERENCES outbound_sidebar_image_preparations(id),
 event_type TEXT NOT NULL,
 payload JSONB NOT NULL,
 evidence_digest TEXT NOT NULL,
 occurred_at TIMESTAMPTZ NOT NULL,
 UNIQUE(preparation_id,event_type,evidence_digest)
);
