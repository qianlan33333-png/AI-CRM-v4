-- Owner: aiassistant (deferred review targets and import receipts).
-- Forward only; no existing target or delivery semantics are changed.
ALTER TABLE ai_assistant_plan_recipients ADD COLUMN deferred_target JSONB;
ALTER TABLE ai_assistant_plan_recipients DROP CONSTRAINT uq_ai_assistant_plan_customer;
ALTER TABLE ai_assistant_plan_recipients DROP CONSTRAINT ck_ai_assistant_customer_ref;
ALTER TABLE ai_assistant_plan_recipients DROP CONSTRAINT ck_ai_assistant_staff_ref;
ALTER TABLE ai_assistant_plan_recipients ADD CONSTRAINT ck_ai_assistant_target CHECK (
 (customer_id > 0 AND staff_id > 0 AND deferred_target IS NULL) OR
 (customer_id = 0 AND staff_id = 0 AND deferred_target IS NOT NULL AND jsonb_typeof(deferred_target)='object'
  AND deferred_target ?& ARRAY['unionid','scope','sender_userid']));
CREATE UNIQUE INDEX ai_assistant_canonical_target ON ai_assistant_plan_recipients(plan_id,customer_id) WHERE customer_id > 0;

CREATE TABLE ai_assistant_excel_imports (
 batch_key TEXT PRIMARY KEY, plan_id BIGINT NOT NULL UNIQUE REFERENCES ai_assistant_plans(id),
 file_digest TEXT NOT NULL, approved_snapshot TEXT NOT NULL DEFAULT ''
);
