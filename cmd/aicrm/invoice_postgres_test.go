package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	customer "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	effects "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects"
	ep "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	identity "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/query"
	invoice "github.com/qianlan33333-png/AI-CRM-v3/internal/invoice"
	p "github.com/qianlan33333-png/AI-CRM-v3/internal/invoice/port"
	op "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	orders "github.com/qianlan33333-png/AI-CRM-v3/internal/order/store"
	pp "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	ps "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/session"
	payments "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/store"
	jobs "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	pg "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"github.com/riverqueue/river"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Synthetic provider only; all owner scoping, transactions, sessions, OneID,
// refund exposure and effect attempts use the real PostgreSQL adapters.
type invoiceFixture struct {
	t                         *testing.T
	ctx                       context.Context
	pool                      *pgxpool.Pool
	s                         *invoice.Service
	provider                  *invoiceFixtureProvider
	effects                   *effects.Repository
	customer, other, identity int64
	token                     string
	count                     int
}
type invoiceFixtureSessions struct{}

func (invoiceFixtureSessions) LookupWithin(ctx context.Context, token string, now time.Time) (pp.SessionActor, error) {
	r, e := ps.NewPostgreSQL().Lookup(ctx, sha256.Sum256([]byte(token)), now)
	if e != nil {
		return pp.SessionActor{}, pp.ErrSessionRequired
	}
	return pp.SessionActor{PayerCustomerID: int64(r.PayerCustomerID), PayerIdentityID: r.PayerIdentityID, Channel: r.Channel}, nil
}

type invoiceFixtureProvider struct {
	s            *invoice.Service
	status, card string
	unknown      bool
	stoppedCode  string
	titleType    string
	calls        int
	mu           sync.Mutex
}

