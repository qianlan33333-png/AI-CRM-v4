package main

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ensureAccessLoginFixtureSchema keeps reduced composition fixtures compatible
// with Access's current staff projection contract. These journeys exercise
// business staff availability, not Access governance, so they deliberately
// retain their narrow migration sets while enforcing the login-grant invariant
// that production's 0152 migration adds.
func ensureAccessLoginFixtureSchema(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `
		DO $$
		BEGIN
			IF NOT EXISTS (
				SELECT 1 FROM information_schema.columns
				 WHERE table_schema = current_schema()
				   AND table_name = 'admin_users'
				   AND column_name = 'login_enabled'
			) THEN
				ALTER TABLE admin_users
					ADD COLUMN login_enabled BOOLEAN NOT NULL DEFAULT TRUE,
					ADD COLUMN legacy_login_reactivation_pending BOOLEAN NOT NULL DEFAULT FALSE,
					ADD COLUMN access_granted_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP;

				UPDATE admin_users
				   SET login_enabled = is_active,
					   legacy_login_reactivation_pending = NOT is_active,
					   access_granted_at = created_at;

				ALTER TABLE admin_users
					ALTER COLUMN access_granted_at DROP NOT NULL,
					ALTER COLUMN access_granted_at DROP DEFAULT,
					ADD CONSTRAINT ck_admin_users_login_requires_access_grant
					CHECK (access_granted_at IS NOT NULL OR login_enabled = FALSE);
			END IF;
		END $$;`)
	return err
}
