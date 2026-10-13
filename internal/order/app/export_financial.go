package app

import (
	"strconv"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	"github.com/xuri/excelize/v2"
)

func init() {
	exportHeaders = append(exportHeaders, "订单应收金额", "买家实付金额", "应分账金额", "已分账金额", "待分账金额", "分账追回金额", "商家应收净额（未扣手续费）", "商家实收净额（未扣手续费）", "分账状态")
}

func exportNumericColumn(c int) bool {
	return c == 7 || c == 9 || c == 18 || c == 19 || (c >= 27 && c <= 34)
}

func exportFinancialColumns(o domain.Snapshot, payment orderport.ExportPayment, commission orderport.ExportCommission) []string {
	paid := int64(0)
	confirmed := payment.PaidAt != nil
	orderPaid := o.Status == domain.StatusPaid || o.Status == domain.StatusPartiallyRefunded || o.Status == domain.StatusRefunded
	if confirmed {
		paid = payment.AmountMinor
	} else if orderPaid {
		paid = o.Amount.AmountMinor
	}
	state := "无分账"
	if commission.PendingMinor > 0 {
		state = "待分账"
	}
	if payment.SharedMinor > 0 {
		state = "已分账"
		if commission.PendingMinor > 0 {
			state = "部分分账"
		}
	}
	if payment.SharingException || commission.Exception {
		state = "异常待核对"
	}
	if payment.SharingProcessing {
		state = "分账处理中"
	}
	if payment.SharingUnknown {
		state = "分账结果待确认"
	}
	missingPayment := orderPaid && o.Amount.AmountMinor > 0 && o.RecordOrigin == domain.RecordOriginNative && o.Provider == domain.ProviderWeChatPay && !confirmed
	if missingPayment {
		state = "支付确认待核对"
	}
	sharingUnavailable := o.RecordOrigin != domain.RecordOriginNative || o.Provider == domain.ProviderWeChatShop || o.Provider == domain.ProviderExternalHistory
	if sharingUnavailable {
		state = "历史或渠道分账未知"
	}
	received := decimalMinor(paid)
	if missingPayment {
		received = ""
	}
	payable, pending, recovered := decimalMinor(commission.PayableMinor), decimalMinor(commission.PendingMinor), decimalMinor(commission.RecoveredMinor)
	expected := decimalMinor(o.Amount.AmountMinor - o.RefundedMinor - commission.PayableMinor)
	shared, net := decimalMinor(payment.SharedMinor), decimalMinor(paid-o.RefundedMinor-payment.SharedMinor+commission.RecoveredMinor)
	if sharingUnavailable {
		shared, payable, pending, recovered, expected = "", "", "", "", ""
	}
	if sharingUnavailable || missingPayment || payment.SharingUnknown || payment.SharingProcessing {
		net = ""
	}
	return []string{decimalMinor(o.Amount.AmountMinor), received, payable, shared, pending, recovered, expected, net, state}
}

var exportMoneyNotes = [][]string{
	{"字段", "金额口径（金额单位与订单币种一致）"},
	{"新增金额列", "每单只在首条商品明细填写，其余明细留空，可直接整列求和；原应付金额和退款金额仍按明细重复，勿直接求和。"},
	{"订单应收金额", "下单冻结的优惠后应付金额；未支付订单也保留应收。"},
	{"买家实付金额", "已确认买家支付金额，未支付为0；历史及渠道订单按已有支付事实展示。"},
	{"应分账金额", "当前佣金应付总额；待分账是活跃佣金尚未支付的部分。"},
	{"已分账金额", "仅计本地已确认接收方成功且结果明确的分账金额，不计受理、处理中、失败或结果未知。"},
	{"分账追回金额", "已记录的人工佣金追回；商家自行承担不算追回。"},
	{"商家应收净额（未扣手续费）", "订单应收金额－成功退款金额－应分账金额。"},
	{"商家实收净额（未扣手续费）", "买家实付金额－成功退款金额－已分账金额＋分账追回金额；退款后佣金未追回可能为负数。"},
	{"空白净额", "分账处理中、结果未知、缺少支付确认或历史/渠道分账记录未知时留空，请核对支付及分账账单，不能当作0。"},
	{"对账边界", "上述净额未扣支付手续费，也不代表银行到账；时间筛选仍为订单创建时间，分账/退款金额为本次导出快照累计值。"},
}

func addExportMoneyNotes(f *excelize.File, header int) error {
	sheet := "金额口径"
	if _, err := f.NewSheet(sheet); err != nil {
		return err
	}
	for r, row := range exportMoneyNotes {
		for c, value := range row {
			cell, _ := excelize.CoordinatesToCellName(c+1, r+1)
			if err := f.SetCellStr(sheet, cell, value); err != nil {
				return err
			}
		}
	}
	if err := f.SetColWidth(sheet, "A", "A", 36); err != nil {
		return err
	}
	if err := f.SetColWidth(sheet, "B", "B", 95); err != nil {
		return err
	}
	wrapped, err := f.NewStyle(&excelize.Style{Alignment: &excelize.Alignment{WrapText: true, Vertical: "top"}})
	if err != nil {
		return err
	}
	if err := f.SetCellStyle(sheet, "A1", "B"+strconv.Itoa(len(exportMoneyNotes)), wrapped); err != nil {
		return err
	}
	if err := f.SetCellStyle(sheet, "A1", "B1", header); err != nil {
		return err
	}
	for r := 2; r <= len(exportMoneyNotes); r++ {
		if err := f.SetRowHeight(sheet, r, 44); err != nil {
			return err
		}
	}
	return nil
}
