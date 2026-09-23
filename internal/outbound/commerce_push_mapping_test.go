package outbound

import (
	"context"
	"encoding/json"
	"errors"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
	"strings"
	"testing"
)

type mappingMobile struct {
	value string
	found bool
	err   error
	calls int
}

func (m *mappingMobile) ReadCheckoutMobileWithin(context.Context, int64) (string, bool, error) {
	m.calls++
	return m.value, m.found, m.err
}

type mappingNames struct {
	values map[customerdomain.CustomerID]string
	err    error
	calls  int
}

func (m *mappingNames) DisplayNames(context.Context, []customerdomain.CustomerID) (map[customerdomain.CustomerID]string, error) {
	m.calls++
	return m.values, m.err
}
func mappingFixture() *productport.FieldMapping {
	return &productport.FieldMapping{Version: 1, Fields: []productport.FieldMappingField{
		{Key: "phone", Source: "variable", ValueType: "string", Variable: "order.mobile"},
		{Key: "name", Source: "variable", ValueType: "string", Variable: "payer.nickname"},
		{Key: "amount", Source: "variable", ValueType: "number", Variable: "order.paid_amount_minor"},
		{Key: "constant", Source: "fixed", ValueType: "json", Value: json.RawMessage(`{"nested":[true,null,9007199254740993]}`)},
	}}
}
func TestCommerceMappedPaidPayloadSourcesAndMissing(t *testing.T) {
	id := int64(12)
	event := orderport.PaidEvent{OrderID: 7}
	event.Order.PayerCustomerID = &id
	event.Order.Amount.AmountMinor = 990
	mobile := &mappingMobile{value: "+8613800000000", found: true}
	names := &mappingNames{values: map[customerdomain.CustomerID]string{12: "昵称"}}
	service := &CommercePushService{}
	service.SetFieldMappingReaders(mobile, names)
	raw, err := service.mappedPaidPayload(context.Background(), event, mappingFixture())
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]json.RawMessage
	if json.Unmarshal(raw, &body) != nil || len(body) != 4 || string(body["amount"]) != "990" || string(body["phone"]) != `"+8613800000000"` || string(body["name"]) != `"昵称"` {
		t.Fatalf("incorrect mapping shape")
	}
	if string(body["constant"]) != `{"nested":[true,null,9007199254740993]}` {
		t.Fatal("fixed precision changed")
	}
	mobile.found = false
	names.values = nil
	raw, err = service.mappedPaidPayload(context.Background(), event, mappingFixture())
	if err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(raw, &body)
	if string(body["phone"]) != "null" || string(body["name"]) != "null" {
		t.Fatal("missing must be null")
	}
	mobile.err = errors.New("private unavailable detail")
	if _, err = service.mappedPaidPayload(context.Background(), event, mappingFixture()); !errors.Is(err, ErrCommerceMappingSourceUnavailable) {
		t.Fatal("source error hidden as missing")
	}
	mobile.err = nil
	names.err = errors.New("private read error")
	if _, err = service.mappedPaidPayload(context.Background(), event, mappingFixture()); !errors.Is(err, ErrCommerceMappingSourceUnavailable) {
		t.Fatal("name error hidden as missing")
	}
}
func TestCommerceMappedSyntheticAndFixedOnly(t *testing.T) {
	raw, err := commerceMappedSyntheticPayload(mappingFixture())
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]json.RawMessage
	_ = json.Unmarshal(raw, &body)
	if len(body) != 4 {
		t.Fatal("synthetic contains extra legacy fields")
	}
	fixed := &productport.FieldMapping{Version: 1, Fields: []productport.FieldMappingField{{Key: "constant", Source: "fixed", ValueType: "boolean", Value: json.RawMessage(`false`)}}}
	service := &CommercePushService{}
	if raw, err = service.mappedPaidPayload(context.Background(), orderport.PaidEvent{}, fixed); err != nil || string(raw) != `{"constant":false}` {
		t.Fatal("fixed mapping needs no source reader", err)
	}
}

func TestCommerceLegacyPreviewUsesActualPaidShapeWithoutIdentityReads(t *testing.T) {
	raw, err := commerceLegacyPaidPreviewPayload(7, "样例商品", CommercePushTarget{PushType: "paid_notify"})
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]json.RawMessage
	_ = json.Unmarshal(raw, &body)
	if string(body["event"]) != `"transaction.paid"` || len(body["order"]) == 0 || len(body["transaction"]) == 0 || len(body["buyer"]) == 0 {
		t.Fatal("preview is not actual paid shape")
	}
	if _, found := body["custom_params"]; found {
		t.Fatal("synthetic-only field added")
	}
}

func TestCommerceCustomHeadersIgnoreMappedBodyFields(t *testing.T) {
	execution := CommercePushExecution{PayloadMode: "custom_fields_v1", SourceReference: "order-paid:11:line:2", TargetSlot: "product:7"}
	body := []byte(`{"event":"attacker-event","delivery_id":"attacker-id","field":true}`)
	event, delivery, ok := commerceExecutionHeaderValues(execution, body)
	if !ok || event != "transaction.paid" || delivery != commerceDeliveryID(11, 2, "product:7") {
		t.Fatal("mapped body controlled transport headers")
	}
	execution.PayloadMode = "legacy"
	event, delivery, ok = commerceExecutionHeaderValues(execution, body)
	if !ok || event != "attacker-event" || delivery != "attacker-id" {
		t.Fatal("legacy header extraction changed")
	}
	execution.PayloadMode = "custom_fields_v1"
	execution.SourceReference = "synthetic:" + strings.Repeat("01", 32)
	event, delivery, ok = commerceExecutionHeaderValues(execution, []byte(`{"a":true}`))
	if !ok || event != "external_push.test" || !strings.HasPrefix(delivery, "commerce_test_") {
		t.Fatal("test headers missing")
	}
	execution.SourceReference = "untrusted"
	if _, _, ok = commerceExecutionHeaderValues(execution, body); ok {
		t.Fatal("malformed frozen source accepted")
	}
}
