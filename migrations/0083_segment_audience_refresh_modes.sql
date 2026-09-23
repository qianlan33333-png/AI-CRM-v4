-- Owner: Segment/Audience. Preserve the old single-cron contract while
-- recording the four donor refresh modes as durable, independently claimed
-- schedule kinds. No scheduler or queue is introduced here.
ALTER TABLE segment_audience_configuration_versions
  ADD COLUMN refresh_mode TEXT NOT NULL DEFAULT 'legacy_custom'
  CHECK(refresh_mode IN ('manual','every_3m','daily_0200','every_3m_plus_daily_0200','legacy_custom'));

ALTER TABLE segment_audience_schedule_states DROP CONSTRAINT segment_audience_schedule_states_pkey;
ALTER TABLE segment_audience_schedule_states
  ADD COLUMN schedule_kind TEXT NOT NULL DEFAULT 'legacy'
  CHECK(schedule_kind IN ('legacy','incremental','daily'));
ALTER TABLE segment_audience_schedule_states
  ADD PRIMARY KEY(configuration_version_id,schedule_kind);
CREATE INDEX segment_audience_schedule_states_kind_due_idx
  ON segment_audience_schedule_states(schedule_kind,next_due_at,package_id);
