package port

import (
	"context"
	"encoding/json"
	"time"
)

// DispatchExecution is the immutable execution snapshot a Group Ops worker
// may pass to its Provider boundary. It deliberately contains no Provider
// credentials, protocol fields, or response body.
type DispatchExecution struct {
	ExecutionID            int64
	ExternalEffectID       string
	State                  ExecutionState
	DeliveryProven         bool
	AttemptRecovery        *AttemptRecoveryLease
	TargetReference        string
	SenderUserID           string
	ContentSnapshot        json.RawMessage
	ContentDigest          string
	MaterialSnapshot       json.RawMessage
	MaterialDigest         string
	MaterialSourceSnapshot json.RawMessage
	MaterialSourceDigest   string
	SourceRefDigest        string
	TargetRefDigest        string
	PayloadDigest          string
	PolicyVersionHash      string
}

// AttemptRecoveryLease is populated only when the EER effect is still
// attempted and its persisted lease has expired. It lets the worker close the
// attempt as outcome_unknown without calling the Provider again.
type AttemptRecoveryLease struct {
	Generation int64
	Fence      int64
	ExpiresAt  time.Time
}

// DispatchExecutionReader must load the immutable 00085 snapshot for one EER
// effect under the owner-domain transaction/fence used by the integration.
// The Group Ops core intentionally does not guess a Provider request schema.
type DispatchExecutionReader interface {
	LoadDispatchExecution(context.Context, string) (DispatchExecution, error)
}

type MaterialReadinessVerifier interface {
	VerifyMaterialReady(context.Context, json.RawMessage, json.RawMessage, string, time.Time) error
}

// FrozenMaterialSourceVerifier proves that the immutable Group Ops material
// snapshot still refers to the same Media-owned source facts. It deliberately
// excludes provider credential lease validity: Outbound's MaterialPreparer
// owns that independent, refreshable concern.
type FrozenMaterialSourceVerifier interface {
	VerifyFrozenMaterialSources(context.Context, json.RawMessage, json.RawMessage, string) error
}

// ExecutionOutcomeProjector persists the matching Group Ops terminal fact
// after EER has completed an attempt. RuntimeService implements this boundary.
type ExecutionOutcomeProjector interface {
	ProjectExecutionOutcome(context.Context, ExecutionOutcomeCommand) (Execution, error)
}

// ReconciliationEvidenceVerifier is deliberately protocol-neutral. It alone
// may establish delivery_proven from independently verified evidence; caller
// input is never evidence. A nil verifier fails closed to false.
type ReconciliationEvidenceVerifier interface {
	VerifyReconciliationEvidence(context.Context, ReconciliationEvidence) (ReconciliationEvidenceResult, error)
}

type ReconciliationEvidence struct {
	ExecutionID      int64
	ExternalEffectID string
	EvidenceDigest   string
}

type ReconciliationEvidenceResult struct {
	DeliveryProven bool
	EvidenceDigest string
}

// ProviderDeliveryReader is a trusted, owner-side read boundary for a
// provider_accepted task.  It owns the protected msgid/sender lookup and the
// Provider pagination; HTTP callers receive neither input nor raw response.
type ProviderDeliveryReader interface {
	ReadProviderDelivery(context.Context, ReconciliationEvidence) (GroupMessageReceipt, bool, error)
}

// GroupMessageReceipt is owner-only Provider evidence. It is intentionally
// absent from EER and HTTP contracts; its identifiers are required solely for
// the independently documented WeCom reconciliation query.
type GroupMessageReceipt struct {
	ExecutionID            int64
	ExternalEffectID       string
	MessageID              string
	SenderUserID           string
	ChatID                 string
	UserID                 string
	TaskEvidenceDigest     string
	DeliveryStatus         *int
	DeliveryEvidenceDigest string
}

type GroupMessageReceiptWriter interface {
	RecordGroupMessageTask(context.Context, GroupMessageReceipt) error
}

type GroupMessageReceiptReader interface {
	FindGroupMessageReceipt(context.Context, ReconciliationEvidence) (GroupMessageReceipt, bool, error)
	RecordGroupMessageDelivery(context.Context, GroupMessageReceipt, string) error
}

type DispatchOutcome string

const (
	// DispatchPreDispatchFailure means validation, snapshot resolution, or
	// Provider configuration failed before its business boundary was crossed.
	DispatchPreDispatchFailure DispatchOutcome = "pre_dispatch_failure"
	// DispatchProviderAccepted means the Provider explicitly accepted the
	// request. It is not a delivery claim.
	DispatchProviderAccepted DispatchOutcome = "provider_accepted"
	// DispatchOutcomeUnknown means the Provider boundary was crossed but no
	// terminal answer can be safely classified. It never auto-retries.
	DispatchOutcomeUnknown DispatchOutcome = "outcome_unknown"
	// DispatchProviderRejected means an explicit Provider result rejected the
	// request after the business boundary was crossed.
	DispatchProviderRejected DispatchOutcome = "provider_rejected"
)

// DispatchRequest is protocol-neutral. An integration may translate these
// immutable snapshots only after it has an approved Provider contract.
type DispatchRequest struct {
	ExecutionID      int64
	ExternalEffectID string
	TargetReference  string
	SenderUserID     string
	ContentSnapshot  json.RawMessage
	ContentDigest    string
	MaterialSnapshot json.RawMessage
	MaterialDigest   string
}

// DispatchProvider is the sole Group Ops outbound boundary. Implementations
// must return a classified result for every pre-dispatch failure; returning an
// error means the call crossed this boundary and is therefore outcome_unknown.
type DispatchProvider interface {
	Dispatch(context.Context, DispatchRequest) (DispatchProviderResult, error)
}

// DispatchProviderResult exposes only a safe digest, never Provider request,
// response, credential, or delivery evidence. Call evidence is explicit: test
// and sandbox Providers must leave both fields false unless they observed the
// corresponding business boundary crossing.
type DispatchProviderResult struct {
	Outcome                  DispatchOutcome
	ReceiptDigest            string
	BusinessCallDispatched   bool
	RealExternalCallExecuted bool
}

// DispatchResult is the owner-safe worker projection. Provider acceptance is
// intentionally distinct from delivery, and unknown results require manual
// reconciliation.
type DispatchResult struct {
	ExecutionID              int64
	EffectID                 string
	State                    ExecutionState
	ProviderCallAttempted    bool
	RealExternalCallExecuted bool
	ProviderAccepted         bool
	DeliveryProven           bool
	ManualReconcileRequired  bool
}
