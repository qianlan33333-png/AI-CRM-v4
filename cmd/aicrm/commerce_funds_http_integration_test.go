package main

import (
	"bytes"
	"context"
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	cryptorand "crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	couponapp "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/app"
	couponport "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/port"
	couponstore "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/store"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerstore "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/store"
	effects "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	orderapp "github.com/qianlan33333-png/AI-CRM-v3/internal/order/app"
	orderdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	orderstore "github.com/qianlan33333-png/AI-CRM-v3/internal/order/store"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/outbound"
	paymentapp "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/app"
	paymenthttp "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/http"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	paymentprovider "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/provider"
	paymentsession "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/session"
	paymentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/store"
	platformjobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	productapp "github.com/qianlan33333-png/AI-CRM-v3/internal/product/app"
	producthttp "github.com/qianlan33333-png/AI-CRM-v3/internal/product/http"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
	productstore "github.com/qianlan33333-png/AI-CRM-v3/internal/product/store"
)

type commerceFundsSecurity struct{}

func (commerceFundsSecurity) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	return accessdomain.Principal{InternalID: 1, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}, nil
}
func (commerceFundsSecurity) AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error) {
	return commerceFundsSecurity{}.Authenticate(context.Background(), nil)
}

type commerceFundsSessionVerifier struct{ fact identitydomain.VerifiedFact }

func (v commerceFundsSessionVerifier) VerifyCode(_ context.Context, code string) (identitydomain.VerifiedFact, error) {
	if code != "provider-verified-session-code" {
		return identitydomain.VerifiedFact{}, errors.New("unverified session code")
	}
	return v.fact, nil
}

type commerceFundsFailingEntitlement struct{ err error }

func (f commerceFundsFailingEntitlement) GrantPaidServicePeriodWithin(context.Context, orderport.ServicePeriodGrantCommand) (orderport.Entitlement, error) {
	return orderport.Entitlement{}, f.err
}
func (f commerceFundsFailingEntitlement) ApplyServicePeriodRefundWithin(context.Context, orderport.ServicePeriodRefundCommand) (orderport.Entitlement, error) {
	return orderport.Entitlement{}, f.err
}

// commerceFundsPaymentReconciler is the verified Provider-read leaf for the
// callback/reconciliation race below. Its gate holds only the test Provider
// read (never a database transaction), so the race still uses the real
// PostgreSQL Payment, Order, coupon, entitlement, paid-event and Outbox path.
type commerceFundsPaymentReconciler struct {
	query   paymentport.WeChatPayPaymentQuery
	started chan<- struct{}
	release <-chan struct{}
}

func (r commerceFundsPaymentReconciler) QueryPayment(context.Context, string) (paymentport.WeChatPayPaymentQuery, error) {
	if r.started != nil {
		r.started <- struct{}{}
	}
	if r.release != nil {
		<-r.release
	}
	return r.query, nil
}

func (commerceFundsPaymentReconciler) QueryRefund(context.Context, string) (paymentport.WeChatPayRefundQuery, error) {
	return paymentport.WeChatPayRefundQuery{}, errors.New("unused Provider query")
}

// commerceFundsPushConfiguration and its sibling adapters deliberately expose
// only the three stable Ports that the paid-event consumer may read. The test
// keeps Product credentials, OneID values, and the Provider transport outside
// Order and Payment while exercising their shared PostgreSQL transaction.
type commerceFundsPushConfiguration struct {
	value productport.ExternalPushConfiguration
}

type commerceFundsProductConfigurationReader struct {
	repository *productstore.Repository
}

func (r commerceFundsProductConfigurationReader) ReadExternalPushConfigurationForOrder(ctx context.Context, id productport.ID) (productport.ExternalPushConfiguration, error) {
	if r.repository == nil {
		return productport.ExternalPushConfiguration{}, errors.New("product configuration repository is required")
	}
	return r.repository.ReadCommerceExternalPushConfigurationForOrder(ctx, id)
}

var _ productport.ExternalPushConfigurationReader = commerceFundsProductConfigurationReader{}

func (c *commerceFundsPushConfiguration) ReadExternalPushConfigurationForOrder(_ context.Context, id productport.ID) (productport.ExternalPushConfiguration, error) {
	if c == nil || id != c.value.ProductID {
		return productport.ExternalPushConfiguration{}, errors.New("product configuration not found")
	}
	return c.value, nil
}

type commerceFundsPushIdentityReader struct{ customerID int64 }

func (r commerceFundsPushIdentityReader) VerifiedExternalIdentityValue(_ context.Context, customerID customerdomain.CustomerID, kind identitydomain.Kind, scope string) (string, bool, error) {
	if int64(customerID) != r.customerID {
		return "", false, nil
	}
	switch {
	case kind == identitydomain.KindWeComExternalUserID && scope == "wecom-corp:commerce-fixture":
		return "fixture-buyer", true, nil
	case kind == identitydomain.KindMPOpenID && scope == "wechat-app:commerce-fixture":
		return "fixture-openid", true, nil
	case kind == identitydomain.KindUnionID && scope == "wechat-open-platform:commerce-fixture":
		return "fixture-unionid", true, nil
	default:
		return "", false, nil
	}
}

func (r commerceFundsPushIdentityReader) VerifiedOutboundPhone(_ context.Context, customerID customerdomain.CustomerID, scope string) (string, bool, error) {
	if int64(customerID) != r.customerID || scope != "phone:cn11" {
		return "", false, nil
	}
	return "13800138000", true, nil
}

var _ identityport.ExternalIdentityValueReader = commerceFundsPushIdentityReader{}
var _ identityport.VerifiedOutboundPhoneReader = commerceFundsPushIdentityReader{}

type commerceFundsPushTargets struct{ target outbound.CommercePushTarget }

func (r *commerceFundsPushTargets) CommercePushProviderEnabled() bool { return r != nil }
func (r *commerceFundsPushTargets) CommercePushTarget(_ context.Context, reference string) (outbound.CommercePushTarget, bool, error) {
	if r == nil || reference != r.target.Reference {
		return outbound.CommercePushTarget{}, false, nil
	}
	return r.target, true, nil
}

var _ outbound.CommercePushTargetResolver = (*commerceFundsPushTargets)(nil)

type commerceFundsPushDelivery struct {
	event, deliveryID, timestamp, signature string
	requestTarget                           string
	body                                    []byte
}

// commerceFundsProductPushStatuses and commerceFundsProductPushEvents keep this
// test on Product's stable Ports while exercising its real PostgreSQL store and
// Unit of Work. Saving configuration never queries delivery status or accepts an
// effect, so neither stub can hide an external-effect outcome.
type commerceFundsDisabledCommerceEffects struct{}

func (commerceFundsDisabledCommerceEffects) AcceptAndQueueWithin(context.Context, effectport.AcceptCommand) (effectport.Projection, effectport.Receipt, error) {
	return effectport.Projection{}, effectport.Receipt{}, errors.New("disabled product push must not accept an external effect")
}

type commerceFundsProductPushStatuses struct{}

func (commerceFundsProductPushStatuses) ReadExternalPushTestStatus(context.Context, productport.ID, string) (productport.ExternalPushTestStatus, error) {
	return productport.ExternalPushTestStatus{}, errors.New("status is not read while saving product configuration")
}

type commerceFundsProductPushEvents struct {
	mu    sync.Mutex
	count int
}

func (e *commerceFundsProductPushEvents) Append(_ context.Context, _ productport.Event) (productport.EventID, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.count++
	return productport.EventID(e.count), nil
}

func (e *commerceFundsProductPushEvents) Count() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.count
}

