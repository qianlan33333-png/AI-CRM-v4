-- Owner: Automation. Forward-only: relax prompt size checks without rewriting
-- existing frozen generation inputs. Keep the nonempty generation contract.
ALTER TABLE automation_generation_items
  DROP CONSTRAINT automation_generation_items_role_prompt_check,
  DROP CONSTRAINT automation_generation_items_task_prompt_check,
  ADD CONSTRAINT automation_generation_items_role_prompt_check CHECK (length(role_prompt) >= 1),
  ADD CONSTRAINT automation_generation_items_task_prompt_check CHECK (length(task_prompt) >= 1);
