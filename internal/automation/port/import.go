package port

import (
	"context"
	"time"
)

// DefinitionImport imports a local agent runtime configuration as disabled.
// Content references are intentionally empty: the configuration migration does
// not import Media, attachments, group invites, generated content, execution,
// provider state, or customer facts.
type DefinitionImport struct {
	Agent     Agent
	Actor     int64
	CreatedAt time.Time
	UpdatedAt time.Time
}

// DefinitionImporter is a migration-only seam. It requires the caller's
// transaction and cannot enable execution or submit an external effect.
type DefinitionImporter interface {
	ImportDefinition(context.Context, DefinitionImport) (Agent, error)
}
