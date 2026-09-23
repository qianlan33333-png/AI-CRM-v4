-- Owner: referral.  Adds activity configuration without crossing into
-- Product, Order, Payment, or Distribution persistence.  Migration 0193 is
-- reserved by Payment on this release line.

ALTER TABLE referral_campaigns
    ADD COLUMN team_mode TEXT NOT NULL DEFAULT 'team',
    ADD COLUMN qualification_mode TEXT NOT NULL DEFAULT 'free_signup',
    ADD COLUMN product_id BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN product_type TEXT NOT NULL DEFAULT '',
    ADD COLUMN leaderboard_metric TEXT NOT NULL DEFAULT 'invites';

ALTER TABLE referral_campaigns
    ADD CONSTRAINT referral_campaigns_team_mode_check CHECK (team_mode IN ('team','individual')),
    ADD CONSTRAINT referral_campaigns_qualification_mode_check CHECK (qualification_mode IN ('free_signup','product_purchase')),
    ADD CONSTRAINT referral_campaigns_leaderboard_metric_check CHECK (leaderboard_metric IN ('invites','sales','sales_amount','sales_orders')),
    ADD CONSTRAINT referral_campaigns_product_target_check CHECK (
        (qualification_mode = 'free_signup' AND product_id = 0 AND product_type = '') OR
        (qualification_mode = 'product_purchase' AND product_id > 0 AND product_type IN ('standard_product','service_period'))
    );

-- A product may not have two simultaneously accepting purchase-qualified
-- activities. Historical and draft activities remain auditable and may keep
-- the same opaque target.
CREATE UNIQUE INDEX referral_campaigns_one_live_product_target_idx
    ON referral_campaigns(product_type, product_id)
    WHERE qualification_mode = 'product_purchase' AND state IN ('scheduled','active');

-- Individual activities intentionally keep a NULL team grouping. Existing
-- team rows and historical participations remain untouched for audit reads.
ALTER TABLE referral_participations ALTER COLUMN team_id DROP NOT NULL;
ALTER TABLE referral_score_events ALTER COLUMN team_id DROP NOT NULL;

