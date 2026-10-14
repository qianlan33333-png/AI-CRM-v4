-- New Webhook batches create one Provider task for all frozen groups.
-- Historical single-group task rows keep their original identifiers and status.
ALTER TABLE group_ops_group_message_tasks
 ADD COLUMN target_chat_references JSONB CHECK (
   target_chat_references IS NULL OR
   (jsonb_typeof(target_chat_references)='array' AND jsonb_array_length(target_chat_references) BETWEEN 1 AND 2000)
 ),
 ADD COLUMN delivery_observations JSONB NOT NULL DEFAULT '{}'::jsonb
   CHECK (jsonb_typeof(delivery_observations)='object');
