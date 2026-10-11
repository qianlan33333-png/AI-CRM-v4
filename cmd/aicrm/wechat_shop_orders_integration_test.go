package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	effects "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	orderapp "github.com/qianlan33333-png/AI-CRM-v3/internal/order/app"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	orderstore "github.com/qianlan33333-png/AI-CRM-v3/internal/order/store"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/outbound"
	jobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	postgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	productapp "github.com/qianlan33333-png/AI-CRM-v3/internal/product/app"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
	productstore "github.com/qianlan33333-png/AI-CRM-v3/internal/product/store"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/wechatshop"
	"github.com/riverqueue/river"
)

func TestPostgreSQLShopOrdersReplayRefundMissingContactAndRecovery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	dsn, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()
	pool, e := pgxpool.New(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer pool.Close()
	wrapped, _ := postgres.Wrap(pool, time.Second)
	uow, _ := postgres.NewUnitOfWork(wrapped)
	workers := river.NewWorkers()
	if e = effects.NewModuleRegistration().RegisterWorkers(workers); e != nil {
		t.Fatal(e)
	}
	_ = river.AddWorkerSafely[wechatshop.OrderArgs](workers, &wechatshop.OrderWorker{})
	client, e := jobqueue.NewInsertClient(pool, workers)
	if e != nil {
		t.Fatal(e)
	}
	eer, e := effects.NewRepository(pool, client)
	if e != nil {
		t.Fatal(e)
	}
	products, e := productstore.NewPostgreSQL(pool, uow)
	if e != nil {
		t.Fatal(e)
	}
	orders, e := orderstore.NewPostgreSQL(pool, uow)
	if e != nil {
		t.Fatal(e)
	}
	cipher, e := outbound.NewCommercePayloadAESGCM(base64.RawStdEncoding.EncodeToString(make([]byte, 32)))
	if e != nil {
		t.Fatal(e)
	}
	var received [][]byte
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		received = append(received, body)
		if r.Header.Get("X-AICRM-Delivery-Id") == "" {
			t.Error("missing stable key")
		}
		if len(received) == 2 {
			w.WriteHeader(503) // Receiver may have processed it: result remains unknown.
		} else {
			w.WriteHeader(204)
		}
	}))
	defer receiver.Close()
	target := protocolRuntimeTarget(receiver.URL)
	targets := &commerceFundsPushTargets{target: target}
	push, e := outbound.NewCommercePushService(pool, uow, eer, commerceFundsProductConfigurationReader{repository: products}, commerceFundsPushIdentityReader{}, targets, cipher)
	if e != nil {
		t.Fatal(e)
	}
	sink, _ := outbound.NewCommercePushCompletionSink(push)
	if e = eer.SetCompletionSink(sink); e != nil {
		t.Fatal(e)
	}
	provider, e := outbound.NewCommercePushProvider(true, push, targets, cipher)
	if e != nil {
		t.Fatal(e)
	}
	service := orderapp.ShopOrders{Store: orders, Products: products, Consumer: push, Cipher: cipher}
	wake := &wechatshop.Service{Enabled: true, AppID: "shop-app", Pool: pool, UOW: uow, Client: client, Products: products, Orders: service, Pending: service}
	productPush, e := productapp.NewCommerceExternalPushService(uow, products, push, commerceFundsProductPushStatuses{}, &commerceFundsProductPushEvents{})
	if e != nil {
		t.Fatal(e)
	}
	productPush.SetShopWakeup(wake)
	t0 := time.Now().UTC().Truncate(time.Second)
	source := orderport.ShopOrder{AppID: "shop-app", ID: "90071992547409931234", TransactionID: "shop-tx", CreatedAt: t0.Add(-24 * time.Hour), PaidAt: t0, UpdatedAt: t0, PaidMinor: 190, RefundedMinor: 190, Lines: []orderport.ShopOrderLine{{ProductID: "parent", SKUID: "sku", Name: "课程", Quantity: 2, UnitMinor: 100, PaidMinor: 90}, {ProductID: "parent", SKUID: "sku", Name: "课程", Quantity: 1, UnitMinor: 100, PaidMinor: 95}}}
	importOrder := func(v orderport.ShopOrder) error {
		return uow.Within(ctx, func(tx context.Context) error { return service.ImportShopOrderWithin(tx, v, t0) })
	}
	if e = importOrder(source); e != nil {
		t.Fatal("unbound order", e)
	}
	count := func(table string) int {
		t.Helper()
		var n int
		if e := pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&n); e != nil {
			t.Fatal(e)
		}
		return n
	}
	if count("orders") != 1 || count("outbound_commerce_push_intents") != 0 {
		t.Fatal("unbound order lost or pushed")
	}
	var productID productport.ID
	if e = uow.Within(ctx, func(tx context.Context) error {
		var err error
		productID, _, err = products.ImportShopSKUWithin(tx, productport.ShopSKU{AppID: "shop-app", ProductID: "parent", SKUID: "sku", Name: "课程", PriceMinor: 100}, "wxshop-fixture", time.Now())
		if err != nil {
			return err
		}
		return wake.WakeShopProductWithin(tx, productID)
	}); e != nil {
		t.Fatal(e)
	}
	if count("river_job") < 1 {
		t.Fatal("product did not wake pending order")
	}
	if e = importOrder(source); e != nil {
		t.Fatal("unconfigured order", e)
	}
	if count("outbound_commerce_push_intents") != 2 || count("external_effects") != 0 {
		t.Fatal("unsent lines not recoverable")
	}
	command := productport.SaveExternalPushConfigurationCommand{ProductID: productID, ProductKind: productport.ExternalPushWeChatPay, Enabled: true, ConfigurationReference: target.Reference, ExpectedRevision: 0, Actor: 1, IdempotencyKey: "shop-push-save-00001", BusinessParametersSet: true, PushType: "member_open"}
	if _, e = productPush.SaveExternalPushConfiguration(ctx, command); e != nil {
		t.Fatal("configuration save", e)
	}
	var group sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		group.Add(1)
		go func() { defer group.Done(); errs <- importOrder(source) }()
	}
	group.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal("concurrent callback/daily", e)
		}
	}
	if count("orders") != 1 || count("order_paid_events") != 1 || count("outbound_commerce_push_intents") != 2 || count("external_effects") != 2 {
		t.Fatal("one effect per line violated")
	}
	var state string
	var amount int64
	var complete bool
	if e = pool.QueryRow(ctx, `SELECT o.status,o.refunded_minor,f.dispatch_complete FROM orders o JOIN order_wechat_shop_facts f ON f.order_id=o.id`).Scan(&state, &amount, &complete); e != nil || state != "refunded" || amount != 190 || !complete {
		t.Fatal("refund-first paid fact blocked dispatch", e)
	}
	var nativePaid int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM order_outbox WHERE event_type='order.paid.v1'`).Scan(&nativePaid)
	if nativePaid != 0 {
		t.Fatal("external entered native fanout")
	}
	rows, e := pool.Query(ctx, `SELECT e.id,e.generation,j.river_job_id FROM external_effects e JOIN external_effect_jobs j ON j.effect_id=e.id AND j.generation=e.generation ORDER BY e.id`)
	if e != nil {
		t.Fatal(e)
	}
	var jobs [][3]int64
	for rows.Next() {
		var job [3]int64
		if e = rows.Scan(&job[0], &job[1], &job[2]); e != nil {
			t.Fatal(e)
		}
		jobs = append(jobs, job)
	}
	rows.Close()
	for _, j := range jobs {
		if e = eer.RunAttempt(ctx, j[0], j[1], j[2], provider); e != nil {
			t.Fatal("receiver attempt", e)
		}
	}
	if len(received) != 2 {
		t.Fatal("same SKU lines collapsed or quantity multiplied")
	}
	var unknown int
	if e = pool.QueryRow(ctx, `SELECT count(*) FROM outbound_commerce_push_intents WHERE state='outcome_unknown'`).Scan(&unknown); e != nil || unknown != 1 {
		t.Fatal("uncertain receiver outcome not retained", e)
	}
	ids := map[string]bool{}
	for _, raw := range received {
		var body struct {
			Phone    string `json:"phone_number"`
			Account  string `json:"account"`
			Delivery string `json:"delivery_id"`
			Order    struct {
				Paid int64 `json:"paid_amount"`
			} `json:"order"`
		}
		if json.Unmarshal(raw, &body) != nil || body.Phone != "" || body.Account != "" || body.Order.Paid != 190 || body.Delivery == "" || ids[body.Delivery] {
			t.Fatal("empty contact or frozen money/key not preserved")
		}
		ids[body.Delivery] = true
	}
	// Changing current configuration/contact cannot rewrite an accepted send.
	command.ExpectedRevision = 1
	command.IdempotencyKey = "shop-push-save-00002"
	command.PushType = "changed"
	if _, e = productPush.SaveExternalPushConfiguration(ctx, command); e != nil {
		t.Fatal(e)
	}
	source.Phone = "13800138000"
	if e = importOrder(source); e != nil {
		t.Fatal(e)
	}
	if count("external_effects") != 2 {
		t.Fatal("accepted intent regenerated")
	}
	for _, j := range jobs {
		if e = eer.RunAttempt(ctx, j[0], j[1], j[2], provider); e != nil {
			t.Fatal("terminal replay", e)
		}
	}
	if len(received) != 2 {
		t.Fatal("unknown outcome blindly resent")
	}
	historical := source
	historical.ID = "before-t0"
	historical.TransactionID = "before-t0-tx"
	historical.PaidAt = t0.Add(-time.Second)
	historical.UpdatedAt = t0
	if e = importOrder(historical); e != nil {
		t.Fatal(e)
	}
	if count("orders") != 2 || count("external_effects") != 2 {
		t.Fatal("history activated")
	}
	rollback := source
	rollback.ID = "rollback"
	rollback.TransactionID = "rollback-tx"
	sentinel := errors.New("abort")
	e = uow.Within(ctx, func(tx context.Context) error {
		if err := service.ImportShopOrderWithin(tx, rollback, t0); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(e, sentinel) || count("orders") != 2 || count("external_effects") != 2 {
		t.Fatal("paid/effect transaction not atomic", e)
	}
	conflict := source
	conflict.PaidMinor++
	if e = importOrder(conflict); e == nil {
		t.Fatal("changed paid fact accepted")
	}
}

func protocolRuntimeTarget(endpoint string) outbound.CommercePushTarget {
	t := outbound.CommercePushTarget{Reference: "shop-ref", Slot: "shop-slot", Endpoint: endpoint, SigningKey: []byte("fixture-signing"), Version: "v1", AllowLoopbackHTTP: true}
	t.BuyerID = outbound.CommercePushIdentity{Kind: identitydomain.KindWeComExternalUserID, Scope: "wecom-corp:fixture"}
	t.BuyerOpenID = outbound.CommercePushIdentity{Kind: identitydomain.KindMPOpenID, Scope: "wechat-app:fixture"}
	t.BuyerUnionID = outbound.CommercePushIdentity{Kind: identitydomain.KindUnionID, Scope: "wechat-open-platform:fixture"}
	t.BuyerPhone = outbound.CommercePushIdentity{Kind: identitydomain.KindPhone, Scope: "phone:cn11"}
	t.BeneficiaryPhone = t.BuyerPhone
	return t
}
