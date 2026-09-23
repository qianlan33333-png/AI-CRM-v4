-- Owner: internal/distribution. This migration records first-level
-- distribution business facts only. Product, Order, Payment and Identity own
-- their referenced rows and are reached exclusively through stable ports.

CREATE TABLE distribution_distributors (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    customer_id BIGINT NOT NULL UNIQUE CHECK (customer_id > 0),
    public_no TEXT NOT NULL UNIQUE CHECK (public_no = btrim(public_no) AND char_length(public_no) BETWEEN 6 AND 64),
    agreement_version TEXT NOT NULL CHECK (agreement_version = btrim(agreement_version) AND char_length(agreement_version) BETWEEN 1 AND 100),
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    receiver_reference TEXT NOT NULL DEFAULT '' CHECK (receiver_reference = btrim(receiver_reference) AND char_length(receiver_reference) <= 200),
    receiver_app_id TEXT NOT NULL DEFAULT '' CHECK (receiver_app_id = btrim(receiver_app_id) AND char_length(receiver_app_id) <= 200),
    receiver_ready BOOLEAN NOT NULL DEFAULT FALSE,
    receiver_reason TEXT NOT NULL DEFAULT '' CHECK (receiver_reason = btrim(receiver_reason) AND char_length(receiver_reason) <= 200),
    receiver_checked_at TIMESTAMPTZ NULL,
    registered_at TIMESTAMPTZ NOT NULL,
    version BIGINT NOT NULL CHECK (version > 0),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    CHECK (updated_at >= created_at),
    CHECK ((receiver_ready AND receiver_reference <> '' AND receiver_app_id <> '' AND receiver_reason = '' AND receiver_checked_at IS NOT NULL) OR (NOT receiver_ready))
);

-- Distribution browser sessions are a bounded access-session projection of a
-- verified WeChat/Payment session. They are not an identity matcher, never
-- permit CRM staff access, and are intentionally distinct from one-time
-- checkout session cookies.
CREATE TABLE distribution_browser_sessions (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    token_digest BYTEA NOT NULL UNIQUE CHECK (octet_length(token_digest) = 32),
    customer_id BIGINT NOT NULL CHECK (customer_id > 0),
    identity_id BIGINT NOT NULL CHECK (identity_id > 0),
    channel TEXT NOT NULL CHECK (channel IN ('mini_program','h5_official_account')),
    app_id TEXT NOT NULL CHECK (app_id = btrim(app_id) AND char_length(app_id) BETWEEN 1 AND 200),
    app_scope TEXT NOT NULL CHECK (app_scope = btrim(app_scope) AND char_length(app_scope) BETWEEN 1 AND 240),
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL,
    CHECK (expires_at > created_at)
);
CREATE INDEX distribution_browser_sessions_lookup_idx ON distribution_browser_sessions(token_digest, expires_at) WHERE revoked_at IS NULL;

CREATE TABLE distribution_agreements (
    version TEXT PRIMARY KEY CHECK (version = btrim(version) AND char_length(version) BETWEEN 1 AND 100),
    content TEXT NOT NULL CHECK (content = btrim(content) AND char_length(content) BETWEEN 1 AND 20000),
    active BOOLEAN NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
);
CREATE UNIQUE INDEX distribution_agreements_one_active ON distribution_agreements(active) WHERE active;
INSERT INTO distribution_agreements(version,content,active,created_at) VALUES
('v1','一级分销协议：仅本人有效购买过同一商品并完成收款准备后可推广；佣金以订单支付、退款复核与微信接收方确认结果为准。',TRUE,clock_timestamp());

CREATE TABLE distribution_product_policies (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    product_id BIGINT NOT NULL CHECK (product_id > 0),
    product_type TEXT NOT NULL CHECK (product_type IN ('standard_product','service_period')),
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    commission_rate_basis_points INTEGER NOT NULL DEFAULT 0 CHECK (commission_rate_basis_points BETWEEN 0 AND 3000),
    wait_days INTEGER NOT NULL DEFAULT 7 CHECK (wait_days BETWEEN 0 AND 29),
    version BIGINT NOT NULL CHECK (version > 0),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    UNIQUE(product_id, product_type),
    CHECK (updated_at >= created_at)
);

