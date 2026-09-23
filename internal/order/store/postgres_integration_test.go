package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	orderapp "github.com/qianlan33333-png/AI-CRM-v3/internal/order/app"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

type failCompleteStore struct{ *Repository }

func (failCompleteStore) Complete(context.Context, int64, json.RawMessage, time.Time) (orderapp.Receipt, error) {
	return orderapp.Receipt{}, errors.New("forced receipt completion failure")
}

func nativeCommand(key string) orderport.CreateCommand {
	payer, beneficiary := int64(11), int64(22)
	return orderport.CreateCommand{Actor: 7, IdempotencyKey: "postgres-order-key-" + key, Input: domain.NewOrderInput{
		Provider: domain.ProviderWeChatPay, SourceSystem: "aicrm-v3", SourceKey: key, MerchantOrderNo: "M-" + key,
		PayerCustomerID: &payer, BeneficiaryCustomerID: &beneficiary,
		Amount:       domain.Money{AmountMinor: 2500, Currency: "CNY"},
		Items:        []domain.ItemSnapshot{{LineNo: 1, ProductCode: "course", ProductName: "课程", UnitAmountMinor: 2500, Quantity: 1, LineAmountMinor: 2500}},
		RecordOrigin: domain.RecordOriginNative,
	}}
}

func TestPostgreSQLCustomerScopedReferenceReadDoesNotLeak(t *testing.T) {
	native, cleanup := orderIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	wrapper, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	service := orderapp.NewService(uow, repository)

	allowedInput := nativeCommand("scoped-allowed")
	allowedCustomer := int64(42)
	allowedInput.Input.PayerCustomerID = &allowedCustomer
	allowedInput.Input.MerchantOrderNo = "M-customer-scoped-reference"
	allowed, err := service.Create(ctx, allowedInput)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := service.GetByReferenceForCustomer(ctx, allowed.MerchantOrderNo, allowedCustomer)
	if err != nil || resolved.ID != allowed.ID {
		t.Fatalf("allowed scoped detail=%+v err=%v", resolved, err)
	}
	if _, err = service.GetByReferenceForCustomer(ctx, allowed.MerchantOrderNo, 43); !errors.Is(err, orderport.ErrNotFound) {
		t.Fatalf("unrelated customer read err=%v", err)
	}
}

func TestPostgreSQLCustomerActivitiesKeepPartyScopeAndKeyset(t *testing.T) {
	native, cleanup := orderIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	wrapper, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	service := orderapp.NewService(uow, repository)
	create := func(key string, payer, beneficiary int64) domain.Snapshot {
		t.Helper()
		command := nativeCommand("customer-activity-" + key)
		command.Input.PayerCustomerID, command.Input.BeneficiaryCustomerID = &payer, &beneficiary
		created, createErr := service.Create(ctx, command)
		if createErr != nil {
			t.Fatal(createErr)
		}
		return created
	}
	payerOrder := create("payer", 101, 202)
	beneficiaryOrder := create("beneficiary", 303, 101)
	_ = create("unrelated", 404, 505)
	watermark := time.Now().UTC().Add(time.Second)

	first, err := service.CustomerActivities(ctx, orderport.CustomerActivityQuery{CustomerID: 101, Limit: 1, Watermark: watermark})
	if err != nil || len(first.Items) != 1 || first.Items[0].OrderID != beneficiaryOrder.ID || first.Items[0].Relationship != "beneficiary" {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	second, err := service.CustomerActivities(ctx, orderport.CustomerActivityQuery{CustomerID: 101, Limit: 10, Watermark: watermark, AfterAt: first.Items[0].OccurredAt, AfterID: first.Items[0].OrderID})
	if err != nil || len(second.Items) != 1 || second.Items[0].OrderID != payerOrder.ID || second.Items[0].Relationship != "payer" {
		t.Fatalf("second=%+v err=%v", second, err)
	}
	beneficiary, err := service.CustomerActivities(ctx, orderport.CustomerActivityQuery{CustomerID: 202, Limit: 10, Watermark: watermark})
	if err != nil || len(beneficiary.Items) != 1 || beneficiary.Items[0].OrderID != payerOrder.ID || beneficiary.Items[0].Relationship != "beneficiary" {
		t.Fatalf("beneficiary=%+v err=%v", beneficiary, err)
	}
	if page, readErr := service.CustomerActivities(ctx, orderport.CustomerActivityQuery{CustomerID: 404, Limit: 10, Watermark: watermark}); readErr != nil || len(page.Items) != 1 || page.Items[0].OrderID == payerOrder.ID || page.Items[0].OrderID == beneficiaryOrder.ID {
		t.Fatalf("unrelated=%+v err=%v", page, readErr)
	}
}

func TestPostgreSQLOrderAtomicReplayCursorAndConstraints(t *testing.T) {
	native, cleanup := orderIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	wrapper, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	service := orderapp.NewService(uow, repository)

	first, err := service.Create(ctx, nativeCommand("one"))
	if err != nil {
		t.Fatal(err)
	}
	replay, err := service.Create(ctx, nativeCommand("one"))
	if err != nil || replay.ID != first.ID {
		t.Fatalf("replay=%#v err=%v", replay, err)
	}
	drift := nativeCommand("one")
	drift.Input.Amount.AmountMinor, drift.Input.Items[0].UnitAmountMinor, drift.Input.Items[0].LineAmountMinor = 3000, 3000, 3000
	if _, err = service.Create(ctx, drift); !errors.Is(err, orderport.ErrConflict) {
		t.Fatalf("drift err=%v", err)
	}
	if _, err = service.Create(ctx, nativeCommand("two")); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Create(ctx, nativeCommand("three")); err != nil {
		t.Fatal(err)
	}
	byReference, err := service.GetByReference(ctx, first.MerchantOrderNo)
	if err != nil || byReference.ID != first.ID || len(byReference.Items) != 1 || byReference.Items[0].ProductName != "课程" {
		t.Fatalf("detail by reference=%#v err=%v", byReference, err)
	}
	settlement := orderport.SettlementCommand{OrderID: first.ID, ExpectedVersion: first.Version, Status: domain.StatusPaid, OccurredAt: first.CreatedAt.Add(time.Second), ActorScope: "payment:settlement", IdempotencyKey: "postgres-settlement-key-one"}
	paid, err := service.ApplySettlement(ctx, settlement)
	if err != nil || paid.Status != domain.StatusPaid || paid.Version != first.Version+1 {
		t.Fatalf("paid=%#v err=%v", paid, err)
	}
	paidReplay, err := service.ApplySettlement(ctx, settlement)
	if err != nil || paidReplay.ID != paid.ID || paidReplay.Version != paid.Version {
		t.Fatalf("settlement replay=%#v err=%v", paidReplay, err)
	}
	page, err := service.List(ctx, orderport.ListQuery{Limit: 2})
	if err != nil || len(page.Items) != 2 || page.NextCursor == "" {
		t.Fatalf("page=%#v err=%v", page, err)
	}
	next, err := service.List(ctx, orderport.ListQuery{Limit: 2, Cursor: page.NextCursor})
	if err != nil || len(next.Items) != 1 || next.NextCursor != "" {
		t.Fatalf("next=%#v err=%v", next, err)
	}

	var orders, items, history, receipts, audits, outbox int
	if err = native.QueryRow(ctx, `SELECT (SELECT count(*) FROM orders),(SELECT count(*) FROM order_items),(SELECT count(*) FROM order_status_history),(SELECT count(*) FROM order_operation_receipts),(SELECT count(*) FROM order_audit_events),(SELECT count(*) FROM order_outbox)`).Scan(&orders, &items, &history, &receipts, &audits, &outbox); err != nil {
		t.Fatal(err)
	}
	if orders != 3 || items != 3 || history != 4 || receipts != 4 || audits != 4 || outbox != 5 {
		t.Fatalf("rows orders=%d items=%d history=%d receipts=%d audits=%d outbox=%d", orders, items, history, receipts, audits, outbox)
	}
	var paidEvents int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM order_paid_events WHERE order_id=$1`, first.ID).Scan(&paidEvents); err != nil || paidEvents != 1 {
		t.Fatalf("paid events=%d err=%v", paidEvents, err)
	}

	broken := orderapp.NewService(uow, failCompleteStore{repository})
	if _, err = broken.Create(ctx, nativeCommand("rollback")); !errors.Is(err, orderport.ErrUnavailable) {
		t.Fatalf("rollback error=%v", err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM orders WHERE source_key='rollback'`).Scan(&orders); err != nil || orders != 0 {
		t.Fatalf("rollback rows=%d err=%v", orders, err)
	}
	if _, err = native.Exec(ctx, `UPDATE orders SET amount_minor=0 WHERE id=$1`, first.ID); err == nil {
		t.Fatal("database accepted zero order amount")
	}
	if _, err = native.Exec(ctx, `UPDATE order_items SET product_name=product_name WHERE order_id=$1`, first.ID); err == nil {
		t.Fatal("immutable item snapshot accepted mutation")
	}
}

