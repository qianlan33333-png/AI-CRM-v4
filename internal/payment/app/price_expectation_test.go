package app

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
)

func TestCheckoutExpectedAmountKeepsLegacyDigestWhenAbsent(t *testing.T) {
	c := paymentport.CreateCommand{ProductID: 7, ProductType: "standard", SessionToken: "test-session-not-a-secret", IdempotencyKey: "digest-compatible-checkout"}
	typ := reflect.TypeOf(c)
	value := reflect.ValueOf(c)
	fields := []reflect.StructField{}
	indices := []int{}
	for i := range typ.NumField() {
		if typ.Field(i).Name != "ExpectedAmountMinor" {
			fields = append(fields, typ.Field(i))
			indices = append(indices, i)
		}
	}
	old := reflect.New(reflect.StructOf(fields)).Elem()
	for i, index := range indices {
		old.Field(i).Set(value.Field(index))
	}
	legacy, e := json.Marshal(old.Interface())
	if e != nil {
		t.Fatal(e)
	}
	encoded, e := json.Marshal(c)
	if e != nil || !bytes.Equal(encoded, legacy) {
		t.Fatalf("absent expected amount changed legacy encoding err=%v", e)
	}
	zero := int64(0)
	c.ExpectedAmountMinor = &zero
	encoded, e = json.Marshal(c)
	if e != nil || bytes.Equal(encoded, legacy) || !bytes.Contains(encoded, []byte(`"ExpectedAmountMinor":0`)) {
		t.Fatalf("explicit zero must bind the request digest err=%v", e)
	}
}
