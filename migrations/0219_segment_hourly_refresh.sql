-- Owner: Segment. Hourly is incremental and reuses the existing durable cursor.
ALTER TABLE segment_audience_configuration_versions
 DROP CONSTRAINT segment_audience_configuration_versions_refresh_mode_check;
ALTER TABLE segment_audience_configuration_versions
 ADD CONSTRAINT segment_audience_configuration_versions_refresh_mode_check
 CHECK(refresh_mode IN ('manual','every_3m','every_1h','daily_0200','every_3m_plus_daily_0200','legacy_custom'));
