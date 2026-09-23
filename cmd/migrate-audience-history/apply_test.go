package main

import (
	"context"
	"encoding/json"
	"testing"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
)

type testResolver struct {
	calls int
	refs  []identitydomain.Reference
}

func (r *testResolver) Resolve(_ context.Context, ref identitydomain.Reference) (identityport.ResolveResult, error) {
	r.calls++
	r.refs = append(r.refs, ref)
	id := int64(10)
	if ref.Value == "conflicting" {
		id = 20
	}
	if ref.Value == "missing" {
		return identityport.ResolveResult{Status: identityport.ResolveNotFound}, nil
	}
	return identityport.ResolveResult{Status: identityport.ResolveFound, CustomerID: customerdomain.CustomerID(id)}, nil
}
func TestResolveHistoricalAudienceNeverGuessesScopeOrRoot(t *testing.T) {
	tests := []struct {
		name, raw string
		scopes    map[string]string
		want      string
		calls     int
	}{
		{"missing scope", `{"unionid":"known"}`, nil, "missing_scope", 0},
		{"scoped exact", `{"unionid":"known"}`, map[string]string{"unionid": "wechat-open-platform:fixture"}, "resolved", 1},
		{"unknown", `{"unionid":"missing"}`, map[string]string{"unionid": "wechat-open-platform:fixture"}, "unresolved", 1},
		{"conflicting roots", `{"unionid":"known","identity_type":"external_userid","identity_value":"conflicting"}`, map[string]string{"unionid": "wechat-open-platform:fixture", "wecom_external_userid": "wecom-corp:fixture"}, "conflict", 2},
		{"unsupported identity", `{"identity_type":"mobile_hash","identity_value":"secret"}`, nil, "invalid", 0},
		{"unscoped secondary", `{"unionid":"known","identity_type":"external_userid","identity_value":"known"}`, map[string]string{"unionid": "wechat-open-platform:fixture"}, "missing_scope", 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var m map[string]json.RawMessage
			json.Unmarshal([]byte(tc.raw), &m)
			resolver := &testResolver{}
			cache := map[identitydomain.Reference]identityport.ResolveResult{}
			id, reason, e := resolveMember(context.Background(), m, tc.scopes, resolver, cache)
			if e != nil || reason != tc.want || resolver.calls != tc.calls {
				t.Fatalf("%d %s %v calls=%d", id, reason, e, resolver.calls)
			}
			for _, ref := range resolver.refs {
				if ref.Assurance != identitydomain.AssuranceDeclared {
					t.Fatal("historical data promoted verified")
				}
			}
			_, _, e = resolveMember(context.Background(), m, tc.scopes, resolver, cache)
			if e != nil || resolver.calls != tc.calls {
				t.Fatal("exact scoped cache")
			}
		})
	}
}
