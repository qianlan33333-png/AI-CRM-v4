package outbound

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

var (
	ErrInvalidMessageIntent  = errors.New("invalid outbound message intent")
	ErrMessageIntentConflict = errors.New("outbound message intent conflict")
)

type MessageService struct {
	pool      *pgxpool.Pool
	uow       platformport.UnitOfWork
	effects   effectport.TransactionalAccepter
	projector outboundport.MessageCompletionProjector
	now       func() time.Time
}

func NewMessageService(pool *pgxpool.Pool, uow platformport.UnitOfWork, effects effectport.TransactionalAccepter, projector outboundport.MessageCompletionProjector) (*MessageService, error) {
	if pool == nil || uow == nil || effects == nil || projector == nil {
		return nil, ErrInvalidMessageIntent
	}
	return &MessageService{pool: pool, uow: uow, effects: effects, projector: projector, now: time.Now}, nil
}
func validMessageIntent(in outboundport.MessageIntent) bool {
	sourceValid := (in.SourceKind == "automation_run" || in.SourceKind == "automation_enrollment") && in.AgentID > 0 && in.AgentPublishedVersion > 0
	if in.SourceKind == "audience_direct_push" {
		sourceValid = in.AgentID == 0 && in.AgentPublishedVersion == 0 && len(bytes.TrimSpace(in.ContentSnapshot)) > 0
	}
	return sourceValid && in.SourceID > 0 && in.RunRecipientID > 0 && in.CustomerID > 0 && in.SenderStaffID > 0 && len(in.ContentReference) > 0 && len(in.ContentReference) <= 200 && len(in.ReceiptKey) >= 16 && len(in.ReceiptKey) <= 128 && strings.TrimSpace(in.ReceiptKey) == in.ReceiptKey && in.SourceDigest != ([32]byte{}) && in.TargetDigest != ([32]byte{}) && in.PayloadDigest != ([32]byte{}) && in.PolicyDigest != ([32]byte{}) && validContentSnapshot(in.ContentSnapshot, in.ContentSnapshotDigest)
}

func validContentSnapshot(raw json.RawMessage, digest [32]byte) bool {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return digest == ([32]byte{})
	}
	return digest != ([32]byte{}) && validContentSnapshotJSON(raw)
}

func validContentSnapshotJSON(raw []byte) bool {
	if !json.Valid(raw) {
		return false
	}
	var object map[string]json.RawMessage
	return json.Unmarshal(raw, &object) == nil && object != nil
}

// normalizedContentSnapshot deliberately turns the legacy no-snapshot form
// into SQL NULL. json.RawMessage(nil) is otherwise encoded as JSON null by
// some PostgreSQL codecs, which is not the historical pure-text shape.
func normalizedContentSnapshot(raw json.RawMessage, digest [32]byte) (any, []byte) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, nil
	}
	return raw, digest[:]
}
func messageIntentDigest(in outboundport.MessageIntent) [32]byte {
	scheduledAt := ""
	if !in.ScheduledAt.IsZero() {
		scheduledAt = in.ScheduledAt.UTC().Format(time.RFC3339Nano)
	}
	// Intents accepted before 0089 did not have a content snapshot. Keep their
	// exact canonical form so a lost response can replay the original receipt.
	// New snapshots are explicitly versioned inside the digest input.
	fields := []any{in.SourceKind, in.SourceID, in.RunRecipientID, in.CustomerID, in.SenderStaffID, in.AgentID, in.AgentPublishedVersion, in.ContentReference, hex.EncodeToString(in.SourceDigest[:]), hex.EncodeToString(in.TargetDigest[:]), hex.EncodeToString(in.PayloadDigest[:])}
	if len(bytes.TrimSpace(in.ContentSnapshot)) != 0 {
		fields = append(fields, "content-snapshot-v1", string(in.ContentSnapshot), hex.EncodeToString(in.ContentSnapshotDigest[:]))
	}
	fields = append(fields, hex.EncodeToString(in.PolicyDigest[:]), scheduledAt)
	raw, _ := json.Marshal(fields)
	return sha256.Sum256(raw)
}
func snapshotBytes(value [32]byte) []byte {
	if value == ([32]byte{}) {
		return nil
	}
	return value[:]
}

