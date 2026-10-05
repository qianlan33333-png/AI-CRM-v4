package wecom

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/idempotency"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/webhook"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

type nameUOW struct{ inside bool }

func (u *nameUOW) Within(ctx context.Context, fn func(context.Context) error) error {
	u.inside = true
	defer func() { u.inside = false }()
	return fn(ctx)
}

type nameInbox struct{ event CallbackEvent }

func (i nameInbox) Lookup(_ context.Context, provider string, key idempotency.Key) (webhook.Delivery, error) {
	payload, _ := json.Marshal(i.event)
	return webhook.Delivery{Provider: provider, IdempotencyKey: key, Payload: payload}, nil
}

type nameIdentity struct {
	result identityport.ResolveResult
	ref    identitydomain.Reference
}

func (i *nameIdentity) Resolve(_ context.Context, ref identitydomain.Reference) (identityport.ResolveResult, error) {
	i.ref = ref
	return i.result, nil
}

type nameDirectory string

func (n nameDirectory) DisplayNames(_ context.Context, ids []customerdomain.CustomerID) (map[customerdomain.CustomerID]string, error) {
	return map[customerdomain.CustomerID]string{ids[0]: string(n)}, nil
}

type nameProvider struct {
	t       *testing.T
	uow     *nameUOW
	calls   int
	contact wecomport.ExternalContact
	err     error
}

func (p *nameProvider) ReadExternalContact(_ context.Context, value string) (wecomport.ExternalContact, error) {
	if p.uow.inside {
		p.t.Fatal("Provider read held a local transaction")
	}
	if value != "external-fixture" {
		p.t.Fatal("wrong callback contact")
	}
	p.calls++
	return p.contact, p.err
}

func TestWelcomeCustomerNameReadsBeforeDirectoryPublication(t *testing.T) {
	for _, tt := range []struct {
		name, local, providerID, providerName string
		result                                identityport.ResolveStatus
		outage, wantError                     bool
		want                                  string
		calls                                 int
	}{
		{name: "new customer", providerID: "external-fixture", providerName: "企微昵称", result: identityport.ResolveNotFound, want: "企微昵称", calls: 1},
		{name: "known but unpublished", providerID: "external-fixture", providerName: "新昵称", result: identityport.ResolveFound, want: "新昵称", calls: 1},
		{name: "database first", local: "数据库名称", result: identityport.ResolveFound, want: "数据库名称"},
		{name: "read outage fallback", result: identityport.ResolveNotFound, outage: true, calls: 1},
		{name: "empty nickname", providerID: "external-fixture", result: identityport.ResolveNotFound, calls: 1},
		{name: "wrong provider target", providerID: "another-contact", providerName: "错误姓名", result: identityport.ResolveNotFound, wantError: true, calls: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			u := &nameUOW{}
			identity := &nameIdentity{result: identityport.ResolveResult{Status: tt.result, CustomerID: 42, IdentityID: 7}}
			p := &nameProvider{t: t, uow: u, contact: wecomport.ExternalContact{ExternalUserID: tt.providerID, Name: tt.providerName}}
			if tt.outage {
				p.err = errors.New("unavailable")
			}
			s := WelcomeCustomerNameService{CorpID: "corp", Inbox: nameInbox{CallbackEvent{CorpID: "corp", Event: "change_external_contact", ChangeType: "add_external_contact", ExternalUserID: "external-fixture", WelcomeGrantRef: "wgrant_1"}}, Identity: identity, Names: nameDirectory(tt.local), Provider: p, UOW: u}
			name, err := s.CallbackCustomerName(context.Background(), "callback-fixture", "wgrant_1")
			if name != tt.want || (err != nil) != tt.wantError || p.calls != tt.calls {
				t.Fatalf("name=%q error=%v calls=%d", name, err, p.calls)
			}
			if identity.ref.Scope != "wecom-corp:corp" || identity.ref.Kind != identitydomain.KindWeComExternalUserID || identity.ref.Value != "external-fixture" {
				t.Fatal("callback identity scope changed")
			}
		})
	}
}

func TestWelcomeCustomerNameRejectsDifferentCallbackGrant(t *testing.T) {
	u := &nameUOW{}
	p := &nameProvider{t: t, uow: u}
	s := WelcomeCustomerNameService{CorpID: "corp", Inbox: nameInbox{CallbackEvent{CorpID: "corp", Event: "change_external_contact", ChangeType: "add_external_contact", ExternalUserID: "external-fixture", WelcomeGrantRef: "wgrant_original"}}, Identity: &nameIdentity{}, Names: nameDirectory(""), Provider: p, UOW: u}
	if _, err := s.CallbackCustomerName(context.Background(), "callback-fixture", "wgrant_other"); err == nil || p.calls != 0 {
		t.Fatal("unbound grant reached Provider")
	}
}
