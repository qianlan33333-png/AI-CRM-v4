package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

func reservationActor(in Reservation) (Actor, error) {
	if strings.TrimSpace(in.ActorScope) == "" {
		return Actor{}, ErrInvalid
	}
	if in.ActorKind == "" && in.ActorRef == "" {
		parts := strings.Split(in.ActorScope, ":")
		if len(parts) == 2 && parts[0] == "admin" {
			staffID, err := strconv.ParseInt(parts[1], 10, 64)
			if err == nil {
				actor := adminActor(staffID)
				if actor.Valid() {
					return actor, nil
				}
			}
		}
		return Actor{}, ErrInvalid
	}
	actor := Actor{Kind: in.ActorKind, Reference: in.ActorRef}
	if actor.Kind == "admin" {
		staffID, parseErr := strconv.ParseInt(strings.TrimPrefix(actor.Reference, "admin:"), 10, 64)
		if parseErr != nil {
			return Actor{}, ErrInvalid
		}
		actor.StaffID = staffID
	}
	if !actor.Valid() || actor.Reference != in.ActorScope {
		return Actor{}, ErrInvalid
	}
	return actor, nil
}

func factActor(fact MutationFact) (Actor, error) {
	if fact.ActorKind == "" && fact.ActorRef == "" {
		actor := adminActor(fact.ActorID)
		if actor.Valid() {
			return actor, nil
		}
		return Actor{}, ErrInvalid
	}
	actor := Actor{Kind: fact.ActorKind, Reference: fact.ActorRef, StaffID: fact.ActorID}
	if !actor.Valid() {
		return Actor{}, ErrInvalid
	}
	return actor, nil
}

func (r *Repository) Reserve(ctx context.Context, in Reservation) (Receipt, bool, error) {
	if strings.TrimSpace(in.Operation) == "" || in.CreatedAt.IsZero() {
		return Receipt{}, false, ErrInvalid
	}
	t, err := tx(ctx)
	if err != nil {
		return Receipt{}, false, err
	}
	actor, err := reservationActor(in)
	if err != nil {
		return Receipt{}, false, err
	}
	var id int64
	err = t.QueryRow(ctx, `INSERT INTO segment_audience_operation_receipts(operation,actor_scope,actor_kind,actor_ref,key_digest,payload_digest,state,created_at)
		VALUES($1,$2,$3,$4,$5,$6,'reserved',$7) ON CONFLICT(operation,actor_scope,key_digest) DO NOTHING RETURNING id`, in.Operation, actor.Reference, actor.Kind, actor.Reference, in.KeyDigest[:], in.PayloadDigest[:], in.CreatedAt).Scan(&id)
	if err == nil {
		return Receipt{ID: id, Operation: in.Operation, ActorScope: actor.Reference, ActorKind: actor.Kind, ActorRef: actor.Reference, KeyDigest: in.KeyDigest, PayloadDigest: in.PayloadDigest, State: "reserved", CreatedAt: in.CreatedAt}, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Receipt{}, false, err
	}
	receipt, err := r.receipt(ctx, in.Operation, actor.Reference, in.KeyDigest)
	if err != nil {
		return Receipt{}, false, err
	}
	if receipt.PayloadDigest != in.PayloadDigest || receipt.ActorKind != actor.Kind || receipt.ActorRef != actor.Reference {
		return Receipt{}, false, ErrConflict
	}
	return receipt, false, nil
}

func (r *Repository) receipt(ctx context.Context, operation, actorScope string, keyDigest [32]byte) (Receipt, error) {
	t, err := tx(ctx)
	if err != nil {
		return Receipt{}, err
	}
	var out Receipt
	var key, payload []byte
	err = t.QueryRow(ctx, `SELECT id,operation,actor_scope,actor_kind,actor_ref,key_digest,payload_digest,state,result_snapshot,created_at,completed_at
		FROM segment_audience_operation_receipts WHERE operation=$1 AND actor_scope=$2 AND key_digest=$3`, operation, actorScope, keyDigest[:]).Scan(&out.ID, &out.Operation, &out.ActorScope, &out.ActorKind, &out.ActorRef, &key, &payload, &out.State, &out.ResultSnapshot, &out.CreatedAt, &out.CompletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Receipt{}, ErrNotFound
	}
	if err != nil {
		return Receipt{}, err
	}
	actor, actorErr := reservationActor(Reservation{ActorScope: out.ActorScope, ActorKind: out.ActorKind, ActorRef: out.ActorRef})
	if len(key) != sha256.Size || len(payload) != sha256.Size || actorErr != nil || actor.Reference != out.ActorRef || out.ActorScope != out.ActorRef {
		return Receipt{}, ErrConflict
	}
	copy(out.KeyDigest[:], key)
	copy(out.PayloadDigest[:], payload)
	return out, nil
}

