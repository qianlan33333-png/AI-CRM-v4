-- Owner: internal/product.
--
-- A post-purchase action is frozen at native checkout. The URL Link source is
-- retained only in this Product-owned paid-action record and is resolved by a
-- server-side Product adapter after Payment authorizes the exact paid order.
-- Existing action rows retain checkout_snapshot=false and therefore preserve
-- their current guidance fallback.

ALTER TABLE product_paid_purchase_actions
    ADD COLUMN completion_target JSONB NULL
        CHECK (completion_target IS NULL OR (jsonb_typeof(completion_target) = 'object' AND octet_length(completion_target::text) <= 8192)),
    ADD COLUMN checkout_snapshot BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE product_paid_purchase_actions
    DROP CONSTRAINT product_paid_purchase_actions_mode_shape;

ALTER TABLE product_paid_purchase_actions
    ADD CONSTRAINT product_paid_purchase_actions_mode_shape CHECK (
      (enabled=FALSE AND action_mode='none' AND lead_channel_id IS NULL AND redirect_url='' AND completion_target IS NULL)
      OR (enabled=TRUE AND action_mode='qr' AND lead_channel_id IS NOT NULL AND redirect_url='' AND completion_target IS NULL)
      OR (enabled=TRUE AND action_mode='redirect' AND lead_channel_id IS NULL
          AND ((completion_target IS NULL AND redirect_url<>'') OR completion_target IS NOT NULL))
    );
