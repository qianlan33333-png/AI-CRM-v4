-- Owner: product. Source is immutable through the public command contract.
ALTER TABLE products ADD COLUMN source_kind text NOT NULL DEFAULT '' CHECK(source_kind IN ('','wechat_shop'));
CREATE TABLE product_wechat_shop_skus (
 app_id text NOT NULL,
 source_product_id text NOT NULL,
 source_sku_id text NOT NULL,
 product_id bigint NOT NULL UNIQUE REFERENCES products(id),
 imported_at timestamptz NOT NULL,
 PRIMARY KEY(app_id,source_product_id,source_sku_id)
);
-- Owner: wechatshop. River owns execution and retry; these are refresh receipts.
CREATE TABLE wechat_shop_product_refreshes (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 app_id text NOT NULL,
 state text NOT NULL CHECK(state IN ('queued','running','retrying','succeeded')),
 created_at timestamptz NOT NULL DEFAULT now(),
 completed_at timestamptz,
 failed_count integer NOT NULL DEFAULT 0
);
CREATE UNIQUE INDEX wechat_shop_one_refresh ON wechat_shop_product_refreshes(app_id) WHERE state<>'succeeded';
CREATE TABLE wechat_shop_product_refresh_items (
 run_id bigint NOT NULL REFERENCES wechat_shop_product_refreshes(id),
 product_id bigint NOT NULL,
 added boolean NOT NULL,
 PRIMARY KEY(run_id,product_id)
);
