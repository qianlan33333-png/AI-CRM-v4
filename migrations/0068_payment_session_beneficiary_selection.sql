-- Migration 0068. Owner: internal/payment.
-- Preserve existing session recipient values as legacy facts. New OAuth and
-- Mini Program sessions have no recipient until the trusted payer explicitly
-- confirms the public "for myself" checkout action.
ALTER TABLE payment_sessions
  DROP CONSTRAINT payment_sessions_beneficiary_customer_id_check,
  ALTER COLUMN beneficiary_customer_id DROP NOT NULL,
  ADD COLUMN beneficiary_selection TEXT NOT NULL DEFAULT 'legacy_prebound',
  ADD COLUMN beneficiary_selected_at TIMESTAMPTZ;

ALTER TABLE payment_sessions
  ADD CONSTRAINT payment_sessions_beneficiary_selection_check
    CHECK (beneficiary_selection IN ('legacy_prebound', 'unresolved', 'payer_self', 'admin_assisted')),
  ADD CONSTRAINT payment_sessions_beneficiary_selection_fact_check
    CHECK (
      (beneficiary_selection = 'legacy_prebound' AND beneficiary_customer_id IS NOT NULL AND beneficiary_selected_at IS NULL)
      OR (beneficiary_selection = 'unresolved' AND beneficiary_customer_id IS NULL AND beneficiary_selected_at IS NULL)
      OR (beneficiary_selection = 'payer_self' AND beneficiary_customer_id IS NOT NULL AND beneficiary_customer_id = payer_customer_id AND beneficiary_selected_at IS NOT NULL)
      OR (beneficiary_selection = 'admin_assisted' AND beneficiary_customer_id IS NOT NULL AND beneficiary_selected_at IS NOT NULL)
    );

-- Product public URLs are product-code paths. OAuth state remains restricted to
-- one same-origin, encoded path segment and cannot become an open redirect.
ALTER TABLE payment_h5_oauth_states
  DROP CONSTRAINT payment_h5_oauth_states_return_path_check;
ALTER TABLE payment_h5_oauth_states
  ADD CONSTRAINT payment_h5_oauth_states_return_path_check
  CHECK (
    return_path ~ '^/(pay/[^/?#]+|s/[^/?#]+(/pay)?|c/[a-z][a-z0-9-]{5,119})$'
    AND return_path !~ '%(2[fF]|5[cC]|3[fF]|23)'
    AND position(E'\\' in return_path)=0
  );
