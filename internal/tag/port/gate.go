package port

import (
	"context"
	"encoding/json"
	"time"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/tag/domain"
)

// ExecutionStatus is an opaque local compatibility payload.  The tag app
// projects only the allowlisted fail-closed fields into domain.ExecutionGate;
// future/provider-specific fields are never exposed to the page.
type ExecutionStatus struct {
	Payload    json.RawMessage
	ObservedAt time.Time
}

// ExecutionStatusReader reads the tag-owned local status projection.  It is a
// read port only and has no credential, CorpID, or Provider-call capability.
type ExecutionStatusReader interface {
	ReadExecutionStatus(context.Context) (ExecutionStatus, error)
}

// ExecutionGateReader is the stable app boundary used by the gate route.
type ExecutionGateReader interface {
	Get(context.Context) (domain.ExecutionGate, error)
}
