-- Owner: internal/groupops. UI metadata and Provider aggregate counts only.
ALTER TABLE group_ops_plans ADD COLUMN plan_type TEXT NOT NULL DEFAULT 'standard' CHECK (plan_type IN ('standard','webhook'));
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='group_ops_plans' AND column_name='legacy_import_definition') THEN
    UPDATE group_ops_plans SET plan_type='webhook' WHERE legacy_import_definition->>'plan_type'='webhook';
  END IF;
END $$;
ALTER TABLE group_ops_directory_groups ADD COLUMN external_member_count INTEGER CHECK (external_member_count >= 0 AND external_member_count <= member_count);
ALTER TABLE group_ops_plan_nodes ADD COLUMN day_index INTEGER NOT NULL DEFAULT 1 CHECK (day_index BETWEEN 1 AND 3650);
ALTER TABLE group_ops_plan_nodes ADD COLUMN scheduled_time TEXT NOT NULL DEFAULT '20:00' CHECK (scheduled_time ~ '^(0[89]|1[0-9]|2[0-3]):(00|30)$');
ALTER TABLE group_ops_plan_nodes ADD COLUMN trigger_time_label TEXT NOT NULL DEFAULT '20:00' CHECK (trigger_time_label = scheduled_time);
ALTER TABLE group_ops_plan_nodes ADD COLUMN action_title TEXT NOT NULL DEFAULT '' CHECK (btrim(action_title) = action_title AND char_length(action_title) <= 200);
ALTER TABLE group_ops_plan_nodes ADD COLUMN node_status TEXT NOT NULL DEFAULT 'active' CHECK (node_status IN ('active','draft','disabled'));

-- Existing nodes predate calendar scheduling. Keep that provenance explicit: a
-- future Day 1 20:00 action must never be reclassified as an old delay node.
ALTER TABLE group_ops_plan_nodes ADD COLUMN schedule_semantics TEXT NOT NULL DEFAULT 'calendar' CHECK (schedule_semantics IN ('calendar','relative_delay'));
UPDATE group_ops_plan_nodes SET schedule_semantics='relative_delay';