func TestPostgreSQLProductExternalPushBusinessParametersRoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	wrapped, err := platformpostgres.Wrap(pool, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := productstore.NewPostgreSQL(pool, uow)
	if err != nil {
		t.Fatal(err)
	}
	var ordinaryID, serviceID int64
	ordinaryProjection := `{"schema_version":1,"status":"enabled","enabled":true}`
	serviceProjection := `{"schema_version":1,"status":"service_period_enabled","enabled":true}`
	if err = pool.QueryRow(ctx, `INSERT INTO products(product_code,name,price_minor,currency,stock_quantity,created_by,legacy_admin_projection) VALUES('push-product','外推商品',1200,'CNY',1,1,$1::jsonb) RETURNING id`, ordinaryProjection).Scan(&ordinaryID); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `INSERT INTO products(product_code,name,price_minor,currency,stock_quantity,created_by,legacy_admin_projection) VALUES('push-service','外推周期商品',1200,'CNY',1,1,$1::jsonb) RETURNING id`, serviceProjection).Scan(&serviceID); err != nil {
		t.Fatal(err)
	}
	day, frequency, expiresAtTS := int64(30), int64(1), int64(2147483647)
	value := productport.ExternalPushConfiguration{
		ProductID: productport.ID(ordinaryID), ProductKind: productport.ExternalPushWeChatPay, Enabled: true, ConfigurationReference: "product-push-roundtrip",
		PushType: "member_open", Day: &day, Frequency: &frequency, ExpiresAtTS: &expiresAtTS, Remark: "保留业务备注", CustomParams: map[string]any{"number": json.Number("9007199254740993"), "flag": false, "nil": nil, "nested": []any{" 空白 ", map[string]any{"k": true}}},
	}
	now := time.Date(2026, 9, 6, 5, 0, 0, 0, time.UTC)
	var saved, read, orderRead productport.ExternalPushConfiguration
	if err = uow.Within(ctx, func(tx context.Context) error {
		var saveErr error
		saved, saveErr = repository.SaveCommerceExternalPushConfiguration(tx, value, now)
		return saveErr
	}); err != nil {
		t.Fatal(err)
	}
	if err = uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		read, readErr = repository.ReadCommerceExternalPushConfiguration(tx, productport.ID(ordinaryID), productport.ExternalPushWeChatPay)
		if readErr != nil {
			return readErr
		}
		orderRead, readErr = repository.ReadCommerceExternalPushConfigurationForOrder(tx, productport.ID(ordinaryID))
		return readErr
	}); err != nil {
		t.Fatal(err)
	}
	if saved.Revision != 1 || read.Revision != 1 || orderRead.ProductKind != productport.ExternalPushWeChatPay || orderRead.PushType != "member_open" || orderRead.Day == nil || *orderRead.Day != 30 || orderRead.Frequency == nil || *orderRead.Frequency != 1 || orderRead.ExpiresAtTS == nil || *orderRead.ExpiresAtTS != expiresAtTS || orderRead.Remark != "保留业务备注" || !commerceFundsJSONEquivalent(t, orderRead.CustomParams, value.CustomParams) {
		t.Fatalf("saved=%#v read=%#v order=%#v", saved, read, orderRead)
	}
	if got, ok := orderRead.CustomParams["number"].(json.Number); !ok || got.String() != "9007199254740993" {
		t.Fatalf("PostgreSQL round trip changed the frozen integer: %#v", orderRead.CustomParams)
	}
	if err = uow.Within(ctx, func(tx context.Context) error {
		service, readErr := repository.ReadCommerceExternalPushConfigurationForOrder(tx, productport.ID(serviceID))
		if readErr != nil {
			return readErr
		}
		if service.ProductKind != productport.ExternalPushServicePeriod {
			return errors.New("service-period product classified as ordinary")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE product_external_push_configurations SET custom_params='[]'::jsonb WHERE product_id=$1`, ordinaryID); err == nil {
		t.Fatal("database accepted a non-object custom_params shape")
	}
	if _, err = pool.Exec(ctx, `UPDATE product_external_push_configurations SET expires_at_ts=-1 WHERE product_id=$1`, ordinaryID); err == nil {
		t.Fatal("database accepted a negative external-push expiry")
	}
}

func TestPostgreSQLProductExternalPushReplaysMain8ecBindingReceipt(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	wrapped, err := platformpostgres.Wrap(pool, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := productstore.NewPostgreSQL(pool, uow)
	if err != nil {
		t.Fatal(err)
	}
	var productID int64
	if err = pool.QueryRow(ctx, `INSERT INTO products(product_code,name,price_minor,currency,stock_quantity,created_by,legacy_admin_projection) VALUES('push-main8ec-replay','主线旧收据商品',1200,'CNY',1,1,'{"schema_version":1,"status":"enabled","enabled":true}'::jsonb) RETURNING id`).Scan(&productID); err != nil {
		t.Fatal(err)
	}
	updated := time.Date(2026, 9, 6, 8, 0, 0, 0, time.UTC)
	command := productport.SaveExternalPushConfigurationCommand{ProductID: productport.ID(productID), ProductKind: productport.ExternalPushWeChatPay, Enabled: true, ConfigurationReference: "push-main8ec-target", Actor: 61, IdempotencyKey: "commerce-push-pg-main8ec-0001"}
	legacyPayload, err := json.Marshal(struct {
		ProductID              productport.ID                      `json:"product_id"`
		ProductKind            productport.ExternalPushProductKind `json:"product_kind"`
		Enabled                bool                                `json:"enabled"`
		ConfigurationReference string                              `json:"configuration_reference"`
	}{command.ProductID, command.ProductKind, command.Enabled, command.ConfigurationReference})
	if err != nil {
		t.Fatal(err)
	}
	legacySnapshot, err := json.Marshal(struct {
		ProductID              productport.ID                      `json:"product_id"`
		ProductKind            productport.ExternalPushProductKind `json:"product_kind"`
		Enabled                bool                                `json:"enabled"`
		ConfigurationReference string                              `json:"configuration_reference,omitempty"`
		UpdatedAt              time.Time                           `json:"updated_at"`
	}{productport.ID(productID), productport.ExternalPushWeChatPay, true, command.ConfigurationReference, updated})
	if err != nil {
		t.Fatal(err)
	}
	payloadDigest := sha256.Sum256(legacyPayload)
	keyDigest := sha256.Sum256([]byte(command.IdempotencyKey))
	if _, err = pool.Exec(ctx, `INSERT INTO product_operation_receipts(operation,actor_scope,idempotency_key_digest,payload_digest,state,result_snapshot,created_at,completed_at) VALUES('external_push_save',$1,$2,$3,'completed',$4::jsonb,$5,$5)`, "admin:61", keyDigest[:], payloadDigest[:], legacySnapshot, updated); err != nil {
		t.Fatal(err)
	}
	events := &commerceFundsProductPushEvents{}
	service, err := productapp.NewCommerceExternalPushService(uow, repository, nil, commerceFundsProductPushStatuses{}, events)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := service.SaveExternalPushConfiguration(ctx, command)
	if err != nil || replayed.Revision != 0 || replayed.ExpiresAtTS != nil || events.Count() != 0 {
		t.Fatalf("main@8ec PostgreSQL replay=%#v events=%d err=%v", replayed, events.Count(), err)
	}
	changed := command
	changed.ConfigurationReference = "changed-main8ec-target"
	if _, err = service.SaveExternalPushConfiguration(ctx, changed); !errors.Is(err, productapp.ErrConflict) {
		t.Fatalf("changed main@8ec binding replay err=%v", err)
	}
	var receipts, configurations int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM product_operation_receipts WHERE operation='external_push_save'`).Scan(&receipts); err != nil || receipts != 1 {
		t.Fatalf("main@8ec receipt count=%d err=%v", receipts, err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM product_external_push_configurations WHERE product_id=$1`, productID).Scan(&configurations); err != nil || configurations != 0 {
		t.Fatalf("main@8ec replay wrote configuration count=%d err=%v", configurations, err)
	}
}

func TestPostgreSQLProductExternalPushFirstBusinessSaveCAS(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	wrapped, err := platformpostgres.Wrap(pool, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := productstore.NewPostgreSQL(pool, uow)
	if err != nil {
		t.Fatal(err)
	}
	var productID int64
	if err = pool.QueryRow(ctx, `INSERT INTO products(product_code,name,price_minor,currency,stock_quantity,created_by,legacy_admin_projection) VALUES('push-first-cas','首次 CAS 商品',1200,'CNY',1,1,'{"schema_version":1,"status":"enabled","enabled":true}'::jsonb) RETURNING id`).Scan(&productID); err != nil {
		t.Fatal(err)
	}
	events := &commerceFundsProductPushEvents{}
	service, err := productapp.NewCommerceExternalPushService(uow, repository, nil, commerceFundsProductPushStatuses{}, events)
	if err != nil {
		t.Fatal(err)
	}
	// The application uses the immutable save receipt and then locks the
	// Product row. Two different administrators holding the default revision
	// must therefore produce one persisted version and one stale conflict.
	commands := []productport.SaveExternalPushConfigurationCommand{
		{ProductID: productport.ID(productID), ProductKind: productport.ExternalPushWeChatPay, Enabled: true, ConfigurationReference: "product-push-first-cas", BusinessParametersSet: true, PushType: "member_open", CustomParams: map[string]any{"big": json.Number("9007199254740993")}, ExpectedRevision: 0, Actor: 41, IdempotencyKey: "commerce-push-pg-first-cas-a"},
		{ProductID: productport.ID(productID), ProductKind: productport.ExternalPushWeChatPay, Enabled: true, ConfigurationReference: "product-push-first-cas", BusinessParametersSet: true, PushType: "member_open", CustomParams: map[string]any{"big": json.Number("9007199254740993")}, ExpectedRevision: 0, Actor: 42, IdempotencyKey: "commerce-push-pg-first-cas-b"},
	}
	// Hold the parent Product row until both commands have begun their locking
	// read.  The old LEFT JOIN ... FOR UPDATE implementation captured an empty
	// configuration snapshot before this wait and deterministically allowed
	// both default-revision commands to write.  Do not reduce this to a timing
	// race: the waiter check proves the intended stale-snapshot interleaving.
	blocked, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocked.Release()
	blocker, err := blocked.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Rollback(ctx) }()
	if _, err = blocker.Exec(ctx, `SELECT id FROM products WHERE id=$1 FOR UPDATE`, productID); err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	results := make(chan error, len(commands))
	var group sync.WaitGroup
	for _, command := range commands {
		command := command
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			_, saveErr := service.SaveExternalPushConfiguration(ctx, command)
			results <- saveErr
		}()
	}
	close(start)
	waitForPostgreSQLLockWaiters(t, ctx, pool, len(commands))
	if err = blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	group.Wait()
	close(results)
	var successes, conflicts int
	for result := range results {
		switch {
		case result == nil:
			successes++
		case errors.Is(result, productapp.ErrConflict):
			conflicts++
		default:
			t.Fatalf("unexpected concurrent first-save error: %v", result)
		}
	}
	if successes != 1 || conflicts != 1 || events.Count() != 1 {
		t.Fatalf("first-save CAS successes=%d conflicts=%d events=%d", successes, conflicts, events.Count())
	}
	var revision, receipts int64
	if err = pool.QueryRow(ctx, `SELECT version FROM product_external_push_configurations WHERE product_id=$1 AND product_kind='wechat_pay'`, productID).Scan(&revision); err != nil || revision != 1 {
		t.Fatalf("stored first-save version=%d err=%v", revision, err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM product_operation_receipts WHERE operation='external_push_save'`).Scan(&receipts); err != nil || receipts != 1 {
		t.Fatalf("first-save receipts=%d err=%v", receipts, err)
	}
}

func waitForPostgreSQLLockWaiters(t *testing.T, ctx context.Context, pool *pgxpool.Pool, want int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		var waiters int
		err := pool.QueryRow(ctx, `SELECT count(*)
FROM pg_stat_activity
WHERE datname=current_database() AND wait_event_type='Lock' AND state='active'`).Scan(&waiters)
		if err != nil {
			t.Fatalf("read PostgreSQL lock waiters: %v", err)
		}
		if waiters >= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected %d blocked external-push saves, observed %d", want, waiters)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestPostgreSQLUnconfiguredPaidOrderPlansDisabledCommercePushOnce(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	wrapped, err := platformpostgres.Wrap(pool, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	products, err := productstore.NewPostgreSQL(pool, uow)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 6, 7, 0, 0, 0, time.UTC)
	var customerID, productID, orderID, outboxID, paidEventID int64
	if err = pool.QueryRow(ctx, `INSERT INTO customers DEFAULT VALUES RETURNING id`).Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `INSERT INTO products(product_code,name,price_minor,currency,stock_quantity,created_by,legacy_admin_projection) VALUES('push-unconfigured','未配置外推商品',1200,'CNY',1,1,'{"schema_version":1,"status":"enabled","enabled":true}'::jsonb) RETURNING id`).Scan(&productID); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `INSERT INTO orders(provider,source_system,source_key,merchant_order_no,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,record_origin,effect_eligible,version,created_at,updated_at) VALUES('wechat_pay','native-checkout','push-unconfigured-source','push-unconfigured-merchant',$1,$1,1200,'CNY','paid','native',TRUE,2,$2,$2) RETURNING id`, customerID, now).Scan(&orderID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO order_items(order_id,line_no,product_id,product_code,product_name,unit_amount_minor,quantity,line_amount_minor) VALUES($1,1,$2,'push-unconfigured','未配置外推商品',1200,1,1200)`, orderID, productID); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `INSERT INTO order_outbox(event_type,idempotency_key,aggregate_id,payload,occurred_at) VALUES('order.paid.v1',$1,$2,'{}'::jsonb,$3) RETURNING id`, "order.paid.v1:"+strconv.FormatInt(orderID, 10), orderID, now).Scan(&outboxID); err != nil {
		t.Fatal(err)
	}
	sourceDigest := orderport.NewPaidEventSourceDigest(orderID, 2)
	if err = pool.QueryRow(ctx, `INSERT INTO order_paid_events(order_id,order_version,source_digest,occurred_at) VALUES($1,2,$2,$3) RETURNING id`, orderID, sourceDigest[:], now).Scan(&paidEventID); err != nil {
		t.Fatal(err)
	}
	service, err := outbound.NewCommercePushService(pool, uow, commerceFundsDisabledCommerceEffects{}, commerceFundsProductConfigurationReader{repository: products}, commerceFundsPushIdentityReader{customerID: customerID}, &commerceFundsPushTargets{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	productRef := productID
	event := orderport.PaidEvent{ID: paidEventID, OrderID: orderID, OrderVersion: 2, DomainEventOutboxID: outboxID, OccurredAt: now, SourceDigest: sourceDigest, Order: orderdomain.Snapshot{
		ID: orderID, Provider: orderdomain.ProviderWeChatPay, SourceSystem: "native-checkout", SourceKey: "push-unconfigured-source", MerchantOrderNo: "push-unconfigured-merchant", PayerCustomerID: &customerID, BeneficiaryCustomerID: &customerID,
		Amount: orderdomain.Money{AmountMinor: 1200, Currency: "CNY"}, Status: orderdomain.StatusPaid, Items: []orderdomain.ItemSnapshot{{LineNo: 1, ProductID: &productRef, ProductCode: "push-unconfigured", ProductName: "未配置外推商品", UnitAmountMinor: 1200, Quantity: 1, LineAmountMinor: 1200}}, RecordOrigin: orderdomain.RecordOriginNative, EffectEligible: true, Version: 2, CreatedAt: now, UpdatedAt: now,
	}}
	for attempt := 0; attempt < 2; attempt++ {
		if err = uow.Within(ctx, func(tx context.Context) error { return service.ConsumePaidEventWithin(tx, event) }); err != nil {
			t.Fatalf("unconfigured paid consume attempt=%d err=%v", attempt, err)
		}
	}
	var state, targetReference string
	var revision int64
	var effects, audits, outbox int
	err = pool.QueryRow(ctx, `SELECT intent.state,intent.target_reference,intent.product_configuration_revision,
  (SELECT count(*) FROM external_effects WHERE kind=$2),
  (SELECT count(*) FROM outbound_commerce_push_audit_events audit WHERE audit.intent_id=intent.id),
  (SELECT count(*) FROM outbound_commerce_push_outbox outbox WHERE outbox.intent_id=intent.id)
FROM outbound_commerce_push_intents intent WHERE intent.order_paid_event_id=$1`, paidEventID, effectport.KindCommerceProductPush).Scan(&state, &targetReference, &revision, &effects, &audits, &outbox)
	if err != nil || state != "planned_disabled" || targetReference != "unconfigured" || revision != 0 || effects != 0 || audits != 1 || outbox != 1 {
		t.Fatalf("unconfigured paid intent state/ref/revision/effects/audits/outbox=%q/%q/%d/%d/%d/%d err=%v", state, targetReference, revision, effects, audits, outbox, err)
	}
}

func TestPostgreSQLExpiredPaidOrderContinuesToTargetResolutionOnce(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	wrapped, err := platformpostgres.Wrap(pool, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	products, err := productstore.NewPostgreSQL(pool, uow)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 6, 7, 0, 0, 0, time.UTC)
	var customerID, productID, orderID, outboxID, paidEventID int64
	if err = pool.QueryRow(ctx, `INSERT INTO customers DEFAULT VALUES RETURNING id`).Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `INSERT INTO products(product_code,name,price_minor,currency,stock_quantity,created_by,legacy_admin_projection) VALUES('push-expired','已到期外推商品',1200,'CNY',1,1,'{"schema_version":1,"status":"enabled","enabled":true}'::jsonb) RETURNING id`).Scan(&productID); err != nil {
		t.Fatal(err)
	}
	expiresAtTS := time.Now().UTC().Add(-time.Second).Unix()
	if err = uow.Within(ctx, func(tx context.Context) error {
		_, saveErr := products.SaveCommerceExternalPushConfiguration(tx, productport.ExternalPushConfiguration{
			ProductID: productport.ID(productID), ProductKind: productport.ExternalPushWeChatPay, Enabled: true, ConfigurationReference: "push-expired-reference",
			ExpiresAtTS: &expiresAtTS, CustomParams: map[string]any{},
		}, now)
		return saveErr
	}); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `INSERT INTO orders(provider,source_system,source_key,merchant_order_no,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,record_origin,effect_eligible,version,created_at,updated_at) VALUES('wechat_pay','native-checkout','push-expired-source','push-expired-merchant',$1,$1,1200,'CNY','paid','native',TRUE,2,$2,$2) RETURNING id`, customerID, now).Scan(&orderID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO order_items(order_id,line_no,product_id,product_code,product_name,unit_amount_minor,quantity,line_amount_minor) VALUES($1,1,$2,'push-expired','已到期外推商品',1200,1,1200)`, orderID, productID); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `INSERT INTO order_outbox(event_type,idempotency_key,aggregate_id,payload,occurred_at) VALUES('order.paid.v1',$1,$2,'{}'::jsonb,$3) RETURNING id`, "order.paid.v1:"+strconv.FormatInt(orderID, 10), orderID, now).Scan(&outboxID); err != nil {
		t.Fatal(err)
	}
	sourceDigest := orderport.NewPaidEventSourceDigest(orderID, 2)
	if err = pool.QueryRow(ctx, `INSERT INTO order_paid_events(order_id,order_version,source_digest,occurred_at) VALUES($1,2,$2,$3) RETURNING id`, orderID, sourceDigest[:], now).Scan(&paidEventID); err != nil {
		t.Fatal(err)
	}
	service, err := outbound.NewCommercePushService(pool, uow, commerceFundsDisabledCommerceEffects{}, commerceFundsProductConfigurationReader{repository: products}, commerceFundsPushIdentityReader{customerID: customerID}, &commerceFundsPushTargets{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	productRef := productID
	event := orderport.PaidEvent{ID: paidEventID, OrderID: orderID, OrderVersion: 2, DomainEventOutboxID: outboxID, OccurredAt: now, SourceDigest: sourceDigest, Order: orderdomain.Snapshot{
		ID: orderID, Provider: orderdomain.ProviderWeChatPay, SourceSystem: "native-checkout", SourceKey: "push-expired-source", MerchantOrderNo: "push-expired-merchant", PayerCustomerID: &customerID, BeneficiaryCustomerID: &customerID,
		Amount: orderdomain.Money{AmountMinor: 1200, Currency: "CNY"}, Status: orderdomain.StatusPaid, Items: []orderdomain.ItemSnapshot{{LineNo: 1, ProductID: &productRef, ProductCode: "push-expired", ProductName: "已到期外推商品", UnitAmountMinor: 1200, Quantity: 1, LineAmountMinor: 1200}}, RecordOrigin: orderdomain.RecordOriginNative, EffectEligible: true, Version: 2, CreatedAt: now, UpdatedAt: now,
	}}
	for attempt := 0; attempt < 2; attempt++ {
		if err = uow.Within(ctx, func(tx context.Context) error { return service.ConsumePaidEventWithin(tx, event) }); err != nil {
			t.Fatalf("expired paid consume attempt=%d err=%v", attempt, err)
		}
	}
	var state, targetReference string
	var revision int64
	var effects, audits, outbox int
	err = pool.QueryRow(ctx, `SELECT intent.state,intent.target_reference,intent.product_configuration_revision,
  (SELECT count(*) FROM external_effects WHERE kind=$2),
  (SELECT count(*) FROM outbound_commerce_push_audit_events audit WHERE audit.intent_id=intent.id),
  (SELECT count(*) FROM outbound_commerce_push_outbox outbox WHERE outbox.intent_id=intent.id)
FROM outbound_commerce_push_intents intent WHERE intent.order_paid_event_id=$1`, paidEventID, effectport.KindCommerceProductPush).Scan(&state, &targetReference, &revision, &effects, &audits, &outbox)
	// The resolver fixture deliberately has no matching target. Reaching this
	// state proves expires_at_ts is persisted metadata rather than a paid-event
	// delivery gate; the former planner would have stopped at
	// planned_config_expired before target lookup.
	if err != nil || state != "planned_target_unavailable" || targetReference != "push-expired-reference" || revision != 1 || effects != 0 || audits != 1 || outbox != 1 {
		t.Fatalf("expired paid intent state/ref/revision/effects/audits/outbox=%q/%q/%d/%d/%d/%d err=%v", state, targetReference, revision, effects, audits, outbox, err)
	}
}

func commerceFundsJSONEquivalent(t *testing.T, left, right any) bool {
	t.Helper()
	leftRaw, leftErr := json.Marshal(left)
	rightRaw, rightErr := json.Marshal(right)
	if leftErr != nil || rightErr != nil {
		t.Fatalf("marshal values %v/%v", leftErr, rightErr)
	}
	var leftValue, rightValue any
	leftDecoder, rightDecoder := json.NewDecoder(bytes.NewReader(leftRaw)), json.NewDecoder(bytes.NewReader(rightRaw))
	leftDecoder.UseNumber()
	rightDecoder.UseNumber()
	return leftDecoder.Decode(&leftValue) == nil && rightDecoder.Decode(&rightValue) == nil && reflect.DeepEqual(leftValue, rightValue)
}

// TestPostgreSQLCommerceFundsHTTPJourney validates the actual composition-root
// journey: provider-verified public session, self selection, coupon reserve,
// signed payment settlement, service-period grant, partial refund and a later
// refund. A forced fulfillment failure proves Payment, Order, Coupon and the
// entitlement facts roll back in one UoW; concurrent callback/refund requests
// then prove the successful lifecycle cannot duplicate those facts.
func TestPostgreSQLCommerceFundsHTTPJourney(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()

	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	wrapped, err := platformpostgres.Wrap(pool, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	orders, err := orderstore.NewPostgreSQL(pool, uow)
	if err != nil {
		t.Fatal(err)
	}
	coupons, err := couponstore.NewPostgreSQL(pool, uow)
	if err != nil {
		t.Fatal(err)
	}
	couponCheckout, err := couponapp.NewCheckoutService(uow, coupons)
	if err != nil {
		t.Fatal(err)
	}
	orderService := orderapp.NewService(uow, orders)
	if err = orderService.SetCheckoutCouponCoordinator(couponCheckout); err != nil {
		t.Fatal(err)
	}

	workers := river.NewWorkers()
	effectsModule := effects.NewModuleRegistration()
	if err = effectsModule.RegisterWorkers(workers); err != nil {
		t.Fatal(err)
	}
	insertClient, err := platformjobqueue.NewInsertClient(pool, workers)
	if err != nil {
		t.Fatal(err)
	}
	effectStore, err := effects.NewRepository(pool, insertClient)
	if err != nil {
		t.Fatal(err)
	}

	var customerID int64
	if err = pool.QueryRow(ctx, "INSERT INTO customers DEFAULT VALUES RETURNING id").Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	sessions, err := paymentsession.NewService(uow, checkoutRecoveryProvisioner{identityID: 901, customerID: customerdomain.CustomerID(customerID)}, customerstore.NewPostgreSQL(), paymentsession.NewPostgreSQL(), 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	product := productport.CheckoutProduct{ID: 17, ProductType: productport.ProductOptionServicePeriod, Code: "period-17", Name: "三十天服务期", PriceMinor: 1200, Currency: "CNY", Version: 4, ServicePeriodDurationDays: 30}
	paymentService := paymentapp.NewService(uow, paymentstore.NewPostgreSQL(), orderService, sessions, effectStore, effectStore)
	if err = paymentService.SetCheckoutProductReader(checkoutRecoveryProductReader{product: product}); err != nil {
		t.Fatal(err)
	}
	if err = paymentService.SetPaymentChannelAppIDs("app", ""); err != nil {
		t.Fatal(err)
	}

	apiKey := []byte("0123456789abcdef0123456789abcdef")
	platformKey, err := rsa.GenerateKey(cryptorand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := paymentprovider.NewCallbackVerifier(map[string]*rsa.PublicKey{"local-platform": &platformKey.PublicKey}, apiKey, "app", "mch")
	if err != nil {
		t.Fatal(err)
	}
	handler, err := paymenthttp.NewHandler(paymentService, verifier, commerceFundsSecurity{}, true)
	if err != nil {
		t.Fatal(err)
	}
	fact, err := identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{Kind: identitydomain.KindMPOpenID, Scope: "wechat-app:local-app", Value: "verified-local-openid", Source: "local-provider"})
	if err != nil {
		t.Fatal(err)
	}
	if err = handler.SetTrustedSessionIssuer(commerceFundsSessionVerifier{fact: fact}, sessions); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	var deliveryLock sync.Mutex
	var deliveries []commerceFundsPushDelivery
	receiver := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, readErr := io.ReadAll(http.MaxBytesReader(writer, request.Body, 64<<10))
		if readErr != nil {
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		deliveryLock.Lock()
		deliveries = append(deliveries, commerceFundsPushDelivery{
			event: request.Header.Get("X-AICRM-Event"), deliveryID: request.Header.Get("X-AICRM-Delivery-Id"),
			timestamp: request.Header.Get("X-AICRM-Timestamp"), signature: request.Header.Get("X-AICRM-Signature"), requestTarget: request.URL.RequestURI(), body: append([]byte(nil), body...),
		})
		deliveryLock.Unlock()
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer receiver.Close()
	commerceTarget := outbound.CommercePushTarget{
		Reference: "commerce-funds-target", Slot: "commerce-funds-slot", Endpoint: receiver.URL + "/legacy/push?tenant=commerce&mode=paid", SigningKey: []byte("commerce-funds-signing-key"), Version: "legacy-v1", TenantID: "aicrm", AllowLoopbackHTTP: true,
		BuyerID:          outbound.CommercePushIdentity{Kind: identitydomain.KindWeComExternalUserID, Scope: "wecom-corp:commerce-fixture"},
		BuyerOpenID:      outbound.CommercePushIdentity{Kind: identitydomain.KindMPOpenID, Scope: "wechat-app:commerce-fixture"},
		BuyerUnionID:     outbound.CommercePushIdentity{Kind: identitydomain.KindUnionID, Scope: "wechat-open-platform:commerce-fixture"},
		BuyerPhone:       outbound.CommercePushIdentity{Kind: identitydomain.KindPhone, Scope: "phone:cn11"},
		BeneficiaryPhone: outbound.CommercePushIdentity{Kind: identitydomain.KindPhone, Scope: "phone:cn11"},
	}
	if err = outbound.ValidateCommercePushTarget(commerceTarget); err != nil {
		t.Fatal(err)
	}
	commerceCipher, err := outbound.NewCommercePayloadAESGCM(base64.RawStdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")))
	if err != nil {
		t.Fatal(err)
	}
	commerceConfiguration := &commerceFundsPushConfiguration{value: productport.ExternalPushConfiguration{ProductID: product.ID, ProductKind: productport.ExternalPushServicePeriod, Enabled: true, ConfigurationReference: commerceTarget.Reference, PushType: "service_period", Day: commerceFundsInt64(30), Frequency: commerceFundsInt64(1), Remark: "commerce-funds-fixture", CustomParams: map[string]any{"nested": map[string]any{"not": "paid payload"}}, Revision: 1, ProductName: product.Name, UpdatedAt: now}}
	commerceTargets := &commerceFundsPushTargets{target: commerceTarget}
	commercePush, err := outbound.NewCommercePushService(pool, uow, effectStore, commerceConfiguration, commerceFundsPushIdentityReader{customerID: customerID}, commerceTargets, commerceCipher)
	if err != nil {
		t.Fatal(err)
	}
	commerceCompletion, err := outbound.NewCommercePushCompletionSink(commercePush)
	if err != nil {
		t.Fatal(err)
	}
	if err = effectStore.SetCompletionSink(commerceCompletion); err != nil {
		t.Fatal(err)
	}
	commerceProvider, err := outbound.NewCommercePushProvider(true, commercePush, commerceTargets, commerceCipher)
	if err != nil {
		t.Fatal(err)
	}
	if err = handler.SetCommercePushDeliveryReaders(orderService, commercePush); err != nil {
		t.Fatal(err)
	}
	if err = orderService.SetPaidEventConsumer(commercePush); err != nil {
		t.Fatal(err)
	}
	days := int32(7)
	rules := couponapp.NewService(uow, coupons, commerceCheckoutProductFacts{17: {ID: 17, ProductType: productport.ProductOptionServicePeriod, Currency: "CNY", PriceMinor: product.PriceMinor}}, coupons)
	rule, err := rules.Create(ctx, couponport.UpsertCommand{Coupon: couponport.Coupon{Name: "资金联合券", DiscountAmountTotal: 200, TotalIssueLimit: 1, PerUserIssueLimit: 1, ClaimStartsAt: now.Add(-time.Hour), ClaimEndsAt: now.Add(time.Hour), ValidityMode: couponport.ValidityRelativeDays, RelativeValidityDays: &days, TargetRefs: []string{"service_period:17"}}, Actor: 1, IdempotencyKey: "commerce-funds-rule-create-0001"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = rules.Publish(ctx, rule.ID, 1, "commerce-funds-rule-publish-0001"); err != nil {
		t.Fatal(err)
	}
	claim, err := couponCheckout.Claim(ctx, couponport.ClaimCommand{CouponID: rule.ID, HolderCustomerID: customerID, ActorScope: "commerce-funds-payer", IdempotencyKey: "commerce-funds-claim-0001", ClaimedAt: now})
	if err != nil {
		t.Fatal(err)
	}

	issue := httptest.NewRequest(http.MethodPost, "/api/v1/wechat-pay/sessions", bytes.NewReader(commerceFundsJSON(t, map[string]string{"code": "provider-verified-session-code"})))
	issue.Header.Set("Content-Type", "application/json")
	issued := httptest.NewRecorder()
	handler.ServeHTTP(issued, issue)
	if issued.Code != http.StatusCreated {
		t.Fatalf("issue status=%d body=%s", issued.Code, issued.Body.String())
	}
	sessionCookie := commerceFundsCookie(t, issued.Result().Cookies(), paymentport.TrustedSessionCookieName)
	if !sessionCookie.HttpOnly || sessionCookie.Value == "" {
		t.Fatalf("unsafe session cookie=%+v", sessionCookie)
	}
	bindingRequest := httptest.NewRequest(http.MethodGet, "/api/v1/wechat-pay/checkout-session", nil)
	bindingRequest.AddCookie(sessionCookie)
	bindingResponse := httptest.NewRecorder()
	handler.ServeHTTP(bindingResponse, bindingRequest)
	binding := commerceFundsObject(t, bindingResponse, http.StatusOK)["checkout_session_binding"].(string)
	if binding == "" || binding == sessionCookie.Value {
		t.Fatalf("binding is not opaque=%q", binding)
	}

	checkoutPayload := map[string]any{"product_id": 17, "product_kind": "service_period", "coupon_claim_id": claim.ClaimID, "beneficiary_selection": "payer_self", "checkout_session_binding": binding}
	checkoutRequest := httptest.NewRequest(http.MethodPost, "/api/v1/wechat-pay/checkouts", bytes.NewReader(commerceFundsJSON(t, checkoutPayload)))
	checkoutRequest.Header.Set("Content-Type", "application/json")
	checkoutRequest.Header.Set("Idempotency-Key", "commerce-funds-checkout-0001")
	checkoutRequest.AddCookie(sessionCookie)
	checkoutResponse := httptest.NewRecorder()
	handler.ServeHTTP(checkoutResponse, checkoutRequest)
	checkout := commerceFundsObject(t, checkoutResponse, http.StatusAccepted)
	orderID := commerceFundsInt(t, checkout, "order_id")
	paymentID := commerceFundsInt(t, checkout, "payment_id")
	merchant := commerceFundsString(t, checkout, "merchant_order_no")
	commerceFundsAssertReserved(t, ctx, pool, orderID, paymentID, claim.ClaimID)
	commerceFundsAssertUnpaidOrderHasNoCommerceDeliveries(t, handler, merchant)

	unknownBody, unknownHeaders := commerceFundsSignedCallback(t, platformKey, apiKey, "commerce-funds-unknown", "TRANSACTION.SUCCESS", map[string]any{"appid": "app", "mchid": "mch", "out_trade_no": "v3pay_unknown_funds", "transaction_id": "tx-unknown", "trade_state": "SUCCESS", "success_time": now.Format(time.RFC3339Nano), "amount": map[string]any{"total": 1000, "currency": "CNY"}})
	unknown := httptest.NewRecorder()
	handler.ServeHTTP(unknown, commerceFundsCallbackRequest("/api/public/wechat-pay/callbacks/payment", unknownBody, unknownHeaders))
	if unknown.Code != http.StatusNotFound {
		t.Fatalf("out-of-order status=%d body=%s", unknown.Code, unknown.Body.String())
	}

	const verifiedTransactionID = "tx-commerce-funds"
	paidAt := now.Add(time.Second)
	paymentBody, paymentHeaders := commerceFundsSignedCallback(t, platformKey, apiKey, "commerce-funds-payment", "TRANSACTION.SUCCESS", map[string]any{"appid": "app", "mchid": "mch", "out_trade_no": merchant, "transaction_id": verifiedTransactionID, "trade_state": "SUCCESS", "success_time": paidAt.Format(time.RFC3339Nano), "amount": map[string]any{"total": 1000, "currency": "CNY"}})
	badHeaders := paymentHeaders.Clone()
	badHeaders.Set("Wechatpay-Signature", "bad")
	bad := httptest.NewRecorder()
	handler.ServeHTTP(bad, commerceFundsCallbackRequest("/api/public/wechat-pay/callbacks/payment", paymentBody, badHeaders))
	if bad.Code != http.StatusUnauthorized {
		t.Fatalf("invalid signature status=%d body=%s", bad.Code, bad.Body.String())
	}
	commerceFundsAssertRollback(t, ctx, pool, orderID, paymentID, merchant, 0)

	if err = orderService.SetServicePeriodEntitlementCoordinator(commerceFundsFailingEntitlement{err: errors.New("forced entitlement failure")}); err != nil {
		t.Fatal(err)
	}
	failed := httptest.NewRecorder()
	handler.ServeHTTP(failed, commerceFundsCallbackRequest("/api/public/wechat-pay/callbacks/payment", paymentBody, paymentHeaders))
	if failed.Code != http.StatusServiceUnavailable {
		t.Fatalf("forced settlement status=%d body=%s", failed.Code, failed.Body.String())
	}
	commerceFundsAssertRollback(t, ctx, pool, orderID, paymentID, merchant, 0)
	commerceFundsAssertPushRollback(t, ctx, pool)

	fulfillment, err := orderapp.NewEntitlementFulfillmentApplication(orders)
	if err != nil {
		t.Fatal(err)
	}
	if err = orderService.SetServicePeriodEntitlementCoordinator(fulfillment); err != nil {
		t.Fatal(err)
	}
	providerStarted := make(chan struct{}, 1)
	providerRelease := make(chan struct{})
	if err = paymentService.SetWeChatPayReconciler(commerceFundsPaymentReconciler{
		query: paymentport.WeChatPayPaymentQuery{
			AppID: "app", MerchantOrderNo: merchant, Currency: "CNY", Status: "SUCCESS", TransactionReference: verifiedTransactionID,
			AmountMinor: 1000, OccurredAt: paidAt, EvidenceDigest: effectport.Hash("commerce-funds-payment-query", merchant), TransactionDigest: effectport.Hash("wechatpay.transaction", verifiedTransactionID),
		},
		started: providerStarted, release: providerRelease,
	}); err != nil {
		t.Fatal(err)
	}

	// Start the Provider read, then race the original verified callback against
	// the reconciliation settlement. The reconciler has no transaction while it
	// waits, so either path may acquire the actual PostgreSQL row lock first.
	reconcileErr := make(chan error, 1)
	go func() {
		_, reconcileErrValue := paymentService.ReconcileWeChatPayPayment(ctx, paymentID)
		reconcileErr <- reconcileErrValue
	}()
	select {
	case <-providerStarted:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	callbackCode := make(chan int, 1)
	go func() {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, commerceFundsCallbackRequest("/api/public/wechat-pay/callbacks/payment", paymentBody, paymentHeaders))
		callbackCode <- response.Code
	}()
	close(providerRelease)
	if reconcileErrValue := <-reconcileErr; reconcileErrValue != nil {
		t.Fatalf("concurrent payment reconciliation: %v", reconcileErrValue)
	}
	if code := <-callbackCode; code != http.StatusOK {
		t.Fatalf("concurrent payment callback status=%d", code)
	}
	// Retain the original duplicate-notification coverage independently of the
	// cross-path race: two simultaneous deliveries of the same event must both
	// acknowledge successfully while retaining one receipt and one paid fanout.
	duplicateCallbackCodes := make(chan int, 2)
	var duplicateCallbackWait sync.WaitGroup
	for range 2 {
		duplicateCallbackWait.Add(1)
		go func() {
			defer duplicateCallbackWait.Done()
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, commerceFundsCallbackRequest("/api/public/wechat-pay/callbacks/payment", paymentBody, paymentHeaders))
			duplicateCallbackCodes <- response.Code
		}()
	}
	duplicateCallbackWait.Wait()
	close(duplicateCallbackCodes)
	for code := range duplicateCallbackCodes {
		if code != http.StatusOK {
			t.Fatalf("concurrent duplicate payment callback status=%d", code)
		}
	}

	// A further independently delivered notification must receive 200 and retain
	// only its immutable receipt—not repeat paid fanout.
	lateBody, lateHeaders := commerceFundsSignedCallback(t, platformKey, apiKey, "commerce-funds-payment-late", "TRANSACTION.SUCCESS", map[string]any{"appid": "app", "mchid": "mch", "out_trade_no": merchant, "transaction_id": verifiedTransactionID, "trade_state": "SUCCESS", "success_time": paidAt.Format(time.RFC3339Nano), "amount": map[string]any{"total": 1000, "currency": "CNY"}})
	late := httptest.NewRecorder()
	handler.ServeHTTP(late, commerceFundsCallbackRequest("/api/public/wechat-pay/callbacks/payment", lateBody, lateHeaders))
	if late.Code != http.StatusOK {
		t.Fatalf("late reconciled payment callback status=%d body=%s", late.Code, late.Body.String())
	}
	commerceFundsAssertPaid(t, ctx, pool, orderID, paymentID, merchant, 2)
	effectID, generation, riverJobID := commerceFundsAssertPushQueued(t, ctx, pool, orderID)
	deliveryLock.Lock()
	queuedDeliveries := len(deliveries)
	deliveryLock.Unlock()
	if queuedDeliveries != 0 {
		t.Fatalf("Provider was called before EER worker attempted the effect: deliveries=%d", queuedDeliveries)
	}
	// A later Product save must not rewrite or revoke this already accepted
	// paid delivery. The frozen revision/body remain the only source of its
	// business fields; target identity/version policy is checked separately.
	commerceConfiguration.value.PushType = "changed_after_acceptance"
	commerceConfiguration.value.Day = commerceFundsInt64(365)
	commerceConfiguration.value.Frequency = commerceFundsInt64(9)
	commerceConfiguration.value.Remark = "new-product-value"
	commerceConfiguration.value.Revision = 2
	if err = effectStore.RunAttempt(ctx, effectID, generation, riverJobID, commerceProvider); err != nil {
		t.Fatal(err)
	}
	commerceFundsAssertPushDelivered(t, ctx, pool, effectID, commerceTarget.SigningKey, &deliveryLock, deliveries)
	commerceFundsAssertOrderDeliveryHistoryRoute(t, ctx, pool, handler, merchant, now)

	// The provider must still reject a frozen delivery if the protected target
	// protocol or trusted identity-selection policy is revoked after acceptance.
	// These synthetic effects share the actual EER/River Provider path but must
	// never make a receiver call after that current-policy rejection.
	commerceFundsAssertTargetPolicyRejected(t, ctx, pool, uow, effectStore, commercePush, commerceProvider, commerceTargets, product, 2, "commerce-funds-target-version", func(target *outbound.CommercePushTarget) { target.Version = "legacy-v2" })
	commerceFundsAssertTargetPolicyRejected(t, ctx, pool, uow, effectStore, commercePush, commerceProvider, commerceTargets, product, 2, "commerce-funds-target-identity", func(target *outbound.CommercePushTarget) { target.BeneficiaryPhone.Scope = "phone:e164" })
	deliveryLock.Lock()
	policyRejectedDeliveries := len(deliveries)
	deliveryLock.Unlock()
	if policyRejectedDeliveries != 1 {
		t.Fatalf("revoked commerce target made a receiver call: deliveries=%d", policyRejectedDeliveries)
	}

	firstRefund := commerceFundsRequestRefund(t, handler, paymentID, 300, "commerce-funds-first-refund", "commerce-funds-first-refund-key", verifiedTransactionID)
	firstRefundBody, firstRefundHeaders := commerceFundsSignedCallback(t, platformKey, apiKey, "commerce-funds-refund-1", "REFUND.SUCCESS", map[string]any{"appid": "app", "mchid": "mch", "out_refund_no": firstRefund, "refund_id": "provider-refund-1", "refund_status": "SUCCESS", "success_time": now.Add(2 * time.Second).Format(time.RFC3339Nano), "amount": map[string]any{"refund": 300, "total": 1000, "currency": "CNY"}})
	firstRefundResponse := httptest.NewRecorder()
	handler.ServeHTTP(firstRefundResponse, commerceFundsCallbackRequest("/api/public/wechat-pay/callbacks/refund", firstRefundBody, firstRefundHeaders))
	if firstRefundResponse.Code != http.StatusOK {
		t.Fatalf("partial refund status=%d body=%s", firstRefundResponse.Code, firstRefundResponse.Body.String())
	}
	replayedRefund := httptest.NewRecorder()
	handler.ServeHTTP(replayedRefund, commerceFundsCallbackRequest("/api/public/wechat-pay/callbacks/refund", firstRefundBody, firstRefundHeaders))
	if replayedRefund.Code != http.StatusOK {
		t.Fatalf("duplicate partial refund status=%d body=%s", replayedRefund.Code, replayedRefund.Body.String())
	}
	firstEnd, firstUpdated := commerceFundsRefundedEntitlement(t, ctx, pool, orderID, 300)

	type refundAttempt struct {
		code     int
		refundNo string
	}
	attempts := make(chan refundAttempt, 2)
	var refundWait sync.WaitGroup
	for index := 0; index < 2; index++ {
		index := index
		refundWait.Add(1)
		go func() {
			defer refundWait.Done()
			result := commerceFundsRefundRequest(handler, paymentID, 700, "commerce-funds-final-refund-"+strconv.Itoa(index), "commerce-funds-final-refund-key-"+strconv.Itoa(index), verifiedTransactionID)
			attempts <- refundAttempt{code: result.code, refundNo: result.refundNo}
		}()
	}
	refundWait.Wait()
	close(attempts)
	var finalRefund string
	var accepted, conflicted int
	for attempt := range attempts {
		if attempt.code == http.StatusAccepted {
			accepted++
			finalRefund = attempt.refundNo
		} else if attempt.code == http.StatusConflict {
			conflicted++
		} else {
			t.Fatalf("concurrent refund status=%d", attempt.code)
		}
	}
	if accepted != 1 || conflicted != 1 || finalRefund == "" {
		t.Fatalf("concurrent refunds accepted=%d conflicted=%d refund=%q", accepted, conflicted, finalRefund)
	}
	finalRefundBody, finalRefundHeaders := commerceFundsSignedCallback(t, platformKey, apiKey, "commerce-funds-refund-2", "REFUND.SUCCESS", map[string]any{"appid": "app", "mchid": "mch", "out_refund_no": finalRefund, "refund_id": "provider-refund-2", "refund_status": "SUCCESS", "success_time": now.Add(3 * time.Second).Format(time.RFC3339Nano), "amount": map[string]any{"refund": 700, "total": 1000, "currency": "CNY"}})
	finalRefundResponse := httptest.NewRecorder()
	handler.ServeHTTP(finalRefundResponse, commerceFundsCallbackRequest("/api/public/wechat-pay/callbacks/refund", finalRefundBody, finalRefundHeaders))
	if finalRefundResponse.Code != http.StatusOK {
		t.Fatalf("final refund status=%d body=%s", finalRefundResponse.Code, finalRefundResponse.Body.String())
	}
	duplicateFinal := httptest.NewRecorder()
	handler.ServeHTTP(duplicateFinal, commerceFundsCallbackRequest("/api/public/wechat-pay/callbacks/refund", finalRefundBody, finalRefundHeaders))
	if duplicateFinal.Code != http.StatusOK {
		t.Fatalf("duplicate final refund status=%d body=%s", duplicateFinal.Code, duplicateFinal.Body.String())
	}
	commerceFundsAssertFinal(t, ctx, pool, orderID, paymentID, merchant, firstEnd, firstUpdated)
}

func commerceFundsInt64(value int64) *int64 { return &value }

func commerceFundsJSON(t *testing.T, value any) []byte {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return body
}
func commerceFundsObject(t *testing.T, response *httptest.ResponseRecorder, want int) map[string]any {
	t.Helper()
	if response.Code != want {
		t.Fatalf("status=%d body=%s want=%d", response.Code, response.Body.String(), want)
	}
	var object map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &object); err != nil {
		t.Fatal(err)
	}
	return object
}
func commerceFundsInt(t *testing.T, value map[string]any, key string) int64 {
	t.Helper()
	number, ok := value[key].(float64)
	if !ok || number < 1 || number != float64(int64(number)) {
		t.Fatalf("invalid %s=%#v", key, value[key])
	}
	return int64(number)
}
func commerceFundsString(t *testing.T, value map[string]any, key string) string {
	t.Helper()
	text, ok := value[key].(string)
	if !ok || text == "" {
		t.Fatalf("invalid %s=%#v", key, value[key])
	}
	return text
}
func commerceFundsCookie(t *testing.T, cookies []*http.Cookie, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie
		}
	}
	t.Fatalf("missing %s cookie", name)
	return nil
}

func commerceFundsRequestRefund(t *testing.T, handler http.Handler, paymentID, amount int64, refundNo, key, verifiedTransactionID string) string {
	t.Helper()
	result := commerceFundsRefundRequest(handler, paymentID, amount, refundNo, key, verifiedTransactionID)
	if result.code != http.StatusAccepted || result.refundNo != refundNo {
		t.Fatalf("refund status=%d refund=%q body=%s", result.code, result.refundNo, result.body)
	}
	return result.refundNo
}
func commerceFundsRefundRequest(handler http.Handler, paymentID, amount int64, refundNo, key, verifiedTransactionID string) struct {
	code     int
	refundNo string
	body     string
} {
	request := httptest.NewRequest(http.MethodPost, "/api/admin/payments/"+strconv.FormatInt(paymentID, 10)+"/refunds", bytes.NewReader(commerceFundsJSONNoTest(map[string]any{"amount_minor": amount, "refund_no": refundNo, "reason": "用户申请退款", "transaction_id_confirmation": verifiedTransactionID})))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", key)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var object map[string]any
	_ = json.Unmarshal(response.Body.Bytes(), &object)
	got, _ := object["out_refund_no"].(string)
	return struct {
		code     int
		refundNo string
		body     string
	}{code: response.Code, refundNo: got, body: response.Body.String()}
}
func commerceFundsJSONNoTest(value any) []byte {
	body, _ := json.Marshal(value)
	return body
}

func commerceFundsAssertPushRollback(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	var events, intents, effects, jobs int
	err := pool.QueryRow(ctx, `SELECT
  (SELECT count(*) FROM order_paid_events),
  (SELECT count(*) FROM outbound_commerce_push_intents),
  (SELECT count(*) FROM external_effects WHERE kind=$1),
  (SELECT count(*) FROM external_effect_jobs job JOIN external_effects effect ON effect.id=job.effect_id WHERE effect.kind=$1)`, effectport.KindCommerceProductPush).Scan(&events, &intents, &effects, &jobs)
	if err != nil || events != 0 || intents != 0 || effects != 0 || jobs != 0 {
		t.Fatalf("paid external push did not roll back events/intents/effects/jobs=%d/%d/%d/%d err=%v", events, intents, effects, jobs, err)
	}
}

// commerceFundsAssertOrderDeliveryHistoryRoute exercises the actual
// compatibility URL through Payment HTTP plus the stable Order/Outbound read
// Ports. The historical fixture deliberately reuses a V2 numeric order ID as
// an unrelated V3 native orders.id; only the distinct historical
// source_system/source_key can read the preserved row.
func commerceFundsAssertUnpaidOrderHasNoCommerceDeliveries(t *testing.T, handler http.Handler, merchant string) {
	t.Helper()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/wechat-pay/orders/"+merchant+"/external-push-deliveries", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"history_mapping_state":"current"`) || !strings.Contains(response.Body.String(), `"total":0`) || strings.Contains(response.Body.String(), `"unavailable"`) {
		t.Fatalf("unpaid native order delivery route status=%d body=%s", response.Code, response.Body.String())
	}
}

func commerceFundsAssertOrderDeliveryHistoryRoute(t *testing.T, ctx context.Context, pool *pgxpool.Pool, handler http.Handler, merchant string, now time.Time) {
	t.Helper()
	current := httptest.NewRecorder()
	handler.ServeHTTP(current, httptest.NewRequest(http.MethodGet, "/api/admin/wechat-pay/orders/"+merchant+"/external-push-deliveries", nil))
	if current.Code != http.StatusOK || !strings.Contains(current.Body.String(), "\"source\":\"current\"") || !strings.Contains(current.Body.String(), "\"provider_accepted\"") || !strings.Contains(current.Body.String(), "\"response_status\":204") || !strings.Contains(current.Body.String(), "\"result_code\":\"provider_accepted\"") || strings.Contains(current.Body.String(), "\"history_mapping_state\":\"pending\"") {
		t.Fatalf("current delivery route status=%d body=%s", current.Code, current.Body.String())
	}

	const sourceOrderID int64 = 909001
	const historySourceSystem = "aicrm-commerce-history-fixture"
	var ownerID int64
	if err := pool.QueryRow(ctx, `SELECT id FROM customers ORDER BY id LIMIT 1`).Scan(&ownerID); err != nil {
		t.Fatal(err)
	}
	historicalOrderDigest := sha256.Sum256([]byte("historical-order-909001"))
	if _, err := pool.Exec(ctx, `INSERT INTO orders(id,provider,source_system,source_key,merchant_order_no,provider_transaction_no,payer_customer_id,beneficiary_customer_id,amount_minor,refunded_minor,currency,status,record_origin,effect_eligible,source_row_digest,version,created_at,updated_at)
OVERRIDING SYSTEM VALUE VALUES
($1,'wechat_pay','v3-checkout','native-collision-909001','native-collision-909001','',$4,$4,100,0,'CNY','paid','native',TRUE,NULL,2,$2,$2),
($3,'wechat_pay','commerce-history','909001','history-collision-909001','',NULL,NULL,100,0,'CNY','paid','history',FALSE,$5,1,$2,$2)`, sourceOrderID, now.UTC(), sourceOrderID+1, ownerID, historicalOrderDigest[:]); err != nil {
		t.Fatal(err)
	}
	wrongScopeDigest := sha256.Sum256([]byte("historical-order-wrong-scope-909001"))
	if _, err := pool.Exec(ctx, `INSERT INTO orders(provider,source_system,source_key,merchant_order_no,provider_transaction_no,payer_customer_id,beneficiary_customer_id,amount_minor,refunded_minor,currency,status,record_origin,effect_eligible,source_row_digest,version,created_at,updated_at)
VALUES('wechat_pay','another-history-scope','909001','history-scope-miss-909001','',NULL,NULL,100,0,'CNY','paid','history',FALSE,$1,1,$2,$2)`, wrongScopeDigest[:], now.UTC()); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("collision-paid-event"))
	if _, err := pool.Exec(ctx, `INSERT INTO order_paid_events(order_id,order_version,source_digest,occurred_at) VALUES($1,2,$2,$3)`, sourceOrderID, digest[:], now.UTC()); err != nil {
		t.Fatal(err)
	}
	rowDigest := sha256.Sum256([]byte("legacy-delivery-909001"))
	var historyRowID, batchID int64
	if err := pool.QueryRow(ctx, `INSERT INTO outbound_commerce_push_history_rows(source_system,source_kind,source_id,source_digest,source_delivery_id,source_event_type,source_target_type,source_target_id,source_order_kind,source_order_scope,source_order_key,source_order_id,source_state,source_attempt_count,source_effect_job_id,source_response_status,source_error_message,source_response_body_protected,source_created_at,source_updated_at,outcome,reason_code,read_only)
VALUES($1,'delivery',909001,$2,'legacy-delivery-909001','transaction.paid','product','101','wechat_pay_order','commerce-history','909001',909001,'failed',3,77,502,'legacy upstream timeout',TRUE,$3,$3,'pending','product_mapping_unavailable',TRUE) RETURNING id`, historySourceSystem, rowDigest[:], now.UTC()).Scan(&historyRowID); err != nil {
		t.Fatal(err)
	}

	simulatedDigest := sha256.Sum256([]byte("legacy-delivery-simulated-909001"))
	var simulatedRowID int64
	if err := pool.QueryRow(ctx, "INSERT INTO outbound_commerce_push_history_rows(source_system,source_kind,source_id,source_digest,source_delivery_id,source_event_type,source_target_type,source_target_id,source_order_kind,source_order_scope,source_order_key,source_order_id,source_state,source_attempt_count,source_effect_job_id,source_effect_state,source_created_at,source_updated_at,outcome,reason_code,read_only) VALUES($1,'delivery',909003,$2,'legacy-delivery-simulated','transaction.paid','product','101','wechat_pay_order','commerce-history','909001',909001,'skipped',1,78,'simulated',$3,$3,'pending','product_mapping_unavailable',TRUE) RETURNING id", historySourceSystem, simulatedDigest[:], now.UTC()).Scan(&simulatedRowID); err != nil {
		t.Fatal(err)
	}
	cancelledDigest := sha256.Sum256([]byte("legacy-delivery-cancelled-909001"))
	var cancelledRowID int64
	if err := pool.QueryRow(ctx, `INSERT INTO outbound_commerce_push_history_rows(source_system,source_kind,source_id,source_digest,source_delivery_id,source_event_type,source_target_type,source_target_id,source_order_kind,source_order_scope,source_order_key,source_order_id,source_state,source_attempt_count,source_effect_job_id,source_effect_state,source_created_at,source_updated_at,outcome,reason_code,read_only) VALUES($1,'delivery',909004,$2,'legacy-delivery-cancelled','transaction.paid','product','101','wechat_pay_order','commerce-history','909001',909001,'skipped',1,79,'cancelled',$3,$3,'pending','product_mapping_unavailable',TRUE) RETURNING id`, historySourceSystem, cancelledDigest[:], now.UTC()).Scan(&cancelledRowID); err != nil {
		t.Fatal(err)
	}
	manifestDigest := sha256.Sum256([]byte("legacy-delivery-batch-909001"))
	if err := pool.QueryRow(ctx, `INSERT INTO outbound_commerce_push_history_batches(source_system,source_revision,manifest_digest,snapshot_at,status,input_count,imported_count,pending_count,excluded_count,applied_at) VALUES($1,$2,$3,$4,'applied',3,0,3,0,$4) RETURNING id`, historySourceSystem, strings.Repeat("d", 40), manifestDigest[:], now.UTC()).Scan(&batchID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO outbound_commerce_push_history_batch_rows(batch_id,source_row_id,source_digest) VALUES($1,$2,$3)`, batchID, historyRowID, rowDigest[:]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "INSERT INTO outbound_commerce_push_history_batch_rows(batch_id,source_row_id,source_digest) VALUES($1,$2,$3)", batchID, simulatedRowID, simulatedDigest[:]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "INSERT INTO outbound_commerce_push_history_batch_rows(batch_id,source_row_id,source_digest) VALUES($1,$2,$3)", batchID, cancelledRowID, cancelledDigest[:]); err != nil {
		t.Fatal(err)
	}
	wrongKindDigest := sha256.Sum256([]byte("legacy-delivery-wrong-kind-909001"))
	var wrongKindRowID, wrongKindBatchID int64
	if err := pool.QueryRow(ctx, `INSERT INTO outbound_commerce_push_history_rows(source_system,source_kind,source_id,source_digest,source_delivery_id,source_event_type,source_target_type,source_target_id,source_order_kind,source_order_scope,source_order_key,source_order_id,source_state,source_attempt_count,source_created_at,source_updated_at,outcome,reason_code,read_only)
VALUES($1,'delivery',909002,$2,'legacy-delivery-wrong-kind','transaction.paid','product','101','wechat_shop_order','commerce-history','909001',909001,'failed',1,$3,$3,'pending','product_mapping_unavailable',TRUE) RETURNING id`, historySourceSystem, wrongKindDigest[:], now.UTC()).Scan(&wrongKindRowID); err != nil {
		t.Fatal(err)
	}
	wrongKindManifest := sha256.Sum256([]byte("legacy-delivery-wrong-kind-batch"))
	if err := pool.QueryRow(ctx, `INSERT INTO outbound_commerce_push_history_batches(source_system,source_revision,manifest_digest,snapshot_at,status,input_count,imported_count,pending_count,excluded_count,applied_at) VALUES($1,$2,$3,$4,'applied',1,0,1,0,$4) RETURNING id`, historySourceSystem, strings.Repeat("e", 40), wrongKindManifest[:], now.UTC()).Scan(&wrongKindBatchID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO outbound_commerce_push_history_batch_rows(batch_id,source_row_id,source_digest) VALUES($1,$2,$3)`, wrongKindBatchID, wrongKindRowID, wrongKindDigest[:]); err != nil {
		t.Fatal(err)
	}

	collision := httptest.NewRecorder()
	handler.ServeHTTP(collision, httptest.NewRequest(http.MethodGet, "/api/admin/wechat-pay/orders/native-collision-909001/external-push-deliveries", nil))
	if collision.Code != http.StatusOK || strings.Contains(collision.Body.String(), "legacy-delivery-909001") || !strings.Contains(collision.Body.String(), `"total":0`) {
		t.Fatalf("numeric source/V3 id collision attached legacy delivery status=%d body=%s", collision.Code, collision.Body.String())
	}
	history := httptest.NewRecorder()
	handler.ServeHTTP(history, httptest.NewRequest(http.MethodGet, "/api/admin/wechat-pay/orders/history-collision-909001/external-push-deliveries", nil))
	if history.Code != http.StatusOK || !strings.Contains(history.Body.String(), "\"source\":\"history\"") || !strings.Contains(history.Body.String(), "\"legacy_delivery_id\":\"legacy-delivery-909001\"") || !strings.Contains(history.Body.String(), "\"legacy_effect_job_id\":77") || !strings.Contains(history.Body.String(), "\"external_effect_id\":null") || !strings.Contains(history.Body.String(), "\"provider_call_attempted\":true") || !strings.Contains(history.Body.String(), "\"real_external_call_executed\":true") || !strings.Contains(history.Body.String(), "\"provider_result_received\":true") || !strings.Contains(history.Body.String(), "\"response_status\":502") || !strings.Contains(history.Body.String(), "legacy upstream timeout") || !strings.Contains(history.Body.String(), "\"response_body_protected\":true") || !strings.Contains(history.Body.String(), "\"total\":3") || strings.Contains(history.Body.String(), "legacy-delivery-wrong-kind") {
		t.Fatalf("mapped history delivery route status=%d body=%s", history.Code, history.Body.String())
	}
	commerceFundsAssertHistoricalNoCallEvidence(t, history.Body.Bytes(), "legacy-delivery-simulated")
	commerceFundsAssertHistoricalUnknownCallEvidence(t, history.Body.Bytes(), "legacy-delivery-cancelled")
	wrongScope := httptest.NewRecorder()
	handler.ServeHTTP(wrongScope, httptest.NewRequest(http.MethodGet, "/api/admin/wechat-pay/orders/history-scope-miss-909001/external-push-deliveries", nil))
	if wrongScope.Code != http.StatusOK || !strings.Contains(wrongScope.Body.String(), `"history_mapping_state":"pending"`) || !strings.Contains(wrongScope.Body.String(), `"total":0`) || strings.Contains(wrongScope.Body.String(), "legacy-delivery-909001") {
		t.Fatalf("unmapped historical source scope status=%d body=%s", wrongScope.Code, wrongScope.Body.String())
	}
}

// commerceFundsAssertHistoricalNoCallEvidence distinguishes a legacy simulated
// record from a response-bearing delivery. The frozen V2 settlement path can
// increment attempt_count for simulated work, but records the no-call terminal
// state separately; this browser-compatible projection must retain false.
func commerceFundsAssertHistoricalNoCallEvidence(t *testing.T, body []byte, deliveryID string) {
	t.Helper()
	var page map[string]any
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatal(err)
	}
	items, ok := page["items"].([]any)
	if !ok {
		t.Fatalf("history delivery page has no items: %s", body)
	}
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok || item["legacy_delivery_id"] != deliveryID {
			continue
		}
		if item["attempt_count"] != float64(1) || item["provider_call_attempted"] != false || item["real_external_call_executed"] != false || item["provider_result_received"] != false || item["response_status"] != nil {
			t.Fatalf("simulated legacy delivery invented Provider call facts: %#v", item)
		}
		return
	}
	t.Fatalf("simulated legacy delivery %q was absent: %s", deliveryID, body)
}

