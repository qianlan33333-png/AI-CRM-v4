-- Owner: referral.  This schema records a pure invitation-growth capability;
-- it has no payment, order, distribution-commission, identity, or provider
-- tables.  Canonical customer IDs are opaque references resolved by a trusted
-- session adapter outside this owner.

CREATE TABLE referral_campaigns (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name TEXT NOT NULL CHECK (name = btrim(name) AND char_length(name) BETWEEN 1 AND 200),
    cover_url TEXT NOT NULL DEFAULT '' CHECK (cover_url = btrim(cover_url) AND char_length(cover_url) <= 2000),
    description TEXT NOT NULL DEFAULT '' CHECK (description = btrim(description) AND char_length(description) <= 5000),
    reward_rules TEXT NOT NULL DEFAULT '' CHECK (reward_rules = btrim(reward_rules) AND char_length(reward_rules) <= 5000),
    state TEXT NOT NULL CHECK (state IN ('draft','scheduled','active','ended','disabled')),
    starts_at TIMESTAMPTZ NOT NULL,
    ends_at TIMESTAMPTZ NOT NULL,
    version BIGINT NOT NULL CHECK (version > 0),
    created_by BIGINT NOT NULL CHECK (created_by >= 0),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    CHECK (ends_at > starts_at),
    CHECK (updated_at >= created_at)
);
CREATE INDEX referral_campaigns_public_idx ON referral_campaigns(state, starts_at, ends_at, id);

CREATE TABLE referral_teams (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    campaign_id BIGINT NOT NULL REFERENCES referral_campaigns(id) ON DELETE RESTRICT,
    name TEXT NOT NULL CHECK (name = btrim(name) AND char_length(name) BETWEEN 1 AND 100),
    logo_url TEXT NOT NULL DEFAULT '' CHECK (logo_url = btrim(logo_url) AND char_length(logo_url) <= 2000),
    captain_customer_id BIGINT NOT NULL CHECK (captain_customer_id > 0),
    version BIGINT NOT NULL CHECK (version > 0),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    UNIQUE(campaign_id, name),
	UNIQUE(campaign_id, captain_customer_id),
    CHECK (updated_at >= created_at)
);
CREATE INDEX referral_teams_campaign_idx ON referral_teams(campaign_id, id);

-- A lock row serializes first participation for one customer/activity even
-- before a participation row exists.  It prevents two concurrent invite
-- accepts from granting duplicate activity scores.
CREATE TABLE referral_participation_locks (
    campaign_id BIGINT NOT NULL REFERENCES referral_campaigns(id) ON DELETE RESTRICT,
    customer_id BIGINT NOT NULL CHECK (customer_id > 0),
    PRIMARY KEY(campaign_id, customer_id)
);

CREATE TABLE referral_invitations (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    campaign_id BIGINT NOT NULL REFERENCES referral_campaigns(id) ON DELETE RESTRICT,
    inviter_customer_id BIGINT NOT NULL CHECK (inviter_customer_id > 0),
    token_digest BYTEA NOT NULL UNIQUE CHECK (octet_length(token_digest) = 32),
    state TEXT NOT NULL CHECK (state IN ('active','revoked','expired')),
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ NULL,
    CHECK (expires_at > created_at),
    CHECK ((state = 'revoked') = (revoked_at IS NOT NULL))
);
CREATE INDEX referral_invitations_lookup_idx ON referral_invitations(token_digest, state, expires_at);
CREATE INDEX referral_invitations_campaign_inviter_idx ON referral_invitations(campaign_id, inviter_customer_id, id DESC);

CREATE TABLE referral_participations (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    campaign_id BIGINT NOT NULL REFERENCES referral_campaigns(id) ON DELETE RESTRICT,
    customer_id BIGINT NOT NULL CHECK (customer_id > 0),
    team_id BIGINT NOT NULL REFERENCES referral_teams(id) ON DELETE RESTRICT,
    invitation_id BIGINT NULL REFERENCES referral_invitations(id) ON DELETE RESTRICT,
    inviter_customer_id BIGINT NULL CHECK (inviter_customer_id > 0),
    inviter_team_id BIGINT NULL REFERENCES referral_teams(id) ON DELETE RESTRICT,
    state TEXT NOT NULL CHECK (state IN ('active','reversed')),
    joined_at TIMESTAMPTZ NOT NULL,
    UNIQUE(campaign_id, customer_id),
    CHECK ((invitation_id IS NULL AND inviter_customer_id IS NULL AND inviter_team_id IS NULL) OR
           (invitation_id IS NOT NULL AND inviter_customer_id IS NOT NULL AND inviter_team_id IS NOT NULL))
);
CREATE INDEX referral_participations_campaign_team_idx ON referral_participations(campaign_id, team_id, joined_at, id);
CREATE INDEX referral_participations_campaign_inviter_idx ON referral_participations(campaign_id, inviter_customer_id, joined_at, id) WHERE inviter_customer_id IS NOT NULL;

