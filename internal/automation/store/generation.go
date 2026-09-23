package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	automationapp "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/app"
	automationdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/domain"
	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
)

var generationFailureCode = regexp.MustCompile(`^[a-z0-9_]{1,80}$`)

func validGenerationItem(item automationdomain.GenerationItem) bool {
	return item.RunID > 0 && item.CustomerID > 0 && item.SenderStaffID > 0 && item.AgentID > 0 && item.AgentPublishedVersion > 0 && item.AgentCode != "" && len(item.AgentCode) <= 120 && item.RolePrompt != "" && len(item.RolePrompt) <= 16000 && item.TaskPrompt != "" && len(item.TaskPrompt) <= 16000 && item.Context.Valid() && item.ModelPolicy.Valid() && item.SourceDigest != ([32]byte{}) && item.TargetDigest != ([32]byte{}) && item.PayloadDigest != ([32]byte{}) && item.PolicyDigest != ([32]byte{}) && item.ReceiptKeyDigest != ([32]byte{}) && item.State == "accepted" && !item.CreatedAt.IsZero()
}

func (r *Repository) CreateGenerationItems(ctx context.Context, items []automationdomain.GenerationItem) ([]automationdomain.GenerationItem, error) {
	t, err := tx(ctx)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, automationapp.ErrRuntimeInvalid
	}
	for index := range items {
		item := &items[index]
		if !validGenerationItem(*item) {
			return nil, automationapp.ErrRuntimeInvalid
		}
		contextRaw, marshalErr := json.Marshal(item.Context)
		if marshalErr != nil {
			return nil, marshalErr
		}
		policyRaw, marshalErr := json.Marshal(item.ModelPolicy)
		if marshalErr != nil {
			return nil, marshalErr
		}
		err = t.QueryRow(ctx, `INSERT INTO automation_generation_items(run_id,customer_id,sender_staff_id,agent_id,agent_published_version,agent_code,role_prompt,task_prompt,context_snapshot,model_policy,source_digest,target_digest,payload_digest,policy_digest,receipt_key_digest,state,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9::jsonb,$10::jsonb,$11,$12,$13,$14,$15,'accepted',$16,$16) RETURNING id`, item.RunID, item.CustomerID, item.SenderStaffID, item.AgentID, item.AgentPublishedVersion, item.AgentCode, item.RolePrompt, item.TaskPrompt, contextRaw, policyRaw, item.SourceDigest[:], item.TargetDigest[:], item.PayloadDigest[:], item.PolicyDigest[:], item.ReceiptKeyDigest[:], item.CreatedAt.UTC()).Scan(&item.ID)
		if unique(err) {
			return nil, automationapp.ErrRuntimeConflict
		}
		if err != nil {
			return nil, err
		}
	}
	return items, nil
}

func (r *Repository) BindGenerationEffect(ctx context.Context, itemID int64, effectID string, now time.Time) error {
	t, err := tx(ctx)
	if err != nil {
		return err
	}
	if itemID < 1 || !strings.HasPrefix(effectID, "eer_") {
		return automationapp.ErrRuntimeInvalid
	}
	tag, err := t.Exec(ctx, `UPDATE automation_generation_items SET effect_id=$2,state='queued',updated_at=$3 WHERE id=$1 AND state='accepted' AND effect_id IS NULL`, itemID, effectID, now.UTC())
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return automationapp.ErrRuntimeConflict
	}
	return generationAudit(ctx, t, itemID, "accept", now, effectID, "queued")
}

func generationAudit(ctx context.Context, t pgx.Tx, itemID int64, operation string, at time.Time, fields ...string) error {
	digest := sha256.Sum256([]byte(strings.Join(append([]string{operation}, fields...), "\x00")))
	_, err := t.Exec(ctx, `INSERT INTO automation_generation_audit_events(generation_item_id,operation,payload_digest,occurred_at) VALUES($1,$2,$3,$4)`, itemID, operation, digest[:], at.UTC())
	return err
}

