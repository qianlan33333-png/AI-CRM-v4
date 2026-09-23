-- Owner: Segment/Audience. Inert source history only; never a runnable SQL DSL.
ALTER TABLE segment_audience_packages ADD COLUMN historical_import BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE segment_audience_packages ADD CONSTRAINT segment_history_never_active CHECK(NOT historical_import OR lifecycle <> 'active');
-- Immutable envelope retains every source group/package/version/member field,
-- including original SQL and original schedules, under authenticated encryption.
CREATE TABLE segment_audience_history_batches (
 id BIGSERIAL PRIMARY KEY,
 source TEXT NOT NULL,
 digest BYTEA NOT NULL CHECK(octet_length(digest)=32),
 captured_at TIMESTAMPTZ NOT NULL,
 encrypted_evidence BYTEA NOT NULL CHECK(octet_length(encrypted_evidence)>32),
 imported_at TIMESTAMPTZ NOT NULL,
 UNIQUE(source,digest)
);
CREATE TABLE segment_audience_history_rows (
 batch_id BIGINT NOT NULL REFERENCES segment_audience_history_batches(id),
 kind TEXT NOT NULL CHECK(kind IN ('group','package','version','member')),
 source_id BIGINT NOT NULL CHECK(source_id>0),
 digest BYTEA NOT NULL CHECK(octet_length(digest)=32),
 PRIMARY KEY(batch_id,kind,source_id)
);
CREATE TABLE segment_audience_history_groups (
 source TEXT NOT NULL, source_id BIGINT NOT NULL,
 target_id BIGINT NOT NULL UNIQUE REFERENCES segment_audience_groups(id),
 target_version BIGINT NOT NULL DEFAULT 1,
 PRIMARY KEY(source,source_id)
);
CREATE TABLE segment_audience_history_packages (
 source TEXT NOT NULL, source_id BIGINT NOT NULL,
 target_id BIGINT NOT NULL UNIQUE REFERENCES segment_audience_packages(id),
 target_version BIGINT NOT NULL DEFAULT 1,
 PRIMARY KEY(source,source_id)
);
CREATE TABLE segment_audience_history_members (
 batch_id BIGINT NOT NULL REFERENCES segment_audience_history_batches(id),
 source_id BIGINT NOT NULL,
 package_id BIGINT NOT NULL REFERENCES segment_audience_packages(id),
 customer_id BIGINT CHECK(customer_id>0),
 disposition TEXT NOT NULL CHECK(disposition IN ('resolved','exited','unresolved','conflict','missing_scope','invalid')),
 PRIMARY KEY(batch_id,source_id),
 CHECK((disposition='resolved')=(customer_id IS NOT NULL))
);
CREATE TRIGGER segment_history_batches_immutable BEFORE UPDATE OR DELETE OR TRUNCATE ON segment_audience_history_batches FOR EACH STATEMENT EXECUTE FUNCTION segment_audience_append_only();
CREATE TRIGGER segment_history_rows_immutable BEFORE UPDATE OR DELETE OR TRUNCATE ON segment_audience_history_rows FOR EACH STATEMENT EXECUTE FUNCTION segment_audience_append_only();
CREATE TRIGGER segment_history_members_immutable BEFORE UPDATE OR DELETE OR TRUNCATE ON segment_audience_history_members FOR EACH STATEMENT EXECUTE FUNCTION segment_audience_append_only();
CREATE TABLE segment_audience_history_snapshots (
 batch_id BIGINT NOT NULL REFERENCES segment_audience_history_batches(id),
 package_id BIGINT NOT NULL REFERENCES segment_audience_packages(id),
 snapshot_id BIGINT NOT NULL REFERENCES segment_audience_snapshots(id),
 PRIMARY KEY(batch_id,package_id),
 UNIQUE(snapshot_id)
);
CREATE TRIGGER segment_history_snapshots_immutable BEFORE UPDATE OR DELETE OR TRUNCATE ON segment_audience_history_snapshots FOR EACH STATEMENT EXECUTE FUNCTION segment_audience_append_only();