CREATE TABLE distribution_policy_versions (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    policy_id BIGINT NOT NULL REFERENCES distribution_product_policies(id) ON DELETE RESTRICT,
    version BIGINT NOT NULL CHECK (version > 0),
    enabled BOOLEAN NOT NULL,
    commission_rate_basis_points INTEGER NOT NULL CHECK (commission_rate_basis_points BETWEEN 0 AND 3000),
    wait_days INTEGER NOT NULL CHECK (wait_days BETWEEN 0 AND 29),
    actor_scope TEXT NOT NULL CHECK (actor_scope = btrim(actor_scope) AND char_length(actor_scope) BETWEEN 1 AND 200),
    occurred_at TIMESTAMPTZ NOT NULL,
    UNIQUE(policy_id, version)
);

CREATE TABLE distribution_promotion_credentials (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    distributor_id BIGINT NOT NULL REFERENCES distribution_distributors(id) ON DELETE RESTRICT,
    product_id BIGINT NOT NULL CHECK (product_id > 0),
    product_type TEXT NOT NULL CHECK (product_type IN ('standard_product','service_period')),
    token_digest BYTEA NOT NULL UNIQUE CHECK (octet_length(token_digest) = 32),
    status TEXT NOT NULL CHECK (status IN ('active','revoked','expired')),
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ NULL,
    CHECK (expires_at > created_at),
    CHECK ((status = 'revoked') = (revoked_at IS NOT NULL))
);
CREATE INDEX distribution_promotion_credentials_lookup_idx ON distribution_promotion_credentials(token_digest, status, expires_at);

-- An attribution freezes the policy and the successful qualification evidence
-- inside the native Order checkout transaction. References to Order facts are
-- immutable identifiers, not cross-domain foreign keys.
CREATE TABLE distribution_order_attributions (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    order_id BIGINT NOT NULL CHECK (order_id > 0),
    order_item_line INTEGER NOT NULL CHECK (order_item_line > 0),
    product_code TEXT NOT NULL CHECK (product_code = btrim(product_code) AND char_length(product_code) BETWEEN 1 AND 200),
    product_name TEXT NOT NULL CHECK (product_name = btrim(product_name) AND char_length(product_name) BETWEEN 1 AND 500),
    distributor_id BIGINT NOT NULL REFERENCES distribution_distributors(id) ON DELETE RESTRICT,
    promotion_credential_id BIGINT NOT NULL REFERENCES distribution_promotion_credentials(id) ON DELETE RESTRICT,
    qualification_evidence_reference TEXT NOT NULL CHECK (qualification_evidence_reference = btrim(qualification_evidence_reference) AND char_length(qualification_evidence_reference) BETWEEN 1 AND 200),
    qualification_state TEXT NOT NULL CHECK (qualification_state = 'eligible'),
    policy_id BIGINT NOT NULL REFERENCES distribution_product_policies(id) ON DELETE RESTRICT,
    policy_version BIGINT NOT NULL CHECK (policy_version > 0),
    commission_rate_basis_points INTEGER NOT NULL CHECK (commission_rate_basis_points BETWEEN 0 AND 3000),
    wait_days INTEGER NOT NULL CHECK (wait_days BETWEEN 0 AND 29),
    attributed_at TIMESTAMPTZ NOT NULL,
    UNIQUE(order_id, order_item_line),
    UNIQUE(order_id, order_item_line, distributor_id)
);
CREATE INDEX distribution_order_attributions_distributor_idx ON distribution_order_attributions(distributor_id, attributed_at DESC, id DESC);

CREATE TABLE distribution_commissions (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    attribution_id BIGINT NOT NULL UNIQUE REFERENCES distribution_order_attributions(id) ON DELETE RESTRICT,
    order_id BIGINT NOT NULL CHECK (order_id > 0),
    order_item_line INTEGER NOT NULL CHECK (order_item_line > 0),
    distributor_id BIGINT NOT NULL REFERENCES distribution_distributors(id) ON DELETE RESTRICT,
    original_item_paid_minor BIGINT NOT NULL CHECK (original_item_paid_minor >= 0),
    successful_refund_minor BIGINT NOT NULL DEFAULT 0 CHECK (successful_refund_minor BETWEEN 0 AND original_item_paid_minor),
    initial_minor BIGINT NOT NULL CHECK (initial_minor >= 0),
    current_payable_minor BIGINT NOT NULL CHECK (current_payable_minor >= 0),
    paid_minor BIGINT NOT NULL DEFAULT 0 CHECK (paid_minor >= 0),
    commission_rate_basis_points INTEGER NOT NULL CHECK (commission_rate_basis_points BETWEEN 0 AND 3000),
    paid_confirmed_at TIMESTAMPTZ NOT NULL,
    due_at TIMESTAMPTZ NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('pending','held','settling','paid','cancelled','exception','zero_commission')),
    hold_reason TEXT NOT NULL DEFAULT '' CHECK (hold_reason = btrim(hold_reason) AND char_length(hold_reason) <= 200),
    cancel_reason TEXT NOT NULL DEFAULT '' CHECK (cancel_reason = btrim(cancel_reason) AND char_length(cancel_reason) <= 200),
    exception_reason TEXT NOT NULL DEFAULT '' CHECK (exception_reason = btrim(exception_reason) AND char_length(exception_reason) <= 200),
    version BIGINT NOT NULL CHECK (version > 0),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    UNIQUE(order_id, order_item_line, distributor_id),
    CHECK (updated_at >= created_at),
    CHECK ((status = 'zero_commission') = (initial_minor = 0 AND current_payable_minor = 0 AND paid_minor = 0)),
    CHECK (paid_minor = 0 OR status IN ('paid','exception'))
);
CREATE INDEX distribution_commissions_due_idx ON distribution_commissions(status, due_at, id);
CREATE INDEX distribution_commissions_distributor_idx ON distribution_commissions(distributor_id, created_at DESC, id DESC);

