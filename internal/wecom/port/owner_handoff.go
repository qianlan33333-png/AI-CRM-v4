package port

import (
	"context"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
)

type OwnerHandoffRelationship struct {
	CustomerID     customerdomain.CustomerID
	CorpScope      string
	EmployeeUserID string
	Active         bool
	VersionDigest  [32]byte
}

type OwnerHandoffRelationshipReader interface {
	OwnerHandoffRelationship(context.Context, customerdomain.CustomerID, string, string) (OwnerHandoffRelationship, error)
}

type CustomerTransferWriter interface {
	TransferCustomer(context.Context, string, string, []string, string) (CustomerTransferResult, error)
	TransferResult(context.Context, string, string, string) (CustomerTransferResult, error)
}

type CustomerTransferObservation struct {
	ExternalUserID string
	// Status is the documented transfer-result state: 1 completed, 2 pending,
	// 3 customer refused, 4 target limit, 5 no transfer record.
	Status       int
	TakeoverTime int64
}

type CustomerTransferResult struct {
	// AcceptedExternalUserIDs applies only to transfer_customer: it means the
	// provider accepted an individual request, not that transfer completed.
	AcceptedExternalUserIDs []string
	// RejectedExternalUserIDs is the exact provider-reported counterpart of
	// AcceptedExternalUserIDs. A count without a row identity is insufficient
	// to decide a local CRM owner update for a batched transfer.
	RejectedExternalUserIDs []string
	FailedCount             int
	Cursor                  string
	Observations            []CustomerTransferObservation
}

type OwnerHandoffRelationshipLister interface {
	ListOwnerHandoffCustomerIDs(context.Context, string, string, int) ([]customerdomain.CustomerID, error)
}
