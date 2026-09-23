// Package port freezes the public Identity boundary.
package port

import (
	"context"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
)

type ResolveStatus string

const (
	ResolveFound    ResolveStatus = "found"
	ResolveNotFound ResolveStatus = "not_found"
	ResolveConflict ResolveStatus = "conflict"
)

type ResolveResult struct {
	Status     ResolveStatus
	CustomerID customerdomain.CustomerID
	IdentityID int64
}

type Resolver interface {
	Resolve(context.Context, identitydomain.Reference) (ResolveResult, error)
}

// CanonicalLineageReader is the narrow, read-only OneID bridge for domains
// that retain immutable customer IDs at ingestion.  It returns the current
// canonical root and only the historical roots which currently resolve to it;
// callers must not copy merge rules or rewrite their historical facts.
type CanonicalLineageReader interface {
	CanonicalLineage(context.Context, customerdomain.CustomerID) ([]customerdomain.CustomerID, error)
}

// CanonicalCustomerRootsReader resolves a bounded batch of historical
// Customer IDs to their current canonical roots. It exposes no identity value
// and must fail closed when a requested root is missing or malformed.
// Implementations de-duplicate duplicate inputs and return one map entry for
// every distinct requested ID.
type CanonicalCustomerRootsReader interface {
	CanonicalCustomerRoots(context.Context, []customerdomain.CustomerID) (map[customerdomain.CustomerID]customerdomain.CustomerID, error)
}

// TrustedCanonicalCustomerReader verifies only whether an already-selected
// canonical Customer root has active provider-verified identity evidence. It
// reveals no identity value, kind, scope, or provider metadata and cannot be
// used to resolve or provision a customer.
type TrustedCanonicalCustomerReader interface {
	HasActiveVerifiedIdentity(context.Context, customerdomain.CustomerID) (bool, error)
}

// LockedCanonicalLineageReader pins roots against concurrent merge for the caller UoW.
type LockedCanonicalLineageReader interface {
	LockedCanonicalLineage(context.Context, customerdomain.CustomerID) ([]customerdomain.CustomerID, error)
}

// OutboundWeComIdentityReader is consumed only by the composition-owned
// private-message target resolver. It may reveal a verified channel identity
// to Outbound in memory, never to an HTTP response or structured log.
type OutboundWeComIdentityReader interface {
	VerifiedWeComIdentityForCustomer(context.Context, customerdomain.CustomerID, string) (string, bool, error)
}

type ProvisionCommand struct {
	Fact           identitydomain.VerifiedFact
	IdempotencyKey string
}

type ProvisionResult struct {
	CustomerID customerdomain.CustomerID
	IdentityID int64
	Created    bool
}

type VerifiedProvisioner interface {
	ProvisionVerifiedIdentity(context.Context, ProvisionCommand) (ProvisionResult, error)
}

// ProvisionedCustomerObserver is a composition-owned bridge invoked only when
// Identity creates a new canonical Customer root. Implementations may ensure
// other domains' minimum local projections inside the caller's existing UoW;
// they must not resolve, link, or merge identities.
type ProvisionedCustomerObserver interface {
	ObserveProvisionedCustomer(context.Context, customerdomain.CustomerID, string) error
}

// DeclaredAttachCommand deliberately contains an existing CustomerID and no
// provisioning or merge instruction. SourceRowDigest is the non-PII replay
// fingerprint retained by the one-time import ledger.
type DeclaredAttachCommand struct {
	CustomerID      customerdomain.CustomerID
	Reference       identitydomain.Reference
	ImportRunID     int64
	SourceRowID     string
	SourceRowDigest [32]byte
	IdempotencyKey  string
}

type DeclaredAttachStatus string

const (
	DeclaredAttached      DeclaredAttachStatus = "attached"
	DeclaredAlreadyLinked DeclaredAttachStatus = "already_linked"
	DeclaredConflict      DeclaredAttachStatus = "conflict"
	DeclaredInvalid       DeclaredAttachStatus = "invalid"
	DeclaredReplayed      DeclaredAttachStatus = "replayed"
)

type DeclaredAttachResult struct {
	Status     DeclaredAttachStatus
	ReplayOf   DeclaredAttachStatus
	CustomerID customerdomain.CustomerID
	IdentityID int64
}

// DeclaredIdentityAttacher can only attach a declared identity to a known
// customer. It intentionally exposes neither Provision nor Merge.
type DeclaredIdentityAttacher interface {
	AttachDeclaredIdentity(context.Context, DeclaredAttachCommand) (DeclaredAttachResult, error)
}

// DeclaredPhoneCommand binds an unverified CN11 phone to an already-resolved
// Customer. It cannot provision or merge roots. IdempotencyKey is stored only
// as a digest; SourceEventID must not contain the phone value.
type DeclaredPhoneCommand struct {
	CustomerID     customerdomain.CustomerID
	Phone          string
	Source         string
	SourceEventID  string
	IdempotencyKey string
}

