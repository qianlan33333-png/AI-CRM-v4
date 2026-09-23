package port

import "context"

// SubmissionObserver accepts durable follow-up work inside the submission UoW.
// It must not call a model or any external provider while this transaction is open.
type SubmissionObserver interface {
	SubmissionCreatedWithin(context.Context, int64, int64) error
}
