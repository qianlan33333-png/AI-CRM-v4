package port

import (
	"context"
	"encoding/json"
	"time"
)

// CoreProduct is a stable operating direction, independent of a sales SKU.
type CoreProduct struct {
	ID               int64     `json:"id"`
	PackageID        int64     `json:"package_id"`
	Name             string    `json:"name"`
	Description      string    `json:"description"`
	AIContext        string    `json:"ai_context"`
	ProductReference string    `json:"product_reference"`
	Enabled          bool      `json:"enabled"`
	Version          int64     `json:"version"`
	UpdatedAt        time.Time `json:"updated_at"`
}
type CorePrompt struct {
	Draft         string `json:"draft"`
	PublishedID   int64  `json:"published_id"`
	PublishedBody string `json:"published_body"`
	Version       int64  `json:"version"`
}
type CorePromptVersion struct {
	ID        int64     `json:"id"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}
type CoreAssignment struct {
	ID            int64      `json:"id"`
	CustomerID    int64      `json:"customer_id"`
	CoreProductID int64      `json:"core_product_id"`
	PackageID     int64      `json:"package_id"`
	Source        string     `json:"source"`
	Reason        string     `json:"reason"`
	Evidence      string     `json:"evidence"`
	PromptVersion int64      `json:"prompt_version"`
	EnteredAt     time.Time  `json:"entered_at"`
	EndedAt       *time.Time `json:"ended_at,omitempty"`
	EndReason     string     `json:"end_reason"`
}
type CoreMaterialRef struct {
	Kind string `json:"kind"`
	ID   int64  `json:"id"`
}
type CorePush struct {
	ID            int64             `json:"id"`
	Source        string            `json:"source"`
	PushID        string            `json:"push_id"`
	CustomerID    int64             `json:"customer_id"`
	PackageID     int64             `json:"package_id"`
	AssignmentID  int64             `json:"assignment_id"`
	Materials     []CoreMaterialRef `json:"materials"`
	OccurredAt    time.Time         `json:"occurred_at"`
	Status        string            `json:"status"`
	StatusVersion int64             `json:"status_version"`
}
type CoreMemberStats struct {
	LastEvaluationAt *time.Time `json:"last_evaluation_at,omitempty"`
	PushCount        int64      `json:"push_count"`
	LastPushAt       *time.Time `json:"last_push_at,omitempty"`
	LastPushStatus   string     `json:"last_push_status"`
	// Null/unknown is deliberately different from zero until R3 is reconciled.
	VisitCount *int64 `json:"visit_count"`
	VisitState string `json:"visit_state"`
}
type CoreAssignmentPage struct {
	Items      []CoreAssignment `json:"items"`
	NextCursor string           `json:"next_cursor"`
}
type CoreMemberDetail struct {
	AssignmentNextCursor string           `json:"assignment_next_cursor"`
	Assignments          []CoreAssignment `json:"assignments"`
	Pushes               []CorePush       `json:"pushes"`
	Stats                CoreMemberStats  `json:"stats"`
	NextCursor           string           `json:"next_cursor"`
}

type CoreRecommendation struct {
	ID              int64           `json:"id"`
	CustomerID      int64           `json:"customer_id"`
	ExpectedEpoch   int64           `json:"-"`
	ActorID         int64           `json:"-"`
	Preview         bool            `json:"preview"`
	PromptVersion   int64           `json:"prompt_version"`
	Products        []CoreProduct   `json:"products"`
	Dispatch        json.RawMessage `json:"-"`
	EffectID        string          `json:"-"`
	State           string          `json:"state"`
	FailureCode     string          `json:"failure_code,omitempty"`
	Reason          string          `json:"reason"`
	Evidence        string          `json:"evidence"`
	ChosenProductID int64           `json:"chosen_product_id"`
	CreatedAt       time.Time       `json:"created_at"`
	CompletedAt     *time.Time      `json:"completed_at,omitempty"`
}

// CoreSupervision accepts a source from the authenticated machine principal.
// It never authenticates a caller itself and cannot trigger message delivery.
type CoreSupervision interface {
	RecordSupervisedPush(context.Context, string, string, CorePush) (CorePush, error)
}

// CoreOperationsReader exposes the existing audience snapshot and its CRM-owned
// operational fields. Authentication and owner scoping remain the API's job.
type CoreOperationsReader interface {
	Products(context.Context) ([]CoreProduct, error)
	CoreMembers(context.Context, int64, string, int) (MemberPage, error)
	MemberHistory(context.Context, int64, int64, string, int) (CoreAssignmentPage, error)
	MemberDetail(context.Context, int64, int64, string, int) (CoreMemberDetail, error)
}
