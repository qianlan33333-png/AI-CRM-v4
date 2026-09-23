// Package store owns only AI Assistant PostgreSQL tables.
package store

import (
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

	aiassistantapp "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/app"
	aiassistantdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/domain"
	aiassistantport "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/port"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

var (
	ErrInvalid  = aiassistantapp.ErrInvalid
	ErrNotFound = aiassistantapp.ErrNotFound
	ErrConflict = aiassistantapp.ErrConflict
)

type Repository struct {
	pool *pgxpool.Pool
	uow  platformport.UnitOfWork
}

func NewPostgreSQL(pool *pgxpool.Pool, uow platformport.UnitOfWork) (*Repository, error) {
	if pool == nil || uow == nil {
		return nil, ErrInvalid
	}
	return &Repository{pool: pool, uow: uow}, nil
}

type Reservation = aiassistantapp.Reservation
type Receipt = aiassistantapp.Receipt

func (r *Repository) UnitOfWork() platformport.UnitOfWork { return r.uow }

func (r *Repository) LoadOutboundContent(ctx context.Context, reference string, expected effectport.Digest) (aiassistantport.ContentVersion, error) {
	parts := strings.Split(reference, ":")
	if len(parts) != 4 || parts[0] != "aiassistant" {
		return aiassistantport.ContentVersion{}, ErrInvalid
	}
	planID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return aiassistantport.ContentVersion{}, ErrInvalid
	}
	recipientID, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return aiassistantport.ContentVersion{}, ErrInvalid
	}
	contentID, err := strconv.ParseInt(parts[3], 10, 64)
	if err != nil {
		return aiassistantport.ContentVersion{}, ErrInvalid
	}
	var content aiassistantport.ContentVersion
	err = r.uow.Within(ctx, func(txctx context.Context) error {
		recipient, value, readErr := r.GetRecipient(txctx, aiassistantport.PlanID(planID), aiassistantport.RecipientID(recipientID), false)
		if readErr != nil {
			return readErr
		}
		if int64(recipient.ContentVersionID) != contentID {
			return ErrConflict
		}
		content = value
		return nil
	})
	if err != nil {
		return aiassistantport.ContentVersion{}, err
	}
	if content.Digest != expected {
		return aiassistantport.ContentVersion{}, ErrConflict
	}
	return content, nil
}

var _ aiassistantport.OutboundPayloadReader = (*Repository)(nil)

