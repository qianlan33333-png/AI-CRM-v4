package wecom

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/idempotency"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/webhook"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/provider"
)

type FollowRelationship struct {
	CorpID     string
	EmployeeID string
	CustomerID customerdomain.CustomerID
	Active     bool
	UpdatedAt  time.Time
}

// FollowRelationshipStore is the Sidebar's current-state query boundary.
// Callback writes use CallbackFollowRelationshipStore because they require an
// event cursor and deterministic ordering.
type FollowRelationshipStore interface {
	Upsert(ctx context.Context, relationship FollowRelationship) error
	IsActive(ctx context.Context, corpID, employeeID string, customerID customerdomain.CustomerID) (bool, error)
}

// CallbackExternalContactEvent is the identity-scoped ordering cursor written
// before OneID resolution. Keeping this cursor independent from customer_id
// lets a delete for an as-yet unknown external contact suppress an older add
// that arrives later. ExternalIdentityDigest is irreversible callback-local
// correlation material; it is never returned by an HTTP API.
type CallbackExternalContactEvent struct {
	CallbackID             string
	CorpID                 string
	EmployeeID             string
	ExternalIdentityDigest [32]byte
	Active                 bool
	OccurredAt             time.Time
}

type ExternalContactEventAdmission struct {
	Admitted bool
	Advanced bool
	Active   bool
}

// CallbackFollowRelationship carries ordering data from a durable callback.
// CallbackID is the Inbox idempotency key. Equal provider timestamps never
// use its hash ordering: a deactivation wins an activation, while equivalent
// state changes are harmlessly coalesced.
type CallbackFollowRelationship struct {
	CallbackID string
	CorpID     string
	EmployeeID string
	CustomerID customerdomain.CustomerID
	Active     bool
	OccurredAt time.Time
}

type FollowRelationshipApplication struct {
	Applied bool
	Active  bool
}

type CallbackFollowRelationshipStore interface {
	AdmitExternalContactEvent(context.Context, CallbackExternalContactEvent) (ExternalContactEventAdmission, error)
	ApplyCallbackEvent(context.Context, CallbackFollowRelationship) (FollowRelationshipApplication, error)
}

type callbackProcessingReceiptStore interface {
	AppendProcessing(context.Context, AppendCallbackProcessingReceipt) (CallbackReceipt, bool, error)
}

// InboxProcessor is invoked by an external oneshot worker. It deliberately has
// no ticker, scheduler, goroutine, or provider network client.
type InboxProcessor struct {
	Enabled   bool
	CorpID    string
	Inbox     *webhook.Service
	UOW       platformport.UnitOfWork
	Lifecycle ExternalContactLifecycle
	// DescriptionJobs is optional while the contract is disabled. When enabled,
	// a processed full-contact callback atomically enqueues a bounded detail
	// observation job; the HTTP callback handler still only inboxes and ACKs.
	DescriptionJobs ContactDescriptionCallbackJobEnqueuer
	Receipts        callbackProcessingReceiptStore
	Audit           *audit.Service
	Now             func() time.Time
}

func (processor InboxProcessor) ProcessOnce(ctx context.Context, owner string, limit int) (int, error) {
	if !processor.Enabled {
		return 0, ErrProviderDisabled
	}
	if processor.Inbox == nil || processor.UOW == nil || processor.Lifecycle.Identity == nil ||
		processor.Lifecycle.Relationships == nil || processor.Lifecycle.States == nil || processor.Lifecycle.Entrants == nil ||
		processor.Receipts == nil || processor.Audit == nil ||
		strings.TrimSpace(processor.CorpID) != processor.CorpID || processor.CorpID == "" {
		return 0, errors.New("wecom inbox processor dependencies are required")
	}
	var deliveries []webhook.Delivery
	if err := processor.UOW.Within(ctx, func(txContext context.Context) error {
		var err error
		deliveries, err = processor.Inbox.Claim(txContext, webhook.Claim{
			Provider: callbackProvider, Owner: owner, Limit: limit, LeaseDuration: time.Minute,
		})
		return err
	}); err != nil {
		return 0, err
	}
	for _, delivery := range deliveries {
		if err := processor.processDelivery(ctx, delivery); err != nil {
			return 0, err
		}
	}
	return len(deliveries), nil
}

type callbackDeliveryError struct {
	code     string
	terminal bool
	cause    error
}

func (problem callbackDeliveryError) Error() string { return problem.code }
func (problem callbackDeliveryError) Unwrap() error { return problem.cause }