// commerceFundsAssertHistoricalUnknownCallEvidence keeps a final cancelled
// legacy effect distinguishable from V2's explicit simulated no-call outcome.
// Cancellation may follow failed_retryable work, so its absence of a response
// cannot answer whether the Provider was reached.
func commerceFundsAssertHistoricalUnknownCallEvidence(t *testing.T, body []byte, deliveryID string) {
	t.Helper()
	var page map[string]any
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatal(err)
	}
	items, ok := page["items"].([]any)
	if !ok {
		t.Fatalf("history delivery page has no items: %s", body)
	}
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok || item["legacy_delivery_id"] != deliveryID {
			continue
		}
		for _, field := range []string{"provider_call_attempted", "real_external_call_executed", "provider_result_received", "response_status"} {
			value, exists := item[field]
			if !exists || value != nil {
				t.Fatalf("cancelled legacy delivery invented known Provider fact %s=%#v: %#v", field, value, item)
			}
		}
		return
	}
	t.Fatalf("cancelled legacy delivery %q was absent: %s", deliveryID, body)
}

// commerceFundsAssertTargetPolicyRejected creates a new explicit synthetic
// operation, changes only current protected target policy, and proves EER
// completes it without a network call. Product business values are deliberately
// not touched here: their acceptance-time payload is immutable instead.
func commerceFundsAssertTargetPolicyRejected(t *testing.T, ctx context.Context, pool *pgxpool.Pool, uow platformport.UnitOfWork, effectsStore *effects.Repository, service *outbound.CommercePushService, provider *outbound.CommercePushProvider, targets *commerceFundsPushTargets, product productport.CheckoutProduct, revision int64, key string, mutate func(*outbound.CommercePushTarget)) {
	t.Helper()
	if uow == nil || effectsStore == nil || service == nil || provider == nil || targets == nil || mutate == nil {
		t.Fatal("target-policy fixture is incomplete")
	}
	digest := sha256.Sum256([]byte(key))
	var accepted productport.ExternalPushTest
	if err := uow.Within(ctx, func(tx context.Context) error {
		var acceptErr error
		accepted, acceptErr = service.AcceptExternalPushTestWithin(tx, productport.ExternalPushTestIntent{ProductID: product.ID, ProductKind: productport.ExternalPushServicePeriod, ConfigurationReference: "commerce-funds-target", ConfigurationRevision: revision, ReceiptKeyDigest: digest})
		return acceptErr
	}); err != nil || accepted.EffectID == "" || (accepted.State != "accepted" && accepted.State != "queued") {
		t.Fatalf("accept policy-rejection fixture accepted=%+v err=%v", accepted, err)
	}
	var effectID, generation, riverJobID int64
	if err := pool.QueryRow(ctx, `SELECT effect.id,effect.generation,job.river_job_id
FROM external_effects effect
JOIN external_effect_jobs job ON job.effect_id=effect.id AND job.generation=effect.generation
WHERE effect.id=substring($1 FROM 5)::bigint`, accepted.EffectID).Scan(&effectID, &generation, &riverJobID); err != nil {
		t.Fatalf("read policy-rejection effect: %v", err)
	}
	original := targets.target
	mutate(&targets.target)
	err := effectsStore.RunAttempt(ctx, effectID, generation, riverJobID, provider)
	targets.target = original
	if err != nil {
		t.Fatalf("run policy-rejection effect: %v", err)
	}
	var state string
	if err := pool.QueryRow(ctx, `SELECT state FROM outbound_commerce_push_intents WHERE effect_id=$1`, accepted.EffectID).Scan(&state); err != nil || state != "final_failed" {
		t.Fatalf("protected target mutation state=%q err=%v", state, err)
	}
}

