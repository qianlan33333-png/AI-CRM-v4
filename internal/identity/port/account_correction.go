package port

import identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"

// AccountCorrectionPlan contains only reviewed internal IDs and CAS versions.
// Provider values are read in-process, never supplied in a maintenance DTO.
type AccountCorrectionPlan struct {
	RunKey               string  `json:"run_key"`
	Operator             string  `json:"operator"`
	LeftCustomerID       int64   `json:"left_customer_id"`
	RightCustomerID      int64   `json:"right_customer_id"`
	LeftVersion          int64   `json:"left_version"`
	RightVersion         int64   `json:"right_version"`
	WrongIdentityID      int64   `json:"wrong_identity_id"`
	WrongIdentityVersion int64   `json:"wrong_identity_version"`
	CandidateID          int64   `json:"candidate_id"`
	CandidateVersion     int64   `json:"candidate_version"`
	ConflictIDs          []int64 `json:"conflict_ids"`
	HXCSubjectID         int64   `json:"hxc_subject_id"`
	HXCSubjectVersion    int64   `json:"hxc_subject_version"`
}

type AccountCorrectionCommand struct {
	Plan                  AccountCorrectionPlan
	LeftExternal          identitydomain.VerifiedFact
	RightExternal         identitydomain.VerifiedFact
	LeftUnion, RightUnion identitydomain.VerifiedFact
}

type AccountCorrectionResult struct {
	EvidenceID        int64   `json:"evidence_id"`
	RetiredIdentityID int64   `json:"retired_identity_id"`
	LeftUnionID       int64   `json:"left_union_identity_id"`
	RightUnionID      int64   `json:"right_union_identity_id"`
	RejectedCandidate int64   `json:"rejected_candidate_id"`
	ResolvedConflicts []int64 `json:"resolved_conflicts"`
	ReviewedSubject   int64   `json:"reviewed_subject_id"`
	Replayed          bool    `json:"replayed"`
}
