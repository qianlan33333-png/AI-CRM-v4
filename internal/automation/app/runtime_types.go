package app

import (
	"encoding/json"
	"time"
)

const (
	MemberEventMissingPolicyOperation        = "member_event_no_active_policy"
	MemberEventDeferredOperation             = "member_event_deferred"
	MemberEventSourceFactsOperation          = "member_event_source_facts_v1"
	MemberEventDeferredHistoricalMergeReason = "historical_identity_merge_deferred"
	MemberEventDeferredPaidCutoffReason      = "historical_paid_before_cutoff"
	MemberEventDeferredPaidMissingReason     = "paid_at_missing_deferred"
	// MemberEventCustomerOnceOperation is the legacy namespace. It remains read
	// compatible so historical accepted receipts still protect aliases; new
	// accepted-only reservations use the v2 namespace below, leaving legacy
	// completed "deferred" receipts non-terminal for later qualifying payments.
	MemberEventCustomerOnceOperation         = "member_event_customer_once"
	MemberEventCustomerOnceAcceptedOperation = "member_event_customer_once_accepted_v2"
	MemberEventDispatchActorScope            = "system:segment-member-event-dispatch"
)

type RuntimeReceipt struct {
	ID                           int64
	Operation, ActorScope, State string
	KeyDigest, PayloadDigest     [32]byte
	Result                       json.RawMessage
}
type RuntimeReservation struct {
	Operation, ActorScope    string
	KeyDigest, PayloadDigest [32]byte
	CreatedAt                time.Time
}
type RuntimeFact struct {
	Kind                 string
	ID                   int64
	Operation, EventType string
	Actor                int64
	Payload              json.RawMessage
	Key                  string
	At                   time.Time
}

// MemberEventDispatchDiagnostic is a durable, read-only explanation of a
// member-entered event that was consumed without an external effect. It
// deliberately contains no customer ID or raw Segment event ID.
type MemberEventDispatchDiagnostic struct {
	ID                     int64     `json:"id,omitempty"`
	PackageID              int64     `json:"package_id"`
	SnapshotID             int64     `json:"snapshot_id"`
	ConfigurationVersionID int64     `json:"configuration_version_id"`
	PolicyID               int64     `json:"policy_id,omitempty"`
	PolicyVersionID        int64     `json:"policy_version_id,omitempty"`
	EventDigest            string    `json:"event_digest"`
	State                  string    `json:"state"`
	Reason                 string    `json:"reason"`
	OccurredAt             time.Time `json:"occurred_at"`
	RecordedAt             time.Time `json:"recorded_at"`
}