func commerceFundsAssertPushQueued(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orderID int64) (int64, int64, int64) {
	t.Helper()
	var paidEvents, intents, audits, outbox int
	var effectID, generation, riverJobID int64
	var intentState, effectState string
	err := pool.QueryRow(ctx, `SELECT
  (SELECT count(*) FROM order_paid_events WHERE order_id=$1),
  (SELECT count(*) FROM outbound_commerce_push_intents intent JOIN order_paid_events event ON event.id=intent.order_paid_event_id WHERE event.order_id=$1),
  (SELECT count(*) FROM outbound_commerce_push_audit_events),
  (SELECT count(*) FROM outbound_commerce_push_outbox),
  effect.id,effect.generation,job.river_job_id,intent.state,effect.state
FROM outbound_commerce_push_intents intent
JOIN order_paid_events event ON event.id=intent.order_paid_event_id
JOIN external_effects effect ON effect.id=substring(intent.effect_id FROM 5)::bigint
JOIN external_effect_jobs job ON job.effect_id=effect.id AND job.generation=effect.generation
WHERE event.order_id=$1`, orderID).Scan(&paidEvents, &intents, &audits, &outbox, &effectID, &generation, &riverJobID, &intentState, &effectState)
	if err != nil || paidEvents != 1 || intents != 1 || audits != 1 || outbox != 1 || effectID < 1 || generation < 1 || riverJobID < 1 || intentState != "queued" || effectState != string(effectport.StateQueued) {
		t.Fatalf("paid push queue facts paid_events/intents/audits/outbox/effect/generation/job/intent/effect=%d/%d/%d/%d/%d/%d/%d/%q/%q err=%v", paidEvents, intents, audits, outbox, effectID, generation, riverJobID, intentState, effectState, err)
	}
	return effectID, generation, riverJobID
}

