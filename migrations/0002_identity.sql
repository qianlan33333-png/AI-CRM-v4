-- Owner: identity
-- Retention: customers, identities, evidence, conflicts and merge lineage are
-- durable business records. This migration is forward-only; rollback disables
-- the feature and never deletes OneID evidence.

CREATE TABLE customers (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    status TEXT NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'merged', 'closed')),
    merged_into_customer_id BIGINT REFERENCES customers(id),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    lineage_version BIGINT NOT NULL DEFAULT 1 CHECK (lineage_version > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    merged_at TIMESTAMPTZ,
    CONSTRAINT ck_customers_not_self_merged CHECK (merged_into_customer_id IS NULL OR merged_into_customer_id <> id),
    CONSTRAINT ck_customers_merged_state CHECK (
        (status = 'merged' AND merged_into_customer_id IS NOT NULL AND merged_at IS NOT NULL)
        OR (status <> 'merged' AND merged_into_customer_id IS NULL AND merged_at IS NULL)
    )
);

-- The active identity key is the external identity's complete namespace.
-- No provider's value is a customer primary key. Version is a CAS token for
-- ownership changes; merge members snapshot both sides of that transition.
CREATE TABLE customer_identities (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    customer_id BIGINT NOT NULL REFERENCES customers(id),
    kind TEXT NOT NULL,
    scope_key TEXT NOT NULL,
    normalized_value TEXT NOT NULL,
    assurance TEXT NOT NULL CHECK (assurance IN ('verified', 'declared')),
    source TEXT NOT NULL,
    source_event_id TEXT NOT NULL DEFAULT '',
    normalizer_version SMALLINT NOT NULL CHECK (normalizer_version > 0),
    status TEXT NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'conflicted', 'retired')),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    verified_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT ck_customer_identities_nonempty CHECK (
        kind <> '' AND scope_key <> '' AND normalized_value <> '' AND source <> ''
        AND length(scope_key) <= 256 AND length(normalized_value) <= 1024 AND length(source) <= 128
        AND scope_key !~ '[[:space:][:cntrl:]]'
        AND normalized_value !~ '[[:cntrl:]]'
        AND source !~ '[[:space:][:cntrl:]]'
    ),
    CONSTRAINT ck_customer_identities_kind_scope CHECK (
        (kind = 'wecom_external_userid' AND left(scope_key, 11) = 'wecom-corp:' AND length(scope_key) > 11)
        OR (kind = 'unionid' AND left(scope_key, 21) = 'wechat-open-platform:' AND length(scope_key) > 21)
        OR (kind IN ('mp_openid', 'oa_openid') AND left(scope_key, 11) = 'wechat-app:' AND length(scope_key) > 11)
        OR (
            kind IN (
                'alipay_user_id', 'alipay_oauth_user_id', 'alipay_oauth_open_id',
                'alipay_buyer_id', 'alipay_buyer_open_id'
            )
            AND left(scope_key, 11) = 'alipay-app:' AND length(scope_key) > 11
        )
        OR (kind = 'first_party_member_id' AND left(scope_key, 12) = 'first-party:' AND length(scope_key) > 12)
        OR (kind = 'phone' AND scope_key = 'phone:e164')
        OR (kind = 'ext' AND left(scope_key, 4) = 'ext:' AND length(scope_key) > 4)
    ),
    CONSTRAINT ck_verified_identity_timestamp CHECK (
        (assurance = 'verified' AND verified_at IS NOT NULL)
        OR (assurance = 'declared' AND verified_at IS NULL)
    )
);

CREATE UNIQUE INDEX ux_customer_identities_active_key
    ON customer_identities(kind, scope_key, normalized_value)
    WHERE status = 'active';

-- A root cannot silently acquire two different verified values in a namespace
-- whose provider contract is single-valued. The Store must turn this conflict
-- into evidence instead of retrying around the constraint.
CREATE UNIQUE INDEX ux_customer_identities_active_strong_namespace
    ON customer_identities(customer_id, kind, scope_key)
    WHERE status = 'active' AND assurance = 'verified' AND kind IN (
        'wecom_external_userid', 'unionid', 'mp_openid', 'oa_openid',
        'alipay_user_id', 'alipay_oauth_user_id', 'alipay_oauth_open_id',
        'alipay_buyer_id', 'alipay_buyer_open_id', 'first_party_member_id'
    );

CREATE INDEX ix_customer_identities_customer_active
    ON customer_identities(customer_id, status);

