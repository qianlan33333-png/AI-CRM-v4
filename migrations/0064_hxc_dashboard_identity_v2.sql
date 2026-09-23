-- Owner: hxcdashboard.
-- Extends the immutable projection with safe OneID resolution metadata.

ALTER TABLE hxc_dashboard_versions
    ADD COLUMN matched_by_unionid_count BIGINT NOT NULL DEFAULT 0 CHECK (matched_by_unionid_count >= 0),
    ADD COLUMN matched_by_phone_count BIGINT NOT NULL DEFAULT 0 CHECK (matched_by_phone_count >= 0),
    ADD COLUMN matched_by_both_count BIGINT NOT NULL DEFAULT 0 CHECK (matched_by_both_count >= 0),
    ADD COLUMN pending_observation_count BIGINT NOT NULL DEFAULT 0 CHECK (pending_observation_count >= 0),
    ADD COLUMN invalid_identity_count BIGINT NOT NULL DEFAULT 0 CHECK (invalid_identity_count >= 0);

-- The v1 resolver only matched scoped UnionID. Preserve that meaning while
-- upgrading already-published projections.
UPDATE hxc_dashboard_versions
SET matched_by_unionid_count = matched_count,
    pending_observation_count = unmatched_count;

ALTER TABLE hxc_dashboard_versions ADD CONSTRAINT ck_hxc_dashboard_v2_matched
    CHECK (matched_count = matched_by_unionid_count + matched_by_phone_count + matched_by_both_count);
ALTER TABLE hxc_dashboard_versions ADD CONSTRAINT ck_hxc_dashboard_v2_unmatched
    CHECK (unmatched_count = pending_observation_count + invalid_identity_count);

ALTER TABLE hxc_dashboard_rows
    ADD COLUMN matched_by TEXT NOT NULL DEFAULT 'none'
        CHECK (matched_by IN ('none','unionid','phone','both')),
    ADD COLUMN identity_reason_code TEXT NOT NULL DEFAULT 'legacy_unmatched'
        CHECK (identity_reason_code <> '' AND length(identity_reason_code) <= 80),
    ADD COLUMN identity_case_id BIGINT,
    ADD COLUMN merge_candidate_id BIGINT;

UPDATE hxc_dashboard_rows
SET matched_by = CASE WHEN identity_state = 'matched' THEN 'unionid' ELSE 'none' END,
    identity_reason_code = CASE identity_state
        WHEN 'matched' THEN 'matched_unionid'
        WHEN 'conflict' THEN 'legacy_source_conflict'
        ELSE 'no_match'
    END;

ALTER TABLE hxc_dashboard_rows ADD CONSTRAINT ck_hxc_dashboard_v2_row_match
    CHECK ((identity_state = 'matched' AND customer_id IS NOT NULL AND matched_by <> 'none')
        OR (identity_state <> 'matched' AND customer_id IS NULL AND matched_by = 'none'));

ALTER TABLE hxc_dashboard_refresh_runs
    ADD COLUMN identity_mode TEXT NOT NULL DEFAULT 'inspect'
        CHECK (identity_mode IN ('inspect','apply')),
    ADD COLUMN identity_replay_verified_count BIGINT NOT NULL DEFAULT 0
        CHECK (identity_replay_verified_count >= 0 AND identity_replay_verified_count <= processed_count);

CREATE INDEX hxc_dashboard_rows_match_source_idx
    ON hxc_dashboard_rows(projection_id, matched_by, subject_digest);
CREATE INDEX hxc_dashboard_rows_reason_idx
    ON hxc_dashboard_rows(projection_id, identity_reason_code, subject_digest);