func (r *Repository) CreatePlan(ctx context.Context, aggregate aiassistantdomain.Plan, recipients []aiassistantport.RecipientCandidate, actor int64, now time.Time) (aiassistantport.Plan, []aiassistantport.Recipient, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return aiassistantport.Plan{}, nil, err
	}
	if r == nil || !aggregate.Valid() || aggregate.Projection.ID != 0 || len(recipients) != aggregate.Projection.TargetCount || actor < 1 || now.IsZero() {
		return aiassistantport.Plan{}, nil, ErrInvalid
	}
	sourceDigest, err := digestBytes(aggregate.Projection.SourceDigest)
	if err != nil {
		return aiassistantport.Plan{}, nil, ErrInvalid
	}
	plan := aggregate.Projection
	plan, err = scanPlan(tx.QueryRow(ctx, `INSERT INTO ai_assistant_plans(name,source_kind,source_digest,state,version,target_count,pending_count,approved_count,rejected_count,ineligible_count,needs_attention_count,created_by,created_at,updated_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$13)
		RETURNING `+planColumns,
		plan.Name, plan.SourceKind, sourceDigest, string(plan.State), plan.Version, plan.TargetCount, plan.PendingCount, plan.ApprovedCount, plan.RejectedCount, plan.IneligibleCount, plan.NeedsAttentionCount, actor, now.UTC()))
	if err != nil {
		if unique(err) {
			return aiassistantport.Plan{}, nil, ErrConflict
		}
		return aiassistantport.Plan{}, nil, err
	}
	created := make([]aiassistantport.Recipient, 0, len(recipients))
	for _, candidate := range recipients {
		if !candidate.Valid() {
			return aiassistantport.Plan{}, nil, ErrInvalid
		}
		var recipient aiassistantport.Recipient
		var createdAt time.Time
		recipient.DeferredTarget = candidate.DeferredTarget
		recipient.PlanID, recipient.CustomerID, recipient.StaffID = plan.ID, candidate.CustomerID, candidate.StaffID
		err = tx.QueryRow(ctx, `INSERT INTO ai_assistant_plan_recipients(plan_id,customer_id,staff_id,created_at,updated_at,deferred_target)
			VALUES($1,$2,$3,$4,$4,$5) RETURNING id,review_state,execution_state,version,created_at,updated_at`,
			plan.ID, candidate.CustomerID, candidate.StaffID, now.UTC(), candidate.DeferredTarget).Scan(&recipient.ID, &recipient.ReviewState, &recipient.ExecutionState, &recipient.Version, &createdAt, &recipient.UpdatedAt)
		if err != nil {
			if unique(err) {
				return aiassistantport.Plan{}, nil, ErrConflict
			}
			return aiassistantport.Plan{}, nil, err
		}
		payload, digest, freezeErr := aiassistantdomain.FreezeContent(candidate.Content)
		if freezeErr != nil {
			return aiassistantport.Plan{}, nil, ErrInvalid
		}
		digestRaw, _ := digestBytes(digest)
		err = tx.QueryRow(ctx, `INSERT INTO ai_assistant_content_versions(recipient_id,version,content_digest,content_payload,created_by,created_at)
			VALUES($1,1,$2,$3::jsonb,$4,$5) RETURNING id`, recipient.ID, digestRaw, payload, actor, now.UTC()).Scan(&recipient.ContentVersionID)
		if err != nil {
			return aiassistantport.Plan{}, nil, err
		}
		if _, err = tx.Exec(ctx, `UPDATE ai_assistant_plan_recipients SET current_content_version_id=$2 WHERE id=$1`, recipient.ID, recipient.ContentVersionID); err != nil {
			return aiassistantport.Plan{}, nil, err
		}
		created = append(created, recipient)
	}
	return plan, created, nil
}

func (r *Repository) GetPlan(ctx context.Context, id aiassistantport.PlanID, lock bool) (aiassistantport.Plan, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return aiassistantport.Plan{}, err
	}
	if id < 1 {
		return aiassistantport.Plan{}, ErrNotFound
	}
	query := `SELECT ` + planColumns + ` FROM ai_assistant_plans WHERE id=$1`
	if lock {
		query += ` FOR UPDATE`
	}
	return scanPlan(tx.QueryRow(ctx, query, id))
}

func (r *Repository) Reserve(ctx context.Context, input Reservation) (Receipt, bool, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return Receipt{}, false, err
	}
	if strings.TrimSpace(input.Operation) == "" || strings.TrimSpace(input.ActorScope) == "" || input.CreatedAt.IsZero() {
		return Receipt{}, false, ErrInvalid
	}
	var receipt Receipt
	var key, payload []byte
	err = tx.QueryRow(ctx, `INSERT INTO ai_assistant_operation_receipts(operation,actor_scope,key_digest,payload_digest,state,created_at)
		VALUES($1,$2,$3,$4,'reserved',$5) ON CONFLICT(operation,actor_scope,key_digest) DO NOTHING
		RETURNING id,operation,actor_scope,key_digest,payload_digest,state,result_snapshot`, input.Operation, input.ActorScope, input.KeyDigest[:], input.PayloadDigest[:], input.CreatedAt.UTC()).Scan(&receipt.ID, &receipt.Operation, &receipt.ActorScope, &key, &payload, &receipt.State, &receipt.ResultSnapshot)
	if err == nil {
		copy(receipt.KeyDigest[:], key)
		copy(receipt.PayloadDigest[:], payload)
		return receipt, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Receipt{}, false, err
	}
	err = tx.QueryRow(ctx, `SELECT id,operation,actor_scope,key_digest,payload_digest,state,result_snapshot FROM ai_assistant_operation_receipts WHERE operation=$1 AND actor_scope=$2 AND key_digest=$3 FOR UPDATE`, input.Operation, input.ActorScope, input.KeyDigest[:]).Scan(&receipt.ID, &receipt.Operation, &receipt.ActorScope, &key, &payload, &receipt.State, &receipt.ResultSnapshot)
	if err != nil {
		return Receipt{}, false, err
	}
	copy(receipt.KeyDigest[:], key)
	copy(receipt.PayloadDigest[:], payload)
	if receipt.PayloadDigest != input.PayloadDigest {
		return Receipt{}, false, aiassistantapp.ErrIdempotencyConflict
	}
	return receipt, false, nil
}

