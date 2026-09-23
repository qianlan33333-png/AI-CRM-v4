package app

import (
	"context"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
)

var (
	ErrInvalidLinkCommand        = errors.New("invalid identity link command")
	ErrInvalidMergeID            = errors.New("invalid merge id")
	ErrInsufficientLinkEvidence  = errors.New("insufficient identity link evidence")
	ErrConcurrentIdentityChange  = errors.New("concurrent identity change")
	ErrLinkIntentPayloadMismatch = errors.New("link intent replay payload mismatch")
	ErrDeclaredPayloadMismatch   = errors.New("declared identity replay payload mismatch")
	ErrHistoricalSubjectConflict = errors.New("historical subject identity conflict")
	ErrResolveConflict           = errors.New("identity resolution conflict")
)

// ProvisionHistoricalSubject creates or resolves one Customer root from the
// first authoritative identity, then attaches the remaining identities only
// when they are absent or already owned by that root. A cross-root hit is
// returned as an error so the caller's PostgreSQL UoW rolls back the candidate
// and the migration stops before assigning orders to the wrong person.
func (service OneIDService) ProvisionHistoricalSubject(ctx context.Context, command identityport.HistoricalSubjectCommand) (identityport.HistoricalSubjectResult, error) {
	if service.Store == nil || command.SubjectKey == "" || len(command.SubjectKey) > 200 || len(command.Facts) == 0 || command.SourceDigest == ([32]byte{}) {
		return identityport.HistoricalSubjectResult{}, ErrHistoricalSubjectConflict
	}
	for _, fact := range command.Facts {
		if !fact.Valid() || fact.Reference().Kind == identitydomain.KindPhone {
			return identityport.HistoricalSubjectResult{}, ErrHistoricalSubjectConflict
		}
	}
	provisioned, err := service.Store.Provision(ctx, command.Facts[0])
	if err != nil {
		return identityport.HistoricalSubjectResult{}, err
	}
	if err = service.observeProvisionedCustomer(ctx, provisioned, command.Facts[0].Reference().Source); err != nil {
		return identityport.HistoricalSubjectResult{}, err
	}
	result := identityport.HistoricalSubjectResult{CustomerID: provisioned.CustomerID, IdentityIDs: []int64{provisioned.IdentityID}}
	for index := 1; index < len(command.Facts); index++ {
		linked, linkErr := service.Store.Link(ctx, LinkCommand{
			SourceCustomerID: provisioned.CustomerID,
			Target:           command.Facts[index],
			Evidence: identitydomain.LinkEvidence{
				Type:          "provider_history_subject",
				Strength:      identitydomain.EvidenceStrong,
				Source:        "commerce_history",
				EventID:       command.SubjectKey,
				Digest:        "sha256:" + hex.EncodeToString(command.SourceDigest[:]),
				PolicyVersion: "commerce-history-v1",
			},
		})
		if linkErr != nil {
			return identityport.HistoricalSubjectResult{}, linkErr
		}
		if (linked.Status != LinkAttached && linked.Status != LinkAlreadyLinked) || linked.CustomerID != provisioned.CustomerID || linked.IdentityID < 1 {
			return identityport.HistoricalSubjectResult{}, ErrHistoricalSubjectConflict
		}
		result.IdentityIDs = append(result.IdentityIDs, linked.IdentityID)
	}
	return result, nil
}

type LinkStatus string

const (
	LinkAttached      LinkStatus = "attached"
	LinkAlreadyLinked LinkStatus = "already_linked"
	LinkCandidate     LinkStatus = "merge_candidate"
	// LinkCandidateRejected is a committed confirmation outcome: the open
	// candidate was rejected because one of its endpoint roots changed after
	// the candidate snapshot was created. It is deliberately not an error, so
	// transactional adapters can persist the rejection atomically.
	LinkCandidateRejected LinkStatus = "candidate_rejected"
	LinkConflict          LinkStatus = "conflict"
	LinkMerged            LinkStatus = "merged"
	LinkIntentExpired     LinkStatus = "intent_expired"
	LinkIntentReplay      LinkStatus = "intent_replayed"
	LinkIntentInvalidated LinkStatus = "intent_invalidated"
	LinkScopeMismatch     LinkStatus = "scope_mismatch"
)

