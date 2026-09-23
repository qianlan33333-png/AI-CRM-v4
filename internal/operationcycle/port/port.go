// Package port exposes only local operation-cycle facts.  It cannot send a
// message, invoke a provider, or create an external-effect retry.
package port

import (
	"context"
	"encoding/json"
	"time"
)

const (
	StatusQueued             = "queued"
	StatusClaimed            = "claimed"
	StatusThreadBound        = "thread_bound"
	StatusTurnStarted        = "turn_started"
	StatusCompleted          = "completed"
	StatusFailed             = "failed"
	StrategyPageMaximumLimit = int32(100)
	// PostgreSQL OFFSET is supplied by the existing int32 strategy-page Port.
	// Keep the bound at that representation limit, rather than presenting a
	// smaller page ceiling that a returned next_offset can cross.
	StrategyPageMaximumOffset = int32(2147483647)
)

type Strategy struct {
	Key        string
	Title      string
	Status     string
	Version    int
	Definition json.RawMessage
	Snapshot   json.RawMessage
	UpdatedAt  time.Time
}

// StrategyPage is a bounded, local read projection. Consumers use it to
// present operation-cycle configuration and must not infer provider or
// execution state from its contents.
type StrategyPage struct {
	Items  []Strategy
	Total  int
	Limit  int32
	Offset int32
}

// StrategyPageReader is the stable read boundary for a page of operation
// strategies. It deliberately contains neither mutations nor provider work.
type StrategyPageReader interface {
	ListOperationCycleStrategies(context.Context, int32, int32) (StrategyPage, error)
}

// StrategyReader is the stable read boundary used by a coordinated domain to
// validate an opaque operation-cycle strategy key inside its already-bound
// PostgreSQL unit of work. It intentionally exposes no strategy mutation,
// run creation, Provider operation or table access.
type StrategyReader interface {
	OperationCycleStrategy(context.Context, string) (Strategy, error)
}

type Run struct {
	Key         string
	StrategyKey string
	Revision    int
	Snapshot    json.RawMessage
	ReceivedAt  time.Time
}

type Runner struct {
	ID                  string
	PrincipalID         string
	ConnectorVersion    string
	CodexVersion        string
	CompatibilityStatus string
	BindingKeys         []string
	LastHeartbeatAt     time.Time
}

type ActionRequest struct {
	ID              string
	StrategyKey     string
	RunKey          string
	ActionKey       string
	ActionTitle     string
	StrategyVersion int
	RunnerID        string
	Status          string
	ParentRequestID string
	ThreadID        string
	TurnID          string
	FinalResult     json.RawMessage
	FailureCode     string
	CreatedBy       string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	CompletedAt     *time.Time
}

type Proposal struct {
	ID                  string
	StrategyKey         string
	BaseStrategyVersion int
	Status              string
	Payload             json.RawMessage
	CreatedBy           string
	DecidedBy           string
	CreatedAt           time.Time
	DecidedAt           *time.Time
}