func TestEntitlementRemarkRejectsCrossProductBeforeMutation(t *testing.T) {
	native, cleanup := orderIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	wrapper, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapper.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	app, err := orderapp.NewEntitlementApplication(uow, repository)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)
	digest := sha256.Sum256([]byte("cross-product-entitlement"))
	var customerID int64
	if err = native.QueryRow(ctx, `INSERT INTO customers DEFAULT VALUES RETURNING id`).Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	var id int64
	if err = native.QueryRow(ctx, `INSERT INTO order_service_entitlements(source_system,source_key,customer_id,service_product_id,product_name,status,start_at,end_at,remark,source_digest,created_at,updated_at) VALUES('test','cross-product',$1,81,'B商品','active',$2,$3,'原备注',$4,$2,$2) RETURNING id`, customerID, base, base.AddDate(0, 0, 30), digest[:]).Scan(&id); err != nil {
		t.Fatal(err)
	}
	_, err = app.UpdateEntitlementRemark(ctx, orderport.RemarkCommand{EntitlementID: id, CustomerID: customerID, ServiceProductID: 80, EmployeeID: "admin:1", Remark: "越权修改", ExpectedVersion: 1, IdempotencyKey: "cross-product-remark-key"})
	if !errors.Is(err, orderport.ErrNotFound) && !errors.Is(err, orderport.ErrConflict) {
		t.Fatalf("cross-product err=%v", err)
	}
	var remark string
	var version int64
	var receipts, audits, outbox int
	if err = native.QueryRow(ctx, `SELECT remark,version,(SELECT count(*) FROM order_entitlement_operation_receipts),(SELECT count(*) FROM order_entitlement_audit_events),(SELECT count(*) FROM order_entitlement_outbox) FROM order_service_entitlements WHERE id=$1`, id).Scan(&remark, &version, &receipts, &audits, &outbox); err != nil {
		t.Fatal(err)
	}
	if remark != "原备注" || version != 1 || receipts != 0 || audits != 0 || outbox != 0 {
		t.Fatalf("cross-product changed remark=%q version=%d receipts=%d audits=%d outbox=%d", remark, version, receipts, audits, outbox)
	}
	updated, err := app.UpdateEntitlementRemark(ctx, orderport.RemarkCommand{EntitlementID: id, CustomerID: 0, ServiceProductID: 81, EmployeeID: "admin:1", Remark: "不公开客户ID的备注", ExpectedVersion: 1, IdempotencyKey: "opaque-member-remark-key"})
	if err != nil {
		t.Fatalf("opaque product-scoped remark: %v", err)
	}
	if updated.ID != id || updated.CustomerID != customerID || updated.ServiceProductID != 81 || updated.Remark != "不公开客户ID的备注" || updated.Version != 2 {
		t.Fatalf("opaque product-scoped result=%+v", updated)
	}
}

