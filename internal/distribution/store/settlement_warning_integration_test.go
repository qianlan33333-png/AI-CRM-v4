package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	distributiondomain "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/domain"
	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func TestPostgreSQLTerminalCommissionResolvesInformationalDeadlineWarning(t *testing.T) {
	pool, cleanup := settlementWarningPool(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
	var distributorID, policyID, credentialID, attributionID, commissionID int64
	if err := pool.QueryRow(ctx, `INSERT INTO distribution_distributors(customer_id,public_no,agreement_version,enabled,registered_at,version,created_at,updated_at) VALUES(101,'DSTWARN101','v1',true,$1,1,$1,$1) RETURNING id`, now).Scan(&distributorID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO distribution_product_policies(product_id,product_type,enabled,commission_rate_basis_points,wait_days,version,created_at,updated_at) VALUES(701,'standard_product',true,1000,7,1,$1,$1) RETURNING id`, now).Scan(&policyID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO distribution_promotion_credentials(distributor_id,product_id,product_type,token_digest,status,created_at,expires_at) VALUES($1,701,'standard_product',$2,'active',$3,$4) RETURNING id`, distributorID, make([]byte, 32), now, now.Add(time.Hour)).Scan(&credentialID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO distribution_order_attributions(order_id,order_item_line,product_code,product_name,distributor_id,promotion_credential_id,qualification_evidence_reference,qualification_state,policy_id,policy_version,commission_rate_basis_points,wait_days,attributed_at) VALUES(8101,1,'deadline-product','Deadline product',$1,$2,'order:8001:item:1','eligible',$3,1,1000,7,$4) RETURNING id`, distributorID, credentialID, policyID, now).Scan(&attributionID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO distribution_commissions(attribution_id,order_id,order_item_line,distributor_id,original_item_paid_minor,successful_refund_minor,initial_minor,current_payable_minor,paid_minor,commission_rate_basis_points,paid_confirmed_at,due_at,status,hold_reason,cancel_reason,exception_reason,version,created_at,updated_at) VALUES($1,8101,1,$2,1000,0,100,100,0,1000,$3,$4,'pending','','','',1,$3,$3) RETURNING id`, attributionID, distributorID, now, now.Add(7*24*time.Hour)).Scan(&commissionID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO distribution_exceptions(commission_id,kind,status,unpaid_due_minor,already_paid_minor,amount_minor,reason,evidence_reference,actor_scope,version,created_at,updated_at) VALUES($1,'settlement_deadline_imminent','open',0,0,0,'split_deadline_within_24h','payref_99','worker:distribution-due',1,$2,$2)`, commissionID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE distribution_commissions SET status='paid',paid_minor=100,version=2,updated_at=$2 WHERE id=$1`, commissionID, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	var status string
	var unpaid, amount int64
	if err := pool.QueryRow(ctx, `SELECT status,unpaid_due_minor,amount_minor FROM distribution_exceptions WHERE commission_id=$1 AND kind='settlement_deadline_imminent'`, commissionID).Scan(&status, &unpaid, &amount); err != nil || status != "resolved" || unpaid != 0 || amount != 0 {
		t.Fatalf("terminal commission left actionable deadline warning status=%q unpaid=%d amount=%d err=%v", status, unpaid, amount, err)
	}
}

func TestPostgreSQLRefundRecheckOverlappingScopeLocksInGlobalOrder(t *testing.T) {
	pool, cleanup := settlementWarningPool(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 9, 14, 11, 0, 0, 0, time.UTC)
	distributor, policy, credential := seedRefundRecheckParentFacts(t, ctx, pool, now)
	commissionOne := seedRefundRecheckCommission(t, ctx, pool, distributor, policy, credential, 8201, now)
	commissionTwo := seedRefundRecheckCommission(t, ctx, pool, distributor, policy, credential, 8202, now)
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
	scopes := []QualificationProductScope{{ProductID: 702, ProductType: distributiondomain.ProductTypeStandard}}
	locked := make(chan struct{})
	release := make(chan struct{})
	first := make(chan error, 1)
	go func() {
		first <- uow.Within(ctx, func(tx context.Context) error {
			rows, lockErr := repository.ListRefundRecheckContextsWithin(tx, 8201, scopes)
			if lockErr != nil || len(rows) != 2 || rows[0].Commission.ID != commissionOne || rows[1].Commission.ID != commissionTwo {
				if lockErr != nil {
					return lockErr
				}
				return ErrInvalid
			}
			close(locked)
			<-release
			return nil
		})
	}()
	select {
	case <-locked:
	case err = <-first:
		t.Fatalf("first scope lock failed before overlap: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("first scope lock did not start")
	}
	second := make(chan error, 1)
	go func() {
		second <- uow.Within(ctx, func(tx context.Context) error {
			rows, lockErr := repository.ListRefundRecheckContextsWithin(tx, 8202, scopes)
			if lockErr != nil || len(rows) != 2 || rows[0].Commission.ID != commissionOne || rows[1].Commission.ID != commissionTwo {
				if lockErr != nil {
					return lockErr
				}
				return ErrInvalid
			}
			return nil
		})
	}()
	select {
	case outcome := <-second:
		t.Fatalf("overlapping scope escaped first lock early: %v", outcome)
	case <-time.After(75 * time.Millisecond):
	}
	close(release)
	for name, result := range map[string]<-chan error{"first": first, "second": second} {
		select {
		case err = <-result:
			if err != nil {
				t.Fatalf("%s overlapping scope transaction=%v", name, err)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("%s overlapping scope deadlocked", name)
		}
	}
}

func seedRefundRecheckParentFacts(t *testing.T, ctx context.Context, pool *pgxpool.Pool, now time.Time) (int64, int64, int64) {
	t.Helper()
	var distributor, policy, credential int64
	if err := pool.QueryRow(ctx, `INSERT INTO distribution_distributors(customer_id,public_no,agreement_version,enabled,registered_at,version,created_at,updated_at) VALUES(202,'DSTREFUND202','v1',true,$1,1,$1,$1) RETURNING id`, now).Scan(&distributor); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO distribution_product_policies(product_id,product_type,enabled,commission_rate_basis_points,wait_days,version,created_at,updated_at) VALUES(702,'standard_product',true,1000,7,1,$1,$1) RETURNING id`, now).Scan(&policy); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO distribution_promotion_credentials(distributor_id,product_id,product_type,token_digest,status,created_at,expires_at) VALUES($1,702,'standard_product',$2,'active',$3,$4) RETURNING id`, distributor, []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32}, now, now.Add(time.Hour)).Scan(&credential); err != nil {
		t.Fatal(err)
	}
	return distributor, policy, credential
}

func seedRefundRecheckCommission(t *testing.T, ctx context.Context, pool *pgxpool.Pool, distributor, policy, credential, orderID int64, now time.Time) int64 {
	t.Helper()
	var attribution, commission int64
	if err := pool.QueryRow(ctx, `INSERT INTO distribution_order_attributions(order_id,order_item_line,product_code,product_name,distributor_id,promotion_credential_id,qualification_evidence_reference,qualification_state,policy_id,policy_version,commission_rate_basis_points,wait_days,attributed_at) VALUES($1,1,'refund-lock-product','Refund lock product',$2,$3,'order:8002:item:1','eligible',$4,1,1000,7,$5) RETURNING id`, orderID, distributor, credential, policy, now).Scan(&attribution); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO distribution_commissions(attribution_id,order_id,order_item_line,distributor_id,original_item_paid_minor,successful_refund_minor,initial_minor,current_payable_minor,paid_minor,commission_rate_basis_points,paid_confirmed_at,due_at,status,hold_reason,cancel_reason,exception_reason,version,created_at,updated_at) VALUES($1,$2,1,$3,1000,0,100,100,0,1000,$4,$5,'pending','','','',1,$4,$4) RETURNING id`, attribution, orderID, distributor, now, now.Add(7*24*time.Hour)).Scan(&commission); err != nil {
		t.Fatal(err)
	}
	return commission
}

func settlementWarningPool(t *testing.T) (*pgxpool.Pool, func()) {
	return settlementWarningPoolWithTracer(t, nil)
}

func settlementWarningPoolWithTracer(t *testing.T, tracer pgx.QueryTracer) (*pgxpool.Pool, func()) {
	t.Helper()
	databaseURL, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured; skipping deadline warning PostgreSQL integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	adminConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.NewWithConfig(ctx, adminConfig)
	if err != nil {
		t.Fatal(err)
	}
	var random [8]byte
	if _, err = rand.Read(random[:]); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	schema := "aicrm_distribution_warning_" + hex.EncodeToString(random[:])
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	config := adminConfig.Copy()
	config.ConnConfig.RuntimeParams["search_path"] = schema
	config.ConnConfig.Tracer = tracer
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close()
		t.Fatal(err)
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		pool.Close()
		admin.Close()
		t.Fatal("locate distribution migrations")
	}
	root := filepath.Join(filepath.Dir(file), "..", "..", "..")
	for _, name := range []string{"0003_access.sql", "0010_product.sql", "0157_distribution_core.sql"} {
		body, readErr := os.ReadFile(filepath.Join(root, "migrations", name))
		if readErr != nil {
			pool.Close()
			admin.Close()
			t.Fatal(readErr)
		}
		if _, execErr := pool.Exec(ctx, string(body)); execErr != nil {
			pool.Close()
			admin.Close()
			t.Fatalf("apply %s: %v", name, execErr)
		}
	}
	return pool, func() {
		pool.Close()
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_, _ = admin.Exec(cleanup, "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close()
	}
}

type orderDistributionReadTracer struct{ statements atomic.Int32 }

func (t *orderDistributionReadTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if strings.Contains(data.SQL, "FROM distribution_order_attributions a") {
		t.statements.Add(1)
	}
	return ctx
}

func (t *orderDistributionReadTracer) TraceQueryEnd(_ context.Context, _ *pgx.Conn, _ pgx.TraceQueryEndData) {
}

func (t *orderDistributionReadTracer) Reset()       { t.statements.Store(0) }
func (t *orderDistributionReadTracer) Count() int32 { return t.statements.Load() }

func TestPostgreSQLCommissionListReadsPaidSystemConfirmationWithoutScanningMoneyAsTime(t *testing.T) {
	readTracer := &orderDistributionReadTracer{}
	pool, cleanup := settlementWarningPoolWithTracer(t, readTracer)
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 9, 15, 9, 0, 0, 0, time.UTC)
	distributor, policy, credential := seedRefundRecheckParentFacts(t, ctx, pool, now)
	commissionID := seedRefundRecheckCommission(t, ctx, pool, distributor, policy, credential, 8301, now)
	otherCommissionID := seedRefundRecheckCommission(t, ctx, pool, distributor, policy, credential, 8302, now)
	confirmed := now.Add(2 * time.Hour)
	if _, err := pool.Exec(ctx, `UPDATE distribution_commissions SET status='paid',paid_minor=100,version=2,updated_at=$2 WHERE id=$1`, commissionID, confirmed); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO distribution_audit_events(event_type,aggregate_type,aggregate_id,actor_scope,payload,occurred_at) VALUES('distribution.settlement_paid.v1','commission',$1,'worker:distribution-due','{"settlement_reference":"settlement:8301"}',$2)`, commissionID, confirmed); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO distribution_commission_adjustments(commission_id,kind,delta_minor,resulting_payable_minor,reason,source_reference,occurred_at) VALUES($1,'buyer_refund',-20,80,'buyer_refund','refund:8301',$2)`, commissionID, confirmed.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO distribution_settlements(commission_id,settlement_reference,amount_minor,currency,original_payment_reference,state,provider_deadline_at,version,created_at,updated_at) VALUES($1,'settlement:8301',60,'CNY','payment:8301','receiver_succeeded',$2,1,$3,$3),($1,'settlement:8301-no-audit',40,'CNY','payment:8301b','receiver_succeeded',$2,1,$3,$3)`, commissionID, confirmed.Add(24*time.Hour), confirmed); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO distribution_exceptions(commission_id,kind,status,unpaid_due_minor,already_paid_minor,amount_minor,reason,evidence_reference,actor_scope,version,created_at,updated_at) VALUES($1,'buyer_refund_after_paid','open',0,100,100,'buyer_refund_after_paid','refund:8301','order-refund:8301',1,$2,$2)`, commissionID, confirmed); err != nil {
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
	var page distributionport.CommissionPage
	if err = uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		page, readErr = repository.ListCommissionsByCustomer(tx, 202, "", "", 20)
		return readErr
	}); err != nil {
		t.Fatalf("paid commission list: %v", err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("paid list=%+v", page.Items)
	}
	var paidItem *distributionport.CommissionListItem
	for i := range page.Items {
		if page.Items[i].OrderReference == "order-8301" {
			paidItem = &page.Items[i]
		}
	}
	if paidItem == nil || paidItem.PaidMinor != 100 || !paidItem.SettlementConfirmedAt.Equal(confirmed) || !paidItem.PaidAt.Equal(confirmed) {
		t.Fatalf("paid list=%+v", page.Items)
	}
	// An audit may arrive later for the commission with an invalid settlement
	// reference. It is not confirmation evidence for any persisted settlement
	// and must not replace the order projection's last real confirmation time.
	if _, err = pool.Exec(ctx, `INSERT INTO distribution_audit_events(event_type,aggregate_type,aggregate_id,actor_scope,payload,occurred_at) VALUES('distribution.settlement_paid.v1','commission',$1,'fixture:late-wrong-reference','{"settlement_reference":"settlement:8301-not-persisted"}',$2)`, commissionID, confirmed.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	readTracer.Reset()
	var byOrder map[int64][]distributionport.OrderDistributionLine
	if err = uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		byOrder, readErr = repository.ReadOrderDistribution(tx, []int64{8301, 8302})
		return readErr
	}); err != nil {
		t.Fatalf("order batch distribution: %v", err)
	}
	if statements := readTracer.Count(); statements != 1 {
		t.Fatalf("order distribution read used %d fact statements; one statement is required so PostgreSQL supplies one MVCC snapshot", statements)
	}
	lines := byOrder[8301]
	if len(lines) != 1 || !lines[0].HasCommission || lines[0].CommissionID != commissionID || lines[0].PaidMinor != 100 || !lines[0].SettlementConfirmedAt.Equal(confirmed) || lines[0].RateBasisPoints != 1000 || lines[0].WaitDays != 7 {
		t.Fatalf("order 8301 lines=%+v", lines)
	}
	if len(lines[0].Adjustments) != 1 || lines[0].Adjustments[0].DeltaMinor != -20 || len(lines[0].Settlements) != 2 || lines[0].Settlements[0].Reference != "settlement:8301" || lines[0].Settlements[0].SettlementConfirmedAt == nil || !lines[0].Settlements[0].SettlementConfirmedAt.Equal(confirmed) || lines[0].Settlements[1].Reference != "settlement:8301-no-audit" || lines[0].Settlements[1].SettlementConfirmedAt != nil || len(lines[0].Exceptions) != 1 || lines[0].Exceptions[0].Kind != "buyer_refund_after_paid" {
		t.Fatalf("order 8301 nested distribution facts=%+v", lines[0])
	}
	otherLines := byOrder[8302]
	if len(otherLines) != 1 || otherLines[0].CommissionID != otherCommissionID || otherLines[0].SettlementConfirmedAt != nil || len(otherLines[0].Adjustments) != 0 || len(otherLines[0].Settlements) != 0 || len(otherLines[0].Exceptions) != 0 {
		t.Fatalf("order 8302 received crossed distribution facts=%+v", otherLines)
	}
}
