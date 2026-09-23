package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"slices"
	"strconv"
	"strings"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

// customerOwnerHandoffCandidates is a Composition-only adapter. It composes
// three owning ports: Access maps local staff IDs, WeCom verifies the exact
// current follow relation, and Identity reads an already canonical external
// identity. It creates or links no identity and writes no WeCom fact.
type customerOwnerHandoffCandidates struct {
	staff interface {
		UserByID(context.Context, int64, bool) (accessdomain.User, error)
		UserByWeComUserID(context.Context, string, bool) (accessdomain.User, error)
	}
	relationships      wecomport.OwnerHandoffRelationshipReader
	relationshipLister wecomport.OwnerHandoffRelationshipLister
	primaries          wecomport.AudiencePrimaryOwnerReader
	primaryLister      wecomport.OwnerHandoffPrimaryOwnerLister
	identities         identityport.ExternalIdentityValueReader
	owners             interface {
		LocalOwner(context.Context, customerdomain.CustomerID, bool) (customerport.LocalOwner, bool, error)
		ListOwnerHandoffCustomerIDs(context.Context, int64, int) ([]customerdomain.CustomerID, error)
	}
}

func (a customerOwnerHandoffCandidates) ResolveOwnerHandoffCandidates(ctx context.Context, mode customerport.OwnerHandoffMode, sourceStaffID, targetStaffID int64, corpScope string, ids []customerdomain.CustomerID) ([]customerport.OwnerHandoffCandidate, error) {
	if a.staff == nil || a.owners == nil || len(ids) == 0 || (mode != customerport.OwnerHandoffLocalOnly && mode != customerport.OwnerHandoffWeComThenCRM) {
		return nil, errors.New("owner handoff candidate adapter unavailable")
	}
	source, err := a.staff.UserByID(ctx, sourceStaffID, false)
	if err != nil || source.ID != sourceStaffID {
		return nil, errors.New("owner handoff source unavailable")
	}
	target, err := a.staff.UserByID(ctx, targetStaffID, false)
	if err != nil || target.ID != targetStaffID || !target.Active {
		return nil, errors.New("owner handoff target unavailable")
	}
	primaryByCustomer := map[customerdomain.CustomerID]wecomport.AudiencePrimaryOwner{}
	if mode == customerport.OwnerHandoffLocalOnly {
		if a.primaries == nil {
			return nil, errors.New("owner handoff primary-owner reader unavailable")
		}
		primaries, primaryErr := a.primaries.AudiencePrimaryOwners(ctx, ids)
		if primaryErr != nil {
			return nil, primaryErr
		}
		for _, primary := range primaries {
			primaryByCustomer[primary.CustomerID] = primary
		}
	}
	out := make([]customerport.OwnerHandoffCandidate, 0, len(ids))
	for _, customerID := range ids {
		owner, found, ownerErr := a.owners.LocalOwner(ctx, customerID, false)
		if ownerErr != nil {
			return nil, ownerErr
		}
		candidate := customerport.OwnerHandoffCandidate{CustomerID: customerID, State: "ready"}
		if found {
			candidate.ExpectedLocalOwnerID, candidate.ExpectedLocalVersion = owner.StaffID, owner.Version
			if owner.StaffID != sourceStaffID {
				candidate.State, candidate.Reason = "conflict", "local_owner_not_source"
				out = append(out, candidate)
				continue
			}
		} else if mode == customerport.OwnerHandoffLocalOnly {
			primary, primaryFound := primaryByCustomer[customerID]
			if !primaryFound || primary.Status != "known" || primary.CorpScope != corpScope || primary.OwnerUserID == "" || primary.OwnerUserID != source.WeComUserID {
				candidate.State, candidate.Reason = "unresolved", "trusted_primary_owner_missing"
				out = append(out, candidate)
				continue
			}
			// The trusted primary owner is a read-only fallback for customers
			// that predate explicit CRM assignment. Resolve it through Access;
			// never turn a user-supplied staff ID into a relationship.
			mapped, mappedErr := a.staff.UserByWeComUserID(ctx, primary.OwnerUserID, false)
			if mappedErr != nil || mapped.ID != sourceStaffID {
				candidate.State, candidate.Reason = "unresolved", "trusted_primary_owner_access_missing"
				out = append(out, candidate)
				continue
			}
			candidate.RelationshipDigest = sha256.Sum256([]byte(strings.Join([]string{"owner-handoff-primary-v1", primary.CorpScope, primary.OwnerUserID, primary.Status, string(primary.VersionDigest[:])}, "\x00")))
			out = append(out, candidate)
			continue
		}
		if mode == customerport.OwnerHandoffLocalOnly {
			candidate.RelationshipDigest = sha256.Sum256([]byte(strings.Join([]string{"owner-handoff-local-v1", strconv.FormatInt(int64(customerID), 10), strconv.FormatInt(candidate.ExpectedLocalOwnerID, 10), strconv.FormatInt(candidate.ExpectedLocalVersion, 10)}, "\x00")))
			out = append(out, candidate)
			continue
		}
		if source.WeComUserID == "" || target.WeComUserID == "" {
			candidate.State, candidate.Reason = "unresolved", "staff_wecom_identity_missing"
			out = append(out, candidate)
			continue
		}
		relation, relationErr := a.relationships.OwnerHandoffRelationship(ctx, customerID, corpScope, source.WeComUserID)
		if relationErr != nil {
			return nil, relationErr
		}
		if !relation.Active {
			candidate.State, candidate.Reason = "unresolved", "source_follow_relationship_missing"
			out = append(out, candidate)
			continue
		}
		externalID, identityFound, identityErr := a.identities.VerifiedExternalIdentityValue(ctx, customerID, identitydomain.KindWeComExternalUserID, corpScope)
		if identityErr != nil {
			return nil, identityErr
		}
		if !identityFound || externalID == "" {
			candidate.State, candidate.Reason = "unresolved", "canonical_wecom_identity_missing"
			out = append(out, candidate)
			continue
		}
		candidate.RelationshipDigest, candidate.SourceUserID, candidate.TargetUserID, candidate.ExternalUserID = relation.VersionDigest, source.WeComUserID, target.WeComUserID, externalID
		out = append(out, candidate)
	}
	return out, nil
}

