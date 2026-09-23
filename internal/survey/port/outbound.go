package port

import (
	"context"
	"encoding/json"
	"time"

	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
)

// CompletionIntent contains only opaque references and digests. It cannot
// carry a URL, credential, external identity, phone number, answer or message
// body across the Survey boundary.
type CompletionIntent struct {
	QuestionnaireID ID
	SubmissionID    ID
	// TestRunID identifies an admin-triggered synthetic push. It is mutually
	// exclusive with SubmissionID and never denotes a Customer or submission.
	TestRunID              string
	ConfigurationReference string
	SourceDigest           string
	TargetDigest           string
	PayloadDigest          string
	PolicyDigest           string
	IdempotencyKey         string
	ScheduledAt            time.Time
}

type EffectBinding struct {
	EffectID string
	State    string
}

// CompletionIntentAccepter must join the caller's PostgreSQL transaction.
// Provider execution is explicitly outside this contract and transaction.
type CompletionIntentAccepter interface {
	AcceptCompletionWithin(context.Context, CompletionIntent) (EffectBinding, error)
}

// CompletionPayload is read only by the configured outbound provider after the
// effect attempt is durably marked. It never enters External Effects, audit
// records, or structured logs. The source and payload digests bind it to the
// immutable submission accepted by Survey.
type CompletionPayload struct {
	QuestionnaireID, SubmissionID ID
	CustomerID                    int64
	ConfigurationReference        string
	SourceDigest, TargetDigest    string
	PayloadDigest, PolicyDigest   string
	IdempotencyKey                string
	QuestionnaireTitle            string
	SubmittedAt                   time.Time
	Answers                       []CompletionAnswer
	AssessmentResult              json.RawMessage
	Policy                        CompletionPolicy
	ExternalUserID                string
	SyntheticTest                 bool
	TestRunID                     string
}

// CompletionTestReceipt is the safe operator projection for one synthetic
// test push. It contains no endpoint, secret, customer, answer, or identity.
type CompletionTestReceipt struct {
	QuestionnaireID ID
	TestRunID       string
	EffectID        string
	State           string
}

type CompletionAnswer struct {
	QuestionTitle string
	QuestionType  QuestionType
	TextValue     string
	OptionTexts   []string
}

// CompletionPolicy is the non-secret, versioned compatibility contract frozen
// with a submission. Endpoint and signing material stay composition-only.
type CompletionPolicy struct {
	ConfigurationReference string              `json:"-"`
	ConfigurationVersion   string              `json:"-"`
	ConfigurationDigest    string              `json:"-"`
	IdentityKind           identitydomain.Kind `json:"-"`
	IdentityScope          string              `json:"-"`
	Day                    *int64              `json:"day,omitempty"`
	Frequency              *int64              `json:"frequency,omitempty"`
	ExpiresAtTS            *int64              `json:"expires_at_ts,omitempty"`
	PushType               string              `json:"type,omitempty"`
	Remark                 string              `json:"remark,omitempty"`
	CustomParams           map[string]string   `json:"custom_params,omitempty"`
}

type CompletionPolicyResolver interface {
	CompletionPolicy(context.Context, string) (CompletionPolicy, bool, error)
}

// CompletionTargetCatalog exposes only Composition's configured opaque target
// references to Survey's administrative binding surface. It must never expose
// endpoint, signing material, or identity policy.
type CompletionTargetCatalog interface {
	CompletionTargetReferences(context.Context) ([]string, error)
}

type CompletionIdentitySnapshotter interface {
	SnapshotCompletionIdentity(context.Context, int64, CompletionPolicy) (string, bool, error)
}

// CompletionPayloadReader is the narrowly scoped read seam used by Outbound
// only while dispatching a Survey completion. Callers must not persist or log
// the returned answers or customer identifier.
type CompletionPayloadReader interface {
	ReadCompletionPayload(context.Context, string) (CompletionPayload, error)
}

// CompletionEffectProjector accepts terminal EER state back into Survey's
// own operation receipt in the completion transaction.
type CompletionEffectProjector interface {
	CompleteCompletionEffect(context.Context, string, string, bool, bool, *bool, string, int32, time.Time) error
}
