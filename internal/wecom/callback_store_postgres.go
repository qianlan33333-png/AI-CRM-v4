package wecom

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"

	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/webhook"
)

var (
	ErrInvalidCallbackReceipt  = errors.New("invalid wecom callback receipt")
	ErrCallbackReceiptConflict = errors.New("wecom callback receipt conflicts with durable fact")
	ErrCallbackReceiptNotFound = errors.New("wecom callback receipt not found")
)

type CallbackReceiptKind string

const (
	CallbackReceiptProcessing     CallbackReceiptKind = "processing"
	CallbackReceiptRetryRequested CallbackReceiptKind = "retry_requested"
)

type CallbackResultCode string

const (
	CallbackCustomerCreated         CallbackResultCode = "customer_created"
	CallbackCustomerResolved        CallbackResultCode = "customer_resolved"
	CallbackRelationshipActivated   CallbackResultCode = "relationship_activated"
	CallbackRelationshipDeactivated CallbackResultCode = "relationship_deactivated"
	CallbackChannelAttributed       CallbackResultCode = "channel_attributed"
	CallbackChannelUnmatched        CallbackResultCode = "channel_unmatched"
	CallbackChannelAmbiguous        CallbackResultCode = "channel_ambiguous"
	CallbackIdentityConflict        CallbackResultCode = "identity_conflict"
	CallbackIgnored                 CallbackResultCode = "ignored"
	CallbackFailedTerminal          CallbackResultCode = "failed_terminal"
)

// CallbackReceipt is the deliberately safe admin projection. It never exposes
// callback payloads, State, external_userid, operation keys, command digests,
// or free-form operator reasons.
type CallbackReceipt struct {
	ID                   int64
	InboxID              int64
	Kind                 CallbackReceiptKind
	TargetReceiptID      int64
	AttemptNumber        int
	EventType            string
	ChangeType           string
	PriorInboxStatus     webhook.Status
	ResultingInboxStatus webhook.Status
	ResultCodes          []CallbackResultCode
	ErrorCode            string
	CreatedAt            time.Time
}

type CallbackReceiptPage struct {
	BeforeID int64
	Limit    int
}

type AppendCallbackProcessingReceipt struct {
	InboxID              int64
	AttemptNumber        int
	CommandDigest        [32]byte
	EventType            string
	ChangeType           string
	ResultingInboxStatus webhook.Status
	ResultCodes          []CallbackResultCode
	ErrorCode            string
}

// BeginCallbackRetry records operator intent only. The caller must, when
// created=true, invoke the platform webhook Retry CAS with ExpectedAttempt and
// ExpectedInboxStatus inside the same UnitOfWork. It must abort that UOW if the
// CAS fails. A replay (created=false) must not invoke Retry again. This ordering
// prevents a retry receipt without a scheduled Inbox retry, blind retry under a
// new key, or an operator overwriting a processing lease.
type BeginCallbackRetry struct {
	TargetReceiptID     int64
	ExpectedAttempt     int
	ExpectedInboxStatus webhook.Status
	ActorAdminUserID    int64
	Reason              string
	OperationKeyDigest  [32]byte
	CommandDigest       [32]byte
}

type PostgreSQLCallbackReceiptStore struct{}

func NewPostgreSQLCallbackReceiptStore() *PostgreSQLCallbackReceiptStore {
	return &PostgreSQLCallbackReceiptStore{}
}

