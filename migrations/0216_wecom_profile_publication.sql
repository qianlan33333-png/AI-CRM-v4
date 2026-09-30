-- Owner: WeCom. Published directory facts and immutable observed tag changes.
ALTER TABLE wecom_customer_sync_runs DROP CONSTRAINT wecom_customer_sync_runs_trigger_type_check;
ALTER TABLE wecom_customer_sync_runs ADD CONSTRAINT wecom_customer_sync_runs_trigger_type_check
 CHECK (trigger_type IN ('initial','daily','manual','tag_refresh','unionid_refresh','new_contact','description_backfill'));
DROP INDEX wecom_customer_sync_runs_one_active_idx;
CREATE UNIQUE INDEX wecom_customer_sync_runs_one_active_idx ON wecom_customer_sync_runs((true))
 WHERE trigger_type <> 'new_contact' AND status IN ('queued','listing_staff','fetching_profiles','ingesting','reconciling','failed_retryable');
CREATE TABLE wecom_directory_publications (
 corp_scope TEXT PRIMARY KEY, revision BIGINT NOT NULL DEFAULT 0,
 last_run_id BIGINT REFERENCES wecom_customer_sync_runs(id), observed_at TIMESTAMPTZ,
 full_observed_at TIMESTAMPTZ, complete BOOLEAN NOT NULL DEFAULT false,
 baseline_initialized BOOLEAN NOT NULL DEFAULT false, baseline_date DATE NOT NULL DEFAULT DATE '2026-09-30'
);
CREATE TABLE wecom_customer_profile_staging (
 run_id BIGINT NOT NULL REFERENCES wecom_customer_sync_runs(id), customer_id BIGINT NOT NULL REFERENCES customers(id),
 identity_id BIGINT NOT NULL, contact JSONB NOT NULL, observed_at TIMESTAMPTZ NOT NULL,
 PRIMARY KEY(run_id,customer_id)
);
CREATE TABLE wecom_customer_follow_staging (
 run_id BIGINT NOT NULL REFERENCES wecom_customer_sync_runs(id), customer_id BIGINT NOT NULL REFERENCES customers(id),
 employee_id TEXT NOT NULL, follow_info JSONB NOT NULL, observed_at TIMESTAMPTZ NOT NULL,
 PRIMARY KEY(run_id,customer_id,employee_id)
);
ALTER TABLE wecom_customer_owner_observations ADD COLUMN detail JSONB NOT NULL DEFAULT '{}';
ALTER TABLE wecom_customer_owner_observations ADD COLUMN publication_revision BIGINT NOT NULL DEFAULT 0;
ALTER TABLE wecom_external_contact_profiles ADD COLUMN publication_revision BIGINT NOT NULL DEFAULT 0;
ALTER TABLE wecom_external_contact_profiles ADD COLUMN complete_follow_observed_at TIMESTAMPTZ;
ALTER TABLE wecom_customer_tag_observations DROP CONSTRAINT wecom_customer_tag_observations_provider_tag_type_check;
ALTER TABLE wecom_customer_tag_observations ADD CONSTRAINT wecom_customer_tag_observations_provider_tag_type_check CHECK(provider_tag_type BETWEEN 1 AND 3);
ALTER TABLE wecom_customer_tag_observations DROP CONSTRAINT wecom_customer_tag_observations_pkey;
ALTER TABLE wecom_customer_tag_observations ADD PRIMARY KEY(customer_id,corp_scope,employee_id,provider_tag_type,provider_tag_id);
ALTER TABLE wecom_customer_tag_observations ADD COLUMN group_name TEXT NOT NULL DEFAULT '';
ALTER TABLE wecom_customer_tag_observations ADD COLUMN period_started_at TIMESTAMPTZ;
ALTER TABLE wecom_customer_tag_observations ADD COLUMN period_start_kind TEXT;
ALTER TABLE wecom_customer_tag_observations ADD COLUMN baseline_date DATE;
ALTER TABLE wecom_customer_tag_observations ADD COLUMN absence_reason TEXT CHECK(absence_reason IN ('tag_removed','follow_not_observed'));
CREATE TABLE wecom_customer_tag_history (
 id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 customer_id BIGINT NOT NULL REFERENCES customers(id), corp_scope TEXT NOT NULL,
 employee_id TEXT NOT NULL, provider_tag_type SMALLINT NOT NULL CHECK(provider_tag_type IN (1,2,3)), provider_tag_id TEXT NOT NULL,
 observed_name TEXT NOT NULL, group_name TEXT NOT NULL DEFAULT '',
 event_type TEXT NOT NULL CHECK(event_type IN ('baseline','added','removed','readded')),
 reason TEXT NOT NULL CHECK(reason IN ('initial_baseline','tag_set_changed','follow_not_observed')),
 discovered_at TIMESTAMPTZ NOT NULL, discovered_date DATE NOT NULL, registration_date DATE NOT NULL,
 previous_observed_at TIMESTAMPTZ, provider_operated_at TIMESTAMPTZ,
 from_revision BIGINT NOT NULL, to_revision BIGINT NOT NULL,
 run_id BIGINT NOT NULL REFERENCES wecom_customer_sync_runs(id),
 customer_transition BOOLEAN NOT NULL DEFAULT false,
 customer_event_type TEXT CHECK(customer_event_type IN ('baseline','added','removed','readded')),
 UNIQUE(run_id,customer_id,corp_scope,employee_id,provider_tag_type,provider_tag_id,event_type)
);
CREATE INDEX wecom_customer_tag_history_customer_idx ON wecom_customer_tag_history(customer_id,id DESC);
CREATE INDEX wecom_customer_tag_history_statistics_idx ON wecom_customer_tag_history(discovered_date,event_type,customer_id);
-- Existing state is registered only when a complete first publication succeeds.
