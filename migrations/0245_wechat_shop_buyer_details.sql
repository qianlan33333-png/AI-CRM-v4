-- Owner: Order. Live buyer metadata is separate from frozen paid/fulfilment facts.
ALTER TABLE order_wechat_shop_facts
 ADD COLUMN buyer_details_ciphertext bytea,
 ADD COLUMN buyer_details_key_version smallint,
 ADD COLUMN buyer_union_scope text NOT NULL DEFAULT '',
 ADD COLUMN buyer_union_digest bytea,
 ADD COLUMN buyer_association_state text NOT NULL DEFAULT 'pending'
   CHECK(buyer_association_state IN ('pending','linked','conflict')),
 ADD CONSTRAINT shop_buyer_details_cipher_shape CHECK
   ((buyer_details_ciphertext IS NULL AND buyer_details_key_version IS NULL)
    OR (buyer_details_ciphertext IS NOT NULL AND buyer_details_key_version IS NOT NULL AND buyer_details_key_version=1)),
 ADD CONSTRAINT shop_buyer_union_shape CHECK
   ((buyer_union_scope='' AND buyer_union_digest IS NULL)
    OR (buyer_union_scope<>'' AND buyer_union_digest IS NOT NULL AND octet_length(buyer_union_digest)=32));
CREATE INDEX order_shop_pending_buyer_lookup
 ON order_wechat_shop_facts(buyer_union_scope,buyer_union_digest,order_id)
 WHERE buyer_association_state='pending' AND buyer_union_digest IS NOT NULL;
