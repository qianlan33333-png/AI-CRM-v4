package app

import (
	"bytes"
	"context"
	"encoding/csv"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	"github.com/xuri/excelize/v2"
)

func TestExportFinancialReconciliationStates(t *testing.T) {
	paidAt := time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)
	base := domain.Snapshot{Amount: domain.Money{AmountMinor: 10000, Currency: "CNY"}, Status: domain.StatusPaid, Provider: domain.ProviderWeChatPay, RecordOrigin: domain.RecordOriginNative}
	paid := orderport.ExportPayment{PaidAt: &paidAt, AmountMinor: 10000}
	cases := []struct {
		name       string
		status     domain.Status
		provider   domain.Provider
		refund     int64
		payment    orderport.ExportPayment
		commission orderport.ExportCommission
		want       []string
	}{
		{"ordinary", domain.StatusPaid, domain.ProviderWeChatPay, 0, paid, orderport.ExportCommission{}, []string{"100.00", "100.00", "0.00", "0.00", "0.00", "0.00", "100.00", "100.00", "无分账"}},
		{"unpaid", domain.StatusPendingPayment, domain.ProviderWeChatPay, 0, orderport.ExportPayment{}, orderport.ExportCommission{}, []string{"100.00", "0.00", "0.00", "0.00", "0.00", "0.00", "100.00", "0.00", "无分账"}},
		{"waiting", domain.StatusPaid, domain.ProviderWeChatPay, 0, paid, orderport.ExportCommission{PayableMinor: 3000, PendingMinor: 3000}, []string{"100.00", "100.00", "30.00", "0.00", "30.00", "0.00", "70.00", "100.00", "待分账"}},
		{"partial", domain.StatusPaid, domain.ProviderWeChatPay, 0, orderport.ExportPayment{PaidAt: &paidAt, AmountMinor: 10000, SharedMinor: 1000}, orderport.ExportCommission{PayableMinor: 3000, PendingMinor: 2000}, []string{"100.00", "100.00", "30.00", "10.00", "20.00", "0.00", "70.00", "90.00", "部分分账"}},
		{"successful", domain.StatusPaid, domain.ProviderWeChatPay, 0, orderport.ExportPayment{PaidAt: &paidAt, AmountMinor: 10000, SharedMinor: 3000}, orderport.ExportCommission{PayableMinor: 3000}, []string{"100.00", "100.00", "30.00", "30.00", "0.00", "0.00", "70.00", "70.00", "已分账"}},
		{"failed", domain.StatusPaid, domain.ProviderWeChatPay, 0, orderport.ExportPayment{PaidAt: &paidAt, AmountMinor: 10000, SharingException: true}, orderport.ExportCommission{PayableMinor: 3000, PendingMinor: 3000}, []string{"100.00", "100.00", "30.00", "0.00", "30.00", "0.00", "70.00", "100.00", "异常待核对"}},
		{"processing", domain.StatusPaid, domain.ProviderWeChatPay, 0, orderport.ExportPayment{PaidAt: &paidAt, AmountMinor: 10000, SharingProcessing: true}, orderport.ExportCommission{PayableMinor: 3000, PendingMinor: 3000}, []string{"100.00", "100.00", "30.00", "0.00", "30.00", "0.00", "70.00", "", "分账处理中"}},
		{"mixed unknown", domain.StatusPaid, domain.ProviderWeChatPay, 0, orderport.ExportPayment{PaidAt: &paidAt, AmountMinor: 10000, SharedMinor: 1000, SharingUnknown: true}, orderport.ExportCommission{PayableMinor: 3000, PendingMinor: 2000}, []string{"100.00", "100.00", "30.00", "10.00", "20.00", "0.00", "70.00", "", "分账结果待确认"}},
		{"refund before split", domain.StatusRefunded, domain.ProviderWeChatPay, 10000, paid, orderport.ExportCommission{}, []string{"100.00", "100.00", "0.00", "0.00", "0.00", "0.00", "0.00", "0.00", "无分账"}},
		{"refund after split loss", domain.StatusRefunded, domain.ProviderWeChatPay, 10000, orderport.ExportPayment{PaidAt: &paidAt, AmountMinor: 10000, SharedMinor: 3000}, orderport.ExportCommission{Exception: true}, []string{"100.00", "100.00", "0.00", "30.00", "0.00", "0.00", "0.00", "-30.00", "异常待核对"}},
		{"refund recovered", domain.StatusRefunded, domain.ProviderWeChatPay, 10000, orderport.ExportPayment{PaidAt: &paidAt, AmountMinor: 10000, SharedMinor: 3000}, orderport.ExportCommission{RecoveredMinor: 3000}, []string{"100.00", "100.00", "0.00", "30.00", "0.00", "30.00", "0.00", "0.00", "已分账"}},
		{"missing confirmation", domain.StatusPaid, domain.ProviderWeChatPay, 0, orderport.ExportPayment{}, orderport.ExportCommission{}, []string{"100.00", "", "0.00", "0.00", "0.00", "0.00", "100.00", "", "支付确认待核对"}},
		{"shop unknown", domain.StatusPaid, domain.ProviderWeChatShop, 0, orderport.ExportPayment{}, orderport.ExportCommission{}, []string{"100.00", "100.00", "", "", "", "", "", "", "历史或渠道分账未知"}},
		{"history unknown", domain.StatusPaid, domain.ProviderExternalHistory, 0, orderport.ExportPayment{}, orderport.ExportCommission{}, []string{"100.00", "100.00", "", "", "", "", "", "", "历史或渠道分账未知"}},
		{"alipay order evidence", domain.StatusPaid, domain.ProviderAlipay, 0, orderport.ExportPayment{}, orderport.ExportCommission{}, []string{"100.00", "100.00", "0.00", "0.00", "0.00", "0.00", "100.00", "100.00", "无分账"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := base
			o.Status = tc.status
			o.Provider = tc.provider
			o.RefundedMinor = tc.refund
			if got := exportFinancialColumns(o, tc.payment, tc.commission); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got=%q want=%q", got, tc.want)
			}
		})
	}
}

