package store

import (
	"errors"
	"strings"
	"testing"

	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
)

func TestAlipayIntentSubjectUsesLegacyFallbackOnlyWhenSnapshotKeyIsMissing(t *testing.T) {
	tests := []struct {
		name         string
		snapshot     string
		fallback     string
		want         string
		wantErr      bool
		wantFallback bool
	}{
		{name: "valid current snapshot", snapshot: `{"subject":"冻结标题"}`, want: "冻结标题"},
		{name: "missing old snapshot field", snapshot: `{"payment_id":7}`, fallback: "订单项标题", want: "订单项标题", wantFallback: true},
		{name: "present empty subject", snapshot: `{"subject":""}`, fallback: "订单项标题", wantErr: true},
		{name: "present null subject", snapshot: `{"subject":null}`, fallback: "订单项标题", wantErr: true},
		{name: "present wrong type", snapshot: `{"subject":7}`, fallback: "订单项标题", wantErr: true},
		{name: "present subject with controls", snapshot: `{"subject":"bad\ntitle"}`, fallback: "订单项标题", wantErr: true},
		{name: "present overlong subject", snapshot: `{"subject":"` + strings.Repeat("课", paymentport.AlipayMaxSubjectRunes+1) + `"}`, fallback: "订单项标题", wantErr: true},
		{name: "present padded subject", snapshot: `{"subject":" 标题 "}`, fallback: "订单项标题", wantErr: true},
		{name: "malformed snapshot", snapshot: `not-json`, fallback: "订单项标题", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fallbackCalled := false
			got, err := alipayIntentSubject([]byte(test.snapshot), func() (string, error) {
				fallbackCalled = true
				return test.fallback, nil
			})
			if test.wantErr {
				if !errors.Is(err, paymentport.ErrConflict) {
					t.Fatalf("expected a fail-closed conflict, got subject=%q err=%v", got, err)
				}
			} else if err != nil || got != test.want {
				t.Fatalf("subject=%q want=%q err=%v", got, test.want, err)
			}
			if fallbackCalled != test.wantFallback {
				t.Fatalf("legacy fallback called=%t want=%t", fallbackCalled, test.wantFallback)
			}
		})
	}
}

func TestLegacyAlipaySubjectRequiresExactlyOneImmutableItem(t *testing.T) {
	tests := []struct {
		name      string
		count     int64
		nameValue string
		code      string
		want      string
		wantErr   bool
	}{
		{name: "one item name", count: 1, nameValue: "商品标题", code: "product-1", want: "商品标题"},
		{name: "one item code fallback", count: 1, code: "product-1", want: "product-1"},
		{name: "truncate long Unicode title", count: 1, nameValue: strings.Repeat("课", paymentport.AlipayMaxSubjectRunes+10), want: strings.Repeat("课", paymentport.AlipayMaxSubjectRunes)},
		{name: "missing item", wantErr: true},
		{name: "ambiguous items", count: 2, nameValue: "商品标题", code: "product-1", wantErr: true},
		{name: "blank item facts", count: 1, nameValue: " ", code: " ", wantErr: true},
		{name: "control character", count: 1, nameValue: "bad\ntitle", code: "product-1", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := alipaySubjectFromOrderItemFacts(test.count, test.nameValue, test.code)
			if test.wantErr {
				if !errors.Is(err, paymentport.ErrConflict) {
					t.Fatalf("expected a fail-closed conflict, subject=%q err=%v", got, err)
				}
			} else if err != nil || got != test.want {
				t.Fatalf("subject=%q want=%q err=%v", got, test.want, err)
			}
		})
	}
}