-- A stable lock row handles a customer with no previous relation.  The current
-- row retains relationship ID and increments a version for every accepted
-- cross-campaign invitation; history is append-only evidence.
CREATE TABLE referral_relationship_locks (
    customer_id BIGINT PRIMARY KEY CHECK (customer_id > 0)
);

CREATE TABLE referral_current_relationships (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    customer_id BIGINT NOT NULL UNIQUE CHECK (customer_id > 0),
    referrer_customer_id BIGINT NOT NULL CHECK (referrer_customer_id > 0 AND referrer_customer_id <> customer_id),
    source_campaign_id BIGINT NOT NULL REFERENCES referral_campaigns(id) ON DELETE RESTRICT,
    invitation_id BIGINT NOT NULL REFERENCES referral_invitations(id) ON DELETE RESTRICT,
    version BIGINT NOT NULL CHECK (version > 0),
    effective_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX referral_current_relationships_referrer_idx ON referral_current_relationships(referrer_customer_id, effective_at DESC, id DESC);

CREATE TABLE referral_relationship_history (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    relationship_id BIGINT NOT NULL REFERENCES referral_current_relationships(id) ON DELETE RESTRICT,
    version BIGINT NOT NULL CHECK (version > 0),
    customer_id BIGINT NOT NULL CHECK (customer_id > 0),
    previous_referrer_customer_id BIGINT NULL CHECK (previous_referrer_customer_id > 0),
    referrer_customer_id BIGINT NOT NULL CHECK (referrer_customer_id > 0 AND referrer_customer_id <> customer_id),
    source_campaign_id BIGINT NOT NULL REFERENCES referral_campaigns(id) ON DELETE RESTRICT,
    invitation_id BIGINT NOT NULL REFERENCES referral_invitations(id) ON DELETE RESTRICT,
    accepted_at TIMESTAMPTZ NOT NULL,
    UNIQUE(relationship_id, version)
);
CREATE INDEX referral_relationship_history_customer_idx ON referral_relationship_history(customer_id, accepted_at DESC, id DESC);

-- Score is event-sourced.  Credit and reversal records are immutable, so a
-- historical board can be reproduced from server-confirmed participation facts.
CREATE TABLE referral_score_events (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    campaign_id BIGINT NOT NULL REFERENCES referral_campaigns(id) ON DELETE RESTRICT,
    participation_id BIGINT NOT NULL REFERENCES referral_participations(id) ON DELETE RESTRICT,
    inviter_customer_id BIGINT NOT NULL CHECK (inviter_customer_id > 0),
    team_id BIGINT NOT NULL REFERENCES referral_teams(id) ON DELETE RESTRICT,
    kind TEXT NOT NULL CHECK (kind IN ('credit','reversal')),
    delta INTEGER NOT NULL CHECK (delta IN (-1,1)),
    reverses_score_event_id BIGINT NULL REFERENCES referral_score_events(id) ON DELETE RESTRICT,
    reason TEXT NOT NULL DEFAULT '' CHECK (reason = btrim(reason) AND char_length(reason) <= 500),
    occurred_at TIMESTAMPTZ NOT NULL,
    CHECK ((kind = 'credit' AND delta = 1 AND reverses_score_event_id IS NULL AND reason = '') OR
           (kind = 'reversal' AND delta = -1 AND reverses_score_event_id IS NOT NULL AND reason <> ''))
);
CREATE UNIQUE INDEX referral_score_credit_one_per_participation ON referral_score_events(participation_id) WHERE kind = 'credit';
CREATE UNIQUE INDEX referral_score_reversal_one_per_credit ON referral_score_events(reverses_score_event_id) WHERE kind = 'reversal';
CREATE INDEX referral_score_personal_rank_idx ON referral_score_events(campaign_id, inviter_customer_id, occurred_at, id);
CREATE INDEX referral_score_team_rank_idx ON referral_score_events(campaign_id, team_id, occurred_at, id);

CREATE TABLE referral_reward_records (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    campaign_id BIGINT NOT NULL REFERENCES referral_campaigns(id) ON DELETE RESTRICT,
    customer_id BIGINT NOT NULL CHECK (customer_id > 0),
    score_event_id BIGINT NULL REFERENCES referral_score_events(id) ON DELETE RESTRICT,
    period TEXT NOT NULL CHECK (period = btrim(period) AND char_length(period) BETWEEN 1 AND 32),
    reward TEXT NOT NULL CHECK (reward = btrim(reward) AND char_length(reward) BETWEEN 1 AND 500),
    evidence_reference TEXT NOT NULL DEFAULT '' CHECK (evidence_reference = btrim(evidence_reference) AND char_length(evidence_reference) <= 500),
    state TEXT NOT NULL CHECK (state IN ('recorded','needs_review')),
    recorded_by BIGINT NOT NULL CHECK (recorded_by > 0),
    recorded_at TIMESTAMPTZ NOT NULL,
    UNIQUE(campaign_id, customer_id, period, reward)
);
CREATE INDEX referral_reward_records_campaign_idx ON referral_reward_records(campaign_id, recorded_at DESC, id DESC);

CREATE TABLE referral_reward_reviews (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    reward_id BIGINT NOT NULL REFERENCES referral_reward_records(id) ON DELETE RESTRICT,
    reason TEXT NOT NULL CHECK (reason = btrim(reason) AND char_length(reason) BETWEEN 1 AND 500),
    score_event_id BIGINT NOT NULL REFERENCES referral_score_events(id) ON DELETE RESTRICT,
    occurred_at TIMESTAMPTZ NOT NULL,
    UNIQUE(reward_id, score_event_id)
);

CREATE TABLE referral_campaign_snapshots (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    campaign_id BIGINT NOT NULL REFERENCES referral_campaigns(id) ON DELETE RESTRICT,
    snapshot_kind TEXT NOT NULL CHECK (snapshot_kind IN ('close')),
    captured_at TIMESTAMPTZ NOT NULL,
    score_watermark BIGINT NOT NULL CHECK (score_watermark >= 0),
    rankings JSONB NOT NULL CHECK (jsonb_typeof(rankings) = 'object'),
    UNIQUE(campaign_id, snapshot_kind)
);

-- Receipts belong to Referral because a public invite accept must atomically
-- cover current relation, immutable activity score, audit and platform outbox.
CREATE TABLE referral_operation_receipts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    operation TEXT NOT NULL CHECK (operation IN ('join','issue_invitation','campaign_create','campaign_update','campaign_state','team_create','invite_reverse','invite_revoke','reward_record','campaign_close')),
    actor_scope TEXT NOT NULL CHECK (actor_scope = btrim(actor_scope) AND char_length(actor_scope) BETWEEN 1 AND 200),
    key_digest BYTEA NOT NULL CHECK (octet_length(key_digest) = 32),
    payload_digest BYTEA NOT NULL CHECK (octet_length(payload_digest) = 32),
    result_kind TEXT NOT NULL CHECK (result_kind IN ('campaign','team','participation','invitation','reward','snapshot')),
    result_id BIGINT NOT NULL CHECK (result_id > 0),
    created_at TIMESTAMPTZ NOT NULL,
    UNIQUE(operation, actor_scope, key_digest)
);

CREATE OR REPLACE FUNCTION referral_append_only_reject_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'referral immutable evidence cannot be changed';
END;
$$;
CREATE TRIGGER referral_relationship_history_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON referral_relationship_history FOR EACH STATEMENT EXECUTE FUNCTION referral_append_only_reject_mutation();
CREATE TRIGGER referral_score_events_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON referral_score_events FOR EACH STATEMENT EXECUTE FUNCTION referral_append_only_reject_mutation();
CREATE TRIGGER referral_reward_reviews_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON referral_reward_reviews FOR EACH STATEMENT EXECUTE FUNCTION referral_append_only_reject_mutation();
CREATE TRIGGER referral_operation_receipts_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON referral_operation_receipts FOR EACH STATEMENT EXECUTE FUNCTION referral_append_only_reject_mutation();
