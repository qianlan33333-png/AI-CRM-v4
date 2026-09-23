-- Owner: internal/product.  A native paid event freezes the post-purchase
-- presentation and the Customer-owned tag-command linkage in the same Order
-- transaction.  It never stores a raw identity, external userid, or Provider
-- payload.
CREATE TABLE product_paid_purchase_actions (
    order_paid_event_id BIGINT PRIMARY KEY REFERENCES order_paid_events(id) ON DELETE RESTRICT,
    order_id BIGINT NOT NULL UNIQUE REFERENCES orders(id) ON DELETE RESTRICT,
    product_id BIGINT NOT NULL REFERENCES products(id) ON DELETE RESTRICT,
    product_version BIGINT NOT NULL CHECK (product_version > 0),
    source_digest BYTEA NOT NULL CHECK (octet_length(source_digest)=32),
    enabled BOOLEAN NOT NULL,
    action_mode TEXT NOT NULL CHECK (action_mode IN ('none','qr','redirect')),
    lead_channel_id BIGINT NULL CHECK (lead_channel_id IS NULL OR lead_channel_id > 0),
    lead_qr_title TEXT NOT NULL DEFAULT '' CHECK (char_length(lead_qr_title) <= 2048),
    lead_qr_subtitle TEXT NOT NULL DEFAULT '' CHECK (char_length(lead_qr_subtitle) <= 2048),
    redirect_url TEXT NOT NULL DEFAULT '' CHECK (char_length(redirect_url) <= 2048),
    tag_ids BIGINT[] NOT NULL DEFAULT '{}' CHECK (cardinality(tag_ids) <= 100),
    tag_state TEXT NOT NULL CHECK (tag_state IN ('disabled','not_configured','queued','partial','outcome_unknown','skipped_customer_unavailable','skipped_target_unavailable')),
    tag_command_id BIGINT NULL REFERENCES customer_tag_commands(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT product_paid_purchase_actions_mode_shape CHECK (
      (enabled=FALSE AND action_mode='none' AND lead_channel_id IS NULL AND redirect_url='')
      OR (enabled=TRUE AND action_mode='qr' AND lead_channel_id IS NOT NULL AND redirect_url='')
      OR (enabled=TRUE AND action_mode='redirect' AND lead_channel_id IS NULL AND redirect_url<>'')
    ),
    CONSTRAINT product_paid_purchase_actions_tag_shape CHECK (
      (cardinality(tag_ids)=0 AND tag_command_id IS NULL AND tag_state IN ('disabled','not_configured'))
      OR (cardinality(tag_ids)>0 AND tag_state IN ('queued','partial','outcome_unknown','skipped_customer_unavailable','skipped_target_unavailable'))
    )
);
CREATE INDEX product_paid_purchase_actions_product_idx ON product_paid_purchase_actions(product_id, created_at DESC);

-- Adding a disabled action pair is lossless: existing product definitions
-- retain every prior field and no historical purchase gains a new action.
UPDATE products
SET legacy_admin_projection = legacy_admin_projection
    || CASE WHEN legacy_admin_projection ? 'purchase_action_enabled' THEN '{}'::jsonb ELSE '{"purchase_action_enabled":false}'::jsonb END
    || CASE WHEN legacy_admin_projection ? 'purchase_action_mode' THEN '{}'::jsonb ELSE '{"purchase_action_mode":""}'::jsonb END
WHERE NOT (legacy_admin_projection ? 'purchase_action_enabled' AND legacy_admin_projection ? 'purchase_action_mode');
