package app

import (
	"context"
	"errors"
	"testing"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	distributiondomain "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/domain"
	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
)

type adminNamesUOW struct{ active bool }

func (u *adminNamesUOW) Within(ctx context.Context, fn func(context.Context) error) error {
	u.active = true
	err := fn(ctx)
	u.active = false
	return err
}

type adminNamesStore struct {
	distributors      distributionport.AdminPage[distributionport.AdminDistributor]
	orders            distributionport.AdminPage[distributionport.AdminOrder]
	exceptions        distributionport.AdminPage[distributionport.AdminException]
	detail            distributionport.AdminDistributorDetail
	orderDistribution map[int64][]distributionport.OrderDistributionLine
}

func (adminNamesStore) Within(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

func (s adminNamesStore) EarningsByCustomer(context.Context, int64) (distributionport.Earnings, error) {
	return distributionport.Earnings{}, nil
}
func (s adminNamesStore) ListCommissionsByCustomer(context.Context, int64, distributiondomain.CommissionStatus, string, int32) (distributionport.CommissionPage, error) {
	return distributionport.CommissionPage{}, nil
}
func (s adminNamesStore) ListAdminDistributors(context.Context, string, int32) (distributionport.AdminPage[distributionport.AdminDistributor], error) {
	return s.distributors, nil
}
func (s adminNamesStore) ListAdminOrders(context.Context, string, int32) (distributionport.AdminPage[distributionport.AdminOrder], error) {
	return s.orders, nil
}
func (s adminNamesStore) ListAdminExceptions(context.Context, string, int32) (distributionport.AdminPage[distributionport.AdminException], error) {
	return s.exceptions, nil
}
func (s adminNamesStore) ReadAdminDistributorDetail(context.Context, int64) (distributionport.AdminDistributorDetail, error) {
	return s.detail, nil
}
func (s adminNamesStore) ListAdminOrdersByDistributor(context.Context, int64, string, int32) (distributionport.AdminPage[distributionport.AdminOrder], error) {
	return s.orders, nil
}
func (s adminNamesStore) ReadAdminOrderDetail(context.Context, int64) (distributionport.AdminOrderDetail, error) {
	return distributionport.AdminOrderDetail{Order: s.orders.Items[0]}, nil
}
func (s adminNamesStore) ReadAdminExceptionDetail(context.Context, int64) (distributionport.AdminException, error) {
	return s.exceptions.Items[0], nil
}
func (s adminNamesStore) ReadOrderDistribution(context.Context, []int64) (map[int64][]distributionport.OrderDistributionLine, error) {
	return s.orderDistribution, nil
}

type adminNamesDirectory struct {
	uow   *adminNamesUOW
	calls int
	ids   []customerdomain.CustomerID
	names map[customerdomain.CustomerID]string
	err   error
}

func (d *adminNamesDirectory) DisplayNames(_ context.Context, ids []customerdomain.CustomerID) (map[customerdomain.CustomerID]string, error) {
	if d.uow.active {
		return nil, errors.New("directory read ran inside distribution transaction")
	}
	d.calls++
	d.ids = append([]customerdomain.CustomerID(nil), ids...)
	return d.names, d.err
}

func TestAdminReadModelEnrichesNamesInOnePostTransactionBatch(t *testing.T) {
	uow := &adminNamesUOW{}
	store := adminNamesStore{
		distributors: distributionport.AdminPage[distributionport.AdminDistributor]{Items: []distributionport.AdminDistributor{{ID: 1, CustomerID: 11}, {ID: 2, CustomerID: 12}, {ID: 3, CustomerID: 11}}},
		orders:       distributionport.AdminPage[distributionport.AdminOrder]{Items: []distributionport.AdminOrder{{AttributionID: 4, DistributorCustomerID: 11}}},
		exceptions:   distributionport.AdminPage[distributionport.AdminException]{Items: []distributionport.AdminException{{ExceptionID: 5, DistributorCustomerID: 12}}},
		detail:       distributionport.AdminDistributorDetail{Distributor: distributionport.AdminDistributor{ID: 1, CustomerID: 11}},
	}
	directory := &adminNamesDirectory{uow: uow, names: map[customerdomain.CustomerID]string{11: "昵称 <b>原样作为数据</b>"}}
	service, err := NewReadModelService(uow, store)
	if err != nil {
		t.Fatal(err)
	}
	if err = service.SetDirectoryDisplayNameReader(directory); err != nil {
		t.Fatal(err)
	}
	page, err := service.ListAdminDistributors(context.Background(), "", 50)
	if err != nil {
		t.Fatal(err)
	}
	if directory.calls != 1 || len(directory.ids) != 2 || directory.ids[0] != 11 || directory.ids[1] != 12 {
		t.Fatalf("directory calls=%d ids=%v; want one deduplicated post-transaction batch", directory.calls, directory.ids)
	}
	if page.Items[0].DisplayName != "昵称 <b>原样作为数据</b>" || page.Items[1].DisplayName != "未设置昵称" || page.Items[2].DisplayName != "昵称 <b>原样作为数据</b>" {
		t.Fatalf("distributor display names=%+v", page.Items)
	}
	orders, err := service.ListAdminOrders(context.Background(), "", 50)
	if err != nil || orders.Items[0].DistributorDisplayName != "昵称 <b>原样作为数据</b>" {
		t.Fatalf("orders=%+v err=%v", orders, err)
	}
	exceptions, err := service.ListAdminExceptions(context.Background(), "", 50)
	if err != nil || exceptions.Items[0].DistributorDisplayName != "未设置昵称" {
		t.Fatalf("exceptions=%+v err=%v", exceptions, err)
	}
	detail, err := service.ReadAdminDistributorDetail(context.Background(), 1)
	if err != nil || detail.Distributor.DisplayName != "昵称 <b>原样作为数据</b>" {
		t.Fatalf("detail=%+v err=%v", detail, err)
	}
}

func TestAdminReadModelFailsClosedWhenDirectoryIsUnavailable(t *testing.T) {
	uow := &adminNamesUOW{}
	store := adminNamesStore{distributors: distributionport.AdminPage[distributionport.AdminDistributor]{Items: []distributionport.AdminDistributor{{ID: 1, CustomerID: 11}}}}
	service, err := NewReadModelService(uow, store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.ListAdminDistributors(context.Background(), "", 50); !errors.Is(err, distributionport.ErrUnavailable) {
		t.Fatalf("missing directory err=%v", err)
	}
	directory := &adminNamesDirectory{uow: uow, err: errors.New("customer projection unavailable")}
	if err = service.SetDirectoryDisplayNameReader(directory); err != nil {
		t.Fatal(err)
	}
	if _, err = service.ListAdminDistributors(context.Background(), "", 50); !errors.Is(err, distributionport.ErrUnavailable) || directory.calls != 1 {
		t.Fatalf("directory failure err=%v calls=%d", err, directory.calls)
	}
}

func TestOrderDistributionReadEnrichesCanonicalDisplayNamesAfterTransaction(t *testing.T) {
	uow := &adminNamesUOW{}
	store := adminNamesStore{orderDistribution: map[int64][]distributionport.OrderDistributionLine{7: {{OrderID: 7, DistributorCustomerID: 11}, {OrderID: 7, DistributorCustomerID: 12}}}}
	directory := &adminNamesDirectory{uow: uow, names: map[customerdomain.CustomerID]string{11: "分销员甲"}}
	service, err := NewReadModelService(uow, store)
	if err != nil {
		t.Fatal(err)
	}
	if err = service.SetDirectoryDisplayNameReader(directory); err != nil {
		t.Fatal(err)
	}
	values, err := service.ReadOrderDistribution(context.Background(), []int64{7})
	if err != nil || directory.calls != 1 || len(directory.ids) != 2 || values[7][0].DistributorDisplayName != "分销员甲" || values[7][1].DistributorDisplayName != "未设置昵称" {
		t.Fatalf("values=%+v err=%v calls=%d ids=%v", values, err, directory.calls, directory.ids)
	}
}
