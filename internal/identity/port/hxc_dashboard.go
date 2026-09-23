package port

import (
	"context"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
)

type HXCDisposition string
type HXCMatchSource string
type HXCReason string

const (
	HXCMatched   HXCDisposition = "matched"
	HXCUnmatched HXCDisposition = "unmatched"
	HXCConflict  HXCDisposition = "conflict"
	HXCInvalid   HXCDisposition = "invalid"

	HXCMatchNone    HXCMatchSource = "none"
	HXCMatchUnionID HXCMatchSource = "unionid"
	HXCMatchPhone   HXCMatchSource = "phone"
	HXCMatchBoth    HXCMatchSource = "both"

	HXCReasonMatchedUnionID        HXCReason = "matched_unionid"
	HXCReasonMatchedPhone          HXCReason = "matched_phone"
	HXCReasonMatchedBoth           HXCReason = "matched_both"
	HXCReasonNoMatch               HXCReason = "no_match"
	HXCReasonMissingIdentity       HXCReason = "missing_identity"
	HXCReasonInvalidUnionID        HXCReason = "invalid_unionid"
	HXCReasonInvalidPhone          HXCReason = "invalid_phone"
	HXCReasonDuplicateUnionID      HXCReason = "duplicate_hxc_unionid"
	HXCReasonDuplicatePhone        HXCReason = "duplicate_hxc_phone"
	HXCReasonDuplicateCustomer     HXCReason = "duplicate_hxc_customer"
	HXCReasonIdentityMultipleRoots HXCReason = "identity_multiple_roots"
	HXCReasonCrossRoot             HXCReason = "unionid_phone_cross_root"
	HXCReasonConcurrentConflict    HXCReason = "concurrent_identity_conflict"
)

// HXCSubject contains raw identities only inside the in-process trusted
// HXC-to-Identity boundary. Implementations must never log or persist these
// fields outside Identity-owned encrypted storage.
type HXCSubject struct {
	Position        int
	SubjectDigest   [32]byte
	PayloadDigest   [32]byte
	UnionIDScope    string
	UnionID         string
	UnionIDVerified bool
	Phone           string
	SourceUpdatedAt time.Time
	ConflictReason  HXCReason
	RuleVersion     string
}

type HXCSubjectResult struct {
	Position         int
	Disposition      HXCDisposition
	MatchedBy        HXCMatchSource
	Reason           HXCReason
	CustomerID       customerdomain.CustomerID
	ConflictID       int64
	MergeCandidateID int64
	Replayed         bool
	UnionCustomerID  customerdomain.CustomerID
	PhoneCustomerID  customerdomain.CustomerID
}

type HXCIdentityInspector interface {
	InspectHXCSubjects(context.Context, []HXCSubject) ([]HXCSubjectResult, error)
}

type HXCIdentityWriter interface {
	ApplyHXCSubject(context.Context, HXCSubject) (HXCSubjectResult, error)
	CompleteHXCSnapshot(context.Context, [][32]byte) error
}

type HXCIdentityCoordinator interface {
	HXCIdentityInspector
	HXCIdentityWriter
}

// Deprecated compatibility surface retained until the HXC application moves
// to HXCIdentityCoordinator in the same release.
type ScopedUnionID struct {
	Position int
	Scope    string
	UnionID  string
}

type ScopedUnionIDResult struct {
	Position   int
	Status     ResolveStatus
	CustomerID customerdomain.CustomerID
}

type HXCUnionIDBatchResolver interface {
	ResolveHXCUnionIDs(context.Context, []ScopedUnionID) ([]ScopedUnionIDResult, error)
}

type HXCSourceConflict struct {
	ID               int64                     `json:"id"`
	SubjectRef       string                    `json:"subject_ref"`
	Reason           HXCReason                 `json:"reason"`
	LeftCustomerID   customerdomain.CustomerID `json:"left_customer_id,omitempty"`
	RightCustomerID  customerdomain.CustomerID `json:"right_customer_id,omitempty"`
	MergeCandidateID int64                     `json:"merge_candidate_id,omitempty"`
	EvidenceDigest   string                    `json:"evidence_digest"`
	Status           string                    `json:"status"`
	Version          int64                     `json:"version"`
	CreatedAt        time.Time                 `json:"created_at"`
	ResolvedAt       *time.Time                `json:"resolved_at,omitempty"`
}

type IgnoreHXCSourceConflictCommand struct {
	ConflictID      int64
	ExpectedVersion int64
	ReasonCode      string
	Operator        string
	IdempotencyKey  string
}

type HXCSourceConflictManager interface {
	IgnoreHXCSourceConflict(context.Context, IgnoreHXCSourceConflictCommand) (HXCSourceConflict, bool, error)
}
