package port

import (
	"context"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
)

const MaximumEvaluationMembers = 100000

type SourceWatermark struct {
	Source     string    `json:"source"`
	AsOf       time.Time `json:"as_of"`
	Fresh      bool      `json:"fresh"`
	SafeDigest Digest    `json:"-"`
}

type Evaluation struct {
	CustomerIDs []customerdomain.CustomerID
	Watermarks  []SourceWatermark
	ReferenceAt time.Time
}

// DefinitionSource evaluates a closed, validated definition. It cannot accept
// arbitrary SQL, table names, sort expressions, or provider identifiers.
type DefinitionSource interface {
	Evaluate(context.Context, Definition, time.Time) (Evaluation, error)
}

// CanonicalCustomerResolver follows Customer-owned aliases and returns only
// canonical roots. It never provisions, binds, or merges a customer.
type CanonicalCustomerResolver interface {
	CanonicalCustomers(context.Context, []customerdomain.CustomerID) ([]customerdomain.CustomerID, error)
}

// GroupRefreshTargetReader lists only active closed-rule dependencies; no SQL
// or source membership data crosses this boundary.
type GroupRefreshTargetReader interface {
	ActiveGroupRefreshTargets(context.Context) ([]string, error)
}
