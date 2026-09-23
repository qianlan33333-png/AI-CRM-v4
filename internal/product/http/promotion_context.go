package http

import (
	"encoding/base64"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const promotionContextQueryKey = "promotion_context"

// publicPromotionContext accepts only the opaque Distribution credential that
// /d places into the exact public product URL. Product cannot inspect its
// attribution semantics: Payment and Order validate the token and the frozen
// product facts in their checkout Unit of Work.
func publicPromotionContext(r *http.Request) (string, bool) {
	if r.URL.RawQuery == "" {
		return "", true
	}
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(values) != 1 || len(values[promotionContextQueryKey]) != 1 {
		return "", false
	}
	value := values.Get(promotionContextQueryKey)
	if value == "" || r.URL.RawQuery != promotionContextQueryKey+"="+value || len(value) != 47 || !strings.HasPrefix(value, "dpc_") {
		return "", false
	}
	raw, decodeErr := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, "dpc_"))
	if decodeErr != nil || len(raw) != 32 {
		return "", false
	}
	return value, true
}

func publicPaymentPath(path, promotionContext string) string {
	if promotionContext == "" {
		return path
	}
	return path + "?" + promotionContextQueryKey + "=" + promotionContext
}

// Old promotion-cookie flows must never decide a new checkout. The current
// flow carries its opaque credential only in this controlled page chain.
func clearLegacyPromotionCookies(w http.ResponseWriter) {
	for _, name := range []string{"aicrm_promotion_context", "aicrm_promotion_handoff", "aicrm_promotion_accepted"} {
		http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, MaxAge: -1, Expires: time.Unix(1, 0)})
	}
}
