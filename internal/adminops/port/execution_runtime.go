package port

import (
	"context"
	"time"
)

// RuntimeObservation is an observed local runtime state. It is deliberately
// not a delivery receipt and never establishes that a provider action ran or
// succeeded.
type RuntimeObservation struct {
	Source     string
	Queue      string
	Status     string
	Attempt    int
	StatusURL  string
	Details    map[string]string
	ObservedAt time.Time
}

// RuntimeControl describes the local control-plane availability. A nil
// control in RuntimeSnapshot is a normal, observable absence rather than a
// failed runtime read.
type RuntimeControl struct {
	Name       string
	State      string
	Details    map[string]string
	ObservedAt time.Time
}

type RuntimeSnapshot struct {
	Control      *RuntimeControl
	Observations []RuntimeObservation
	ObservedAt   time.Time
}

type ExecutionGraphNode struct {
	ID         string
	Kind       string
	Status     string
	Message    string
	Details    map[string]string
	ObservedAt time.Time
	Children   []ExecutionGraphNode
}

type ExecutionGraph struct {
	Roots     []ExecutionGraphNode
	Items     []RuntimeObservation
	Truncated bool
}

type ExecutionTimeline struct {
	ExecutionID string
	Graph       ExecutionGraph
	ObservedAt  time.Time
}

// ExecutionRuntimeReader is a read-only seam for the already-owned Channel
// Entry, Group Ops, and WeCom media-status readers. Implementations must not
// write data or contact a provider while serving either method.
type ExecutionRuntimeReader interface {
	ReadExecutionRuntime(context.Context) (RuntimeSnapshot, error)
	ReadExecutionTimeline(context.Context, string) (ExecutionTimeline, bool, error)
}
