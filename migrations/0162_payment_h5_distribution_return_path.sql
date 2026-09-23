-- Owner: Payment. Forward-only; preserve existing OAuth states while adding
-- the Distribution application return path already validated by Payment H5 OAuth.
-- Product context is a fixed public reference only: no identities, commission
-- terms, receiver data, alternate keys or non-canonical query ordering may enter.
ALTER TABLE payment_h5_oauth_states
  DROP CONSTRAINT payment_h5_oauth_states_return_path_check;
ALTER TABLE payment_h5_oauth_states
  ADD CONSTRAINT payment_h5_oauth_states_return_path_check CHECK (
    return_path ~ '^/(p/[^/?#]+([?]promotion_context=dpc_[A-Za-z0-9_-]{43})?|pay/[^/?#]+([?]promotion_context=dpc_[A-Za-z0-9_-]{43})?|s/[^/?#]+(/pay)?([?]promotion_context=dpc_[A-Za-z0-9_-]{43})?|c/[a-z][a-z0-9-]{5,119}|distribution([?]product_id=[1-9][0-9]*&product_type=(standard_product|service_period))?)$'
    AND return_path !~ '%(2[fF]|5[cC]|3[fF]|23)'
    AND position(E'\\' in return_path)=0
    -- Promotion returns are new in this migration. Their route and opaque
    -- capability must use one literal canonical form; legacy query-free
    -- commerce returns keep their existing compatibility contract.
    AND CASE WHEN return_path ~ '[?]promotion_context=' THEN return_path !~ '%' ELSE TRUE END
    -- The application path's positive integer must remain representable by
    -- Payment's int64 parser. CASE keeps the numeric cast unreachable for
    -- non-Distribution legacy returns.
    AND CASE WHEN return_path ~ '^/distribution[?]' THEN
      (regexp_match(return_path, '^/distribution[?]product_id=([1-9][0-9]*)&product_type=(standard_product|service_period)$'))[1]::numeric <= 9223372036854775807
    ELSE TRUE END
  );
