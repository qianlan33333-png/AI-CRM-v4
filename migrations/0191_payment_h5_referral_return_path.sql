-- Owner: Payment. Forward-only repair for the already-composed Referral H5
-- OAuth return contract. The returned route is a same-origin, literal
-- canonical path only; it carries no browser-supplied identity or next URL.
ALTER TABLE payment_h5_oauth_states
  DROP CONSTRAINT payment_h5_oauth_states_return_path_check;
ALTER TABLE payment_h5_oauth_states
  ADD CONSTRAINT payment_h5_oauth_states_return_path_check CHECK (
    return_path ~ '^/(p/[^/?#]+([?]promotion_context=dpc_[A-Za-z0-9_-]{43})?|pay/[^/?#]+([?]promotion_context=dpc_[A-Za-z0-9_-]{43})?|s/[^/?#]+(/pay)?([?]promotion_context=dpc_[A-Za-z0-9_-]{43})?|c/[a-z][a-z0-9-]{5,119}|distribution([?]product_id=[1-9][0-9]*&product_type=(standard_product|service_period))?|referral([?]campaign=[1-9][0-9]*(&invite=rfi_[A-Za-z0-9_-]{43})?)?)$'
    AND return_path !~ '%(2[fF]|5[cC]|3[fF]|23)'
    AND position(E'\\' in return_path)=0
    AND CASE WHEN return_path ~ '[?]promotion_context=' THEN return_path !~ '%' ELSE TRUE END
    -- Both application return routes are parsed as int64 by Payment before
    -- redirecting. Keep the database boundary identical to that parser.
    AND CASE
      WHEN return_path ~ '^/distribution[?]' THEN
        (regexp_match(return_path, '^/distribution[?]product_id=([1-9][0-9]*)&product_type=(standard_product|service_period)$'))[1]::numeric <= 9223372036854775807
      WHEN return_path ~ '^/referral[?]' THEN
        (regexp_match(return_path, '^/referral[?]campaign=([1-9][0-9]*)(?:&invite=rfi_[A-Za-z0-9_-]{43})?$'))[1]::numeric <= 9223372036854775807
      ELSE TRUE
    END
  );
