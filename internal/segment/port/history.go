package port

import (
	"context"
	"time"
)

// HistoricalImport is an explicit one-time migration contract. Evidence is an
// authenticated encrypted source snapshot; its key is never persisted here.
// This Port cannot evaluate SQL, provision identities, refresh, or send.
type HistoricalImport struct {
	Source                 string
	Digest                 Digest
	CapturedAt             time.Time
	ResolutionSourceDigest Digest
	ResolutionParentDigest Digest
	ResolutionDerivedAt    time.Time
	EncryptedEvidence      []byte
	Actor                  MutationActor
	Groups                 []HistoricalGroup
	Packages               []HistoricalPackage
	Rows                   []HistoricalRow
}
type HistoricalGroup struct {
	SourceID int64
	Name     string
}
type HistoricalPackage struct {
	SourceID, GroupSourceID int64
	Name                    string
	Archived                bool
	Members                 []HistoricalMember
}
type HistoricalMember struct {
	SourceID   int64
	CustomerID int64 // canonical identity resolved by the Identity Port
	Active     bool
	EnteredAt  time.Time
	Reason     string // resolved, exited, unresolved, conflict, missing_scope, invalid
}
type HistoricalRow struct {
	Kind     string
	SourceID int64
	Digest   Digest
}
type HistoricalImportResult struct {
	Groups           int  `json:"groups"`
	Packages         int  `json:"packages"`
	SourceRows       int  `json:"source_rows"`
	SourceMembers    int  `json:"source_members"`
	ResolvedActive   int  `json:"resolved_active"`
	Exited           int  `json:"exited"`
	Quarantined      int  `json:"quarantined"`
	PublishedMembers int  `json:"published_members"`
	Replayed         bool `json:"replayed"`
}
type HistoricalImporter interface {
	// ImportHistorical participates in the caller's existing PostgreSQL UoW.
	ImportHistorical(context.Context, HistoricalImport) (HistoricalImportResult, error)
}

// HistoricalVerifier checks imported facts and immutable native snapshot membership
// through the owner; it never returns external identities or source SQL.
type HistoricalVerifier interface {
	VerifyHistorical(context.Context, string, Digest) (HistoricalImportResult, error)
}
