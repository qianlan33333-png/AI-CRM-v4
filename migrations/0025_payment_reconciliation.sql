-- Owner: internal/payment. Closes exact Provider reconciliation.
ALTER TABLE payment_refunds
  ADD COLUMN provider_refund_reference TEXT
  CHECK (provider_refund_reference IS NULL OR (
    length(provider_refund_reference) BETWEEN 1 AND 200 AND
    btrim(provider_refund_reference)=provider_refund_reference
  ));

CREATE UNIQUE INDEX payment_refunds_provider_reference_unique
  ON payment_refunds(provider,provider_refund_reference)
  WHERE provider_refund_reference IS NOT NULL;

CREATE UNIQUE INDEX payment_reconciliations_evidence_unique
  ON payment_reconciliations(evidence_digest);
