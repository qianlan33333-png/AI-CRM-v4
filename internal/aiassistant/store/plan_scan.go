package store

import (
	"errors"

	"github.com/jackc/pgx/v5"

	aiassistantport "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/port"
)

const planColumns = `id,name,source_kind,source_digest,state,version,target_count,pending_count,approved_count,rejected_count,ineligible_count,needs_attention_count,created_by,created_actor_kind,created_actor_ref,created_at,updated_at`

type planRow interface {
	Scan(...any) error
}

func scanPlan(row planRow) (aiassistantport.Plan, error) {
	var plan aiassistantport.Plan
	var digest []byte
	var createdBy *int64
	var actorKind, actorRef *string
	err := row.Scan(
		&plan.ID, &plan.Name, &plan.SourceKind, &digest, &plan.State, &plan.Version,
		&plan.TargetCount, &plan.PendingCount, &plan.ApprovedCount, &plan.RejectedCount,
		&plan.IneligibleCount, &plan.NeedsAttentionCount, &createdBy, &actorKind, &actorRef,
		&plan.CreatedAt, &plan.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return aiassistantport.Plan{}, ErrNotFound
	}
	if err != nil {
		return aiassistantport.Plan{}, err
	}
	if createdBy != nil {
		plan.CreatedBy = *createdBy
	}
	if actorKind != nil {
		plan.CreatedActorKind = *actorKind
	}
	if actorRef != nil {
		plan.CreatedActorRef = *actorRef
	}
	plan.SourceDigest, err = digestFromBytes(digest)
	if err != nil {
		return aiassistantport.Plan{}, err
	}
	return plan, nil
}