func (processor InboxProcessor) processDelivery(ctx context.Context, delivery webhook.Delivery) error {
	processErr := processor.UOW.Within(ctx, func(txContext context.Context) error {
		event, problem := processor.decodeDelivery(delivery)
		if problem != nil {
			return problem
		}
		fact, problem := processor.lifecycleFact(delivery, event)
		if problem != nil {
			return problem
		}
		result, err := processor.Lifecycle.ProcessWithin(txContext, fact)
		if err != nil {
			return callbackDeliveryError{code: "callback_lifecycle", cause: err}
		}
		if processor.DescriptionJobs != nil && event.ChangeType == ChangeAddExternalContact && result.CustomerID > 0 {
			if err = processor.DescriptionJobs.EnqueueContactDescriptionObservation(txContext, delivery.ID); err != nil {
				return callbackDeliveryError{code: "callback_description_enqueue", cause: err}
			}
		}
		codes, err := callbackResultCodes(result.Outcomes)
		if err != nil {
			return callbackDeliveryError{code: "invalid_lifecycle_result", terminal: true, cause: err}
		}
		return processor.finalize(txContext, delivery, event.Event, event.ChangeType,
			webhook.StatusProcessed, codes, "", result.CustomerID)
	})
	if processErr == nil {
		return nil
	}

	// The first transaction is deliberately rolled back before recording a
	// failure. This guarantees a lifecycle error cannot commit a partial OneID,
	// relationship or entrant write. The failure receipt and Inbox transition
	// then commit together in a clean transaction.
	var problem callbackDeliveryError
	if !errors.As(processErr, &problem) {
		problem = callbackDeliveryError{code: "callback_processing", cause: processErr}
	}
	status := webhook.StatusRetryable
	codes := []CallbackResultCode(nil)
	if problem.terminal || delivery.AttemptCount >= delivery.MaxAttempts {
		status = webhook.StatusFailed
		codes = []CallbackResultCode{CallbackFailedTerminal}
	}
	eventType, changeType := safeReceiptEventLabels(delivery.Payload)
	return processor.UOW.Within(ctx, func(txContext context.Context) error {
		return processor.finalize(txContext, delivery, eventType, changeType, status, codes, problem.code, 0)
	})
}