CREATE TABLE identity_link_evidence (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    left_customer_id BIGINT NOT NULL REFERENCES customers(id),
    right_customer_id BIGINT REFERENCES customers(id),
    left_identity_id BIGINT REFERENCES customer_identities(id),
    right_identity_id BIGINT REFERENCES customer_identities(id),
    evidence_type TEXT NOT NULL,
    strength TEXT NOT NULL CHECK (strength IN ('strong', 'medium', 'weak')),
    source TEXT NOT NULL,
    source_event_id TEXT NOT NULL DEFAULT '',
    evidence_digest TEXT NOT NULL,
    policy_version TEXT NOT NULL,
    operator TEXT NOT NULL DEFAULT 'system',
    metadata_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT ck_identity_link_evidence_nonempty CHECK (
        evidence_type <> '' AND source <> '' AND evidence_digest <> '' AND policy_version <> '' AND operator <> ''
    ),
    CONSTRAINT ck_identity_link_evidence_metadata_object CHECK (jsonb_typeof(metadata_json) = 'object'),
    CONSTRAINT uq_identity_link_evidence_candidate UNIQUE (id, left_customer_id, right_customer_id, strength)
);

CREATE INDEX ix_identity_link_evidence_customers
    ON identity_link_evidence(left_customer_id, right_customer_id);

-- Endpoint versions freeze the roots reviewed by the operator. Confirmation
-- locks both roots and compares these values before it can write a merge.
CREATE TABLE customer_merge_candidates (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    left_customer_id BIGINT NOT NULL REFERENCES customers(id),
    right_customer_id BIGINT NOT NULL REFERENCES customers(id),
    left_customer_version BIGINT NOT NULL CHECK (left_customer_version > 0),
    right_customer_version BIGINT NOT NULL CHECK (right_customer_version > 0),
    evidence_id BIGINT NOT NULL,
    evidence_strength TEXT NOT NULL CHECK (evidence_strength IN ('strong', 'medium', 'weak')),
    reason TEXT NOT NULL CHECK (reason <> ''),
    status TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'confirmed', 'rejected')),
    selected_survivor_customer_id BIGINT REFERENCES customers(id),
    resolved_by TEXT NOT NULL DEFAULT '',
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    resolved_at TIMESTAMPTZ,
    CONSTRAINT ck_customer_merge_candidates_distinct CHECK (left_customer_id <> right_customer_id),
    CONSTRAINT ck_customer_merge_candidates_resolution CHECK (
        (
            status = 'open' AND selected_survivor_customer_id IS NULL
            AND resolved_by = '' AND resolved_at IS NULL
        )
        OR (
            status = 'confirmed'
            AND evidence_strength = 'strong'
            AND selected_survivor_customer_id IN (left_customer_id, right_customer_id)
            AND resolved_by <> '' AND resolved_at IS NOT NULL
        )
        OR (
            status = 'rejected' AND selected_survivor_customer_id IS NULL
            AND resolved_by <> '' AND resolved_at IS NOT NULL
        )
    ),
    CONSTRAINT fk_customer_merge_candidates_evidence FOREIGN KEY (
        evidence_id, left_customer_id, right_customer_id, evidence_strength
    ) REFERENCES identity_link_evidence (id, left_customer_id, right_customer_id, strength),
    CONSTRAINT uq_customer_merge_candidates_confirmation UNIQUE (
        id, left_customer_id, right_customer_id, selected_survivor_customer_id, evidence_id
    )
);

-- LEAST/GREATEST only canonicalize pair uniqueness; they never select the
-- survivor. selected_survivor_customer_id is always an explicit confirmation.
CREATE UNIQUE INDEX ux_customer_merge_candidates_open_pair
    ON customer_merge_candidates(LEAST(left_customer_id, right_customer_id), GREATEST(left_customer_id, right_customer_id))
    WHERE status = 'open';

CREATE TABLE customer_identity_conflicts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    left_customer_id BIGINT NOT NULL REFERENCES customers(id),
    right_customer_id BIGINT NOT NULL REFERENCES customers(id),
    evidence_id BIGINT REFERENCES identity_link_evidence(id),
    reason TEXT NOT NULL CHECK (reason <> ''),
    status TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'resolved', 'ignored')),
    resolved_by TEXT NOT NULL DEFAULT '',
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    resolved_at TIMESTAMPTZ,
    CONSTRAINT ck_customer_identity_conflicts_resolution CHECK (
        (status = 'open' AND resolved_by = '' AND resolved_at IS NULL)
        OR (status <> 'open' AND resolved_by <> '' AND resolved_at IS NOT NULL)
    )
);

CREATE UNIQUE INDEX ux_customer_identity_conflicts_open_pair_reason
    ON customer_identity_conflicts(LEAST(left_customer_id, right_customer_id), GREATEST(left_customer_id, right_customer_id), reason)
    WHERE status = 'open';

