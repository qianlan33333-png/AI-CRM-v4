package port

import (
	"context"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
)

type DirectPushEligibility struct {
	SnapshotID       int64
	SenderSetVersion int64
}

type DirectPushEligibilityReader interface {
	DirectPushEligibility(context.Context, PackageID, customerdomain.CustomerID, int64) (DirectPushEligibility, string, error)
	DirectPushPackageExists(context.Context, PackageID) (bool, error)
}
