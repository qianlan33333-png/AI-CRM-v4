-- Owner: internal/order. Preserve the frozen member grid's admin_alliance
-- fact without creating a Product-owned copy or treating missing history as a
-- confirmed empty value.
ALTER TABLE order_service_entitlements
    ADD COLUMN alliance TEXT;
ALTER TABLE order_service_entitlements
    ADD CONSTRAINT order_service_entitlements_alliance_length_check
    CHECK (alliance IS NULL OR char_length(alliance) <= 500);

ALTER TABLE order_entitlement_operation_receipts
    DROP CONSTRAINT order_entitlement_operation_receipts_operation_check;
ALTER TABLE order_entitlement_operation_receipts
    ADD CONSTRAINT order_entitlement_operation_receipts_operation_check
    CHECK (operation IN ('remark','alliance'));

ALTER TABLE order_entitlement_audit_events
    DROP CONSTRAINT order_entitlement_audit_events_operation_check;
ALTER TABLE order_entitlement_audit_events
    ADD CONSTRAINT order_entitlement_audit_events_operation_check
    CHECK (operation IN ('remark','alliance','grant','renew','refund'));

ALTER TABLE order_entitlement_outbox
    DROP CONSTRAINT order_entitlement_outbox_event_type_check;
ALTER TABLE order_entitlement_outbox
    ADD CONSTRAINT order_entitlement_outbox_event_type_check
    CHECK (event_type IN (
        'order.entitlement.remark_updated.v1',
        'order.entitlement.alliance_updated.v1',
        'order.entitlement.granted.v1',
        'order.entitlement.renewed.v1',
        'order.entitlement.refunded.v1'
    ));
