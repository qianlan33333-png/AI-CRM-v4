package provider

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"testing"

	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
)

type alipayMaterialUOW struct{}

func (alipayMaterialUOW) Within(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

type alipayIntentReader struct{ intent paymentport.ProviderIntent }

func (reader alipayIntentReader) ProviderIntent(context.Context, effectport.Kind, effectport.Digest) (paymentport.ProviderIntent, error) {
	return reader.intent, nil
}

type alipayCheckoutReader struct{ snapshot orderport.CheckoutSnapshot }

func (reader alipayCheckoutReader) ReadCheckoutSnapshotWithin(context.Context, int64) (orderport.CheckoutSnapshot, error) {
	return reader.snapshot, nil
}

func TestAlipayMaterialUsesImmutableOrderCheckoutSubject(t *testing.T) {
	source := effectport.Hash("payment", "48")
	intent := paymentport.ProviderIntent{Kind: effectport.KindAlipayWapPay, PaymentID: 48, OrderID: 48, MerchantOrderNo: "M-alipay-48", AmountMinor: 1980000, Currency: "CNY", SourceRefDigest: source, PayloadDigest: effectport.Hash("payload", "48")}
	snapshot := orderport.CheckoutSnapshot{OrderID: 48, ProductName: "OPC/OPT商学院", PayableAmountMinor: 1980000, Currency: "CNY"}
	loader := DBMaterialLoader{UOW: alipayMaterialUOW{}, Intents: alipayIntentReader{intent}, Checkouts: alipayCheckoutReader{snapshot}}
	material, err := loader.Load(context.Background(), effectport.KindAlipayWapPay, source)
	if err != nil || material.AlipaySubject != snapshot.ProductName || material.Intent.ProductID != "" {
		t.Fatalf("legacy intent did not recover frozen product title: %+v, %v", material, err)
	}

	snapshot.PayableAmountMinor++
	loader.Checkouts = alipayCheckoutReader{snapshot}
	if _, err = loader.Load(context.Background(), effectport.KindAlipayWapPay, source); !errors.Is(err, ErrInvalidMaterial) {
		t.Fatalf("mismatched amount accepted: %v", err)
	}
	loader.Checkouts = nil
	if _, err = loader.Load(context.Background(), effectport.KindAlipayWapPay, source); !errors.Is(err, ErrInvalidMaterial) {
		t.Fatalf("missing Order port accepted: %v", err)
	}
}

func TestAlipayExecuteSignsFrozenProductTitleWithoutNetworkCall(t *testing.T) {
	provider, _ := alipayContractFixture(t, "")
	for _, kind := range []effectport.Kind{effectport.KindAlipayWapPay, effectport.KindAlipayPagePay} {
		envelope := effectport.Envelope{Owner: effectport.OwnerPayment, Kind: kind, PayloadDigest: effectport.Hash("payload", string(kind))}
		provider.loader = loaderStub{material: Material{Intent: paymentport.ProviderIntent{MerchantOrderNo: "M-alipay-48", AmountMinor: 1980000, PayloadDigest: envelope.PayloadDigest}, AlipaySubject: "OPC/OPT商学院"}}
		result, err := provider.Execute(context.Background(), envelope, effectport.Attempt{Number: 1})
		if err != nil || result.Completion != effectport.StateExecuted || result.Artifact.Kind != "alipay_web_pay_url_v1" {
			t.Fatalf("%s URL not generated: %+v, %v", kind, result, err)
		}
		var handoff struct {
			RedirectURL string `json:"redirectUrl"`
		}
		if err = json.Unmarshal(result.Artifact.Payload, &handoff); err != nil {
			t.Fatal(err)
		}
		parsed, err := url.Parse(handoff.RedirectURL)
		if err != nil || parsed.Query().Get("sign") == "" {
			t.Fatalf("%s unsigned redirect URL: %v", kind, err)
		}
		var biz struct {
			Subject     string `json:"subject"`
			OutTradeNo  string `json:"out_trade_no"`
			TotalAmount string `json:"total_amount"`
		}
		if err = json.Unmarshal([]byte(parsed.Query().Get("biz_content")), &biz); err != nil || biz.Subject != "OPC/OPT商学院" || biz.OutTradeNo != "M-alipay-48" || biz.TotalAmount != "19800.00" {
			t.Fatalf("%s signed content invalid: %+v, %v", kind, biz, err)
		}
	}
}
