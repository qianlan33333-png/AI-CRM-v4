package port

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestProfitSharingPurchaseDescriptionPreservesBuyerAndProduct(t *testing.T) {
	for _, row := range []struct{ name, buyer, product, want string }{
		{"requested wording", "崔晓玲", "裂变商品", "崔晓玲购买裂变商品分账"},
		{"emoji", "Zoe😊", "训练营", "Zoe😊购买训练营分账"},
		{"missing nickname", "", "裂变商品", "微信用户购买裂变商品分账"},
		{"controls", "\n Zoe\t", "\r训练营\n", "Zoe购买训练营分账"},
	} {
		t.Run(row.name, func(t *testing.T) {
			got := FormatProfitSharingPurchaseDescription(row.buyer, row.product)
			if got != row.want || !ValidProfitSharingDescription(got) {
				t.Fatalf("description=%q", got)
			}
		})
	}
	for _, row := range [][2]string{{strings.Repeat("很长昵称😊", 30), "裂变商品"}, {"买家", strings.Repeat("超长商品名称", 30)}, {strings.Repeat("昵称😊", 30), strings.Repeat("商品名称", 30)}} {
		got := FormatProfitSharingPurchaseDescription(row[0], row[1])
		if !utf8.ValidString(got) || !ValidProfitSharingDescription(got) || !strings.Contains(got, "购买") || !strings.HasSuffix(got, "分账") || !strings.Contains(got, "…") {
			t.Fatalf("long description failed provider constraints: bytes=%d", len(got))
		}
	}
	for _, value := range []string{"", " note", "note\n", strings.Repeat("x", 81), string([]byte{0xff})} {
		if ValidProfitSharingDescription(value) {
			t.Fatal("invalid provider text accepted")
		}
	}
}