func (r *Repository) Complete(ctx context.Context, id int64, result json.RawMessage, now time.Time) (Receipt, error) {
	if id < 1 || now.IsZero() || !jsonObject(result) {
		return Receipt{}, ErrInvalid
	}
	t, err := tx(ctx)
	if err != nil {
		return Receipt{}, err
	}
	command, err := t.Exec(ctx, `UPDATE segment_audience_operation_receipts SET state='completed',result_snapshot=$2::jsonb,completed_at=$3 WHERE id=$1 AND state='reserved'`, id, result, now)
	if err != nil {
		return Receipt{}, err
	}
	if command.RowsAffected() != 1 {
		return Receipt{}, ErrConflict
	}
	var operation, scope string
	var keyBytes []byte
	if err = t.QueryRow(ctx, `SELECT operation,actor_scope,key_digest FROM segment_audience_operation_receipts WHERE id=$1`, id).Scan(&operation, &scope, &keyBytes); err != nil || len(keyBytes) != sha256.Size {
		if err != nil {
			return Receipt{}, err
		}
		return Receipt{}, ErrConflict
	}
	var key [32]byte
	copy(key[:], keyBytes)
	return r.receipt(ctx, operation, scope, key)
}

func (r *Repository) AppendMutationFacts(ctx context.Context, fact MutationFact) (int64, error) {
	if fact.ResourceID < 1 || strings.TrimSpace(fact.Operation) == "" || strings.TrimSpace(fact.EventType) == "" || strings.TrimSpace(fact.IdempotencyKey) == "" || fact.OccurredAt.IsZero() || !validKind(fact.ResourceKind) || !jsonObject(fact.Payload) {
		return 0, ErrInvalid
	}
	actor, err := factActor(fact)
	if err != nil {
		return 0, err
	}
	t, err := tx(ctx)
	if err != nil {
		return 0, err
	}
	payloadDigest := sha256.Sum256(fact.Payload)
	idempotencyDigest := sha256.Sum256([]byte(fact.IdempotencyKey))
	var auditID int64
	err = t.QueryRow(ctx, `INSERT INTO segment_audience_audit_events(resource_kind,resource_id,operation,actor_id,actor_kind,actor_ref,occurred_at,payload_digest)
		VALUES($1,$2,$3,NULLIF($4,0),$5,$6,$7,$8) RETURNING id`, fact.ResourceKind, fact.ResourceID, fact.Operation, actor.StaffID, actor.Kind, actor.Reference, fact.OccurredAt, payloadDigest[:]).Scan(&auditID)
	if err != nil {
		return 0, err
	}
	_, err = t.Exec(ctx, `INSERT INTO segment_audience_outbox(event_type,aggregate_kind,aggregate_id,payload,idempotency_digest,occurred_at)
		VALUES($1,$2,$3,$4::jsonb,$5,$6)`, fact.EventType, fact.ResourceKind, fact.ResourceID, fact.Payload, idempotencyDigest[:], fact.OccurredAt)
	if unique(err) {
		return 0, ErrConflict
	}
	return auditID, err
}

func validKind(kind string) bool {
	return kind == "core_operations" || kind == "core_recommendation" || kind == "group" || kind == "package" || kind == "configuration" || kind == "refresh_run" || kind == "snapshot" || kind == "webhook_receipt" || kind == "binding" || kind == "sender_set" || kind == "schedule" || kind == "member_event_batch" || kind == "member_exit_batch"
}
func jsonObject(raw json.RawMessage) bool {
	var object map[string]json.RawMessage
	return json.Unmarshal(raw, &object) == nil && object != nil
}