var _ customerport.OwnerHandoffCandidateResolver = customerOwnerHandoffCandidates{}

// customerOwnerHandoffStaffDirectory is the Customer-facing projection of the
// existing Access repository. It reads inside the caller UoW and deliberately
// drops credentials, roles and provider IDs before the Host receives it.
type customerOwnerHandoffStaffDirectory struct {
	profiles staffDisplayProfileReader
	uow      platformport.UnitOfWork
	staff    interface {
		ListUsers(context.Context) ([]accessdomain.User, error)
	}
}

func (a customerOwnerHandoffStaffDirectory) ListOwnerHandoffStaff(ctx context.Context) ([]customerport.OwnerHandoffStaff, error) {
	if a.uow == nil || a.staff == nil {
		return nil, errors.New("owner handoff staff directory unavailable")
	}
	out := make([]customerport.OwnerHandoffStaff, 0)
	err := a.uow.Within(ctx, func(tx context.Context) error {
		users, err := a.staff.ListUsers(tx)
		if err != nil {
			return err
		}
		out = make([]customerport.OwnerHandoffStaff, 0, len(users))
		for _, user := range staffWithDisplayProfiles(tx, users, a.profiles) {
			out = append(out, customerport.OwnerHandoffStaff{ID: user.ID, UserID: user.WeComUserID, DisplayName: user.DisplayName, Active: user.Active})
		}
		return nil
	})
	return out, err
}

var _ customerport.OwnerHandoffStaffDirectory = customerOwnerHandoffStaffDirectory{}

