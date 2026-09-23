-- Owner: internal/coupon.
--
-- Public slugs are optional for imported rules until their first explicit
-- share. Once assigned they are immutable; numeric rule IDs never become a
-- public alias. The unique partial index permits legacy rows to remain
-- unshared while a Coupon-owned action creates one stable code on demand.

ALTER TABLE coupon_rules
    ADD COLUMN public_slug TEXT CHECK (public_slug IS NULL OR public_slug ~ '^[a-z][a-z0-9-]{5,119}$');

CREATE UNIQUE INDEX coupon_rules_public_slug_unique
    ON coupon_rules(public_slug) WHERE public_slug IS NOT NULL;

CREATE FUNCTION coupon_rules_public_slug_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.public_slug IS NOT NULL AND NEW.public_slug IS DISTINCT FROM OLD.public_slug THEN
        RAISE EXCEPTION 'coupon public slug is immutable';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER coupon_rules_public_slug_immutable_before_update
    BEFORE UPDATE ON coupon_rules
    FOR EACH ROW EXECUTE FUNCTION coupon_rules_public_slug_immutable();
