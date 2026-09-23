// Package domain contains pure operation-cycle invariants. It deliberately
// models no customer, segment, campaign, recipient, or Provider identity.
package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"

	operationport "github.com/qianlan33333-png/AI-CRM-v3/internal/operationcycle/port"
)

var (
	phoneNumber  = regexp.MustCompile(`(?:^|[^0-9])[1-9][0-9]{10}(?:$|[^0-9])`)
	emailAddress = regexp.MustCompile(`(?i)[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}`)
	cssColor     = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)
)

var (
	ErrInvalidStrategy = errors.New("invalid operation-cycle strategy")
	ErrInvalidRun      = errors.New("invalid operation-cycle run")
	ErrInvalidRunner   = errors.New("invalid operation-cycle runner")
	ErrInvalidAction   = errors.New("invalid operation-cycle action")
	ErrInvalidProposal = errors.New("invalid operation-cycle proposal")
	ErrInvalidScope    = errors.New("operation-cycle payload contains excluded scope")
	ErrInvalidStatus   = errors.New("invalid operation-cycle status transition")
)

const (
	StatusDraft    = "draft"
	StatusActive   = "active"
	StatusPaused   = "paused"
	StatusArchived = "archived"

	ActionQueued      = "queued"
	ActionClaimed     = "claimed"
	ActionThreadBound = "thread_bound"
	ActionTurnStarted = "turn_started"
	ActionCompleted   = "completed"
	ActionFailed      = "failed"

	ProposalPending  = "pending"
	ProposalAccepted = "accepted"
	ProposalRejected = "rejected"
)

// These aliases make the domain contracts available without forcing callers
// to depend on an adapter package for validation.
type Strategy = operationport.Strategy
type Run = operationport.Run
type Runner = operationport.Runner
type Stage = operationport.ActionRequest
type ActionRequest = operationport.ActionRequest
type Proposal = operationport.Proposal

func ValidateStrategy(value Strategy) error {
	if !ValidKey(value.Key, 120) || !ValidText(value.Title, 200) || !validStrategyStatus(value.Status) || value.Version < 1 ||
		!ValidJSON(value.Definition) || !ValidJSON(value.Snapshot) || value.UpdatedAt.IsZero() || ContainsForbidden(value.Definition) || ContainsForbidden(value.Snapshot) {
		return ErrInvalidStrategy
	}
	return nil
}

func ValidateRun(value Run) error {
	if !ValidKey(value.Key, 160) || !ValidKey(value.StrategyKey, 120) || value.Revision < 1 || !ValidJSON(value.Snapshot) || value.ReceivedAt.IsZero() || ContainsForbidden(value.Snapshot) {
		return ErrInvalidRun
	}
	return nil
}

func ValidateRunner(value Runner) error {
	if !ValidKey(value.ID, 160) || !ValidKey(value.PrincipalID, 240) || !ValidKey(value.ConnectorVersion, 120) ||
		!ValidKey(value.CodexVersion, 120) || !validCompatibilityStatus(value.CompatibilityStatus) || len(value.BindingKeys) > 32 || value.LastHeartbeatAt.IsZero() {
		return ErrInvalidRunner
	}
	for _, key := range value.BindingKeys {
		if !ValidKey(key, 240) {
			return ErrInvalidRunner
		}
	}
	return nil
}

func ValidateAction(value ActionRequest) error {
	if !ValidKey(value.ID, 160) || !ValidKey(value.StrategyKey, 120) || !ValidKey(value.RunKey, 160) ||
		!ValidKey(value.ActionKey, 120) || !ValidText(value.ActionTitle, 200) || value.StrategyVersion < 1 ||
		!ValidKey(value.RunnerID, 160) || !validActionStatus(value.Status) || !ValidKey(value.CreatedBy, 240) ||
		value.CreatedAt.IsZero() || value.UpdatedAt.IsZero() || ContainsForbidden(value.FinalResult) {
		return ErrInvalidAction
	}
	if value.ParentRequestID != "" && !ValidKey(value.ParentRequestID, 160) {
		return ErrInvalidAction
	}
	if value.ThreadID != "" && !ValidKey(value.ThreadID, 200) || value.TurnID != "" && !ValidKey(value.TurnID, 200) {
		return ErrInvalidAction
	}
	if value.Status == ActionCompleted || value.Status == ActionFailed {
		if value.CompletedAt == nil || value.CompletedAt.IsZero() {
			return ErrInvalidAction
		}
	} else if value.CompletedAt != nil {
		return ErrInvalidAction
	}
	return nil
}