const generationColumns = `id,run_id,customer_id,sender_staff_id,agent_id,agent_published_version,agent_code,role_prompt,task_prompt,context_snapshot,model_policy,source_digest,target_digest,payload_digest,policy_digest,receipt_key_digest,COALESCE(effect_id,''),state,COALESCE(generated_text,''),COALESCE(result_digest,''::bytea),COALESCE(failure_code,''),attempt_count,created_at,updated_at,completed_at`

func scanGeneration(row pgx.Row) (automationdomain.GenerationItem, error) {
	var item automationdomain.GenerationItem
	var contextRaw, policyRaw []byte
	var source, target, payload, policy, key, result []byte
	err := row.Scan(&item.ID, &item.RunID, &item.CustomerID, &item.SenderStaffID, &item.AgentID, &item.AgentPublishedVersion, &item.AgentCode, &item.RolePrompt, &item.TaskPrompt, &contextRaw, &policyRaw, &source, &target, &payload, &policy, &key, &item.EffectID, &item.State, &item.GeneratedText, &result, &item.FailureCode, &item.AttemptCount, &item.CreatedAt, &item.UpdatedAt, &item.CompletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return item, automationapp.ErrRuntimeNotFound
	}
	if err != nil {
		return item, err
	}
	if len(source) != 32 || len(target) != 32 || len(payload) != 32 || len(policy) != 32 || len(key) != 32 || (len(result) != 0 && len(result) != 32) || json.Unmarshal(contextRaw, &item.Context) != nil || json.Unmarshal(policyRaw, &item.ModelPolicy) != nil || !item.Context.Valid() || !item.ModelPolicy.Valid() {
		return item, automationapp.ErrRuntimeConflict
	}
	copy(item.SourceDigest[:], source)
	copy(item.TargetDigest[:], target)
	copy(item.PayloadDigest[:], payload)
	copy(item.PolicyDigest[:], policy)
	copy(item.ReceiptKeyDigest[:], key)
	copy(item.ResultDigest[:], result)
	return item, nil
}

func (r *Repository) GenerationByEffect(ctx context.Context, effectID string) (automationdomain.GenerationItem, error) {
	t, err := tx(ctx)
	if err != nil {
		return automationdomain.GenerationItem{}, err
	}
	if !strings.HasPrefix(effectID, "eer_") {
		return automationdomain.GenerationItem{}, automationapp.ErrRuntimeInvalid
	}
	return scanGeneration(t.QueryRow(ctx, `SELECT `+generationColumns+` FROM automation_generation_items WHERE effect_id=$1`, effectID))
}

func completionState(state effectport.State) (string, bool) {
	switch state {
	case effectport.StateExecuted:
		return "executed", true
	case effectport.StateRetryable:
		return "retryable_failed", true
	case effectport.StateFinalFailed:
		return "final_failed", true
	case effectport.StateUnknown:
		return "outcome_unknown", true
	default:
		return "", false
	}
}

func artifactText(artifact effectport.ResultArtifact, item automationdomain.GenerationItem) (string, error) {
	if artifact.Kind != "automation.ai_agent_generate.text.v1" || !artifact.Valid() {
		return "", automationapp.ErrRuntimeConflict
	}
	text := strings.TrimSpace(string(artifact.Payload))
	if !automationport.ValidGeneratedText(text, item.RolePrompt, item.TaskPrompt) {
		return "", automationapp.ErrRuntimeConflict
	}
	return text, nil
}

func digestRaw(value effectport.Digest) ([]byte, error) {
	if !effectport.ValidDigest(value) {
		return nil, automationapp.ErrRuntimeInvalid
	}
	return hex.DecodeString(string(value)[7:])
}