func (r *Repository) Complete(ctx context.Context, receiptID int64, result json.RawMessage, now time.Time) (Receipt, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return Receipt{}, err
	}
	if receiptID < 1 || len(result) == 0 || !json.Valid(result) || now.IsZero() {
		return Receipt{}, ErrInvalid
	}
	var receipt Receipt
	var key, payload []byte
	err = tx.QueryRow(ctx, `UPDATE ai_assistant_operation_receipts SET state='completed',result_snapshot=$2::jsonb,completed_at=$3 WHERE id=$1 AND state='reserved'
		RETURNING id,operation,actor_scope,key_digest,payload_digest,state,result_snapshot`, receiptID, result, now.UTC()).Scan(&receipt.ID, &receipt.Operation, &receipt.ActorScope, &key, &payload, &receipt.State, &receipt.ResultSnapshot)
	if errors.Is(err, pgx.ErrNoRows) {
		return Receipt{}, ErrConflict
	}
	if err != nil {
		return Receipt{}, err
	}
	copy(receipt.KeyDigest[:], key)
	copy(receipt.PayloadDigest[:], payload)
	return receipt, nil
}

func (r *Repository) AppendEvent(ctx context.Context, event aiassistantport.Event) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	if event.AggregateID < 1 || event.ActorID < 1 || event.Type == "" || event.IdempotencyKey == "" || event.OccurredAt.IsZero() || len(event.Payload) == 0 || !json.Valid(event.Payload) {
		return ErrInvalid
	}
	digest := sha256.Sum256([]byte(event.IdempotencyKey))
	var recipient any
	if event.RecipientID > 0 {
		recipient = event.RecipientID
	}
	if _, err = tx.Exec(ctx, `INSERT INTO ai_assistant_audit_events(plan_id,recipient_id,operation,actor_id,payload_digest,occurred_at) VALUES($1,$2,$3,$4,$5,$6)`, event.AggregateID, recipient, event.Type, event.ActorID, digest[:], event.OccurredAt.UTC()); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO ai_assistant_outbox(event_type,plan_id,payload,idempotency_digest,occurred_at) VALUES($1,$2,$3::jsonb,$4,$5)`, event.Type, event.AggregateID, event.Payload, digest[:], event.OccurredAt.UTC())
	return err
}

func digestBytes(value effectport.Digest) ([]byte, error) {
	if !effectport.ValidDigest(value) {
		return nil, ErrInvalid
	}
	return hex.DecodeString(strings.TrimPrefix(string(value), "sha256:"))
}

func digestFromBytes(value []byte) (effectport.Digest, error) {
	if len(value) != sha256.Size {
		return "", ErrInvalid
	}
	digest := effectport.Digest("sha256:" + hex.EncodeToString(value))
	if !effectport.ValidDigest(digest) {
		return "", ErrInvalid
	}
	return digest, nil
}

func unique(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "unique")
}
