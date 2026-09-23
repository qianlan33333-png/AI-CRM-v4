package store

import (
	"context"
	"encoding/json"
	"strings"

	automationapp "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/app"
	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
)

var _ automationport.DefinitionImporter = (*Repository)(nil)

// ImportDefinition creates a safe Automation runtime configuration inside the
// caller's transaction. Active source records become paused; archives stay
// archived. Fixed content Media references and dynamic cards are rejected.
func (r *Repository) ImportDefinition(ctx context.Context, input automationport.DefinitionImport) (automationport.Agent, error) {
	t, err := tx(ctx)
	if err != nil {
		return automationport.Agent{}, err
	}
	a := input.Agent
	if input.Actor < 1 || a.ID != 0 || strings.TrimSpace(a.AgentName) != a.AgentName || a.AgentName == "" ||
		strings.TrimSpace(a.AgentCode) != a.AgentCode || a.AgentCode == "" ||
		(a.AutomationType != automationport.AutomationTypeAgent && a.AutomationType != automationport.AutomationTypeFixedScript) ||
		input.CreatedAt.IsZero() || input.UpdatedAt.IsZero() || input.UpdatedAt.Before(input.CreatedAt) ||
		len(a.FixedContentPackage.ImageLibraryIDs) != 0 || len(a.FixedContentPackage.MiniprogramLibraryIDs) != 0 || len(a.FixedContentPackage.AttachmentLibraryIDs) != 0 ||
		len(a.FixedContentPackage.GroupInviteLibraryIDs) != 0 || len(a.FixedContentPackage.DynamicMiniprogramCard) != 0 || !json.Valid(a.LegacyConfiguration) ||
		(a.Status != automationport.AgentStatusActive && a.Status != automationport.AgentStatusPaused && a.Status != automationport.AgentStatusArchived) ||
		a.DraftVersion < 1 || a.PublishedVersion < 1 || a.PublishedVersion > a.DraftVersion {
		return automationport.Agent{}, ErrInvalid
	}
	var legacy map[string]json.RawMessage
	if json.Unmarshal(a.LegacyConfiguration, &legacy) != nil || legacy == nil {
		return automationport.Agent{}, ErrInvalid
	}
	if a.AutomationType != automationport.AutomationTypeFixedScript && a.FixedContentPackage.ContentText != "" {
		return automationport.Agent{}, ErrInvalid
	}
	if a.Status == automationport.AgentStatusActive {
		a.Status = automationport.AgentStatusPaused
	}
	a.ExecutionEnabled = false
	a.FixedContentPackage = automationport.FixedContentPackage{ContentText: a.FixedContentPackage.ContentText, ImageLibraryIDs: []int64{}, MiniprogramLibraryIDs: []int64{}, AttachmentLibraryIDs: []int64{}, GroupInviteLibraryIDs: []int64{}}
	a.ID, a.CreatedBy, a.UpdatedBy, a.CreatedAt, a.UpdatedAt = 1, input.Actor, input.Actor, input.CreatedAt.UTC(), input.UpdatedAt.UTC()
	if !automationapp.ValidPersistedForImport(a) {
		return automationport.Agent{}, ErrInvalid
	}
	fixed, err := json.Marshal(a.FixedContentPackage)
	if err != nil {
		return automationport.Agent{}, err
	}
	legacyRaw, err := json.Marshal(legacy)
	if err != nil {
		return automationport.Agent{}, err
	}
	row := t.QueryRow(ctx, `INSERT INTO automation_agents(agent_name,agent_code,automation_type,status,execution_enabled,draft_role_prompt,draft_task_prompt,published_role_prompt,published_task_prompt,draft_version,published_version,fixed_content_package,legacy_configuration,created_by,updated_by,created_at,updated_at,archived_at)
		VALUES($1,$2,$3,$4,FALSE,$5,$6,$7,$8,$9,$10,$11::jsonb,$12::jsonb,$13,$13,$14::timestamptz,$15::timestamptz,CASE WHEN $4='archived' THEN $15::timestamptz ELSE NULL::timestamptz END) RETURNING `+agentColumns,
		a.AgentName, a.AgentCode, a.AutomationType, a.Status, a.DraftRolePrompt, a.DraftTaskPrompt, a.PublishedRolePrompt, a.PublishedTaskPrompt,
		a.DraftVersion, a.PublishedVersion, fixed, legacyRaw, input.Actor, input.CreatedAt.UTC(), input.UpdatedAt.UTC())
	return scanAgent(row)
}
