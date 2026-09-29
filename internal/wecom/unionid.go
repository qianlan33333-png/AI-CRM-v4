package wecom

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
	wecomprovider "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/provider"
)

// ContactUnionIDLinker uses only Identity's stable Port. A missing Provider
// value leaves the existing identity untouched; a different owner is handled
// by OneID's ordinary conflict/merge-candidate path.
type ContactUnionIDLinker struct {
	Scope    string
	Identity identityport.VerifiedIdentityLinker
}

func (linker ContactUnionIDLinker) Ready() bool {
	return linker.Identity != nil && identitydomain.ValidateNamespace(identitydomain.KindUnionID, linker.Scope) == nil
}

func (linker ContactUnionIDLinker) Link(ctx context.Context, customerID customerdomain.CustomerID, expectedExternalID string, contact wecomport.ExternalContact, source string, eventID int64) (string, error) {
	if !linker.Ready() || customerID < 1 || expectedExternalID == "" || eventID < 1 {
		return "", ErrSyncNotReady
	}
	if contact.ExternalUserID != expectedExternalID {
		return "", errors.New("wecom contact identity mismatch")
	}
	if contact.UnionID == "" {
		return "missing", nil
	}
	fact, err := wecomprovider.VerifiedContactUnionID(expectedExternalID, linker.Scope, source, contact)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(expectedExternalID + "\x00" + linker.Scope + "\x00" + contact.UnionID))
	linked, err := linker.Identity.LinkVerifiedIdentityToCustomer(ctx, identityport.VerifiedLinkCommand{
		CustomerID: customerID, Fact: fact,
		Evidence: identitydomain.LinkEvidence{
			Type: "wecom_contact_unionid", Strength: identitydomain.EvidenceStrong, Source: source,
			EventID: strconv.FormatInt(eventID, 10), Digest: "sha256:" + hex.EncodeToString(digest[:]),
			PolicyVersion: "wecom-contact-unionid-v1",
		},
	})
	if err != nil {
		return "", err
	}
	return linked.Status, nil
}
