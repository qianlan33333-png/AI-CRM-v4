package port

import "context"

// PhoneRecoveryResult exposes safe dispositions, never a phone or merge rule.
// Only a newly recovered result may update a Customer projection. Original
// receipts and their immutable source customer are never rewritten.
type PhoneRecoveryResult struct {
	Disposition string
	Binding     DeclaredAttachResult
}

type DeclaredPhoneCanonicalRecovery interface {
	InspectDeclaredPhoneRecovery(context.Context, DeclaredPhoneCommand) (PhoneRecoveryResult, error)
	RecoverDeclaredPhoneToCanonicalCustomer(context.Context, DeclaredPhoneCommand) (PhoneRecoveryResult, error)
}
