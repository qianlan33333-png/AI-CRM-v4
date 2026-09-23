// Package query exposes minimal, read-only OneID administration views.
//
// These types deliberately omit normalized identity values, evidence metadata,
// digests, sources, and merge operators so they are safe to serialize in an
// administrator-facing API.
package query

import (
	"context"
	"errors"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
)

const (
	DefaultLimit = 50
	MaximumLimit = 100
)

var (
	ErrInvalidQuery = errors.New("invalid identity query")
	ErrNotFound     = errors.New("identity query record not found")
)

type ListOptions struct {
	Status string
	Limit  int
	Offset int
}

type IdentitySummary struct {
	Kind  identitydomain.Kind `json:"kind"`
	Scope string              `json:"scope"`
}

type MergeLineageSummary struct {
	ID               int64                     `json:"id"`
	FromCustomerID   customerdomain.CustomerID `json:"from_customer_id"`
	ToCustomerID     customerdomain.CustomerID `json:"to_customer_id"`
	ReversibleStatus string                    `json:"reversible_status"`
	MergedAt         time.Time                 `json:"merged_at"`
	ReversedAt       *time.Time                `json:"reversed_at,omitempty"`
}

type CustomerDetail struct {
	CustomerID          customerdomain.CustomerID `json:"customer_id"`
	Status              customerdomain.Status     `json:"status"`
	CanonicalCustomerID customerdomain.CustomerID `json:"canonical_customer_id"`
	CanonicalStatus     customerdomain.Status     `json:"canonical_status"`
	Identities          []IdentitySummary         `json:"identities"`
	MergeLineage        []MergeLineageSummary     `json:"merge_lineage"`
}

// CanonicalCustomerRoot is the minimal, channel-neutral OneID result used by
// bulk audience evaluation. It intentionally contains no external identity.
type CanonicalCustomerRoot struct {
	RequestedCustomerID customerdomain.CustomerID
	CustomerID          customerdomain.CustomerID
}

type CanonicalCustomerReader interface {
	CanonicalCustomers(context.Context, []customerdomain.CustomerID) ([]CanonicalCustomerRoot, error)
}

type Conflict struct {
	ID              int64                     `json:"id"`
	LeftCustomerID  customerdomain.CustomerID `json:"left_customer_id"`
	RightCustomerID customerdomain.CustomerID `json:"right_customer_id"`
	Reason          string                    `json:"reason"`
	Status          string                    `json:"status"`
	CreatedAt       time.Time                 `json:"created_at"`
	ResolvedAt      *time.Time                `json:"resolved_at,omitempty"`
}

type ConflictPage struct {
	Items  []Conflict `json:"items"`
	Limit  int        `json:"limit"`
	Offset int        `json:"offset"`
}

type MergeCandidate struct {
	ID                         int64                           `json:"id"`
	LeftCustomerID             customerdomain.CustomerID       `json:"left_customer_id"`
	RightCustomerID            customerdomain.CustomerID       `json:"right_customer_id"`
	EvidenceStrength           identitydomain.EvidenceStrength `json:"evidence_strength"`
	Reason                     string                          `json:"reason"`
	Status                     string                          `json:"status"`
	SelectedSurvivorCustomerID *customerdomain.CustomerID      `json:"selected_survivor_customer_id,omitempty"`
	CreatedAt                  time.Time                       `json:"created_at"`
	ResolvedAt                 *time.Time                      `json:"resolved_at,omitempty"`
}

type MergeCandidatePage struct {
	Items  []MergeCandidate `json:"items"`
	Limit  int              `json:"limit"`
	Offset int              `json:"offset"`
}

type HXCSourceObservation struct {
	Kind         string `json:"kind"`
	Scope        string `json:"scope"`
	DisplayValue string `json:"display_value"`
	Assurance    string `json:"assurance"`
	Status       string `json:"status"`
}

type HXCSourceConflict struct {
	ID               int64                     `json:"id"`
	SubjectRef       string                    `json:"subject_ref"`
	Reason           string                    `json:"reason"`
	LeftCustomerID   customerdomain.CustomerID `json:"left_customer_id,omitempty"`
	RightCustomerID  customerdomain.CustomerID `json:"right_customer_id,omitempty"`
	MergeCandidateID int64                     `json:"merge_candidate_id,omitempty"`
	EvidenceDigest   string                    `json:"evidence_digest"`
	Status           string                    `json:"status"`
	Version          int64                     `json:"version"`
	Observations     []HXCSourceObservation    `json:"observations"`
	CreatedAt        time.Time                 `json:"created_at"`
	ResolvedAt       *time.Time                `json:"resolved_at,omitempty"`
}

type HXCSourceConflictPage struct {
	Items  []HXCSourceConflict `json:"items"`
	Limit  int                 `json:"limit"`
	Offset int                 `json:"offset"`
}

type Reader interface {
	Customer(context.Context, customerdomain.CustomerID) (CustomerDetail, error)
	Conflicts(context.Context, ListOptions) (ConflictPage, error)
	MergeCandidates(context.Context, ListOptions) (MergeCandidatePage, error)
	HXCSourceConflicts(context.Context, ListOptions) (HXCSourceConflictPage, error)
}
