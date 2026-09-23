package port

import (
	"context"
	"encoding/json"
	"time"
)

// DefinitionImport is an explicitly migration-only local product definition.
// It is accepted only with a caller-bound PostgreSQL transaction. It has no
// customer, order, payment, entitlement, provider, or external-effect field.
type DefinitionImport struct {
	ProductCode           string
	Name                  string
	Description           string
	PriceMinor            int64
	Currency              string
	StockQuantity         int32
	Images                []string
	LegacyAdminProjection json.RawMessage
	// ServicePeriodDurationDays is zero for an ordinary product and positive
	// only for the two imported service-period product definitions.
	ServicePeriodDurationDays int32
	Actor                     int64
	CreatedAt                 time.Time
	UpdatedAt                 time.Time
}

// DefinitionImporter is an internal composition seam for a one-time
// configuration migration. Implementations must reject an unbound transaction
// context and must not create receipts, outbox entries, or external effects.
type DefinitionImporter interface {
	ImportDefinition(context.Context, DefinitionImport) (Product, error)
}

// CutoverDefinitionChecker compares only commerce dependency facts. Display
// material, tagging, completion and other operator configuration remain owned
// by the target and are never overwritten when a source mapping is reused.
type CutoverDefinitionChecker interface {
	CheckCutoverDefinition(context.Context, ID, DefinitionImport) error
}
