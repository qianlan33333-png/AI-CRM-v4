package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"time"

	aiassistantdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/domain"
	aiassistantport "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// CreateMachinePlan stores an authenticated machine reference without creating
// or borrowing a numeric administrator identity.
func (r *Repository) CreateMachinePlan(ctx context.Context, aggregate aiassistantdomain.Plan, recipients []aiassistantport.RecipientCandidate, actor aiassistantport.MachineActor, now time.Time) (aiassistantport.Plan, []aiassistantport.Recipient, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return aiassistantport.Plan{}, nil, err
	}
	if r == nil || !aggregate.Valid() || aggregate.Projection.ID != 0 || len(recipients) != aggregate.Projection.TargetCount || !actor.Valid() || aggregate.Projection.CreatedBy != 0 || aggregate.Projection.CreatedActorKind != aiassistantport.MachineActorKind || aggregate.Projection.CreatedActorRef != actor.Reference || now.IsZero() {
		return aiassistantport.Plan{}, nil, ErrInvalid
	}
	sourceDigest, err := digestBytes(aggregate.Projection.SourceDigest)
	if err != nil {
		return aiassistantport.Plan{}, nil, ErrInvalid
	}
	plan := aggregate.Projection
	plan, err = scanPlan(tx.QueryRow(ctx, `INSERT INTO ai_assistant_plans(name,source_kind,source_digest,state,version,target_count,pending_count,approved_count,rejected_count,ineligible_count,needs_attention_count,created_by,created_actor_kind,created_actor_ref,created_at,updated_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,NULL,$12,$13,$14,$14)
		RETURNING `+planColumns,
		plan.Name, plan.SourceKind, sourceDigest, string(plan.State), plan.Version, plan.TargetCount, plan.PendingCount, plan.ApprovedCount, plan.RejectedCount, plan.IneligibleCount, plan.NeedsAttentionCount, actor.Kind, actor.Reference, now.UTC()))
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
		recipient.PlanID, recipient.CustomerID, recipient.StaffID = plan.ID, candidate.CustomerID, candidate.StaffID
		err = tx.QueryRow(ctx, `INSERT INTO ai_assistant_plan_recipients(plan_id,customer_id,staff_id,created_at,updated_at)
			VALUES($1,$2,$3,$4,$4) RETURNING id,review_state,execution_state,version,created_at,updated_at`,
			plan.ID, candidate.CustomerID, candidate.StaffID, now.UTC()).Scan(&recipient.ID, &recipient.ReviewState, &recipient.ExecutionState, &recipient.Version, &createdAt, &recipient.UpdatedAt)
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
		err = tx.QueryRow(ctx, `INSERT INTO ai_assistant_content_versions(recipient_id,version,content_digest,content_payload,created_by,created_actor_kind,created_actor_ref,created_at)
			VALUES($1,1,$2,$3::jsonb,NULL,$4,$5,$6) RETURNING id`, recipient.ID, digestRaw, payload, actor.Kind, actor.Reference, now.UTC()).Scan(&recipient.ContentVersionID)
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

func (r *Repository) AppendMachineEvent(ctx context.Context, event aiassistantport.MachineEvent) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	if !event.Actor.Valid() || event.AggregateID < 1 || event.Type == "" || event.IdempotencyKey == "" || event.OccurredAt.IsZero() || len(event.Payload) == 0 || !json.Valid(event.Payload) {
		return ErrInvalid
	}
	digest := sha256.Sum256([]byte(event.Actor.Reference + ":" + event.IdempotencyKey))
	var recipient any
	if event.RecipientID > 0 {
		recipient = event.RecipientID
	}
	if _, err = tx.Exec(ctx, `INSERT INTO ai_assistant_audit_events(plan_id,recipient_id,operation,actor_id,actor_kind,actor_ref,payload_digest,occurred_at)
		VALUES($1,$2,$3,NULL,$4,$5,$6,$7)`, event.AggregateID, recipient, event.Type, event.Actor.Kind, event.Actor.Reference, digest[:], event.OccurredAt.UTC()); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO ai_assistant_outbox(event_type,plan_id,payload,idempotency_digest,occurred_at)
		VALUES($1,$2,$3::jsonb,$4,$5)`, event.Type, event.AggregateID, event.Payload, digest[:], event.OccurredAt.UTC())
	return err
}

// MachineExecutionSummary reads immutable recipient execution facts under the
// caller's transaction. Creator authorization remains in the app layer, where
// it is checked before this summary is loaded.
func (r *Repository) MachineExecutionSummary(ctx context.Context, planID aiassistantport.PlanID) (aiassistantport.MachineExecutionSummary, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return aiassistantport.MachineExecutionSummary{}, err
	}
	if planID < 1 {
		return aiassistantport.MachineExecutionSummary{}, ErrNotFound
	}
	var summary aiassistantport.MachineExecutionSummary
	err = tx.QueryRow(ctx, `SELECT
		count(*) FILTER (WHERE execution_state='outcome_unknown'),
		count(*) FILTER (WHERE execution_state='retryable_failed')
		FROM ai_assistant_plan_recipients WHERE plan_id=$1`, planID).Scan(&summary.OutcomeUnknownCount, &summary.RetryableFailureCount)
	return summary, err
}