-- A consumed token records a digest of the complete normalized command and
-- the original committed outcome. Same-payload replay can return that outcome;
-- payload drift fails closed without applying another link.
CREATE TABLE identity_link_intents (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    token_hash TEXT NOT NULL UNIQUE CHECK (token_hash <> ''),
    source_customer_id BIGINT NOT NULL REFERENCES customers(id),
    source_customer_version BIGINT NOT NULL CHECK (source_customer_version > 0),
    purpose TEXT NOT NULL CHECK (purpose IN ('bind_wecom', 'bind_provider_identity')),
    target_kind TEXT NOT NULL CHECK (target_kind IN (
        'wecom_external_userid', 'unionid', 'mp_openid', 'oa_openid',
        'alipay_user_id', 'alipay_oauth_user_id', 'alipay_oauth_open_id',
        'alipay_buyer_id', 'alipay_buyer_open_id', 'first_party_member_id', 'phone', 'ext'
    )),
    expected_scope_key TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'consumed', 'expired', 'cancelled')),
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    consumption_fingerprint TEXT,
    consumed_evidence_id BIGINT REFERENCES identity_link_evidence(id),
    consumed_identity_id BIGINT REFERENCES customer_identities(id),
    consumed_customer_id BIGINT REFERENCES customers(id),
    result_status TEXT CHECK (result_status IN ('attached', 'already_linked', 'merge_candidate', 'conflict')),
    result_candidate_id BIGINT REFERENCES customer_merge_candidates(id),
    result_conflict_id BIGINT REFERENCES customer_identity_conflicts(id),
    source TEXT NOT NULL CHECK (
        source <> '' AND length(source) <= 128 AND source !~ '[[:space:][:cntrl:]]'
    ),
    source_event_id TEXT NOT NULL DEFAULT '',
    metadata_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT ck_identity_link_intents_expiry CHECK (expires_at > created_at),
    CONSTRAINT ck_identity_link_intents_purpose_kind CHECK (
        purpose <> 'bind_wecom' OR target_kind = 'wecom_external_userid'
    ),
    CONSTRAINT ck_identity_link_intents_expected_scope CHECK (
        expected_scope_key = ''
        OR (target_kind = 'wecom_external_userid' AND left(expected_scope_key, 11) = 'wecom-corp:' AND length(expected_scope_key) > 11)
        OR (target_kind = 'unionid' AND left(expected_scope_key, 21) = 'wechat-open-platform:' AND length(expected_scope_key) > 21)
        OR (target_kind IN ('mp_openid', 'oa_openid') AND left(expected_scope_key, 11) = 'wechat-app:' AND length(expected_scope_key) > 11)
        OR (
            target_kind IN (
                'alipay_user_id', 'alipay_oauth_user_id', 'alipay_oauth_open_id',
                'alipay_buyer_id', 'alipay_buyer_open_id'
            )
            AND left(expected_scope_key, 11) = 'alipay-app:' AND length(expected_scope_key) > 11
        )
        OR (target_kind = 'first_party_member_id' AND left(expected_scope_key, 12) = 'first-party:' AND length(expected_scope_key) > 12)
        OR (target_kind = 'phone' AND expected_scope_key = 'phone:e164')
        OR (target_kind = 'ext' AND left(expected_scope_key, 4) = 'ext:' AND length(expected_scope_key) > 4)
    ),
    CONSTRAINT ck_identity_link_intents_metadata_object CHECK (jsonb_typeof(metadata_json) = 'object'),
    CONSTRAINT ck_identity_link_intent_consumed CHECK (
        (
            status = 'consumed'
            AND consumed_at IS NOT NULL AND consumption_fingerprint IS NOT NULL AND consumption_fingerprint <> ''
            AND consumed_evidence_id IS NOT NULL AND consumed_customer_id IS NOT NULL AND result_status IS NOT NULL
            AND (
                (result_status IN ('attached', 'already_linked') AND consumed_identity_id IS NOT NULL AND result_candidate_id IS NULL AND result_conflict_id IS NULL)
                OR (result_status = 'merge_candidate' AND consumed_identity_id IS NOT NULL AND result_candidate_id IS NOT NULL AND result_conflict_id IS NULL)
                OR (result_status = 'conflict' AND result_candidate_id IS NULL AND result_conflict_id IS NOT NULL)
            )
        )
        OR (
            status <> 'consumed'
            AND consumed_at IS NULL AND consumption_fingerprint IS NULL AND consumed_evidence_id IS NULL
            AND consumed_identity_id IS NULL AND consumed_customer_id IS NULL AND result_status IS NULL
            AND result_candidate_id IS NULL AND result_conflict_id IS NULL
        )
    )
);

CREATE INDEX ix_identity_link_intents_pending_expiry
    ON identity_link_intents(expires_at) WHERE status = 'pending';

