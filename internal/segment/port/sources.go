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
	// QualifiedPaidOrder carries the latest qualifying current-paid order for
	// each evaluated member that needs downstream payment-event policy. It is
	// absent for legacy template evaluations that do not establish this fact.
	QualifiedPaidOrder map[customerdomain.CustomerID]PaidOrderFact
	Watermarks         []SourceWatermark
	ReferenceAt        time.Time
}

// PaidOrderFact is stable order-owner evidence copied into a Segment snapshot
// and its durable member event. It never contains Provider/customer identity.
type PaidOrderFact struct {
	PaidOrderID int64
	PaidAt      time.Time
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