func ValidateProposal(value Proposal) error {
	if !ValidKey(value.ID, 160) || !ValidKey(value.StrategyKey, 120) || value.BaseStrategyVersion < 1 ||
		!validProposalStatus(value.Status) || !ValidJSON(value.Payload) || !ValidKey(value.CreatedBy, 240) ||
		value.CreatedAt.IsZero() || ContainsForbidden(value.Payload) {
		return ErrInvalidProposal
	}
	if value.Status == ProposalPending {
		if value.DecidedBy != "" || value.DecidedAt != nil {
			return ErrInvalidProposal
		}
	} else if !ValidKey(value.DecidedBy, 240) || value.DecidedAt == nil || value.DecidedAt.IsZero() {
		return ErrInvalidProposal
	}
	return nil
}

// CanTransitionActionStatus is the local stage state machine. A failed stage
// can terminate from any active state; no transition performs external work.
func CanTransitionActionStatus(from, to string) bool {
	if from == to && validActionStatus(from) {
		return true
	}
	if !validActionStatus(from) || !validActionStatus(to) {
		return false
	}
	if to == ActionFailed {
		return from == ActionQueued || from == ActionClaimed || from == ActionThreadBound || from == ActionTurnStarted
	}
	switch from {
	case ActionQueued:
		return to == ActionClaimed
	case ActionClaimed:
		return to == ActionThreadBound
	case ActionThreadBound:
		return to == ActionTurnStarted
	case ActionTurnStarted:
		return to == ActionCompleted
	default:
		return false
	}
}

// CanTransitionStrategyStatus models enable/pause/archive state locally.
func CanTransitionStrategyStatus(from, to string) bool {
	if !validStrategyStatus(from) || !validStrategyStatus(to) || from == StatusArchived {
		return false
	}
	if from == to {
		return true
	}
	switch from {
	case StatusDraft:
		return to == StatusActive || to == StatusPaused || to == StatusArchived
	case StatusActive:
		return to == StatusPaused || to == StatusArchived
	case StatusPaused:
		return to == StatusActive || to == StatusArchived
	default:
		return false
	}
}

func ValidKey(value string, maximum int) bool {
	value = strings.TrimSpace(value)
	return value != "" && utf8.ValidString(value) && len(value) <= maximum && !strings.ContainsAny(value, "\t\r\n")
}

func ValidText(value string, maximum int) bool {
	return value != "" && value == strings.TrimSpace(value) && utf8.ValidString(value) && len([]rune(value)) <= maximum
}

func ValidJSON(value json.RawMessage) bool { return len(value) > 0 && json.Valid(value) }

// ContainsForbidden rejects identity and execution scopes outside this local
// lifecycle. Human-readable labels such as "audience" are allowed; concrete
// customer/segment/campaign/recipient identifiers and provider effects are not.
func ContainsForbidden(value any) bool {
	switch item := value.(type) {
	case json.RawMessage:
		if len(item) == 0 {
			return false
		}
		var decoded any
		if !json.Valid(item) || json.Unmarshal(item, &decoded) != nil {
			return true
		}
		return ContainsForbidden(decoded)
	case []byte:
		return ContainsForbidden(json.RawMessage(item))
	case map[string]any:
		for key, child := range item {
			lowered := strings.ToLower(key)
			normalized := strings.NewReplacer("_", "", "-", "", " ", "").Replace(lowered)
			if forbiddenKey(normalized) || normalized == "externaleffects" && resultString(item, key) != "" && resultString(item, key) != "none" {
				return true
			}
			if ContainsForbidden(child) {
				return true
			}
		}
	case map[string]string:
		for key, value := range item {
			normalized := strings.NewReplacer("_", "", "-", "", " ", "").Replace(strings.ToLower(key))
			if forbiddenKey(normalized) || ContainsForbidden(value) {
				return true
			}
		}
	case []any:
		for _, child := range item {
			if ContainsForbidden(child) {
				return true
			}
		}
	case string:
		return strings.Contains(item, "/Users/") || strings.HasPrefix(item, "file://") || phoneNumber.MatchString(item) || emailAddress.MatchString(item)
	}
	return false
}

