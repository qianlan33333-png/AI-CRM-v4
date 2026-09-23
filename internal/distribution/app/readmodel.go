package app

import (
	"context"
	"strings"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	distributiondomain "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/domain"
	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

type readModelStore interface {
	Within(context.Context, func(context.Context) error) error
	EarningsByCustomer(context.Context, int64) (distributionport.Earnings, error)
	ListCommissionsByCustomer(context.Context, int64, distributiondomain.CommissionStatus, string, int32) (distributionport.CommissionPage, error)
	ListAdminDistributors(context.Context, string, int32) (distributionport.AdminPage[distributionport.AdminDistributor], error)
	ListAdminOrders(context.Context, string, int32) (distributionport.AdminPage[distributionport.AdminOrder], error)
	ListAdminExceptions(context.Context, string, int32) (distributionport.AdminPage[distributionport.AdminException], error)
	ReadAdminDistributorDetail(context.Context, int64) (distributionport.AdminDistributorDetail, error)
	ListAdminOrdersByDistributor(context.Context, int64, string, int32) (distributionport.AdminPage[distributionport.AdminOrder], error)
	ReadAdminOrderDetail(context.Context, int64) (distributionport.AdminOrderDetail, error)
	ReadAdminExceptionDetail(context.Context, int64) (distributionport.AdminException, error)
}
type ReadModelService struct {
	uow       platformport.UnitOfWork
	store     readModelStore
	directory customerport.DirectoryDisplayNameReader
}

func NewReadModelService(uow platformport.UnitOfWork, store readModelStore) (*ReadModelService, error) {
	if uow == nil || store == nil {
		return nil, distributionport.ErrUnavailable
	}
	return &ReadModelService{uow: uow, store: store}, nil
}

// SetDirectoryDisplayNameReader injects Customer's presentation-safe, batch
// projection. It is deliberately invoked after the Distribution store read is
// complete: this avoids crossing domains inside Distribution's PostgreSQL
// Unit of Work or creating a nested transaction.
func (s *ReadModelService) SetDirectoryDisplayNameReader(reader customerport.DirectoryDisplayNameReader) error {
	if s == nil || reader == nil {
		return distributionport.ErrUnavailable
	}
	s.directory = reader
	return nil
}
func (s *ReadModelService) Earnings(c context.Context, a distributionport.TrustedSessionActor) (v distributionport.Earnings, e error) {
	if s == nil || !a.Valid() {
		return v, distributionport.ErrUnauthorized
	}
	e = s.uow.Within(c, func(t context.Context) error { v, e = s.store.EarningsByCustomer(t, a.CustomerID); return e })
	return
}
func (s *ReadModelService) ListCommissions(c context.Context, a distributionport.TrustedSessionActor, st distributiondomain.CommissionStatus, cur string, l int32) (v distributionport.CommissionPage, e error) {
	if s == nil || !a.Valid() {
		return v, distributionport.ErrUnauthorized
	}
	e = s.uow.Within(c, func(t context.Context) error {
		v, e = s.store.ListCommissionsByCustomer(t, a.CustomerID, st, cur, l)
		return e
	})
	return
}
func (s *ReadModelService) ListAdminDistributors(c context.Context, x string, l int32) (v distributionport.AdminPage[distributionport.AdminDistributor], e error) {
	e = s.uow.Within(c, func(t context.Context) error { v, e = s.store.ListAdminDistributors(t, x, l); return e })
	if e != nil {
		return v, e
	}
	e = s.enrichAdminDistributors(c, v.Items)
	return
}
func (s *ReadModelService) ListAdminOrders(c context.Context, x string, l int32) (v distributionport.AdminPage[distributionport.AdminOrder], e error) {
	e = s.uow.Within(c, func(t context.Context) error { v, e = s.store.ListAdminOrders(t, x, l); return e })
	if e != nil {
		return v, e
	}
	e = s.enrichAdminOrders(c, v.Items)
	return
}
func (s *ReadModelService) ListAdminExceptions(c context.Context, x string, l int32) (v distributionport.AdminPage[distributionport.AdminException], e error) {
	e = s.uow.Within(c, func(t context.Context) error { v, e = s.store.ListAdminExceptions(t, x, l); return e })
	if e != nil {
		return v, e
	}
	e = s.enrichAdminExceptions(c, v.Items)
	return
}
func (s *ReadModelService) ReadAdminDistributorDetail(c context.Context, id int64) (v distributionport.AdminDistributorDetail, e error) {
	if id < 1 {
		return v, distributionport.ErrNotFound
	}
	e = s.uow.Within(c, func(t context.Context) error { v, e = s.store.ReadAdminDistributorDetail(t, id); return e })
	if e != nil {
		return v, e
	}
	// Read Customer's projection only after the Distribution transaction is
	// complete. The presentation-only display name is not persisted here.
	page := []distributionport.AdminDistributor{v.Distributor}
	e = s.enrichAdminDistributors(c, page)
	if e == nil {
		v.Distributor = page[0]
	}
	return
}
func (s *ReadModelService) ListAdminOrdersByDistributor(c context.Context, id int64, x string, l int32) (v distributionport.AdminPage[distributionport.AdminOrder], e error) {
	if id < 1 {
		return v, distributionport.ErrNotFound
	}
	e = s.uow.Within(c, func(t context.Context) error { v, e = s.store.ListAdminOrdersByDistributor(t, id, x, l); return e })
	if e != nil {
		return v, e
	}
	e = s.enrichAdminOrders(c, v.Items)
	return
}
func (s *ReadModelService) ReadAdminOrderDetail(c context.Context, id int64) (v distributionport.AdminOrderDetail, e error) {
	if id < 1 {
		return v, distributionport.ErrNotFound
	}
	e = s.uow.Within(c, func(t context.Context) error { v, e = s.store.ReadAdminOrderDetail(t, id); return e })
	if e != nil {
		return v, e
	}
	page := []distributionport.AdminOrder{v.Order}
	e = s.enrichAdminOrders(c, page)
	if e == nil {
		v.Order = page[0]
	}
	return
}

// orderDistributionStore is deliberately optional so older public/admin read
// model fakes and callers do not acquire Order data access. Repository is the
// only production implementation and still owns its Distribution tables.
type orderDistributionStore interface {
	ReadOrderDistribution(context.Context, []int64) (map[int64][]distributionport.OrderDistributionLine, error)
}

// ReadOrderDistribution supplies Order with a bounded, batch-only snapshot.
// Customer display names are resolved after Distribution's transaction, as in
// the admin read model, and no Order table is read here.
func (s *ReadModelService) ReadOrderDistribution(c context.Context, ids []int64) (v map[int64][]distributionport.OrderDistributionLine, e error) {
	if len(ids) == 0 {
		return map[int64][]distributionport.OrderDistributionLine{}, nil
	}
	store, ok := s.store.(orderDistributionStore)
	if !ok {
		return nil, distributionport.ErrUnavailable
	}
	v = make(map[int64][]distributionport.OrderDistributionLine)
	e = s.uow.Within(c, func(t context.Context) error {
		v, e = store.ReadOrderDistribution(t, ids)
		return e
	})
	if e != nil {
		return nil, e
	}
	customerIDs := make([]customerdomain.CustomerID, 0)
	for _, lines := range v {
		for _, line := range lines {
			customerIDs = append(customerIDs, line.DistributorCustomerID)
		}
	}
	if len(customerIDs) == 0 {
		return v, nil
	}
	names, e := s.adminDisplayNames(c, uniqueAdminCustomerIDs(customerIDs))
	if e != nil {
		return nil, e
	}
	for orderID, lines := range v {
		for i := range lines {
			lines[i].DistributorDisplayName = adminDisplayName(names, lines[i].DistributorCustomerID)
		}
		v[orderID] = lines
	}
	return v, nil
}

func (s *ReadModelService) ReadAdminExceptionDetail(c context.Context, id int64) (v distributionport.AdminException, e error) {
	if id < 1 {
		return v, distributionport.ErrNotFound
	}
	e = s.uow.Within(c, func(t context.Context) error { v, e = s.store.ReadAdminExceptionDetail(t, id); return e })
	if e != nil {
		return v, e
	}
	page := []distributionport.AdminException{v}
	e = s.enrichAdminExceptions(c, page)
	if e == nil {
		v = page[0]
	}
	return
}

func (s *ReadModelService) adminDisplayNames(c context.Context, ids []customerdomain.CustomerID) (map[customerdomain.CustomerID]string, error) {
	if s == nil || s.directory == nil {
		return nil, distributionport.ErrUnavailable
	}
	names, err := s.directory.DisplayNames(c, ids)
	if err != nil {
		return nil, distributionport.ErrUnavailable
	}
	return names, nil
}

func adminDisplayName(names map[customerdomain.CustomerID]string, id customerdomain.CustomerID) string {
	if name := strings.TrimSpace(names[id]); name != "" {
		return name
	}
	return "未设置昵称"
}

func (s *ReadModelService) enrichAdminDistributors(c context.Context, values []distributionport.AdminDistributor) error {
	if len(values) == 0 {
		return nil
	}
	ids := make([]customerdomain.CustomerID, 0, len(values))
	for _, value := range values {
		ids = append(ids, value.CustomerID)
	}
	names, err := s.adminDisplayNames(c, uniqueAdminCustomerIDs(ids))
	if err != nil {
		return err
	}
	for index := range values {
		values[index].DisplayName = adminDisplayName(names, values[index].CustomerID)
	}
	return nil
}

func (s *ReadModelService) enrichAdminOrders(c context.Context, values []distributionport.AdminOrder) error {
	if len(values) == 0 {
		return nil
	}
	ids := make([]customerdomain.CustomerID, 0, len(values))
	for _, value := range values {
		ids = append(ids, value.DistributorCustomerID)
	}
	names, err := s.adminDisplayNames(c, uniqueAdminCustomerIDs(ids))
	if err != nil {
		return err
	}
	for index := range values {
		values[index].DistributorDisplayName = adminDisplayName(names, values[index].DistributorCustomerID)
	}
	return nil
}

func (s *ReadModelService) enrichAdminExceptions(c context.Context, values []distributionport.AdminException) error {
	if len(values) == 0 {
		return nil
	}
	ids := make([]customerdomain.CustomerID, 0, len(values))
	for _, value := range values {
		ids = append(ids, value.DistributorCustomerID)
	}
	names, err := s.adminDisplayNames(c, uniqueAdminCustomerIDs(ids))
	if err != nil {
		return err
	}
	for index := range values {
		values[index].DistributorDisplayName = adminDisplayName(names, values[index].DistributorCustomerID)
	}
	return nil
}

func uniqueAdminCustomerIDs(values []customerdomain.CustomerID) []customerdomain.CustomerID {
	result := make([]customerdomain.CustomerID, 0, len(values))
	seen := make(map[customerdomain.CustomerID]struct{}, len(values))
	for _, value := range values {
		if value < 1 {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
