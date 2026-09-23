-- Owner: internal/customer.
-- Forward-only repair: every existing canonical Customer root receives the
-- minimum searchable Customer-owned directory projection. No external
-- identity value, nickname guess, merge, or Provider call is introduced.
INSERT INTO customer_directory_projection (
    customer_id,
    customer_status,
    display_name,
    oneid_label,
    activation_status,
    source,
    source_version,
    last_synced_at,
    updated_at
)
SELECT
    customer.id,
    customer.status,
    '微信用户',
    'CID-' || customer.id::text,
    CASE WHEN customer.status = 'active' THEN 'active' ELSE 'stale' END,
    'identity_provision_backfill',
    1,
    customer.updated_at,
    customer.updated_at
FROM customers customer
WHERE NOT EXISTS (
    SELECT 1
    FROM customer_directory_projection directory
    WHERE directory.customer_id = customer.id
)
ON CONFLICT (customer_id) DO NOTHING;