func TestExportFinancialColumnsCountEachOrderOnceAndKeepSignedNumbers(t *testing.T) {
	store := &exportMemoryStore{memoryStore: newMemoryStore()}
	s := NewService(directUOW{}, store)
	input := orderInput("financial-multi-item")
	input.Items = append(input.Items, domain.ItemSnapshot{LineNo: 2, ProductCode: "second", ProductName: "=fixture", Quantity: 1, UnitAmountMinor: 1000, LineAmountMinor: 1000})
	input.Amount.AmountMinor = 2000
	o, err := domain.NewOrder(input)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := o.Snapshot()
	snapshot.ID = 1
	snapshot.Status = domain.StatusRefunded
	snapshot.RefundedMinor = 2000
	store.orders[1] = snapshot
	store.nextID = 2
	paid := time.Now()
	s.SetExportFactsReader(exportFactsStub{facts: orderport.ExportFacts{Payments: map[int64]orderport.ExportPayment{1: {PaidAt: &paid, AmountMinor: 2000, SharedMinor: 1}}}})
	result, err := s.Export(context.Background(), orderport.ListQuery{}, "xlsx", 7, "financial-multi-item-xlsx")
	if err != nil {
		t.Fatal(err)
	}
	f, err := excelize.OpenReader(bytes.NewReader(result.Content))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rows, err := f.GetRows("订单信息")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || rows[1][34] != "-0.01" {
		t.Fatalf("rows=%q", rows)
	}
	for c := 27; c <= 35; c++ {
		cell, _ := excelize.CoordinatesToCellName(c+1, 3)
		v, _ := f.GetCellValue("订单信息", cell)
		if v != "" {
			t.Fatalf("item repeats money at %s: %q", cell, v)
		}
	}
	for _, cell := range []string{"AB2", "AC2", "AE2", "AI2"} {
		kind, _ := f.GetCellType("订单信息", cell)
		if kind != excelize.CellTypeNumber && kind != excelize.CellTypeUnset {
			t.Fatalf("money %s is not numeric: %v", cell, kind)
		}
	}
	if note, _ := f.GetCellValue("金额口径", "B2"); !strings.Contains(note, "只在首条商品明细") {
		t.Fatal("missing accounting note")
	}
	c, err := s.ExportCSV(context.Background(), orderport.ListQuery{}, 7, "financial-multi-item-csv")
	if err != nil {
		t.Fatal(err)
	}
	csvRows, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(string(c.Content), "\ufeff"))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if csvRows[1][34] != "-0.01" || csvRows[2][17] != "'=fixture" {
		t.Fatalf("signed amount or formula escaping lost: %q", csvRows)
	}
	for _, value := range csvRows[2][27:] {
		if value != "" {
			t.Fatal("CSV repeated order money")
		}
	}
}
