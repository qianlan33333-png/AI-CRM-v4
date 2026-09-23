package app_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	orderdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	productapp "github.com/qianlan33333-png/AI-CRM-v3/internal/product/app"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
	productstore "github.com/qianlan33333-png/AI-CRM-v3/internal/product/store"
)

type guidanceOrderStub struct {
	orderport.Query
	order orderdomain.Snapshot
}

type checkoutSnapshotStub struct {
	values map[int64]orderport.CheckoutSnapshot
}

func (stub checkoutSnapshotStub) ReadCheckoutSnapshotWithin(_ context.Context, orderID int64) (orderport.CheckoutSnapshot, error) {
	value, found := stub.values[orderID]
	if !found {
		return orderport.CheckoutSnapshot{}, orderport.ErrNotFound
	}
	return value, nil
}

func (s *guidanceOrderStub) Get(context.Context, int64) (orderdomain.Snapshot, error) {
	return s.order, nil
}

type paidPurchaseTagSubmitterStub struct {
	calls int
	err   error
}

func (stub *paidPurchaseTagSubmitterStub) SubmitTagCommand(context.Context, customerport.TagCommand) (customerport.TagCommandResult, error) {
	return customerport.TagCommandResult{}, errors.New("must join existing transaction")
}

func (stub *paidPurchaseTagSubmitterStub) SubmitTagCommandWithin(ctx context.Context, command customerport.TagCommand) (customerport.TagCommandResult, error) {
	if _, err := platformpostgres.RequireTransaction(ctx); err != nil {
		return customerport.TagCommandResult{}, err
	}
	stub.calls++
	if stub.err != nil {
		return customerport.TagCommandResult{}, stub.err
	}
	if command.Source != "product_paid_purchase" || command.SourceRef == "" || command.SourceRef != command.IdempotencyKey || len(command.Targets) != 1 || command.Targets[0].CustomerID != customerdomain.CustomerID(8) {
		return customerport.TagCommandResult{}, errors.New("unexpected paid tag command")
	}
	return customerport.TagCommandResult{ID: 1, State: "queued"}, nil
}

