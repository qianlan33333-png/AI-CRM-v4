package port

import (
	"context"
	"encoding/json"
	"errors"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	"time"
)

type CheckDefinition struct {
	ID     string        `json:"id"`
	Owner  string        `json:"owner"`
	Title  string        `json:"title"`
	Scope  string        `json:"scope"`
	MaxAge time.Duration `json:"-"`
}

// CheckObservation is a bounded, non-PII Owner observation. Codes and metric
// names are machine identifiers, never error messages or arbitrary labels.
type CheckObservation struct {
	Status     string           `json:"status"`
	Code       string           `json:"code"`
	ObservedAt time.Time        `json:"observed_at"`
	Metrics    map[string]int64 `json:"metrics"`
}
type InspectionCollector interface {
	CheckID() string
	Collect(context.Context, time.Time) (CheckObservation, error)
}
type CollectorFunc struct {
	ID   string
	Read func(context.Context, time.Time) (CheckObservation, error)
}

func (c CollectorFunc) CheckID() string { return c.ID }
func (c CollectorFunc) Collect(ctx context.Context, at time.Time) (CheckObservation, error) {
	return c.Read(ctx, at)
}

type CheckResult struct {
	CheckDefinition
	CheckObservation
	RunID int64 `json:"run_id"`
}
type InspectionRun struct {
	ID          int64         `json:"id"`
	RequestKey  string        `json:"-"`
	Slot        time.Time     `json:"slot"`
	State       string        `json:"state"`
	ReleaseSHA  string        `json:"release_sha"`
	StartedAt   time.Time     `json:"started_at"`
	CompletedAt *time.Time    `json:"completed_at,omitempty"`
	Results     []CheckResult `json:"results"`
}
type InspectionIssue struct {
	ID          int64      `json:"id"`
	CheckID     string     `json:"check_id"`
	Code        string     `json:"code"`
	Status      string     `json:"status"`
	Severity    string     `json:"severity"`
	Version     int64      `json:"version"`
	FirstSeen   time.Time  `json:"first_seen"`
	LastSeen    time.Time  `json:"last_seen"`
	ResolvedAt  *time.Time `json:"resolved_at,omitempty"`
	Occurrences int64      `json:"occurrences"`
}
type InspectionOverview struct {
	ObservedAt time.Time         `json:"observed_at"`
	Fresh      bool              `json:"fresh"`
	Latest     *InspectionRun    `json:"latest,omitempty"`
	Checks     []CheckResult     `json:"checks"`
	Issues     []InspectionIssue `json:"issues"`
}

// ErrOpsReportPayloadExpired is terminal: its immutable acceptance remains,
// but the disposable report body is outside the 720-hour delivery/read window.
var ErrOpsReportPayloadExpired = errors.New("ops report payload expired")

type OpsReport struct {
	NotificationKind string          `json:"notification_kind"`
	EventKey         string          `json:"event_key"`
	ID               int64           `json:"id"`
	HourKey          time.Time       `json:"hour_key"`
	Content          json.RawMessage `json:"content"`
	EffectID         string          `json:"effect_id,omitempty"`
	EffectState      string          `json:"effect_state"`
	CreatedAt        time.Time       `json:"created_at"`
}
type OpsReportPayload struct {
	SourceDigest     effectport.Digest
	PolicyDigest     effectport.Digest
	NotificationKind string
	EventKey         string
	ReportID         int64
	HourKey          time.Time
	TargetRef        string
	Content          json.RawMessage
	PayloadDigest    effectport.Digest
}
type OpsReportReader interface {
	LoadOpsReportPayload(context.Context, effectport.Envelope) (OpsReportPayload, error)
}
type OpsReportObserver interface {
	ObserveOpsReportWithin(context.Context, effectport.Projection) error
}

// DiagnosticObservation accepts classified errors and static route templates.
// Correlation is digested before storage; it must not be an error or URL.
type DiagnosticObservation struct {
	Component, Code, Correlation, RouteTemplate, JobRef, EffectRef string
	// JobAttempt is River's attempt; zero means no captured attempt evidence.
	JobAttempt int
}
