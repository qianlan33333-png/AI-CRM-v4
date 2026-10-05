package app

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	couponport "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	"github.com/xuri/excelize/v2"
)

type exportMemoryStore struct {
	*memoryStore
	contacts   map[int64]orderport.CheckoutContact
	readErr    error
	lastFilter ListFilter
}

func (s *exportMemoryStore) ReadShippingAddressSnapshot(_ context.Context, id int64) (orderport.ShippingAddress, bool, error) {
	v := s.contacts[id]
	return v.ShippingAddress, v.AddressCollected, s.readErr
}
func (s *exportMemoryStore) ReadContactSnapshot(_ context.Context, id int64) ([]byte, int16, bool, error) {
	v := s.contacts[id]
	return []byte(v.MobileE164), 1, v.MobileCollected, s.readErr
}
func (s *exportMemoryStore) Export(ctx context.Context, f ListFilter, limit int32) ([]domain.Order, error) {
	s.lastFilter = f
	rows, err := s.memoryStore.Export(ctx, f, limit)
	sort.Slice(rows, func(i, j int) bool { return rows[i].Snapshot().ID > rows[j].Snapshot().ID })
	return rows, err
}

type exportFactsStub struct {
	facts    orderport.ExportFacts
	err      error
	fallback *[]int64
}

func (s exportFactsStub) ReadOrderExportFactsWithin(_ context.Context, _ []domain.Snapshot, ids []int64) (orderport.ExportFacts, error) {
	if s.fallback != nil {
		*s.fallback = ids
	}
	return s.facts, s.err
}

type exportCipher struct{ bad bool }

func (exportCipher) Encrypt(v string) ([]byte, error) { return []byte(v), nil }
func (exportCipher) KeyVersion() int16                { return 1 }
func (s exportCipher) Decrypt(v []byte, _ int16) (string, error) {
	if s.bad {
		return "", errors.New("decrypt failed")
	}
	return string(v), nil
}

func TestExportXLSXPreservesFullFactsBlankContactsAndEveryItem(t *testing.T) {
	store := &exportMemoryStore{memoryStore: newMemoryStore(), contacts: map[int64]orderport.CheckoutContact{}}
	service := NewService(directUOW{}, store)
	service.SetContactCipher(exportCipher{})
	first := orderInput("full-export-1")
	first.MerchantOrderNo = "000123456789012345678901"
	first.Items[0].ProductCode = "0001"
	first.Items[0].ProductName = "=HYPERLINK(\"fixture\")"
	first.Items[0].Quantity = 2
	first.Items[0].LineAmountMinor = 2000
	first.Amount.AmountMinor = 2050
	first.Items = append(first.Items, domain.ItemSnapshot{LineNo: 2, ProductCode: "0002", ProductName: "第二商品", Quantity: 1, UnitAmountMinor: 50, LineAmountMinor: 50})
	o, err := service.Create(context.Background(), orderport.CreateCommand{Input: first, Actor: 7, IdempotencyKey: "order-create-full-export-0001"})
	if err != nil {
		t.Fatal(err)
	}
	store.contacts[o.ID] = orderport.CheckoutContact{MobileE164: "+8613800000000", MobileCollected: true, AddressCollected: true, ShippingAddress: orderport.ShippingAddress{RecipientName: "测试小区A座", ProvinceName: "浙江省", CityName: "杭州市", DistrictName: "西湖区", DetailAddress: "202"}}
	second := orderInput("full-export-2")
	second.Provider = domain.ProviderAlipay
	b, err := service.Create(context.Background(), orderport.CreateCommand{Input: second, Actor: 7, IdempotencyKey: "order-create-full-export-0002"})
	if err != nil {
		t.Fatal(err)
	}
	// Reuse a synthetic merged customer's presentation, never the beneficiary.
	payer := *b.PayerCustomerID
	var fallback []int64
	paid := time.Date(2026, 10, 5, 0, 3, 0, 0, time.UTC)
	service.SetExportFactsReader(exportFactsStub{fallback: &fallback, facts: orderport.ExportFacts{Customers: map[int64]orderport.ExportCustomer{payer: {Number: "0000123", Name: "买家测试昵称", Phone: "13800000001"}}, Payments: map[int64]orderport.ExportPayment{o.ID: {PaidAt: &paid, TransactionNo: "000012345678901234567890"}}, Distributors: map[int64][]orderport.ExportDistributor{o.ID: {{ItemLine: 2, Number: "0000333", Name: "测试分销员"}}}}})
	preview, err := service.PreviewExport(context.Background(), orderport.ListQuery{Offset: 50, Limit: 1})
	if err != nil || preview.Orders != 2 || preview.ItemRows != 3 || preview.Truncated {
		t.Fatalf("preview counts=%+v err=%v", preview, err)
	}
	result, err := service.Export(context.Background(), orderport.ListQuery{Offset: 50, Limit: 1}, "xlsx", 7, "full-export-key-0001")
	if err != nil {
		t.Fatal(err)
	}
	if result.Rows != 3 || store.lastFilter.Offset != 0 || len(fallback) != 1 {
		t.Fatal("export must include every page/item and only fallback for an absent order phone")
	}
	f, err := excelize.OpenReader(bytes.NewReader(result.Content))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rows, err := f.GetRows("订单信息")
	if err != nil || len(rows) != 4 || len(rows[0]) != 27 {
		t.Fatal("xlsx schema/row count mismatch")
	}
	for i := 1; i < len(rows); i++ {
		for len(rows[i]) < 27 {
			rows[i] = append(rows[i], "")
		}
	}
	if rows[1][10] != "" || rows[1][11] != "13800000001" || rows[1][12] != "" || rows[1][5] != "支付宝" {
		t.Fatal("non-shipping order must retain buyer phone and blank shipping fields")
	}
	if rows[2][0] != first.MerchantOrderNo || rows[2][2] != "0000123" || rows[2][4] != "000012345678901234567890" || rows[2][6] != "2026-10-05 08:03:00" || rows[2][7] != "20.50" || rows[2][10] != "测试小区A座" || rows[2][11] != "13800000000" || rows[2][15] != "202" || rows[2][16] != "0001" || rows[2][18] != "2" {
		t.Fatal("full-data order facts or raw anomaly changed")
	}
	if rows[3][22] != "0000333" || rows[3][23] != "测试分销员" || rows[2][22] != "" {
		t.Fatal("distribution borrowed another item")
	}
	formula, _ := f.GetCellFormula("订单信息", "R3")
	kind, _ := f.GetCellType("订单信息", "R3")
	if formula != "" || kind != excelize.CellTypeSharedString || rows[2][17] != first.Items[0].ProductName {
		t.Fatal("user content must remain literal text")
	}
	replay, err := service.Export(context.Background(), orderport.ListQuery{}, "xlsx", 7, "full-export-key-0001")
	if err != nil || !bytes.Equal(replay.Content, result.Content) || replay.ReceiptID != result.ReceiptID {
		t.Fatal("xlsx replay must be byte deterministic")
	}
	if _, err = service.ExportCSV(context.Background(), orderport.ListQuery{}, 7, "full-export-key-0001"); !errors.Is(err, orderport.ErrConflict) {
		t.Fatal("same key cannot change format")
	}
	csvExport, err := service.ExportCSV(context.Background(), orderport.ListQuery{}, 7, "full-export-csv-new-key-0001")
	if err != nil {
		t.Fatal(err)
	}
	csvRows, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(string(csvExport.Content), "\ufeff"))).ReadAll()
	if err != nil || len(csvRows) != 4 || len(csvRows[0]) != 27 || csvRows[2][17] != "'"+first.Items[0].ProductName || csvRows[2][11] != "13800000000" || csvRows[2][7] != "20.50" {
		t.Fatal("CSV must share full facts and protect formula text")
	}

}