func (*PostgreSQLCallbackReceiptStore) AppendProcessing(ctx context.Context, command AppendCallbackProcessingReceipt) (CallbackReceipt, bool, error) {
	command.ResultCodes = canonicalCallbackResultCodes(command.ResultCodes)
	if !validProcessingReceipt(command) {
		return CallbackReceipt{}, false, ErrInvalidCallbackReceipt
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return CallbackReceipt{}, false, err
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('wecom.callback.processing:' || $1::bigint::text || ':' || $2::integer::text, 0))`, command.InboxID, command.AttemptNumber); err != nil {
		return CallbackReceipt{}, false, err
	}
	var provider string
	var attempt int
	err = tx.QueryRow(ctx, `SELECT provider, attempt_count FROM webhook_inbox WHERE id = $1 FOR SHARE`, command.InboxID).Scan(&provider, &attempt)
	if errors.Is(err, pgx.ErrNoRows) {
		return CallbackReceipt{}, false, ErrCallbackReceiptNotFound
	}
	if err != nil {
		return CallbackReceipt{}, false, err
	}
	if provider != "wecom.external_contact" || attempt != command.AttemptNumber {
		return CallbackReceipt{}, false, ErrCallbackReceiptConflict
	}

	record, err := scanCallbackReceiptRecord(tx.QueryRow(ctx, callbackReceiptByAttemptSQL, command.InboxID, command.AttemptNumber))
	if err == nil {
		if !sameProcessingReceipt(record, command) {
			return CallbackReceipt{}, false, ErrCallbackReceiptConflict
		}
		return record.safe(), false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return CallbackReceipt{}, false, err
	}

	results := make([]string, len(command.ResultCodes))
	for index, result := range command.ResultCodes {
		results[index] = string(result)
	}
	record, err = scanCallbackReceiptRecord(tx.QueryRow(ctx, `
		INSERT INTO wecom_callback_receipts (
			inbox_id, receipt_kind, attempt_number, command_digest,
			event_type, change_type, prior_inbox_status,
			resulting_inbox_status, result_codes, error_code
		) VALUES ($1, 'processing', $2, $3, $4, $5, 'processing', $6, $7, $8)
		RETURNING `+callbackReceiptColumns,
		command.InboxID, command.AttemptNumber, command.CommandDigest[:],
		command.EventType, command.ChangeType, command.ResultingInboxStatus,
		results, command.ErrorCode,
	))
	if err != nil {
		return CallbackReceipt{}, false, err
	}
	return record.safe(), true, nil
}

// BeginRetry appends only the immutable retry operation receipt. See
// BeginCallbackRetry for the required same-UOW platform webhook CAS contract.
func (*PostgreSQLCallbackReceiptStore) BeginRetry(ctx context.Context, command BeginCallbackRetry) (CallbackReceipt, bool, error) {
	if !validRetryReceipt(command) {
		return CallbackReceipt{}, false, ErrInvalidCallbackReceipt
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return CallbackReceipt{}, false, err
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('wecom.callback.retry:' || $1::bigint::text || ':' || encode($2::bytea, 'hex'), 0))`, command.ActorAdminUserID, command.OperationKeyDigest[:]); err != nil {
		return CallbackReceipt{}, false, err
	}

	record, err := scanCallbackReceiptRecord(tx.QueryRow(ctx, callbackRetryReceiptByKeySQL, command.ActorAdminUserID, command.OperationKeyDigest[:]))
	if err == nil {
		if !sameRetryReceipt(record, command) {
			return CallbackReceipt{}, false, ErrCallbackReceiptConflict
		}
		return record.safe(), false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return CallbackReceipt{}, false, err
	}

	target, err := scanCallbackReceiptRecord(tx.QueryRow(ctx, callbackReceiptByIDForShareSQL, command.TargetReceiptID))
	if errors.Is(err, pgx.ErrNoRows) {
		return CallbackReceipt{}, false, ErrCallbackReceiptNotFound
	}
	if err != nil {
		return CallbackReceipt{}, false, err
	}
	if target.Kind != CallbackReceiptProcessing || target.AttemptNumber != command.ExpectedAttempt || target.ResultingInboxStatus != command.ExpectedInboxStatus {
		return CallbackReceipt{}, false, ErrCallbackReceiptConflict
	}

	record, err = scanCallbackReceiptRecord(tx.QueryRow(ctx, `
		INSERT INTO wecom_callback_receipts (
			inbox_id, receipt_kind, target_receipt_id, attempt_number,
			command_digest, prior_inbox_status, resulting_inbox_status,
			actor_admin_user_id, reason, operation_key_digest
		) VALUES ($1, 'retry_requested', $2, $3, $4, $5, 'retryable', $6, $7, $8)
		RETURNING `+callbackReceiptColumns,
		target.InboxID, target.ID, command.ExpectedAttempt,
		command.CommandDigest[:], command.ExpectedInboxStatus,
		command.ActorAdminUserID, command.Reason, command.OperationKeyDigest[:],
	))
	if err != nil {
		return CallbackReceipt{}, false, err
	}
	return record.safe(), true, nil
}

func (*PostgreSQLCallbackReceiptStore) Get(ctx context.Context, receiptID int64) (CallbackReceipt, error) {
	if receiptID < 1 {
		return CallbackReceipt{}, ErrInvalidCallbackReceipt
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return CallbackReceipt{}, err
	}
	record, err := scanCallbackReceiptRecord(tx.QueryRow(ctx, callbackReceiptByIDSQL, receiptID))
	if errors.Is(err, pgx.ErrNoRows) {
		return CallbackReceipt{}, ErrCallbackReceiptNotFound
	}
	if err != nil {
		return CallbackReceipt{}, err
	}
	return record.safe(), nil
}

