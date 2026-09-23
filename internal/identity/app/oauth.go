package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
)

// ProvisionVerifiedOAuthSubject preserves the existing OpenID payer root.
// A UnionID owned elsewhere creates the ordinary audited merge candidate;
// it never merges roots or issues a usable cross-root result.
func (s OneIDService) ProvisionVerifiedOAuthSubject(ctx context.Context, c identityport.OAuthSubjectCommand) (identityport.OAuthSubjectResult, error) {
	if s.Store == nil || !c.OpenID.Valid() || !c.UnionID.Valid() || c.OpenID.Reference().Kind != identitydomain.KindOAOpenID || c.UnionID.Reference().Kind != identitydomain.KindUnionID || len(c.EventID) < 16 || len(c.EventID) > 200 {
		return identityport.OAuthSubjectResult{}, ErrInvalidLinkCommand
	}
	_, found, err := s.Store.Resolve(ctx, c.OpenID.Reference())
	if err != nil {
		return identityport.OAuthSubjectResult{}, err
	}
	first, target := c.OpenID, c.UnionID
	if !found {
		first, target = c.UnionID, c.OpenID
	}
	p, err := s.Store.Provision(ctx, first)
	if err != nil {
		return identityport.OAuthSubjectResult{}, err
	}
	if err = s.observeProvisionedCustomer(ctx, p, first.Reference().Source); err != nil {
		return identityport.OAuthSubjectResult{}, err
	}
	digest := sha256.Sum256([]byte(c.EventID))
	linked, err := s.Store.Link(ctx, LinkCommand{SourceCustomerID: p.CustomerID, Target: target, Evidence: identitydomain.LinkEvidence{Type: "provider_oauth_userinfo", Strength: identitydomain.EvidenceStrong, Source: "wechat.oauth.userinfo", EventID: c.EventID, Digest: "sha256:" + hex.EncodeToString(digest[:]), PolicyVersion: "oauth-userinfo-v1"}})
	if err != nil {
		return identityport.OAuthSubjectResult{}, err
	}
	if target.Reference().Kind == identitydomain.KindOAOpenID {
		p.IdentityID = linked.IdentityID
	}
	return identityport.OAuthSubjectResult{ProvisionResult: identityport.ProvisionResult{CustomerID: p.CustomerID, IdentityID: p.IdentityID, Created: p.Created}, Conflict: (linked.Status != LinkAttached && linked.Status != LinkAlreadyLinked) || linked.CustomerID != p.CustomerID}, nil
}