// DiscoverOwnerHandoffCustomerIDs implements the server-selected all range.
// A WeCom run is restricted to active source relations; local-only includes
// Customer-owned assignments. Both are capped before Preview persists a row.
func (a customerOwnerHandoffCandidates) DiscoverOwnerHandoffCustomerIDs(ctx context.Context, mode customerport.OwnerHandoffMode, sourceStaffID int64, corpScope string, limit int) ([]customerdomain.CustomerID, error) {
	if a.staff == nil || a.owners == nil || sourceStaffID < 1 || limit < 1 || limit > 20000 {
		return nil, errors.New("owner handoff all-range adapter unavailable")
	}
	source, err := a.staff.UserByID(ctx, sourceStaffID, false)
	if err != nil || source.ID != sourceStaffID {
		return nil, errors.New("owner handoff source unavailable")
	}
	seen := map[customerdomain.CustomerID]struct{}{}
	add := func(values []customerdomain.CustomerID) error {
		for _, id := range values {
			if id < 1 {
				continue
			}
			seen[id] = struct{}{}
			if len(seen) > limit {
				return errors.New("owner handoff all-range exceeds limit")
			}
		}
		return nil
	}
	if mode == customerport.OwnerHandoffLocalOnly {
		// Customer-local ownership is authoritative when it exists. Start with
		// its source rows, then add only WeCom primaries that have no local row.
		// This preserves an explicit reassignment to another staff member even
		// while a historical WeCom observation still names the old source.
		localIDs, localErr := a.owners.ListOwnerHandoffCustomerIDs(ctx, sourceStaffID, limit+1)
		if localErr != nil {
			return nil, localErr
		}
		if err = add(localIDs); err != nil {
			return nil, err
		}
		// An Access user without a provider userid can still complete the
		// old local-only workflow for its explicit Customer-owned rows. There
		// is simply no trusted WeCom primary fallback to add in that case.
		if source.WeComUserID != "" {
			if a.primaryLister == nil {
				return nil, errors.New("owner handoff primary-owner discovery unavailable")
			}
			primaryIDs, primaryErr := a.primaryLister.ListOwnerHandoffPrimaryOwnerCustomerIDs(ctx, corpScope, source.WeComUserID, limit+1)
			if primaryErr != nil {
				return nil, primaryErr
			}
			// The source query is intentionally bounded before Customer applies
			// local-owner precedence. A full page at this bound may hide eligible
			// rows after overridden rows are filtered, so fail closed rather than
			// claim this is an all-range preview.
			if len(primaryIDs) > limit {
				return nil, errors.New("owner handoff all-range primary source reached limit; narrow the range")
			}
			for _, customerID := range primaryIDs {
				owner, found, ownerErr := a.owners.LocalOwner(ctx, customerID, false)
				if ownerErr != nil {
					return nil, ownerErr
				}
				if found && owner.StaffID != sourceStaffID {
					continue
				}
				if err = add([]customerdomain.CustomerID{customerID}); err != nil {
					return nil, err
				}
			}
		}
	}
	if mode == customerport.OwnerHandoffWeComThenCRM {
		if a.relationshipLister == nil || source.WeComUserID == "" {
			return nil, errors.New("owner handoff source relation unavailable")
		}
		ids, listErr := a.relationshipLister.ListOwnerHandoffCustomerIDs(ctx, corpScope, source.WeComUserID, limit+1)
		if listErr != nil {
			return nil, listErr
		}
		if err = add(ids); err != nil {
			return nil, err
		}
	}
	if len(seen) == 0 {
		return nil, errors.New("owner handoff all-range has no eligible candidates")
	}
	ids := make([]customerdomain.CustomerID, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids, nil
}

var _ customerport.OwnerHandoffCandidateDiscoverer = customerOwnerHandoffCandidates{}

// customerOwnerHandoffPreviewPresenter projects existing, authorized admin
// display data only after Customer has selected its canonical preview rows.
// It uses stable owning Ports and never turns a displayed external ID into an
// identity decision or an owner write.
type customerOwnerHandoffPreviewPresenter struct {
	uow        platformport.UnitOfWork
	display    customerport.DirectoryDisplayNameReader
	identities identityport.ExternalIdentityValueReader
	staff      interface {
		UserByID(context.Context, int64, bool) (accessdomain.User, error)
	}
}

func (a customerOwnerHandoffPreviewPresenter) PresentOwnerHandoffPreview(ctx context.Context, corpScope string, sourceStaffID int64, rows []customerport.OwnerHandoffPreviewRow) (map[customerdomain.CustomerID]customerport.OwnerHandoffPreviewPresentation, error) {
	if a.uow == nil || a.display == nil || a.identities == nil || a.staff == nil || strings.TrimSpace(corpScope) == "" || sourceStaffID < 1 {
		return nil, errors.New("owner handoff preview presentation unavailable")
	}
	out := make(map[customerdomain.CustomerID]customerport.OwnerHandoffPreviewPresentation, len(rows))
	if len(rows) == 0 {
		return out, nil
	}
	return out, a.uow.Within(ctx, func(tx context.Context) error {
		ids := make([]customerdomain.CustomerID, 0, len(rows))
		seen := make(map[customerdomain.CustomerID]struct{}, len(rows))
		staffIDs := map[int64]struct{}{sourceStaffID: {}}
		for _, row := range rows {
			if row.CustomerID < 1 {
				return errors.New("owner handoff preview presentation invalid customer")
			}
			if _, exists := seen[row.CustomerID]; !exists {
				seen[row.CustomerID] = struct{}{}
				ids = append(ids, row.CustomerID)
			}
			if row.ExpectedOwnerID > 0 {
				staffIDs[row.ExpectedOwnerID] = struct{}{}
			}
		}
		names := make(map[customerdomain.CustomerID]string, len(ids))
		for start := 0; start < len(ids); start += 200 {
			end := min(start+200, len(ids))
			values, err := a.display.DisplayNames(tx, ids[start:end])
			if err != nil {
				return err
			}
			for id, name := range values {
				names[id] = name
			}
		}
		ownerUserIDs := make(map[int64]string, len(staffIDs))
		for staffID := range staffIDs {
			user, err := a.staff.UserByID(tx, staffID, false)
			if err != nil {
				return err
			}
			ownerUserIDs[staffID] = user.WeComUserID
		}
		for _, row := range rows {
			externalID, found, err := a.identities.VerifiedExternalIdentityValue(tx, row.CustomerID, identitydomain.KindWeComExternalUserID, corpScope)
			if err != nil {
				return err
			}
			ownerID := row.ExpectedOwnerID
			if ownerID < 1 {
				ownerID = sourceStaffID
			}
			if !found {
				externalID = ""
			}
			out[row.CustomerID] = customerport.OwnerHandoffPreviewPresentation{ExternalUserID: externalID, CustomerDisplayName: names[row.CustomerID], CurrentOwnerUserID: ownerUserIDs[ownerID]}
		}
		return nil
	})
}

var _ customerport.OwnerHandoffPreviewPresenter = customerOwnerHandoffPreviewPresenter{}
