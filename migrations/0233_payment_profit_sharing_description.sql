-- Owner: Payment. Empty preserves the original English description of a legacy
-- instruction. This is presentation material, not identity or money authority.
ALTER TABLE payment_profit_sharing_instructions
  ADD COLUMN description_snapshot TEXT NOT NULL DEFAULT ''
  CHECK (octet_length(description_snapshot) <= 80);

CREATE FUNCTION payment_profit_sharing_description_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.description_snapshot IS DISTINCT FROM OLD.description_snapshot THEN
    RAISE EXCEPTION 'profit sharing description is immutable';
  END IF;
  RETURN NEW;
END;
$$;

CREATE TRIGGER payment_profit_sharing_description_immutable
BEFORE UPDATE OF description_snapshot ON payment_profit_sharing_instructions
FOR EACH ROW EXECUTE FUNCTION payment_profit_sharing_description_immutable();
