-- Owner: HXC. Old published rows remain explicitly unobserved, not zero usage.
ALTER TABLE hxc_dashboard_rows
 ADD COLUMN last_token_used_at TIMESTAMPTZ,
 ADD COLUMN token_usage_observed_at TIMESTAMPTZ;
