package port

import (
	"context"
	"encoding/json"
	"time"
)

type SidebarSendCommand struct {
	CustomerID     int64
	EmployeeID     string
	ResourceKind   string
	ResourceID     string
	ContentDigest  [32]byte
	Payload        json.RawMessage
	IdempotencyKey string
}

type SidebarSendAcceptance struct {
	IntentID       int64           `json:"intent_id"`
	EffectID       string          `json:"effect_id"`
	State          string          `json:"state"`
	Grant          string          `json:"grant,omitempty"`
	GrantExpiresAt time.Time       `json:"grant_expires_at,omitempty"`
	Payload        json.RawMessage `json:"payload"`
	Replayed       bool            `json:"replayed"`
}

type SidebarSendOutcomeCommand struct {
	IntentID       int64
	CustomerID     int64
	EmployeeID     string
	Grant          string
	Outcome        string
	EvidenceDigest [32]byte
}

type SidebarSendAccepter interface {
	AcceptSidebarSend(context.Context, SidebarSendCommand) (SidebarSendAcceptance, error)
	CompleteSidebarSend(context.Context, SidebarSendOutcomeCommand) (SidebarSendAcceptance, error)
}
