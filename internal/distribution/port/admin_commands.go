package port

import "context"

// AdminDistributorCommand is a staff-authorized command. The browser never
// supplies ActorScope: the HTTP adapter derives it from Access's principal.
type AdminDistributorCommand struct {
	DistributorID, ExpectedVersion     int64
	ActorScope, Reason, IdempotencyKey string
}

// AdminCommandService is the bounded administrative mutation surface. It is
// intentionally separate from the public distributor application and never
// accepts a payout result or payment receiver from a browser.
type AdminCommandService interface {
	SetDistributorEnabled(context.Context, AdminDistributorCommand, bool) error
	ReconcileException(context.Context, AdminExceptionCommand) error
	RecordRecovery(context.Context, AdminExceptionCommand) error
	RecordMerchantLiability(context.Context, AdminExceptionCommand) error
}
