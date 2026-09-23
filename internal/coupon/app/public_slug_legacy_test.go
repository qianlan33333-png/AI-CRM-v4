package app

import "testing"

func TestPublicSlugLegacyURLSafeGrammar(t *testing.T) {
	for _, v := range []string{"cp-abcdef123456", "Legacy_coupon_ABC123xyz789", "123456_legacy"} {
		if !validPublicSlug(v) {
			t.Fatal("legacy public URL rejected")
		}
	}
	for _, v := range []string{"short", "coupon/path", "coupon%2Fpath", " coupon_ABC", "coupon_ABC?x=1", "coupon_ABC#x", "coupon_中文"} {
		if validPublicSlug(v) {
			t.Fatal("unsafe public URL accepted")
		}
	}
}
