package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeQueryRequestAcceptsFrontendFilterNames(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/admin/hxc-dashboard/query", strings.NewReader(`{
		"projection_id": 9,
		"filters": {
			"stage": ["active_used"],
			"subscription_tier": ["member"],
			"last_capability": ["peer_chat"],
			"business_stage": ["起步"],
			"user_segment": ["创业者"],
			"identity_state": ["unmatched"]
		},
		"sort": "last_used_at_desc",
		"group_by": "subscription_tier",
		"limit": 50
	}`))

	var got queryRequest
	if err := decode(request, &got); err != nil {
		t.Fatalf("decode frontend query: %v", err)
	}
	if got.ProjectionID != 9 || got.Limit != 50 || got.Sort != "last_used_at_desc" || got.GroupBy != "subscription_tier" {
		t.Fatalf("query metadata not decoded: %#v", got)
	}
	assertSingleValue(t, "stage", got.Filters.Stage, "active_used")
	assertSingleValue(t, "subscription_tier", got.Filters.SubscriptionTier, "member")
	assertSingleValue(t, "last_capability", got.Filters.LastCapability, "peer_chat")
	assertSingleValue(t, "business_stage", got.Filters.BusinessStage, "起步")
	assertSingleValue(t, "user_segment", got.Filters.UserSegment, "创业者")
	assertSingleValue(t, "identity_state", got.Filters.IdentityState, "unmatched")
}

func assertSingleValue(t *testing.T, field string, got []string, want string) {
	t.Helper()
	if len(got) != 1 || got[0] != want {
		t.Fatalf("%s = %#v, want [%q]", field, got, want)
	}
}
