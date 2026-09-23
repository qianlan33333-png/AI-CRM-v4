// Package port defines the release-plane boundary. It accepts local
// attestations only; it never invokes deployment, backup, payment, or provider
// systems.
package port

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

var (
	ErrConflict    = errors.New("release fact conflict")
	ErrNotFound    = errors.New("release fact not found")
	ErrUnavailable = errors.New("release store unavailable")
)

type CandidateState string

const (
	CandidateDraft         CandidateState = "draft"
	CandidatePrepared      CandidateState = "prepared"
	CandidateCutoverActive CandidateState = "cutover_active"
	CandidateActivated     CandidateState = "activated"
	// CandidateRollbackPending means rollback was requested and the plane is
	// waiting for reconciliation evidence from the external execution. It does
	// not claim that this local module executed a rollback.
	CandidateRollbackPending CandidateState = "rollback_pending"
	CandidateRolledBack      CandidateState = "rolled_back"
)

type PrerequisiteKind string

const (
	PrerequisiteNightly            PrerequisiteKind = "nightly"
	PrerequisiteBackupRestoreDrill PrerequisiteKind = "backup_restore_drill"
	PrerequisiteMigration          PrerequisiteKind = "migration"
	PrerequisiteContactClosure     PrerequisiteKind = "contact_closure"
	PrerequisiteCampaignClosure    PrerequisiteKind = "campaign_closure"
	PrerequisiteOutboundClosure    PrerequisiteKind = "outbound_closure"
	PrerequisiteCommerceClosure    PrerequisiteKind = "commerce_closure"
)

type CutoverStep string

const (
	CutoverAnnounce     CutoverStep = "announce"
	CutoverQuiesce      CutoverStep = "quiesce"
	CutoverSchemaVerify CutoverStep = "schema_verify"
	CutoverSwitch       CutoverStep = "switch"
	CutoverVerify       CutoverStep = "verify"
)

var FixedCutoverSteps = []CutoverStep{
	CutoverAnnounce,
	CutoverQuiesce,
	CutoverSchemaVerify,
	CutoverSwitch,
	CutoverVerify,
}

type RollbackCheckKind string

const (
	RollbackSchemaCompatibility     RollbackCheckKind = "schema_compatibility"
	RollbackDataReconciliation      RollbackCheckKind = "data_reconciliation"
	RollbackOutboundReconciliation  RollbackCheckKind = "outbound_reconciliation"
	RollbackExecutionReconciliation RollbackCheckKind = "rollback_execution_reconciliation"
)

type Candidate struct {
	ID                  int64
	CommitSHA           string
	ArtifactDigest      string
	ManifestDigest      string
	ConfigDigest        string
	TargetSchemaVersion int64
	State               CandidateState
	CreatedBy           int64
	CreatedAt           time.Time
	PreparedAt          *time.Time
	ActivatedAt         *time.Time
	RollbackRequestedAt *time.Time
	RolledBackAt        *time.Time
}

type PrerequisiteReceipt struct {
	ID                      int64
	CandidateID             int64
	CandidateCommitSHA      string
	CandidateArtifactDigest string
	CandidateManifestDigest string
	CandidateConfigDigest   string
	CandidateSchemaVersion  int64
	Kind                    PrerequisiteKind
	EvidenceSHA             string
	RecordedBy              int64
	RecordedAt              time.Time
}

type Readiness struct {
	CandidateID int64
	Ready       bool
	Missing     []PrerequisiteKind
	Invalid     []PrerequisiteKind
	CheckedAt   time.Time
}

type WorkerLease struct {
	CandidateID int64
	Generation  int64
	Fence       string
	StartedBy   int64
	StartedAt   time.Time
	Active      bool
	RetiredAt   *time.Time
}

type CutoverJournalEntry struct {
	ID          int64
	CandidateID int64
	Generation  int64
	Step        CutoverStep
	Fence       string
	CompletedBy int64
	CompletedAt time.Time
}

// CutoverProgressEntry is the ordinary read projection. Fence remains only in
// worker management commands and is deliberately absent here.
type CutoverProgressEntry struct {
	ID          int64
	CandidateID int64
	Generation  int64
	Step        CutoverStep
	CompletedBy int64
	CompletedAt time.Time
}

type WorkerSummary struct {
	CandidateID int64
	Generation  int64
	StartedBy   int64
	StartedAt   time.Time
}

type RollbackCheck struct {
	ID          int64
	CandidateID int64
	Kind        RollbackCheckKind
	Passed      bool
	EvidenceSHA string
	RecordedBy  int64
	RecordedAt  time.Time
}

type RollbackEligibility struct {
	CandidateID int64
	Eligible    bool
	Missing     []RollbackCheckKind
	Blocked     []RollbackCheckKind
	CheckedAt   time.Time
}

type Detail struct {
	Candidate           Candidate
	Prerequisites       []PrerequisiteReceipt
	Readiness           Readiness
	CutoverProgress     []CutoverProgressEntry
	RollbackChecks      []RollbackCheck
	RollbackEligibility RollbackEligibility
	ActiveWorker        *WorkerSummary
}

type OperationReceipt struct {
	ID            int64
	Action        string
	ActorID       int64
	KeyDigest     string
	PayloadDigest string
	State         string
	Result        json.RawMessage
	CreatedAt     time.Time
	CompletedAt   *time.Time
}

type Repository interface {
	CreateCandidate(context.Context, Candidate) (Candidate, error)
	GetCandidate(context.Context, int64) (Candidate, error)
	LockCandidate(context.Context, int64) (Candidate, error)
	ListCandidates(context.Context, int32) ([]Candidate, error)
	TransitionCandidate(context.Context, int64, CandidateState, CandidateState, time.Time) (Candidate, error)

	CreatePrerequisite(context.Context, PrerequisiteReceipt) (PrerequisiteReceipt, error)
	ListPrerequisites(context.Context, int64) ([]PrerequisiteReceipt, error)

	StartWorker(context.Context, WorkerLease) (WorkerLease, error)
	GetActiveWorker(context.Context, int64) (WorkerLease, error)
	FindActiveWorkerSummary(context.Context, int64) (*WorkerSummary, error)
	RetireWorker(context.Context, int64, int64, string, time.Time) error
	AppendCutoverStep(context.Context, CutoverJournalEntry) (CutoverJournalEntry, error)
	ListCutoverSteps(context.Context, int64) ([]CutoverJournalEntry, error)

	CreateRollbackCheck(context.Context, RollbackCheck) (RollbackCheck, error)
	ListRollbackChecks(context.Context, int64) ([]RollbackCheck, error)

	ReserveOperationReceipt(context.Context, OperationReceipt) (OperationReceipt, bool, error)
	CompleteOperationReceipt(context.Context, int64, json.RawMessage, time.Time) (OperationReceipt, error)
}