CREATE TABLE distribution_commission_adjustments (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    commission_id BIGINT NOT NULL REFERENCES distribution_commissions(id) ON DELETE RESTRICT,
    kind TEXT NOT NULL CHECK (kind IN ('buyer_refund','qualification_hold','qualification_revoke','qualification_restore','manual_recovery','merchant_liability')),
    delta_minor BIGINT NOT NULL,
    resulting_payable_minor BIGINT NOT NULL CHECK (resulting_payable_minor >= 0),
    reason TEXT NOT NULL CHECK (reason = btrim(reason) AND char_length(reason) BETWEEN 1 AND 200),
    source_reference TEXT NOT NULL DEFAULT '' CHECK (source_reference = btrim(source_reference) AND char_length(source_reference) <= 200),
    occurred_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX distribution_commission_adjustments_commission_idx ON distribution_commission_adjustments(commission_id, id);

CREATE TABLE distribution_settlements (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    commission_id BIGINT NOT NULL REFERENCES distribution_commissions(id) ON DELETE RESTRICT,
    settlement_reference TEXT NOT NULL UNIQUE CHECK (settlement_reference = btrim(settlement_reference) AND char_length(settlement_reference) BETWEEN 1 AND 200),
    amount_minor BIGINT NOT NULL CHECK (amount_minor > 0),
    currency TEXT NOT NULL CHECK (currency = 'CNY'),
    original_payment_reference TEXT NOT NULL CHECK (original_payment_reference = btrim(original_payment_reference) AND char_length(original_payment_reference) BETWEEN 1 AND 200),
    payment_instruction_reference TEXT NOT NULL DEFAULT '' CHECK (payment_instruction_reference = btrim(payment_instruction_reference) AND char_length(payment_instruction_reference) <= 200),
    payment_effect_reference TEXT NOT NULL DEFAULT '' CHECK (payment_effect_reference = btrim(payment_effect_reference) AND char_length(payment_effect_reference) <= 200),
    state TEXT NOT NULL CHECK (state IN ('planned','accepted','attempted','outcome_unknown','receiver_succeeded','cancelled','exception')),
    provider_deadline_at TIMESTAMPTZ NULL,
    version BIGINT NOT NULL CHECK (version > 0),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    CHECK (updated_at >= created_at)
);
CREATE INDEX distribution_settlements_commission_idx ON distribution_settlements(commission_id, created_at DESC, id DESC);
CREATE UNIQUE INDEX distribution_settlements_instruction_unique ON distribution_settlements(payment_instruction_reference) WHERE payment_instruction_reference <> '';

CREATE TABLE distribution_exceptions (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    commission_id BIGINT NOT NULL REFERENCES distribution_commissions(id) ON DELETE RESTRICT,
    settlement_id BIGINT NULL REFERENCES distribution_settlements(id) ON DELETE RESTRICT,
    kind TEXT NOT NULL CHECK (kind IN ('settlement_unknown','settlement_deadline','settlement_deadline_imminent','receiver_unavailable','qualification_revoked_after_paid','buyer_refund_after_paid','unfreeze_final_failed','merchant_liability','recovery')),
    status TEXT NOT NULL CHECK (status IN ('open','querying','resolved','merchant_liability_recorded','recovery_recorded')),
    unpaid_due_minor BIGINT NOT NULL CHECK (unpaid_due_minor >= 0),
    already_paid_minor BIGINT NOT NULL CHECK (already_paid_minor >= 0),
    amount_minor BIGINT NOT NULL DEFAULT 0 CHECK (amount_minor >= 0),
    reason TEXT NOT NULL CHECK (reason = btrim(reason) AND char_length(reason) BETWEEN 1 AND 500),
    evidence_reference TEXT NOT NULL DEFAULT '' CHECK (evidence_reference = btrim(evidence_reference) AND char_length(evidence_reference) <= 500),
    actor_scope TEXT NOT NULL CHECK (actor_scope = btrim(actor_scope) AND char_length(actor_scope) BETWEEN 1 AND 200),
    version BIGINT NOT NULL CHECK (version > 0),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    CHECK (updated_at >= created_at)
);
CREATE INDEX distribution_exceptions_open_idx ON distribution_exceptions(status, updated_at DESC, id DESC);

CREATE TABLE distribution_operation_receipts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    operation TEXT NOT NULL CHECK (operation IN ('register','policy','credential','attribution','paid_event','settlement','exception_reconcile','recovery','merchant_liability')),
    actor_scope TEXT NOT NULL CHECK (actor_scope = btrim(actor_scope) AND char_length(actor_scope) BETWEEN 1 AND 200),
    key_digest BYTEA NOT NULL CHECK (octet_length(key_digest) = 32),
    payload_digest BYTEA NOT NULL CHECK (octet_length(payload_digest) = 32),
    result_kind TEXT NOT NULL CHECK (result_kind IN ('distributor','policy','credential','attribution','commission','settlement','exception')),
    result_id BIGINT NOT NULL CHECK (result_id > 0),
    created_at TIMESTAMPTZ NOT NULL,
    UNIQUE(operation, actor_scope, key_digest)
);

