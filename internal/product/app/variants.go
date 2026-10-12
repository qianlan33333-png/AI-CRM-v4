package app

import (
	"strings"

	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

func normalizeVariants(input *[]productport.Variant) error {
	if input == nil {
		return nil
	}
	seen := map[int64]bool{}
	values := append([]productport.Variant{}, (*input)...)
	for i := range values {
		v := &values[i]
		v.Name, v.Description = strings.TrimSpace(v.Name), strings.TrimSpace(v.Description)
		if v.ID < 0 || v.Name == "" || len(v.Name) > 200 || len(v.Description) > 10000 || v.PriceMinor < 0 || (v.ID > 0 && seen[v.ID]) {
			return ErrInvalidProduct
		}
		seen[v.ID] = true
		v.Attributes = append([]productport.VariantAttribute{}, v.Attributes...)
		keys := map[string]bool{}
		for j := range v.Attributes {
			a := &v.Attributes[j]
			a.Key = strings.TrimSpace(a.Key)
			a.Value = strings.TrimSpace(a.Value)
			if a.Key == "" || a.Value == "" || len(a.Key) > 200 || len(a.Value) > 10000 || keys[a.Key] {
				return ErrInvalidProduct
			}
			keys[a.Key] = true
		}
	}
	*input = values
	return nil
}
