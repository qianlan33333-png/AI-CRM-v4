package port

import (
	"context"
	"errors"
	"strconv"
	"strings"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
)

const ContactDescriptionContractVersion = "wecom.contact.description.v1"

const (
	ContactDescriptionOperationWrite    = "write"
	ContactDescriptionOperationReadback = "readback"
)

var (
	// ErrContactDescriptionReplanRequired protects an immutable snapshot from
	// being silently replaced by a later directory observation.
	ErrContactDescriptionReplanRequired = errors.New("contact description explicit replan required")
	// ErrContactDescriptionOutcomeUnknown makes an operator reconcile the
	// original EER effect before any new write plan can be accepted.
	ErrContactDescriptionOutcomeUnknown = errors.New("contact description outcome unknown")
	ErrContactDescriptionInFlight       = errors.New("contact description plan in flight")
)

// ContactDescriptionObservedDigest is the one stable digest for a provider
// description snapshot. Keep its construction at this Port boundary: callers
// must not accidentally use a raw hash that the Outbound executor cannot
// compare with the live read.
func ContactDescriptionObservedDigest(value string) effectport.Digest {
	return effectport.Hash("wecom.contact.description.observed.v1", value)
}

func ContactDescriptionTargetDigest(employeeID, externalUserID string) effectport.Digest {
	return effectport.Hash("wecom.contact.description.target.v1", employeeID, externalUserID)
}

func ContactDescriptionPayloadDigest(observed effectport.Digest) effectport.Digest {
	return effectport.Hash("wecom.contact.description.payload.v1", string(observed))
}

// ContactDescriptionRelationshipKey is deliberately independent of a
// description observation and source (full sync versus callback). One active
// follow relationship therefore converges on the same first immutable plan.
func ContactDescriptionRelationshipKey(corpScope, employeeID string, externalUserIDDigest effectport.Digest) effectport.Digest {
	return effectport.Hash("wecom.contact.description.relationship.v1", corpScope, employeeID, string(externalUserIDDigest), ContactDescriptionContractVersion)
}

// ContactDescriptionEffectReceiptKey derives a new EER receipt only for an
// explicit immutable plan revision. Revision 1 is the stable relationship
// receipt; later revisions are allowed only after a terminal, non-unknown
// predecessor has been inspected and a caller explicitly asks to replan.
func ContactDescriptionEffectReceiptKey(relationship effectport.Digest, revision int64) effectport.Digest {
	return effectport.Hash("wecom.contact.description.effect-receipt.v1", string(relationship), strconv.FormatInt(revision, 10))
}

// ContactDescriptionIntentCommand accepts one already-observed active follow
// relationship. Its digests commit the exact observation; raw provider IDs and
// human description are used only while calculating those digests and are not
// retained by the External Effects envelope.
type ContactDescriptionIntentCommand struct {
	CustomerID                customerdomain.CustomerID
	EmployeeUserID            string
	SourceDigest              effectport.Digest
	TargetDigest              effectport.Digest
	ObservedDescriptionDigest effectport.Digest
	PayloadDigest             effectport.Digest
	// ReceiptKey is the stable relationship-level receipt key. It must not
	// contain the observed description or source run so full and incremental
	// maintenance converge on one plan.
	ReceiptKey effectport.Digest
	// SourceRunID is an opaque WeCom sync-run reference used only for safe
	// operational aggregation. It is not a foreign-key dependency on a WeCom
	// table and is zero for callback-driven maintenance.
	SourceRunID int64
	// Replan is a conscious operation after a terminal snapshot outcome. It
	// never bypasses outcome_unknown or an in-flight original effect.
	Replan bool
	// Operation is write for the immutable compare-before-write plan and
	// readback for an operator-requested, provider-read-only confirmation.
	Operation string
}

func (c ContactDescriptionIntentCommand) Valid() bool {
	return c.CustomerID > 0 && c.EmployeeUserID != "" && c.EmployeeUserID == strings.TrimSpace(c.EmployeeUserID) && len(c.EmployeeUserID) <= 1024 &&
		effectport.ValidDigest(c.SourceDigest) && effectport.ValidDigest(c.TargetDigest) && effectport.ValidDigest(c.ObservedDescriptionDigest) && effectport.ValidDigest(c.PayloadDigest) && effectport.ValidDigest(c.ReceiptKey) && c.SourceRunID >= 0 && (c.Operation == ContactDescriptionOperationWrite || c.Operation == ContactDescriptionOperationReadback)
}

type ContactDescriptionIntentResult struct {
	IntentID int64
	EffectID string
	Replayed bool
}

// ContactDescriptionIntentWriter participates in the caller's existing UoW.
// It is the sole accept path for description effects and cannot write WeCom.
type ContactDescriptionIntentWriter interface {
	WriteContactDescriptionIntentWithin(context.Context, ContactDescriptionIntentCommand) (ContactDescriptionIntentResult, error)
}

// ContactDescriptionRunStats is digest-safe status for one source sync run.
// Sync completion only proves enumeration; BackfillCompleted counts provider
// readback confirmations plus already-present relationships.
type ContactDescriptionRunStats struct {
	Discovered         int64 `json:"discovered"`
	Queued             int64 `json:"queued"`
	Written            int64 `json:"written"`
	AlreadyPresent     int64 `json:"already_present"`
	ReadbackConfirmed  int64 `json:"readback_confirmed"`
	ReadbackFailed     int64 `json:"readback_failed"`
	DescriptionChanged int64 `json:"description_changed"`
	TooLong            int64 `json:"too_long"`
	NotAuthorized      int64 `json:"not_authorized"`
	RetryableFailed    int64 `json:"retryable_failed"`
	TerminalFailed     int64 `json:"terminal_failed"`
	OutcomeUnknown     int64 `json:"outcome_unknown"`
	BackfillCompleted  int64 `json:"backfill_completed"`
}

type ContactDescriptionRunStatusReader interface {
	ContactDescriptionRunStats(context.Context, int64) (ContactDescriptionRunStats, error)
}

// ContactDescriptionReadbackScheduler accepts only provider-read follow-ups
// for executions whose write was already accepted but whose first readback
// failed. It never retries an outcome_unknown write or schedules a new write.
type ContactDescriptionReadbackScheduler interface {
	ScheduleContactDescriptionReadbacksWithin(context.Context, int64) (int64, error)
}
