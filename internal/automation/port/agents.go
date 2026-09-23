// Package port defines the Automation-owned configuration-only Agent contract.
package port

import (
	"context"
	"encoding/json"
	"time"
)

type AgentID int64
type AutomationType string

const (
	AutomationTypeAgent       AutomationType = "agent"
	AutomationTypeFixedScript AutomationType = "fixed_script"
)

type AgentStatus string

const (
	AgentStatusActive   AgentStatus = "active"
	AgentStatusPaused   AgentStatus = "paused"
	AgentStatusArchived AgentStatus = "archived"
)

// FixedContentPackage stores immutable references only; it does not upload,
// generate, enqueue, or send anything.
type FixedContentPackage struct {
	ContentText            string          `json:"content_text"`
	ImageLibraryIDs        []int64         `json:"image_library_ids"`
	MiniprogramLibraryIDs  []int64         `json:"miniprogram_library_ids"`
	AttachmentLibraryIDs   []int64         `json:"attachment_library_ids"`
	GroupInviteLibraryIDs  []int64         `json:"group_invite_library_ids"`
	DynamicMiniprogramCard json.RawMessage `json:"dynamic_miniprogram_card,omitempty"`
}

// ImageReferenceReader is the Automation-owned read-only answer to whether a
// fixed content package references one Media image. It returns only local
// Automation agent IDs in ascending order.
type ImageReferenceReader interface {
	ListImageReferenceAgentIDs(context.Context, int64) ([]int64, error)
}

// AttachmentReferenceReader is the Automation-owned read-only answer to
// whether a fixed content package references one private attachment.
type AttachmentReferenceReader interface {
	ListAttachmentReferenceAgentIDs(context.Context, int64) ([]int64, error)
}

type Agent struct {
	ID                  AgentID             `json:"id"`
	AgentName           string              `json:"agent_name"`
	AgentCode           string              `json:"agent_code"`
	AutomationType      AutomationType      `json:"automation_type"`
	Status              AgentStatus         `json:"status"`
	ExecutionEnabled    bool                `json:"execution_enabled"`
	DraftRolePrompt     string              `json:"draft_role_prompt"`
	DraftTaskPrompt     string              `json:"draft_task_prompt"`
	PublishedRolePrompt string              `json:"published_role_prompt"`
	PublishedTaskPrompt string              `json:"published_task_prompt"`
	DraftVersion        int64               `json:"draft_version"`
	PublishedVersion    int64               `json:"published_version"`
	FixedContentPackage FixedContentPackage `json:"fixed_content_package"`
	LegacyConfiguration json.RawMessage     `json:"legacy_configuration"`
	CreatedBy           int64               `json:"created_by"`
	UpdatedBy           int64               `json:"updated_by"`
	CreatedAt           time.Time           `json:"created_at"`
	UpdatedAt           time.Time           `json:"updated_at"`
}

type Page struct {
	Items []Agent `json:"items"`
	Total int64   `json:"total"`
}
type CreateCommand struct {
	Agent          Agent
	Actor          int64
	IdempotencyKey string
}

// UpdateCommand does not include AgentCode: it is immutable after creation.
type UpdateCommand struct {
	ID                  AgentID
	AgentName           *string
	AutomationType      *AutomationType
	Status              *AgentStatus
	RolePrompt          *string
	TaskPrompt          *string
	FixedContentPackage *FixedContentPackage
	LegacyConfiguration *json.RawMessage
	Actor               int64
	IdempotencyKey      string
}
type MutationCommand struct {
	ID             AgentID
	Actor          int64
	IdempotencyKey string
}
type FixedContentCommand struct {
	ID             AgentID
	ContentPackage FixedContentPackage
	Actor          int64
	IdempotencyKey string
}

type AgentService interface {
	List(context.Context, AutomationType) (Page, error)
	Get(context.Context, AgentID) (Agent, error)
	Create(context.Context, CreateCommand) (Agent, error)
	Update(context.Context, UpdateCommand) (Agent, error)
	Copy(context.Context, MutationCommand) (Agent, error)
	Publish(context.Context, MutationCommand) (Agent, error)
	SetStatus(context.Context, MutationCommand, AgentStatus) (Agent, error)
	SaveFixedContent(context.Context, FixedContentCommand) (Agent, error)
}
