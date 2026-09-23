package port

import "context"

type CustomerAcquisitionLinkInput struct {
	LinkName      string
	UserIDs       []string
	DepartmentIDs []int64
	SkipVerify    bool
}
type CustomerAcquisitionLink struct {
	LinkID, LinkName, URL string
	UserIDs               []string
	DepartmentIDs         []int64
	SkipVerify            bool
}
type CustomerAcquisitionLinkPage struct {
	Links      []CustomerAcquisitionLink
	NextCursor string
}
type CustomerAcquisitionLinkProvider interface {
	ListManagedAcquisitionLinks(context.Context, string, int) (CustomerAcquisitionLinkPage, error)
	GetManagedAcquisitionLink(context.Context, string) (CustomerAcquisitionLink, error)
	CreateManagedAcquisitionLink(context.Context, CustomerAcquisitionLinkInput) (CustomerAcquisitionLink, error)
	UpdateManagedAcquisitionLink(context.Context, string, CustomerAcquisitionLinkInput) (CustomerAcquisitionLink, error)
	DeleteManagedAcquisitionLink(context.Context, string) error
}