type MergeCandidate struct {
	ID              int64
	LeftCustomerID  customerdomain.CustomerID
	RightCustomerID customerdomain.CustomerID
	Evidence        identitydomain.LinkEvidence
	Reason          string
	Status          string
	LeftVersion     int64
	RightVersion    int64
}

type Conflict struct {
	ID              int64
	LeftCustomerID  customerdomain.CustomerID
	RightCustomerID customerdomain.CustomerID
	Reason          string
	Evidence        identitydomain.LinkEvidence
}

type MergeRecord struct {
	ID                int64
	CandidateID       int64
	FromCustomerID    customerdomain.CustomerID
	ToCustomerID      customerdomain.CustomerID
	FromVersionBefore int64
	FromVersionAfter  int64
	ToVersionBefore   int64
	ToVersionAfter    int64
	FromLineageBefore int64
	FromLineageAfter  int64
	ToLineageBefore   int64
	ToLineageAfter    int64
	Evidence          identitydomain.LinkEvidence
	Rule              string
	Operator          string
	Reversed          bool
}

type LinkCommand struct {
	SourceCustomerID customerdomain.CustomerID
	Target           identitydomain.VerifiedFact
	Evidence         identitydomain.LinkEvidence
}

type LinkResult struct {
	Status     LinkStatus
	ReplayOf   LinkStatus
	CustomerID customerdomain.CustomerID
	IdentityID int64
	Candidate  *MergeCandidate
	Conflict   *Conflict
	Merge      *MergeRecord
}

type LinkIntentPurpose string

const (
	LinkIntentBindWeCom            LinkIntentPurpose = "bind_wecom"
	LinkIntentBindProviderIdentity LinkIntentPurpose = "bind_provider_identity"
)

type LinkIntentCommand struct {
	SourceCustomerID customerdomain.CustomerID
	Purpose          LinkIntentPurpose
	TargetKind       identitydomain.Kind
	ExpectedScope    string
	ExpiresAt        time.Time
	Source           string
	SourceEventID    string
}

type CreatedLinkIntent struct {
	ID        int64
	Token     string // returned once; stores retain only the token digest.
	ExpiresAt time.Time
}

type ConsumeLinkIntentCommand struct {
	Token    string
	Target   identitydomain.VerifiedFact
	Evidence identitydomain.LinkEvidence
}

type ConfirmMergeCommand struct {
	CandidateID        int64
	SurvivorCustomerID customerdomain.CustomerID
	Operator           string
}

type StoredIdentity struct {
	ID         int64
	CustomerID customerdomain.CustomerID
	Reference  identitydomain.NormalizedReference
}

type ProvisionedIdentity struct {
	CustomerID customerdomain.CustomerID
	IdentityID int64
	Created    bool
}

// Store is the Identity-owned transactional contract. A PostgreSQL adapter
// must execute each mutating method in one database transaction, lock the
// identity key before customer roots, and persist audit/outbox facts with the
// mutation. Candidate confirmation must compare endpoint versions, intent
// consumption must persist its payload fingerprint and result, and reversal
// must CAS every recorded merge member before moving any identity. MemoryStore
// is a deterministic test implementation only.
type Store interface {
	Resolve(context.Context, identitydomain.NormalizedReference) (StoredIdentity, bool, error)
	Provision(context.Context, identitydomain.VerifiedFact) (ProvisionedIdentity, error)
	Link(context.Context, LinkCommand) (LinkResult, error)
	CreateLinkIntent(context.Context, LinkIntentCommand) (CreatedLinkIntent, error)
	ConsumeLinkIntent(context.Context, ConsumeLinkIntentCommand) (LinkResult, error)
	ConfirmMerge(context.Context, ConfirmMergeCommand) (LinkResult, error)
	RevertMerge(context.Context, int64) (MergeRecord, error)
	AttachDeclaredPhone(context.Context, identityport.DeclaredPhoneCommand, identitydomain.NormalizedReference) (identityport.DeclaredAttachResult, error)
}