-- A merge is a ledger entry, never a deletion. The candidate endpoint columns
-- deliberately duplicate the reviewed snapshot so the composite FK and CHECK
-- make it impossible to merge an unrelated root or an implicit survivor.
CREATE TABLE customer_merges (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    candidate_id BIGINT NOT NULL,
    candidate_left_customer_id BIGINT NOT NULL,
    candidate_right_customer_id BIGINT NOT NULL,
    from_customer_id BIGINT NOT NULL REFERENCES customers(id),
    to_customer_id BIGINT NOT NULL REFERENCES customers(id),
    evidence_id BIGINT NOT NULL REFERENCES identity_link_evidence(id),
    from_customer_version_before BIGINT NOT NULL CHECK (from_customer_version_before > 0),
    from_customer_version_after BIGINT NOT NULL,
    to_customer_version_before BIGINT NOT NULL CHECK (to_customer_version_before > 0),
    to_customer_version_after BIGINT NOT NULL,
    from_lineage_version_before BIGINT NOT NULL CHECK (from_lineage_version_before > 0),
    from_lineage_version_after BIGINT NOT NULL,
    to_lineage_version_before BIGINT NOT NULL CHECK (to_lineage_version_before > 0),
    to_lineage_version_after BIGINT NOT NULL,
    rule TEXT NOT NULL CHECK (rule <> ''),
    operator TEXT NOT NULL CHECK (operator <> ''),
    source TEXT NOT NULL CHECK (source <> ''),
    source_event_id TEXT NOT NULL DEFAULT '',
    reversible_status TEXT NOT NULL DEFAULT 'not_reversed'
        CHECK (reversible_status IN ('not_reversed', 'reversed', 'not_reversible')),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    merged_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    reversed_at TIMESTAMPTZ,
    CONSTRAINT ck_customer_merges_distinct CHECK (from_customer_id <> to_customer_id),
    CONSTRAINT ck_customer_merges_candidate_endpoints CHECK (
        (from_customer_id = candidate_left_customer_id AND to_customer_id = candidate_right_customer_id)
        OR (from_customer_id = candidate_right_customer_id AND to_customer_id = candidate_left_customer_id)
    ),
    CONSTRAINT ck_customer_merges_version_steps CHECK (
        from_customer_version_after = from_customer_version_before + 1
        AND to_customer_version_after = to_customer_version_before + 1
        AND from_lineage_version_after = from_lineage_version_before + 1
        AND to_lineage_version_after = to_lineage_version_before + 1
    ),
    CONSTRAINT ck_customer_merges_reversal_state CHECK (
        (reversible_status = 'reversed' AND reversed_at IS NOT NULL)
        OR (reversible_status <> 'reversed' AND reversed_at IS NULL)
    ),
    CONSTRAINT fk_customer_merges_confirmed_candidate FOREIGN KEY (
        candidate_id, candidate_left_customer_id, candidate_right_customer_id, to_customer_id, evidence_id
    ) REFERENCES customer_merge_candidates (
        id, left_customer_id, right_customer_id, selected_survivor_customer_id, evidence_id
    ),
    CONSTRAINT uq_customer_merges_lineage UNIQUE (id, from_customer_id, to_customer_id),
    CONSTRAINT uq_customer_merges_candidate UNIQUE (candidate_id)
);

CREATE UNIQUE INDEX ux_customer_merges_unreversed_source
    ON customer_merges(from_customer_id) WHERE reversible_status <> 'reversed';
CREATE INDEX ix_customer_merges_from_customer ON customer_merges(from_customer_id, id);
CREATE INDEX ix_customer_merges_to_customer ON customer_merges(to_customer_id, id);

-- Only these exact members may move back during reversal. A Store first locks
-- and verifies every identity still has post-merge owner/version, verifies no
-- later related merge exists, then updates all members and the ledger in one
-- transaction. Later identities on the survivor are not members and stay put.
CREATE TABLE customer_merge_identity_members (
    merge_id BIGINT NOT NULL,
    identity_id BIGINT NOT NULL REFERENCES customer_identities(id),
    from_customer_id BIGINT NOT NULL,
    to_customer_id BIGINT NOT NULL,
    identity_version_before BIGINT NOT NULL CHECK (identity_version_before > 0),
    identity_version_after BIGINT NOT NULL,
    restored_at TIMESTAMPTZ,
    identity_version_after_restore BIGINT,
    CONSTRAINT pk_customer_merge_identity_members PRIMARY KEY (merge_id, identity_id),
    CONSTRAINT fk_customer_merge_identity_members_lineage FOREIGN KEY (merge_id, from_customer_id, to_customer_id)
        REFERENCES customer_merges(id, from_customer_id, to_customer_id),
    CONSTRAINT ck_customer_merge_identity_members_version_step CHECK (
        identity_version_after = identity_version_before + 1
    ),
    CONSTRAINT ck_customer_merge_identity_members_restore CHECK (
        (restored_at IS NULL AND identity_version_after_restore IS NULL)
        OR (
            restored_at IS NOT NULL
            AND identity_version_after_restore = identity_version_after + 1
        )
    )
);
