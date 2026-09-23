package port

import (
	"context"
	"encoding/json"
	"time"
)

// DefinitionImport contains a local plan definition only. The coordinator
// supplies the explicit migration actor. Runs, executions, provider state,
// webhooks, recipients, and identity facts are intentionally absent.
type DefinitionImport struct {
	Name        string
	Status      PlanStatus
	Members     []Member
	GroupAssets []GroupAsset
	Nodes       []Node
	// LegacyDefinition and LegacyNodeDefinitions retain safe, non-executing
	// source scheduling semantics that the frozen donor UI does not edit.
	LegacyDefinition      json.RawMessage
	LegacyNodeDefinitions map[int32]json.RawMessage
	Actor                 int64
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

type DefinitionImporter interface {
	ImportDefinition(context.Context, DefinitionImport) (Plan, error)
}