func (service OneIDService) AttachDeclaredPhoneToCustomer(ctx context.Context, command identityport.DeclaredPhoneCommand) (identityport.DeclaredAttachResult, error) {
	if service.Store == nil || command.CustomerID < 1 || command.Source != "survey" && command.Source != "phone_import" && command.Source != "sidebar" && command.Source != "hxc" || command.SourceEventID == "" || len(command.SourceEventID) > 200 || command.IdempotencyKey == "" || len(command.IdempotencyKey) > 200 {
		return identityport.DeclaredAttachResult{Status: identityport.DeclaredInvalid}, nil
	}
	normalized, err := identitydomain.Normalize(identitydomain.Reference{Kind: identitydomain.KindPhone, Scope: "phone:cn11", Value: command.Phone, Assurance: identitydomain.AssuranceDeclared, Source: command.Source})
	if err != nil {
		return identityport.DeclaredAttachResult{Status: identityport.DeclaredInvalid}, nil
	}
	return service.Store.AttachDeclaredPhone(ctx, command, normalized)
}

// AttachDeclaredIdentity adds a low-assurance reference to an existing active
// Customer root. The command cannot create a customer, create a merge
// candidate, or promote the reference to verified.
func (service OneIDService) AttachDeclaredIdentity(ctx context.Context, command identityport.DeclaredAttachCommand) (identityport.DeclaredAttachResult, error) {
	if service.Store == nil || command.CustomerID < 1 || command.ImportRunID < 1 || command.SourceRowID == "" || command.IdempotencyKey == "" ||
		command.Reference.Assurance != identitydomain.AssuranceDeclared || command.Reference.Kind != identitydomain.KindPhone ||
		command.Reference.Scope != "phone:e164" || command.Reference.Source != "phone_import" {
		return identityport.DeclaredAttachResult{Status: identityport.DeclaredInvalid}, nil
	}
	normalized, err := identitydomain.Normalize(command.Reference)
	if err != nil {
		return identityport.DeclaredAttachResult{Status: identityport.DeclaredInvalid}, nil
	}
	phone := strings.TrimPrefix(normalized.NormalizedValue, "+86")
	return service.AttachDeclaredPhoneToCustomer(ctx, identityport.DeclaredPhoneCommand{
		CustomerID: command.CustomerID, Phone: phone, Source: "phone_import",
		SourceEventID:  "phone-import:" + strconv.FormatInt(command.ImportRunID, 10) + ":" + hex.EncodeToString(command.SourceRowDigest[:]),
		IdempotencyKey: command.IdempotencyKey,
	})
}

// OneIDService owns identity resolution, verified provisioning and explicit
// cross-root linking. It never creates a customer from Resolve.
type OneIDService struct {
	Store               Store
	ProvisionedCustomer identityport.ProvisionedCustomerObserver
}

func (service OneIDService) observeProvisionedCustomer(ctx context.Context, provisioned ProvisionedIdentity, source string) error {
	if !provisioned.Created || service.ProvisionedCustomer == nil {
		return nil
	}
	return service.ProvisionedCustomer.ObserveProvisionedCustomer(ctx, provisioned.CustomerID, source)
}

func (service OneIDService) Resolve(ctx context.Context, reference identitydomain.Reference) (identityport.ResolveResult, error) {
	normalized, err := identitydomain.Normalize(reference)
	if err != nil {
		return identityport.ResolveResult{}, err
	}
	stored, found, err := service.Store.Resolve(ctx, normalized)
	if errors.Is(err, ErrResolveConflict) {
		return identityport.ResolveResult{Status: identityport.ResolveConflict}, nil
	}
	if err != nil {
		return identityport.ResolveResult{}, err
	}
	if !found {
		return identityport.ResolveResult{Status: identityport.ResolveNotFound}, nil
	}
	return identityport.ResolveResult{
		Status:     identityport.ResolveFound,
		CustomerID: stored.CustomerID,
		IdentityID: stored.ID,
	}, nil
}

