package http

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPublicCouponRoutesPreserveImportedLegacySlug(t *testing.T) {
	for _, slug := range []string{"Legacy_coupon_ABC123xyz789", "123456_legacy", "cp-a1b2c3"} {
		t.Run(slug, func(t *testing.T) {
			h, public, claims := newPublicCouponHandler(t, "published")
			public.coupon.PublicSlug = slug
			for _, path := range []string{"/c/" + slug, "/api/h5/coupons/" + slug} {
				response := httptest.NewRecorder()
				h.ServeHTTP(response, trustedCouponRequest(http.MethodGet, path))
				if response.Code != http.StatusOK {
					t.Fatalf("legacy route=%s status=%d body=%s", path, response.Code, response.Body.String())
				}
			}
			request := trustedCouponRequest(http.MethodPost, "/api/h5/coupons/"+slug+"/claim")
			request.Header.Set("Idempotency-Key", "legacy-slug-claim-0001")
			request.Body = io.NopCloser(strings.NewReader("{}"))
			request.ContentLength = 2
			response := httptest.NewRecorder()
			h.ServeHTTP(response, request)
			if response.Code != http.StatusOK || claims.command.HolderCustomerID != 9 || claims.command.ActorScope != "coupon-public:"+slug {
				t.Fatalf("legacy claim status=%d command=%+v body=%s", response.Code, claims.command, response.Body.String())
			}
		})
	}
	for _, slug := range []string{"short", "coupon/path", "coupon%2Fpath", " coupon_ABC", "coupon_ABC?x=1", "coupon_ABC#x", "coupon_中文"} {
		if validCouponPublicSlug(slug) {
			t.Fatalf("unsafe slug accepted: %q", slug)
		}
	}
}
