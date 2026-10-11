package domain

import (
	"testing"
	"time"
)

func TestShopExternalSourceMoneyDoesNotRelaxNativeMath(t *testing.T) {
	native := nativeInput()
	native.Items[0].LineAmountMinor = 80
	native.Amount.AmountMinor = 85
	if _, e := NewOrder(native); e == nil {
		t.Fatal("native discount mismatch accepted")
	}
	at := time.Now()
	s := Snapshot{Provider: ProviderWeChatShop, SourceSystem: "wechat-shop:app", SourceKey: "shop", MerchantOrderNo: "shop", Status: StatusPaid, RecordOrigin: RecordOriginExternal, Version: 2, Amount: Money{AmountMinor: 85, Currency: "CNY"}, CreatedAt: at, UpdatedAt: at, Items: []ItemSnapshot{{LineNo: 1, ProductCode: "sku", ProductName: "商品", UnitAmountMinor: 100, Quantity: 2, LineAmountMinor: 80}}}
	if _, e := Restore(s); e != nil {
		t.Fatal("external source money rejected", e)
	}
	s.Amount.AmountMinor = 0
	s.Items[0].LineAmountMinor = 0
	s.Items[0].UnitAmountMinor = 0
	if _, e := Restore(s); e != nil {
		t.Fatal("external zero paid rejected", e)
	}
	s.EffectEligible = true
	if _, e := Restore(s); e == nil {
		t.Fatal("external native fanout allowed")
	}
	s.EffectEligible = false
	s.Provider = ProviderAlipay
	if _, e := Restore(s); e == nil {
		t.Fatal("external path used to weaken other provider math")
	}
}
