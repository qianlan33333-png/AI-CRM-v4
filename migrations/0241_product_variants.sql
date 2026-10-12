-- Product owns optional native product variants. Historical products keep their price.
ALTER TABLE products ADD COLUMN variants_enabled boolean NOT NULL DEFAULT false;
CREATE TABLE product_variants (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 product_id bigint NOT NULL REFERENCES products(id) ON DELETE CASCADE,
 name text NOT NULL CHECK(length(name)>0),
 description text NOT NULL DEFAULT '',
 attributes jsonb NOT NULL DEFAULT '[]' CHECK(jsonb_typeof(attributes)='array'),
 price_minor bigint NOT NULL CHECK(price_minor>=0),
 version bigint NOT NULL DEFAULT 1 CHECK(version>0),
 position integer NOT NULL,
 removed_at timestamptz
);
CREATE INDEX product_variants_product ON product_variants(product_id,position,id);
