-- Owners: internal/outbound and internal/sidebar.
-- Coupon cards use the existing JSSDK send intent, receipt and grant. The
-- public Coupon claim command remains the only stock-allocation path.
ALTER TABLE outbound_sidebar_send_intents
    DROP CONSTRAINT outbound_sidebar_send_intents_resource_kind_check;
ALTER TABLE outbound_sidebar_send_intents
    ADD CONSTRAINT outbound_sidebar_send_intents_resource_kind_check
    CHECK (resource_kind IN ('product','coupon','material','radar_link'));
