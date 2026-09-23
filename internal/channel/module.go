package channel

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"
)

type ModuleRegistration struct{}

func NewModuleRegistration() *ModuleRegistration { return &ModuleRegistration{} }

func (module *ModuleRegistration) Readiness(ctx context.Context, pool *pgxpool.Pool) error {
	if module == nil || pool == nil {
		return errors.New("channel module dependencies are required")
	}
	var ready bool
	err := pool.QueryRow(ctx, `SELECT NOT EXISTS (SELECT 1 FROM unnest(ARRAY['channels','channel_config_versions','channel_assignees','channel_operation_receipts','channel_acquisition_state_bindings','channel_acquisition_entrant_receipts','channel_history_import_runs','channel_history_source_maps','channel_history_contacts','channel_history_assignees','channel_history_effects','channel_acquisition_assets','channel_asset_reconciliation_receipts','channel_entrant_assignments','channel_entrant_actions','channel_welcome_intents','channel_welcome_message_snapshots','channel_acquisition_link_receipts','channel_acquisition_link_reconciliations','channel_semantic_repair_runs','channel_semantic_repair_conflicts','channel_semantic_repaired_configs','channel_legacy_acquisition_assets','channel_legacy_material_maps','channel_legacy_tag_maps','channel_history_contact_reconciliations']) AS required(name) WHERE to_regclass(current_schema() || '.' || required.name) IS NULL)`).Scan(&ready)
	if err != nil {
		return err
	}
	if !ready {
		return errors.New("channel schema is not ready")
	}
	return nil
}
