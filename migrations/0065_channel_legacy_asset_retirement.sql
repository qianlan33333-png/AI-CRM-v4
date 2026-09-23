-- Migration 0065. Owner: internal/channel.
-- A successful replacement retires a legacy asset without invalidating the
-- immutable fact that its provider readback was verified. Forward-only because
-- restoring the old constraint would reject already-retired verified assets.
ALTER TABLE channel_legacy_acquisition_assets
    DROP CONSTRAINT channel_legacy_acquisition_assets_check;

ALTER TABLE channel_legacy_acquisition_assets
    ADD CONSTRAINT channel_legacy_acquisition_assets_check CHECK (
        verification_status <> 'legacy_verified_active'
        OR (
            provider_asset_ref <> ''
            AND result_url <> ''
            AND verified_at IS NOT NULL
        )
    );
