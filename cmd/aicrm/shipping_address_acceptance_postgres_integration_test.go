package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	orderapp "github.com/qianlan33333-png/AI-CRM-v3/internal/order/app"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderhttp "github.com/qianlan33333-png/AI-CRM-v3/internal/order/http"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	ordersecure "github.com/qianlan33333-png/AI-CRM-v3/internal/order/secure"
	orderstore "github.com/qianlan33333-png/AI-CRM-v3/internal/order/store"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// Run with scripts/accept-shipping-address-staging.sh. The test creates and
// removes its own schema inside a dedicated staging acceptance database.
func TestPostgreSQLShippingAddressSnapshotAndTransactionDetailAcceptance(t *testing.T) {
	rawURL, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Fatal("shipping acceptance requires a dedicated staging database")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || !strings.HasSuffix(strings.TrimPrefix(parsed.Path, "/"), "_acceptance_test") {
		t.Fatal("shipping acceptance requires a dedicated *_acceptance_test database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()
	native, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer native.Close()
	wrapper, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := orderstore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	service := orderapp.NewService(uow, repository)
	cipher, err := ordersecure.NewContactCipher(base64.RawStdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil || service.SetContactCipher(cipher) != nil {
		t.Fatalf("contact cipher unavailable: %v", err)
	}
	var customerID int64
	if err = native.QueryRow(ctx, `INSERT INTO customers DEFAULT VALUES RETURNING id`).Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	address := orderport.ShippingAddress{
		RecipientName: "预发布收件人", ProvinceCode: "11", ProvinceName: "北京市",
		CityCode: "1101", CityName: "北京市", DistrictCode: "110101", DistrictName: "东城区",
		DetailAddress: "预发布测试路 1 号",
	}
	command := orderport.PaymentOrderCommand{
		Provider: domain.ProviderWeChatPay, MerchantOrderNo: "shipping-acceptance-001",
		PayerCustomerID: customerID, BeneficiaryCustomerID: customerID,
		ProductID: 9, ProductCode: "shipping-acceptance", ProductName: "预发布实体商品", ProductVersion: 1,
		ProductType: "standard_product", UnitAmountMinor: 990, Currency: "CNY",
		MobileE164: "+8613812345678", ContactCollectionLevel: "shipping_address", ShippingAddress: address,
		ActorScope: "staging-shipping-acceptance", IdempotencyKey: "staging-shipping-acceptance-0001",
	}
	create := func(input orderport.PaymentOrderCommand) (domain.Snapshot, error) {
		var snapshot domain.Snapshot
		err := uow.Within(ctx, func(tx context.Context) error {
			var createErr error
			snapshot, createErr = service.CreatePaymentOrderWithin(tx, input)
			return createErr
		})
		return snapshot, err
	}
	created, err := create(command)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := create(command)
	if err != nil || replayed.ID != created.ID {
		t.Fatalf("checkout replay created a different order: first=%d replay=%d err=%v", created.ID, replayed.ID, err)
	}
	changed := command
	changed.ShippingAddress.DetailAddress = "另一地址"
	if _, err = create(changed); !errors.Is(err, orderport.ErrConflict) {
		t.Fatalf("same idempotency key accepted changed address: %v", err)
	}
	var persisted orderport.ShippingAddress
	if err = native.QueryRow(ctx, `SELECT recipient_name,province_code,province_name,city_code,city_name,district_code,district_name,detail_address FROM order_shipping_address_snapshots WHERE order_id=$1`, created.ID).Scan(
		&persisted.RecipientName, &persisted.ProvinceCode, &persisted.ProvinceName,
		&persisted.CityCode, &persisted.CityName, &persisted.DistrictCode, &persisted.DistrictName, &persisted.DetailAddress,
	); err != nil || persisted != address {
		t.Fatalf("frozen shipping snapshot mismatch: %+v err=%v", persisted, err)
	}
	if _, err = native.Exec(ctx, `UPDATE order_shipping_address_snapshots SET detail_address='rewritten' WHERE order_id=$1`, created.ID); err == nil {
		t.Fatal("database allowed shipping snapshot mutation")
	}
	handler, err := orderhttp.NewHandler(service, journeyHTTPSecurity{})
	if err != nil || handler.SetCheckoutContactReader(service) != nil {
		t.Fatalf("transaction detail unavailable: %v", err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/orders/"+created.MerchantOrderNo, nil))
	if response.Code != http.StatusOK {
		t.Fatalf("transaction detail status=%d body=%s", response.Code, response.Body.String())
	}
	var detail struct {
		ContactCollectionLevel string `json:"contact_collection_level"`
		ShippingMobileMasked   string `json:"shipping_mobile_masked"`
		orderport.ShippingAddress
	}
	if err = json.Unmarshal(response.Body.Bytes(), &detail); err != nil || detail.ContactCollectionLevel != "shipping_address" || detail.ShippingMobileMasked != "138****5678" || detail.ShippingAddress != address || strings.Contains(response.Body.String(), "+8613812345678") {
		t.Fatalf("transaction detail did not read frozen contact: %+v err=%v body=%s", detail, err, response.Body.String())
	}
	t.Logf("shipping_address_acceptance: PASS order_id=%d snapshot=immutable detail=readback mobile=masked", created.ID)
}