func digestToEffect(namespace string, value [32]byte) effectport.Digest {
	return effectport.Hash(namespace, hex.EncodeToString(value[:]))
}
func (s *MessageService) AcceptMessageWithin(ctx context.Context, in outboundport.MessageIntent) (outboundport.MessageAcceptance, error) {
	if s == nil || !validMessageIntent(in) {
		return outboundport.MessageAcceptance{}, ErrInvalidMessageIntent
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return outboundport.MessageAcceptance{}, err
	}
	intentDigest := messageIntentDigest(in)
	contentSnapshot, contentSnapshotDigest := normalizedContentSnapshot(in.ContentSnapshot, in.ContentSnapshotDigest)
	keyDigest := sha256.Sum256([]byte(in.ReceiptKey))
	now := s.now().UTC()
	envelope := effectport.Envelope{Owner: effectport.OwnerOutbound, Kind: effectport.KindAutomationMessage, SourceRefDigest: digestToEffect("automation.message.source", in.SourceDigest), TargetRefDigest: digestToEffect("automation.message.target", in.TargetDigest), PayloadDigest: digestToEffect("automation.message.payload", in.PayloadDigest), PolicyVersionHash: digestToEffect("automation.message.policy", in.PolicyDigest)}
	fingerprint := string(envelope.Fingerprint())
	var id int64
	var existingDigest []byte
	var effectID, queueID *string
	err = tx.QueryRow(ctx, `INSERT INTO outbound_message_intents(source_kind,source_id,run_recipient_id,customer_id,sender_staff_id,agent_id,agent_published_version,content_reference,source_digest,target_digest,payload_digest,content_snapshot,content_snapshot_digest,policy_digest,receipt_key_digest,intent_digest,envelope_fingerprint,state,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12::jsonb,$13::bytea,$14,$15,$16,$17,'accepted',$18,$18) ON CONFLICT(receipt_key_digest) DO NOTHING RETURNING id,intent_digest,effect_id,queue_receipt_id`, in.SourceKind, in.SourceID, in.RunRecipientID, in.CustomerID, in.SenderStaffID, in.AgentID, in.AgentPublishedVersion, in.ContentReference, in.SourceDigest[:], in.TargetDigest[:], in.PayloadDigest[:], contentSnapshot, contentSnapshotDigest, in.PolicyDigest[:], keyDigest[:], intentDigest[:], fingerprint, now).Scan(&id, &existingDigest, &effectID, &queueID)
	replayed := false
	if errors.Is(err, pgx.ErrNoRows) {
		replayed = true
		err = tx.QueryRow(ctx, `SELECT id,intent_digest,effect_id,queue_receipt_id FROM outbound_message_intents WHERE receipt_key_digest=$1 FOR UPDATE`, keyDigest[:]).Scan(&id, &existingDigest, &effectID, &queueID)
	}
	if err != nil {
		return outboundport.MessageAcceptance{}, err
	}
	if len(existingDigest) != 32 || string(existingDigest) != string(intentDigest[:]) {
		return outboundport.MessageAcceptance{}, ErrMessageIntentConflict
	}
	if replayed && effectID != nil && queueID != nil {
		return outboundport.MessageAcceptance{MessageIntentID: id, EffectID: *effectID, QueueReceiptID: *queueID, Replayed: true}, nil
	}
	projection, receipt, err := s.effects.AcceptAndQueueWithin(ctx, effectport.AcceptCommand{ReceiptKey: effectport.Hash("outbound.message.accept", in.ReceiptKey), Envelope: envelope, ScheduledAt: in.ScheduledAt})
	if err != nil {
		return outboundport.MessageAcceptance{}, err
	}
	if projection.ID == "" || projection.QueueJobID < 1 || receipt.QueueReceiptID == "" {
		return outboundport.MessageAcceptance{}, ErrMessageIntentConflict
	}
	tag, err := tx.Exec(ctx, `UPDATE outbound_message_intents SET effect_id=$2,queue_receipt_id=$3,state='queued',updated_at=$4 WHERE id=$1 AND effect_id IS NULL`, id, projection.ID, receipt.QueueReceiptID, now)
	if err != nil || tag.RowsAffected() != 1 {
		if err != nil {
			return outboundport.MessageAcceptance{}, err
		}
		return outboundport.MessageAcceptance{}, ErrMessageIntentConflict
	}
	payload, _ := json.Marshal(map[string]any{"message_intent_id": id, "source_kind": in.SourceKind, "source_id": in.SourceID, "run_recipient_id": in.RunRecipientID, "effect_id": projection.ID, "state": "queued"})
	payloadDigest := sha256.Sum256(payload)
	eventKey := sha256.Sum256([]byte("accepted:" + strconv.FormatInt(id, 10)))
	if _, err = tx.Exec(ctx, `INSERT INTO outbound_message_audit_events(message_intent_id,operation,payload_digest,occurred_at) VALUES($1,'accept',$2,$3)`, id, payloadDigest[:], now); err != nil {
		return outboundport.MessageAcceptance{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO outbound_message_outbox(event_type,message_intent_id,payload,idempotency_digest,occurred_at) VALUES('outbound.message.queued.v1',$1,$2::jsonb,$3,$4)`, id, payload, eventKey[:], now); err != nil {
		return outboundport.MessageAcceptance{}, err
	}
	return outboundport.MessageAcceptance{MessageIntentID: id, EffectID: projection.ID, QueueReceiptID: receipt.QueueReceiptID, Replayed: replayed}, nil
}
func (s *MessageService) MessageExecution(ctx context.Context, fingerprint string) (outboundport.MessageExecution, bool, error) {
	if s == nil || !effectport.ValidDigest(effectport.Digest(fingerprint)) {
		return outboundport.MessageExecution{}, false, ErrInvalidMessageIntent
	}
	var out outboundport.MessageExecution
	var payloadDigest []byte
	var contentSnapshot, contentSnapshotDigest []byte
	err := s.uow.Within(ctx, func(txctx context.Context) error {
		tx, e := platformpostgres.RequireTransaction(txctx)
		if e != nil {
			return e
		}
		return tx.QueryRow(txctx, `SELECT id,run_recipient_id,customer_id,sender_staff_id,agent_id,agent_published_version,content_reference,payload_digest,content_snapshot,content_snapshot_digest FROM outbound_message_intents WHERE envelope_fingerprint=$1 AND state IN ('queued','attempted')`, fingerprint).Scan(&out.MessageIntentID, &out.RunRecipientID, &out.CustomerID, &out.SenderStaffID, &out.AgentID, &out.AgentPublishedVersion, &out.ContentReference, &payloadDigest, &contentSnapshot, &contentSnapshotDigest)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return out, false, nil
	}
	if err == nil {
		if len(payloadDigest) != len(out.PayloadDigest) {
			return outboundport.MessageExecution{}, false, ErrMessageIntentConflict
		}
		copy(out.PayloadDigest[:], payloadDigest)
		if (contentSnapshot == nil) != (contentSnapshotDigest == nil) || (contentSnapshot != nil && (!validContentSnapshotJSON(contentSnapshot) || len(contentSnapshotDigest) != len(out.ContentSnapshotDigest))) {
			return outboundport.MessageExecution{}, false, ErrMessageIntentConflict
		}
		if contentSnapshot != nil {
			copy(out.ContentSnapshotDigest[:], contentSnapshotDigest)
			if !validContentSnapshot(contentSnapshot, out.ContentSnapshotDigest) {
				return outboundport.MessageExecution{}, false, ErrMessageIntentConflict
			}
			out.ContentSnapshot = append(json.RawMessage(nil), contentSnapshot...)
		}
	}
	return out, err == nil, err
}
func (s *MessageService) CompleteEffect(ctx context.Context, effectRef string, envelope effectport.Envelope, attempt effectport.Attempt, result effectport.AdapterResult) error {
	if s == nil || envelope.Kind != effectport.KindAutomationMessage || !effectport.ValidDigest(result.ReceiptDigest) {
		return ErrInvalidMessageIntent
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	state := outboundport.CompletionFinalFailed
	switch result.Completion {
	case effectport.StateExecuted:
		state = outboundport.CompletionProviderAccepted
	case effectport.StateUnknown:
		state = outboundport.CompletionOutcomeUnknown
	case effectport.StateRetryable:
		state = outboundport.CompletionRetryableFailed
	case effectport.StateFinalFailed:
		state = outboundport.CompletionFinalFailed
	case effectport.StateReconciled:
		state = outboundport.CompletionReconciled
	default:
		return ErrInvalidMessageIntent
	}
	receiptRaw, err := effectDigestBytes(result.ReceiptDigest)
	if err != nil {
		return err
	}
	var id int64
	var recipientID int64
	err = tx.QueryRow(ctx, `UPDATE outbound_message_intents SET state=$2,attempt_count=$3,receipt_digest=$4,updated_at=$5 WHERE effect_id=$1 RETURNING id,run_recipient_id`, effectRef, state, attempt.Number, receiptRaw, s.now().UTC()).Scan(&id, &recipientID)
	if err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]any{"message_intent_id": id, "run_recipient_id": recipientID, "effect_id": effectRef, "state": state, "attempt_count": attempt.Number})
	eventKey := sha256.Sum256([]byte("complete:" + effectRef + ":" + strconv.FormatInt(attempt.Generation, 10)))
	payloadDigest := sha256.Sum256(payload)
	if _, err = tx.Exec(ctx, `INSERT INTO outbound_message_audit_events(message_intent_id,operation,payload_digest,occurred_at) VALUES($1,'complete',$2,$3)`, id, payloadDigest[:], s.now().UTC()); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO outbound_message_outbox(event_type,message_intent_id,payload,idempotency_digest,occurred_at) VALUES('outbound.message.completed.v1',$1,$2::jsonb,$3,$4) ON CONFLICT(event_type,idempotency_digest) DO NOTHING`, id, payload, eventKey[:], s.now().UTC()); err != nil {
		return err
	}
	var digest [32]byte
	copy(digest[:], receiptRaw)
	return s.projector.ProjectMessageCompletion(ctx, outboundport.MessageCompletion{EffectID: effectRef, State: state, ReceiptDigest: digest, AttemptCount: attempt.Number})
}
func effectDigestBytes(v effectport.Digest) ([]byte, error) {
	if !effectport.ValidDigest(v) {
		return nil, ErrInvalidMessageIntent
	}
	return hex.DecodeString(string(v)[7:])
}

var _ outboundport.TransactionalMessageAccepter = (*MessageService)(nil)
var _ outboundport.MessageExecutionReader = (*MessageService)(nil)
var _ effectport.CompletionSink = (*MessageService)(nil)