func (processor InboxProcessor) decodeDelivery(delivery webhook.Delivery) (CallbackEvent, error) {
	var event CallbackEvent
	decoder := json.NewDecoder(bytes.NewReader(delivery.Payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&event); err != nil {
		return CallbackEvent{}, callbackDeliveryError{code: "invalid_callback_payload", terminal: true, cause: err}
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return CallbackEvent{}, callbackDeliveryError{code: "invalid_callback_payload", terminal: true}
	}
	if event.CorpID == "" {
		// Rows accepted by the pre-hardening v3 callback did not persist CorpID
		// in the typed payload. They are still scoped by this provider worker.
		event.CorpID = processor.CorpID
	}
	if event.CorpID != processor.CorpID || event.Event != "change_external_contact" ||
		!validCallbackLabel(event.ChangeType) || event.CreateTime <= 0 ||
		!validText(event.ExternalUserID, 1024) || (event.UserID != "" && !validText(event.UserID, 1024)) {
		return CallbackEvent{}, callbackDeliveryError{code: "invalid_callback_payload", terminal: true}
	}
	return event, nil
}

func (processor InboxProcessor) lifecycleFact(delivery webhook.Delivery, event CallbackEvent) (ExternalContactLifecycleFact, error) {
	verified, err := provider.VerifiedExternalContact(processor.CorpID, event.ExternalUserID, "wecom.callback")
	if err != nil {
		return ExternalContactLifecycleFact{}, callbackDeliveryError{code: "invalid_verified_fact", terminal: true, cause: err}
	}
	var stateDigest [32]byte
	if event.StatePresent {
		stateDigest, err = ParseStateDigest(event.StateDigest)
		if err != nil {
			return ExternalContactLifecycleFact{}, callbackDeliveryError{code: "invalid_state_digest", terminal: true, cause: err}
		}
	} else if event.StateDigest != "" {
		return ExternalContactLifecycleFact{}, callbackDeliveryError{code: "invalid_state_digest", terminal: true}
	}
	fact := ExternalContactLifecycleFact{
		CallbackID: string(delivery.IdempotencyKey), InboxID: delivery.ID, CorpID: processor.CorpID,
		ChangeType: event.ChangeType, ExternalUserID: event.ExternalUserID, EmployeeUserID: event.UserID,
		HasState: event.StatePresent, StateDigest: stateDigest, OccurredAt: time.Unix(event.CreateTime, 0).UTC(),
		WelcomeGrantRef:  event.WelcomeGrantRef,
		VerifiedIdentity: verified,
	}
	if !fact.Valid() {
		return ExternalContactLifecycleFact{}, callbackDeliveryError{code: "invalid_callback_fact", terminal: true}
	}
	return fact, nil
}

func (processor InboxProcessor) finalize(ctx context.Context, delivery webhook.Delivery, eventType, changeType string,
	status webhook.Status, resultCodes []CallbackResultCode, errorCode string, customerID customerdomain.CustomerID,
) error {
	resultCodes = canonicalProcessorResults(resultCodes)
	command := AppendCallbackProcessingReceipt{
		InboxID: delivery.ID, AttemptNumber: delivery.AttemptCount,
		CommandDigest: callbackReceiptCommandDigest(delivery, eventType, changeType, status, resultCodes, errorCode),
		EventType:     eventType, ChangeType: changeType, ResultingInboxStatus: status,
		ResultCodes: resultCodes, ErrorCode: errorCode,
	}
	if _, _, err := processor.Receipts.AppendProcessing(ctx, command); err != nil {
		return err
	}
	auditKey, err := idempotency.Parse("wecom:callback:" + strconv.FormatInt(delivery.ID, 10) + ":" + strconv.Itoa(delivery.AttemptCount))
	if err != nil {
		return err
	}
	auditPayload := map[string]any{
		"attempt": delivery.AttemptCount, "inbox_id": delivery.ID,
		"result_codes": resultCodes, "status": status,
	}
	if customerID > 0 {
		auditPayload["customer_id"] = customerID
	}
	payload, err := json.Marshal(auditPayload)
	if err != nil {
		return err
	}
	if _, err = processor.Audit.Append(ctx, audit.Event{
		IdempotencyKey: auditKey, Action: callbackAuditAction(status), ActorType: "provider",
		ResourceType: "wecom_callback", ResourceID: strconv.FormatInt(delivery.ID, 10), Payload: payload,
	}); err != nil && !errors.Is(err, audit.ErrDuplicateEvent) {
		return err
	}
	completion := webhook.Completion{
		ID: delivery.ID, ExpectedAttempt: delivery.AttemptCount, Status: status, LastErrorCode: errorCode,
	}
	if status == webhook.StatusRetryable {
		next := processor.now().Add(time.Minute)
		completion.NextAttemptAt = &next
	}
	_, err = processor.Inbox.Complete(ctx, completion)
	return err
}

func callbackResultCodes(outcomes []CallbackOutcome) ([]CallbackResultCode, error) {
	if len(outcomes) == 0 || len(outcomes) > 4 {
		return nil, errors.New("invalid callback lifecycle outcomes")
	}
	codes := make([]CallbackResultCode, 0, len(outcomes))
	for _, outcome := range outcomes {
		code := CallbackResultCode(outcome)
		switch code {
		case CallbackCustomerCreated, CallbackCustomerResolved, CallbackRelationshipActivated,
			CallbackRelationshipDeactivated, CallbackChannelAttributed, CallbackChannelUnmatched,
			CallbackChannelAmbiguous, CallbackIdentityConflict, CallbackIgnored:
			codes = append(codes, code)
		default:
			return nil, errors.New("invalid callback lifecycle outcome")
		}
	}
	return canonicalProcessorResults(codes), nil
}

func canonicalProcessorResults(values []CallbackResultCode) []CallbackResultCode {
	results := append([]CallbackResultCode(nil), values...)
	sort.Slice(results, func(left, right int) bool { return results[left] < results[right] })
	return results
}

func callbackReceiptCommandDigest(delivery webhook.Delivery, eventType, changeType string, status webhook.Status, results []CallbackResultCode, errorCode string) [32]byte {
	parts := []string{"processing-v1", strconv.FormatInt(delivery.ID, 10), strconv.Itoa(delivery.AttemptCount),
		base64.RawURLEncoding.EncodeToString(delivery.PayloadHash[:]), eventType, changeType, string(status), errorCode}
	for _, result := range canonicalProcessorResults(results) {
		parts = append(parts, string(result))
	}
	return sha256.Sum256([]byte(strings.Join(parts, "\x00")))
}

func safeReceiptEventLabels(payload json.RawMessage) (string, string) {
	var event struct {
		Event      string `json:"event"`
		ChangeType string `json:"change_type"`
	}
	if json.Unmarshal(payload, &event) != nil || !validCallbackLabel(event.Event) || !validCallbackLabel(event.ChangeType) {
		return "invalid", "invalid"
	}
	return event.Event, event.ChangeType
}

func callbackAuditAction(status webhook.Status) string {
	switch status {
	case webhook.StatusProcessed:
		return "wecom.callback_processed"
	case webhook.StatusRetryable:
		return "wecom.callback_retryable"
	default:
		return "wecom.callback_failed"
	}
}

func (processor InboxProcessor) now() time.Time {
	if processor.Now != nil {
		return processor.Now().UTC()
	}
	return time.Now().UTC()
}
