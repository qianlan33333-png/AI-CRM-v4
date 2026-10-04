package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/csv"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	customerapp "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/app"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	customerstore "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/store"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	identityquery "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/query"
	identitysecure "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/secure"
	identitystore "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/store"
	orderapp "github.com/qianlan33333-png/AI-CRM-v3/internal/order/app"
	orderdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	ordermigration "github.com/qianlan33333-png/AI-CRM-v3/internal/order/migration"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	orderstore "github.com/qianlan33333-png/AI-CRM-v3/internal/order/store"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

const testScope = "wechat-open-platform:test-history"

func csvFixture(t *testing.T, rows [][]string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "orders.csv")
	f, e := os.Create(p)
	if e != nil {
		t.Fatal(e)
	}
	w := csv.NewWriter(f)
	w.Write([]string{"序号", "内部订单号", "用户UNION_ID", "买家手机号", "商品ID", "商品名称", "支付时间", "订单状态", "售后状态", "订单实收金额", "订单已成功退款金额（出）"})
	w.WriteAll(rows)
	w.Flush()
	if e = w.Error(); e != nil {
		t.Fatal(e)
	}
	f.Close()
	return p
}
func TestCSVHistoryPreservesMoneyTimeBundleAndDuplicates(t *testing.T) {
	row := []string{"1", "source-1", "union-1", "13800000001", "bundle-A/bundle-B", "原组合商品", "2024-01-02 03:04:05", "已完成", "部分退款", "123.45", "20.01"}

	a := csvFixture(t, [][]string{row})
	row[0] = "2"
	b := csvFixture(t, [][]string{row, {"3", "zero", "", "", "free", "零元", "", "已关闭", "", "0", "0"}})
	src, e := load([]string{a, b}, testScope)
	if e != nil {
		t.Fatal(e)
	}
	if src.Input != 3 || src.Duplicates != 1 || len(src.Rows) != 2 {
		t.Fatalf("counts %+v", src)
	}
	s := snapshot(src.Rows[0], decision{Customer: 1}, time.Now())
	if s.Amount.AmountMinor != 12345 || s.RefundedMinor != 2001 || s.Status != orderdomain.StatusPartiallyRefunded || len(s.Items) != 1 || s.Items[0].ProductID != nil || s.ExternalHistory.SourcePaidAt.UTC().Format(time.RFC3339) != "2024-01-01T19:04:05Z" {
		t.Fatalf("snapshot %+v", s)
	}
	z := snapshot(src.Rows[1], decision{Customer: 1}, time.Now())
	if _, e = orderdomain.Restore(z); e == nil || z.ExternalHistory.SourcePaidAt != nil || z.EffectEligible {
		t.Fatalf("zero history: %v", e)
	}
	native := z
	native.Provider = orderdomain.ProviderWeChatPay
	native.ExternalHistory = nil
	if _, e = orderdomain.Restore(native); e == nil {
		t.Fatal("native zero accepted")
	}
	row[9] = "123.46"
	c := csvFixture(t, [][]string{row})
	if _, e = load([]string{a, c}, testScope); e == nil {
		t.Fatal("changed duplicate accepted")
	}
	for _, v := range []string{"-1", "1.001", "1e2", "92233720368547758.08"} {
		if _, e = minor(v); e == nil {
			t.Fatalf("invalid money accepted %s", v)
		}
	}
	if ref, ok := phoneReference("+86-13800000001"); !ok || ref.Scope != "phone:cn11" {
		t.Fatal("mainland normalization")
	}
	if ref, ok := phoneReference("+1-212-555-0100"); !ok || ref.Scope != "phone:e164" {
		t.Fatal("international normalization")
	}
}
func historyPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url, e := platformconfig.DatabaseURL()
	if e != nil {
		t.Skip("PostgreSQL unavailable")
	}
	ctx := context.Background()
	admin, e := pgxpool.New(ctx, url)
	if e != nil {
		t.Fatal(e)
	}
	schema := fmt.Sprintf("external_history_%d", time.Now().UnixNano())
	if _, e = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); e != nil {
		t.Fatal(e)
	}
	cfg, e := pgxpool.ParseConfig(url)
	if e != nil {
		t.Fatal(e)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	cfg.MaxConns = 8
	pool, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		pool.Close()
		admin.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close()
	})
	files, e := filepath.Glob("../../migrations/*.sql")
	if e != nil {
		t.Fatal(e)
	}
	for _, path := range files {
		body, e := os.ReadFile(path)
		if e != nil {
			t.Fatal(e)
		}
		sql := strings.ReplaceAll(string(body), "public.external_effects_reject_delete", "external_effects_reject_delete")
		sql = strings.ReplaceAll(sql, "public.wecom_callback_facts_reject_mutation", "wecom_callback_facts_reject_mutation")
		if _, e = pool.Exec(ctx, sql); e != nil {
			t.Fatalf("migration %s: %v", filepath.Base(path), e)
		}
	}
	return pool
}

