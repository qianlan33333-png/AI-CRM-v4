-- Coupon Owner. Preserve actual legacy public coupon URLs without rewriting
-- their case or underscores. Route separators/whitespace/percent remain banned.
ALTER TABLE coupon_rules DROP CONSTRAINT coupon_rules_public_slug_check;
ALTER TABLE coupon_rules ADD CONSTRAINT coupon_rules_public_slug_check
 CHECK(public_slug IS NULL OR public_slug ~ '^[A-Za-z0-9_-]{6,120}$');