func forbiddenKey(key string) bool {
	switch key {
	case "tenant", "tenantid", "tenantids", "customerid", "customerids", "externaluserid", "externaluserids", "externalidentityid", "externalidentityids", "identityid", "identityids", "identitykey", "identitykeys", "oneid", "oneidid", "oneidids", "openid", "openids", "unionid", "unionids", "mobile", "phone", "phonenumber", "email", "emailaddress", "segmentid", "segmentids", "campaignid", "campaignids", "recipientid", "recipientids", "audienceid", "audienceids", "orderid", "orderids", "orderstate", "orderstatus", "entitlementid", "entitlementids", "entitlementstate", "entitlementstatus", "membershipid", "membershipids", "membershipstate", "membershipstatus", "subscriptionid", "subscriptionids", "subscriptionstate", "subscriptionstatus", "serviceperiodid", "serviceperiodids", "serviceperiodstate", "serviceperiodstatus", "token", "accesstoken", "refreshtoken", "password", "cookie", "privatekey", "apikey", "authorization":
		return true
	default:
		return strings.Contains(key, "credential") || strings.Contains(key, "secret") || strings.Contains(key, "token") || strings.Contains(key, "password") || strings.Contains(key, "cookie") || strings.Contains(key, "privatekey")
	}
}

// ProjectReportSnapshot accepts only the local, presentation-safe report
// shape.  Reports used to retain an arbitrary JSON object after a denylist
// scan, which made it possible for a newly named secret or PII field to reach
// the projection, audit event and outbox.  DecodeUnknownFields makes this
// boundary fail closed; the returned map is reconstructed from the typed
// value, never from the caller's object.
func ProjectReportSnapshot(value map[string]any) (map[string]any, error) {
	if value == nil || ContainsForbidden(value) {
		return nil, ErrInvalidScope
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, ErrInvalidScope
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var snapshot operationCycleReportSnapshot
	if err = decoder.Decode(&snapshot); err != nil {
		return nil, ErrInvalidScope
	}
	if err = decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, ErrInvalidScope
	}
	if snapshot.Revision == 0 {
		snapshot.Revision = 1
	}
	if snapshot.StrategyVersion == 0 {
		snapshot.StrategyVersion = snapshot.Revision
	}
	if snapshot.Status == "" {
		snapshot.Status = StatusActive
	}
	if snapshot.Title == "" {
		snapshot.Title = snapshot.StrategyKey
	}
	if snapshot.SchemaVersion != "operation_cycle_snapshot.v1" || !ValidKey(snapshot.StrategyKey, 120) || !ValidKey(snapshot.RunKey, 160) || snapshot.Revision < 1 || snapshot.StrategyVersion < 1 || !validStrategyStatus(snapshot.Status) || !ValidText(snapshot.Title, 200) {
		return nil, ErrInvalidScope
	}
	if err = snapshot.validate(); err != nil {
		return nil, ErrInvalidScope
	}
	projected, err := json.Marshal(snapshot)
	if err != nil {
		return nil, ErrInvalidScope
	}
	var result map[string]any
	if err = json.Unmarshal(projected, &result); err != nil || ContainsForbidden(result) {
		return nil, ErrInvalidScope
	}
	return result, nil
}

type operationCycleReportSnapshot struct {
	SchemaVersion   string                `json:"schema_version"`
	StrategyKey     string                `json:"strategy_key"`
	RunKey          string                `json:"run_key"`
	Revision        int32                 `json:"revision"`
	StrategyVersion int32                 `json:"strategy_version"`
	Status          string                `json:"status"`
	Title           string                `json:"title"`
	Name            string                `json:"name,omitempty"`
	Cron            string                `json:"cron,omitempty"`
	Dot             string                `json:"dot,omitempty"`
	Action          string                `json:"action,omitempty"`
	Steps           []operationCycleStep  `json:"steps,omitempty"`
	Dossier         operationCycleDossier `json:"dossier,omitempty"`
}

