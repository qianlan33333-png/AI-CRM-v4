package wecom

import (
	"context"
	"encoding/hex"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
)

type contactIdentityOutcome struct {
	Provision identityport.ProvisionResult
	Conflict  bool
}

// resolveOrBindWeComContact keeps the verified WeCom identity pair on one
// OneID root whenever either identity is new. Existing cross-root identities
// produce OneID's audited merge candidate and return Conflict without moving
// either identity or authorizing downstream entrant work.
func resolveOrBindWeComContact(ctx context.Context, resolver identityport.Resolver, provisioner identityport.VerifiedProvisioner,
	linker identityport.VerifiedIdentityLinker, extFact identitydomain.VerifiedFact, unionFact *identitydomain.VerifiedFact,
	idempotencyKey string, evidence identitydomain.LinkEvidence,
) (contactIdentityOutcome, error) {
	if provisioner == nil || !extFact.Valid() || extFact.Reference().Kind != identitydomain.KindWeComExternalUserID || idempotencyKey == "" {
		return contactIdentityOutcome{Conflict: true}, nil
	}
	if unionFact == nil {
		if resolver == nil {
			// Keep legacy ext-only directory/callback callers operational when
			// they do not provide the optional resolver port. The original
			// ProvisionVerifiedIdentity call is idempotent and does not merge roots.
			provisioned, err := provisioner.ProvisionVerifiedIdentity(ctx, identityport.ProvisionCommand{Fact: extFact, IdempotencyKey: idempotencyKey})
			return contactIdentityOutcome{Provision: provisioned}, err
		}
		resolved, err := resolver.Resolve(ctx, factReference(extFact))
		if err != nil {
			return contactIdentityOutcome{}, err
		}
		switch resolved.Status {
		case identityport.ResolveFound:
			return contactIdentityOutcome{Provision: identityport.ProvisionResult{CustomerID: resolved.CustomerID, IdentityID: resolved.IdentityID}}, nil
		case identityport.ResolveNotFound:
			provisioned, provisionErr := provisioner.ProvisionVerifiedIdentity(ctx, identityport.ProvisionCommand{Fact: extFact, IdempotencyKey: idempotencyKey})
			return contactIdentityOutcome{Provision: provisioned}, provisionErr
		default:
			return contactIdentityOutcome{Conflict: true}, nil
		}
	}
	if resolver == nil || linker == nil || !unionFact.Valid() || unionFact.Reference().Kind != identitydomain.KindUnionID || !evidence.Valid() || evidence.Strength != identitydomain.EvidenceStrong {
		return contactIdentityOutcome{Conflict: true}, nil
	}

	extResolved, err := resolver.Resolve(ctx, factReference(extFact))
	if err != nil {
		return contactIdentityOutcome{}, err
	}
	unionResolved, err := resolver.Resolve(ctx, factReference(*unionFact))
	if err != nil {
		return contactIdentityOutcome{}, err
	}
	if extResolved.Status == identityport.ResolveConflict || unionResolved.Status == identityport.ResolveConflict {
		return contactIdentityOutcome{Conflict: true}, nil
	}
	if (extResolved.Status != identityport.ResolveFound && extResolved.Status != identityport.ResolveNotFound) ||
		(unionResolved.Status != identityport.ResolveFound && unionResolved.Status != identityport.ResolveNotFound) {
		return contactIdentityOutcome{Conflict: true}, nil
	}
	if extResolved.Status == identityport.ResolveFound && unionResolved.Status == identityport.ResolveFound && extResolved.CustomerID == unionResolved.CustomerID {
		return contactIdentityOutcome{Provision: identityport.ProvisionResult{CustomerID: extResolved.CustomerID, IdentityID: extResolved.IdentityID}}, nil
	}

	var customerID customerdomain.CustomerID
	var identityID int64
	created := false
	sourceIsExternalRoot := false
	switch {
	case extResolved.Status == identityport.ResolveFound:
		customerID, identityID = extResolved.CustomerID, extResolved.IdentityID
		sourceIsExternalRoot = true
	case unionResolved.Status == identityport.ResolveFound:
		customerID = unionResolved.CustomerID
	case extResolved.Status == identityport.ResolveNotFound:
		provisioned, provisionErr := provisioner.ProvisionVerifiedIdentity(ctx, identityport.ProvisionCommand{Fact: extFact, IdempotencyKey: idempotencyKey})
		if provisionErr != nil {
			return contactIdentityOutcome{}, provisionErr
		}
		if provisioned.CustomerID < 1 || provisioned.IdentityID < 1 {
			return contactIdentityOutcome{Conflict: true}, nil
		}
		customerID, identityID, created = provisioned.CustomerID, provisioned.IdentityID, provisioned.Created
		sourceIsExternalRoot = true
	default:
		return contactIdentityOutcome{Conflict: true}, nil
	}

	target := extFact
	if sourceIsExternalRoot {
		// The external-contact root owns the existing WeCom history. Attach a
		// missing UnionID to it, or ask OneID for a candidate if UnionID belongs
		// to another root.
		target = *unionFact
	}
	linked, err := linker.LinkVerifiedIdentityToCustomer(ctx, identityport.VerifiedLinkCommand{
		CustomerID: customerID,
		Fact:       target,
		Evidence:   evidence,
	})
	if err != nil {
		return contactIdentityOutcome{}, err
	}
	switch linked.Status {
	case "attached", "already_linked":
		if linked.CustomerID < 1 || linked.IdentityID < 1 {
			return contactIdentityOutcome{Conflict: true}, nil
		}
		// The WeCom profile projection is keyed by the external-contact identity,
		// even when this operation just attached its UnionID. Preserve that
		// identity id for the profile store while using OneID's canonical root.
		if sourceIsExternalRoot && identityID > 0 {
			return contactIdentityOutcome{Provision: identityport.ProvisionResult{CustomerID: linked.CustomerID, IdentityID: identityID, Created: created}}, nil
		}
		return contactIdentityOutcome{Provision: identityport.ProvisionResult{CustomerID: linked.CustomerID, IdentityID: linked.IdentityID, Created: created}}, nil
	case "merge_candidate", "conflict":
		// If the external identity already belongs to a WeCom root, retain that
		// root for its existing profile projection while reporting the OneID
		// candidate as unresolved. Never return the payer root as a linked result.
		if extResolved.Status == identityport.ResolveFound {
			return contactIdentityOutcome{Provision: identityport.ProvisionResult{CustomerID: extResolved.CustomerID, IdentityID: extResolved.IdentityID}, Conflict: true}, nil
		}
		if customerID > 0 && identityID > 0 {
			return contactIdentityOutcome{Provision: identityport.ProvisionResult{CustomerID: customerID, IdentityID: identityID, Created: created}, Conflict: true}, nil
		}
		return contactIdentityOutcome{Conflict: true}, nil
	default:
		return contactIdentityOutcome{Conflict: true}, nil
	}
}

func factReference(fact identitydomain.VerifiedFact) identitydomain.Reference {
	ref := fact.Reference()
	return identitydomain.Reference{Kind: ref.Kind, Scope: ref.Scope, Value: ref.NormalizedValue, Assurance: ref.Assurance, Source: ref.Source}
}

func contactLinkEvidence(source, eventID string, digest [32]byte) identitydomain.LinkEvidence {
	return identitydomain.LinkEvidence{
		Type: "verified_wecom_unionid_contact_pair", Strength: identitydomain.EvidenceStrong,
		Source: source, EventID: eventID, Digest: "sha256:" + hex.EncodeToString(digest[:]),
		PolicyVersion: "wecom-unionid-link-v1",
	}
}
