package main

import (
	"context"
	"github.com/jackc/pgx/v5/pgxpool"
	ch "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/http"
	cp "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/port"
	cs "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/store"
	pg "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	pp "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type couponClaimUnusedRules struct{ cp.RuleApplication }
type couponClaimUnusedProducts struct{ pp.ProductOptionReader }

func (couponClaimUnusedProducts) ReadProductTargets(context.Context, []pp.ProductTargetReference) ([]pp.ProductTargetLookup, error) {
	return []pp.ProductTargetLookup{}, nil
}

func TestCouponClaimCompositionStartsReadTransaction(t *testing.T) {
	ctx := context.Background()
	dsn, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()
	native, e := pgxpool.New(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer native.Close()
	pool, e := pg.Wrap(native, time.Second)
	if e != nil {
		t.Fatal(e)
	}
	uow, e := pg.NewUnitOfWork(pool)
	if e != nil {
		t.Fatal(e)
	}
	repo, e := cs.NewPostgreSQL(native, uow)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = repo.ListCouponClaims(ctx, 1, 100, 0); e == nil {
		t.Fatal("fixture must exercise repository transaction requirement")
	}
	reader, e := composeCouponClaimAdmin(uow, repo)
	if e != nil {
		t.Fatal(e)
	}
	handler, e := ch.NewHandlerWithClaims(couponClaimUnusedRules{}, couponClaimUnusedProducts{}, reader, commerceFundsSecurity{})
	if e != nil {
		t.Fatal(e)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/coupons/1/claims", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("claims HTTP status=%d", response.Code)
	}
}