type operationCycleStep struct {
	Label string `json:"label"`
	Color string `json:"color"`
	Dim   bool   `json:"dim"`
}
type operationCycleStage struct {
	Label  string `json:"label"`
	Status string `json:"status"`
}
type operationCycleAttempt struct {
	Label       string                `json:"label"`
	StatusLabel string                `json:"status_label"`
	Tone        string                `json:"tone"`
	Summary     string                `json:"summary"`
	StartedAt   string                `json:"started_at"`
	FinishedAt  string                `json:"finished_at"`
	Stages      []operationCycleStage `json:"stages"`
}
type operationCycleFunnel struct {
	Label string `json:"label"`
	Value string `json:"value"`
}
type operationCycleMetric struct {
	Label string `json:"label"`
	Value string `json:"value"`
	Desc  string `json:"desc"`
}
type operationCycleWindow struct {
	Label       string                 `json:"label"`
	StatusLabel string                 `json:"status_label"`
	Tone        string                 `json:"tone"`
	Metrics     []operationCycleMetric `json:"metrics"`
	Start       string                 `json:"start"`
	End         string                 `json:"end"`
	Quality     string                 `json:"quality"`
	Limitation  string                 `json:"limitation"`
}
type operationCycleDelivery struct {
	Sent           string `json:"sent"`
	Failed         string `json:"failed"`
	Retryable      string `json:"retryable"`
	Rate           string `json:"rate"`
	StatusLabel    string `json:"status_label"`
	Source         string `json:"source"`
	FailureSummary string `json:"failure_summary"`
}
type operationCycleRetro struct {
	Summary     string   `json:"summary"`
	Detail      string   `json:"detail"`
	Findings    []string `json:"findings"`
	Limitations []string `json:"limitations"`
}
type operationCycleNext struct {
	StatusLabel    string   `json:"status_label"`
	Tone           string   `json:"tone"`
	Summary        string   `json:"summary"`
	Rationale      string   `json:"rationale"`
	ConfirmedAt    string   `json:"confirmed_at"`
	AppliedVersion string   `json:"applied_version"`
	Note           string   `json:"note"`
	Changes        []string `json:"changes"`
}
type operationCycleReference struct {
	Label string `json:"label"`
	Desc  string `json:"desc"`
}
type operationCycleDossier struct {
	Label            string                    `json:"label"`
	Objective        string                    `json:"objective"`
	Strategy         string                    `json:"strategy"`
	Audience         string                    `json:"audience"`
	IntendedSendAt   string                    `json:"intended_send_at"`
	PlanScheduledFor string                    `json:"plan_scheduled_for"`
	FirstSentAt      string                    `json:"first_sent_at"`
	LastSentAt       string                    `json:"last_sent_at"`
	Attempts         []operationCycleAttempt   `json:"attempts"`
	Funnel           []operationCycleFunnel    `json:"funnel"`
	AudienceNote     string                    `json:"audience_note"`
	ReviewStatus     string                    `json:"review_status"`
	ReviewTone       string                    `json:"review_tone"`
	PlanVersion      string                    `json:"plan_version"`
	PlanStatus       string                    `json:"plan_status"`
	PlanSource       string                    `json:"plan_source"`
	TargetCount      string                    `json:"target_count"`
	Delivery         operationCycleDelivery    `json:"delivery"`
	Windows          []operationCycleWindow    `json:"windows"`
	Retro            operationCycleRetro       `json:"retro"`
	Next             operationCycleNext        `json:"next"`
	References       []operationCycleReference `json:"references"`
}

