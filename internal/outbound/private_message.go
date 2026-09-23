package outbound

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

type PrivateMessageWriter struct {
	effects effectport.TransactionalAccepter
	pool    *pgxpool.Pool
}

type PrivateMessageCompletionProjector interface {
	CompletePrivateMessage(context.Context, string, string, time.Time) error
}

func (w *PrivateMessageWriter) CompletePrivateMessage(ctx context.Context, effectID, state string, now time.Time) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE outbound_private_message_intents SET state=$2,updated_at=$3 WHERE external_effect_id=$1`, effectID, state, now.UTC())
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("outbound private message intent not found")
	}
	return nil
}

func NewPrivateMessageWriter(effects effectport.TransactionalAccepter) (*PrivateMessageWriter, error) {
	if effects == nil {
		return nil, errors.New("external effects accepter is required")
	}
	return &PrivateMessageWriter{effects: effects}, nil
}

func NewPrivateMessageRepository(pool *pgxpool.Pool, effects effectport.TransactionalAccepter) (*PrivateMessageWriter, error) {
	writer, err := NewPrivateMessageWriter(effects)
	if err != nil || pool == nil {
		return nil, errors.New("private message repository dependencies are required")
	}
	writer.pool = pool
	return writer, nil
}

func (w *PrivateMessageWriter) PrivateMessageIntentForEnvelope(ctx context.Context, envelope effectport.Envelope) (PrivateMessageIntent, error) {
	if w == nil || w.pool == nil || envelope.Kind != effectport.KindOutboundMessage {
		return PrivateMessageIntent{}, errors.New("private message intent reader unavailable")
	}
	var value PrivateMessageIntent
	var digest []byte
	err := w.pool.QueryRow(ctx, `SELECT deferred_target_reference,customer_id,staff_id,payload_reference,payload_digest FROM outbound_private_message_intents WHERE source_digest=$1 AND target_digest=$2 AND payload_digest=$3 AND policy_hash=$4`, digestBytes(envelope.SourceRefDigest), digestBytes(envelope.TargetRefDigest), digestBytes(envelope.PayloadDigest), digestBytes(envelope.PolicyVersionHash)).Scan(&value.DeferredTargetReference, &value.CustomerID, &value.StaffID, &value.PayloadReference, &digest)
	if err != nil {
		return PrivateMessageIntent{}, err
	}
	value.PayloadDigest = effectport.Digest("sha256:" + hex.EncodeToString(digest))
	return value, nil
}

func (w *PrivateMessageWriter) WritePrivateMessageIntentWithin(ctx context.Context, command outboundport.PrivateMessageIntentCommand) (outboundport.PrivateMessageIntentResult, error) {
	if w == nil || w.effects == nil || !command.Valid() {
		return outboundport.PrivateMessageIntentResult{}, outboundport.ErrInvalidPrivateMessageIntent
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return outboundport.PrivateMessageIntentResult{}, err
	}
	var prior outboundport.PrivateMessageIntentResult
	var customerID, staffID int64
	var sourceReference, payloadReference, deferredReference string
	var sourceDigest, targetDigest, payloadDigest, policyHash []byte
	err = tx.QueryRow(ctx, `SELECT deferred_target_reference,id,external_effect_id,customer_id,staff_id,source_reference,payload_reference,source_digest,target_digest,payload_digest,policy_hash FROM outbound_private_message_intents WHERE receipt_key=$1 FOR UPDATE`, digestBytes(command.ReceiptKey)).Scan(&deferredReference, &prior.IntentID, &prior.EffectID, &customerID, &staffID, &sourceReference, &payloadReference, &sourceDigest, &targetDigest, &payloadDigest, &policyHash)
	if err == nil {
		if deferredReference != command.DeferredTargetReference || customerID != int64(command.CustomerID) || staffID != command.StaffID || sourceReference != command.SourceReference || payloadReference != command.PayloadReference ||
			!bytes.Equal(sourceDigest, digestBytes(command.SourceDigest)) || !bytes.Equal(targetDigest, digestBytes(command.TargetDigest)) || !bytes.Equal(payloadDigest, digestBytes(command.PayloadDigest)) || !bytes.Equal(policyHash, digestBytes(command.PolicyHash)) {
			return outboundport.PrivateMessageIntentResult{}, errors.New("outbound private message payload mismatch")
		}
		prior.Replayed = true
		return prior, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return outboundport.PrivateMessageIntentResult{}, err
	}
	envelope := effectport.Envelope{Owner: effectport.OwnerOutbound, Kind: effectport.KindOutboundMessage, SourceRefDigest: command.SourceDigest, TargetRefDigest: command.TargetDigest, PayloadDigest: command.PayloadDigest, PolicyVersionHash: command.PolicyHash}
	projection, receipt, err := w.effects.AcceptAndQueueWithin(ctx, effectport.AcceptCommand{ReceiptKey: command.ReceiptKey, Envelope: envelope, Lane: command.Lane})
	if err != nil {
		return outboundport.PrivateMessageIntentResult{}, err
	}
	if projection.ID == "" || projection.QueueJobID < 1 || receipt.QueueReceiptID == "" {
		return outboundport.PrivateMessageIntentResult{}, errors.New("incomplete external effect acceptance")
	}
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO outbound_private_message_intents(source_reference,customer_id,staff_id,payload_reference,source_digest,target_digest,payload_digest,policy_hash,receipt_key,external_effect_id,state,deferred_target_reference)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'queued',$11) RETURNING id`, command.SourceReference, command.CustomerID, command.StaffID, command.PayloadReference, digestBytes(command.SourceDigest), digestBytes(command.TargetDigest), digestBytes(command.PayloadDigest), digestBytes(command.PolicyHash), digestBytes(command.ReceiptKey), projection.ID, command.DeferredTargetReference).Scan(&id)
	if err != nil {
		return outboundport.PrivateMessageIntentResult{}, err
	}
	return outboundport.PrivateMessageIntentResult{IntentID: id, EffectID: projection.ID}, nil
}

func digestBytes(value effectport.Digest) []byte {
	raw, _ := hex.DecodeString(strings.TrimPrefix(string(value), "sha256:"))
	return raw
}

var _ outboundport.PrivateMessageIntentWriter = (*PrivateMessageWriter)(nil)
