package port

import (
	"context"
	"time"
)

// CatalogMutationOperation describes one provider catalog write. It contains
// no customer relationship or external-contact identifier.
type CatalogMutationOperation string

const (
	CatalogGroupCreate  CatalogMutationOperation = "group_create"
	CatalogGroupUpdate  CatalogMutationOperation = "group_update"
	CatalogGroupArchive CatalogMutationOperation = "group_archive"
	CatalogTagCreate    CatalogMutationOperation = "tag_create"
	CatalogTagUpdate    CatalogMutationOperation = "tag_update"
	CatalogTagArchive   CatalogMutationOperation = "tag_archive"
)

type CatalogMutationPlan struct {
	Operation          CatalogMutationOperation
	Actor              int64
	GroupID, TagID     int64
	GroupName, TagName string
	IdempotencyKey     string
	TraceID            string
}

// CatalogMutationScope names the local catalog resource whose next provider
// mutation must wait for every earlier provider outcome. Tag writes use their
// parent group scope as well, so a pending child create cannot race a group
// archive and a pending group write cannot race a child rename.
type CatalogMutationScope struct {
	Operation      CatalogMutationOperation
	GroupID, TagID int64
}

type CatalogMutationIntent struct {
	ID                 int64
	Operation          CatalogMutationOperation
	Actor              int64
	GroupID, TagID     int64
	GroupName, TagName string
	ProviderGroupID    string
	ProviderTagID      string
}

type CatalogMutationDispatchReader interface {
	ReadCatalogMutationDispatch(context.Context, string) (CatalogMutationDispatch, error)
}

// CatalogMutationStore is Tag-owned persistence for its frozen provider
// dispatch snapshot and terminal outcome. It has no network dependency.
type CatalogMutationStore interface {
	GuardCatalogMutation(context.Context, CatalogMutationScope) error
	ReserveCatalogMutation(context.Context, CatalogMutationPlan) (CatalogMutationIntent, error)
	AcceptCatalogMutation(context.Context, int64, CatalogMutationEffectReceipt) error
	ReadCatalogMutationDispatch(context.Context, string) (CatalogMutationDispatch, error)
	CompleteCatalogMutation(context.Context, CatalogMutationCompletion) error
}

// CatalogMutationEnqueuer is the only cross-domain intent submission point.
// Outbound implements it and accepts the EER effect in the caller's UoW.
type CatalogMutationEnqueuer interface {
	EnqueueCatalogMutation(context.Context, CatalogMutationIntent, string) (CatalogMutationEffectReceipt, error)
}

type CatalogMutationEffectReceipt struct {
	EffectID, QueueJobID                                    int64
	EffectRef, EffectState, AcceptReceiptID, QueueReceiptID string
}

// CatalogMutationDispatch is read by Outbound only after EER recorded an
// attempted fact. SourceRefDigest identifies exactly one immutable intent;
// it must not be reconstructed from a name or a new key.
type CatalogMutationDispatch struct {
	CatalogMutationIntent
	EffectRef, SourceRefDigest string
}

type CatalogMutationCompletion struct {
	EffectRef, State, ResultDigest string
	ProviderGroupID, ProviderTagID string
	ReadbackAt                     *time.Time
	Attempt                        int32
	Generation, Fence              int64
	CompletedAt                    time.Time
}

// ArchiveMutationOperation is a durable record for a locally archived catalog
// item whose provider outcome still requires attention. It deliberately
// contains local IDs only; provider identifiers remain Tag-owned internals.
type ArchiveMutationOperation struct {
	Operation  CatalogMutationOperation
	LocalID    int64
	State      string
	RecordedAt time.Time
	ReadbackAt *time.Time
}

// ArchiveMutationOperationReader exposes the small visible recovery surface
// needed after archive hides a catalog row. It has no provider writer or retry
// capability and is implemented only by the Tag-owned store.
type ArchiveMutationOperationReader interface {
	ListArchiveMutationOperations(context.Context, int) ([]ArchiveMutationOperation, error)
}