CREATE TABLE distribution_audit_events (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    event_type TEXT NOT NULL CHECK (event_type ~ '^distribution[.][a-z_]+([.]v[0-9]+)?$'),
    aggregate_type TEXT NOT NULL CHECK (aggregate_type IN ('distributor','policy','credential','attribution','commission','settlement','exception')),
    aggregate_id BIGINT NOT NULL CHECK (aggregate_id > 0),
    actor_scope TEXT NOT NULL CHECK (actor_scope = btrim(actor_scope) AND char_length(actor_scope) BETWEEN 1 AND 200),
    payload JSONB NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
    occurred_at TIMESTAMPTZ NOT NULL
);
CREATE TABLE distribution_outbox (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    event_type TEXT NOT NULL CHECK (event_type ~ '^distribution[.][a-z_]+([.]v[0-9]+)?$'),
    idempotency_key TEXT NOT NULL UNIQUE CHECK (idempotency_key = btrim(idempotency_key) AND char_length(idempotency_key) BETWEEN 1 AND 200),
    aggregate_id BIGINT NOT NULL CHECK (aggregate_id > 0),
    payload JSONB NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
    occurred_at TIMESTAMPTZ NOT NULL,
    published_at TIMESTAMPTZ NULL
);

CREATE OR REPLACE FUNCTION distribution_append_only_reject_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'distribution immutable evidence cannot be changed';
END;
$$;
CREATE TRIGGER distribution_policy_versions_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON distribution_policy_versions FOR EACH STATEMENT EXECUTE FUNCTION distribution_append_only_reject_mutation();
CREATE TRIGGER distribution_commission_adjustments_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON distribution_commission_adjustments FOR EACH STATEMENT EXECUTE FUNCTION distribution_append_only_reject_mutation();
CREATE TRIGGER distribution_operation_receipts_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON distribution_operation_receipts FOR EACH STATEMENT EXECUTE FUNCTION distribution_append_only_reject_mutation();
CREATE TRIGGER distribution_audit_events_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON distribution_audit_events FOR EACH STATEMENT EXECUTE FUNCTION distribution_append_only_reject_mutation();
CREATE TRIGGER distribution_outbox_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON distribution_outbox FOR EACH STATEMENT EXECUTE FUNCTION distribution_append_only_reject_mutation();

-- The near-deadline record is informational. Once the underlying commission
-- reaches an actual terminal paid/cancelled fact it must no longer remain in
-- the actionable exception queue or be mistaken for merchant liability.
CREATE OR REPLACE FUNCTION distribution_resolve_deadline_warning() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.status IN ('paid','cancelled') AND OLD.status IS DISTINCT FROM NEW.status THEN
        UPDATE distribution_exceptions
        SET status='resolved', version=version+1, updated_at=NEW.updated_at
        WHERE commission_id=NEW.id
          AND kind='settlement_deadline_imminent'
          AND status='open';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER distribution_commission_terminal_resolves_deadline_warning
AFTER UPDATE OF status ON distribution_commissions
FOR EACH ROW EXECUTE FUNCTION distribution_resolve_deadline_warning();
