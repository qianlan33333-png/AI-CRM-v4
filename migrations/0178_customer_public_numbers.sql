-- Owner: identity
-- Public numbers are stable business aliases, not customer roots or identities.
-- Existing primary keys, lineage and all foreign keys remain unchanged.
CREATE SEQUENCE customer_public_number_seq MINVALUE 1000000 MAXVALUE 9999999 START 1000000 NO CYCLE;
ALTER TABLE customers ADD COLUMN public_number BIGINT;
WITH numbered AS (
 SELECT id, 999999 + row_number() OVER (ORDER BY id) AS number FROM customers
)
UPDATE customers c SET public_number=n.number FROM numbered n WHERE c.id=n.id;
ALTER TABLE customers ADD CONSTRAINT customers_public_number_range CHECK (public_number BETWEEN 1000000 AND 9999999);
ALTER TABLE customers ADD CONSTRAINT customers_public_number_unique UNIQUE(public_number);
SELECT setval('customer_public_number_seq', COALESCE((SELECT max(public_number) FROM customers),1000000), EXISTS(SELECT 1 FROM customers));
ALTER TABLE customers ALTER COLUMN public_number SET DEFAULT nextval('customer_public_number_seq');
ALTER TABLE customers ALTER COLUMN public_number SET NOT NULL;
ALTER SEQUENCE customer_public_number_seq OWNED BY customers.public_number;
