-- Owner: AI Assistant.
-- Retention: forward-only. Existing human rows and receipt bytes are retained
-- unchanged. A machine actor is an opaque authenticated client reference,
-- never a numeric admin.

ALTER TABLE ai_assistant_plans
    ALTER COLUMN created_by DROP NOT NULL,
    ADD COLUMN created_actor_kind TEXT,
    ADD COLUMN created_actor_ref TEXT,
    ADD CONSTRAINT ck_ai_assistant_plan_machine_actor CHECK (
        (created_by IS NOT NULL AND created_by > 0 AND created_actor_kind IS NULL AND created_actor_ref IS NULL) OR
        (created_by IS NULL AND created_actor_kind IS NOT DISTINCT FROM 'machine' AND created_actor_ref IS NOT NULL AND created_actor_ref ~ '^machine:[A-Za-z0-9._-]{1,160}$')
    );
CREATE INDEX ai_assistant_plans_creator_ref_idx ON ai_assistant_plans(created_actor_ref, id);

ALTER TABLE ai_assistant_content_versions
    ALTER COLUMN created_by DROP NOT NULL,
    ADD COLUMN created_actor_kind TEXT,
    ADD COLUMN created_actor_ref TEXT,
    ADD CONSTRAINT ck_ai_assistant_content_machine_actor CHECK (
        (created_by IS NOT NULL AND created_by > 0 AND created_actor_kind IS NULL AND created_actor_ref IS NULL) OR
        (created_by IS NULL AND created_actor_kind IS NOT DISTINCT FROM 'machine' AND created_actor_ref IS NOT NULL AND created_actor_ref ~ '^machine:[A-Za-z0-9._-]{1,160}$')
    );

ALTER TABLE ai_assistant_audit_events
    ALTER COLUMN actor_id DROP NOT NULL,
    ADD COLUMN actor_kind TEXT,
    ADD COLUMN actor_ref TEXT,
    ADD CONSTRAINT ck_ai_assistant_audit_machine_actor CHECK (
        (actor_id IS NOT NULL AND actor_id > 0 AND actor_kind IS NULL AND actor_ref IS NULL) OR
        (actor_id IS NULL AND actor_kind IS NOT DISTINCT FROM 'machine' AND actor_ref IS NOT NULL AND actor_ref ~ '^machine:[A-Za-z0-9._-]{1,160}$')
    );
CREATE INDEX ai_assistant_audit_actor_ref_idx ON ai_assistant_audit_events(actor_ref, id);
