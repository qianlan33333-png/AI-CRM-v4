-- Owner: AdminOps. Correlation links a request's events; it is not one event.
-- Zero preserves the meaning of historical observations whose River attempt
-- was not captured. It is never a guessed Provider attempt.
ALTER TABLE adminops_diagnostic_events
 ADD COLUMN job_attempt INTEGER NOT NULL DEFAULT 0
 CHECK(job_attempt >= 0 AND (job_attempt = 0 OR job_ref <> ''));

ALTER TABLE adminops_diagnostic_events
 DROP CONSTRAINT adminops_diagnostic_events_component_code_correlation_diges_key;
ALTER TABLE adminops_diagnostic_events
 ADD CONSTRAINT adminops_diagnostic_event_identity
 UNIQUE(component,code,correlation_digest,job_ref,effect_ref,job_attempt);