func commerceFundsAssertPushDelivered(t *testing.T, ctx context.Context, pool *pgxpool.Pool, effectID int64, signingKey []byte, lock *sync.Mutex, deliveries []commerceFundsPushDelivery) {
	t.Helper()
	lock.Lock()
	deferred := append([]commerceFundsPushDelivery(nil), deliveries...)
	lock.Unlock()
	if len(deferred) != 1 {
		t.Fatalf("signed commerce provider deliveries=%d", len(deferred))
	}
	delivery := deferred[0]
	mac := hmac.New(sha256.New, signingKey)
	_, _ = mac.Write([]byte(delivery.timestamp))
	_, _ = mac.Write([]byte("."))
	_, _ = mac.Write(delivery.body)
	expectedSignature := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if delivery.event != "transaction.paid" || delivery.deliveryID == "" || delivery.timestamp == "" || delivery.requestTarget != "/legacy/push?tenant=commerce&mode=paid" || !hmac.Equal([]byte(delivery.signature), []byte(expectedSignature)) {
		t.Fatalf("legacy signed delivery contract event=%q delivery_present=%t timestamp_present=%t request_target=%q signature_match=%t", delivery.event, delivery.deliveryID != "", delivery.timestamp != "", delivery.requestTarget, hmac.Equal([]byte(delivery.signature), []byte(expectedSignature)))
	}
	var body struct {
		PhoneNumber string `json:"phone_number"`
		PushType    string `json:"type"`
		Day         *int64 `json:"day"`
		Frequency   *int64 `json:"frequency"`
		Remark      string `json:"remark"`
		Event       string `json:"event"`
		DeliveryID  string `json:"delivery_id"`
		Order       struct {
			Status     string `json:"status"`
			PaidAmount int64  `json:"paid_amount"`
		} `json:"order"`
		Product struct {
			Price int64 `json:"price"`
		} `json:"product"`
		Buyer       struct{ ID, OpenID, UnionID, Phone string } `json:"buyer"`
		Transaction struct {
			TransactionID string `json:"transaction_id"`
			TradeState    string `json:"trade_state"`
			SuccessTime   string `json:"success_time"`
		} `json:"transaction"`
		DomainEventOutboxID int64 `json:"domain_event_outbox_id"`
	}
	var raw map[string]json.RawMessage
	if json.Unmarshal(delivery.body, &body) != nil || json.Unmarshal(delivery.body, &raw) != nil || body.PhoneNumber != "13800138000" || body.PushType != "service_period" || body.Day == nil || *body.Day != 30 || body.Frequency == nil || *body.Frequency != 1 || body.Remark != "commerce-funds-fixture" || body.Event != "transaction.paid" || body.DeliveryID != delivery.deliveryID || body.Order.Status != "paid" || body.Order.PaidAmount != 1000 || body.Product.Price != 1200 || body.Buyer.ID != "fixture-buyer" || body.Buyer.OpenID != "fixt***enid" || body.Buyer.UnionID != "fixture-unionid" || body.Buyer.Phone != "13800138000" || body.Transaction.TransactionID != "tx-commerce-funds" || body.Transaction.TradeState != "SUCCESS" || body.Transaction.SuccessTime == "" || body.DomainEventOutboxID < 1 {
		t.Fatalf("legacy paid payload did not preserve frozen member, transaction, and payer facts")
	}
	if _, found := raw["custom_params"]; found {
		t.Fatalf("synthetic-only custom params leaked into a paid delivery")
	}
	var expectedOutboxID int64
	var expectedOccurredAt time.Time
	err := pool.QueryRow(ctx, `SELECT outbox.id,event.occurred_at
FROM outbound_commerce_push_intents intent
JOIN order_paid_events event ON event.id=intent.order_paid_event_id
JOIN order_outbox outbox ON outbox.idempotency_key=('order.paid.v1:' || event.id::text)
WHERE intent.effect_id=$1`, "eer_"+strconv.FormatInt(effectID, 10)).Scan(&expectedOutboxID, &expectedOccurredAt)
	if err != nil || body.DomainEventOutboxID != expectedOutboxID || body.Transaction.SuccessTime != expectedOccurredAt.UTC().Format(time.RFC3339) {
		t.Fatalf("paid delivery did not carry its immutable order outbox fact id=%d want=%d occurred=%q want=%q err=%v", body.DomainEventOutboxID, expectedOutboxID, body.Transaction.SuccessTime, expectedOccurredAt.UTC().Format(time.RFC3339), err)
	}
	var intentState, effectState string
	var calls int
	var attempted, executed, received bool
	var responseStatus *int
	var resultCode *string
	err = pool.QueryRow(ctx, "SELECT intent.state,effect.state,effect.attempt_count,attempt.call_attempted,attempt.real_external_call_executed,intent.provider_result_received,intent.provider_response_status,intent.provider_result_code "+
		"FROM outbound_commerce_push_intents intent "+
		"JOIN external_effects effect ON effect.id=substring(intent.effect_id FROM 5)::bigint "+
		"JOIN external_effect_attempts attempt ON attempt.effect_id=effect.id AND attempt.number=1 "+
		"WHERE effect.id=$1", effectID).Scan(&intentState, &effectState, &calls, &attempted, &executed, &received, &responseStatus, &resultCode)
	if err != nil || intentState != "provider_accepted" || effectState != string(effectport.StateExecuted) || calls != 1 || !attempted || !executed || !received || responseStatus == nil || *responseStatus != http.StatusNoContent || resultCode == nil || *resultCode != "provider_accepted" {
		t.Fatalf("commerce effect completion state/effect/calls/attempted/executed/received/response/code=%q/%q/%d/%t/%t/%t/%v/%v err=%v", intentState, effectState, calls, attempted, executed, received, responseStatus, resultCode, err)
	}
}

