package port

import (
	"context"
	"errors"
	"time"
)

var (
	ErrMaterialSourceChanged       = errors.New("material source snapshot changed")
	ErrInvalidMaterialRequest      = errors.New("invalid material request")
	ErrMaterialOperationConflict   = errors.New("material operation conflict")
	ErrMaterialPreparationNotFound = errors.New("material preparation not found")
)

type MediaPreparationPendingError struct {
	Code  string
	After time.Duration
}

func (e MediaPreparationPendingError) Error() string             { return "media preparation pending" }
func (e MediaPreparationPendingError) FailureCode() string       { return e.Code }
func (e MediaPreparationPendingError) RetryAfter() time.Duration { return e.After }

// MediaPreparationTerminalError means the accepted upload cannot safely make
// progress automatically. A containing message effect may enter its local
// failure projection without making the message Provider call. outcome_unknown
// remains blocked until reconciliation and must never be converted to pending.
type MediaPreparationTerminalError struct {
	Code  string
	State string
}

func (e MediaPreparationTerminalError) Error() string {
	return "media preparation requires reconciliation"
}
func (e MediaPreparationTerminalError) FailureCode() string { return e.Code }

type MaterialSourceSnapshot struct {
	SourceRef       string
	SourceType      string
	ContentDigest   [32]byte
	FileName        string
	MediaType       string
	SizeBytes       int64
	SnapshotVersion int64
}

type MaterialSnapshotPageRequest struct {
	CorpScopeDigest string
	Cursor          string
	Limit           int
}

type MaterialSnapshotPage struct {
	Items []MaterialSourceSnapshot
	// Failures are source-owned scan facts that cannot produce an uploadable
	// snapshot. A refresh records them and continues the page.
	Failures   []MaterialSourceFailure
	NextCursor string
	Done       bool
}

type MaterialSourceFailure struct {
	SourceRef   string
	FailureCode string
}

type MaterialSourceContent struct {
	Bytes               []byte
	FileName, MediaType string
}

// MaterialSourceReader is implemented by Media through composition. Reads
// require the frozen digest and version, so a changed source cannot be uploaded
// under an already accepted effect.
type MaterialSourceReader interface {
	ListEnabledSourceSnapshots(context.Context, MaterialSnapshotPageRequest) (MaterialSnapshotPage, error)
	ReadSourceBytes(context.Context, MaterialSourceSnapshot) (MaterialSourceContent, error)
	GetSourceSnapshot(context.Context, string) (MaterialSourceSnapshot, error)
}

type MaterialRequest struct {
	MaterialSourceSnapshot
	CorpScopeDigest string
	ForceRefresh    bool
	RoundDate       string
	RefreshRoundID  int64
	ValidThrough    time.Time
	// OperationKey is required for an operator-forced refresh. Replaying the
	// same key returns the same preparation; a different key creates a new one.
	OperationKey string
	ActorAdminID int64
}

type MaterialResult struct {
	State, EffectID, MediaID string
	// State reports the latest preparation attempt. CredentialState reports
	// whether the last confirmed provider credential can be used now, even when
	// a later refresh failed or is still pending.
	CredentialState   string
	CredentialUsable  bool
	FailureCode       string
	SourceDigest      [32]byte
	ProviderCreatedAt time.Time
	ExpiresAt         time.Time
	LastSucceededAt   time.Time
	NextRefreshAt     time.Time
}

type MaterialPreparer interface {
	Prepare(context.Context, MaterialRequest) (MaterialResult, error)
	ReadyForSend(context.Context, MaterialRequest) (MaterialResult, error)
}

// MaterialPreparationAccepter is the atomic post-mutation seam used by Media.
// The caller must already hold its PostgreSQL Unit of Work after persisting the
// frozen source snapshot. The implementation only accepts durable EER work;
// it never performs the Provider upload in this transaction.
type MaterialPreparationAccepter interface {
	AcceptMaterialPreparationWithin(context.Context, MaterialRequest) (MaterialResult, error)
}

type MaterialStatusReader interface {
	GetMaterialStatus(context.Context, MaterialSourceSnapshot, string) (MaterialResult, error)
}

type MaterialRefreshCommand struct {
	LocalDate, OperationKey string
	Force                   bool
	ActorAdminID            int64
}

type MaterialRefreshRound struct {
	ID                                        int64
	LocalDate, State, Cursor, OperationKey    string
	ActorAdminID                              int64
	Total, Queued, Succeeded, Failed, Unknown int64
	StartedAt                                 time.Time
	CompletedAt                               *time.Time
}

type MaterialRefreshEnqueuer interface {
	EnqueueMaterialRefreshWithin(context.Context, int64) (int64, error)
}

type MaterialRefresher interface {
	RefreshAll(context.Context, MaterialRefreshCommand) (MaterialRefreshRound, error)
	GetRefreshRound(context.Context, int64) (MaterialRefreshRound, error)
	GetTodayRefreshRound(context.Context) (MaterialRefreshRound, bool, error)
}

type MaterialUploadReceipt struct {
	MediaID           string    `json:"media_id"`
	ProviderCreatedAt time.Time `json:"provider_created_at"`
}

type MaterialUploader interface {
	UploadMaterial(context.Context, MaterialSourceSnapshot, MaterialSourceContent, string) (MaterialUploadReceipt, bool, error)
}

type MaterialUploadError interface {
	error
	OutcomeUnknown() bool
	Retryable() bool
	FailureCode() string
}
