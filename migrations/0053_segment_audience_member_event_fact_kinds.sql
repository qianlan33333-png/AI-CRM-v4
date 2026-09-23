-- Owner: Segment/Audience.
-- Retention: durable audit and outbox facts remain append-only.
-- Strategy: forward-only repair. Migration 0048 added schedule facts but
-- accidentally replaced the member_event_batch kind introduced by 0045.

ALTER TABLE segment_audience_audit_events
  DROP CONSTRAINT segment_audience_audit_events_resource_kind_check;
ALTER TABLE segment_audience_audit_events
  ADD CONSTRAINT segment_audience_audit_events_resource_kind_check
  CHECK(resource_kind IN (
    'group','package','configuration','refresh_run','snapshot',
    'webhook_receipt','binding','sender_set','schedule','member_event_batch'
  ));

ALTER TABLE segment_audience_outbox
  DROP CONSTRAINT segment_audience_outbox_aggregate_kind_check;
ALTER TABLE segment_audience_outbox
  ADD CONSTRAINT segment_audience_outbox_aggregate_kind_check
  CHECK(aggregate_kind IN (
    'group','package','configuration','refresh_run','snapshot',
    'webhook_receipt','binding','sender_set','schedule','member_event_batch'
  ));
