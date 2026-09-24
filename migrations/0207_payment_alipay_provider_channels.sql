-- Owner: internal/payment. Complete the Alipay WAP/Page/refund database
-- allowlists that were omitted when Alipay intents were introduced in 0202.
-- Forward-only: preserve all existing payment and refund rows.
ALTER TABLE payments
  DROP CONSTRAINT payments_provider_check,
  DROP CONSTRAINT payments_payment_channel_check;
ALTER TABLE payments
  ADD CONSTRAINT payments_provider_check CHECK (provider IN ('wechat_pay','wechat_shop','alipay')),
  ADD CONSTRAINT payments_payment_channel_check CHECK (payment_channel IN ('mini_program','h5_official_account','alipay_wap','alipay_page')),
  ADD CONSTRAINT payments_provider_channel_shape CHECK (
    (provider='alipay' AND payment_channel IN ('alipay_wap','alipay_page')) OR
    (provider IN ('wechat_pay','wechat_shop') AND payment_channel IN ('mini_program','h5_official_account'))
  );

ALTER TABLE payment_refunds
  DROP CONSTRAINT payment_refunds_provider_check;
ALTER TABLE payment_refunds
  ADD CONSTRAINT payment_refunds_provider_check CHECK (provider IN ('wechat_pay','wechat_shop','alipay'));
