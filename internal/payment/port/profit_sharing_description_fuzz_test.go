package port

import (
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

func FuzzProfitSharingDescriptionPreservesProviderContract(f *testing.F) {
	for _, seed := range [][2]string{{"尚子仪Zoe", "裂变商品"}, {"", ""}, {"\x00\xff\n", "商品\t"}, {strings.Repeat("😀", 80), strings.Repeat("课程", 80)}, {" buyer ", " product "}} {
		f.Add(seed[0], seed[1])
	}
	f.Fuzz(func(t *testing.T, buyer, product string) {
		got := FormatProfitSharingPurchaseDescription(buyer, product)
		if !ValidProfitSharingDescription(got) || !strings.Contains(got, "购买") || !strings.HasSuffix(got, "分账") {
			t.Fatalf("unsafe or incomplete provider wording: %q", got)
		}
		if got != FormatProfitSharingPurchaseDescription(buyer, product) {
			t.Fatal("same input changed immutable wording")
		}
		clean := func(v string) bool {
			return v != "" && utf8.ValidString(v) && !strings.ContainsRune(v, utf8.RuneError) && strings.TrimSpace(v) == v && strings.IndexFunc(v, unicode.IsControl) == -1
		}
		exact := buyer + "购买" + product + "分账"
		if clean(buyer) && clean(product) && len(exact) <= ProfitSharingDescriptionMaxBytes && got != exact {
			t.Fatalf("valid fitting names lost: got=%q want=%q", got, exact)
		}
	})
}