// TestPaidPurchaseActionPostgreSQLTransactionBoundaries exercises the actual
// Product table and 0117 ledger on the dedicated test database. It covers old
// projections, independent QR/tag controls, response replay after a later
// Product edit, and tag-intent rollback without turning the payment into a
// partially committed action.
func TestPaidPurchaseActionPostgreSQLTransactionBoundaries(t *testing.T) {
	databaseURL, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured; skipping paid purchase action PostgreSQL test")
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
	defer admin.Close()
	var random [6]byte
	if _, err = rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	schema := "aicrm_paid_purchase_" + hex.EncodeToString(random[:])
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(context.Background(), "DROP SCHEMA "+identifier+" CASCADE")
	config := adminConfig.Copy()
	config.ConnConfig.RuntimeParams["search_path"] = schema
	native, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer native.Close()
	for _, statement := range []string{
		`CREATE TABLE products(id BIGINT PRIMARY KEY,product_code TEXT NOT NULL,name TEXT NOT NULL,description TEXT NOT NULL DEFAULT '',price_minor BIGINT NOT NULL,currency TEXT NOT NULL,stock_quantity INTEGER NOT NULL,images JSONB NOT NULL DEFAULT '[]'::jsonb,created_by BIGINT NOT NULL,created_at TIMESTAMPTZ NOT NULL,updated_at TIMESTAMPTZ NOT NULL,version BIGINT NOT NULL,legacy_admin_projection JSONB NOT NULL)`,
		`CREATE TABLE orders(id BIGINT PRIMARY KEY)`,
		`CREATE TABLE order_paid_events(id BIGINT PRIMARY KEY,order_id BIGINT NOT NULL REFERENCES orders(id))`,
		`CREATE TABLE customer_tag_commands(id BIGINT PRIMARY KEY)`,
	} {
		if _, err = native.Exec(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	sql, err := os.ReadFile(paidPurchaseActionMigration(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, string(sql)); err != nil {
		t.Fatalf("apply 0117: %v", err)
	}
	targetSnapshotSQL, err := os.ReadFile(paidPurchaseActionTargetSnapshotMigration(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, string(targetSnapshotSQL)); err != nil {
		t.Fatalf("apply 0173: %v", err)
	}
	if _, err = native.Exec(ctx, `INSERT INTO customer_tag_commands(id) VALUES(1)`); err != nil {
		t.Fatal(err)
	}
	wrapped, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := productstore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	tags := &paidPurchaseTagSubmitterStub{}
	service, err := productapp.NewPaidPurchaseActionService(uow, repository, tags)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 8, 11, 0, 0, 0, time.UTC)
	insertProduct := func(id int64, projection string) {
		if _, e := native.Exec(ctx, `INSERT INTO products(id,product_code,name,description,price_minor,currency,stock_quantity,created_by,created_at,updated_at,version,legacy_admin_projection) VALUES($1,$2,'商品','',990,'CNY',10,1,$3,$3,2,$4::jsonb)`, id, fmt.Sprintf("purchase-%d", id), now, projection); e != nil {
			t.Fatal(e)
		}
	}
	insertEvent := func(id, productID int64) orderport.PaidEvent {
		if _, e := native.Exec(ctx, `INSERT INTO orders(id) VALUES($1)`, id); e != nil {
			t.Fatal(e)
		}
		if _, e := native.Exec(ctx, `INSERT INTO order_paid_events(id,order_id) VALUES($1,$1)`, id); e != nil {
			t.Fatal(e)
		}
		payer := int64(8)
		return orderport.PaidEvent{ID: id, OrderID: id, OrderVersion: 2, DomainEventOutboxID: id + 1000, CheckoutProductID: productID, CheckoutGrossAmountMinor: 990, OccurredAt: now, SourceDigest: orderport.NewPaidEventSourceDigest(id, 2), Order: orderdomain.Snapshot{ID: id, Provider: orderdomain.ProviderWeChatPay, SourceSystem: "v3-checkout", SourceKey: "paid-action", MerchantOrderNo: "M-paid", PayerCustomerID: &payer, BeneficiaryCustomerID: &payer, Amount: orderdomain.Money{AmountMinor: 990, Currency: "CNY"}, Status: orderdomain.StatusPaid, Items: []orderdomain.ItemSnapshot{{LineNo: 1, ProductID: &productID, ProductCode: "purchase", ProductName: "商品", UnitAmountMinor: 990, Quantity: 1, LineAmountMinor: 990}}, RecordOrigin: orderdomain.RecordOriginNative, EffectEligible: true, Version: 2, CreatedAt: now.Add(-time.Minute), UpdatedAt: now}}
	}
	consume := func(event orderport.PaidEvent) error {
		return uow.Within(ctx, func(tx context.Context) error { return service.ConsumePaidEventWithin(tx, event) })
	}

	// A pre-0117 projection has no action and must still settle successfully;
	// [] is stored rather than NULL despite the database default not applying to
	// an explicit nil pgx parameter.
	insertProduct(1, `{"schema_version":1,"status":"active","enabled":true,"buy_button_text":"购买","require_mobile":false,"lead_program_id":null,"lead_channel_id":null,"lead_qr_title":"","lead_qr_subtitle":"","completion_redirect_enabled":false,"completion_redirect_url":"","completion_target":null,"wecom_tagging":{},"slices":[]}`)
	if err = consume(insertEvent(101, 1)); err != nil {
		t.Fatal(err)
	}
	var mode, tagState string
	var tagIDs []int64
	if err = native.QueryRow(ctx, `SELECT action_mode,tag_state,tag_ids FROM product_paid_purchase_actions WHERE order_id=101`).Scan(&mode, &tagState, &tagIDs); err != nil || mode != "none" || tagState != "disabled" || tagIDs == nil || len(tagIDs) != 0 {
		t.Fatalf("old action mode=%q tag_state=%q tag_ids=%v err=%v", mode, tagState, tagIDs, err)
	}

	// Current guidance can improve an old none snapshot without replaying tags.
	if _, err = native.Exec(ctx, `UPDATE products SET legacy_admin_projection=jsonb_set(jsonb_set(jsonb_set(legacy_admin_projection,'{purchase_action_enabled}','true'),'{purchase_action_mode}','"qr"'),'{lead_channel_id}','7') WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	productID := int64(1)
	orders := &guidanceOrderStub{order: orderdomain.Snapshot{ID: 101, Status: orderdomain.StatusPaid, Amount: orderdomain.Money{AmountMinor: 990, Currency: "CNY"}, Items: []orderdomain.ItemSnapshot{{ProductID: &productID, ProductCode: "purchase-1"}}}}
	service.SetPaidGuidanceOrderReader(orders)
	for _, scenario := range []string{"old_none", "missing_snapshot", "free_paid", "wrong_product", "unpaid", "refunded"} {
		orders.order.ID = 101
		orders.order.Status = orderdomain.StatusPaid
		orders.order.Amount.AmountMinor = 990
		orders.order.RefundedMinor = 0
		orders.order.Items[0].ProductCode = "purchase-1"
		switch scenario {
		case "missing_snapshot":
			orders.order.ID = 999
		case "free_paid":
			orders.order.Amount.AmountMinor = 0
		case "wrong_product":
			orders.order.Items[0].ProductCode = "wrong"
		case "unpaid":
			orders.order.Status = orderdomain.StatusPendingPayment
		case "refunded":
			orders.order.Status = orderdomain.StatusRefunded
			orders.order.RefundedMinor = 990
		}
		action, e := service.ReadPaidPurchaseGuidance(ctx, orders.order.ID)
		shouldWork := scenario == "old_none" || scenario == "missing_snapshot" || scenario == "free_paid"
		if shouldWork && (e != nil || action.Mode != productport.PaidPurchaseActionQR || action.LeadChannelID != 7) {
			t.Fatalf("%s action=%+v err=%v", scenario, action, e)
		}
		if !shouldWork && e == nil {
			t.Fatalf("%s accepted invalid ownership/product fact", scenario)
		}
	}
	if tags.calls != 0 {
		t.Fatal("display replayed tags")
	}
	if err = native.QueryRow(ctx, `SELECT action_mode FROM product_paid_purchase_actions WHERE order_id=101`).Scan(&mode); err != nil || mode != "none" {
		t.Fatal("immutable snapshot changed", err)
	}
	var snapshots int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM product_paid_purchase_actions`).Scan(&snapshots); err != nil || snapshots != 1 {
		t.Fatal("display created snapshot", err)
	}

	insertProduct(2, `{"schema_version":1,"status":"active","enabled":true,"buy_button_text":"购买","require_mobile":false,"lead_program_id":null,"lead_channel_id":7,"lead_qr_title":"扫码","lead_qr_subtitle":"继续","completion_redirect_enabled":false,"completion_redirect_url":"","completion_target":null,"purchase_action_enabled":true,"purchase_action_mode":"qr","wecom_tagging":{"enabled":false,"tag_ids":[9]},"slices":[]}`)
	if err = consume(insertEvent(102, 2)); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT action_mode,tag_state,tag_ids FROM product_paid_purchase_actions WHERE order_id=102`).Scan(&mode, &tagState, &tagIDs); err != nil || mode != "qr" || tagState != "disabled" || len(tagIDs) != 0 || tags.calls != 0 {
		t.Fatalf("qr action mode=%q tag_state=%q tag_ids=%v calls=%d err=%v", mode, tagState, tagIDs, tags.calls, err)
	}

	insertProduct(3, `{"schema_version":1,"status":"active","enabled":true,"buy_button_text":"购买","require_mobile":false,"lead_program_id":null,"lead_channel_id":null,"lead_qr_title":"","lead_qr_subtitle":"","completion_redirect_enabled":false,"completion_redirect_url":"","completion_target":null,"purchase_action_enabled":false,"purchase_action_mode":"","wecom_tagging":{"enabled":true,"tag_ids":[9]},"slices":[]}`)
	event := insertEvent(103, 3)
	if err = consume(event); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT action_mode,tag_state,tag_ids FROM product_paid_purchase_actions WHERE order_id=103`).Scan(&mode, &tagState, &tagIDs); err != nil || mode != "none" || tagState != "queued" || len(tagIDs) != 1 || tagIDs[0] != 9 || tags.calls != 1 {
		t.Fatalf("tag action mode=%q tag_state=%q tag_ids=%v calls=%d err=%v", mode, tagState, tagIDs, tags.calls, err)
	}
	if _, err = native.Exec(ctx, `UPDATE products SET legacy_admin_projection='{"invalid":true}'::jsonb WHERE id=3`); err != nil {
		t.Fatal(err)
	}
	if err = consume(event); err != nil || tags.calls != 1 {
		t.Fatalf("replay after later product edit err=%v calls=%d", err, tags.calls)
	}

	insertProduct(4, `{"schema_version":1,"status":"active","enabled":true,"buy_button_text":"购买","require_mobile":false,"lead_program_id":null,"lead_channel_id":null,"lead_qr_title":"","lead_qr_subtitle":"","completion_redirect_enabled":false,"completion_redirect_url":"","completion_target":null,"purchase_action_enabled":false,"purchase_action_mode":"","wecom_tagging":{"enabled":true,"tag_ids":[9]},"slices":[]}`)
	tags.err = errors.New("tag intent acceptance unavailable")
	if err = consume(insertEvent(104, 4)); err == nil {
		t.Fatal("tag acceptance failure committed paid action")
	}
	var count int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM product_paid_purchase_actions WHERE order_id=104`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed tag action count=%d err=%v", count, err)
	}

	// Payment completion reads the current Product action, while the checkout
	// snapshot remains an immutable sale fact for audit and validation.
	if err = service.SetCheckoutSnapshotReader(checkoutSnapshotStub{values: map[int64]orderport.CheckoutSnapshot{105: {
		OrderID: 105, ProductID: 5, ProductVersion: 1,
		PostPurchaseAction: []byte(`{"schema_version":1,"purchase_action_enabled":true,"purchase_action_mode":"redirect","lead_channel_id":null,"lead_qr_title":"","lead_qr_subtitle":"","completion_redirect_url":"/frozen-after-paid","completion_target":null}`),
	}}}); err != nil {
		t.Fatal(err)
	}
	insertProduct(5, `{"schema_version":1,"status":"active","enabled":true,"buy_button_text":"购买","require_mobile":false,"lead_program_id":null,"lead_channel_id":null,"lead_qr_title":"","lead_qr_subtitle":"","completion_redirect_enabled":false,"completion_redirect_url":"/changed-after-checkout","completion_target":null,"purchase_action_enabled":true,"purchase_action_mode":"redirect","wecom_tagging":{},"slices":[]}`)
	if err = consume(insertEvent(105, 5)); err != nil {
		t.Fatal(err)
	}
	var redirectURL string
	var frozen bool
	if err = native.QueryRow(ctx, `SELECT redirect_url,checkout_snapshot FROM product_paid_purchase_actions WHERE order_id=105`).Scan(&redirectURL, &frozen); err != nil || redirectURL != "/changed-after-checkout" || frozen {
		t.Fatalf("current checkout action redirect=%q frozen=%t err=%v", redirectURL, frozen, err)
	}
	orders.order = orderdomain.Snapshot{ID: 105, Status: orderdomain.StatusPaid, Amount: orderdomain.Money{AmountMinor: 990, Currency: "CNY"}, Items: []orderdomain.ItemSnapshot{{ProductID: func() *int64 { id := int64(5); return &id }(), ProductCode: "purchase-5"}}}
	guidance, guidanceErr := service.ReadPaidPurchaseGuidance(ctx, 105)
	if guidanceErr != nil || guidance.RedirectURL != "/changed-after-checkout" {
		t.Fatalf("paid checkout guidance=%+v err=%v", guidance, guidanceErr)
	}
	orders.order.Status, orders.order.RefundedMinor = orderdomain.StatusRefunded, 990
	if _, guidanceErr = service.ReadPaidPurchaseGuidance(ctx, 105); guidanceErr == nil {
		t.Fatal("fully refunded checkout retained a completion action")
	}
}

func paidPurchaseActionMigration(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime caller unavailable")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations", "0117_product_paid_purchase_actions.sql")
}

func paidPurchaseActionTargetSnapshotMigration(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime caller unavailable")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations", "0173_product_paid_purchase_action_target_snapshot.sql")
}