func TestEntitlementAlliancePreservesUnknownClearCASAndRollback(t *testing.T) {
	native, cleanup := orderIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	wrapper, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapper.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	app, err := orderapp.NewEntitlementApplication(uow, repo)
	if err != nil {
		t.Fatal(err)
	}
	var customerID, entitlementID int64
	if err = native.QueryRow(ctx, `INSERT INTO customers DEFAULT VALUES RETURNING id`).Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	digest := sha256.Sum256([]byte("alliance-cas"))
	if err = native.QueryRow(ctx, `INSERT INTO order_service_entitlements(source_system,source_key,customer_id,service_product_id,product_name,status,start_at,end_at,remark,source_digest,created_at,updated_at)
		VALUES('test','alliance-cas',$1,91,'联盟商品','active',$2,$3,'',$4,$2,$2) RETURNING id`, customerID, base, base.AddDate(0, 0, 30), digest[:]).Scan(&entitlementID); err != nil {
		t.Fatal(err)
	}
	if _, err = app.UpdateEntitlementAlliance(ctx, orderport.AllianceCommand{EntitlementID: entitlementID, CustomerID: customerID, ServiceProductID: 90, EmployeeID: "admin:1", Alliance: "越权联盟", ExpectedVersion: 1, IdempotencyKey: "cross-product-alliance-key"}); !errors.Is(err, orderport.ErrConflict) && !errors.Is(err, orderport.ErrNotFound) {
		t.Fatalf("cross-product err=%v", err)
	}
	var alliance *string
	var version, receipts, audits, outbox int64
	if err = native.QueryRow(ctx, `SELECT alliance,version,(SELECT count(*) FROM order_entitlement_operation_receipts),(SELECT count(*) FROM order_entitlement_audit_events),(SELECT count(*) FROM order_entitlement_outbox) FROM order_service_entitlements WHERE id=$1`, entitlementID).Scan(&alliance, &version, &receipts, &audits, &outbox); err != nil {
		t.Fatal(err)
	}
	if alliance != nil || version != 1 || receipts != 0 || audits != 0 || outbox != 0 {
		t.Fatalf("cross-product changed alliance=%v version=%d receipts=%d audits=%d outbox=%d", alliance, version, receipts, audits, outbox)
	}

	command := orderport.AllianceCommand{EntitlementID: entitlementID, CustomerID: 0, ServiceProductID: 91, EmployeeID: "admin:1", Alliance: " 联盟甲 ", ExpectedVersion: 1, IdempotencyKey: "opaque-member-alliance-key"}
	updated, err := app.UpdateEntitlementAlliance(ctx, command)
	if err != nil || updated.Alliance == nil || *updated.Alliance != "联盟甲" || updated.Version != 2 {
		t.Fatalf("alliance update=%+v err=%v", updated, err)
	}
	replayed, err := app.UpdateEntitlementAlliance(ctx, command)
	if err != nil || replayed.Version != 2 || replayed.Alliance == nil || *replayed.Alliance != "联盟甲" {
		t.Fatalf("alliance replay=%+v err=%v", replayed, err)
	}
	if _, err = app.UpdateEntitlementAlliance(ctx, orderport.AllianceCommand{EntitlementID: entitlementID, CustomerID: customerID, ServiceProductID: 91, EmployeeID: "admin:1", Alliance: "联盟乙", ExpectedVersion: 1, IdempotencyKey: "stale-alliance-key"}); !errors.Is(err, orderport.ErrConflict) {
		t.Fatalf("alliance stale err=%v", err)
	}

	if _, err = native.Exec(ctx, `CREATE FUNCTION fail_alliance_outbox() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'rollback alliance outbox'; END $$; CREATE TRIGGER fail_alliance_outbox BEFORE INSERT ON order_entitlement_outbox FOR EACH ROW EXECUTE FUNCTION fail_alliance_outbox()`); err != nil {
		t.Fatal(err)
	}
	if _, err = app.UpdateEntitlementAlliance(ctx, orderport.AllianceCommand{EntitlementID: entitlementID, CustomerID: 0, ServiceProductID: 91, EmployeeID: "admin:1", Alliance: "不会提交", ExpectedVersion: 2, IdempotencyKey: "rollback-alliance-key"}); err == nil {
		t.Fatal("outbox failure accepted")
	}
	if _, err = native.Exec(ctx, `DROP TRIGGER fail_alliance_outbox ON order_entitlement_outbox; DROP FUNCTION fail_alliance_outbox()`); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT alliance,version,(SELECT count(*) FROM order_entitlement_operation_receipts),(SELECT count(*) FROM order_entitlement_audit_events),(SELECT count(*) FROM order_entitlement_outbox) FROM order_service_entitlements WHERE id=$1`, entitlementID).Scan(&alliance, &version, &receipts, &audits, &outbox); err != nil {
		t.Fatal(err)
	}
	if alliance == nil || *alliance != "联盟甲" || version != 2 || receipts != 2 || audits != 1 || outbox != 1 {
		t.Fatalf("outbox rollback leaked alliance=%v version=%d receipts=%d audits=%d outbox=%d", alliance, version, receipts, audits, outbox)
	}
	cleared, err := app.UpdateEntitlementAlliance(ctx, orderport.AllianceCommand{EntitlementID: entitlementID, CustomerID: 0, ServiceProductID: 91, EmployeeID: "admin:1", Alliance: "", ExpectedVersion: 2, IdempotencyKey: "clear-alliance-key"})
	if err != nil || cleared.Alliance == nil || *cleared.Alliance != "" || cleared.Version != 3 {
		t.Fatalf("explicit empty alliance=%+v err=%v", cleared, err)
	}

	// A missing legacy fact must stay unknown to grid predicates. It may not be
	// folded into the explicit clear above merely because both sort as blank.
	for _, row := range []struct {
		key      string
		alliance *string
	}{
		{key: "alliance-unknown", alliance: nil},
		{key: "alliance-value", alliance: stringPointer("联盟甲")},
	} {
		var otherCustomer int64
		if err = native.QueryRow(ctx, `INSERT INTO customers DEFAULT VALUES RETURNING id`).Scan(&otherCustomer); err != nil {
			t.Fatal(err)
		}
		rowDigest := sha256.Sum256([]byte(row.key))
		if _, err = native.Exec(ctx, `INSERT INTO order_service_entitlements(source_system,source_key,customer_id,service_product_id,product_name,status,start_at,end_at,remark,alliance,source_digest,created_at,updated_at)
			VALUES('test',$1,$2,91,'联盟商品','active',$3,$4,'',$5,$6,$3,$3)`, row.key, otherCustomer, base, base.AddDate(0, 0, 30), row.alliance, rowDigest[:]); err != nil {
			t.Fatal(err)
		}
	}
	query := orderport.ServicePeriodMemberQuery{ServiceProductID: 91, Limit: 10, SnapshotAt: base, GridFilters: []orderport.MemberGridFilter{{Field: "alliance", Operator: "is_empty"}}}
	page, err := app.ListServicePeriodMembers(ctx, query)
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != entitlementID || page.Items[0].Alliance == nil || *page.Items[0].Alliance != "" {
		t.Fatalf("alliance empty predicate=%+v err=%v", page, err)
	}
	query.GridFilters = []orderport.MemberGridFilter{{Field: "alliance", Operator: "not_contains", Text: "甲"}}
	page, err = app.ListServicePeriodMembers(ctx, query)
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != entitlementID {
		t.Fatalf("alliance not-contains predicate=%+v err=%v", page, err)
	}
	query.GridFilters = []orderport.MemberGridFilter{{Field: "alliance", Operator: "is_not_empty"}}
	page, err = app.ListServicePeriodMembers(ctx, query)
	if err != nil || len(page.Items) != 1 || page.Items[0].Alliance == nil || *page.Items[0].Alliance != "联盟甲" {
		t.Fatalf("alliance known-value predicate=%+v err=%v", page, err)
	}
}

func stringPointer(value string) *string { return &value }

func TestPostgreSQLMemberGridUsesFrozenCeilDaysForFiltersAndPagination(t *testing.T) {
	native, cleanup := orderIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	wrapper, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapper.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	app, err := orderapp.NewEntitlementApplication(uow, repository)
	if err != nil {
		t.Fatal(err)
	}

	snapshot := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	type seeded struct {
		key string
		end time.Time
		id  int64
	}
	items := []seeded{
		{key: "just-under-one-day", end: snapshot.Add(24*time.Hour - time.Second)},
		{key: "exactly-one-day", end: snapshot.Add(24 * time.Hour)},
		{key: "just-over-one-day", end: snapshot.Add(24*time.Hour + time.Second)},
		{key: "expired", end: snapshot.Add(-time.Second)},
	}
	for index := range items {
		var customerID int64
		if err = native.QueryRow(ctx, `INSERT INTO customers DEFAULT VALUES RETURNING id`).Scan(&customerID); err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256([]byte("member-grid-days-" + items[index].key))
		if err = native.QueryRow(ctx, `INSERT INTO order_service_entitlements(source_system,source_key,customer_id,service_product_id,product_name,status,start_at,end_at,remark,source_digest,created_at,updated_at) VALUES('test',$1,$2,739,'Member grid','active',$3,$4,'',$5,$3,$3) RETURNING id`, items[index].key, customerID, snapshot.Add(-time.Hour), items[index].end, digest[:]).Scan(&items[index].id); err != nil {
			t.Fatal(err)
		}
	}

	base := orderport.ServicePeriodMemberQuery{ServiceProductID: 739, Limit: 50, Sort: "remaining_days_asc", SnapshotAt: snapshot}
	// Product supplies a frozen render instant even to its ordinary default
	// list. The repository must not bind it when no SQL predicate consumes it.
	plain, err := app.ListServicePeriodMembers(ctx, base)
	if err != nil || len(plain.Items) != 4 || !plain.SnapshotAt.Equal(snapshot) {
		t.Fatalf("plain member list=%+v err=%v", plain, err)
	}
	oneDay := base
	oneDay.RemainingDays = &orderport.MemberGridNumberFilter{Operator: "equals", Values: []int64{1}}
	page, err := app.ListServicePeriodMembers(ctx, oneDay)
	if err != nil || len(page.Items) != 2 || page.Items[0].ID != items[1].id || page.Items[1].ID != items[0].id {
		t.Fatalf("one-day ceil filter page=%+v err=%v", page, err)
	}
	grouped := oneDay
	grouped.GroupByRemainingDays = true
	page, err = app.ListServicePeriodMembers(ctx, grouped)
	if err != nil || len(page.Items) != 2 || page.Items[0].MemberGridGroupCount != 2 || page.Items[1].MemberGridGroupCount != 2 {
		t.Fatalf("one-day group counts page=%+v err=%v", page, err)
	}
	groupedPage := grouped
	groupedPage.Limit = 1
	firstGroup, err := app.ListServicePeriodMembers(ctx, groupedPage)
	if err != nil || len(firstGroup.Items) != 1 || firstGroup.NextCursor == "" || firstGroup.Items[0].MemberGridGroupCount != 2 {
		t.Fatalf("first grouped cursor page=%+v err=%v", firstGroup, err)
	}
	// The cursor's static snapshot is retained, but its count remains the
	// complete filtered one-day group rather than the one remaining row.
	groupedPage.Cursor = firstGroup.NextCursor
	groupedPage.SnapshotAt = snapshot.Add(48 * time.Hour)
	secondGroup, err := app.ListServicePeriodMembers(ctx, groupedPage)
	if err != nil || len(secondGroup.Items) != 1 || secondGroup.NextCursor != "" || !secondGroup.SnapshotAt.Equal(snapshot) || secondGroup.Items[0].MemberGridGroupCount != 2 {
		t.Fatalf("second grouped cursor page=%+v err=%v", secondGroup, err)
	}

	expired := base
	expired.RemainingDays = &orderport.MemberGridNumberFilter{Operator: "lte", Values: []int64{0}}
	page, err = app.ListServicePeriodMembers(ctx, expired)
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != items[3].id {
		t.Fatalf("expired clamp filter page=%+v err=%v", page, err)
	}

	paged := base
	paged.Limit = 2
	paged.RemainingDays = &orderport.MemberGridNumberFilter{Operator: "gte", Values: []int64{1}}
	first, err := app.ListServicePeriodMembers(ctx, paged)
	if err != nil || len(first.Items) != 2 || first.NextCursor == "" || !first.SnapshotAt.Equal(snapshot) || first.Items[0].ID != items[1].id || first.Items[1].ID != items[0].id {
		t.Fatalf("first filtered page=%+v err=%v", first, err)
	}
	paged.Cursor = first.NextCursor
	// A subsequent browser request inevitably has a newer wall clock. The
	// opaque Order cursor, rather than the Product Host, owns the retained
	// snapshot so the second page uses the same filter/display instant.
	paged.SnapshotAt = snapshot.Add(48 * time.Hour)
	second, err := app.ListServicePeriodMembers(ctx, paged)
	if err != nil || len(second.Items) != 1 || second.NextCursor != "" || !second.SnapshotAt.Equal(snapshot) || second.Items[0].ID != items[2].id {
		t.Fatalf("second filtered page=%+v err=%v", second, err)
	}
}

func TestPostgreSQLMemberGridAppliesMultipleOrderFactsBeforeStablePaging(t *testing.T) {
	native, cleanup := orderIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	wrapper, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapper.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	members, err := orderapp.NewEntitlementApplication(uow, repository)
	if err != nil {
		t.Fatal(err)
	}

	snapshot := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	type seeded struct {
		remark string
		end    time.Time
		id     int64
	}
	seed := []seeded{
		{remark: " beta ", end: snapshot.Add(24 * time.Hour)},
		{remark: "Alpha", end: snapshot.Add(48 * time.Hour)},
		{remark: "alpha", end: snapshot.Add(24 * time.Hour)},
	}
	for index := range seed {
		var customerID int64
		if err = native.QueryRow(ctx, `INSERT INTO customers DEFAULT VALUES RETURNING id`).Scan(&customerID); err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256([]byte("member-grid-facts-" + strconv.Itoa(index)))
		if err = native.QueryRow(ctx, `INSERT INTO order_service_entitlements(source_system,source_key,customer_id,service_product_id,product_name,status,start_at,end_at,remark,source_digest,created_at,updated_at) VALUES('legacy-import',$1,$2,740,'Member grid facts','active',$3,$4,$5,$6,$3,$3) RETURNING id`, "member-grid-facts-"+strconv.Itoa(index), customerID, snapshot.Add(-time.Hour), seed[index].end, seed[index].remark, digest[:]).Scan(&seed[index].id); err != nil {
			t.Fatal(err)
		}
	}

	base := orderport.ServicePeriodMemberQuery{
		ServiceProductID: 740,
		Limit:            1,
		FilterLogic:      "and",
		SnapshotAt:       snapshot,
		GridFilters: []orderport.MemberGridFilter{
			{Field: "remaining_days", Operator: "gte", Numbers: []float64{0.5}},
			{Field: "remark", Operator: "contains", Text: "a"},
			{Field: "renewal_count", Operator: "is_empty"},
		},
		GridSorts: []orderport.MemberGridOrder{
			{Field: "remark", Direction: "asc"},
			{Field: "remaining_days", Direction: "desc"},
		},
	}
	first, err := members.ListServicePeriodMembers(ctx, base)
	if err != nil || len(first.Items) != 1 || first.Items[0].ID != seed[1].id || first.NextCursor == "" {
		t.Fatalf("first multi-sort page=%+v err=%v", first, err)
	}
	base.Cursor, base.SnapshotAt = first.NextCursor, snapshot.AddDate(0, 0, 1)
	second, err := members.ListServicePeriodMembers(ctx, base)
	if err != nil || len(second.Items) != 1 || second.Items[0].ID != seed[2].id || second.NextCursor == "" || !second.SnapshotAt.Equal(snapshot) {
		t.Fatalf("second multi-sort page=%+v err=%v", second, err)
	}
	base.Cursor = second.NextCursor
	third, err := members.ListServicePeriodMembers(ctx, base)
	if err != nil || len(third.Items) != 1 || third.Items[0].ID != seed[0].id || third.NextCursor != "" || !third.SnapshotAt.Equal(snapshot) {
		t.Fatalf("third multi-sort page=%+v err=%v", third, err)
	}

	// Two group levels are counted in the complete filtered relation before the
	// cursor. remaining_days=1 contains beta and alpha; each second-level
	// remark partition contains one record.
	grouped := base
	grouped.Cursor, grouped.SnapshotAt = "", snapshot
	grouped.GridSorts = nil
	grouped.GridGroups = []orderport.MemberGridOrder{{Field: "remaining_days", Direction: "asc"}, {Field: "remark", Direction: "asc"}}
	seen := map[int64][]int64{}
	for {
		page, pageErr := members.ListServicePeriodMembers(ctx, grouped)
		if pageErr != nil {
			t.Fatal(pageErr)
		}
		for _, item := range page.Items {
			seen[item.ID] = append([]int64(nil), item.MemberGridGroupCounts...)
		}
		if page.NextCursor == "" {
			break
		}
		grouped.Cursor = page.NextCursor
		grouped.SnapshotAt = snapshot.AddDate(0, 0, 2)
	}
	if len(seen) != 3 || len(seen[seed[0].id]) != 2 || len(seen[seed[1].id]) != 2 || len(seen[seed[2].id]) != 2 || seen[seed[0].id][0] != 2 || seen[seed[2].id][0] != 2 || seen[seed[1].id][0] != 1 || seen[seed[0].id][1] != 1 || seen[seed[1].id][1] != 1 || seen[seed[2].id][1] != 1 {
		t.Fatalf("complete two-level group counts=%+v", seen)
	}
}

func TestPostgreSQLMemberGridRenewalCountUsesVerifiedOrderSources(t *testing.T) {
	native, cleanup := orderIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	wrapper, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapper.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	members, err := orderapp.NewEntitlementApplication(uow, repository)
	if err != nil {
		t.Fatal(err)
	}
	fulfillment, err := orderapp.NewEntitlementFulfillmentApplication(repository)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	grant := func(orderID, customerID, productID int64, name string, at time.Time) {
		t.Helper()
		if err = uow.Within(ctx, func(txctx context.Context) error {
			_, grantErr := fulfillment.GrantPaidServicePeriodWithin(txctx, orderport.ServicePeriodGrantCommand{SourceOrderID: orderID, BeneficiaryCustomerID: customerID, ServiceProductID: productID, ProductName: name, DurationDays: 31, PaidAt: at, ProcessedAt: at})
			return grantErr
		}); err != nil {
			t.Fatal(err)
		}
	}
	list := func(productID int64) orderport.Entitlement {
		t.Helper()
		page, listErr := members.ListServicePeriodMembers(ctx, orderport.ServicePeriodMemberQuery{ServiceProductID: productID, Limit: 10, SnapshotAt: base})
		if listErr != nil || len(page.Items) != 1 {
			t.Fatalf("product=%d page=%+v err=%v", productID, page, listErr)
		}
		return page.Items[0]
	}

	var nativeCustomer int64
	if err = native.QueryRow(ctx, `INSERT INTO customers DEFAULT VALUES RETURNING id`).Scan(&nativeCustomer); err != nil {
		t.Fatal(err)
	}
	first := entitlementTestOrder(t, native, nativeCustomer, "renewal-native-first", base)
	second := entitlementTestOrder(t, native, nativeCustomer, "renewal-native-second", base.Add(time.Minute))
	third := entitlementTestOrder(t, native, nativeCustomer, "renewal-native-third", base.Add(2*time.Minute))
	for index, orderID := range []int64{first, second, third} {
		grant(orderID, nativeCustomer, 751, "原生续费", base.Add(time.Duration(index)*time.Minute))
	}
	item := list(751)
	if !item.RenewalCountAvailable || item.RenewalCount != 2 {
		t.Fatalf("three effective paid enrollments should have two renewals: %+v", item)
	}
	if err = uow.Within(ctx, func(txctx context.Context) error {
		_, refundErr := fulfillment.ApplyServicePeriodRefundWithin(txctx, orderport.ServicePeriodRefundCommand{SourceOrderID: second, RefundAmountMinor: 1, ProcessedAt: base.Add(3 * time.Minute)})
		return refundErr
	}); err != nil {
		t.Fatal(err)
	}
	item = list(751)
	if !item.RenewalCountAvailable || item.RenewalCount != 1 {
		t.Fatalf("refunded source must not count as an effective renewal: %+v", item)
	}

	// A legacy aggregate without a reconciled historical source has no proof
	// of its first enrollment. A later native grant therefore remains unknown
	// instead of being misrepresented as zero or one renewal.
	var unmatchedCustomer int64
	if err = native.QueryRow(ctx, `INSERT INTO customers DEFAULT VALUES RETURNING id`).Scan(&unmatchedCustomer); err != nil {
		t.Fatal(err)
	}
	legacyDigest := sha256.Sum256([]byte("renewal-unmapped-legacy"))
	if _, err = native.Exec(ctx, `INSERT INTO order_service_entitlements(source_system,source_key,customer_id,service_product_id,product_name,status,start_at,end_at,remark,source_digest,created_at,updated_at) VALUES('legacy-import','renewal-unmapped-legacy',$1,752,'未映射历史','active',$2,$3,'',$4,$2,$2)`, unmatchedCustomer, base.Add(-24*time.Hour), base.AddDate(0, 0, 31), legacyDigest[:]); err != nil {
		t.Fatal(err)
	}
	grant(entitlementTestOrder(t, native, unmatchedCustomer, "renewal-unmapped-native", base), unmatchedCustomer, 752, "未映射历史", base)
	item = list(752)
	if item.RenewalCountAvailable || item.RenewalCount != 0 {
		t.Fatalf("unmapped legacy source must remain unavailable: %+v", item)
	}

	// Once history supplies its exact source order, it forms the first
	// enrollment and a later native grant is a verifiable first renewal.
	var mappedCustomer int64
	if err = native.QueryRow(ctx, `INSERT INTO customers DEFAULT VALUES RETURNING id`).Scan(&mappedCustomer); err != nil {
		t.Fatal(err)
	}
	historyOrder := entitlementTestOrder(t, native, mappedCustomer, "renewal-mapped-history", base)
	entitlementTestOrderItem(t, native, historyOrder, 1, 753, "mapped-753")
	mappedDigest := sha256.Sum256([]byte("renewal-mapped-legacy"))
	var mappedEntitlementID int64
	if err = native.QueryRow(ctx, `INSERT INTO order_service_entitlements(source_system,source_key,customer_id,service_product_id,product_name,last_order_id,status,start_at,end_at,remark,source_digest,created_at,updated_at) VALUES('legacy-import','renewal-mapped-legacy',$1,753,'已映射历史',$2,'active',$3,$4,'',$5,$3,$3) RETURNING id`, mappedCustomer, historyOrder, base.Add(-24*time.Hour), base.AddDate(0, 0, 31), mappedDigest[:]).Scan(&mappedEntitlementID); err != nil {
		t.Fatal(err)
	}
	if err = uow.Within(ctx, func(txctx context.Context) error {
		return fulfillment.RecordHistoricalServicePeriodSourceWithin(txctx, orderport.HistoricalServicePeriodSourceCommand{SourceOrderID: historyOrder, SourceLineNo: 1, EntitlementID: mappedEntitlementID, ServiceProductID: 753, ServiceProductCode: "mapped-753", DurationDays: 31, StartAt: base.Add(-24 * time.Hour), EndAt: base.AddDate(0, 0, 31), ImportedAt: base})
	}); err != nil {
		t.Fatal(err)
	}
	grant(entitlementTestOrder(t, native, mappedCustomer, "renewal-mapped-native", base.Add(time.Minute)), mappedCustomer, 753, "已映射历史", base.Add(time.Minute))
	item = list(753)
	if !item.RenewalCountAvailable || item.RenewalCount != 1 {
		t.Fatalf("mapped historical first order plus native grant should have one renewal: %+v", item)
	}
}

func TestPostgreSQLOrderCheckoutSnapshotIsAtomicAndDatabaseFrozen(t *testing.T) {
	native, cleanup := orderIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	wrapper, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	service := orderapp.NewService(uow, repository)
	command := orderport.PaymentOrderCommand{Provider: domain.ProviderWeChatPay, MerchantOrderNo: "checkout-snapshot-pg-001", PayerCustomerID: 11, BeneficiaryCustomerID: 22, ProductID: 9, ProductCode: "standard-9", ProductName: "标准商品", ProductVersion: 3, ProductType: "standard_product", UnitAmountMinor: 8800, Currency: "CNY", ActorScope: "payment-session:snapshot", IdempotencyKey: "checkout-snapshot-pg-key-0001"}
	var created domain.Snapshot
	if err = uow.Within(ctx, func(txctx context.Context) error {
		var createErr error
		created, createErr = service.CreatePaymentOrderWithin(txctx, command)
		return createErr
	}); err != nil {
		t.Fatal(err)
	}
	var productType, productCode, currency, reservationRef string
	var duration int32
	var gross, discount, payable int64
	var couponApplied bool
	if err = native.QueryRow(ctx, `SELECT product_type,product_code,service_period_duration_days,gross_amount_minor,discount_amount_minor,payable_amount_minor,currency,coupon_applied,coupon_reservation_ref FROM order_checkout_snapshots WHERE order_id=$1`, created.ID).Scan(&productType, &productCode, &duration, &gross, &discount, &payable, &currency, &couponApplied, &reservationRef); err != nil || productType != "standard_product" || productCode != "standard-9" || duration != 0 || gross != 8800 || discount != 0 || payable != 8800 || currency != "CNY" || couponApplied || reservationRef != "" {
		t.Fatalf("checkout snapshot type=%q code=%q duration=%d gross=%d discount=%d payable=%d currency=%q applied=%t ref=%q err=%v", productType, productCode, duration, gross, discount, payable, currency, couponApplied, reservationRef, err)
	}
	if _, err = native.Exec(ctx, `UPDATE order_checkout_snapshots SET coupon_applied=TRUE WHERE order_id=$1`, created.ID); err == nil {
		t.Fatal("database accepted coupon state without immutable reservation facts")
	}
	if _, err = native.Exec(ctx, `UPDATE order_checkout_snapshots SET service_period_duration_days=31 WHERE order_id=$1`, created.ID); err == nil {
		t.Fatal("database accepted a service period on standard-product checkout")
	}
	if _, err = native.Exec(ctx, `UPDATE order_checkout_snapshots SET product_code='replacement-code' WHERE order_id=$1`, created.ID); err == nil {
		t.Fatal("database accepted a valid-looking immutable checkout rewrite")
	}
	if _, err = native.Exec(ctx, `DELETE FROM order_checkout_snapshots WHERE order_id=$1`, created.ID); err == nil {
		t.Fatal("database accepted checkout snapshot deletion")
	}
}

func TestPostgreSQLServicePeriodFulfillmentKeepsLegacyCoverageAndRevokesOnce(t *testing.T) {
	native, cleanup := orderIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	wrapper, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	fulfillment, err := orderapp.NewEntitlementFulfillmentApplication(repository)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 1, 10, 9, 0, 0, 0, time.UTC)
	var customerID int64
	if err = native.QueryRow(ctx, `INSERT INTO customers DEFAULT VALUES RETURNING id`).Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	legacyStart, legacyEnd := base.AddDate(0, 0, -9), base.AddDate(0, 1, 5)
	legacyDigest := sha256.Sum256([]byte("legacy-entitlement"))
	if _, err = native.Exec(ctx, `INSERT INTO order_service_entitlements(source_system,source_key,customer_id,service_product_id,product_name,status,start_at,end_at,remark,source_digest,created_at,updated_at) VALUES('legacy-import','legacy-period-1',$1,77,'既有服务期','active',$2,$3,'',$4,$2,$2)`, customerID, legacyStart, legacyEnd, legacyDigest[:]); err != nil {
		t.Fatal(err)
	}
	firstOrder := entitlementTestOrder(t, native, customerID, "legacy-renew-one", base)
	grant := orderport.ServicePeriodGrantCommand{SourceOrderID: firstOrder, BeneficiaryCustomerID: customerID, ServiceProductID: 77, ProductName: "服务期", DurationDays: 31, PaidAt: base, ProcessedAt: base}
	var granted orderport.Entitlement
	if err = uow.Within(ctx, func(txctx context.Context) error {
		var grantErr error
		granted, grantErr = fulfillment.GrantPaidServicePeriodWithin(txctx, grant)
		return grantErr
	}); err != nil {
		t.Fatal(err)
	}
	wantRenewedEnd := legacyEnd.AddDate(0, 0, 31)
	if !granted.EndAt.Equal(wantRenewedEnd) || granted.Status != "active" || !granted.StartAt.Equal(legacyStart) {
		t.Fatalf("legacy renewal=%+v want end=%s", granted, wantRenewedEnd)
	}
	// A delivery retry at a later wall-clock time must replay the original paid
	// fact, including its first receipt snapshot.
	retry := grant
	retry.ProcessedAt = base.Add(5 * time.Minute)
	var replay orderport.Entitlement
	if err = uow.Within(ctx, func(txctx context.Context) error {
		var replayErr error
		replay, replayErr = fulfillment.GrantPaidServicePeriodWithin(txctx, retry)
		return replayErr
	}); err != nil || replay.ID != granted.ID || !replay.UpdatedAt.Equal(granted.UpdatedAt) {
		t.Fatalf("grant replay=%+v err=%v", replay, err)
	}

	partialAt := base.Add(24 * time.Hour)
	var partial orderport.Entitlement
	if err = uow.Within(ctx, func(txctx context.Context) error {
		var refundErr error
		partial, refundErr = fulfillment.ApplyServicePeriodRefundWithin(txctx, orderport.ServicePeriodRefundCommand{SourceOrderID: firstOrder, RefundAmountMinor: 100, ProcessedAt: partialAt})
		return refundErr
	}); err != nil {
		t.Fatal(err)
	}
	if partial.Status != "active" || !partial.EndAt.Equal(legacyEnd) || !partial.UpdatedAt.Equal(partialAt) {
		t.Fatalf("partial refund should retain imported coverage: %+v want end=%s updated=%s", partial, legacyEnd, partialAt)
	}
	// A subsequent refund of that source order succeeds without another day
	// deduction, even when the amount and receipt delivery time differ.
	var laterRefund orderport.Entitlement
	if err = uow.Within(ctx, func(txctx context.Context) error {
		var refundErr error
		laterRefund, refundErr = fulfillment.ApplyServicePeriodRefundWithin(txctx, orderport.ServicePeriodRefundCommand{SourceOrderID: firstOrder, RefundAmountMinor: 900, ProcessedAt: partialAt.Add(time.Hour)})
		return refundErr
	}); err != nil || laterRefund.ID != partial.ID || !laterRefund.EndAt.Equal(partial.EndAt) || !laterRefund.UpdatedAt.Equal(partial.UpdatedAt) {
		t.Fatalf("subsequent refund=%+v err=%v", laterRefund, err)
	}
	var firstRefundAmount int64
	if err = native.QueryRow(ctx, `SELECT refund_amount_minor FROM order_entitlement_fulfillment_receipts WHERE operation='refund' AND source_order_id=$1`, firstOrder).Scan(&firstRefundAmount); err != nil || firstRefundAmount != 100 {
		t.Fatalf("frozen first refund amount=%d err=%v", firstRefundAmount, err)
	}

	// A reconciled historical paid order has no native grant receipt. Its owner
	// mapping permits a later refund to revoke the original imported period,
	// without inventing a new payment fulfillment record.
	var historyCustomer int64
	if err = native.QueryRow(ctx, `INSERT INTO customers DEFAULT VALUES RETURNING id`).Scan(&historyCustomer); err != nil {
		t.Fatal(err)
	}
	historyOrder := entitlementTestOrder(t, native, historyCustomer, "historical-paid-order", base)
	entitlementTestOrderItem(t, native, historyOrder, 1, 79, "service-79")
	historyDigest := sha256.Sum256([]byte("history-linked-entitlement"))
	var historyEntitlementID int64
	if err = native.QueryRow(ctx, `INSERT INTO order_service_entitlements(source_system,source_key,customer_id,service_product_id,product_name,last_order_id,status,start_at,end_at,remark,source_digest,created_at,updated_at) VALUES('history','paid-order-linked',$1,79,'历史服务期',$2,'active',$3,$4,'',$5,$3,$3) RETURNING id`, historyCustomer, historyOrder, base, base.AddDate(0, 0, 31), historyDigest[:]).Scan(&historyEntitlementID); err != nil {
		t.Fatal(err)
	}
	validHistorySource := orderport.HistoricalServicePeriodSourceCommand{SourceOrderID: historyOrder, SourceLineNo: 1, EntitlementID: historyEntitlementID, ServiceProductID: 79, ServiceProductCode: "service-79", DurationDays: 31, StartAt: base, EndAt: base.AddDate(0, 0, 31), ImportedAt: base}
	wrongProduct := validHistorySource
	wrongProduct.ServiceProductCode = "other-service"
	if err = uow.Within(ctx, func(txctx context.Context) error {
		return fulfillment.RecordHistoricalServicePeriodSourceWithin(txctx, wrongProduct)
	}); !errors.Is(err, orderport.ErrConflict) {
		t.Fatalf("wrong historical product mapping err=%v", err)
	}
	pendingOrder := entitlementTestOrder(t, native, historyCustomer, "historical-pending-order", base)
	entitlementTestOrderItem(t, native, pendingOrder, 1, 79, "service-79")
	if _, err = native.Exec(ctx, `UPDATE orders SET status='pending_payment' WHERE id=$1`, pendingOrder); err != nil {
		t.Fatal(err)
	}
	pending := validHistorySource
	pending.SourceOrderID = pendingOrder
	if err = uow.Within(ctx, func(txctx context.Context) error {
		return fulfillment.RecordHistoricalServicePeriodSourceWithin(txctx, pending)
	}); !errors.Is(err, orderport.ErrNotFound) {
		t.Fatalf("unpaid historical mapping err=%v", err)
	}
	if err = uow.Within(ctx, func(txctx context.Context) error {
		return fulfillment.RecordHistoricalServicePeriodSourceWithin(txctx, validHistorySource)
	}); err != nil {
		t.Fatal(err)
	}
	entitlementTestOrderItem(t, native, historyOrder, 2, 79, "service-79")
	conflictingHistorySource := validHistorySource
	conflictingHistorySource.SourceLineNo = 2
	if err = uow.Within(ctx, func(txctx context.Context) error {
		return fulfillment.RecordHistoricalServicePeriodSourceWithin(txctx, conflictingHistorySource)
	}); !errors.Is(err, orderport.ErrConflict) {
		t.Fatalf("conflicting historical mapping err=%v", err)
	}
	historyRefundAt := base.Add(2 * time.Hour)
	var historyRefund orderport.Entitlement
	if err = uow.Within(ctx, func(txctx context.Context) error {
		var refundErr error
		historyRefund, refundErr = fulfillment.ApplyServicePeriodRefundWithin(txctx, orderport.ServicePeriodRefundCommand{SourceOrderID: historyOrder, RefundAmountMinor: 10, ProcessedAt: historyRefundAt})
		return refundErr
	}); err != nil || historyRefund.Status != "refunded" || !historyRefund.EndAt.Equal(historyRefundAt) {
		t.Fatalf("historical source refund=%+v err=%v", historyRefund, err)
	}
	var nativeGrantReceipts int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM order_entitlement_fulfillment_receipts WHERE operation='grant' AND source_order_id=$1`, historyOrder).Scan(&nativeGrantReceipts); err != nil || nativeGrantReceipts != 0 {
		t.Fatalf("historical refund manufactured grant receipts=%d err=%v", nativeGrantReceipts, err)
	}

	// The donor determines whether the prior period is still active using the
	// processing clock. A delayed payment confirmation that arrives after the
	// old end therefore starts at paid_at rather than extending stale access.
	var delayedCustomer int64
	if err = native.QueryRow(ctx, `INSERT INTO customers DEFAULT VALUES RETURNING id`).Scan(&delayedCustomer); err != nil {
		t.Fatal(err)
	}
	delayedDigest := sha256.Sum256([]byte("delayed-renewal"))
	if _, err = native.Exec(ctx, `INSERT INTO order_service_entitlements(source_system,source_key,customer_id,service_product_id,product_name,status,start_at,end_at,remark,source_digest,created_at,updated_at) VALUES('history','delayed-old',$1,80,'延迟续期','active',$2,$3,'',$4,$2,$2)`, delayedCustomer, base.AddDate(0, 0, -31), base.Add(time.Hour), delayedDigest[:]); err != nil {
		t.Fatal(err)
	}
	delayedOrder := entitlementTestOrder(t, native, delayedCustomer, "delayed-paid-order", base)
	var delayed orderport.Entitlement
	if err = uow.Within(ctx, func(txctx context.Context) error {
		var grantErr error
		delayed, grantErr = fulfillment.GrantPaidServicePeriodWithin(txctx, orderport.ServicePeriodGrantCommand{SourceOrderID: delayedOrder, BeneficiaryCustomerID: delayedCustomer, ServiceProductID: 80, ProductName: "延迟续期", DurationDays: 31, PaidAt: base, ProcessedAt: base.AddDate(0, 0, 2)})
		return grantErr
	}); err != nil || !delayed.StartAt.Equal(base) || !delayed.EndAt.Equal(base.AddDate(0, 0, 31)) {
		t.Fatalf("delayed payment clock=%+v err=%v", delayed, err)
	}

	// Two first paid orders for the same customer/product have no pre-existing
	// native row. The aggregate advisory lock serializes the insert and renewal.
	var secondCustomer int64
	if err = native.QueryRow(ctx, `INSERT INTO customers DEFAULT VALUES RETURNING id`).Scan(&secondCustomer); err != nil {
		t.Fatal(err)
	}
	orderA := entitlementTestOrder(t, native, secondCustomer, "concurrent-a", base)
	orderB := entitlementTestOrder(t, native, secondCustomer, "concurrent-b", base)
	results := make(chan error, 2)
	var wait sync.WaitGroup
	for _, sourceOrderID := range []int64{orderA, orderB} {
		wait.Add(1)
		go func(sourceOrderID int64) {
			defer wait.Done()
			results <- uow.Within(ctx, func(txctx context.Context) error {
				_, grantErr := fulfillment.GrantPaidServicePeriodWithin(txctx, orderport.ServicePeriodGrantCommand{SourceOrderID: sourceOrderID, BeneficiaryCustomerID: secondCustomer, ServiceProductID: 88, ProductName: "并发服务期", DurationDays: 31, PaidAt: base, ProcessedAt: base})
				return grantErr
			})
		}(sourceOrderID)
	}
	wait.Wait()
	close(results)
	for grantErr := range results {
		if grantErr != nil {
			t.Fatalf("concurrent grant: %v", grantErr)
		}
	}
	var count int
	var concurrentEnd time.Time
	if err = native.QueryRow(ctx, `SELECT count(*),max(end_at) FROM order_service_entitlements WHERE customer_id=$1 AND service_product_id=88`, secondCustomer).Scan(&count, &concurrentEnd); err != nil || count != 1 || !concurrentEnd.Equal(base.AddDate(0, 0, 62)) {
		t.Fatalf("concurrent aggregate count=%d end=%s err=%v", count, concurrentEnd, err)
	}
	var inferredHistoricalSources int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM order_entitlement_historical_sources WHERE source_order_id IN ($1,$2)`, orderA, orderB).Scan(&inferredHistoricalSources); err != nil || inferredHistoricalSources != 0 {
		t.Fatalf("62-day aggregate inferred historical sources=%d err=%v", inferredHistoricalSources, err)
	}

	// The first partial refund removes all days issued by its source order, not
	// a proportional amount. A second unrefunded order keeps the aggregate
	// active; when it too is refunded the entitlement ends at the processing
	// time and is marked refunded.
	firstRefundAt := base.Add(24 * time.Hour)
	if err = uow.Within(ctx, func(txctx context.Context) error {
		_, refundErr := fulfillment.ApplyServicePeriodRefundWithin(txctx, orderport.ServicePeriodRefundCommand{SourceOrderID: orderA, RefundAmountMinor: 1, ProcessedAt: firstRefundAt})
		return refundErr
	}); err != nil {
		t.Fatal(err)
	}
	var remaining orderport.Entitlement
	if err = uow.Within(ctx, func(txctx context.Context) error {
		tx, txErr := platformpostgres.RequireTransaction(txctx)
		if txErr != nil {
			return txErr
		}
		var readErr error
		var found bool
		remaining, found, readErr = latestServicePeriodEntitlement(txctx, tx, secondCustomer, 88)
		if readErr == nil && !found {
			return errors.New("missing concurrent entitlement")
		}
		return readErr
	}); err != nil || remaining.Status != "active" || !remaining.EndAt.Equal(base.AddDate(0, 0, 31)) {
		t.Fatalf("partial source revocation remaining=%+v err=%v", remaining, err)
	}
	lastRefundAt := firstRefundAt.Add(time.Hour)
	var refunded orderport.Entitlement
	if err = uow.Within(ctx, func(txctx context.Context) error {
		var refundErr error
		refunded, refundErr = fulfillment.ApplyServicePeriodRefundWithin(txctx, orderport.ServicePeriodRefundCommand{SourceOrderID: orderB, RefundAmountMinor: 999, ProcessedAt: lastRefundAt})
		return refundErr
	}); err != nil || refunded.Status != "refunded" || !refunded.EndAt.Equal(lastRefundAt) || !refunded.UpdatedAt.Equal(lastRefundAt) {
		t.Fatalf("last source revocation refunded=%+v err=%v", refunded, err)
	}
}

// This covers the aggregate fact that a period is the sum of still-valid
// source orders, not the order in which their grants happened. In particular,
// a prior_active_end_at captured by the second native grant is not historical
// coverage: after both native sources are refunded it must not survive.
func TestPostgreSQLServicePeriodRefundKeepsOnlyIndependentHistory(t *testing.T) {
	native, cleanup := orderIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	wrapper, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	fulfillment, err := orderapp.NewEntitlementFulfillmentApplication(repository)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 2, 3, 9, 0, 0, 0, time.UTC)
	type scenario struct {
		name           string
		grantOrder     []int
		refundOrder    []int
		withHistorical bool
	}
	for _, scenario := range []scenario{
		{name: "grant_a_then_b_refund_a_then_b", grantOrder: []int{0, 1}, refundOrder: []int{0, 1}},
		{name: "grant_b_then_a_refund_a_then_b", grantOrder: []int{1, 0}, refundOrder: []int{0, 1}},
		{name: "grant_a_then_b_refund_b_then_a", grantOrder: []int{0, 1}, refundOrder: []int{1, 0}},
		{name: "grant_b_then_a_refund_b_then_a", grantOrder: []int{1, 0}, refundOrder: []int{1, 0}},
		{name: "independent_legacy_history", grantOrder: []int{0, 1}, refundOrder: []int{1, 0}, withHistorical: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			var customerID int64
			if err := native.QueryRow(ctx, `INSERT INTO customers DEFAULT VALUES RETURNING id`).Scan(&customerID); err != nil {
				t.Fatal(err)
			}
			legacyEnd := base.AddDate(0, 0, 100)
			if scenario.withHistorical {
				digest := sha256.Sum256([]byte("unmapped-legacy-" + scenario.name))
				if _, err := native.Exec(ctx, `INSERT INTO order_service_entitlements(source_system,source_key,customer_id,service_product_id,product_name,status,start_at,end_at,remark,source_digest,created_at,updated_at) VALUES('legacy-import',$1,$2,99,'可追溯前置历史','active',$3,$4,'',$5,$3,$3)`, scenario.name, customerID, base.AddDate(0, 0, -9), legacyEnd, digest[:]); err != nil {
					t.Fatal(err)
				}
			}
			orders := []int64{
				entitlementTestOrder(t, native, customerID, scenario.name+"-a", base),
				entitlementTestOrder(t, native, customerID, scenario.name+"-b", base),
			}
			for _, index := range scenario.grantOrder {
				orderID := orders[index]
				if err := uow.Within(ctx, func(txctx context.Context) error {
					_, grantErr := fulfillment.GrantPaidServicePeriodWithin(txctx, orderport.ServicePeriodGrantCommand{SourceOrderID: orderID, BeneficiaryCustomerID: customerID, ServiceProductID: 99, ProductName: "并发退款顺序", DurationDays: 31, PaidAt: base, ProcessedAt: base})
					return grantErr
				}); err != nil {
					t.Fatal(err)
				}
			}
			firstAt := base.Add(time.Hour)
			var first orderport.Entitlement
			if err := uow.Within(ctx, func(txctx context.Context) error {
				var refundErr error
				first, refundErr = fulfillment.ApplyServicePeriodRefundWithin(txctx, orderport.ServicePeriodRefundCommand{SourceOrderID: orders[scenario.refundOrder[0]], RefundAmountMinor: 1, ProcessedAt: firstAt})
				return refundErr
			}); err != nil {
				t.Fatal(err)
			}
			wantFirstEnd := base.AddDate(0, 0, 31)
			if scenario.withHistorical {
				wantFirstEnd = legacyEnd.AddDate(0, 0, 31)
			}
			if first.Status != "active" || !first.EndAt.Equal(wantFirstEnd) {
				t.Fatalf("first refund=%+v want active through %s", first, wantFirstEnd)
			}
			var duplicate orderport.Entitlement
			if err := uow.Within(ctx, func(txctx context.Context) error {
				var refundErr error
				duplicate, refundErr = fulfillment.ApplyServicePeriodRefundWithin(txctx, orderport.ServicePeriodRefundCommand{SourceOrderID: orders[scenario.refundOrder[0]], RefundAmountMinor: 999, ProcessedAt: firstAt.Add(time.Hour)})
				return refundErr
			}); err != nil || duplicate.Status != first.Status || !duplicate.EndAt.Equal(first.EndAt) || !duplicate.UpdatedAt.Equal(first.UpdatedAt) {
				t.Fatalf("duplicate refund=%+v first=%+v err=%v", duplicate, first, err)
			}
			secondAt := firstAt.Add(2 * time.Hour)
			var final orderport.Entitlement
			if err := uow.Within(ctx, func(txctx context.Context) error {
				var refundErr error
				final, refundErr = fulfillment.ApplyServicePeriodRefundWithin(txctx, orderport.ServicePeriodRefundCommand{SourceOrderID: orders[scenario.refundOrder[1]], RefundAmountMinor: 1000, ProcessedAt: secondAt})
				return refundErr
			}); err != nil {
				t.Fatal(err)
			}
			if scenario.withHistorical {
				if final.Status != "active" || !final.EndAt.Equal(legacyEnd) {
					t.Fatalf("independent history was not restored: %+v want %s", final, legacyEnd)
				}
			} else if final.Status != "refunded" || !final.EndAt.Equal(secondAt) {
				t.Fatalf("native periods survived both refunds: %+v want refunded at %s", final, secondAt)
			}
		})
	}
}

func TestPostgreSQLServicePeriodRefundRevokesMappedHistoryAndNeverReclassifiesExpiredHistory(t *testing.T) {
	native, cleanup := orderIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	wrapper, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	fulfillment, err := orderapp.NewEntitlementFulfillmentApplication(repository)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 3, 4, 9, 0, 0, 0, time.UTC)
	for _, refundMappedFirst := range []bool{true, false} {
		t.Run(map[bool]string{true: "mapped_history_then_native", false: "native_then_mapped_history"}[refundMappedFirst], func(t *testing.T) {
			var customerID int64
			if err := native.QueryRow(ctx, `INSERT INTO customers DEFAULT VALUES RETURNING id`).Scan(&customerID); err != nil {
				t.Fatal(err)
			}
			historyOrder := entitlementTestOrder(t, native, customerID, "mapped-history-"+strconv.FormatBool(refundMappedFirst), base)
			entitlementTestOrderItem(t, native, historyOrder, 1, 109, "mapped-service")
			digest := sha256.Sum256([]byte("mapped-history-entitlement-" + strconv.FormatBool(refundMappedFirst)))
			legacyEnd := base.AddDate(0, 0, 31)
			var entitlementID int64
			if err := native.QueryRow(ctx, `INSERT INTO order_service_entitlements(source_system,source_key,customer_id,service_product_id,product_name,last_order_id,status,start_at,end_at,remark,source_digest,created_at,updated_at) VALUES('legacy-import',$1,$2,109,'可撤回历史',$3,'active',$4,$5,'',$6,$4,$4) RETURNING id`, "mapped-history-"+strconv.FormatBool(refundMappedFirst), customerID, historyOrder, base, legacyEnd, digest[:]).Scan(&entitlementID); err != nil {
				t.Fatal(err)
			}
			if err := uow.Within(ctx, func(txctx context.Context) error {
				return fulfillment.RecordHistoricalServicePeriodSourceWithin(txctx, orderport.HistoricalServicePeriodSourceCommand{SourceOrderID: historyOrder, SourceLineNo: 1, EntitlementID: entitlementID, ServiceProductID: 109, ServiceProductCode: "mapped-service", DurationDays: 31, StartAt: base, EndAt: legacyEnd, ImportedAt: base})
			}); err != nil {
				t.Fatal(err)
			}
			nativeOrder := entitlementTestOrder(t, native, customerID, "mapped-native-"+strconv.FormatBool(refundMappedFirst), base)
			if err := uow.Within(ctx, func(txctx context.Context) error {
				_, grantErr := fulfillment.GrantPaidServicePeriodWithin(txctx, orderport.ServicePeriodGrantCommand{SourceOrderID: nativeOrder, BeneficiaryCustomerID: customerID, ServiceProductID: 109, ProductName: "可撤回历史", DurationDays: 31, PaidAt: base, ProcessedAt: base})
				return grantErr
			}); err != nil {
				t.Fatal(err)
			}
			first, second := nativeOrder, historyOrder
			if refundMappedFirst {
				first, second = historyOrder, nativeOrder
			}
			var afterFirst orderport.Entitlement
			if err := uow.Within(ctx, func(txctx context.Context) error {
				var refundErr error
				afterFirst, refundErr = fulfillment.ApplyServicePeriodRefundWithin(txctx, orderport.ServicePeriodRefundCommand{SourceOrderID: first, RefundAmountMinor: 1, ProcessedAt: base.Add(time.Hour)})
				return refundErr
			}); err != nil || afterFirst.Status != "active" || !afterFirst.EndAt.Equal(legacyEnd) {
				t.Fatalf("first mapped/native refund=%+v err=%v", afterFirst, err)
			}
			var final orderport.Entitlement
			if err := uow.Within(ctx, func(txctx context.Context) error {
				var refundErr error
				final, refundErr = fulfillment.ApplyServicePeriodRefundWithin(txctx, orderport.ServicePeriodRefundCommand{SourceOrderID: second, RefundAmountMinor: 2, ProcessedAt: base.Add(2 * time.Hour)})
				return refundErr
			}); err != nil || final.Status != "refunded" || !final.EndAt.Equal(base.Add(2*time.Hour)) {
				t.Fatalf("mapped history survived its own refund: %+v err=%v", final, err)
			}
		})
	}

	// A legacy aggregate can retain its source_system after the original period
	// has expired. Its first native grant rightly has no historical baseline;
	// a second renewal must not freeze the new native end as one.
	var expiredCustomer int64
	if err := native.QueryRow(ctx, `INSERT INTO customers DEFAULT VALUES RETURNING id`).Scan(&expiredCustomer); err != nil {
		t.Fatal(err)
	}
	expiredDigest := sha256.Sum256([]byte("expired-history"))
	if _, err := native.Exec(ctx, `INSERT INTO order_service_entitlements(source_system,source_key,customer_id,service_product_id,product_name,status,start_at,end_at,remark,source_digest,created_at,updated_at) VALUES('legacy-import','expired-history',$1,110,'过期历史','expired',$2,$3,'',$4,$2,$2)`, expiredCustomer, base.AddDate(0, 0, -62), base.AddDate(0, 0, -1), expiredDigest[:]); err != nil {
		t.Fatal(err)
	}
	expiredA := entitlementTestOrder(t, native, expiredCustomer, "expired-native-a", base)
	expiredB := entitlementTestOrder(t, native, expiredCustomer, "expired-native-b", base)
	for _, sourceOrderID := range []int64{expiredA, expiredB} {
		if err := uow.Within(ctx, func(txctx context.Context) error {
			_, grantErr := fulfillment.GrantPaidServicePeriodWithin(txctx, orderport.ServicePeriodGrantCommand{SourceOrderID: sourceOrderID, BeneficiaryCustomerID: expiredCustomer, ServiceProductID: 110, ProductName: "过期历史", DurationDays: 31, PaidAt: base, ProcessedAt: base})
			return grantErr
		}); err != nil {
			t.Fatal(err)
		}
	}
	for index, sourceOrderID := range []int64{expiredA, expiredB} {
		at := base.Add(time.Duration(index+1) * time.Hour)
		var refunded orderport.Entitlement
		if err := uow.Within(ctx, func(txctx context.Context) error {
			var refundErr error
			refunded, refundErr = fulfillment.ApplyServicePeriodRefundWithin(txctx, orderport.ServicePeriodRefundCommand{SourceOrderID: sourceOrderID, RefundAmountMinor: 1, ProcessedAt: at})
			return refundErr
		}); err != nil {
			t.Fatal(err)
		}
		if index == 0 && (refunded.Status != "active" || !refunded.EndAt.Equal(base.AddDate(0, 0, 31))) {
			t.Fatalf("first expired-history native refund=%+v", refunded)
		}
		if index == 1 && (refunded.Status != "refunded" || !refunded.EndAt.Equal(at)) {
			t.Fatalf("expired history was reclassified as coverage: %+v", refunded)
		}
	}
}

func entitlementTestOrder(t *testing.T, pool *pgxpool.Pool, customerID int64, source string, at time.Time) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(context.Background(), `INSERT INTO orders(provider,source_system,source_key,merchant_order_no,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,record_origin,effect_eligible,created_at,updated_at) VALUES('wechat_pay','entitlement-test',$1,$2,$3,$3,1000,'CNY','paid','native',true,$4,$4) RETURNING id`, source, "M-"+source, customerID, at).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func entitlementTestOrderItem(t *testing.T, pool *pgxpool.Pool, orderID int64, lineNo int32, productID int64, productCode string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `INSERT INTO order_items(order_id,line_no,product_id,product_code,product_name,unit_amount_minor,quantity,line_amount_minor) VALUES($1,$2,$3,$4,'历史周期商品',1000,1,1000)`, orderID, lineNo, productID, productCode); err != nil {
		t.Fatal(err)
	}
}

func TestPostgreSQLPaidProductProjectionUsesHistoryAndExactFallback(t *testing.T) {
	native, cleanup := orderIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	wrapper, _ := platformpostgres.Wrap(native, time.Second)
	uow, _ := platformpostgres.NewUnitOfWork(wrapper)
	repository, _ := NewPostgreSQL(native, uow)
	now := time.Date(2026, 9, 4, 0, 0, 0, 0, time.UTC)
	insertOrder := func(source, status string, refunded int64, productID *int64, code string, duplicate bool, paidHistory bool) int64 {
		t.Helper()
		var orderID int64
		if err := native.QueryRow(ctx, `INSERT INTO orders(provider,source_system,source_key,merchant_order_no,payer_customer_id,beneficiary_customer_id,amount_minor,refunded_minor,currency,status,record_origin,effect_eligible,source_row_digest,version,created_at,updated_at) VALUES('wechat_pay','test',$1,$2,11,11,100,$3,'CNY',$4,'history',false,$5,2,$6,$6) RETURNING id`, source, "M-"+source, refunded, status, make([]byte, 32), now).Scan(&orderID); err != nil {
			t.Fatal(err)
		}
		lines := 1
		if duplicate {
			lines = 2
		}
		for line := 1; line <= lines; line++ {
			if _, err := native.Exec(ctx, `INSERT INTO order_items(order_id,line_no,product_id,product_code,product_name,unit_amount_minor,quantity,line_amount_minor) VALUES($1,$2,$3,$4,'Product',100,1,100)`, orderID, line, productID, code); err != nil {
				t.Fatal(err)
			}
		}
		toStatus := "pending_payment"
		if paidHistory {
			toStatus = "paid"
		}
		if _, err := native.Exec(ctx, `INSERT INTO order_status_history(order_id,from_status,to_status,refunded_minor,order_version,actor_scope,occurred_at) VALUES($1,NULL,$2,0,1,'test',$3)`, orderID, toStatus, now); err != nil {
			t.Fatal(err)
		}
		return orderID
	}
	productOne, productTwo := int64(101), int64(202)
	closed := insertOrder("closed", "closed", 0, &productOne, "P-1", false, true)
	partial := insertOrder("partial", "partially_refunded", 40, &productOne, "P-1", true, true)
	_ = insertOrder("pending", "pending_payment", 0, &productOne, "P-1", false, false)
	legacy := insertOrder("legacy", "paid", 0, nil, "P-1", false, true)
	_ = insertOrder("mismatch", "paid", 0, &productTwo, "P-1", false, true)
	var facts []orderport.ProductOrderFact
	err := uow.Within(ctx, func(tx context.Context) error {
		var inner error
		facts, inner = repository.ReadPaidProductOrdersWithin(tx, []orderport.ProductSalesKey{{ProductID: productOne, ProductCode: "P-1"}})
		return inner
	})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[int64]orderport.ProductOrderFact{}
	for _, fact := range facts {
		seen[fact.OrderID] = fact
	}
	if len(seen) != 3 || seen[closed].OrderRefunded || !seen[partial].OrderRefunded || seen[legacy].ProductID != nil {
		t.Fatalf("facts=%+v", facts)
	}
}

func TestPostgreSQLPaidAudienceOrdersUsePayerAndPaymentEvidence(t *testing.T) {
	native, cleanup := orderIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	wrapper, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	created := time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC)
	paidOutside := created.Add(-24 * time.Hour)
	insert := func(source, status string, payer, beneficiary, refundedMinor int64, paidAt *time.Time) {
		t.Helper()
		version := int64(2)
		if status == "partially_refunded" {
			version = 3
		}
		var id int64
		if err := native.QueryRow(ctx, `INSERT INTO orders(provider,source_system,source_key,merchant_order_no,payer_customer_id,beneficiary_customer_id,amount_minor,refunded_minor,currency,status,record_origin,effect_eligible,source_row_digest,version,created_at,updated_at) VALUES('wechat_pay','test',$1,$2,$3,$4,100,$5,'CNY',$6,'history',false,$7,$8,$9,$9) RETURNING id`, source, "M-"+source, payer, beneficiary, refundedMinor, status, make([]byte, 32), version, created).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if _, err := native.Exec(ctx, `INSERT INTO order_items(order_id,line_no,product_code,product_name,unit_amount_minor,quantity,line_amount_minor) VALUES($1,1,'course','Course',100,1,100)`, id); err != nil {
			t.Fatal(err)
		}
		if paidAt != nil {
			if _, err := native.Exec(ctx, `INSERT INTO order_status_history(order_id,from_status,to_status,refunded_minor,order_version,actor_scope,occurred_at) VALUES($1,'pending_payment','paid',0,2,'payment:settlement',$2)`, id, *paidAt); err != nil {
				t.Fatal(err)
			}
			if status == "partially_refunded" {
				if _, err := native.Exec(ctx, `INSERT INTO order_status_history(order_id,from_status,to_status,refunded_minor,order_version,actor_scope,occurred_at) VALUES($1,'paid','partially_refunded',$2,3,'payment:refund',$3)`, id, refundedMinor, paidAt.Add(time.Minute)); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	// The first row proves that payer identity, rather than the beneficiary,
	// is what reaches the audience. A partial refund is never paid-only.
	insert("payer", "paid", 101, 202, 0, &paidOutside)
	insert("partial", "partially_refunded", 303, 303, 40, &paidOutside)
	// This historical paid row has no payment-time evidence. It remains
	// eligible for an unbounded paid audience but has a nil timestamp for the
	// template's half-open time window to reject.
	insert("unknown-time", "paid", 404, 404, 0, nil)
	var facts []orderport.PaidAudienceOrder
	if err = uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		facts, readErr = repository.PaidAudienceOrders(tx, created)
		return readErr
	}); err != nil {
		t.Fatal(err)
	}
	if len(facts) != 2 {
		t.Fatalf("facts=%+v", facts)
	}
	byCustomer := map[int64]orderport.PaidAudienceOrder{}
	for _, fact := range facts {
		byCustomer[int64(fact.CustomerID)] = fact
	}
	if got := byCustomer[101]; got.ProductCode != "course" || got.OwnerReference != "" || got.PaidAt == nil || !got.PaidAt.Equal(paidOutside) {
		t.Fatalf("payer fact=%+v", got)
	}
	if got := byCustomer[404]; got.PaidAt != nil {
		t.Fatalf("historical no-time fact=%+v", got)
	}
	if _, exists := byCustomer[202]; exists {
		t.Fatalf("beneficiary leaked into audience facts=%+v", facts)
	}
	if _, exists := byCustomer[303]; exists {
		t.Fatalf("partially refunded order leaked into paid facts=%+v", facts)
	}
}

func TestPostgreSQLHistoricalImportIsEffectIneligibleAndReplayableAcrossRuns(t *testing.T) {
	native, cleanup := orderIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	wrapper, _ := platformpostgres.Wrap(native, time.Second)
	uow, _ := platformpostgres.NewUnitOfWork(wrapper)
	repository, _ := NewPostgreSQL(native, uow)
	service := orderapp.NewService(uow, repository)

	manifest, schema := [32]byte{1}, [32]byte{2}
	for _, run := range []string{"history-run-1", "history-run-2"} {
		if _, err := native.Exec(ctx, `INSERT INTO order_import_runs(run_key,source_manifest_digest,source_schema_digest,status) VALUES($1,$2,$3,'applying')`, run, manifest[:], schema[:]); err != nil {
			t.Fatal(err)
		}
	}
	input := nativeCommand("history").Input
	input.RecordOrigin, input.SourceSystem = domain.RecordOriginHistory, "aicrm-production"
	input.PayerCustomerID, input.BeneficiaryCustomerID = nil, nil
	input.CreatedAt = time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	history, err := domain.NewOrder(input)
	if err != nil {
		t.Fatal(err)
	}
	digest := [32]byte{9}
	command := orderport.HistoricalImportCommand{RunID: "history-run-1", SourceDigest: digest, Order: history.Snapshot()}
	first, err := service.ImportHistorical(ctx, command)
	if err != nil || first.EffectEligible || first.RecordOrigin != domain.RecordOriginHistory {
		t.Fatalf("first=%#v err=%v", first, err)
	}
	replay, err := service.ImportHistorical(ctx, command)
	if err != nil || replay.ID != first.ID {
		t.Fatalf("same-run replay=%#v err=%v", replay, err)
	}
	command.RunID = "history-run-2"
	crossRun, err := service.ImportHistorical(ctx, command)
	if err != nil || crossRun.ID != first.ID {
		t.Fatalf("cross-run replay=%#v err=%v", crossRun, err)
	}
	command.SourceDigest = [32]byte{8}
	if _, err = service.ImportHistorical(ctx, command); !errors.Is(err, orderport.ErrConflict) {
		t.Fatalf("source drift err=%v", err)
	}
	if _, err = native.Exec(ctx, `UPDATE orders SET effect_eligible=TRUE WHERE id=$1`, first.ID); err == nil {
		t.Fatal("historical order became effect eligible")
	}
	attributionRunDigest := make([]byte, 32)
	attributionSchemaDigest := make([]byte, 32)
	var attributionRunID int64
	if err = native.QueryRow(ctx, `INSERT INTO order_history_attribution_runs(run_key,source_manifest_digest,source_schema_digest,identity_scope,snapshot_at,status,input_count) VALUES('attribution-run-1',$1,$2,'wecom-corp:test',CURRENT_TIMESTAMP,'applying',1) RETURNING id`, attributionRunDigest, attributionSchemaDigest).Scan(&attributionRunID); err != nil {
		t.Fatal(err)
	}
	attribution := orderapp.NewHistoricalAttributionService(repository)
	evidence := [32]byte{7}
	attributionCommand := orderport.HistoricalAttributionCommand{RunID: attributionRunID, SourceKey: "history-order-1", OrderReference: first.MerchantOrderNo, EvidenceDigest: evidence, Outcome: orderport.AttributionLinked, PayerCustomerID: 55, PayerIdentityID: 66, OccurredAt: time.Now().UTC()}
	var linked orderport.HistoricalAttributionResult
	if err = uow.Within(ctx, func(tx context.Context) error {
		linked, err = attribution.RecordHistoricalAttributionWithin(tx, attributionCommand)
		return err
	}); err != nil || linked.Outcome != orderport.AttributionLinked || linked.PayerCustomerID != 55 {
		t.Fatalf("linked=%+v err=%v", linked, err)
	}
	var attributionReplay orderport.HistoricalAttributionResult
	if err = uow.Within(ctx, func(tx context.Context) error {
		attributionReplay, err = attribution.RecordHistoricalAttributionWithin(tx, attributionCommand)
		return err
	}); err != nil || !attributionReplay.Replayed || attributionReplay.Outcome != orderport.AttributionLinked {
		t.Fatalf("replay=%+v err=%v", attributionReplay, err)
	}
	persisted, err := service.Get(ctx, first.ID)
	if err != nil || persisted.PayerCustomerID == nil || *persisted.PayerCustomerID != 55 || persisted.BeneficiaryCustomerID != nil || persisted.EffectEligible {
		t.Fatalf("persisted attribution=%+v err=%v", persisted, err)
	}
	var attributionReceipts, attributionAudits, attributionOutbox int
	if err = native.QueryRow(ctx, `SELECT (SELECT count(*) FROM order_history_attribution_receipts),(SELECT count(*) FROM order_audit_events WHERE event_type='order.payer_attributed'),(SELECT count(*) FROM order_outbox WHERE event_type='order.payer_attributed')`).Scan(&attributionReceipts, &attributionAudits, &attributionOutbox); err != nil || attributionReceipts != 1 || attributionAudits != 1 || attributionOutbox != 1 {
		t.Fatalf("attribution receipts=%d audits=%d outbox=%d err=%v", attributionReceipts, attributionAudits, attributionOutbox, err)
	}
	var orderCount, receiptCount int
	if err = native.QueryRow(ctx, `SELECT (SELECT count(*) FROM orders),(SELECT count(*) FROM order_import_receipts)`).Scan(&orderCount, &receiptCount); err != nil || orderCount != 1 || receiptCount != 2 {
		t.Fatalf("orders=%d receipts=%d err=%v", orderCount, receiptCount, err)
	}
}

func orderIntegrationPool(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	url, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("DATABASE_URL is not configured; skipping Order PostgreSQL integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var random [8]byte
	if _, err = rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	schemaName := "aicrm_order_test_" + hex.EncodeToString(random[:])
	admin, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schemaName}.Sanitize()); err != nil {
		admin.Close(ctx)
		t.Fatal(err)
	}
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schemaName
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate integration test")
	}
	for _, name := range []string{"0002_identity.sql", "0005_external_effects.sql", "0010_product.sql", "0020_order.sql", "0024_order_product_version.sql", "0049_order_history_attribution.sql", "0055_order_service_entitlements.sql", "0070_service_period_entitlement_fulfillment.sql", "0076_order_checkout_snapshots.sql", "0088_order_service_entitlement_alliance.sql", "0095_product_external_push.sql", "0158_order_distribution_qualification_evidence.sql", "0172_order_checkout_post_purchase_action.sql", "0197_order_referral_activity_context.sql"} {
		migration, readErr := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations", name))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, err = pool.Exec(ctx, string(migration)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
	}
	for _, table := range []string{"orders", "order_items", "order_status_history", "order_operation_receipts", "order_export_receipts", "order_audit_events", "order_outbox", "order_paid_events", "order_import_runs", "order_import_receipts", "order_import_quarantine"} {
		var owned bool
		if err = pool.QueryRow(ctx, `SELECT tableowner=current_user FROM pg_tables WHERE schemaname=current_schema() AND tablename=$1`, table).Scan(&owned); err != nil || !owned {
			t.Fatalf("table %s owner=%t err=%v", table, owned, err)
		}
	}
	return pool, func() {
		pool.Close()
		cleanup, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_, _ = admin.Exec(cleanup, "DROP SCHEMA "+pgx.Identifier{schemaName}.Sanitize()+" CASCADE")
		admin.Close(cleanup)
	}
}
