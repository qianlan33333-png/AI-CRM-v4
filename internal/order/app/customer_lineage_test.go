package app

import (
	"context"
	"errors"
	"testing"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
)

type orderLineageContextKey struct{}

type orderLineageUOW struct{}

func (orderLineageUOW) Within(ctx context.Context, fn func(context.Context) error) error {
	return fn(context.WithValue(ctx, orderLineageContextKey{}, true))
}

type orderLineageReaderStub struct {
	ids []customerdomain.CustomerID
	err error
}

func (reader orderLineageReaderStub) LockedCanonicalLineage(ctx context.Context, _ customerdomain.CustomerID) ([]customerdomain.CustomerID, error) {
	if ctx.Value(orderLineageContextKey{}) != true {
		return nil, errors.New("lineage read outside caller transaction")
	}
	return reader.ids, reader.err
}

type orderLineageReadStore struct {
	*memoryStore
	calls int
	rows  []domain.Order
}

func (store *orderLineageReadStore) List(ctx context.Context, _ *Cursor, _ int32, _ ListFilter) ([]domain.Order, error) {
	store.calls++
	if ctx.Value(orderLineageContextKey{}) != true {
		return nil, errors.New("orders read outside caller transaction")
	}
	return store.rows, nil
}

func (store *orderLineageReadStore) Count(ctx context.Context, _ ListFilter) (int64, error) {
	store.calls++
	if ctx.Value(orderLineageContextKey{}) != true {
		return 0, errors.New("orders counted outside caller transaction")
	}
	return 0, nil
}

func TestCustomerOrderLineageFailureNeverReadsUnscopedOrders(t *testing.T) {
	for _, fixture := range []struct {
		name string
		ids  []customerdomain.CustomerID
		err  error
	}{
		{name: "resolver unavailable", err: errors.New("unavailable")},
		{name: "empty lineage"},
		{name: "unrelated roots", ids: []customerdomain.CustomerID{22, 33}},
		{name: "invalid member", ids: []customerdomain.CustomerID{11, 0}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			store := &orderLineageReadStore{memoryStore: newMemoryStore()}
			service := NewService(orderLineageUOW{}, store)
			if err := service.SetLockedCanonicalLineageReader(orderLineageReaderStub{ids: fixture.ids, err: fixture.err}); err != nil {
				t.Fatal(err)
			}
			if summary, err := service.CustomerOrderSummary(context.Background(), 11, 10); !errors.Is(err, orderport.ErrUnavailable) || summary.Total != 0 || len(summary.Recent) != 0 {
				t.Fatalf("failed lineage must return no summary: %+v error=%v", summary, err)
			}
			if page, err := service.CustomerActivities(context.Background(), orderport.CustomerActivityQuery{CustomerID: 11, Limit: 10, Watermark: time.Now()}); !errors.Is(err, orderport.ErrUnavailable) || len(page.Items) != 0 {
				t.Fatalf("failed lineage must return no activities: %+v error=%v", page, err)
			}
			if store.calls != 0 {
				t.Fatalf("failed lineage executed %d order queries", store.calls)
			}
		})
	}
}

func TestCustomerActivitiesLineageKeepsPartyRolesAndRejectsUnrelatedRows(t *testing.T) {
	for _, fixture := range []struct {
		name               string
		payer, beneficiary int64
		want               string
	}{
		{"historical payer", 22, 99, "payer"},
		{"historical beneficiary", 99, 22, "beneficiary"},
		{"both roots", 22, 11, "payer_and_beneficiary"},
		{"unrelated", 99, 99, ""},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			input := orderInput("lineage-" + fixture.name)
			input.CreatedAt = time.Now()
			order, err := domain.NewOrder(input)
			if err != nil {
				t.Fatal(err)
			}
			snapshot := order.Snapshot()
			snapshot.ID = 1
			snapshot.PayerCustomerID, snapshot.BeneficiaryCustomerID = &fixture.payer, &fixture.beneficiary
			order, err = domain.Restore(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			store := &orderLineageReadStore{memoryStore: newMemoryStore(), rows: []domain.Order{order}}
			service := NewService(orderLineageUOW{}, store)
			if err = service.SetLockedCanonicalLineageReader(orderLineageReaderStub{ids: []customerdomain.CustomerID{11, 22}}); err != nil {
				t.Fatal(err)
			}
			page, err := service.CustomerActivities(context.Background(), orderport.CustomerActivityQuery{CustomerID: 11, Limit: 10, Watermark: time.Now().Add(time.Second)})
			if fixture.want == "" {
				if !errors.Is(err, orderport.ErrUnavailable) || len(page.Items) != 0 {
					t.Fatalf("store scope failure leaked order: %+v error=%v", page, err)
				}
			} else if err != nil || len(page.Items) != 1 || page.Items[0].Relationship != fixture.want {
				t.Fatalf("relationship=%+v want=%s error=%v", page, fixture.want, err)
			}
		})
	}
}
