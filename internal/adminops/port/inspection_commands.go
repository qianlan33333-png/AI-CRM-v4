package port

import (
	"context"
	"time"
)

// ManualInspectionAcceptance proves durable acceptance only. It does not
// claim a finished inspection, even when a previous request is replayed.
type ManualInspectionAcceptance struct {
	State      string    `json:"state"`
	JobID      int64     `json:"job_id"`
	AcceptedAt time.Time `json:"accepted_at"`
	Replay     bool      `json:"replay"`
}

// ManualInspectionCommand is the actor-bound permanent completion evidence.
// Completion does not imply healthy checks, and its run details may expire.
type ManualInspectionCommand struct {
	State       string     `json:"state"`
	JobID       int64      `json:"job_id"`
	AcceptedAt  time.Time  `json:"accepted_at"`
	RunID       *int64     `json:"run_id,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

type ManualInspectionEnqueuer interface {
	EnqueueManualInspection(context.Context, int64, string) (ManualInspectionAcceptance, error)
}