func (f *invoiceFixtureProvider) Title(context.Context, p.Application, string) (p.Launch, error) {
	return p.Launch{AppID: "wx-invoice-fixture", Path: "pages/title", UserName: "gh_fixture", ExpiresAt: time.Now().Add(30 * time.Minute)}, nil
}
func (f *invoiceFixtureProvider) ReadTitle(_ context.Context, a p.Application) (p.Buyer, error) {
	kind := f.titleType
	if kind == "" {
		kind = "ORGANIZATION"
	}
	return p.Buyer{Type: kind, TaxpayerID: "FIXTURE-TAX", Name: "隔离测试抬头", Amount: a.Snapshot.Amount, Phone: "13800000000", Email: "invoice@fixture.test", Address: "fixture address", BankName: "fixture bank"}, nil
}
func (f *invoiceFixtureProvider) Query(_ context.Context, a p.Application) (p.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.status == "" {
		return p.Result{}, p.ErrPending
	}
	r := p.Result{InvoiceID: a.InvoiceID, Status: f.status, TotalAmount: a.Snapshot.Amount}
	r.Seller.TaxpayerID = a.Snapshot.SellerTaxID
	r.Blue.Number = "BLUE-fixture"
	r.Card.Status = f.card
	if r.Status == "REVERSED" {
		r.Red.Number = "RED-fixture"
	}
	return r, nil
}
func (f *invoiceFixtureProvider) Write(context.Context, p.Application, bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.unknown {
		return p.ErrUnavailable
	}
	return nil
}
func (f *invoiceFixtureProvider) File(context.Context, p.Application) ([]byte, error) {
	return []byte("%PDF-fixture"), nil
}
func (f *invoiceFixtureProvider) Verify(context.Context, *http.Request) (p.Notice, error) {
	return p.Notice{}, p.ErrInvalid
}
func (f *invoiceFixtureProvider) SignPage(context.Context, string) (p.JSConfig, error) {
	return p.JSConfig{}, p.ErrDisabled
}
func (f *invoiceFixtureProvider) Execute(ctx context.Context, en ep.Envelope, attempt ep.Attempt) (ep.AdapterResult, error) {
	a, e := f.s.PrepareInvoiceEffect(ctx, attempt.EffectID, en.Kind == ep.KindInvoiceReverse)
	if errors.Is(e, p.ErrIneligible) {
		return ep.AdapterResult{Completion: ep.StateFinalFailed, ReceiptDigest: ep.Hash("fixture.stopped", attempt.EffectID)}, nil
	}
	if e != nil {
		return ep.AdapterResult{}, e
	}
	if f.stoppedCode != "" {
		return ep.AdapterResult{Completion: ep.StateFinalFailed, ReceiptDigest: ep.Hash("fixture.payment-read", attempt.EffectID), FailureCode: f.stoppedCode}, nil
	}
	e = f.Write(ctx, a, en.Kind == ep.KindInvoiceReverse)
	state := ep.StateExecuted
	if e != nil {
		state = ep.StateUnknown
	}
	return ep.AdapterResult{Completion: state, CallAttempted: true, RealExternalCallExecuted: e == nil, ReceiptDigest: ep.Hash("fixture.result", attempt.EffectID)}, nil
}
func newInvoiceFixture(t *testing.T) *invoiceFixture {
	t.Helper()
	ctx := context.Background()
	dsn, cleanup := adminAccessCompositionDatabase(t, ctx)
	t.Cleanup(cleanup)
	pool, e := pgxpool.New(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(pool.Close)
	wrapper, _ := pg.Wrap(pool, time.Second)
	uow, _ := pg.NewUnitOfWork(wrapper)
	workers := river.NewWorkers()
	river.AddWorkerSafely[effects.EffectJobArgs](workers, effects.NewWorker(nil, nil))
	river.AddWorkerSafely[invoice.QueryArgs](workers, &invoice.QueryWorker{})
	client, e := jobs.NewInsertClient(pool, workers)
	if e != nil {
		t.Fatal(e)
	}
	effect, e := effects.NewRepository(pool, client)
	if e != nil {
		t.Fatal(e)
	}
	o, _ := orders.NewPostgreSQL(pool, uow)
	identities := identity.NewPostgreSQL()
	f := &invoiceFixture{t: t, ctx: ctx, pool: pool, effects: effect, token: "invoice-fixture-token-0000000000001"}
	for _, dest := range []*int64{&f.customer, &f.other} {
		if e = pool.QueryRow(ctx, `INSERT INTO customers DEFAULT VALUES RETURNING id`).Scan(dest); e != nil {
			t.Fatal(e)
		}
	}
	if e = pool.QueryRow(ctx, `INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,status,verified_at)VALUES($1,'oa_openid','wechat-app:wx-fixture','openid-isolated-fixture','verified','invoice.fixture',1,'active',now())RETURNING id`, f.customer).Scan(&f.identity); e != nil {
		t.Fatal(e)
	}
	e = uow.Within(ctx, func(tx context.Context) error {
		_, e := ps.NewPostgreSQL().Insert(tx, ps.Record{TokenDigest: sha256.Sum256([]byte(f.token)), PayerIdentityID: f.identity, PayerCustomerID: customer.CustomerID(f.customer), BeneficiarySelection: "unresolved", AppScopeDigest: sha256.Sum256([]byte("wechat-app:wx-fixture")), Channel: "h5_official_account", UnionIDVerified: true, CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)})
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
	f.provider = &invoiceFixtureProvider{}
	f.s = &invoice.Service{UoW: uow, Sessions: invoiceFixtureSessions{}, Lineage: identities, Identities: identities, Orders: o, Refunds: payments.NewPostgreSQL(), Effects: effect, EffectStates: effect, Finality: effect, Jobs: invoice.QueryEnqueuer{Client: client}, Provider: f.provider, Config: p.Config{Enabled: true, MerchantID: "merchant-fixture", AppID: "wx-fixture", AppScope: "wechat-app:wx-fixture", SellerTaxID: "seller-tax-fixture", SellerName: "测试商户", BillingPersonID: "finance-fixture", Taxes: map[string]p.Tax{"course": {Code: "3010101020203000000", Rate: 600, GoodsName: "*培训服务*课程"}}}}
	f.provider.s = f.s
	effect.SetCompletionSink(f.s)
	return f
}
func (f *invoiceFixture) order(payer, beneficiary int64, status, name, provider string, at time.Time) int64 {
	f.t.Helper()
	f.count++
	no := fmt.Sprintf("FIXTURE-%06d", f.count)
	native := provider != "external_history"
	origin := "native"
	var digest any
	transaction := ""
	if !native {
		origin = "history"
		digest = make([]byte, 32)
	} else if status == "paid" {
		transaction = "TRANSACTION-" + no
	}
	var id int64
	e := f.pool.QueryRow(f.ctx, `INSERT INTO orders(provider,source_system,source_key,merchant_order_no,provider_transaction_no,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,record_origin,effect_eligible,source_row_digest,version,created_at,updated_at)VALUES($1,'invoice-fixture',$2,$2,$3,$4,$5,9900,'CNY',$6,$7,$8,$9,2,$10,$10)RETURNING id`, provider, no, transaction, payer, beneficiary, status, origin, native, digest, at).Scan(&id)
	if e != nil {
		f.t.Fatal(e)
	}
	if _, e = f.pool.Exec(f.ctx, `INSERT INTO order_items(order_id,line_no,product_code,product_name,unit_amount_minor,quantity,line_amount_minor)VALUES($1,1,'course',$2,9900,1,9900)`, id, name); e != nil {
		f.t.Fatal(e)
	}
	if !native {
		if _, e = f.pool.Exec(f.ctx, `INSERT INTO order_external_history_facts(order_id,imported_at,source_status)VALUES($1,now(),'原始已付款')`, id); e != nil {
			f.t.Fatal(e)
		}
	}
	if status == "paid" && native {
		if _, e = f.pool.Exec(f.ctx, `INSERT INTO order_paid_events(order_id,order_version,source_digest,occurred_at)VALUES($1,2,$2,$3)`, id, make([]byte, 32), at); e != nil {
			f.t.Fatal(e)
		}
	}
	return id
}
func (f *invoiceFixture) create(id int64, key string) p.Receipt {
	f.t.Helper()
	r, e := f.s.Create(f.ctx, f.token, id, key)
	if e != nil {
		f.t.Fatal(e)
	}
	return r
}
func (f *invoiceFixture) read(id string) p.Application {
	f.t.Helper()
	a, e := f.s.Read(f.ctx, f.token, id)
	if e != nil {
		f.t.Fatal(e)
	}
	return a
}
func (f *invoiceFixture) run(ref string) {
	f.t.Helper()
	var id, generation, job int64
	e := f.pool.QueryRow(f.ctx, `SELECT e.id,e.generation,j.river_job_id FROM external_effects e JOIN external_effect_jobs j ON j.effect_id=e.id AND j.generation=e.generation WHERE 'eer_'||e.id::text=$1`, ref).Scan(&id, &generation, &job)
	if e != nil {
		f.t.Fatal(e)
	}
	if e = f.effects.RunAttempt(f.ctx, id, generation, job, f.provider); e != nil {
		f.t.Fatal(e)
	}
}
func (f *invoiceFixture) refund(id int64, amount int64) error {
	return f.s.UoW.Within(f.ctx, func(tx context.Context) error {
		t, e := pg.RequireTransaction(tx)
		if e != nil {
			return e
		}
		state := "partially_refunded"
		if amount == 9900 {
			state = "refunded"
		}
		_, e = t.Exec(tx, `UPDATE orders SET status=$2,refunded_minor=$3,version=version+1 WHERE id=$1`, id, state, amount)
		if e != nil {
			return e
		}
		o, e := f.s.Orders.GetSelfWithin(tx, id, []int64{f.customer}, true)
		if e != nil {
			return e
		}
		return f.s.ConsumeRefundSettlementWithin(tx, op.RefundSettlementEvent{Order: o.Order, RefundedDelta: amount, OccurredAt: time.Now(), ReceiptKey: fmt.Sprint("refund-", id)})
	})
}
func (f *invoiceFixture) pendingRefund(id int64, status string) {
	f.t.Helper()
	var paymentID int64
	e := f.pool.QueryRow(f.ctx, `INSERT INTO payments(order_id,provider,payment_channel,merchant_order_no,payer_identity_id,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,version,created_at,updated_at)SELECT id,'wechat_pay','h5_official_account',merchant_order_no,$2,payer_customer_id,beneficiary_customer_id,amount_minor,'CNY','paid',1,created_at,updated_at FROM orders WHERE id=$1 ON CONFLICT(order_id)DO UPDATE SET version=payments.version RETURNING id`, id, f.identity).Scan(&paymentID)
	if e != nil {
		f.t.Fatal(e)
	}
	_, e = f.pool.Exec(f.ctx, `INSERT INTO payment_refunds(payment_id,provider,refund_no,amount_minor,reason,status,version,created_at,updated_at)VALUES($1,'wechat_pay',$2,100,'fixture',$3,1,now(),now())ON CONFLICT(refund_no)DO UPDATE SET status=excluded.status`, paymentID, fmt.Sprint("pending-", id), status)
	if e != nil {
		f.t.Fatal(e)
	}
}
func TestInvoicePostgreSQLOwnershipFiltersAndSession(t *testing.T) {
	f := newInvoiceFixture(t)
	at := time.Date(2026, 10, 7, 16, 0, 0, 0, time.UTC)
	own := f.order(f.customer, f.customer, "paid", "AI课程100%", "wechat_pay", at)
	f.order(f.other, f.customer, "paid", "赠送课程", "wechat_pay", at.Add(-time.Second))
	f.order(f.other, f.other, "paid", "其他用户", "wechat_pay", at)
	f.order(f.customer, f.customer, "pending_payment", "未支付课程", "alipay", at)
	f.order(f.customer, f.customer, "closed", "关闭课程", "wechat_pay", at)
	f.order(f.customer, f.customer, "paid", "历史课程", "external_history", at)
	page, e := f.s.List(f.ctx, f.token, p.OrdersQuery{})
	if e != nil || len(page.Items) != 5 {
		t.Fatalf("own rows=%+v %v", page, e)
	}
	for _, r := range page.Items {
		if r.ProductNames[0] == "赠送课程" && r.InvoiceEligible {
			t.Fatal("beneficiary invoice permission")
		}
		if r.Provider == "external_history" && (r.Time != nil || r.TimeKind != "unknown") {
			t.Fatal("import time fabricated")
		}
	}
	page, e = f.s.List(f.ctx, f.token, p.OrdersQuery{Keyword: "100%", OrderNo: "FIXTURE-000001", From: &at})
	if e != nil || len(page.Items) != 1 || page.Items[0].ID != own {
		t.Fatalf("literal/exact/date=%+v %v", page, e)
	}
	end := at
	page, e = f.s.List(f.ctx, f.token, p.OrdersQuery{Until: &end, State: "paid"})
	if e != nil || len(page.Items) != 1 {
		t.Fatalf("end exclusive=%+v %v", page, e)
	}
	page, e = f.s.List(f.ctx, f.token, p.OrdersQuery{Limit: 2})
	if e != nil || len(page.Items) != 2 || page.NextCursor == "" {
		t.Fatal("pagination", e)
	}
	next, e := f.s.List(f.ctx, f.token, p.OrdersQuery{Limit: 2, Cursor: page.NextCursor})
	if e != nil || len(next.Items) != 2 || next.Items[0].ID == page.Items[1].ID {
		t.Fatal("cursor repeat", e)
	}
	if _, e = f.s.Create(f.ctx, f.token, own+2, "ownership-key-0000001"); !errors.Is(e, p.ErrNotFound) {
		t.Fatal("other order", e)
	}
	if _, e = f.s.Create(f.ctx, f.token, own+1, "beneficiary-key-00001"); !errors.Is(e, p.ErrIneligible) {
		t.Fatal("beneficiary", e)
	}
	_, e = f.pool.Exec(f.ctx, `UPDATE customer_identities SET status='conflicted' WHERE id=$1`, f.identity)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.s.List(f.ctx, f.token, p.OrdersQuery{}); !errors.Is(e, pp.ErrSessionRequired) {
		t.Fatal("conflicted session", e)
	}
	f.pool.Exec(f.ctx, `UPDATE customer_identities SET status='active' WHERE id=$1`, f.identity)
	f.pool.Exec(f.ctx, `UPDATE customers SET status='merged',merged_into_customer_id=$2,merged_at=now(),version=version+1,lineage_version=lineage_version+1 WHERE id=$1`, f.customer, f.other)
	page, e = f.s.List(f.ctx, f.token, p.OrdersQuery{})
	if e != nil || len(page.Items) != 6 {
		t.Fatal("OneID merged lineage", e, len(page.Items))
	}
	f.pool.Exec(f.ctx, `UPDATE payment_sessions SET expires_at=now() WHERE payer_identity_id=$1`, f.identity)
	if _, e = f.s.List(f.ctx, f.token, p.OrdersQuery{}); !errors.Is(e, pp.ErrSessionRequired) {
		t.Fatal("expired", e)
	}
}

type invoiceFaultJobs struct {
	inner p.QueryJobEnqueuer
	fail  bool
}

func (f *invoiceFaultJobs) EnqueueWithin(ctx context.Context, id string, at time.Time) error {
	if f.fail {
		return errors.New("injected job failure")
	}
	return f.inner.EnqueueWithin(ctx, id, at)
}

type invoiceFaultEffects struct {
	inner ep.TransactionalAccepter
	fail  bool
}

func (f *invoiceFaultEffects) AcceptAndQueueWithin(ctx context.Context, c ep.AcceptCommand) (ep.Projection, ep.Receipt, error) {
	a, b, e := f.inner.AcceptAndQueueWithin(ctx, c)
	if e == nil && f.fail {
		return a, b, errors.New("injected accept rollback")
	}
	return a, b, e
}
func TestInvoicePostgreSQLAtomicReplayAndUnknownRecovery(t *testing.T) {
	f := newInvoiceFixture(t)
	id := f.order(f.customer, f.customer, "paid", "自动开票课程", "wechat_pay", time.Now())
	failJobs := &invoiceFaultJobs{inner: f.s.Jobs, fail: true}
	f.s.Jobs = failJobs
	if _, e := f.s.Create(f.ctx, f.token, id, "atomic-key-000000001"); e == nil {
		t.Fatal("job failure ignored")
	}
	var count int
	f.pool.QueryRow(f.ctx, `SELECT count(*) FROM invoice_applications`).Scan(&count)
	if count != 0 {
		t.Fatal("application committed without job")
	}
	failJobs.fail = false
	var wg sync.WaitGroup
	answers := make(chan p.Receipt, 8)
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, e := f.s.Create(f.ctx, f.token, id, "atomic-key-000000001")
			answers <- r
			errs <- e
		}()
	}
	wg.Wait()
	close(answers)
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	var r p.Receipt
	for a := range answers {
		if r.ID != "" && a.ID != r.ID {
			t.Fatal("duplicate application")
		}
		r = a
	}
	f.create(id, "another-key-00000001")
	id2 := f.order(f.customer, f.customer, "paid", "第二订单", "wechat_pay", time.Now())
	if _, e := f.s.Create(f.ctx, f.token, id2, "another-key-00000001"); !errors.Is(e, p.ErrConflict) {
		t.Fatal("key rebound", e)
	}
	fault := &invoiceFaultEffects{inner: f.effects, fail: true}
	f.s.Effects = fault
	if e := f.s.Reconcile(f.ctx, r.ID); e == nil {
		t.Fatal("accept failure ignored")
	}
	a := f.read(r.ID)
	if a.State != "awaiting_title" || a.IssueEffect != "" {
		t.Fatal("non-atomic invoice", a.State)
	}
	f.pool.QueryRow(f.ctx, `SELECT count(*) FROM external_effects WHERE owner='invoice'`).Scan(&count)
	if count != 0 {
		t.Fatal("effect escaped rollback")
	}
	fault.fail = false
	if e := f.s.Reconcile(f.ctx, r.ID); e != nil {
		t.Fatal(e)
	}
	a = f.read(r.ID)
	if a.Buyer.Phone != "13800000000" || a.Buyer.Email != "invoice@fixture.test" || a.Buyer.Address != "fixture address" || a.Buyer.BankName != "fixture bank" {
		t.Fatal("official buyer fields lost in durable snapshot")
	}
	f.provider.unknown = true
	f.run(a.IssueEffect)
	f.run(a.IssueEffect)
	if f.provider.calls != 1 {
		t.Fatal("unknown was resubmitted")
	}
	// Simulate a process lost after committing attempted. The original River
	// job must project unknown and wake reconciliation without another write.
	if _, e := f.pool.Exec(f.ctx, `UPDATE external_effects SET state='attempted',lease_expires_at=now()-interval '1 second' WHERE 'eer_'||id::text=$1`, a.IssueEffect); e != nil {
		t.Fatal(e)
	}
	if _, e := f.pool.Exec(f.ctx, `UPDATE external_effect_attempts SET state='attempted',completed_at=NULL WHERE 'eer_'||effect_id::text=$1`, a.IssueEffect); e != nil {
		t.Fatal(e)
	}
	f.run(a.IssueEffect)
	if f.provider.calls != 1 {
		t.Fatal("expired attempted invoice was resubmitted")
	}
	f.provider.status = "ISSUED"
	f.provider.card = "INSERTED"
	if e := f.s.Reconcile(f.ctx, r.ID); e != nil {
		t.Fatal(e)
	}
	a = f.read(r.ID)
	if a.State != "issued" || a.CardState != "INSERTED" {
		t.Fatal("issued finality")
	}
	f.pool.Exec(f.ctx, `UPDATE external_effects SET lease_expires_at=now()-interval '1 second' WHERE 'eer_'||id::text=$1`, a.IssueEffect)
	if e := f.s.Reconcile(f.ctx, r.ID); e != nil {
		t.Fatal(e)
	}
	effect, _ := f.effects.Get(f.ctx, a.IssueEffect)
	if effect.State != ep.StateReconciled {
		t.Fatal("unknown EER not reconciled", effect.State)
	}
	notice := p.Notice{ID: "notify-fixture", ApplicationID: a.ID, SubMerchantID: a.Snapshot.MerchantID, BodyDigest: string(ep.Hash("fixture.body", "one"))}
	if e := f.s.Notification(f.ctx, notice); e != nil {
		t.Fatal(e)
	}
	if e := f.s.Notification(f.ctx, notice); e != nil {
		t.Fatal(e)
	}
	notice.BodyDigest = string(ep.Hash("fixture.body", "two"))
	if e := f.s.Notification(f.ctx, notice); !errors.Is(e, p.ErrConflict) {
		t.Fatal("changed callback accepted", e)
	}
	// A second valid verified session cannot read or resume another payer's invoice.
	var otherIdentity int64
	if e := f.pool.QueryRow(f.ctx, `INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,status,verified_at)VALUES($1,'oa_openid','wechat-app:wx-fixture','openid-other-fixture','verified','invoice.fixture',1,'active',now())RETURNING id`, f.other).Scan(&otherIdentity); e != nil {
		t.Fatal(e)
	}
	otherToken := "other-invoice-fixture-token-000001"
	if e := f.s.UoW.Within(f.ctx, func(tx context.Context) error {
		_, e := ps.NewPostgreSQL().Insert(tx, ps.Record{TokenDigest: sha256.Sum256([]byte(otherToken)), PayerIdentityID: otherIdentity, PayerCustomerID: customer.CustomerID(f.other), BeneficiarySelection: "unresolved", AppScopeDigest: sha256.Sum256([]byte("wechat-app:wx-fixture")), Channel: "h5_official_account", UnionIDVerified: true, CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)})
		return e
	}); e != nil {
		t.Fatal(e)
	}
	for _, suffix := range []string{"", "/resume", "/file"} {
		method := "GET"
		body := ""
		if suffix == "/resume" {
			method = "POST"
			body = "{}"
		}
		r := httptest.NewRequest(method, "https://crm.fixture/api/h5/me/invoices/"+a.ID+suffix, strings.NewReader(body))
		r.AddCookie(&http.Cookie{Name: pp.TrustedSessionCookieName, Value: otherToken})
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", "https://crm.fixture")
		w := httptest.NewRecorder()
		(&invoice.Handler{Service: f.s, Origins: []string{"https://crm.fixture"}}).ServeHTTP(w, r)
		if w.Code != 404 {
			t.Fatal("foreign invoice", suffix, w.Code)
		}
	}
	// An authenticated callback advances the existing scheduled query, never
	// creating a second active job or resurrecting an operator-cancelled job.
	var scheduled time.Time
	var queryJobs int
	f.pool.QueryRow(f.ctx, `SELECT count(*),min(scheduled_at) FROM river_job WHERE kind='invoice.provider-query.v1' AND args->>'application_id'=$1 AND state IN ('available','scheduled','retryable')`, a.ID).Scan(&queryJobs, &scheduled)
	if queryJobs != 1 || scheduled.After(time.Now().Add(time.Second)) {
		t.Fatal("notification did not wake same query", queryJobs, scheduled)
	}
	// An authenticated result may arrive before the queued attempt. Stopping
	// that obsolete local attempt must not erase the already-issued invoice.
	r2 := f.create(id2, "late-issued-query-00001")
	if e := f.s.Reconcile(f.ctx, r2.ID); e != nil {
		t.Fatal(e)
	}
	a2 := f.read(r2.ID)
	if e := f.s.Reconcile(f.ctx, r2.ID); e != nil {
		t.Fatal(e)
	}
	f.run(a2.IssueEffect)
	if f.read(r2.ID).State != "issued" || f.provider.calls != 1 {
		t.Fatal("stopped obsolete issue erased verified result or made another call")
	}

}
func TestInvoicePostgreSQLRefundRacesAndBoundedRetries(t *testing.T) {
	f := newInvoiceFixture(t)
	for _, amount := range []int64{100, 9900} {
		id := f.order(f.customer, f.customer, "paid", "退款测试课程", "wechat_pay", time.Now())
		r := f.create(id, fmt.Sprintf("refund-key-0000-%06d", id))
		if e := f.s.Reconcile(f.ctx, r.ID); e != nil {
			t.Fatal(e)
		}
		a := f.read(r.ID)
		f.run(a.IssueEffect)
		f.provider.status = "ISSUED"
		f.provider.card = "INSERTED"
		if e := f.refund(id, amount); e != nil {
			t.Fatal(e)
		}
		if e := f.s.Reconcile(f.ctx, r.ID); e != nil && !errors.Is(e, p.ErrPending) {
			t.Fatal("issue/refund race", e)
		}
		a = f.read(r.ID)
		if a.State != "reversing" || a.ReverseEffect == "" {
			t.Fatal("whole invoice reversal not queued")
		}
		f.run(a.ReverseEffect)
		f.provider.status = "REVERSE_FAILED"
		if e := f.s.Reconcile(f.ctx, r.ID); !errors.Is(e, p.ErrPending) && e != nil {
			t.Fatal(e)
		}
		a = f.read(r.ID)
		first := a.ReverseEffect
		if amount == 9900 {
			if _, e := f.pool.Exec(f.ctx, `UPDATE invoice_applications SET reverse_attempt=4,updated_at=now()-interval '6 minutes' WHERE id=$1`, a.ID); e != nil {
				t.Fatal(e)
			}
			if e := f.s.Reconcile(f.ctx, r.ID); e != nil {
				t.Fatal(e)
			}
			exhausted := f.read(r.ID)
			if exhausted.State != "reverse_failed" || exhausted.ReverseEffect != first {
				t.Fatal("exhausted reverse retry did not preserve finance handoff")
			}
		}
		f.s.Reconcile(f.ctx, r.ID)
		if a.ReverseEffect != f.read(r.ID).ReverseEffect {
			t.Fatal("reverse replay queued duplicate")
		}
		if a.State == "reverse_failed" {
			f.pool.Exec(f.ctx, `UPDATE invoice_applications SET updated_at=now()-interval '6 minutes' WHERE id=$1`, a.ID)
			f.s.Reconcile(f.ctx, r.ID)
		}
		a = f.read(r.ID)
		if a.ReverseEffect == first && a.State == "reversing" {
			f.pool.Exec(f.ctx, `UPDATE invoice_applications SET updated_at=now()-interval '6 minutes' WHERE id=$1`, a.ID)
			f.s.Reconcile(f.ctx, r.ID)
			a = f.read(r.ID)
		}
		f.provider.status = "REVERSED"
		f.provider.card = "DISCARDED"
		if e := f.s.Reconcile(f.ctx, r.ID); e != nil {
			t.Fatal(e)
		}
		if f.read(r.ID).State != "reversed" {
			t.Fatal("red finality")
		}
		f.run(a.ReverseEffect)
		if f.read(r.ID).State != "reversed" {
			t.Fatal("late stopped reverse retry downgraded verified red invoice")
		}
		f.provider.status = "ISSUED"
		if e := f.s.Reconcile(f.ctx, r.ID); e != nil {
			t.Fatal(e)
		}
		if f.read(r.ID).State != "reversed" {
			t.Fatal("late blue downgraded red")
		}
	}
	id := f.order(f.customer, f.customer, "paid", "开票前退款", "wechat_pay", time.Now())
	r := f.create(id, "before-refund-00000001")
	if e := f.refund(id, 9900); e != nil {
		t.Fatal(e)
	}
	if f.read(r.ID).State != "cancelled" {
		t.Fatal("title not cancelled")
	}
	id = f.order(f.customer, f.customer, "paid", "退款处理中", "wechat_pay", time.Now())
	r = f.create(id, "pending-refund-0000001")
	f.s.Reconcile(f.ctx, r.ID)
	a := f.read(r.ID)
	f.pendingRefund(id, "requested")
	ready, _, e := f.s.InvoicePreflight(f.ctx, a.IssueEffect)
	if e != nil || ready {
		t.Fatal("pending refund did not pause", e)
	}
	f.run(a.IssueEffect)
	if f.read(r.ID).State != "awaiting_title" {
		t.Fatal("no-call race not recoverable")
	}
	f.pendingRefund(id, "final_failed")
	if e = f.s.Reconcile(f.ctx, r.ID); e != nil {
		t.Fatal(e)
	}
	a = f.read(r.ID)
	f.run(a.IssueEffect)
	f.provider.status = "ISSUE_FAILED"
	f.provider.card = ""
	if e = f.s.Reconcile(f.ctx, r.ID); e != nil {
		t.Fatal(e)
	}
	old := a.InvoiceID
	if _, e = f.s.Resume(f.ctx, f.token, r.ID); e != nil {
		t.Fatal(e)
	}
	a = f.read(r.ID)
	if a.InvoiceID == old || a.IssueAttempt != 1 {
		t.Fatal("verified failure retry missing")
	}
	f.provider.status = ""
	if _, e = f.s.Resume(f.ctx, f.token, r.ID); e != nil {
		t.Fatal(e)
	}
	if f.read(r.ID).InvoiceID != a.InvoiceID {
		t.Fatal("pending retried with new ID")
	}
	for retry := 2; retry <= 4; retry++ {
		f.run(a.IssueEffect)
		f.provider.status = "ISSUE_FAILED"
		if e = f.s.Reconcile(f.ctx, r.ID); e != nil {
			t.Fatal(e)
		}
		if _, e = f.s.Resume(f.ctx, f.token, r.ID); e != nil {
			t.Fatal("bounded verified retry", retry, e)
		}
		a = f.read(r.ID)
		if a.IssueAttempt != retry {
			t.Fatal("retry count", a.IssueAttempt)
		}
	}
	f.run(a.IssueEffect)
	if e = f.s.Reconcile(f.ctx, r.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = f.s.Resume(f.ctx, f.token, r.ID); !errors.Is(e, p.ErrIneligible) {
		t.Fatal("fifth retry accepted", e)
	}
	if f.read(r.ID).InvoiceID != a.InvoiceID {
		t.Fatal("exhausted retry changed invoice identifier")
	}
	// An unavailable payment read made no invoice call and must recover with
	// the original invoice identifier. A proved mismatch remains stopped.
	id = f.order(f.customer, f.customer, "paid", "付款查询暂不可用", "wechat_pay", time.Now())
	r = f.create(id, "payment-read-unavailable-01")
	f.provider.status = ""
	f.s.Reconcile(f.ctx, r.ID)
	a = f.read(r.ID)
	f.provider.stoppedCode = "invoice_payment_check_unavailable"
	f.run(a.IssueEffect)
	if f.read(r.ID).State != "awaiting_title" || f.read(r.ID).InvoiceID != a.InvoiceID {
		t.Fatal("read outage stranded application or changed invoice ID")
	}
	f.provider.stoppedCode = "invoice_payment_unverified"
	f.s.Reconcile(f.ctx, r.ID)
	f.run(f.read(r.ID).IssueEffect)
	if f.read(r.ID).State != "cancelled" {
		t.Fatal("proved payment mismatch remained retryable")
	}
}
func TestInvoicePreview(t *testing.T) {
	listen := os.Getenv("AICRM_INVOICE_PREVIEW_LISTEN")
	if listen == "" {
		t.Skip("explicit local browser fixture only")
	}
	if listen != "127.0.0.1:8797" {
		t.Fatal("preview restricted to loopback")
	}
	f := newInvoiceFixture(t)
	at := time.Date(2026, 10, 8, 2, 38, 0, 0, time.UTC)
	for _, x := range []struct{ s, n, p string }{{"paid", "AI 共创营 · 第三期", "wechat_pay"}, {"paid", "一人公司成长课", "wechat_pay"}, {"pending_payment", "AI 自动化工作坊", "wechat_pay"}, {"closed", "内容创作训练营", "wechat_pay"}, {"paid", "历史导入课程", "external_history"}, {"paid", "2026 共创营", "alipay"}} {
		f.order(f.customer, f.customer, x.s, x.n, x.p, at)
		at = at.Add(-24 * time.Hour)
	}
	root, _ := filepath.Abs("../../web/dist")
	handler := &invoice.Handler{Service: f.s, Dist: root, Origins: []string{"http://" + listen}}
	server := &http.Server{Addr: listen, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.AddCookie(&http.Cookie{Name: pp.TrustedSessionCookieName, Value: f.token})
		handler.ServeHTTP(w, r)
	})}
	t.Log("Synthetic local preview http://" + listen + "/my/orders")
	if e := server.ListenAndServe(); !errors.Is(e, http.ErrServerClosed) {
		t.Fatal(e)
	}
}
