-- Frozen historical refunds must not become executable live refund intents.
ALTER TABLE payment_refunds DROP CONSTRAINT payment_refunds_status_check;
ALTER TABLE payment_refunds ADD CONSTRAINT payment_refunds_status_check CHECK (
 status IN ('requested','effect_accepted','outcome_unknown','completed','final_failed',
 'history_requested','history_processing','history_failed','history_closed')
);
ALTER TABLE payment_refunds ADD CONSTRAINT payment_refunds_historical_no_effect CHECK (
 status NOT IN ('history_requested','history_processing','history_failed','history_closed') OR external_effect_id IS NULL
);
