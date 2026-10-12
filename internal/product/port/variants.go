package port

import "errors"

var ErrVariantSelection = errors.New("product variant selection invalid or outdated")

type VariantAttribute struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type Variant struct {
	ID          int64              `json:"id"`
	Name        string             `json:"name"`
	Description string             `json:"description"`
	Attributes  []VariantAttribute `json:"attributes"`
	PriceMinor  int64              `json:"price_minor"`
	Version     int64              `json:"version"`
}

// SelectedVariant validates a selection from Product's transaction-locked facts.
func (p CheckoutProduct) SelectedVariant(id, version int64) (*Variant, error) {
	if !p.VariantsEnabled {
		if id != 0 || version != 0 {
			return nil, ErrVariantSelection
		}
		return nil, nil
	}
	if p.ProductType != ProductOptionStandard || id < 1 || version < 1 {
		return nil, ErrVariantSelection
	}
	for _, v := range p.Variants {
		if v.ID == id && v.Version == version {
			return &v, nil
		}
	}
	return nil, ErrVariantSelection
}

func (p Product) SalePriceRange() (int64, int64) {
	if !p.VariantsEnabled {
		return p.PriceMinor, p.PriceMinor
	}
	var low, high int64
	for i, v := range p.Variants {
		if i == 0 || v.PriceMinor < low {
			low = v.PriceMinor
		}
		if v.PriceMinor > high {
			high = v.PriceMinor
		}
	}
	return low, high
}
