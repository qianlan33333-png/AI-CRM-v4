package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	distributiondomain "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/domain"
	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func TestPostgreSQLCommissionListReadsOnlyMatchingLatestSettlementConfirmation(t *testing.T) {
	pool, cleanup := settlementWarningPool(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 9, 15, 10, 0, 0, 0, time.UTC)
	owner := seedAdminDetailDistributor(t, ctx, pool, 202, "DSTPAID202", now)
	other := seedAdminDetailDistributor(t, ctx, pool, 203, "DSTPAID203", now)
	policy := seedAdminDetailPolicy(t, ctx, pool, now)
	ownerCredential := seedAdminDetailCredential(t, ctx, pool, owner, now)
	otherCredential := seedAdminDetailCredential(t, ctx, pool, other, now)

	matched := commissionForAttribution(t, ctx, pool, seedAdminDetailAttribution(t, ctx, pool, owner, ownerCredential, policy, 9501, "matched-confirmation", now))
	withoutAudit := commissionForAttribution(t, ctx, pool, seedAdminDetailAttribution(t, ctx, pool, owner, ownerCredential, policy, 9502, "missing-confirmation", now.Add(time.Minute)))
	zeroPaid := commissionForAttribution(t, ctx, pool, seedAdminDetailAttribution(t, ctx, pool, owner, ownerCredential, policy, 9504, "zero-paid", now.Add(90*time.Second)))
	otherCustomer := commissionForAttribution(t, ctx, pool, seedAdminDetailAttribution(t, ctx, pool, other, otherCredential, policy, 9503, "other-customer", now.Add(2*time.Minute)))
	for _, commissionID := range []int64{matched, withoutAudit, otherCustomer} {
		if _, err := pool.Exec(ctx, `UPDATE distribution_commissions SET status='paid',paid_minor=100,version=2,updated_at=$2 WHERE id=$1`, commissionID, now.Add(3*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE distribution_commissions SET status='paid',paid_minor=0,version=2,updated_at=$2 WHERE id=$1`, zeroPaid, now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}

	firstConfirmation := now.Add(4 * time.Minute)
	latestConfirmation := now.Add(5 * time.Minute)
	wrongReferenceConfirmation := now.Add(6 * time.Minute)
	insertSettlement(t, ctx, pool, matched, "settlement-matched-first", 50, now)
	insertSettlement(t, ctx, pool, matched, "settlement-matched-latest", 50, now.Add(time.Minute))
	insertSettlement(t, ctx, pool, withoutAudit, "settlement-without-audit", 100, now.Add(2*time.Minute))
	insertSettlement(t, ctx, pool, zeroPaid, "settlement-zero-paid", 100, now.Add(2*time.Minute))
	insertSettlement(t, ctx, pool, otherCustomer, "settlement-other-customer", 100, now.Add(3*time.Minute))
	insertSettlementAudit(t, ctx, pool, matched, "settlement-matched-first", firstConfirmation)
	insertSettlementAudit(t, ctx, pool, matched, "settlement-matched-latest", latestConfirmation)
	// The later audit is for the same commission but does not name one of its
	// settlement records, so it cannot become a public confirmation time.
	insertSettlementAudit(t, ctx, pool, matched, "settlement-wrong-reference", wrongReferenceConfirmation)
	insertSettlementAudit(t, ctx, pool, zeroPaid, "settlement-zero-paid", wrongReferenceConfirmation)
	insertSettlementAudit(t, ctx, pool, otherCustomer, "settlement-other-customer", wrongReferenceConfirmation.Add(time.Minute))

	wrapped, err := platformpostgres.Wrap(pool, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := NewPostgreSQL(pool, uow)
	if err != nil {
		t.Fatal(err)
	}
	var pageOrderReferences = make(map[string]distributionport.CommissionListItem)
	err = uow.Within(ctx, func(tx context.Context) error {
		page, readErr := repository.ListCommissionsByCustomer(tx, 202, distributiondomain.CommissionStatus(""), "", 50)
		if readErr != nil {
			return readErr
		}
		if page.NextCursor != "" || len(page.Items) != 3 {
			return fmt.Errorf("owner page cursor=%q count=%d", page.NextCursor, len(page.Items))
		}
		for _, item := range page.Items {
			pageOrderReferences[item.OrderReference] = item
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := pageOrderReferences["order-9501"]; !got.SettlementConfirmedAt.Equal(latestConfirmation) || !got.PaidAt.Equal(latestConfirmation) {
		t.Fatalf("matching confirmation=%s alias=%s want latest matching audit %s", got.SettlementConfirmedAt, got.PaidAt, latestConfirmation)
	}
	if got := pageOrderReferences["order-9502"]; !got.SettlementConfirmedAt.IsZero() || !got.PaidAt.IsZero() {
		t.Fatalf("paid commission without matching audit confirmation=%s alias=%s", got.SettlementConfirmedAt, got.PaidAt)
	}
	if got := pageOrderReferences["order-9504"]; !got.SettlementConfirmedAt.IsZero() || !got.PaidAt.IsZero() {
		t.Fatalf("zero-paid commission must not expose confirmation=%s alias=%s", got.SettlementConfirmedAt, got.PaidAt)
	}
	if _, leaked := pageOrderReferences["order-9503"]; leaked {
		t.Fatalf("other customer commission leaked into public customer page: %+v", pageOrderReferences)
	}
}

func commissionForAttribution(t *testing.T, ctx context.Context, pool *pgxpool.Pool, attributionID int64) int64 {
	t.Helper()
	var commissionID int64
	if err := pool.QueryRow(ctx, `SELECT id FROM distribution_commissions WHERE attribution_id=$1`, attributionID).Scan(&commissionID); err != nil {
		t.Fatal(err)
	}
	return commissionID
}

func insertSettlement(t *testing.T, ctx context.Context, pool *pgxpool.Pool, commissionID int64, reference string, amount int64, now time.Time) {
	t.Helper()
	if _, err := pool.Exec(ctx, `INSERT INTO distribution_settlements(commission_id,settlement_reference,amount_minor,currency,original_payment_reference,state,provider_deadline_at,version,created_at,updated_at) VALUES($1,$2,$3,'CNY',$4,'receiver_succeeded',NULL,1,$5,$5)`, commissionID, reference, amount, "payment:"+reference, now); err != nil {
		t.Fatal(err)
	}
}

func insertSettlementAudit(t *testing.T, ctx context.Context, pool *pgxpool.Pool, commissionID int64, reference string, occurredAt time.Time) {
	t.Helper()
	if _, err := pool.Exec(ctx, `INSERT INTO distribution_audit_events(event_type,aggregate_type,aggregate_id,actor_scope,payload,occurred_at) VALUES('distribution.settlement_paid.v1','commission',$1,'worker:distribution-due',jsonb_build_object('settlement_reference',$2::text),$3)`, commissionID, reference, occurredAt); err != nil {
		t.Fatal(err)
	}
}
