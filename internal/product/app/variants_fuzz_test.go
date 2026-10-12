package app

import (
	"reflect"
	"testing"

	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

func FuzzVariantNormalizationPreservesCallerAndIsIdempotent(f *testing.F) {
	f.Add("标准版", "规格", "颜色", "蓝色", int64(1), int64(9900))
	f.Add("", "", "", "", int64(-1), int64(-1))
	f.Add(" 零元 ", " 描述 ", " 类型 ", " 免费 ", int64(0), int64(0))
	f.Fuzz(func(t *testing.T, name, description, key, value string, id, price int64) {
		if len(name)+len(description)+len(key)+len(value) > 32<<10 {
			return
		}
		original := []productport.Variant{{ID: id, Name: name, Description: description, PriceMinor: price, Attributes: []productport.VariantAttribute{{Key: key, Value: value}}}}
		expected := []productport.Variant{{ID: id, Name: name, Description: description, PriceMinor: price, Attributes: []productport.VariantAttribute{{Key: key, Value: value}}}}
		normalized := original
		err := normalizeVariants(&normalized)
		if !reflect.DeepEqual(original, expected) {
			t.Fatal("validation changed caller-owned variant or attributes")
		}
		if err != nil {
			return
		}
		stable := normalized
		if err := normalizeVariants(&stable); err != nil || !reflect.DeepEqual(normalized, stable) {
			t.Fatal("accepted normalized variants cannot be replayed unchanged")
		}
		normalized[0].Attributes[0].Value = "changed later"
		if !reflect.DeepEqual(original, expected) {
			t.Fatal("accepted variant retains mutable caller-owned attributes")
		}
	})
}
