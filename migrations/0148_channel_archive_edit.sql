-- Owner: channel. Forward-only; preserve all channel rows and immutable snapshots.
-- Archive controls availability; operators may explicitly restore a channel.
-- Code immutability, delete/truncate guards and snapshot guards stay in force.
CREATE OR REPLACE FUNCTION channel_catalog_guard() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog AS $$
BEGIN
    IF TG_TABLE_NAME = 'channels' THEN
        IF TG_OP = 'DELETE' OR TG_OP = 'TRUNCATE' THEN
            RAISE EXCEPTION 'channels are archive-only';
        END IF;
        IF NEW.code IS DISTINCT FROM OLD.code THEN
            RAISE EXCEPTION 'channel code is immutable';
        END IF;
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'channel configuration and assignment snapshots are immutable';
END;
$$;
