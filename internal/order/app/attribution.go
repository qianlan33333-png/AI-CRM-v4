package app

import (
	"context"

	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
)

type AttributionStatus string

const (
	AttributionResolved    AttributionStatus = "resolved"
	AttributionQuarantined AttributionStatus = "quarantined"
	AttributionRejected    AttributionStatus = "rejected"
)

type ActorEvidence struct {
	References []identitydomain.Reference
}

type AttributionCommand struct {
	RecordOrigin domain.RecordOrigin
	Payer        ActorEvidence
	Beneficiary  ActorEvidence
}

// AttributionReceipt is safe to persist in an import quarantine ledger: it
// contains no external identity values.
type AttributionReceipt struct {
	Status                   AttributionStatus
	ReasonCode               string
	PayerCustomerID          *int64
	BeneficiaryCustomerID    *int64
	PayerEvidenceCount       int
	BeneficiaryEvidenceCount int
}

type Attributor struct {
	identity identityport.CommerceResolver
}

func NewAttributor(identity identityport.CommerceResolver) *Attributor {
	return &Attributor{identity: identity}
}

func (a *Attributor) Attribute(ctx context.Context, command AttributionCommand) (AttributionReceipt, error) {
	receipt := AttributionReceipt{
		PayerEvidenceCount:       len(command.Payer.References),
		BeneficiaryEvidenceCount: len(command.Beneficiary.References),
	}
	if a == nil || a.identity == nil || (command.RecordOrigin != domain.RecordOriginNative && command.RecordOrigin != domain.RecordOriginHistory) {
		receipt.Status, receipt.ReasonCode = AttributionRejected, "invalid_attribution_command"
		return receipt, nil
	}
	if command.RecordOrigin == domain.RecordOriginNative && (!allVerified(command.Payer.References) || !allVerified(command.Beneficiary.References)) {
		receipt.Status, receipt.ReasonCode = AttributionRejected, "native_identity_not_verified"
		return receipt, nil
	}

	payer, err := a.identity.ResolveCommerce(ctx, identityport.CommerceReferenceSet{References: command.Payer.References})
	if err != nil {
		return AttributionReceipt{}, err
	}
	beneficiary, err := a.identity.ResolveCommerce(ctx, identityport.CommerceReferenceSet{References: command.Beneficiary.References})
	if err != nil {
		return AttributionReceipt{}, err
	}
	receipt.PayerCustomerID = resolvedCustomer(payer)
	receipt.BeneficiaryCustomerID = resolvedCustomer(beneficiary)

	if payer.Status == identityport.CommerceResolved && beneficiary.Status == identityport.CommerceResolved {
		receipt.Status = AttributionResolved
		return receipt, nil
	}
	reason := attributionReason(payer.Status, beneficiary.Status)
	if command.RecordOrigin == domain.RecordOriginHistory {
		receipt.Status, receipt.ReasonCode = AttributionQuarantined, reason
		return receipt, nil
	}
	receipt.Status, receipt.ReasonCode = AttributionRejected, reason
	return receipt, nil
}

func allVerified(references []identitydomain.Reference) bool {
	if len(references) == 0 || len(references) > identityport.MaximumCommerceReferences {
		return false
	}
	for _, reference := range references {
		if reference.Assurance != identitydomain.AssuranceVerified {
			return false
		}
	}
	return true
}

func resolvedCustomer(result identityport.CommerceResolution) *int64 {
	if result.Status != identityport.CommerceResolved || result.CustomerID < 1 {
		return nil
	}
	value := int64(result.CustomerID)
	return &value
}

func attributionReason(payer, beneficiary identityport.CommerceResolveStatus) string {
	if payer == identityport.CommerceConflict || beneficiary == identityport.CommerceConflict {
		return "identity_multi_root_conflict"
	}
	if payer == identityport.CommerceInvalid || beneficiary == identityport.CommerceInvalid {
		return "identity_evidence_invalid"
	}
	if payer == identityport.CommercePartial || beneficiary == identityport.CommercePartial {
		return "identity_evidence_partial"
	}
	return "identity_not_found"
}