type failImport struct {
	orderport.ExternalHistoryRepository
}

func (f failImport) ImportHistoricalWithin(context.Context, orderport.HistoricalImportCommand) (orderdomain.Snapshot, bool, error) {
	return orderdomain.Snapshot{}, false, errors.New("injected interruption")
}

type failAfterPhone struct {
	customerport.CollectedPhoneRecorder
}

func (f failAfterPhone) CollectPhoneWithin(ctx context.Context, command customerport.CollectedPhoneCommand) (identityport.DeclaredAttachResult, error) {
	result, err := f.CollectedPhoneRecorder.CollectPhoneWithin(ctx, command)
	if err != nil {
		return result, err
	}
	return result, errors.New("injected interruption after phone projection")
}

func TestExternalHistoryPostgreSQLAtomicResumeReplayAndReadback(t *testing.T) {
	pool := historyPool(t)
	ctx := context.Background()
	wrapper, e := platformpostgres.Wrap(pool, time.Second)
	if e != nil {
		t.Fatal(e)
	}
	uow, _ := platformpostgres.NewUnitOfWork(wrapper)
	ro, _ := platformpostgres.NewReadOnlyRepeatableReadUnitOfWork(wrapper)
	vault, _ := identitysecure.NewPhoneVault(base64.RawStdEncoding.EncodeToString(make([]byte, 32)))
	one := identityapp.OneIDService{Store: identitystore.NewPostgresStore(vault)}
	cs := customerstore.NewPostgreSQL()
	phones, _ := customerapp.NewCollectedPhoneService(one, cs)
	orders, _ := orderstore.NewPostgreSQL(pool, uow)
	q := identityquery.PostgreSQL{}
	m := matcher{one, q, q, cs, testScope}
	ids := []int64{}
	for i := 1; i <= 4; i++ {
		var id int64
		if e = pool.QueryRow(ctx, "INSERT INTO customers DEFAULT VALUES RETURNING id").Scan(&id); e != nil {
			t.Fatal(e)
		}
		ids = append(ids, id)
		if _, e = pool.Exec(ctx, `INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,verified_at) VALUES($1,'unionid',$2,$3,'verified','test',1,now())`, id, testScope, fmt.Sprintf("union-%d", i)); e != nil {
			t.Fatal(e)
		}
	}
	collect := func(id int64, phone, key string) identityport.DeclaredAttachResult {
		var result identityport.DeclaredAttachResult
		e := uow.Within(ctx, func(tx context.Context) error {
			var err error
			result, err = phones.CollectPhoneWithin(tx, customerport.CollectedPhoneCommand{DeclaredPhoneCommand: identityport.DeclaredPhoneCommand{OnlyIfEmpty: true, CustomerID: customerdomain.CustomerID(id), Phone: phone, Source: "order", SourceEventID: key, IdempotencyKey: key}, CollectedAt: time.Now(), Historical: true})
			return err
		})
		if e != nil {
			t.Fatal(e)
		}
		return result
	}
	if collect(ids[1], "13800000002", "seed-2").Status != identityport.DeclaredAttached {
		t.Fatal("seed phone")
	}
	oldActivity := time.Now().Add(-90 * 24 * time.Hour).UTC().Truncate(time.Microsecond)
	if _, e = pool.Exec(ctx, `INSERT INTO customer_directory_projection(customer_id,customer_status,oneid_label,activation_status,source_version,updated_at) VALUES($1,'active','CID-test','active',1,$2)`, ids[0], oldActivity); e != nil {
		t.Fatal(e)
	}
	a := csvFixture(t, [][]string{
		{"1", "a-paid", "union-1", "13800000001", "bundle-A/bundle-B", "原组合商品", "2024-01-02 03:04:05", "已完成", "", "1", "0"},
		{"2", "b-conflict", "union-3", "13800000002", "legacy", "历史", "2024-01-02 03:04:05", "已完成", "部分退款", "2", "1"},
		{"3", "c-phone", "unknown-union", "13800000002", "legacy", "历史", "2024-01-02 03:04:05", "已关闭", "", "3", "0"},
		{"4", "d-skip", "not-found", "13800000004", "legacy", "历史", "", "已关闭", "", "4", "0"},
		{"6", "f-phone-only", "union-4", "13800000005", "legacy", "退款只补号", "2024-01-02 03:04:05", "已完成", "全额退款", "1", "1"},
		{"7", "g-unmatched", "unknown", "13800000009", "legacy", "未匹配", "2024-01-02 03:04:05", "已完成", "", "4", "0"},
		{"8", "h-zero", "union-3", "13800000008", "free", "零元", "2024-01-02 03:04:05", "已完成", "", "0", "0"},
		{"5", "e-international", "union-4", "+1-212-555-0100", "legacy", "国际", "2024-01-02 03:04:05", "已发货", "全额退款", "5", "5"},
	})
	src, e := load([]string{a}, testScope)
	if e != nil {
		t.Fatal(e)
	}
	out, decisions, e := inspect(ctx, ro, m, orders, src)
	if e != nil {
		t.Fatal(e)
	}
	if out.Union != 1 || out.Phone != 1 || out.Skipped != 1 || out.PhoneFillCustomers != 2 || out.Conflicts != 1 || out.UnknownTime != 0 || out.Zero != 0 || out.Amount != 400 || out.Refund != 0 || out.RefundMatchedRows != 3 || out.ExcludedNoTime != 1 || out.ExcludedZero != 1 {
		t.Fatalf("inspect %+v", out)
	}
	for _, sameCustomer := range []bool{true, false} {
		ambiguous := src
		paid := time.Now().Add(-time.Hour)
		ambiguous.Rows = []sourceRow{
			{Key: "candidate-one", Union: "union-3", Phone: "13800000031", PaidAt: &paid, Amount: 100},
			{Key: "candidate-two", Union: "union-3", Phone: "13800000032", PaidAt: &paid, Amount: 100},
		}
		if !sameCustomer {
			ambiguous.Rows[1].Union = "union-4"
			ambiguous.Rows[1].Phone = ambiguous.Rows[0].Phone
		}
		if _, _, e = inspect(ctx, ro, m, orders, ambiguous); !errors.Is(e, errReview) {
			t.Fatalf("conflicting source phone candidates accepted: same customer %v: %v", sameCustomer, e)
		}
	}
	runs := ordermigration.PostgreSQLRuns{Pool: pool}
	if e = runs.Begin(ctx, out.RunKey, src.Digest, 2); e != nil {
		t.Fatal(e)
	}
	if e = apply(ctx, uow, m, failImport{orders}, phones, src, decisions, &out, "apply"); e == nil {
		t.Fatal("interruption missing")
	}
	if out.Imported != 0 || out.Filled != 0 {
		t.Fatal("uncommitted counters")
	}
	var n int
	pool.QueryRow(ctx, "SELECT count(*) FROM customer_identities WHERE kind='phone'").Scan(&n)
	if n != 1 {
		t.Fatal("phone did not roll back")
	}
	pool.QueryRow(ctx, "SELECT count(*) FROM customer_directory_projection").Scan(&n)
	if n != 2 {
		t.Fatal("display did not roll back")
	}
	// A refund-only row atomically rolls back its identity, projection and receipt.
	refundOnly := src
	refundOnly.Rows = []sourceRow{src.Rows[5]}
	for _, row := range src.Rows {
		if row.Key == "f-phone-only" {
			refundOnly.Rows[0] = row
		}
	}
	refundOut, refundPlan, e := inspect(ctx, ro, m, orders, refundOnly)
	if e != nil {
		t.Fatal(e)
	}
	if e = apply(ctx, uow, m, orders, failAfterPhone{phones}, refundOnly, refundPlan, &refundOut, "apply"); e == nil || refundOut.Filled != 0 {
		t.Fatalf("refund-only rollback %v %+v", e, refundOut)
	}
	for _, query := range []string{
		"SELECT count(*) FROM customer_identities WHERE kind='phone' AND customer_id=$1",
		"SELECT count(*) FROM customer_directory_projection WHERE customer_id=$1",
		"SELECT count(*) FROM identity_declared_phone_receipts WHERE customer_id=$1",
	} {
		if e = pool.QueryRow(ctx, query, ids[3]).Scan(&n); e != nil || n != 0 {
			t.Fatalf("refund-only state survived rollback: %v count=%d", e, n)
		}
	}
	if e = apply(ctx, ro, m, orders, phones, refundOnly, refundPlan, &refundOut, "reconcile"); e == nil {
		t.Fatal("unapplied refund phone fill passed reconciliation")
	}
	if e = apply(ctx, uow, m, orders, phones, src, decisions, &out, "apply"); e != nil {
		t.Fatal(e)
	}
	if e = runs.CompleteOrders(ctx, out.RunKey, 2); e != nil {
		t.Fatal(e)
	}
	if out.Imported != 2 || out.Filled != 2 || out.PhoneOnlyFilled != 1 {
		t.Fatalf("apply %+v", out)
	}
	var activity time.Time
	if e = pool.QueryRow(ctx, "SELECT updated_at FROM customer_directory_projection WHERE customer_id=$1", ids[0]).Scan(&activity); e != nil || !activity.Equal(oldActivity) {
		t.Fatal("historical phone fill manufactured recent activity")
	}
	var activation string
	if e = pool.QueryRow(ctx, "SELECT activation_status FROM customer_directory_projection WHERE customer_id=$1", ids[3]).Scan(&activation); e != nil || activation != "stale" {
		t.Fatal("phone-only fill activated a missing projection")
	}

	read, decisions, e := inspect(ctx, ro, m, orders, src)
	if e != nil {
		t.Fatal(e)
	}
	if e = apply(ctx, ro, m, orders, phones, src, decisions, &read, "reconcile"); e != nil || read.Reconciled != 2 || read.PhoneOnlyReconciled != 3 {
		t.Fatalf("readback %+v %v", read, e)
	}
	if e = runs.Begin(ctx, read.RunKey, src.Digest, 2); e != nil {
		t.Fatal(e)
	}
	if e = apply(ctx, uow, m, orders, phones, src, decisions, &read, "apply"); e != nil || read.Replayed != 2 || read.Filled != 0 {
		t.Fatalf("replay %+v %v", read, e)
	}
	if e = runs.CompleteOrders(ctx, read.RunKey, 2); e != nil {
		t.Fatal(e)
	}
	read.RunKey += "-new-file-set"
	if e = runs.Begin(ctx, read.RunKey, sha256.Sum256([]byte("another file set")), 2); e != nil {
		t.Fatal(e)
	}
	read.Replayed = 0
	if e = apply(ctx, uow, m, orders, phones, src, decisions, &read, "apply"); e != nil {
		t.Fatal(e)
	}
	if e = runs.CompleteOrders(ctx, read.RunKey, 2); e != nil {
		t.Fatal(e)
	}
	modified := src
	modified.Rows = append([]sourceRow(nil), src.Rows...)
	modified.Rows[0].Digest = sha256.Sum256([]byte("changed"))
	if _, _, e = inspect(ctx, ro, m, orders, modified); e == nil {
		t.Fatal("changed source accepted")
	}
	changedRefund := refundOnly
	changedRefund.Rows = append([]sourceRow(nil), refundOnly.Rows...)
	changedRefund.Rows[0].Digest = sha256.Sum256([]byte("changed refund source"))
	if _, _, e = inspect(ctx, ro, m, orders, changedRefund); !errors.Is(e, identityapp.ErrDeclaredPayloadMismatch) {
		t.Fatalf("changed refund-only source accepted: %v", e)
	}
	for _, table := range []string{"payments", "payment_refunds", "external_effects", "order_service_entitlements"} {
		if e = pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&n); e != nil {
			t.Fatal(e)
		}
		if n != 0 {
			t.Fatalf("unexpected effect %s %d", table, n)
		}
	}
	pool.QueryRow(ctx, "SELECT count(*) FROM order_outbox WHERE event_type<>'order.history_imported'").Scan(&n)
	if n != 0 {
		t.Fatal("unexpected paid event")
	}
	pool.QueryRow(ctx, "SELECT count(*) FROM customer_identities WHERE kind='unionid' AND normalized_value='unknown-union'").Scan(&n)
	if n != 0 {
		t.Fatal("phone fallback bound union")
	}
	pool.QueryRow(ctx, "SELECT count(*) FROM customer_identities WHERE kind='phone' AND customer_id=ANY($1)", []int64{ids[2]}).Scan(&n)
	if n != 0 {
		t.Fatal("conflict/international phone filled")
	}
	if e = uow.Within(ctx, func(tx context.Context) error {
		audience, err := orders.PaidAudienceOrders(tx, time.Now())
		if len(audience) != 0 {
			t.Fatal("historical automation eligibility")
		}
		return err
	}); e != nil {
		t.Fatal(e)
	}
	// Dates use original source payment time; unknown dates never match import day.
	if e = ro.Within(ctx, func(tx context.Context) error {
		from := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
		to := from.Add(48 * time.Hour)
		rows, err := orders.List(tx, nil, 100, orderapp.ListFilter{CreatedFrom: &from, CreatedTo: &to})
		if err != nil {
			return err
		}
		if len(rows) != 2 {
			t.Fatalf("original-date rows %d", len(rows))
		}
		from = time.Now().UTC().Truncate(24 * time.Hour)
		to = from.Add(24 * time.Hour)
		rows, err = orders.List(tx, nil, 100, orderapp.ListFilter{CreatedFrom: &from, CreatedTo: &to})
		if len(rows) != 0 {
			t.Fatal("import timestamp used as original date")
		}
		return err
	}); e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, `UPDATE order_external_history_facts SET source_status='changed'`); e == nil {
		t.Fatal("immutable source facts updated")
	}
	if _, e = pool.Exec(ctx, `INSERT INTO orders(provider,source_system,source_key,merchant_order_no,amount_minor,currency,status,record_origin,effect_eligible,version,created_at,updated_at,payer_customer_id,beneficiary_customer_id) VALUES('wechat_pay','native-test','zero','native-zero',0,'CNY','pending_payment','native',true,1,now(),now(),1,1)`); e == nil {
		t.Fatal("database accepted native zero")
	}
	// A concurrent owner taking a formerly available phone must pause before importing.
	conflictPaid := time.Now().Add(-time.Hour)
	conflictSrc := source{Rows: []sourceRow{{Key: "new-conflict", PaidAt: &conflictPaid, Union: "union-3", Phone: "13800000099", ProductCode: "p", ProductName: "p", Status: "已完成", Amount: 100, Digest: sha256.Sum256([]byte("conflict")), FileIndex: 1, Line: 2}}, Input: 1, Digest: sha256.Sum256([]byte("new source"))}
	conflictReport, conflictPlan, e := inspect(ctx, ro, m, orders, conflictSrc)
	if e != nil {
		t.Fatal(e)
	}
	var other int64
	pool.QueryRow(ctx, "INSERT INTO customers DEFAULT VALUES RETURNING id").Scan(&other)
	collect(other, "13800000099", "concurrent-owner")
	if e = runs.Begin(ctx, conflictReport.RunKey, conflictSrc.Digest, 1); e != nil {
		t.Fatal(e)
	}
	if e = apply(ctx, uow, m, orders, phones, conflictSrc, conflictPlan, &conflictReport, "apply"); !errors.Is(e, errReview) {
		t.Fatalf("new ambiguity did not pause: %v", e)
	}

	// Two concurrent writers cannot put two numbers on a formerly empty customer.
	var fresh int64
	pool.QueryRow(ctx, "INSERT INTO customers DEFAULT VALUES RETURNING id").Scan(&fresh)
	var wg sync.WaitGroup
	var mu sync.Mutex
	attached := 0
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			result := collect(fresh, fmt.Sprintf("1380000001%d", i), fmt.Sprintf("concurrent-%d", i))
			if result.Status == identityport.DeclaredAttached {
				mu.Lock()
				attached++
				mu.Unlock()
			}
		}(i)
	}
	close(start)
	wg.Wait()
	if attached != 1 {
		t.Fatalf("concurrent attached %d", attached)
	}
}
