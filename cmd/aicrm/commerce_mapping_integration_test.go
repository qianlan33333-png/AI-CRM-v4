package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"github.com/jackc/pgx/v5/pgxpool"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	effects "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	orderapp "github.com/qianlan33333-png/AI-CRM-v3/internal/order/app"
	orderdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	ordersecure "github.com/qianlan33333-png/AI-CRM-v3/internal/order/secure"
	orderstore "github.com/qianlan33333-png/AI-CRM-v3/internal/order/store"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/outbound"
	platformjobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
	"github.com/riverqueue/river"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type mappingPaidCapture struct {
	service *outbound.CommercePushService
	event   orderport.PaidEvent
}

func (c *mappingPaidCapture) ConsumePaidEventWithin(ctx context.Context, event orderport.PaidEvent) error {
	c.event = event
	return c.service.ConsumePaidEventWithin(ctx, event)
}

type mappingCustomerName struct{ name string }

func (n *mappingCustomerName) DisplayNames(_ context.Context, ids []customerdomain.CustomerID) (map[customerdomain.CustomerID]string, error) {
	return map[customerdomain.CustomerID]string{ids[0]: n.name}, nil
}
func TestPostgreSQLCommerceLegacyPaidIgnoresHistoricalMappingAndExpiry(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	dsn, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	wrapped, err := platformpostgres.Wrap(pool, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	workers := river.NewWorkers()
	if err = effects.NewModuleRegistration().RegisterWorkers(workers); err != nil {
		t.Fatal(err)
	}
	client, err := platformjobqueue.NewInsertClient(pool, workers)
	if err != nil {
		t.Fatal(err)
	}
	effectStore, err := effects.NewRepository(pool, client)
	if err != nil {
		t.Fatal(err)
	}
	orders, err := orderstore.NewPostgreSQL(pool, uow)
	if err != nil {
		t.Fatal(err)
	}
	orderService := orderapp.NewService(uow, orders)
	contact, err := ordersecure.NewContactCipher(base64.RawStdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	_ = orderService.SetContactCipher(contact)
	var customerID int64
	if err = pool.QueryRow(ctx, "INSERT INTO customers DEFAULT VALUES RETURNING id").Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	var received []byte
	var headerEvent, headerDelivery string
	var calls int
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		received, _ = io.ReadAll(r.Body)
		headerEvent = r.Header.Get("X-AICRM-Event")
		headerDelivery = r.Header.Get("X-AICRM-Delivery-Id")
		mac := hmac.New(sha256.New, []byte("mapping-signing-key"))
		_, _ = mac.Write([]byte(r.Header.Get("X-AICRM-Timestamp") + "."))
		_, _ = mac.Write(received)
		if r.Header.Get("X-AICRM-Signature") != "sha256="+hex.EncodeToString(mac.Sum(nil)) {
			t.Error("signature does not cover exact paid body")
		}
		w.WriteHeader(204)
	}))
	defer receiver.Close()
	target := outbound.CommercePushTarget{Reference: "mapping-ref", Slot: "mapping-slot", Endpoint: receiver.URL, SigningKey: []byte("mapping-signing-key"), Version: "v1", AllowLoopbackHTTP: true}
	target.BuyerID = outbound.CommercePushIdentity{Kind: identitydomain.KindWeComExternalUserID, Scope: "wecom-corp:fixture"}
	target.BuyerOpenID = outbound.CommercePushIdentity{Kind: identitydomain.KindMPOpenID, Scope: "wechat-app:fixture"}
	target.BuyerUnionID = outbound.CommercePushIdentity{Kind: identitydomain.KindUnionID, Scope: "wechat-open-platform:fixture"}
	target.BuyerPhone = outbound.CommercePushIdentity{Kind: identitydomain.KindPhone, Scope: "phone:cn11"}
	target.BeneficiaryPhone = target.BuyerPhone
	cipher, err := outbound.NewCommercePayloadAESGCM(base64.RawStdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	mapping := &productport.FieldMapping{Version: 1, Fields: []productport.FieldMappingField{
		{Key: "mobile", Source: "variable", ValueType: "string", Variable: "order.mobile"},
		{Key: "nickname", Source: "variable", ValueType: "string", Variable: "payer.nickname"},
		{Key: "amount", Source: "variable", ValueType: "number", Variable: "order.paid_amount_minor"},
		{Key: "event", Source: "fixed", ValueType: "string", Value: json.RawMessage(`"mapped-event"`)},
		{Key: "delivery_id", Source: "fixed", ValueType: "string", Value: json.RawMessage(`"mapped-id"`)},
	}}
	configuration := &commerceFundsPushConfiguration{value: productport.ExternalPushConfiguration{ProductID: 7, ProductKind: productport.ExternalPushWeChatPay, Enabled: true, ConfigurationReference: target.Reference, Revision: 1, FieldMapping: mapping}}
	service, err := outbound.NewCommercePushService(pool, uow, effectStore, configuration, commerceFundsPushIdentityReader{customerID: customerID}, &commerceFundsPushTargets{target: target}, cipher)
	if err != nil {
		t.Fatal(err)
	}
	expiredLegacyDeadline := time.Now().Add(-time.Hour).Unix()
	configuration.value.ExpiresAtTS = &expiredLegacyDeadline
	sink, err := outbound.NewCommercePushCompletionSink(service)
	if err != nil {
		t.Fatal(err)
	}
	if err = effectStore.SetCompletionSink(sink); err != nil {
		t.Fatal(err)
	}
	provider, err := outbound.NewCommercePushProvider(true, service, &commerceFundsPushTargets{target: target}, cipher)
	if err != nil {
		t.Fatal(err)
	}
	capture := &mappingPaidCapture{service: service}
	_ = orderService.SetPaidEventConsumer(capture)
	productID := int64(7)
	created, err := orderService.Create(ctx, orderport.CreateCommand{Actor: 1, IdempotencyKey: "mapping-order-create-0001", Input: orderdomain.NewOrderInput{Provider: orderdomain.ProviderWeChatPay, SourceSystem: "aicrm-v3", SourceKey: "mapping-order", MerchantOrderNo: "mapping-order", PayerCustomerID: &customerID, BeneficiaryCustomerID: &customerID, Amount: orderdomain.Money{AmountMinor: 990, Currency: "CNY"}, Items: []orderdomain.ItemSnapshot{{LineNo: 1, ProductID: &productID, ProductCode: "mapping-product", ProductName: "映射商品", Quantity: 1, UnitAmountMinor: 990, LineAmountMinor: 990}}, RecordOrigin: orderdomain.RecordOriginNative}})
	if err != nil {
		t.Fatal(err)
	}
	frozen, _ := contact.Encrypt("+8613800000000")
	if err = uow.Within(ctx, func(tx context.Context) error {
		if e := orders.InsertContactSnapshot(tx, created.ID, frozen, 1, time.Now()); e != nil {
			return e
		}
		_, e := orderService.SettlePaymentWithin(tx, orderport.PaymentSettlementCommand{OrderID: created.ID, ProviderTransactionNo: "mapping-provider-paid", OccurredAt: time.Now(), ReceiptKey: "mapping-paid-receipt-0001"})
		return e
	}); err != nil {
		t.Fatal(err)
	}
	var before []byte
	var beforeState string
	if err = pool.QueryRow(ctx, "SELECT state,payload_ciphertext FROM outbound_commerce_push_intents WHERE source_kind='order_paid'").Scan(&beforeState, &before); err != nil || beforeState != "queued" || len(before) < 29 {
		t.Fatalf("paid intent state=%q ciphertext=%d err=%v", beforeState, len(before), err)
	}
	// Historic mapping data remains persisted for compatibility but a fresh
	// transaction.paid intent now uses the frozen legacy protocol.
	mapping.Fields[3].Value = json.RawMessage(`"changed"`)
	configuration.value.Revision = 2
	if err = uow.Within(ctx, func(tx context.Context) error { return service.ConsumePaidEventWithin(tx, capture.event) }); err != nil {
		t.Fatal(err)
	}
	var count int
	var after []byte
	var afterState string
	if err = pool.QueryRow(ctx, "SELECT count(*) FROM outbound_commerce_push_intents WHERE source_kind='order_paid'").Scan(&count); err != nil || count != 1 {
		t.Fatal("replay duplicated intent", err)
	}
	if err = pool.QueryRow(ctx, "SELECT state,payload_ciphertext FROM outbound_commerce_push_intents WHERE source_kind='order_paid'").Scan(&afterState, &after); err != nil || afterState != "queued" || string(before) != string(after) {
		t.Fatal("queued frozen payload changed")
	}
	run := func(kind string) {
		t.Helper()
		var effectID, generation, jobID int64
		if e := pool.QueryRow(ctx, `SELECT e.id,e.generation,j.river_job_id FROM external_effects e JOIN external_effect_jobs j ON j.effect_id=e.id AND j.generation=e.generation JOIN outbound_commerce_push_intents i ON i.effect_id='eer_'||e.id::text WHERE i.source_kind=$1`, kind).Scan(&effectID, &generation, &jobID); e != nil {
			t.Fatal(e)
		}
		if e := effectStore.RunAttempt(ctx, effectID, generation, jobID, provider); e != nil {
			t.Fatal(e)
		}
	}
	run("order_paid")
	if calls != 1 || headerEvent != "transaction.paid" || headerDelivery == "mapped-id" || !strings.HasPrefix(headerDelivery, "commerce_") {
		t.Fatal("mapped fields replaced transport metadata")
	}
	var paid map[string]json.RawMessage
	if json.Unmarshal(received, &paid) != nil || string(paid["event"]) != `"transaction.paid"` || len(paid["transaction"]) == 0 || len(paid["order"]) == 0 || len(paid["product"]) == 0 || len(paid["buyer"]) == 0 {
		t.Fatalf("sent body was not the legacy paid protocol: %s", received)
	}
	var buyer struct {
		Phone string `json:"phone"`
	}
	if err = json.Unmarshal(paid["buyer"], &buyer); err != nil || buyer.Phone != "13800138000" {
		t.Fatalf("decrypted buyer phone=%q err=%v", buyer.Phone, err)
	}
	for _, forbidden := range []string{"mobile", "nickname", "amount", "custom_params", "expires_at_ts"} {
		if _, found := paid[forbidden]; found {
			t.Fatalf("paid body retained historical mapping field %q: %s", forbidden, received)
		}
	}
	digest := sha256.Sum256([]byte("mapping-synthetic-key"))
	if err = uow.Within(ctx, func(tx context.Context) error {
		_, e := service.AcceptExternalPushTestWithin(tx, productport.ExternalPushTestIntent{ProductID: 7, ProductKind: productport.ExternalPushWeChatPay, ConfigurationReference: target.Reference, ConfigurationRevision: 2, ReceiptKeyDigest: digest})
		return e
	}); err != nil {
		t.Fatal(err)
	}
	run("synthetic_test")
	var body map[string]json.RawMessage
	_ = json.Unmarshal(received, &body)
	var payloadMode string
	if err = pool.QueryRow(ctx, "SELECT payload_mode FROM outbound_commerce_push_intents WHERE source_kind='synthetic_test'").Scan(&payloadMode); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || headerEvent != "external_push.test" || string(body["event"]) != `"external_push.test"` || len(body["delivery_id"]) == 0 || len(body["product"]) == 0 || len(body["custom_params"]) == 0 || payloadMode != "legacy" {
		t.Fatalf("retained mapping altered new legacy synthetic payload mode=%q body=%s", payloadMode, received)
	}
	for _, mapped := range []string{"mobile", "nickname", "amount"} {
		if _, found := body[mapped]; found {
			t.Fatalf("retained mapping leaked synthetic field %q: %s", mapped, received)
		}
	}
}
