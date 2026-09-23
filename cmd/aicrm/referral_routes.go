package main

import (
	"net/http"
	"strings"
)

// referralUnavailableHandler exposes no activity, participant, or invitation
// data when Referral is not fully composed. It intentionally has the same
// fail-closed posture as the existing Distribution public boundary.
type referralUnavailableHandler struct{}

func (referralUnavailableHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte(`{"error":"referral_unavailable"}`))
}

// mountReferral reserves Referral's public invitation/API and employee-admin
// API prefixes. The public `/referral` document remains mounted by webshell;
// it contains no authority and all its API reads and writes pass through the
// Referral handler below.
func mountReferral(next, public, admin http.Handler) http.Handler {
	if public == nil {
		public = referralUnavailableHandler{}
	}
	if admin == nil {
		admin = referralUnavailableHandler{}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/referral/invite" || strings.HasPrefix(r.URL.Path, "/referral/invite/") || r.URL.Path == "/api/v1/referral" || strings.HasPrefix(r.URL.Path, "/api/v1/referral/"):
			public.ServeHTTP(w, r)
		case r.URL.Path == "/api/admin/referral" || strings.HasPrefix(r.URL.Path, "/api/admin/referral/"):
			admin.ServeHTTP(w, r)
		default:
			next.ServeHTTP(w, r)
		}
	})
}

// mountSecuredReferral applies the host's browser boundary only to Referral's
// own public and admin endpoints. Referral is mounted after the main router's
// security layer so wrapping the individual handlers prevents the new routes
// from becoming an exception, while preserving every pre-existing route's
// behavior.
func mountSecuredReferral(next, public, admin http.Handler, publicOrigin string, h5Origins ...string) http.Handler {
	secure := func(handler http.Handler) http.Handler {
		if handler == nil {
			handler = referralUnavailableHandler{}
		}
		return securityHeaders(rejectCrossSiteUnsafeRequests(handler, canonicalOrigin(publicOrigin), h5Origins...))
	}
	return mountReferral(next, secure(public), secure(admin))
}
