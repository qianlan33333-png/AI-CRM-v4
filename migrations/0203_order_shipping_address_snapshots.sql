-- Owner: internal/order. Immutable checkout shipping facts for ordinary
-- physical products. Logistics, rates and provider identifiers are out of
-- scope; the administrative region codes/names are frozen at checkout time.
CREATE TABLE order_shipping_address_snapshots (
    order_id BIGINT PRIMARY KEY REFERENCES orders(id) ON DELETE RESTRICT,
    recipient_name TEXT NOT NULL CHECK (char_length(recipient_name) BETWEEN 1 AND 80),
    province_code TEXT NOT NULL CHECK (char_length(province_code) BETWEEN 1 AND 32),
    province_name TEXT NOT NULL CHECK (char_length(province_name) BETWEEN 1 AND 80),
    city_code TEXT NOT NULL CHECK (char_length(city_code) BETWEEN 1 AND 32),
    city_name TEXT NOT NULL CHECK (char_length(city_name) BETWEEN 1 AND 80),
    district_code TEXT NOT NULL CHECK (char_length(district_code) BETWEEN 1 AND 32),
    district_name TEXT NOT NULL CHECK (char_length(district_name) BETWEEN 1 AND 80),
    detail_address TEXT NOT NULL CHECK (char_length(detail_address) BETWEEN 1 AND 300),
    created_at TIMESTAMPTZ NOT NULL
);

CREATE TRIGGER order_shipping_address_snapshots_no_mutation
BEFORE UPDATE OR DELETE OR TRUNCATE ON order_shipping_address_snapshots
FOR EACH STATEMENT EXECUTE FUNCTION order_immutable_facts_reject_mutation();
