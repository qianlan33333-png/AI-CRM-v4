package app

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/presentationtime"
	"github.com/xuri/excelize/v2"
)

var exportHeaders = []string{"线上订单号", "下单时间", "买家帐号", "买家昵称", "支付单号", "付款方式", "付款时间", "应付金额", "币种", "退款金额", "收货人姓名", "手机", "省份", "城市", "区县", "详细地址", "商品编码", "商品名称", "数量", "商品单价", "状态", "订单来源", "分销员编号", "分销员名称", "原平台状态", "原平台售后状态", "导入时间"}

func (s *Service) SetExportFactsReader(reader orderport.ExportFactsReader) error {
	if s == nil || reader == nil {
		return orderport.ErrConflict
	}
	s.exportFacts = reader
	return nil
}

func exportFilter(q orderport.ListQuery) ListFilter { f := filterFrom(q); f.Offset = 0; return f }
func itemRows(orders []domain.Order) int {
	n := 0
	for _, o := range orders {
		n += len(o.Snapshot().Items)
	}
	return n
}

func (s *Service) PreviewExport(ctx context.Context, q orderport.ListQuery) (orderport.ExportPreview, error) {
	if !ready(s) || !validListQuery(q) {
		return orderport.ExportPreview{}, orderport.ErrConflict
	}
	var orders []domain.Order
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var err error
		orders, err = s.store.Export(tx, exportFilter(q), 10001)
		return err
	})
	if err != nil {
		return orderport.ExportPreview{}, classify(err)
	}
	n := itemRows(orders)
	return orderport.ExportPreview{Rows: min(n, 10000), Orders: len(orders), ItemRows: n, Truncated: len(orders) > 10000 || n > 10000}, nil
}
func (s *Service) ExportCSV(ctx context.Context, q orderport.ListQuery, actor int64, key string) (orderport.ExportResult, error) {
	return s.Export(ctx, q, "csv", actor, key)
}
func (s *Service) Export(ctx context.Context, q orderport.ListQuery, format string, actor int64, key string) (orderport.ExportResult, error) {
	if !ready(s) || actor < 1 || !validKey(key) || !validListQuery(q) || (format != "xlsx" && format != "csv") {
		return orderport.ExportResult{}, orderport.ErrConflict
	}
	payload, _ := json.Marshal(struct {
		Schema, Format string
		Filter         ListFilter
	}{"order-export-27-v1", format, exportFilter(q)})
	receipt := ExportReceipt{Actor: actor, KeyDigest: sha256.Sum256([]byte(key)), FilterDigest: sha256.Sum256(payload), CreatedAt: s.now().UTC()}
	var result orderport.ExportResult
	err := s.uow.Within(ctx, func(tx context.Context) error {
		// Every local enrichment read and the audit receipt shares this snapshot.
		if snapshot, ok := s.store.(interface{ BeginExportSnapshot(context.Context) error }); ok {
			if err := snapshot.BeginExportSnapshot(tx); err != nil {
				return err
			}
		}
		orders, err := s.store.Export(tx, exportFilter(q), 10001)
		if err != nil {
			return err
		}
		if len(orders) > 10000 || itemRows(orders) > 10000 {
			return orderport.ErrExportTooLarge
		}
		rows, err := s.exportRowsWithin(tx, orders)
		if err != nil {
			return err
		}
		var content []byte
		if format == "xlsx" {
			content, err = encodeXLSX(rows)
		} else {
			content = encodeExportCSV(rows)
		}
		if err != nil {
			return err
		}
		if len(content) > 5<<20 {
			return orderport.ErrExportTooLarge
		}
		receipt.RowCount, receipt.ByteCount, receipt.ContentDigest = len(rows), len(content), sha256.Sum256(content)
		stored, _, err := s.store.RecordExport(tx, receipt)
		if err != nil {
			return err
		}
		if stored.FilterDigest != receipt.FilterDigest || stored.ContentDigest != receipt.ContentDigest || stored.RowCount != receipt.RowCount || stored.ByteCount != receipt.ByteCount {
			return orderport.ErrConflict
		}
		result = orderport.ExportResult{ReceiptID: stored.ID, Rows: len(rows), Bytes: len(content), Content: content, ContentDigest: receipt.ContentDigest}
		return nil
	})
	if errors.Is(err, orderport.ErrExportTooLarge) {
		return orderport.ExportResult{}, err
	}
	return result, classify(err)
}

