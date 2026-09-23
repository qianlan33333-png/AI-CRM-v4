package port

import "context"

// AcquisitionAssetRequest contains only employee-side configuration. Customer
// external identifiers and callback secrets are deliberately absent.
type AcquisitionAssetRequest struct {
	Name, State  string
	SkipVerify   bool
	StaffUserIDs []string
}

type AcquisitionAssetResult struct{ ProviderAssetRef, URL string }

type AcquisitionAssetWriter interface {
	CreateContactWay(context.Context, AcquisitionAssetRequest) (AcquisitionAssetResult, error)
	GetContactWay(context.Context, string) (AcquisitionAssetResult, error)
	UpdateContactWay(context.Context, string, AcquisitionAssetRequest) (AcquisitionAssetResult, error)
	DeleteContactWay(context.Context, string) error
	CreateCustomerAcquisitionLink(context.Context, AcquisitionAssetRequest) (AcquisitionAssetResult, error)
}