func commerceFundsAssertReserved(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orderID, paymentID, claimID int64) {
	t.Helper()
	var orderStatus, paymentStatus, redemptionStatus, claimStatus string
	var payable int64
	err := pool.QueryRow(ctx, "SELECT (SELECT status FROM orders WHERE id=$1),(SELECT status FROM payments WHERE id=$2),(SELECT status FROM coupon_order_redemptions WHERE claim_id=$3),(SELECT status FROM coupon_customer_claims WHERE id=$3),(SELECT payable_amount_minor FROM order_checkout_snapshots WHERE order_id=$1)", orderID, paymentID, claimID).Scan(&orderStatus, &paymentStatus, &redemptionStatus, &claimStatus, &payable)
	if err != nil || orderStatus != "pending_payment" || paymentStatus != "awaiting_prepay" || redemptionStatus != "reserved" || claimStatus != "reserved" || payable != 1000 {
		t.Fatalf("reserved order=%q payment=%q redemption=%q claim=%q payable=%d err=%v", orderStatus, paymentStatus, redemptionStatus, claimStatus, payable, err)
	}
}
func commerceFundsAssertRollback(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orderID, paymentID int64, merchant string, callbacks int) {
	t.Helper()
	var orderStatus, paymentStatus, redemptionStatus, claimStatus string
	var callbackCount, entitlementCount, consumeCount int
	err := pool.QueryRow(ctx, "SELECT (SELECT status FROM orders WHERE id=$1),(SELECT status FROM payments WHERE id=$2),(SELECT status FROM coupon_order_redemptions WHERE order_reference=$3),(SELECT status FROM coupon_customer_claims WHERE id=(SELECT claim_id FROM coupon_order_redemptions WHERE order_reference=$3)),(SELECT count(*) FROM payment_callback_receipts),(SELECT count(*) FROM order_service_entitlements),(SELECT count(*) FROM coupon_redemption_operation_receipts receipt JOIN coupon_order_redemptions redemption ON redemption.id=receipt.redemption_id WHERE redemption.order_reference=$3 AND receipt.operation='consume')", orderID, paymentID, merchant).Scan(&orderStatus, &paymentStatus, &redemptionStatus, &claimStatus, &callbackCount, &entitlementCount, &consumeCount)
	if err != nil || orderStatus != "pending_payment" || paymentStatus != "awaiting_prepay" || redemptionStatus != "reserved" || claimStatus != "reserved" || callbackCount != callbacks || entitlementCount != 0 || consumeCount != 0 {
		t.Fatalf("rollback order=%q payment=%q redemption=%q claim=%q callbacks=%d entitlements=%d consumes=%d err=%v", orderStatus, paymentStatus, redemptionStatus, claimStatus, callbackCount, entitlementCount, consumeCount, err)
	}
}
func commerceFundsAssertPaid(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orderID, paymentID int64, merchant string, paymentCallbacks int) {
	t.Helper()
	var orderStatus, paymentStatus, redemptionStatus, claimStatus, entitlementStatus string
	var callbackCount, grantCount, consumeCount, paidEvents, paidOutbox int
	err := pool.QueryRow(ctx, "SELECT (SELECT status FROM orders WHERE id=$1),(SELECT status FROM payments WHERE id=$2),(SELECT status FROM coupon_order_redemptions WHERE order_reference=$3),(SELECT status FROM coupon_customer_claims WHERE id=(SELECT claim_id FROM coupon_order_redemptions WHERE order_reference=$3)),(SELECT status FROM order_service_entitlements WHERE last_order_id=$1),(SELECT count(*) FROM payment_callback_receipts),(SELECT count(*) FROM order_entitlement_fulfillment_receipts WHERE operation='grant' AND source_order_id=$1),(SELECT count(*) FROM coupon_redemption_operation_receipts receipt JOIN coupon_order_redemptions redemption ON redemption.id=receipt.redemption_id WHERE redemption.order_reference=$3 AND receipt.operation='consume'),(SELECT count(*) FROM order_paid_events WHERE order_id=$1),(SELECT count(*) FROM order_outbox WHERE aggregate_id=$1 AND event_type='order.paid.v1')", orderID, paymentID, merchant).Scan(&orderStatus, &paymentStatus, &redemptionStatus, &claimStatus, &entitlementStatus, &callbackCount, &grantCount, &consumeCount, &paidEvents, &paidOutbox)
	if err != nil || orderStatus != "paid" || paymentStatus != "paid" || redemptionStatus != "consumed" || claimStatus != "redeemed" || entitlementStatus != "active" || callbackCount != paymentCallbacks || grantCount != 1 || consumeCount != 1 || paidEvents != 1 || paidOutbox != 1 {
		t.Fatalf("paid order=%q payment=%q redemption=%q claim=%q entitlement=%q callbacks=%d grants=%d consumes=%d paid_events=%d paid_outbox=%d err=%v", orderStatus, paymentStatus, redemptionStatus, claimStatus, entitlementStatus, callbackCount, grantCount, consumeCount, paidEvents, paidOutbox, err)
	}
}
func commerceFundsRefundedEntitlement(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orderID, amount int64) (time.Time, time.Time) {
	t.Helper()
	var status string
	var endAt, updatedAt time.Time
	var firstAmount int64
	err := pool.QueryRow(ctx, "SELECT entitlement.status,entitlement.end_at,entitlement.updated_at,receipt.refund_amount_minor FROM order_service_entitlements entitlement JOIN order_entitlement_fulfillment_receipts receipt ON receipt.operation='refund' AND receipt.source_order_id=$1 WHERE entitlement.last_order_id=$1", orderID).Scan(&status, &endAt, &updatedAt, &firstAmount)
	if err != nil || status != "refunded" || firstAmount != amount {
		t.Fatalf("partial refund entitlement status=%q amount=%d end=%s updated=%s err=%v", status, firstAmount, endAt, updatedAt, err)
	}
	return endAt, updatedAt
}
func commerceFundsAssertFinal(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orderID, paymentID int64, merchant string, firstEnd, firstUpdated time.Time) {
	t.Helper()
	var orderStatus, paymentStatus, redemptionStatus, entitlementStatus string
	var completedRefunds, entitlementReceipts, callbackReceipts int
	var endAt, updatedAt time.Time
	err := pool.QueryRow(ctx, "SELECT (SELECT status FROM orders WHERE id=$1),(SELECT status FROM payments WHERE id=$2),(SELECT status FROM coupon_order_redemptions WHERE order_reference=$3),(SELECT status FROM order_service_entitlements WHERE last_order_id=$1),(SELECT count(*) FROM payment_refunds WHERE payment_id=$2 AND status='completed'),(SELECT count(*) FROM order_entitlement_fulfillment_receipts WHERE operation='refund' AND source_order_id=$1),(SELECT count(*) FROM payment_callback_receipts),(SELECT end_at FROM order_service_entitlements WHERE last_order_id=$1),(SELECT updated_at FROM order_service_entitlements WHERE last_order_id=$1)", orderID, paymentID, merchant).Scan(&orderStatus, &paymentStatus, &redemptionStatus, &entitlementStatus, &completedRefunds, &entitlementReceipts, &callbackReceipts, &endAt, &updatedAt)
	if err != nil || orderStatus != "refunded" || paymentStatus != "paid" || redemptionStatus != "consumed" || entitlementStatus != "refunded" || completedRefunds != 2 || entitlementReceipts != 1 || callbackReceipts != 4 || !endAt.Equal(firstEnd) || !updatedAt.Equal(firstUpdated) {
		t.Fatalf("final order=%q payment=%q redemption=%q entitlement=%q refunds=%d receipts=%d callbacks=%d end=%s updated=%s err=%v", orderStatus, paymentStatus, redemptionStatus, entitlementStatus, completedRefunds, entitlementReceipts, callbackReceipts, endAt, updatedAt, err)
	}
}

