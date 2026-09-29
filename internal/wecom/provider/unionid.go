package provider

import (
	"errors"
	"strings"

	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

// VerifiedContactUnionID is only called with a successful authenticated
// WeCom directory/detail response. The caller must supply the explicitly
// verified Open Platform scope for this enterprise's customer API binding.
func VerifiedContactUnionID(expectedExternalID, openPlatformScope, source string, contact wecomport.ExternalContact) (identitydomain.VerifiedFact, error) {
	if expectedExternalID == "" || contact.ExternalUserID != expectedExternalID ||
		strings.TrimSpace(contact.UnionID) == "" || strings.TrimSpace(contact.UnionID) != contact.UnionID ||
		(source != "wecom.directory_sync" && source != "wecom.callback_detail") ||
		identitydomain.ValidateNamespace(identitydomain.KindUnionID, openPlatformScope) != nil {
		return identitydomain.VerifiedFact{}, errors.New("invalid verified wecom unionid observation")
	}
	return identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{
		Kind: identitydomain.KindUnionID, Scope: openPlatformScope, Value: contact.UnionID, Source: source,
	})
}