// ProvisionCustomerFromVerifiedIdentity is intentionally separate from
// Resolve. Only a provider adapter can supply the opaque VerifiedFact input.
func (service OneIDService) ProvisionCustomerFromVerifiedIdentity(ctx context.Context, fact identitydomain.VerifiedFact) (identityport.ProvisionResult, error) {
	if !fact.Valid() || fact.Reference().Kind == identitydomain.KindPhone {
		return identityport.ProvisionResult{}, identitydomain.ErrInvalidReference
	}
	provisioned, err := service.Store.Provision(ctx, fact)
	if err != nil {
		return identityport.ProvisionResult{}, err
	}
	if err = service.observeProvisionedCustomer(ctx, provisioned, fact.Reference().Source); err != nil {
		return identityport.ProvisionResult{}, err
	}
	return identityport.ProvisionResult{
		CustomerID: provisioned.CustomerID,
		IdentityID: provisioned.IdentityID,
		Created:    provisioned.Created,
	}, nil
}

// ProvisionVerifiedIdentity preserves the published Identity port while
// keeping the command's input opaque and provider-verified.
func (service OneIDService) ProvisionVerifiedIdentity(ctx context.Context, command identityport.ProvisionCommand) (identityport.ProvisionResult, error) {
	return service.ProvisionCustomerFromVerifiedIdentity(ctx, command.Fact)
}

func (service OneIDService) LinkVerifiedIdentity(ctx context.Context, command LinkCommand) (LinkResult, error) {
	if command.SourceCustomerID < 1 || !command.Target.Valid() || command.Target.Reference().Kind == identitydomain.KindPhone || !command.Evidence.Valid() {
		return LinkResult{}, ErrInvalidLinkCommand
	}
	return service.Store.Link(ctx, command)
}

func (service OneIDService) CreateLinkIntent(ctx context.Context, command LinkIntentCommand) (CreatedLinkIntent, error) {
	if command.SourceCustomerID < 1 || !validLinkIntentPurpose(command.Purpose) || identitydomain.ValidateKind(command.TargetKind) != nil || command.TargetKind == identitydomain.KindPhone ||
		(command.ExpectedScope != "" && identitydomain.ValidateNamespace(command.TargetKind, command.ExpectedScope) != nil) ||
		(command.Purpose == LinkIntentBindWeCom && command.TargetKind != identitydomain.KindWeComExternalUserID) ||
		command.ExpiresAt.IsZero() || !command.ExpiresAt.After(time.Now()) || command.Source == "" {
		return CreatedLinkIntent{}, ErrInvalidLinkCommand
	}
	return service.Store.CreateLinkIntent(ctx, command)
}

// A consumed one-time intent can attach a missing identity, but any existing
// cross-root association remains a merge candidate for separate confirmation.
func (service OneIDService) ConsumeLinkIntent(ctx context.Context, command ConsumeLinkIntentCommand) (LinkResult, error) {
	if command.Token == "" || !command.Target.Valid() || command.Target.Reference().Kind == identitydomain.KindPhone || !command.Evidence.Valid() || command.Evidence.Strength != identitydomain.EvidenceStrong {
		return LinkResult{}, ErrInvalidLinkCommand
	}
	return service.Store.ConsumeLinkIntent(ctx, command)
}

// ConfirmMerge is the only path that changes two active customer roots into a
// merge lineage. The selected survivor must be a candidate endpoint; when
// exactly one endpoint has a WeCom identity, that endpoint is mandatory.
func (service OneIDService) ConfirmMerge(ctx context.Context, command ConfirmMergeCommand) (LinkResult, error) {
	if command.CandidateID < 1 || command.SurvivorCustomerID < 1 || command.Operator == "" {
		return LinkResult{}, ErrInvalidLinkCommand
	}
	return service.Store.ConfirmMerge(ctx, command)
}

func (service OneIDService) RevertConfirmedMerge(ctx context.Context, mergeID int64) (MergeRecord, error) {
	if mergeID < 1 {
		return MergeRecord{}, ErrInvalidMergeID
	}
	return service.Store.RevertMerge(ctx, mergeID)
}

func validLinkIntentPurpose(purpose LinkIntentPurpose) bool {
	return purpose == LinkIntentBindWeCom || purpose == LinkIntentBindProviderIdentity
}
