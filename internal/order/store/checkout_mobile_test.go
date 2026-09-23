package store

import (
	"context"
	"encoding/base64"
	orderapp "github.com/qianlan33333-png/AI-CRM-v3/internal/order/app"
	ordersecure "github.com/qianlan33333-png/AI-CRM-v3/internal/order/secure"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"os"
	"strings"
	"testing"
	"time"
)

func TestPostgreSQLFrozenCheckoutMobileOwnerRead(t *testing.T) {
	pool, cleanup := orderIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	migration, err := os.ReadFile("../../../migrations/0061_product_public_purchase.sql")
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(migration), "CREATE TABLE order_contact_snapshots")
	if start < 0 {
		t.Fatal("missing owner schema")
	}
	if _, err = pool.Exec(ctx, string(migration[start:])); err != nil {
		t.Fatal(err)
	}
	wrapped, err := platformpostgres.Wrap(pool, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := NewPostgreSQL(pool, uow)
	if err != nil {
		t.Fatal(err)
	}
	service := orderapp.NewService(uow, repository)
	order, err := service.Create(ctx, nativeCommand("frozen-mobile"))
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := ordersecure.NewContactCipher(base64.RawStdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	if err = service.SetContactCipher(cipher); err != nil {
		t.Fatal(err)
	}
	ciphertext, err := cipher.Encrypt("+8613800000000")
	if err != nil {
		t.Fatal(err)
	}
	err = uow.Within(ctx, func(tx context.Context) error {
		if _, found, e := service.ReadCheckoutMobileWithin(tx, order.ID); e != nil || found {
			t.Fatal("missing data incorrect", e)
		}
		if e := repository.InsertContactSnapshot(tx, order.ID, ciphertext, 1, time.Now()); e != nil {
			return e
		}
		value, found, e := service.ReadCheckoutMobileWithin(tx, order.ID)
		if e != nil || !found || value != "+8613800000000" {
			t.Fatal("frozen owner read incorrect", e)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