-- Opaque product-activity contexts are issued by Referral and resolved only
-- by digest at checkout. The raw token never enters an Order or Payment row.
CREATE TABLE referral_product_activity_contexts (
    context_digest BYTEA PRIMARY KEY CHECK (octet_length(context_digest)=32),
    campaign_id BIGINT NOT NULL REFERENCES referral_campaigns(id) ON DELETE RESTRICT,
    product_id BIGINT NOT NULL CHECK (product_id > 0),
    product_type TEXT NOT NULL CHECK (product_type IN ('standard_product','service_period')),
    sales_metric TEXT NOT NULL CHECK (sales_metric IN ('amount','orders')),
    state TEXT NOT NULL CHECK (state IN ('active','revoked','expired')),
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX referral_product_activity_contexts_lookup_idx ON referral_product_activity_contexts(campaign_id, product_id, product_type, state, expires_at);

CREATE TABLE referral_product_sale_checkouts (
    order_id BIGINT PRIMARY KEY CHECK (order_id > 0),
    order_version BIGINT NOT NULL CHECK (order_version > 0),
    campaign_id BIGINT NOT NULL REFERENCES referral_campaigns(id) ON DELETE RESTRICT,
    product_id BIGINT NOT NULL CHECK (product_id > 0),
    product_type TEXT NOT NULL CHECK (product_type IN ('standard_product','service_period')),
    product_code TEXT NOT NULL CHECK (product_code = btrim(product_code) AND char_length(product_code) BETWEEN 1 AND 200),
    product_name TEXT NOT NULL CHECK (product_name = btrim(product_name) AND char_length(product_name) BETWEEN 1 AND 500),
    product_version BIGINT NOT NULL CHECK (product_version > 0),
    promoter_customer_id BIGINT NULL CHECK (promoter_customer_id > 0),
    promotion_credential_ref TEXT NULL CHECK (promotion_credential_ref IS NULL OR char_length(promotion_credential_ref) BETWEEN 1 AND 200),
    policy_version BIGINT NULL CHECK (policy_version > 0),
    commission_rate_basis_points INTEGER NOT NULL DEFAULT 0 CHECK (commission_rate_basis_points BETWEEN 0 AND 3000),
    wait_days INTEGER NOT NULL DEFAULT 0 CHECK (wait_days BETWEEN 0 AND 29),
    buyer_customer_id BIGINT NOT NULL CHECK (buyer_customer_id > 0),
    beneficiary_customer_id BIGINT NOT NULL CHECK (beneficiary_customer_id > 0),
    activity_context_digest BYTEA NOT NULL CHECK (octet_length(activity_context_digest)=32),
    promotion_context_digest BYTEA NOT NULL CHECK (octet_length(promotion_context_digest)=32),
    created_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE referral_product_sale_events (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    campaign_id BIGINT NOT NULL REFERENCES referral_campaigns(id) ON DELETE RESTRICT,
    order_id BIGINT NOT NULL CHECK (order_id > 0),
    order_version BIGINT NOT NULL CHECK (order_version > 0),
    product_id BIGINT NOT NULL CHECK (product_id > 0),
    product_type TEXT NOT NULL CHECK (product_type IN ('standard_product','service_period')),
    product_code TEXT NOT NULL,
    product_name TEXT NOT NULL,
    product_version BIGINT NOT NULL CHECK (product_version > 0),
    participation_id BIGINT NULL REFERENCES referral_participations(id) ON DELETE RESTRICT,
    team_id BIGINT NULL REFERENCES referral_teams(id) ON DELETE RESTRICT,
    promoter_customer_id BIGINT NULL CHECK (promoter_customer_id > 0),
    promotion_credential_ref TEXT NULL,
    policy_version BIGINT NULL CHECK (policy_version > 0),
    commission_rate_basis_points INTEGER NOT NULL CHECK (commission_rate_basis_points BETWEEN 0 AND 3000),
    wait_days INTEGER NOT NULL CHECK (wait_days BETWEEN 0 AND 29),
    buyer_customer_id BIGINT NOT NULL CHECK (buyer_customer_id > 0),
    beneficiary_customer_id BIGINT NOT NULL CHECK (beneficiary_customer_id > 0),
    kind TEXT NOT NULL CHECK (kind IN ('credit','reversal')),
    amount_delta_minor BIGINT NOT NULL CHECK (amount_delta_minor <> 0),
    order_count_delta INTEGER NOT NULL CHECK (order_count_delta IN (-1,0,1)),
    source_paid_event_id BIGINT NOT NULL CHECK (source_paid_event_id > 0),
    reverses_sale_event_id BIGINT NULL REFERENCES referral_product_sale_events(id) ON DELETE RESTRICT,
    refund_receipt_key TEXT NOT NULL DEFAULT '',
    source_digest BYTEA NOT NULL CHECK (octet_length(source_digest)=32),
    occurred_at TIMESTAMPTZ NOT NULL,
    UNIQUE(reverses_sale_event_id, refund_receipt_key)
);
CREATE UNIQUE INDEX referral_product_sale_credit_unique_idx ON referral_product_sale_events(order_id, product_id) WHERE kind='credit';
CREATE INDEX referral_product_sale_events_campaign_idx ON referral_product_sale_events(campaign_id, occurred_at, id);
CREATE INDEX referral_product_sale_events_promoter_idx ON referral_product_sale_events(campaign_id, promoter_customer_id, occurred_at, id);

-- Sales rankings consume only immutable checkout attribution snapshots owned by
-- Referral. Order IDs and promoter IDs are opaque references; no foreign key
-- reaches another domain. Refund processing updates the cumulative projection
-- under a CAS while retaining the original paid amount and paid timestamp.
CREATE TABLE referral_sales_facts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    campaign_id BIGINT NOT NULL REFERENCES referral_campaigns(id) ON DELETE RESTRICT,
    order_id BIGINT NOT NULL CHECK (order_id > 0),
    order_item_line INTEGER NOT NULL CHECK (order_item_line > 0),
    product_id BIGINT NOT NULL CHECK (product_id > 0),
    product_type TEXT NOT NULL CHECK (product_type IN ('standard_product','service_period')),
    promoter_customer_id BIGINT NOT NULL CHECK (promoter_customer_id > 0),
    team_id BIGINT NULL REFERENCES referral_teams(id) ON DELETE RESTRICT,
    original_paid_minor BIGINT NOT NULL CHECK (original_paid_minor > 0),
    successful_refund_minor BIGINT NOT NULL DEFAULT 0 CHECK (successful_refund_minor >= 0 AND successful_refund_minor <= original_paid_minor),
    source_reference TEXT NOT NULL CHECK (source_reference = btrim(source_reference) AND char_length(source_reference) BETWEEN 1 AND 200),
    paid_at TIMESTAMPTZ NOT NULL,
    version BIGINT NOT NULL CHECK (version > 0),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    CHECK (updated_at >= created_at),
    UNIQUE(campaign_id, order_id, order_item_line)
);
CREATE INDEX referral_sales_facts_campaign_paid_idx ON referral_sales_facts(campaign_id, paid_at, id);
CREATE INDEX referral_sales_facts_promoter_idx ON referral_sales_facts(campaign_id, promoter_customer_id, paid_at, id);
