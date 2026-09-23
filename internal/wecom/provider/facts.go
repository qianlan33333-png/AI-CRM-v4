// Package provider contains trusted WeCom provider adapters. It is the only
// WeCom package allowed to promote authenticated provider data into a OneID
// VerifiedFact.
package provider

import (
	"errors"
	"strings"

	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
)

// VerifiedExternalContact is called only after a callback envelope has passed
// signature, time-window, decryption and CorpID checks and has been durably
// stored in the platform inbox.
func VerifiedExternalContact(corpID, externalUserID, source string) (identitydomain.VerifiedFact, error) {
	if strings.TrimSpace(corpID) != corpID || corpID == "" || strings.TrimSpace(externalUserID) != externalUserID || externalUserID == "" ||
		(source != "wecom.callback" && source != "wecom.directory_sync" && source != "wecom.message_archive") {
		return identitydomain.VerifiedFact{}, errors.New("invalid verified wecom external contact")
	}
	return identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{
		Kind: identitydomain.KindWeComExternalUserID, Scope: "wecom-corp:" + corpID, Value: externalUserID, Source: source,
	})
}