func (s *Service) exportRowsWithin(ctx context.Context, orders []domain.Order) ([][]string, error) {
	rows := make([][]string, 0, itemRows(orders))
	if len(orders) == 0 {
		return rows, nil
	}
	if s.exportFacts == nil {
		return nil, orderport.ErrUnavailable
	}
	snapshots := make([]domain.Snapshot, 0, len(orders))
	contacts := make(map[int64]orderport.CheckoutContact)
	var fallbackIDs []int64
	for _, o := range orders {
		snapshot := o.Snapshot()
		snapshots = append(snapshots, snapshot)
		address, collected, err := s.ReadCheckoutShippingAddressWithin(ctx, snapshot.ID)
		if err != nil {
			return nil, err
		}
		mobile, mobileCollected, err := s.ReadCheckoutMobileWithin(ctx, snapshot.ID)
		if err != nil {
			return nil, err
		}
		contacts[snapshot.ID] = orderport.CheckoutContact{ShippingAddress: address, AddressCollected: collected, MobileE164: mobile, MobileCollected: mobileCollected}
		if !mobileCollected && snapshot.PayerCustomerID != nil {
			fallbackIDs = append(fallbackIDs, *snapshot.PayerCustomerID)
		}
	}
	facts, err := s.exportFacts.ReadOrderExportFactsWithin(ctx, snapshots, fallbackIDs)
	if err != nil {
		return nil, orderport.ErrUnavailable
	}
	for _, o := range snapshots {
		var buyer orderport.ExportCustomer
		if o.PayerCustomerID != nil {
			buyer = facts.Customers[*o.PayerCustomerID]
		}
		contact := contacts[o.ID]
		phone := contact.MobileE164
		if !contact.MobileCollected {
			phone = buyer.Phone
		}
		phone = strings.TrimPrefix(phone, "+86")
		payment := facts.Payments[o.ID]
		transactionNo := o.ProviderTransactionNo
		if transactionNo == "" {
			transactionNo = payment.TransactionNo
		}
		paidAt := ""
		if payment.PaidAt != nil {
			paidAt = presentationtime.FormatShanghaiDateTime(*payment.PaidAt)
		}
		statusLabel := orderStatusLabel(o.Status)
		if o.Provider == domain.ProviderNoPayment && o.Status == domain.StatusPaid {
			statusLabel = "领取成功"
		}
		sourceStatus, sourceAfterSales, importedAt := "", "", ""
		if history := o.ExternalHistory; history != nil {
			if history.SourcePaidAt != nil {
				paidAt = presentationtime.FormatShanghaiDateTime(*history.SourcePaidAt)
			}
			sourceStatus, sourceAfterSales = history.SourceStatus, history.SourceAfterSales
			importedAt = presentationtime.FormatShanghaiDateTime(history.ImportedAt)
		}
		createdAt := presentationtime.FormatShanghaiDateTime(o.CreatedAt)
		if o.Provider == domain.ProviderExternalHistory {
			createdAt = ""
		} // only original payment time was imported; never label import time as checkout time.
		method := orderProviderLabel(o.Provider)
		if o.Provider == domain.ProviderExternalHistory {
			method = ""
		}
		address := contact.ShippingAddress
		items := append([]domain.ItemSnapshot(nil), o.Items...)
		sort.Slice(items, func(i, j int) bool { return items[i].LineNo < items[j].LineNo })
		for _, item := range items {
			var numbers, names []string
			for _, d := range facts.Distributors[o.ID] {
				if d.ItemLine == item.LineNo {
					numbers = append(numbers, d.Number)
					names = append(names, d.Name)
				}
			}
			rows = append(rows, []string{o.MerchantOrderNo, createdAt, buyer.Number, buyer.Name, transactionNo, method, paidAt, decimalMinor(o.Amount.AmountMinor), o.Amount.Currency, decimalMinor(o.RefundedMinor), address.RecipientName, phone, address.ProvinceName, address.CityName, address.DistrictName, address.DetailAddress, item.ProductCode, item.ProductName, strconv.Itoa(int(item.Quantity)), decimalMinor(item.UnitAmountMinor), statusLabel, orderRecordOriginLabel(o.RecordOrigin), strings.Join(numbers, ";"), strings.Join(names, ";"), sourceStatus, sourceAfterSales, importedAt})
		}
	}
	return rows, nil
}
func decimalMinor(n int64) string {
	return strconv.FormatInt(n/100, 10) + "." + string('0'+byte(n%100/10)) + string('0'+byte(n%10))
}
func encodeExportCSV(rows [][]string) []byte {
	var b bytes.Buffer
	b.WriteString("\xef\xbb\xbf")
	w := csv.NewWriter(&b)
	w.UseCRLF = true
	_ = w.Write(exportHeaders)
	for _, row := range rows {
		values := append([]string(nil), row...)
		for i, v := range values {
			trimmed := strings.TrimLeft(v, " \t\r\n")
			if strings.ContainsAny(trimmed[:min(1, len(trimmed))], "=+-@") || strings.HasPrefix(v, "\t") || strings.HasPrefix(v, "\r") {
				values[i] = "'" + v
			}
		}
		_ = w.Write(values)
	}
	w.Flush()
	return b.Bytes()
}
func encodeXLSX(rows [][]string) ([]byte, error) {
	f := excelize.NewFile()
	defer f.Close()
	sheet := "订单信息"
	if err := f.SetSheetName("Sheet1", sheet); err != nil {
		return nil, err
	}
	if err := f.SetDocProps(&excelize.DocProperties{Created: "2000-01-01T00:00:00Z", Modified: "2000-01-01T00:00:00Z"}); err != nil {
		return nil, err
	}
	all := append([][]string{exportHeaders}, rows...)
	for r, row := range all {
		for c, v := range row {
			if utf8.RuneCountInString(v) > 32767 {
				return nil, orderport.ErrConflict
			}
			for _, ch := range v {
				if (ch < 32 && ch != '\t' && ch != '\n' && ch != '\r') || ch == 0xfffe || ch == 0xffff {
					return nil, orderport.ErrConflict
				}
			}
			cell, _ := excelize.CoordinatesToCellName(c+1, r+1)
			if r > 0 && (c == 7 || c == 9 || c == 18 || c == 19) {
				if err := f.SetCellDefault(sheet, cell, v); err != nil {
					return nil, err
				}
			} else if err := f.SetCellStr(sheet, cell, v); err != nil {
				return nil, err
			}
		}
	}
	money, err := f.NewStyle(&excelize.Style{NumFmt: 2})
	if err != nil {
		return nil, err
	}
	if len(rows) > 0 {
		for _, col := range []string{"H", "J", "T"} {
			if err = f.SetCellStyle(sheet, col+"2", col+strconv.Itoa(len(rows)+1), money); err != nil {
				return nil, err
			}
		}
	}
	header, err := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true}})
	if err != nil {
		return nil, err
	}
	if err = f.SetCellStyle(sheet, "A1", "AA1", header); err != nil {
		return nil, err
	}
	if err = f.SetColWidth(sheet, "A", "AA", 20); err != nil {
		return nil, err
	}
	if err = f.SetColWidth(sheet, "P", "P", 45); err != nil {
		return nil, err
	}
	if err = f.SetPanes(sheet, &excelize.Panes{Freeze: true, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"}); err != nil {
		return nil, err
	}
	if err = f.SetSheetDimension(sheet, "A1:AA"+strconv.Itoa(len(rows)+1)); err != nil {
		return nil, err
	}
	buffer, err := f.WriteToBuffer()
	if err != nil {
		return nil, err
	}
	return stableZIP(buffer.Bytes())
}

// Excelize's package traversal order is not a byte-level replay contract.
// Sort entries and freeze ZIP metadata before the existing receipt hashes it.
func stableZIP(raw []byte) ([]byte, error) {
	z, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, err
	}
	sort.Slice(z.File, func(i, j int) bool { return z.File[i].Name < z.File[j].Name })
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for _, entry := range z.File {
		reader, err := entry.Open()
		if err != nil {
			return nil, err
		}
		content, err := io.ReadAll(reader)
		reader.Close()
		if err != nil {
			return nil, err
		}
		header := &zip.FileHeader{Name: entry.Name, Method: zip.Deflate}
		header.SetModTime(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))
		writer, err := w.CreateHeader(header)
		if err != nil {
			return nil, err
		}
		if _, err = writer.Write(content); err != nil {
			return nil, err
		}
	}
	if err = w.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
