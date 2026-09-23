package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// TestPostgreSQLAdminDistributorOrderDetailPagination proves the drawer's
// related-order list is scoped and paged by the database. The browser must
// never fetch a global page and filter it locally, or lose matching rows that
// land on later pages.
func TestPostgreSQLAdminDistributorOrderDetailPagination(t *testing.T) {
	pool, cleanup := settlementWarningPool(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 9, 14, 10, 30, 0, 0, time.UTC)
	owner := seedAdminDetailDistributor(t, ctx, pool, 301, "DSTDETAIL301", now)
	other := seedAdminDetailDistributor(t, ctx, pool, 302, "DSTDETAIL302", now)
	policy := seedAdminDetailPolicy(t, ctx, pool, now)
	credential := seedAdminDetailCredential(t, ctx, pool, owner, now)
	otherCredential := seedAdminDetailCredential(t, ctx, pool, other, now)

	ownerIDs := make([]int64, 0, 11)
	for offset := int64(0); offset < 11; offset++ {
		ownerIDs = append(ownerIDs, seedAdminDetailAttribution(t, ctx, pool, owner, credential, policy, 9101+offset, fmt.Sprintf("owner-%02d", offset+1), now.Add(time.Duration(offset)*time.Minute)))
	}
	_ = seedAdminDetailAttribution(t, ctx, pool, other, otherCredential, policy, 9199, "other-distributor", now.Add(3*time.Minute))
	// A partial buyer refund is an append-only adjustment against a commission;
	// the following zero-commission order is still a real attributed sale fact.
	// This makes the staff summary prove both cases without filtering either out.
	updateAdminDetailCommission(t, ctx, pool, ownerIDs[9], 500, 100, 50, "pending")
	if _, err := pool.Exec(ctx, `INSERT INTO distribution_commission_adjustments(commission_id,kind,delta_minor,resulting_payable_minor,reason,source_reference,occurred_at) SELECT id,'buyer_refund',-50,50,'buyer_refund','refund:9110',$2 FROM distribution_commissions WHERE attribution_id=$1`, ownerIDs[9], now.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	updateAdminDetailCommission(t, ctx, pool, ownerIDs[10], 0, 0, 0, "zero_commission")

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

	var first, secondIDs []int64
	var cursor string
	var earnings distributionport.Earnings
	var distributorPage distributionport.AdminPage[distributionport.AdminDistributor]
	var globalOrderPage distributionport.AdminPage[distributionport.AdminOrder]
	err = uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		distributorPage, readErr = repository.ListAdminDistributors(tx, "", 10)
		if readErr != nil {
			return readErr
		}
		globalOrderPage, readErr = repository.ListAdminOrders(tx, "", 50)
		if readErr != nil {
			return readErr
		}
		page, readErr := repository.ListAdminOrdersByDistributor(tx, owner, "", 10)
		if readErr != nil {
			return readErr
		}
		for _, item := range page.Items {
			first = append(first, item.AttributionID)
			if item.DistributorPublicNo != "DSTDETAIL301" || item.DistributorCustomerID != 301 {
				return fmt.Errorf("wrong distributor projection public=%q customer=%d", item.DistributorPublicNo, item.DistributorCustomerID)
			}
		}
		cursor = page.NextCursor
		page, readErr = repository.ListAdminOrdersByDistributor(tx, owner, cursor, 10)
		if readErr != nil {
			return readErr
		}
		for _, item := range page.Items {
			secondIDs = append(secondIDs, item.AttributionID)
			if item.DistributorPublicNo != "DSTDETAIL301" || item.DistributorCustomerID != 301 {
				return fmt.Errorf("wrong distributor projection public=%q customer=%d", item.DistributorPublicNo, item.DistributorCustomerID)
			}
		}
		if page.NextCursor != "" {
			return fmt.Errorf("unexpected terminal cursor %q", page.NextCursor)
		}
		detail, readErr := repository.ReadAdminDistributorDetail(tx, owner)
		if readErr != nil {
			return readErr
		}
		if detail.Distributor.CustomerID != 301 {
			return fmt.Errorf("detail customer projection=%d", detail.Distributor.CustomerID)
		}
		earnings = detail.Earnings
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(distributorPage.Items) != 2 || distributorPage.Items[0].CustomerID != 301 || distributorPage.Items[1].CustomerID != 302 {
		t.Fatalf("admin distributor customer projections=%+v", distributorPage.Items)
	}
	if len(globalOrderPage.Items) != 12 {
		t.Fatalf("global admin order page count=%d", len(globalOrderPage.Items))
	}
	for _, item := range globalOrderPage.Items {
		if item.DistributorCustomerID != 301 && item.DistributorCustomerID != 302 {
			t.Fatalf("global order customer projection=%d", item.DistributorCustomerID)
		}
	}
	if len(first) != 10 || cursor != fmt.Sprint(ownerIDs[9]) {
		t.Fatalf("first detail page ids=%v cursor=%q", first, cursor)
	}
	for index, attributionID := range first {
		if attributionID != ownerIDs[index] {
			t.Fatalf("first detail page ids=%v want=%v", first, ownerIDs[:10])
		}
	}
	if len(secondIDs) != 1 || secondIDs[0] != ownerIDs[10] {
		t.Fatalf("second detail page ids=%v", secondIDs)
	}
	if earnings.GrossPaidSalesMinor != 11000 || earnings.SuccessfulRefundsMinor != 500 || earnings.InitialCommissionMinor != 1000 || earnings.CommissionAdjustmentsMinor != -50 || earnings.UnsettledPayableMinor != 950 || earnings.PaidCommissionMinor != 0 || earnings.RecoveredMinor != 0 {
		t.Fatalf("earnings omitted zero/refund facts: %+v", earnings)
	}
}

// TestPostgreSQLAdminOrderDetailSettlementConfirmationMatchesReference keeps
// a generic settlement row update distinct from the actual receiver-success
// audit. The administrator detail must present the same settlement-reference
// evidence that the Order read model uses, never its updated_at as a proxy.
func TestPostgreSQLAdminOrderDetailSettlementConfirmationMatchesReference(t *testing.T) {
	pool, cleanup := settlementWarningPool(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 9, 15, 8, 0, 0, 0, time.UTC)
	distributor := seedAdminDetailDistributor(t, ctx, pool, 501, "DSTDETAIL501", now)
	policy := seedAdminDetailPolicy(t, ctx, pool, now)
	credential := seedAdminDetailCredential(t, ctx, pool, distributor, now)
	attribution := seedAdminDetailAttribution(t, ctx, pool, distributor, credential, policy, 9501, "确认时间商品", now)
	var commissionID int64
	if err := pool.QueryRow(ctx, `SELECT id FROM distribution_commissions WHERE attribution_id=$1`, attribution).Scan(&commissionID); err != nil {
		t.Fatal(err)
	}
	confirmedAt := now.Add(2 * time.Minute)
	updatedAt := now.Add(8 * time.Minute)
	if _, err := pool.Exec(ctx, `INSERT INTO distribution_settlements(commission_id,settlement_reference,amount_minor,currency,original_payment_reference,state,provider_deadline_at,version,created_at,updated_at) VALUES($1,'dstl_admin_confirmed',100,'CNY','payment:admin:9501','receiver_succeeded',$2,2,$3,$4)`, commissionID, now.Add(24*time.Hour), now, updatedAt); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO distribution_audit_events(event_type,aggregate_type,aggregate_id,actor_scope,payload,occurred_at) VALUES('distribution.settlement_paid.v1','commission',$1,'worker:distribution-due',jsonb_build_object('settlement_reference','dstl_admin_confirmed'),$2),('distribution.settlement_paid.v1','commission',$1,'worker:distribution-due',jsonb_build_object('settlement_reference','dstl_other'),$3)`, commissionID, confirmedAt, now.Add(12*time.Minute)); err != nil {
		t.Fatal(err)
	}
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
	var detail distributionport.AdminOrderDetail
	if err = uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		detail, readErr = repository.ReadAdminOrderDetail(tx, attribution)
		return readErr
	}); err != nil {
		t.Fatal(err)
	}
	if len(detail.Settlements) != 1 || detail.Settlements[0].SettlementConfirmedAt == nil || !detail.Settlements[0].SettlementConfirmedAt.Equal(confirmedAt) || detail.Settlements[0].UpdatedAt == nil || !detail.Settlements[0].UpdatedAt.Equal(updatedAt) {
		t.Fatalf("settlement confirmation must match its audit reference rather than row update: %+v", detail.Settlements)
	}
}

func updateAdminDetailCommission(t *testing.T, ctx context.Context, pool *pgxpool.Pool, attributionID, successfulRefund, initial, payable int64, status string) {
	t.Helper()
	if _, err := pool.Exec(ctx, `UPDATE distribution_commissions SET successful_refund_minor=$2,initial_minor=$3,current_payable_minor=$4,status=$5 WHERE attribution_id=$1`, attributionID, successfulRefund, initial, payable, status); err != nil {
		t.Fatal(err)
	}
}

func seedAdminDetailDistributor(t *testing.T, ctx context.Context, pool *pgxpool.Pool, customerID int64, publicNo string, now time.Time) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(ctx, `INSERT INTO distribution_distributors(customer_id,public_no,agreement_version,enabled,registered_at,version,created_at,updated_at) VALUES($1,$2,'v1',true,$3,1,$3,$3) RETURNING id`, customerID, publicNo, now).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func seedAdminDetailPolicy(t *testing.T, ctx context.Context, pool *pgxpool.Pool, now time.Time) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(ctx, `INSERT INTO distribution_product_policies(product_id,product_type,enabled,commission_rate_basis_points,wait_days,version,created_at,updated_at) VALUES(9301,'standard_product',true,1000,7,1,$1,$1) RETURNING id`, now).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func seedAdminDetailCredential(t *testing.T, ctx context.Context, pool *pgxpool.Pool, distributorID int64, now time.Time) int64 {
	t.Helper()
	var id int64
	digest := make([]byte, 32)
	digest[0] = byte(distributorID)
	if err := pool.QueryRow(ctx, `INSERT INTO distribution_promotion_credentials(distributor_id,product_id,product_type,token_digest,status,created_at,expires_at) VALUES($1,9301,'standard_product',$2,'active',$3,$4) RETURNING id`, distributorID, digest, now, now.Add(time.Hour)).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func seedAdminDetailAttribution(t *testing.T, ctx context.Context, pool *pgxpool.Pool, distributorID, credentialID, policyID, orderID int64, productName string, now time.Time) int64 {
	t.Helper()
	var attributionID int64
	if err := pool.QueryRow(ctx, `INSERT INTO distribution_order_attributions(order_id,order_item_line,product_code,product_name,distributor_id,promotion_credential_id,qualification_evidence_reference,qualification_state,policy_id,policy_version,commission_rate_basis_points,wait_days,attributed_at) VALUES($1,1,$2,$3,$4,$5,$6,'eligible',$7,1,1000,7,$8) RETURNING id`, orderID, productName, productName, distributorID, credentialID, "order:"+fmt.Sprint(orderID)+":line:1", policyID, now).Scan(&attributionID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO distribution_commissions(attribution_id,order_id,order_item_line,distributor_id,original_item_paid_minor,successful_refund_minor,initial_minor,current_payable_minor,paid_minor,commission_rate_basis_points,paid_confirmed_at,due_at,status,hold_reason,cancel_reason,exception_reason,version,created_at,updated_at) VALUES($1,$2,1,$3,1000,0,100,100,0,1000,$4,$5,'pending','','','',1,$4,$4)`, attributionID, orderID, distributorID, now, now.Add(7*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	return attributionID
}