func TestExportFailsClosedForReadDecryptAndEnrichmentFailures(t *testing.T) {
	for _, kind := range []string{"read", "decrypt", "enrichment"} {
		t.Run(kind, func(t *testing.T) {
			store := &exportMemoryStore{memoryStore: newMemoryStore(), contacts: map[int64]orderport.CheckoutContact{1: {MobileCollected: true, MobileE164: "13800000000"}}}
			service := NewService(directUOW{}, store)
			service.SetContactCipher(exportCipher{bad: kind == "decrypt"})
			service.SetExportFactsReader(exportFactsStub{})
			if kind == "read" {
				store.readErr = errors.New("read failure")
			}
			if kind == "enrichment" {
				service.SetExportFactsReader(exportFactsStub{err: errors.New("facts unavailable")})
			}
			input := orderInput("failed-export")
			o, _ := domain.NewOrder(input)
			snapshot := o.Snapshot()
			snapshot.ID = 1
			store.orders[1] = snapshot
			store.nextID = 2
			if _, err := service.Export(context.Background(), orderport.ListQuery{}, "xlsx", 7, "failed-export-key-0001"); !errors.Is(err, orderport.ErrUnavailable) || len(store.exports) != 0 {
				t.Fatal("failed reads cannot become a successful incomplete download")
			}
		})
	}
}
func TestExportEmptyWorkbookAndExistingLimits(t *testing.T) {
	store := &exportMemoryStore{memoryStore: newMemoryStore()}
	service := NewService(directUOW{}, store)
	result, err := service.Export(context.Background(), orderport.ListQuery{}, "xlsx", 7, "empty-export-key-0001")
	if err != nil {
		t.Fatal(err)
	}
	f, err := excelize.OpenReader(bytes.NewReader(result.Content))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	dimension, _ := f.GetSheetDimension("订单信息")
	if dimension != "A1:AA1" {
		t.Fatal("empty export must publish every header to spreadsheet readers")
	}
	rows, _ := f.GetRows("订单信息")
	if len(rows) != 1 || len(rows[0]) != 27 || result.Rows != 0 {
		t.Fatal("empty export still needs exact headers")
	}
	input := orderInput("large-export")
	for i := 2; i <= 100; i++ {
		input.Items = append(input.Items, domain.ItemSnapshot{LineNo: int32(i), ProductCode: "sku", ProductName: "fixture", Quantity: 1, UnitAmountMinor: 1000, LineAmountMinor: 1000})
	}
	input.Amount.AmountMinor = 100000
	order, err := domain.NewOrder(input)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := order.Snapshot()
	snapshot.ID = 1
	store.orders[1] = snapshot
	store.nextID = 2
	for id := int64(2); id <= 101; id++ {
		v := snapshot
		v.ID = id
		store.orders[id] = v
	}
	store.nextID = 102
	preview, err := service.PreviewExport(context.Background(), orderport.ListQuery{})
	if err != nil || !preview.Truncated || preview.ItemRows != 10100 {
		t.Fatal("must detect detail row limit before download")
	}
	if _, err = service.Export(context.Background(), orderport.ListQuery{}, "xlsx", 7, "large-export-key-0001"); !errors.Is(err, orderport.ErrExportTooLarge) {
		t.Fatal("must reject oversized detail export without truncation")
	}
}

