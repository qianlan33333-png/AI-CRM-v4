package store

import (
	"context"
	"encoding/json"

	groupopsdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/domain"
	groupopsport "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/port"
)

var _ groupopsport.DefinitionImporter = (*Repository)(nil)

// ImportDefinition persists a local plan inside the caller's transaction. It
// creates neither run nor execution records, and rejects imported Media refs.
func (r *Repository) ImportDefinition(ctx context.Context, input groupopsport.DefinitionImport) (groupopsport.Plan, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return groupopsport.Plan{}, err
	}
	if input.Actor < 1 || !groupopsdomain.ValidText(input.Name, groupopsdomain.MaxNameLength) || input.CreatedAt.IsZero() || input.UpdatedAt.IsZero() || input.UpdatedAt.Before(input.CreatedAt) || input.Status != groupopsport.PlanPaused || len(input.Members) != 0 || !validJSONObject(input.LegacyDefinition) {
		return groupopsport.Plan{}, ErrInvalid
	}
	var plan groupopsport.Plan
	err = tx.QueryRow(ctx, `INSERT INTO group_ops_plans(name,status,revision,created_by,updated_by,created_at,updated_at,legacy_import_definition) VALUES($1,$2,1,$3,$3,$4,$5,$6::jsonb) RETURNING id,name,status,revision,created_by,updated_by,created_at,updated_at`,
		input.Name, input.Status, input.Actor, input.CreatedAt.UTC(), input.UpdatedAt.UTC(), input.LegacyDefinition).Scan(&plan.ID, &plan.Name, &plan.Status, &plan.Revision, &plan.CreatedBy, &plan.UpdatedBy, &plan.CreatedAt, &plan.UpdatedAt)
	if err != nil {
		return groupopsport.Plan{}, err
	}
	for _, member := range input.Members {
		if member.StaffID < 1 {
			return groupopsport.Plan{}, ErrInvalid
		}
		if _, err = tx.Exec(ctx, `INSERT INTO group_ops_plan_members(plan_id,staff_id) VALUES($1,$2)`, plan.ID, member.StaffID); err != nil {
			return groupopsport.Plan{}, err
		}
	}
	for _, asset := range input.GroupAssets {
		if asset.ID != 0 || !groupopsdomain.ValidOpaque(asset.AssetRef) {
			return groupopsport.Plan{}, ErrInvalid
		}
		if _, err = tx.Exec(ctx, `INSERT INTO group_ops_plan_group_assets(plan_id,asset_reference) VALUES($1,$2)`, plan.ID, asset.AssetRef); err != nil {
			return groupopsport.Plan{}, err
		}
	}
	for _, node := range input.Nodes {
		if node.ID != 0 || len(node.MaterialPlan.References) != 0 || node.MaterialRef != "" {
			return groupopsport.Plan{}, ErrInvalid
		}
		check := node
		check.ID = 1
		if groupopsdomain.ValidateNode(check) != nil {
			return groupopsport.Plan{}, ErrInvalid
		}
		raw, marshalErr := json.Marshal(groupopsport.MaterialPlan{References: []groupopsport.MaterialReference{}})
		if marshalErr != nil {
			return groupopsport.Plan{}, marshalErr
		}
		legacy, ok := input.LegacyNodeDefinitions[node.Position]
		if !ok || !validJSONObject(legacy) {
			return groupopsport.Plan{}, ErrInvalid
		}
		if _, err = tx.Exec(ctx, `INSERT INTO group_ops_plan_nodes(plan_id,position,kind,message_text,delay_minutes,material_reference,material_plan,legacy_import_definition,schedule_semantics) VALUES($1,$2,$3,$4,$5,'',$6::jsonb,$7::jsonb,'relative_delay')`, plan.ID, node.Position, node.Kind, node.MessageText, node.DelayMinutes, raw, legacy); err != nil {
			return groupopsport.Plan{}, err
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO group_ops_plan_webhook_descriptors(plan_id,reference) VALUES($1,'')`, plan.ID); err != nil {
		return groupopsport.Plan{}, err
	}
	return plan, nil
}

func validJSONObject(raw json.RawMessage) bool {
	if !json.Valid(raw) {
		return false
	}
	var value map[string]json.RawMessage
	return json.Unmarshal(raw, &value) == nil && value != nil
}