func commerceFundsSignedCallback(t *testing.T, platformKey *rsa.PrivateKey, apiKey []byte, eventID, eventType string, payload map[string]any) ([]byte, http.Header) {
	t.Helper()
	plain := commerceFundsJSON(t, payload)
	block, err := aes.NewCipher(apiKey)
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("nonce:" + eventID))
	resourceNonce := hex.EncodeToString(digest[:6])
	associated := "transaction"
	ciphertext := base64.StdEncoding.EncodeToString(gcm.Seal(nil, []byte(resourceNonce), plain, []byte(associated)))
	body := commerceFundsJSON(t, map[string]any{"id": eventID, "event_type": eventType, "resource": map[string]string{"algorithm": "AEAD_AES_256_GCM", "ciphertext": ciphertext, "nonce": resourceNonce, "associated_data": associated}})
	timestamp := strconv.FormatInt(time.Now().UTC().Unix(), 10)
	headerNonce := "local-" + hex.EncodeToString(digest[6:12])
	signature := commerceFundsSign(t, platformKey, timestamp+"\n"+headerNonce+"\n"+string(body)+"\n")
	return body, http.Header{"Wechatpay-Timestamp": {timestamp}, "Wechatpay-Nonce": {headerNonce}, "Wechatpay-Serial": {"local-platform"}, "Wechatpay-Signature": {signature}}
}
func commerceFundsSign(t *testing.T, key *rsa.PrivateKey, message string) string {
	t.Helper()
	digest := sha256.Sum256([]byte(message))
	signature, err := rsa.SignPKCS1v15(cryptorand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(signature)
}
func commerceFundsCallbackRequest(path string, body []byte, headers http.Header) *http.Request {
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	for key, values := range headers {
		request.Header[key] = append([]string(nil), values...)
	}
	return request
}

// TestPostgreSQLProductExternalPushTestHTTPReturnsReceiverDeliveryID runs the
// admin test-button POST through Product's real HTTP handler, then executes
// its accepted EER effect against a local loopback-only receiver. The returned
// correlation value must be the same legacy delivery_id the receiver sees;
// accepted state itself remains distinct from Provider delivery proof.
func TestPostgreSQLProductExternalPushTestHTTPReturnsReceiverDeliveryID(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	wrapped, err := platformpostgres.Wrap(pool, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	products, err := productstore.NewPostgreSQL(pool, uow)
	if err != nil {
		t.Fatal(err)
	}

	var productID int64
	if err = pool.QueryRow(ctx, `INSERT INTO products(product_code,name,price_minor,currency,stock_quantity,created_by,legacy_admin_projection)
VALUES('push-test-delivery-id','测试推送回执商品',1200,'CNY',1,1,'{"schema_version":1,"status":"enabled","enabled":true}'::jsonb) RETURNING id`).Scan(&productID); err != nil {
		t.Fatal(err)
	}

	var receiverLock sync.Mutex
	var receiverDeliveryID string
	receiver := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, readErr := io.ReadAll(http.MaxBytesReader(writer, request.Body, 64<<10))
		if readErr != nil {
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		var payload struct {
			DeliveryID string `json:"delivery_id"`
		}
		if json.Unmarshal(body, &payload) != nil || payload.DeliveryID == "" || request.Header.Get("X-AICRM-Delivery-Id") != payload.DeliveryID {
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		receiverLock.Lock()
		receiverDeliveryID = payload.DeliveryID
		receiverLock.Unlock()
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer receiver.Close()

	target := outbound.CommercePushTarget{
		Reference: "product-test-delivery-target", Slot: "product-test-delivery-slot", Endpoint: receiver.URL + "/legacy/test", SigningKey: []byte("product-test-delivery-signing-key"), Version: "legacy-v1", TenantID: "aicrm", AllowLoopbackHTTP: true,
		BuyerID:          outbound.CommercePushIdentity{Kind: identitydomain.KindWeComExternalUserID, Scope: "wecom-corp:commerce-fixture"},
		BuyerOpenID:      outbound.CommercePushIdentity{Kind: identitydomain.KindMPOpenID, Scope: "wechat-app:commerce-fixture"},
		BuyerUnionID:     outbound.CommercePushIdentity{Kind: identitydomain.KindUnionID, Scope: "wechat-open-platform:commerce-fixture"},
		BuyerPhone:       outbound.CommercePushIdentity{Kind: identitydomain.KindPhone, Scope: "phone:cn11"},
		BeneficiaryPhone: outbound.CommercePushIdentity{Kind: identitydomain.KindPhone, Scope: "phone:cn11"},
	}
	if err = outbound.ValidateCommercePushTarget(target); err != nil {
		t.Fatal(err)
	}
	if err = uow.Within(ctx, func(tx context.Context) error {
		_, saveErr := products.SaveCommerceExternalPushConfiguration(tx, productport.ExternalPushConfiguration{
			ProductID: productport.ID(productID), ProductKind: productport.ExternalPushWeChatPay, Enabled: true, ConfigurationReference: target.Reference,
			PushType: "paid_notify", CustomParams: map[string]any{"fixture": "test"},
		}, time.Now().UTC())
		return saveErr
	}); err != nil {
		t.Fatal(err)
	}

	workers := river.NewWorkers()
	effectsModule := effects.NewModuleRegistration()
	if err = effectsModule.RegisterWorkers(workers); err != nil {
		t.Fatal(err)
	}
	insertClient, err := platformjobqueue.NewInsertClient(pool, workers)
	if err != nil {
		t.Fatal(err)
	}
	effectStore, err := effects.NewRepository(pool, insertClient)
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := outbound.NewCommercePayloadAESGCM(base64.RawStdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")))
	if err != nil {
		t.Fatal(err)
	}
	targets := &commerceFundsPushTargets{target: target}
	commerce, err := outbound.NewCommercePushService(pool, uow, effectStore, commerceFundsProductConfigurationReader{repository: products}, commerceFundsPushIdentityReader{}, targets, cipher)
	if err != nil {
		t.Fatal(err)
	}
	completion, err := outbound.NewCommercePushCompletionSink(commerce)
	if err != nil {
		t.Fatal(err)
	}
	if err = effectStore.SetCompletionSink(completion); err != nil {
		t.Fatal(err)
	}
	provider, err := outbound.NewCommercePushProvider(true, commerce, targets, cipher)
	if err != nil {
		t.Fatal(err)
	}
	events := &commerceFundsProductPushEvents{}
	external, err := productapp.NewCommerceExternalPushService(uow, products, commerce, commerce, events)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := producthttp.NewHandler(memberGridPGCatalog{}, memberGridPGLifecycle{}, memberGridPGService{}, external, commerceFundsSecurity{})
	if err != nil {
		t.Fatal(err)
	}

	path := "/api/admin/wechat-pay/products/" + strconv.FormatInt(productID, 10) + "/external-push/test"
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "product-test-delivery-http-0001")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("test-button POST status=%d body=%s", response.Code, response.Body.String())
	}
	var accepted productport.ExternalPushTest
	if err = json.Unmarshal(response.Body.Bytes(), &accepted); err != nil || accepted.EffectID == "" || !strings.HasPrefix(accepted.DeliveryID, "commerce_test_") {
		t.Fatalf("test-button response=%s accepted=%#v err=%v", response.Body.String(), accepted, err)
	}

	replayRequest := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
	replayRequest.Header.Set("Content-Type", "application/json")
	replayRequest.Header.Set("Idempotency-Key", "product-test-delivery-http-0001")
	replay := httptest.NewRecorder()
	handler.ServeHTTP(replay, replayRequest)
	var replayed productport.ExternalPushTest
	if replay.Code != http.StatusAccepted || json.Unmarshal(replay.Body.Bytes(), &replayed) != nil || replayed.DeliveryID != accepted.DeliveryID || replayed.EffectID != accepted.EffectID {
		t.Fatalf("test-button replay status=%d body=%s first=%#v replay=%#v", replay.Code, replay.Body.String(), accepted, replayed)
	}

	var effectID, generation, riverJobID int64
	if err = pool.QueryRow(ctx, `SELECT effect.id,effect.generation,job.river_job_id
FROM external_effects effect
JOIN external_effect_jobs job ON job.effect_id=effect.id AND job.generation=effect.generation
WHERE effect.id=substring($1 FROM 5)::bigint`, accepted.EffectID).Scan(&effectID, &generation, &riverJobID); err != nil {
		t.Fatal(err)
	}
	if err = effectStore.RunAttempt(ctx, effectID, generation, riverJobID, provider); err != nil {
		t.Fatal(err)
	}
	receiverLock.Lock()
	receivedDeliveryID := receiverDeliveryID
	receiverLock.Unlock()
	if receivedDeliveryID != accepted.DeliveryID {
		t.Fatalf("test-button response delivery_id=%q receiver delivery_id=%q", accepted.DeliveryID, receivedDeliveryID)
	}

	read := httptest.NewRecorder()
	handler.ServeHTTP(read, httptest.NewRequest(http.MethodGet, path, nil))
	var listed struct {
		Items []productport.ExternalPushTest `json:"items"`
	}
	if read.Code != http.StatusOK || json.Unmarshal(read.Body.Bytes(), &listed) != nil || len(listed.Items) != 1 || listed.Items[0].DeliveryID != accepted.DeliveryID || listed.Items[0].DeliveryProven {
		t.Fatalf("test-button readback status=%d body=%s listed=%#v", read.Code, read.Body.String(), listed)
	}
}