func (snapshot operationCycleReportSnapshot) validate() error {
	if len(snapshot.Steps) > 32 || len(snapshot.Dossier.Attempts) > 32 || len(snapshot.Dossier.Funnel) > 32 || len(snapshot.Dossier.Windows) > 32 || len(snapshot.Dossier.References) > 64 {
		return fmt.Errorf("report snapshot collection is too large")
	}
	for _, value := range snapshot.allText() {
		if len([]rune(value)) > 1000 || ContainsForbidden(value) {
			return fmt.Errorf("report snapshot contains unsafe display text")
		}
	}
	if snapshot.Dot != "" && !cssColor.MatchString(snapshot.Dot) {
		return fmt.Errorf("report snapshot dot is not a color")
	}
	for _, step := range snapshot.Steps {
		if !cssColor.MatchString(step.Color) {
			return fmt.Errorf("report snapshot step color is not a color")
		}
	}
	return nil
}

func (snapshot operationCycleReportSnapshot) allText() []string {
	values := []string{snapshot.Name, snapshot.Cron, snapshot.Dot, snapshot.Action, snapshot.Dossier.Label, snapshot.Dossier.Objective, snapshot.Dossier.Strategy, snapshot.Dossier.Audience, snapshot.Dossier.IntendedSendAt, snapshot.Dossier.PlanScheduledFor, snapshot.Dossier.FirstSentAt, snapshot.Dossier.LastSentAt, snapshot.Dossier.AudienceNote, snapshot.Dossier.ReviewStatus, snapshot.Dossier.ReviewTone, snapshot.Dossier.PlanVersion, snapshot.Dossier.PlanStatus, snapshot.Dossier.PlanSource, snapshot.Dossier.TargetCount, snapshot.Dossier.Delivery.Sent, snapshot.Dossier.Delivery.Failed, snapshot.Dossier.Delivery.Retryable, snapshot.Dossier.Delivery.Rate, snapshot.Dossier.Delivery.StatusLabel, snapshot.Dossier.Delivery.Source, snapshot.Dossier.Delivery.FailureSummary, snapshot.Dossier.Retro.Summary, snapshot.Dossier.Retro.Detail, snapshot.Dossier.Next.StatusLabel, snapshot.Dossier.Next.Tone, snapshot.Dossier.Next.Summary, snapshot.Dossier.Next.Rationale, snapshot.Dossier.Next.ConfirmedAt, snapshot.Dossier.Next.AppliedVersion, snapshot.Dossier.Next.Note}
	for _, step := range snapshot.Steps {
		values = append(values, step.Label, step.Color)
	}
	for _, attempt := range snapshot.Dossier.Attempts {
		values = append(values, attempt.Label, attempt.StatusLabel, attempt.Tone, attempt.Summary, attempt.StartedAt, attempt.FinishedAt)
		for _, stage := range attempt.Stages {
			values = append(values, stage.Label, stage.Status)
		}
	}
	for _, funnel := range snapshot.Dossier.Funnel {
		values = append(values, funnel.Label, funnel.Value)
	}
	for _, window := range snapshot.Dossier.Windows {
		values = append(values, window.Label, window.StatusLabel, window.Tone, window.Start, window.End, window.Quality, window.Limitation)
		for _, metric := range window.Metrics {
			values = append(values, metric.Label, metric.Value, metric.Desc)
		}
	}
	values = append(values, snapshot.Dossier.Retro.Findings...)
	values = append(values, snapshot.Dossier.Retro.Limitations...)
	values = append(values, snapshot.Dossier.Next.Changes...)
	for _, reference := range snapshot.Dossier.References {
		values = append(values, reference.Label, reference.Desc)
	}
	return values
}

func resultString(value map[string]any, key string) string {
	raw, _ := value[key].(string)
	return strings.TrimSpace(raw)
}

func validStrategyStatus(value string) bool {
	return value == StatusDraft || value == StatusActive || value == StatusPaused || value == StatusArchived
}

func validCompatibilityStatus(value string) bool {
	return value == "ready" || value == "incompatible" || value == "unavailable"
}

func validActionStatus(value string) bool {
	return value == ActionQueued || value == ActionClaimed || value == ActionThreadBound || value == ActionTurnStarted || value == ActionCompleted || value == ActionFailed
}

func validProposalStatus(value string) bool {
	return value == ProposalPending || value == ProposalAccepted || value == ProposalRejected
}