func (*PostgreSQLCallbackReceiptStore) List(ctx context.Context, page CallbackReceiptPage) ([]CallbackReceipt, error) {
	if page.BeforeID < 0 || page.Limit < 1 || page.Limit > 100 {
		return nil, ErrInvalidCallbackReceipt
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `
		SELECT `+callbackReceiptColumns+`
		FROM wecom_callback_receipts
		WHERE ($1::bigint = 0 OR id < $1)
		ORDER BY id DESC
		LIMIT $2`, page.BeforeID, page.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	receipts := make([]CallbackReceipt, 0, page.Limit)
	for rows.Next() {
		record, scanErr := scanCallbackReceiptRecord(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		receipts = append(receipts, record.safe())
	}
	return receipts, rows.Err()
}

const callbackReceiptColumns = `id, inbox_id, receipt_kind, target_receipt_id,
	attempt_number, command_digest, event_type, change_type, prior_inbox_status,
	resulting_inbox_status, result_codes, error_code, actor_admin_user_id,
	reason, operation_key_digest, created_at`

const callbackReceiptByAttemptSQL = `
	SELECT ` + callbackReceiptColumns + `
	FROM wecom_callback_receipts
	WHERE inbox_id = $1 AND attempt_number = $2 AND receipt_kind = 'processing'
	FOR UPDATE`

const callbackRetryReceiptByKeySQL = `
	SELECT ` + callbackReceiptColumns + `
	FROM wecom_callback_receipts
	WHERE actor_admin_user_id = $1 AND operation_key_digest = $2
	  AND receipt_kind = 'retry_requested'
	FOR UPDATE`

const callbackReceiptByIDSQL = `
	SELECT ` + callbackReceiptColumns + `
	FROM wecom_callback_receipts
	WHERE id = $1`

const callbackReceiptByIDForShareSQL = callbackReceiptByIDSQL + ` FOR SHARE`

type callbackReceiptRecord struct {
	CallbackReceipt
	CommandDigest      [32]byte
	ActorAdminUserID   int64
	Reason             string
	OperationKeyDigest [32]byte
}

type callbackReceiptScanner interface {
	Scan(...any) error
}

func scanCallbackReceiptRecord(scanner callbackReceiptScanner) (callbackReceiptRecord, error) {
	var record callbackReceiptRecord
	var kind, priorStatus, resultingStatus string
	var targetReceiptID, actorAdminUserID *int64
	var commandDigest, operationKeyDigest []byte
	var resultCodes []string
	err := scanner.Scan(
		&record.ID, &record.InboxID, &kind, &targetReceiptID,
		&record.AttemptNumber, &commandDigest, &record.EventType,
		&record.ChangeType, &priorStatus, &resultingStatus, &resultCodes,
		&record.ErrorCode, &actorAdminUserID, &record.Reason,
		&operationKeyDigest, &record.CreatedAt,
	)
	if err != nil {
		return callbackReceiptRecord{}, err
	}
	if len(commandDigest) != len(record.CommandDigest) || (operationKeyDigest != nil && len(operationKeyDigest) != len(record.OperationKeyDigest)) {
		return callbackReceiptRecord{}, ErrCallbackReceiptConflict
	}
	record.Kind = CallbackReceiptKind(kind)
	record.PriorInboxStatus = webhook.Status(priorStatus)
	record.ResultingInboxStatus = webhook.Status(resultingStatus)
	copy(record.CommandDigest[:], commandDigest)
	if targetReceiptID != nil {
		record.TargetReceiptID = *targetReceiptID
	}
	if actorAdminUserID != nil {
		record.ActorAdminUserID = *actorAdminUserID
	}
	if operationKeyDigest != nil {
		copy(record.OperationKeyDigest[:], operationKeyDigest)
	}
	record.ResultCodes = make([]CallbackResultCode, len(resultCodes))
	for index, result := range resultCodes {
		record.ResultCodes[index] = CallbackResultCode(result)
	}
	record.CreatedAt = record.CreatedAt.UTC()
	return record, nil
}

func (record callbackReceiptRecord) safe() CallbackReceipt {
	receipt := record.CallbackReceipt
	receipt.ResultCodes = append([]CallbackResultCode(nil), receipt.ResultCodes...)
	return receipt
}

func sameProcessingReceipt(record callbackReceiptRecord, command AppendCallbackProcessingReceipt) bool {
	return record.Kind == CallbackReceiptProcessing &&
		record.InboxID == command.InboxID && record.AttemptNumber == command.AttemptNumber &&
		record.CommandDigest == command.CommandDigest && record.EventType == command.EventType &&
		record.ChangeType == command.ChangeType && record.PriorInboxStatus == webhook.StatusProcessing &&
		record.ResultingInboxStatus == command.ResultingInboxStatus &&
		sameCallbackResults(record.ResultCodes, command.ResultCodes) && record.ErrorCode == command.ErrorCode
}

func sameRetryReceipt(record callbackReceiptRecord, command BeginCallbackRetry) bool {
	return record.Kind == CallbackReceiptRetryRequested &&
		record.TargetReceiptID == command.TargetReceiptID &&
		record.AttemptNumber == command.ExpectedAttempt &&
		record.CommandDigest == command.CommandDigest &&
		record.PriorInboxStatus == command.ExpectedInboxStatus &&
		record.ResultingInboxStatus == webhook.StatusRetryable &&
		record.ActorAdminUserID == command.ActorAdminUserID &&
		record.Reason == command.Reason && record.OperationKeyDigest == command.OperationKeyDigest
}

func validProcessingReceipt(command AppendCallbackProcessingReceipt) bool {
	if command.InboxID < 1 || command.AttemptNumber < 1 || !digestPresent(command.CommandDigest) ||
		!validCallbackToken(command.EventType) || !validCallbackToken(command.ChangeType) ||
		!validCallbackErrorCode(command.ErrorCode) || len(command.ResultCodes) > 4 || !validCallbackResults(command.ResultCodes) {
		return false
	}
	switch command.ResultingInboxStatus {
	case webhook.StatusProcessed:
		return len(command.ResultCodes) > 0 && command.ErrorCode == ""
	case webhook.StatusRetryable:
		return len(command.ResultCodes) == 0 && command.ErrorCode != ""
	case webhook.StatusFailed:
		return len(command.ResultCodes) == 1 && command.ResultCodes[0] == CallbackFailedTerminal && command.ErrorCode != ""
	default:
		return false
	}
}

func validRetryReceipt(command BeginCallbackRetry) bool {
	return command.TargetReceiptID > 0 && command.ExpectedAttempt > 0 &&
		(command.ExpectedInboxStatus == webhook.StatusRetryable || command.ExpectedInboxStatus == webhook.StatusFailed) &&
		command.ActorAdminUserID > 0 && validCallbackReason(command.Reason) &&
		digestPresent(command.OperationKeyDigest) && digestPresent(command.CommandDigest)
}

func canonicalCallbackResultCodes(values []CallbackResultCode) []CallbackResultCode {
	canonical := append([]CallbackResultCode(nil), values...)
	sort.Slice(canonical, func(left, right int) bool { return canonical[left] < canonical[right] })
	return canonical
}

func validCallbackResults(values []CallbackResultCode) bool {
	seen := make(map[CallbackResultCode]struct{}, len(values))
	for _, value := range values {
		switch value {
		case CallbackCustomerCreated, CallbackCustomerResolved, CallbackRelationshipActivated,
			CallbackRelationshipDeactivated, CallbackChannelAttributed, CallbackChannelUnmatched,
			CallbackChannelAmbiguous, CallbackIdentityConflict, CallbackIgnored, CallbackFailedTerminal:
		default:
			return false
		}
		if _, duplicate := seen[value]; duplicate {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func sameCallbackResults(left, right []CallbackResultCode) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func validCallbackToken(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if !(character == '_' || character >= 'a' && character <= 'z' || character >= '0' && character <= '9') {
			return false
		}
	}
	return true
}

func validCallbackErrorCode(value string) bool {
	if value == "" {
		return true
	}
	if len(value) > 120 || !callbackErrorCodeAlphaNumeric(value[0]) {
		return false
	}
	for _, character := range value[1:] {
		if !(character == '_' || character == '.' || character == ':' || character == '-' ||
			character >= 'a' && character <= 'z' || character >= '0' && character <= '9') {
			return false
		}
	}
	return true
}

func validCallbackReason(value string) bool {
	return value != "" && len(value) <= 500 && strings.TrimSpace(value) == value &&
		strings.IndexFunc(value, unicode.IsControl) < 0
}

func callbackErrorCodeAlphaNumeric(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= '0' && value <= '9'
}

func digestPresent(value [32]byte) bool {
	return value != [32]byte{}
}
