-- Owner: Segment. Keep source evidence with each snapshot and calibrate once.
ALTER TABLE segment_audience_refresh_runs DROP CONSTRAINT segment_audience_refresh_runs_refresh_kind_check;
ALTER TABLE segment_audience_refresh_runs ADD CONSTRAINT segment_audience_refresh_runs_refresh_kind_check
 CHECK(refresh_kind IN ('legacy','manual','incremental','daily','source_rebase'));
ALTER TABLE segment_audience_refresh_runs ADD COLUMN source_watermarks JSONB NOT NULL DEFAULT '[]';
ALTER TABLE segment_audience_snapshots ADD COLUMN source_watermarks JSONB NOT NULL DEFAULT '[]';
