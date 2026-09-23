package port

import (
	"context"
	"time"
)

// DefinitionImport is a configuration-only coupon rule. Issuance, claims,
// redemptions, orders, payment, entitlement and customer facts are excluded.
type DefinitionImport struct {
	Coupon
	Actor     int64
	CreatedAt time.Time
	UpdatedAt time.Time
}

// DefinitionImporter participates in the configuration migration's caller
// transaction; it is not a normal Coupon administration command.
type DefinitionImporter interface {
	ImportDefinition(context.Context, DefinitionImport) (Coupon, error)
}

// CutoverDefinitionImport preserves source issuance totals and public links.
// ExistingID is set only by a trusted migration source mapping. Existing rules
// must still match the supplied business definition; only explicitly proven
// source limit increases and receipt-checked counters may advance.
type CutoverDefinitionImport struct {
	DefinitionImport
	ExistingID               ID
	PublicSlug               string
	ExpectedIssuedCount      *int64
	ExpectedTotalIssueLimit  int64
	AllowSourceLimitIncrease bool
}
type CutoverDefinitionImporter interface {
	ImportCutoverDefinition(context.Context, CutoverDefinitionImport) (Coupon, error)
}

// ReviewedCutoverImporter is a narrow explicit source-authoritative exception:
// full before-state CAS, unchanged core terms/targets, zero target claims.
type ReviewedCutoverImporter interface {
	CutoverReviewDigest(context.Context, ID) ([32]byte, error)
	ImportReviewedCutoverDefinition(context.Context, CutoverDefinitionImport, [32]byte) (Coupon, error)
}
