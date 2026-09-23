package adapter

import (
	"context"
	"github.com/jackc/pgx/v5/pgxpool"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"testing"
	"time"
)

type transactionCanonical struct{ transaction any }

func (s *transactionCanonical) ResolveCanonicalCustomer(ctx context.Context, id customerdomain.CustomerID) (customerport.CanonicalCustomer, error) {
	tx, e := platformpostgres.RequireTransaction(ctx)
	if e != nil {
		return customerport.CanonicalCustomer{}, e
	}
	s.transaction = tx
	return customerport.CanonicalCustomer{RequestedCustomerID: id, CustomerID: id}, nil
}
func TestPostgreSQLCanonicalReadReusesCompletionTransaction(t *testing.T) {
	url, e := platformconfig.DatabaseURL()
	if e != nil {
		t.Skip("database not configured")
	}
	ctx := context.Background()
	native, e := pgxpool.New(ctx, url)
	if e != nil {
		t.Fatal(e)
	}
	defer native.Close()
	pool, e := platformpostgres.Wrap(native, time.Second)
	if e != nil {
		t.Fatal(e)
	}
	uow, e := platformpostgres.NewUnitOfWork(pool)
	if e != nil {
		t.Fatal(e)
	}
	resolver := &transactionCanonical{}
	adapter := CanonicalCustomers{UoW: uow, Resolver: resolver}
	e = uow.Within(ctx, func(txctx context.Context) error {
		tx, e := platformpostgres.RequireTransaction(txctx)
		if e != nil {
			return e
		}
		ids, e := adapter.CanonicalCustomers(txctx, []customerdomain.CustomerID{7})
		if e != nil {
			return e
		}
		if len(ids) != 1 || ids[0] != 7 || resolver.transaction != tx {
			t.Fatal("canonical read escaped completion transaction")
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = adapter.CanonicalCustomers(ctx, []customerdomain.CustomerID{7}); e != nil {
		t.Fatal(e)
	}
}
