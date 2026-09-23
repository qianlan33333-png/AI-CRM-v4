package domain

import (
	"time"

	accessport "github.com/qianlan33333-png/AI-CRM-v3/internal/access/port"
	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
)

type AutomationBinding struct {
	ID                    int64                         `json:"id"`
	PackageID             int64                         `json:"package_id"`
	Version               int64                         `json:"version"`
	AgentID               automationport.AgentID        `json:"agent_id"`
	AutomationType        automationport.AutomationType `json:"automation_type"`
	AgentPublishedVersion int64                         `json:"agent_published_version"`
	ContentDigest         [32]byte                      `json:"-"`
	MaterialsDigest       [32]byte                      `json:"-"`
	CreatedBy             int64                         `json:"created_by"`
	CreatedActorKind      string                        `json:"created_actor_kind"`
	CreatedActorRef       string                        `json:"created_actor_ref"`
	CreatedAt             time.Time                     `json:"created_at"`
}
type SenderSet struct {
	ID               int64     `json:"id"`
	PackageID        int64     `json:"package_id"`
	Version          int64     `json:"version"`
	Members          []Sender  `json:"members"`
	CreatedBy        int64     `json:"created_by"`
	CreatedActorKind string    `json:"created_actor_kind"`
	CreatedActorRef  string    `json:"created_actor_ref"`
	CreatedAt        time.Time `json:"created_at"`
}
type Sender struct {
	SortOrder              int                `json:"sort_order"`
	StaffID                accessport.StaffID `json:"staff_id"`
	EligibilityVersion     int64              `json:"eligibility_version"`
	EligibilityRefreshedAt time.Time          `json:"eligibility_refreshed_at"`
}
