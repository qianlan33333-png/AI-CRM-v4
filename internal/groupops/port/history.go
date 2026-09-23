package port

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"time"
)

var (
	ErrHistoryInvalid     = errors.New("invalid Group Ops history")
	ErrHistoryConflict    = errors.New("Group Ops history conflict")
	ErrHistoryUnavailable = errors.New("Group Ops history unavailable")
)

type HistoricalPlan struct {
	PlanID         int64      `json:"plan_id,string"`
	Name           string     `json:"name"`
	Status         PlanStatus `json:"status"`
	Revision       int64      `json:"revision"`
	CreatedBy      *int64     `json:"created_by"`
	UpdatedBy      *int64     `json:"updated_by"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	SourcePlanID   int64      `json:"source_plan_id"`
	SourceCode     string     `json:"source_code"`
	PlanType       string     `json:"plan_type"`
	OriginalStatus string     `json:"original_status"`
	OwnerStaffID   *int64     `json:"owner_staff_id"`
	ArchivedAt     *time.Time `json:"archived_at"`
	// Source references remain owner-only archival facts. The frozen page has
	// no action that needs them, so they never become current Access subjects.
	SourceCreatedByReference *string `json:"-"`
	SourceUpdatedByReference *string `json:"-"`
	SourceOwnerReference     *string `json:"-"`
}

type HistoricalDirectory struct {
	ID                   int64     `json:"id"`
	SourceKind           string    `json:"source_kind"`
	SourceID             *int64    `json:"source_id"`
	ChatReference        string    `json:"chat_reference"`
	DisplayName          *string   `json:"display_name"`
	OwnerStaffID         *int64    `json:"owner_staff_id"`
	OwnerName            *string   `json:"owner_name"`
	MemberCount          *int32    `json:"member_count"`
	InternalMemberCount  *int32    `json:"internal_member_count"`
	ExternalMemberCount  *int32    `json:"external_member_count"`
	OriginalStatus       string    `json:"original_status"`
	RecordedAt           time.Time `json:"recorded_at"`
	SourceOwnerReference *string   `json:"-"`
}

type HistoricalGroup struct {
	ID                   int64      `json:"id"`
	SourceGroupID        int64      `json:"source_group_id"`
	SourcePlanID         int64      `json:"source_plan_id"`
	PlanID               int64      `json:"plan_id,string"`
	ChatReference        string     `json:"chat_reference"`
	DisplayName          string     `json:"display_name"`
	OwnerStaffID         *int64     `json:"owner_staff_id"`
	InternalMemberCount  int32      `json:"internal_member_count"`
	ExternalMemberCount  int32      `json:"external_member_count"`
	OriginalStatus       string     `json:"original_status"`
	CreatedAt            time.Time  `json:"created_at"`
	RemovedAt            *time.Time `json:"removed_at"`
	SourceOwnerReference *string    `json:"-"`
}

type HistoricalNode struct {
	ID             int64           `json:"id"`
	SourceNodeID   int64           `json:"source_node_id"`
	SourcePlanID   int64           `json:"source_plan_id"`
	PlanID         int64           `json:"plan_id,string"`
	DayIndex       int32           `json:"day_index"`
	TriggerTime    string          `json:"trigger_time"`
	SortOrder      int32           `json:"sort_order"`
	OriginalStatus string          `json:"original_status"`
	ActionTitle    string          `json:"action_title"`
	TextContent    string          `json:"text_content"`
	Attachments    json.RawMessage `json:"attachments"`
	ContentPackage json.RawMessage `json:"content_package"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

type HistoricalReceipt struct {
	SourceIdentifier string
	PayloadDigest    [sha256.Size]byte
	TargetID         int64
	TargetDigest     [sha256.Size]byte
	Replayed         bool
}

type HistoricalStore interface {
	CreateHistoricalPlan(context.Context, HistoricalPlan) (HistoricalPlan, error)
	GetHistoricalPlan(context.Context, int64) (HistoricalPlan, error)
	CreateHistoricalDirectory(context.Context, HistoricalDirectory) (HistoricalDirectory, error)
	GetHistoricalDirectory(context.Context, int64) (HistoricalDirectory, error)
	CreateHistoricalGroup(context.Context, HistoricalGroup) (HistoricalGroup, error)
	GetHistoricalGroup(context.Context, int64) (HistoricalGroup, error)
	CreateHistoricalNode(context.Context, HistoricalNode) (HistoricalNode, error)
	GetHistoricalNode(context.Context, int64) (HistoricalNode, error)
}

type HistoricalJournal interface {
	LoadGroupOpsHistory(context.Context, string, string) (HistoricalReceipt, bool, error)
	RecordGroupOpsHistory(context.Context, string, HistoricalReceipt) error
}

// HistoricalImportRecord is a sealed V1 fact prepared by the one existing
// configuration-import command. It never represents a current plan, Access
// principal, execution, River job, or Provider effect.
type HistoricalImportRecord struct {
	SourceKind       string
	SourceKey        string
	SourceDigest     [sha256.Size]byte
	Plan             *HistoricalPlan
	Directory        *HistoricalDirectory
	Group            *HistoricalGroup
	Node             *HistoricalNode
	QuarantineReason string
}

type HistoricalImportBatch struct {
	SourceSystem   string
	SourceRevision string
	SnapshotDigest [sha256.Size]byte
	Manifest       json.RawMessage
}

type HistoricalImportResult struct {
	BatchID     int64
	NoOp        bool
	Imported    int64
	Quarantined int64
}

// HistoricalImporter is the Group Ops owner write port for its four sealed
// V1 history projections and their append-only source ledger.
type HistoricalImporter interface {
	PreflightHistoricalImport(context.Context, HistoricalImportBatch) error
	ApplyHistoricalImport(context.Context, HistoricalImportBatch, []HistoricalImportRecord) (HistoricalImportResult, error)
	VerifyHistoricalImport(context.Context, HistoricalImportBatch, []HistoricalImportRecord) (HistoricalImportResult, error)
}

type HistoricalReader interface {
	ListHistoricalPlans(context.Context, int32, int32) ([]HistoricalPlan, int64, error)
	ListHistoricalDirectory(context.Context, int32, int32) ([]HistoricalDirectory, int64, error)
	ListHistoricalGroups(context.Context, int64, int32, int32) ([]HistoricalGroup, int64, error)
	ListHistoricalNodes(context.Context, int64, int32, int32) ([]HistoricalNode, int64, error)
}

// Historical pages deliberately do not embed Safety. These are sealed source
// records: the donor contract distinguishes them from current Group Ops plans
// and only permits read-only observation. They can never enter runtime or
// Provider execution paths.
type HistoricalPlanPage struct {
	Source                   string           `json:"source"`
	ReadOnly                 bool             `json:"read_only"`
	RealExternalCallExecuted bool             `json:"real_external_call_executed"`
	Items                    []HistoricalPlan `json:"items"`
	Total                    int64            `json:"total"`
	Limit                    int32            `json:"limit"`
	Offset                   int32            `json:"offset"`
}

type HistoricalDirectoryPage struct {
	Source                   string                `json:"source"`
	ReadOnly                 bool                  `json:"read_only"`
	RealExternalCallExecuted bool                  `json:"real_external_call_executed"`
	Items                    []HistoricalDirectory `json:"items"`
	Total                    int64                 `json:"total"`
	Limit                    int32                 `json:"limit"`
	Offset                   int32                 `json:"offset"`
}

type HistoricalGroupPage struct {
	Source                   string            `json:"source"`
	ReadOnly                 bool              `json:"read_only"`
	RealExternalCallExecuted bool              `json:"real_external_call_executed"`
	Items                    []HistoricalGroup `json:"items"`
	Total                    int64             `json:"total"`
	Limit                    int32             `json:"limit"`
	Offset                   int32             `json:"offset"`
	PlanID                   int64             `json:"plan_id,string"`
}

type HistoricalNodePage struct {
	Source                   string           `json:"source"`
	ReadOnly                 bool             `json:"read_only"`
	RealExternalCallExecuted bool             `json:"real_external_call_executed"`
	Items                    []HistoricalNode `json:"items"`
	Total                    int64            `json:"total"`
	Limit                    int32            `json:"limit"`
	Offset                   int32            `json:"offset"`
	PlanID                   int64            `json:"plan_id,string"`
}
