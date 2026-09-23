-- Persist a safe provider failure category for admin readback.
ALTER TABLE segment_core_recommendations
  ADD COLUMN failure_code TEXT NOT NULL DEFAULT ''
  CHECK (failure_code = '' OR failure_code ~ '^[a-z0-9_]{1,80}$');
