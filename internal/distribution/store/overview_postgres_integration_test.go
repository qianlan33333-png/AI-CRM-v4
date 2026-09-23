package store

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func TestPostgreSQLOverviewSeparatesPeriodPerformanceFromCurrentSettlement(t *testing.T) {
	pool, cleanup := settlementWarningPool(t)
	defer cleanup()
	ctx := context.Background()
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 0, 7)

	var distributorID, policyID, credentialID int64
	if err := pool.QueryRow(ctx, `INSERT INTO distribution_distributors(customer_id,public_no,agreement_version,enabled,registered_at,version,created_at,updated_at) VALUES(901,'DSTOVERVIEW901','v1',true,$1,1,$1,$1) RETURNING id`, start).Scan(&distributorID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO distribution_product_policies(product_id,product_type,enabled,commission_rate_basis_points,wait_days,version,created_at,updated_at) VALUES(1901,'standard_product',true,1000,7,1,$1,$1) RETURNING id`, start).Scan(&policyID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO distribution_promotion_credentials(distributor_id,product_id,product_type,token_digest,status,created_at,expires_at) VALUES($1,1901,'standard_product',$2,'active',$3,$4) RETURNING id`, distributorID, make([]byte, 32), start, end.AddDate(0, 0, 30)).Scan(&credentialID); err != nil {
		t.Fatal(err)
	}

	inWindow := seedOverviewCommission(t, ctx, pool, distributorID, policyID, credentialID, 9011, 1, start.Add(time.Hour), 1000, 100, 100, 0, "pending")
	outOfWindow := seedOverviewCommission(t, ctx, pool, distributorID, policyID, credentialID, 9012, 1, end, 2000, 200, 200, 0, "pending")
	// Two commissions and three actionable records belong to order 9011. The
	// admin summary must show one actionable order while the legacy operating
	// to-do still reports all three actionable exception records.
	inWindowSecondCommission := seedOverviewCommission(t, ctx, pool, distributorID, policyID, credentialID, 9011, 2, start.Add(2*time.Hour), 700, 70, 70, 0, "pending")
	paid := seedOverviewCommission(t, ctx, pool, distributorID, policyID, credentialID, 9013, 1, start.AddDate(0, 0, -1), 500, 50, 50, 50, "paid")
	for _, fixture := range []struct {
		commissionID int64
		status       string
	}{
		{inWindow, "open"},
		{inWindow, "querying"},
		{inWindowSecondCommission, "open"},
		{outOfWindow, "querying"},
		{paid, "resolved"},
	} {
		if _, err := pool.Exec(ctx, `INSERT INTO distribution_exceptions(commission_id,kind,status,unpaid_due_minor,already_paid_minor,amount_minor,reason,evidence_reference,actor_scope,version,created_at,updated_at) VALUES($1,'settlement_unknown',$2,0,0,0,'provider_result_pending','proof','admin:1',1,$3,$3)`, fixture.commissionID, fixture.status, start); err != nil {
			t.Fatal(err)
		}
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
	var got struct {
		periodSales, periodCommission, periodCount, unsettled, settled, exceptionOrders, exceptions int64
	}
	err = uow.Within(ctx, func(tx context.Context) error {
		result, readErr := repository.ReadOverview(tx, distributionport.OverviewWindow{Start: start, End: end})
		if readErr != nil {
			return readErr
		}
		got.periodSales = result.PeriodPaidSalesMinor
		got.periodCommission = result.PeriodInitialCommission
		got.periodCount = result.PeriodCommissionCount
		got.unsettled = result.CurrentUnsettledMinor
		got.settled = result.CurrentSettledMinor
		got.exceptionOrders = result.CurrentExceptionOrderCount
		got.exceptions = result.OpenExceptionCount
		if result.Currency != "CNY" {
			t.Fatalf("currency=%q", result.Currency)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.periodSales != 1700 || got.periodCommission != 170 || got.periodCount != 2 {
		t.Fatalf("period={sales:%d commission:%d count:%d}", got.periodSales, got.periodCommission, got.periodCount)
	}
	if got.unsettled != 370 || got.settled != 50 || got.exceptionOrders != 2 || got.exceptions != 4 {
		t.Fatalf("current={unsettled:%d settled:%d exception_orders:%d exceptions:%d}", got.unsettled, got.settled, got.exceptionOrders, got.exceptions)
	}
}

func seedOverviewCommission(t *testing.T, ctx context.Context, pool *pgxpool.Pool, distributorID, policyID, credentialID, orderID int64, line int, paidAt time.Time, original, initial, current, paid int64, status string) int64 {
	t.Helper()
	var attributionID, commissionID int64
	if err := pool.QueryRow(ctx, `INSERT INTO distribution_order_attributions(order_id,order_item_line,product_code,product_name,distributor_id,promotion_credential_id,qualification_evidence_reference,qualification_state,policy_id,policy_version,commission_rate_basis_points,wait_days,attributed_at) VALUES($1,$2,'overview-product','Overview product',$3,$4,'payment:confirmed','eligible',$5,1,1000,7,$6) RETURNING id`, orderID, line, distributorID, credentialID, policyID, paidAt).Scan(&attributionID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO distribution_commissions(attribution_id,order_id,order_item_line,distributor_id,original_item_paid_minor,successful_refund_minor,initial_minor,current_payable_minor,paid_minor,commission_rate_basis_points,paid_confirmed_at,due_at,status,hold_reason,cancel_reason,exception_reason,version,created_at,updated_at) VALUES($1,$2,$3,$4,$5,0,$6,$7,$8,1000,$9,$10,$11,'','','',1,$9,$9) RETURNING id`, attributionID, orderID, line, distributorID, original, initial, current, paid, paidAt, paidAt.AddDate(0, 0, 7), status).Scan(&commissionID); err != nil {
		t.Fatal(err)
	}
	return commissionID
}
