package wecom

import (
	"context"
	"encoding/json"
	"strings"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/idempotency"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/webhook"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

type welcomeCallbackLookup interface {
	Lookup(context.Context, string, idempotency.Key) (webhook.Delivery, error)
}

// WelcomeCustomerNameService closes the fast-welcome/directory-publication
// race. Provider reads run after the local UOW, using only the authenticated
// callback's contact; no customer or directory record is written here.
type WelcomeCustomerNameService struct {
	CorpID   string
	Inbox    welcomeCallbackLookup
	Identity identityport.Resolver
	Names    customerport.DirectoryDisplayNameReader
	Provider wecomport.ExternalContactReader
	UOW      platformport.UnitOfWork
}

func (s WelcomeCustomerNameService) CallbackCustomerName(ctx context.Context, callbackID, grantRef string) (string, error) {
	if s.CorpID == "" || s.Inbox == nil || s.Identity == nil || s.Names == nil || s.Provider == nil || s.UOW == nil {
		return "", ErrWelcomeGrantUnavailable
	}
	var event CallbackEvent
	var name string
	err := s.UOW.Within(ctx, func(tx context.Context) error {
		delivery, err := s.Inbox.Lookup(tx, callbackProvider, idempotency.Key(callbackID))
		if err != nil {
			return err
		}
		if delivery.Provider != callbackProvider || string(delivery.IdempotencyKey) != callbackID || json.Unmarshal(delivery.Payload, &event) != nil || event.CorpID != s.CorpID || !event.IsEntrant() || event.ExternalUserID == "" || event.WelcomeGrantRef != grantRef || grantRef == "" {
			return ErrWelcomeGrantUnavailable
		}
		resolved, err := s.Identity.Resolve(tx, identitydomain.Reference{Kind: identitydomain.KindWeComExternalUserID, Scope: "wecom-corp:" + s.CorpID, Value: event.ExternalUserID, Assurance: identitydomain.AssuranceVerified, Source: "wecom.callback_welcome_name"})
		if err != nil {
			return err
		}
		if resolved.Status == identityport.ResolveFound && resolved.CustomerID > 0 {
			names, err := s.Names.DisplayNames(tx, []customerdomain.CustomerID{resolved.CustomerID})
			if err != nil {
				return err
			}
			name = names[resolved.CustomerID]
		}
		return nil
	})
	if err != nil || strings.TrimSpace(name) != "" {
		return name, err
	}
	contact, err := s.Provider.ReadExternalContact(ctx, event.ExternalUserID)
	if err != nil {
		// A read outage must retain the established missing-name fallback.
		return "", nil
	}
	if contact.ExternalUserID != event.ExternalUserID {
		return "", ErrWelcomeGrantUnavailable
	}
	return contact.Name, nil
}

var _ wecomport.CallbackCustomerNameReader = WelcomeCustomerNameService{}
