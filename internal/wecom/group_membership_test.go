package wecom

import (
	"context"
	"errors"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
	"testing"
	"time"
)

type groupTestUOW struct{ in bool }

func (u *groupTestUOW) Within(c context.Context, f func(context.Context) error) error {
	u.in = true
	defer func() { u.in = false }()
	return f(c)
}

type groupTestStore struct {
	codes []string
	fact  wecomport.AudienceGroupMembership
}

func (s *groupTestStore) SaveGroupMembership(_ context.Context, _, _ string, f wecomport.AudienceGroupMembership, code string) error {
	s.codes = append(s.codes, code)
	s.fact = f
	return nil
}

type groupTestProvider struct {
	store *groupTestStore
	uow   *groupTestUOW
	fail  bool
	ids   []string
	t     *testing.T
}

func (p groupTestProvider) ReadGroupMembership(context.Context, string) (wecomport.GroupMembership, error) {
	if p.uow.in || len(p.store.codes) != 1 || p.store.codes[0] != "refreshing" {
		p.t.Fatal("network before invalidation or inside UoW")
	}
	if p.fail {
		return wecomport.GroupMembership{}, errors.New("provider error")
	}
	return wecomport.GroupMembership{ChatID: "g", ExternalUserIDs: p.ids}, nil
}

type groupTestResolver struct{ fail bool }

func (r groupTestResolver) Resolve(_ context.Context, ref identitydomain.Reference) (identityport.ResolveResult, error) {
	if ref.Kind != identitydomain.KindWeComExternalUserID || ref.Scope != "wecom-corp:corp" {
		return identityport.ResolveResult{}, errors.New("wrong scope")
	}
	if r.fail {
		return identityport.ResolveResult{}, errors.New("identity unavailable")
	}
	if ref.Value == "missing" {
		return identityport.ResolveResult{Status: identityport.ResolveNotFound}, nil
	}
	return identityport.ResolveResult{Status: identityport.ResolveFound, CustomerID: 7}, nil
}
func TestGroupRefreshFailsClosedWithoutProvision(t *testing.T) {
	for _, tc := range []struct {
		name                           string
		ids                            []string
		providerFail, resolverFail, ok bool
		code                           string
	}{
		{"complete", []string{"x", "y"}, false, false, true, ""},
		{"empty", []string{}, false, false, true, ""},
		{"partial", []string{"x", "missing"}, false, false, true, "identity_unresolved"},
		{"provider", nil, true, false, false, "provider_read_failed"},
		{"identity", []string{"x"}, false, true, false, "identity_read_failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u := &groupTestUOW{}
			s := &groupTestStore{}
			a := &staffRefreshAudit{}
			service := GroupMembershipRefresh{Provider: groupTestProvider{s, u, tc.providerFail, tc.ids, t}, Resolver: groupTestResolver{tc.resolverFail}, Store: s, Audit: a, UOW: u, CorpScope: "wecom-corp:corp", Now: func() time.Time { return time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC) }}
			f, e := service.Refresh(context.Background(), "g")
			if (e == nil) != tc.ok || f.ProviderComplete != tc.ok || f.Complete != (tc.code == "") || len(s.codes) != 2 || s.codes[1] != tc.code || a.count != 2 {
				t.Fatalf("success=%v complete=%v codes=%v audit=%d", e == nil, f.Complete, s.codes, a.count)
			}
			if tc.name == "complete" && len(f.CustomerIDs) != 1 {
				t.Fatal("canonical duplicate not deduped")
			}
		})
	}
}