func TestExportCSVByteLimitRejectsWithoutReceipt(t *testing.T) {
	store := &exportMemoryStore{memoryStore: newMemoryStore(), contacts: map[int64]orderport.CheckoutContact{}}
	service := NewService(directUOW{}, store)
	service.SetExportFactsReader(exportFactsStub{})
	input := orderInput("export-byte-limit")
	input.Items = nil
	for i := 1; i <= 100; i++ {
		input.Items = append(input.Items, domain.ItemSnapshot{LineNo: int32(i), ProductCode: strings.Repeat("s", 200), ProductName: strings.Repeat("n", 500), Quantity: 1, UnitAmountMinor: 1000, LineAmountMinor: 1000})
	}
	input.Amount.AmountMinor = 100000
	o, err := domain.NewOrder(input)
	if err != nil {
		t.Fatal(err)
	}
	for id := int64(1); id <= 100; id++ {
		snapshot := o.Snapshot()
		snapshot.ID = id
		store.orders[id] = snapshot
	}
	store.nextID = 101
	if _, err = service.ExportCSV(context.Background(), orderport.ListQuery{}, 7, "export-byte-limit-key-0001"); !errors.Is(err, orderport.ErrExportTooLarge) {
		t.Fatal("must reject existing byte limit")
	}
	if len(store.exports) != 0 {
		t.Fatal("oversized export must not persist a receipt")
	}
}

func TestExportUsesFrozenCouponPriceAndConfirmedRefund(t *testing.T) {
	store := &exportMemoryStore{memoryStore: newMemoryStore(), contacts: map[int64]orderport.CheckoutContact{}}
	service := NewService(directUOW{}, store)
	coupons := &checkoutCouponStub{reserved: couponport.ReservationSnapshot{CouponApplied: true, ReservationRef: "redemption-export", ClaimID: 21, CouponID: 22, ProductID: 5, ProductType: "standard_product", ProductCode: "course-5", RuleVersion: 3, GrossAmountMinor: 8800, DiscountAmountMinor: 1800, PayableAmountMinor: 7000, Currency: "CNY"}}
	service.SetCheckoutCouponCoordinator(coupons)
	o, err := service.CreatePaymentOrderWithin(context.Background(), orderport.PaymentOrderCommand{Provider: domain.ProviderWeChatPay, MerchantOrderNo: "export-coupon-checkout", PayerCustomerID: 11, BeneficiaryCustomerID: 22, ProductID: 5, CouponClaimID: 21, ProductCode: "course-5", ProductName: "优惠订单商品", ProductVersion: 3, ProductType: "standard_product", UnitAmountMinor: 8800, Currency: "CNY", ActorScope: "payment-session:export-coupon", IdempotencyKey: "checkout-export-coupon-key-0001"})
	if err != nil {
		t.Fatal(err)
	}
	paid := o.CreatedAt.Add(time.Minute)
	if _, err = service.SettlePaymentWithin(context.Background(), orderport.PaymentSettlementCommand{OrderID: o.ID, ProviderTransactionNo: "paid-coupon-fact", OccurredAt: paid, ReceiptKey: "paid-export-coupon-key-0001"}); err != nil {
		t.Fatal(err)
	}
	if _, err = service.SettlePaymentWithin(context.Background(), orderport.PaymentSettlementCommand{OrderID: o.ID, RefundedDelta: 7000, OccurredAt: paid.Add(time.Minute), ReceiptKey: "refund-export-coupon-key-0001"}); err != nil {
		t.Fatal(err)
	}
	service.SetExportFactsReader(exportFactsStub{facts: orderport.ExportFacts{Payments: map[int64]orderport.ExportPayment{o.ID: {PaidAt: &paid}}}})
	result, err := service.Export(context.Background(), orderport.ListQuery{}, "xlsx", 7, "coupon-export-key-0001")
	if err != nil {
		t.Fatal(err)
	}
	f, err := excelize.OpenReader(bytes.NewReader(result.Content))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for cell, want := range map[string]string{"H2": "70.00", "J2": "70.00", "T2": "70.00", "U2": "已退款"} {
		value, _ := f.GetCellValue("订单信息", cell)
		if value != want {
			t.Fatalf("%s=%s want=%s", cell, value, want)
		}
	}
}
