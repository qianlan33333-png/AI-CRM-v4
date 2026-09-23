package port

import (
	"context"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
)

type HistoricalVerifiedInput struct {
	Kind, Scope, Value, Source string
}

// HistoricalFactFactory is implemented only by an audited provider-history
// adapter. Migration orchestration cannot mint verified facts itself.
type HistoricalFactFactory interface {
	VerifiedHistoricalFact(HistoricalVerifiedInput) (domain.VerifiedFact, error)
}

// HistoricalSubjectCommand binds every authoritative identity exported for one
// source person to one Customer root. SourceDigest is safe audit evidence; raw
// identifiers must never be copied into evidence metadata or logs.
type HistoricalSubjectCommand struct {
	SubjectKey   string
	Facts        []domain.VerifiedFact
	SourceDigest [32]byte
}

type HistoricalSubjectResult struct {
	CustomerID  customerdomain.CustomerID
	IdentityIDs []int64
}

// HistoricalSubjectProvisioner is intentionally narrower than the interactive
// merge API. An existing cross-root identity fails closed; this port can never
// confirm or perform a merge.
type HistoricalSubjectProvisioner interface {
	ProvisionHistoricalSubject(context.Context, HistoricalSubjectCommand) (HistoricalSubjectResult, error)
}