// SettleGeneration is called only from the External Effects completion UoW.
// It locks the run while deciding whether the last successful generation can
// create its one pending-review plan, so concurrent completions cannot race.
func (r *Repository) SettleGeneration(ctx context.Context, completion automationport.GenerationCompletion) (automationdomain.GenerationItem, automationdomain.RuntimeRun, bool, error) {
	t, err := tx(ctx)
	if err != nil {
		return automationdomain.GenerationItem{}, automationdomain.RuntimeRun{}, false, err
	}
	state, ok := completionState(completion.State)
	if !ok || completion.Attempt.Number < 1 || completion.CompletedAt.IsZero() || !effectport.ValidDigest(completion.ReceiptDigest) || !strings.HasPrefix(completion.EffectID, "eer_") {
		return automationdomain.GenerationItem{}, automationdomain.RuntimeRun{}, false, automationapp.ErrRuntimeInvalid
	}
	item, err := scanGeneration(t.QueryRow(ctx, `SELECT `+generationColumns+` FROM automation_generation_items WHERE effect_id=$1 FOR UPDATE`, completion.EffectID))
	if err != nil {
		return item, automationdomain.RuntimeRun{}, false, fmt.Errorf("lock generation item: %w", err)
	}
	run, err := scanRun(t.QueryRow(ctx, `SELECT `+runColumns+` FROM automation_runs WHERE id=$1 FOR UPDATE`, item.RunID))
	if err != nil {
		return item, run, false, fmt.Errorf("lock generation run: %w", err)
	}
	if item.State == "executed" || item.State == "retryable_failed" || item.State == "final_failed" || item.State == "outcome_unknown" || item.State == "cancelled" {
		return item, run, false, nil
	}
	var text string
	var resultDigest []byte
	failure := ""
	if state == "executed" {
		text, err = artifactText(completion.Artifact, item)
		if err != nil {
			state, failure = "final_failed", "generation_output_invalid"
		} else {
			resultDigest, err = digestRaw(completion.Artifact.Digest)
			if err != nil {
				return item, run, false, err
			}
		}
	} else {
		failure = completion.FailureCode
		if !generationFailureCode.MatchString(failure) {
			failure = "generation_" + strings.TrimSuffix(state, "_failed")
		}
	}
	completed := completion.CompletedAt.UTC()
	tag, err := t.Exec(ctx, `UPDATE automation_generation_items SET state=$2,generated_text=$3::text,result_digest=$4::bytea,failure_code=$5::text,attempt_count=$6,updated_at=$7,completed_at=$7 WHERE id=$1 AND state IN ('accepted','queued')`, item.ID, state, nullableText(text), nullableBytes(resultDigest), nullableText(failure), completion.Attempt.Number, completed)
	if err != nil {
		return item, run, false, fmt.Errorf("settle generation item: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return item, run, false, automationapp.ErrRuntimeConflict
	}
	if err = generationAudit(ctx, t, item.ID, "complete", completed, completion.EffectID, state); err != nil {
		return item, run, false, fmt.Errorf("audit generation completion: %w", err)
	}
	item.State, item.GeneratedText, item.FailureCode, item.AttemptCount, item.UpdatedAt, item.CompletedAt = state, text, failure, completion.Attempt.Number, completed, &completed
	var queued, unknown, succeeded int64
	err = t.QueryRow(ctx, `SELECT count(*) FILTER (WHERE state IN ('accepted','queued')),count(*) FILTER (WHERE state='outcome_unknown'),count(*) FILTER (WHERE state='executed') FROM automation_generation_items WHERE run_id=$1`, item.RunID).Scan(&queued, &unknown, &succeeded)
	if err != nil {
		return item, run, false, fmt.Errorf("aggregate generation completion: %w", err)
	}
	allFinal := queued == 0
	newRunState := "preparing"
	if unknown > 0 {
		newRunState = "outcome_unknown"
	} else if allFinal && succeeded == 0 {
		newRunState = "partial_failed"
	}
	if _, err = t.Exec(ctx, `UPDATE automation_runs SET state=$2,updated_at=$3::timestamptz,completed_at=CASE WHEN $4 THEN $3::timestamptz ELSE NULL END WHERE id=$1`, item.RunID, newRunState, completed, allFinal && succeeded == 0); err != nil {
		return item, run, false, fmt.Errorf("project generation run: %w", err)
	}
	run, err = scanRun(t.QueryRow(ctx, `SELECT `+runColumns+` FROM automation_runs WHERE id=$1`, item.RunID))
	if err != nil {
		return item, run, false, fmt.Errorf("read generation run: %w", err)
	}
	// Unknown and failed items are terminal exclusions. They must not block the
	// successful subset from entering the existing human-review flow.
	return item, run, allFinal && succeeded > 0 && run.AIPlanID == 0, nil
}

func nullableText(value string) any {
	if value == "" {
		return nil
	}
	return value
}
func nullableBytes(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return value
}

func (r *Repository) GenerationItemsForPlan(ctx context.Context, runID int64) ([]automationdomain.GenerationItem, error) {
	t, err := tx(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := t.Query(ctx, `SELECT `+generationColumns+` FROM automation_generation_items WHERE run_id=$1 AND state='executed' ORDER BY id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []automationdomain.GenerationItem{}
	for rows.Next() {
		item, scanErr := scanGeneration(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *Repository) AttachGenerationPlan(ctx context.Context, runID, planID int64, now time.Time) error {
	t, err := tx(ctx)
	if err != nil {
		return err
	}
	if runID < 1 || planID < 1 {
		return automationapp.ErrRuntimeInvalid
	}
	tag, err := t.Exec(ctx, `UPDATE automation_runs SET ai_plan_id=$2,state='pending_review',updated_at=$3 WHERE id=$1 AND ai_plan_id IS NULL`, runID, planID, now.UTC())
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return automationapp.ErrRuntimeConflict
	}
	return nil
}

func (r *Repository) GenerationProgress(ctx context.Context, runID int64) (automationport.GenerationProgress, error) {
	t, err := tx(ctx)
	if err != nil {
		return automationport.GenerationProgress{}, err
	}
	var progress automationport.GenerationProgress
	err = t.QueryRow(ctx, `SELECT count(*),count(*) FILTER (WHERE state IN ('accepted','queued')),count(*) FILTER (WHERE state='executed'),count(*) FILTER (WHERE state IN ('retryable_failed','final_failed','cancelled')),count(*) FILTER (WHERE state='outcome_unknown') FROM automation_generation_items WHERE run_id=$1`, runID).Scan(&progress.Total, &progress.Queued, &progress.Succeeded, &progress.Failed, &progress.Unknown)
	return progress, err
}

func (r *Repository) GenerationItems(ctx context.Context, runID, cursor int64, limit int) ([]automationport.GenerationItem, string, error) {
	t, err := tx(ctx)
	if err != nil {
		return nil, "", err
	}
	if limit < 1 || limit > 100 {
		return nil, "", automationapp.ErrRuntimeInvalid
	}
	var exists bool
	if err = t.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM automation_runs WHERE id=$1)`, runID).Scan(&exists); err != nil {
		return nil, "", err
	}
	if !exists {
		return nil, "", automationapp.ErrRuntimeNotFound
	}
	rows, err := t.Query(ctx, `SELECT id,run_id,customer_id,sender_staff_id,COALESCE(effect_id,''),state,COALESCE(failure_code,''),attempt_count,COALESCE(generated_text,''),created_at,updated_at,context_snapshot,model_policy FROM automation_generation_items WHERE run_id=$1 AND ($2=0 OR id>$2) ORDER BY id LIMIT $3`, runID, cursor, limit+1)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	items := []automationport.GenerationItem{}
	for rows.Next() {
		var item automationport.GenerationItem
		if err = rows.Scan(&item.ID, &item.RunID, &item.CustomerID, &item.SenderStaffID, &item.EffectID, &item.State, &item.FailureCode, &item.AttemptCount, &item.GeneratedText, &item.CreatedAt, &item.UpdatedAt, &item.ContextSnapshot, &item.ModelPolicySummary); err != nil {
			return nil, "", err
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(items) > limit {
		next = strconv.FormatInt(items[limit-1].ID, 10)
		items = items[:limit]
	}
	return items, next, nil
}