type DeclaredPhoneAttacher interface {
	AttachDeclaredPhoneToCustomer(context.Context, DeclaredPhoneCommand) (DeclaredAttachResult, error)
}

type DirectoryIdentitySummary struct {
	Kind      identitydomain.Kind      `json:"kind"`
	Scope     string                   `json:"scope"`
	Assurance identitydomain.Assurance `json:"assurance"`
	Status    string                   `json:"status"`
	Source    string                   `json:"source"`
	CreatedAt time.Time                `json:"created_at"`
}

type MaskedPhone struct {
	Masked    string                   `json:"masked"`
	Assurance identitydomain.Assurance `json:"assurance"`
}

// DirectoryIdentityReader is the read-only Identity boundary used by the
// customer directory. Only RevealPhone may return a raw phone, and callers
// must enforce RBAC, CSRF, no-store and audit before returning it.
type DirectoryIdentityReader interface {
	VerifiedWeComCustomer(context.Context, string, string) (customerdomain.CustomerID, bool, error)
	CustomerForPhone(context.Context, string) (customerdomain.CustomerID, bool, error)
	DirectoryIdentities(context.Context, customerdomain.CustomerID) ([]DirectoryIdentitySummary, []MaskedPhone, error)
	RevealPhone(context.Context, customerdomain.CustomerID) (string, bool, error)
}

// ExternalIdentityValueReader is a narrowly scoped, transaction-bound read
// used by composition to resolve a current Provider relationship. Callers may
// use the returned value only transiently for an authorized Provider call and
// must never persist or log it outside Identity/WeCom ownership.
type ExternalIdentityValueReader interface {
	VerifiedExternalIdentityValue(context.Context, customerdomain.CustomerID, identitydomain.Kind, string) (string, bool, error)
}

// MachineIdentityFactReader is the explicit, audited Identity-owned export
// seam for machine clients. Values retain their real scope and assurance; a
// caller must never infer either from a normalized string.
type MachineIdentityFact struct {
	Kind      identitydomain.Kind
	Scope     string
	Value     string
	Assurance identitydomain.Assurance
	Source    string
	Status    string
}
type MachineIdentityFactReader interface {
	MachineIdentityFacts(context.Context, customerdomain.CustomerID) ([]MachineIdentityFact, error)
}

// MachineIdentityExport is the canonical, typed result for an authorized
// machine identity read. A caller must preserve Conflict and Missing instead
// of treating either state as an empty set of identities.
type MachineIdentityExportStatus string

const (
	MachineIdentityExportFound    MachineIdentityExportStatus = "found"
	MachineIdentityExportMissing  MachineIdentityExportStatus = "missing"
	MachineIdentityExportConflict MachineIdentityExportStatus = "conflict"
)

type MachineIdentityExport struct {
	Status              MachineIdentityExportStatus
	CanonicalCustomerID customerdomain.CustomerID
	Facts               []MachineIdentityFact
}

type MachineIdentityExportReader interface {
	MachineIdentityExport(context.Context, customerdomain.CustomerID) (MachineIdentityExport, error)
}

// GroupCandidateIdentityReader reads a unique active verified identity only.
// Missing or ambiguous evidence must remain unknown; this never provisions.
type GroupCandidateIdentityReader interface {
	VerifiedWeComIdentityForCustomer(context.Context, customerdomain.CustomerID, string) (string, bool, error)
}

// AdminRadarVisitorExternalContactStatus states whether Identity can safely
// display an external contact identifier to an already-authorized Radar admin.
// It is not a Provider-call capability and is never an instruction to select
// an arbitrary historical identity.
type AdminRadarVisitorExternalContactStatus string

const (
	AdminRadarVisitorExternalContactAvailable   AdminRadarVisitorExternalContactStatus = "available"
	AdminRadarVisitorExternalContactMissing     AdminRadarVisitorExternalContactStatus = "missing"
	AdminRadarVisitorExternalContactAmbiguous   AdminRadarVisitorExternalContactStatus = "ambiguous"
	AdminRadarVisitorExternalContactUnavailable AdminRadarVisitorExternalContactStatus = "unavailable"
)

type AdminRadarVisitorIdentity struct {
	CanonicalCustomerID   customerdomain.CustomerID
	ExternalContactID     string
	ExternalContactStatus AdminRadarVisitorExternalContactStatus
}

// AdminRadarVisitorIdentityReader is a narrow, transaction-bound Identity
// read for a CSRF- and role-authorized Radar management response. It returns
// only the one scoped external contact value that is safe to display and
// current canonical roots; it cannot resolve/provision/link identities and
// never exposes OpenID, UnionID, phones, identity IDs, scope or evidence.
type AdminRadarVisitorIdentityReader interface {
	AdminRadarVisitorIdentities(context.Context, string, []customerdomain.CustomerID) (map[customerdomain.CustomerID]AdminRadarVisitorIdentity, error)
	SearchAdminRadarVisitorCustomers(context.Context, string, string, []customerdomain.CustomerID, int) ([]customerdomain.CustomerID, error)
}
