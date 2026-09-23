package store

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	aiassistantdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/domain"
	ai "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/port"
	effect "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	outbound "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	platform "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"strconv"
	"strings"
	"time"
)

func (r *Repository) ExcelPlan(ctx context.Context, key string) (ai.PlanID, error) {
	tx, err := platform.RequireTransaction(ctx)
	if err != nil {
		return 0, err
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "excel-import:"+key); err != nil {
		return 0, err
	}
	var id ai.PlanID
	err = tx.QueryRow(ctx, `SELECT plan_id FROM ai_assistant_excel_imports WHERE batch_key=$1`, key).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	return id, err
}
func (r *Repository) BindExcelPlan(ctx context.Context, key, digest string, id ai.PlanID) error {
	tx, err := platform.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO ai_assistant_excel_imports(batch_key,plan_id,file_digest,created_at) VALUES($1,$2,$3,clock_timestamp())`, key, id, digest)
	return err
}

// BindOperationExcelBatch makes a newly created review plan visible in the
// operation-cycle detail without making operationcycle own, or write, an AI
// Assistant table. It is deliberately transaction-only.
func (r *Repository) BindOperationExcelBatch(ctx context.Context, key, digest, strategyKey string, id ai.PlanID, actor int64, now time.Time, rows []ai.ExcelBatchRow) error {
	tx, err := platform.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	if id < 1 || actor < 1 || strategyKey == "" || now.IsZero() || len(rows) == 0 {
		return ErrInvalid
	}
	if _, err = tx.Exec(ctx, `INSERT INTO ai_assistant_excel_imports(batch_key,plan_id,file_digest,operation_cycle_strategy_key,source_origin,content_revision,created_at)
		VALUES($1,$2,$3,$4,'excel',1,$5)`, key, id, digest, strategyKey, now.UTC()); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE ai_assistant_plan_recipients SET excel_batch_content_revision=1
		WHERE plan_id=$1 AND excel_batch_content_revision IS NULL`, id); err != nil {
		return err
	}
	segments := make([]string, 0, len(rows))
	for _, row := range rows {
		segments = append(segments, row.Segment)
	}
	if _, err = tx.Exec(ctx, `WITH ordered AS (
		SELECT id,row_number() OVER (ORDER BY id) AS ordinal
		FROM ai_assistant_plan_recipients WHERE plan_id=$1 AND excel_batch_content_revision=1
	), supplied AS (SELECT segment,ordinal FROM unnest($2::text[]) WITH ORDINALITY AS values(segment,ordinal))
		UPDATE ai_assistant_plan_recipients recipient SET excel_segment=supplied.segment
		FROM ordered JOIN supplied USING (ordinal) WHERE recipient.id=ordered.id`, id, segments); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO ai_assistant_excel_batch_versions(plan_id,content_revision,file_digest,created_by,created_at)
		VALUES($1,1,$2,$3,$4)`, id, digest, actor, now.UTC())
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO ai_assistant_excel_batch_version_covers(plan_id,content_revision,cover_digest,created_by,created_at)
		VALUES($1,1,'',$2,$3)`, id, actor, now.UTC())
	return err
}

func (r *Repository) ExcelBatch(ctx context.Context, id ai.PlanID, lock bool) (ai.ExcelBatchMeta, error) {
	tx, err := platform.RequireTransaction(ctx)
	if err != nil {
		return ai.ExcelBatchMeta{}, err
	}
	query := `SELECT plan_id,batch_key,COALESCE(operation_cycle_strategy_key,''),source_origin,file_digest,content_revision,
		COALESCE((SELECT cover.cover_digest FROM ai_assistant_excel_batch_version_covers cover WHERE cover.plan_id=ai_assistant_excel_imports.plan_id AND cover.content_revision=ai_assistant_excel_imports.content_revision ORDER BY cover.created_at DESC,cover.cover_digest DESC LIMIT 1),''),
		COALESCE((SELECT cover.cover_image_id FROM ai_assistant_excel_batch_version_covers cover WHERE cover.plan_id=ai_assistant_excel_imports.plan_id AND cover.content_revision=ai_assistant_excel_imports.content_revision ORDER BY cover.created_at DESC,cover.cover_digest DESC LIMIT 1),0),created_at
		FROM ai_assistant_excel_imports WHERE plan_id=$1`
	if lock {
		query += ` FOR UPDATE`
	}
	var value ai.ExcelBatchMeta
	err = tx.QueryRow(ctx, query, id).Scan(&value.PlanID, &value.BatchKey, &value.StrategyKey, &value.SourceOrigin, &value.FileDigest, &value.Revision, &value.CoverDigest, &value.CoverImageID, &value.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ai.ExcelBatchMeta{}, ErrNotFound
	}
	return value, err
}

func (r *Repository) ExcelBatchByKey(ctx context.Context, key string) (ai.ExcelBatchMeta, error) {
	tx, err := platform.RequireTransaction(ctx)
	if err != nil {
		return ai.ExcelBatchMeta{}, err
	}
	var value ai.ExcelBatchMeta
	err = tx.QueryRow(ctx, `SELECT plan_id,batch_key,COALESCE(operation_cycle_strategy_key,''),source_origin,file_digest,content_revision,
		COALESCE((SELECT cover.cover_digest FROM ai_assistant_excel_batch_version_covers cover WHERE cover.plan_id=ai_assistant_excel_imports.plan_id AND cover.content_revision=ai_assistant_excel_imports.content_revision ORDER BY cover.created_at DESC,cover.cover_digest DESC LIMIT 1),''),
		COALESCE((SELECT cover.cover_image_id FROM ai_assistant_excel_batch_version_covers cover WHERE cover.plan_id=ai_assistant_excel_imports.plan_id AND cover.content_revision=ai_assistant_excel_imports.content_revision ORDER BY cover.created_at DESC,cover.cover_digest DESC LIMIT 1),0),created_at
		FROM ai_assistant_excel_imports WHERE batch_key=$1`, key).Scan(&value.PlanID, &value.BatchKey, &value.StrategyKey, &value.SourceOrigin, &value.FileDigest, &value.Revision, &value.CoverDigest, &value.CoverImageID, &value.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ai.ExcelBatchMeta{}, ErrNotFound
	}
	return value, err
}

func (r *Repository) ListOperationExcelBatches(ctx context.Context, strategyKey string, limit int) ([]ai.ExcelBatchMeta, error) {
	tx, err := platform.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	if strategyKey == "" || limit < 1 || limit > 100 {
		return nil, ErrInvalid
	}
	rows, err := tx.Query(ctx, `SELECT plan_id,batch_key,COALESCE(operation_cycle_strategy_key,''),source_origin,file_digest,content_revision,
		COALESCE((SELECT cover.cover_digest FROM ai_assistant_excel_batch_version_covers cover WHERE cover.plan_id=ai_assistant_excel_imports.plan_id AND cover.content_revision=ai_assistant_excel_imports.content_revision ORDER BY cover.created_at DESC,cover.cover_digest DESC LIMIT 1),''),
		COALESCE((SELECT cover.cover_image_id FROM ai_assistant_excel_batch_version_covers cover WHERE cover.plan_id=ai_assistant_excel_imports.plan_id AND cover.content_revision=ai_assistant_excel_imports.content_revision ORDER BY cover.created_at DESC,cover.cover_digest DESC LIMIT 1),0),created_at
		FROM ai_assistant_excel_imports WHERE operation_cycle_strategy_key=$1 ORDER BY created_at DESC,plan_id DESC LIMIT $2`, strategyKey, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ai.ExcelBatchMeta{}
	for rows.Next() {
		var value ai.ExcelBatchMeta
		if err = rows.Scan(&value.PlanID, &value.BatchKey, &value.StrategyKey, &value.SourceOrigin, &value.FileDigest, &value.Revision, &value.CoverDigest, &value.CoverImageID, &value.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, value)
	}
	return items, rows.Err()
}

// ListLatestOperationExcelBatchOverviews is a single bounded query for the
// newest batch and its current content summary per strategy key. It keeps the
// Excel list read in AI Assistant ownership and avoids an N+1 plan/summary
// read from the browser or composition root.
func (r *Repository) ListLatestOperationExcelBatchOverviews(ctx context.Context, strategyKeys []string) ([]ai.ExcelBatchOverview, error) {
	tx, err := platform.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	if len(strategyKeys) == 0 || len(strategyKeys) > 100 {
		return nil, ErrInvalid
	}
	for _, key := range strategyKeys {
		if strings.TrimSpace(key) == "" {
			return nil, ErrInvalid
		}
	}
	rows, err := tx.Query(ctx, `
		WITH latest AS (
			SELECT DISTINCT ON (operation_cycle_strategy_key)
				plan_id,batch_key,operation_cycle_strategy_key,source_origin,file_digest,content_revision,created_at
			FROM ai_assistant_excel_imports
			WHERE operation_cycle_strategy_key = ANY($1)
			ORDER BY operation_cycle_strategy_key,created_at DESC,plan_id DESC
		)
		SELECT latest.plan_id,latest.batch_key,latest.operation_cycle_strategy_key,latest.source_origin,latest.file_digest,latest.content_revision,
			COALESCE((SELECT cover.cover_digest FROM ai_assistant_excel_batch_version_covers cover WHERE cover.plan_id=latest.plan_id AND cover.content_revision=latest.content_revision ORDER BY cover.created_at DESC,cover.cover_digest DESC LIMIT 1),''),
			COALESCE((SELECT cover.cover_image_id FROM ai_assistant_excel_batch_version_covers cover WHERE cover.plan_id=latest.plan_id AND cover.content_revision=latest.content_revision ORDER BY cover.created_at DESC,cover.cover_digest DESC LIMIT 1),0),
			latest.created_at,plan.state,plan.version,plan.source_kind,
			count(recipient.id),
			count(recipient.id) FILTER (WHERE recipient.excel_excluded),
			count(recipient.id) FILTER (WHERE recipient.id IS NOT NULL AND COALESCE(content.content_payload->1->'excel_card'->>'title','')=''),
			count(recipient.id) FILTER (WHERE NOT recipient.excel_excluded AND COALESCE(content.content_payload->1->'excel_card'->>'title','')<>'')
		FROM latest
		JOIN ai_assistant_plans plan ON plan.id=latest.plan_id
		LEFT JOIN ai_assistant_plan_recipients recipient ON recipient.plan_id=latest.plan_id AND recipient.excel_batch_content_revision=latest.content_revision
		LEFT JOIN ai_assistant_content_versions content ON content.id=recipient.current_content_version_id
		GROUP BY latest.plan_id,latest.batch_key,latest.operation_cycle_strategy_key,latest.source_origin,latest.file_digest,latest.content_revision,latest.created_at,plan.state,plan.version,plan.source_kind
		ORDER BY latest.created_at DESC,latest.plan_id DESC`, strategyKeys)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]ai.ExcelBatchOverview, 0, len(strategyKeys))
	for rows.Next() {
		var value ai.ExcelBatchOverview
		if err = rows.Scan(
			&value.Meta.PlanID, &value.Meta.BatchKey, &value.Meta.StrategyKey, &value.Meta.SourceOrigin, &value.Meta.FileDigest, &value.Meta.Revision, &value.Meta.CoverDigest, &value.Meta.CoverImageID, &value.Meta.CreatedAt,
			&value.State, &value.PlanVersion, &value.SourceKind,
			&value.Summary.TotalRows, &value.Summary.ExcludedRows, &value.Summary.EmptyTitleRows, &value.Summary.ExpectedTasks,
		); err != nil {
			return nil, err
		}
		items = append(items, value)
	}
	return items, rows.Err()
}

func (r *Repository) LinkExcelBatch(ctx context.Context, id ai.PlanID, strategyKey string) error {
	tx, err := platform.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE ai_assistant_excel_imports SET operation_cycle_strategy_key=$2
		WHERE plan_id=$1 AND operation_cycle_strategy_key IS NULL`, id, strategyKey)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}

func (r *Repository) ExcelBatchRowAttributes(ctx context.Context, planID ai.PlanID, recipientID ai.RecipientID, lock bool) (ai.ExcelBatchRowAttributes, error) {
	tx, err := platform.RequireTransaction(ctx)
	if err != nil {
		return ai.ExcelBatchRowAttributes{}, err
	}
	query := `SELECT excel_batch_content_revision,excel_segment,excel_excluded
		FROM ai_assistant_plan_recipients WHERE plan_id=$1 AND id=$2`
	if lock {
		query += ` FOR UPDATE`
	}
	var value ai.ExcelBatchRowAttributes
	err = tx.QueryRow(ctx, query, planID, recipientID).Scan(&value.ContentRevision, &value.Segment, &value.Excluded)
	if errors.Is(err, pgx.ErrNoRows) {
		return ai.ExcelBatchRowAttributes{}, ErrNotFound
	}
	return value, err
}

func (r *Repository) PatchExcelBatchRowAttributes(ctx context.Context, planID ai.PlanID, recipientID ai.RecipientID, revision int, segment string, excluded bool, now time.Time) error {
	tx, err := platform.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	if revision < 1 || (segment != "" && segment != "A" && segment != "B" && segment != "C" && segment != "D") {
		return ErrInvalid
	}
	tag, err := tx.Exec(ctx, `UPDATE ai_assistant_plan_recipients SET excel_segment=$4,excel_excluded=$5,updated_at=$6
		WHERE plan_id=$1 AND id=$2 AND excel_batch_content_revision=$3`, planID, recipientID, revision, segment, excluded, now.UTC())
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}

// ResetOperationExcelReview invalidates every current row review after any
// mutable content, segment, exclusion, or cover change. Historical revisions
// are deliberately excluded from this projection.
func (r *Repository) ResetOperationExcelReview(ctx context.Context, planID ai.PlanID, revision int, now time.Time) (ai.Plan, error) {
	tx, err := platform.RequireTransaction(ctx)
	if err != nil {
		return ai.Plan{}, err
	}
	if revision < 1 || now.IsZero() {
		return ai.Plan{}, ErrInvalid
	}
	if _, err = tx.Exec(ctx, `UPDATE ai_assistant_plan_recipients
		SET review_state=CASE WHEN excel_excluded THEN 'rejected' ELSE 'pending_review' END,
		    version=version+1,updated_at=$3
		WHERE plan_id=$1 AND excel_batch_content_revision=$2`, planID, revision, now.UTC()); err != nil {
		return ai.Plan{}, err
	}
	var total, excluded int
	if err = tx.QueryRow(ctx, `SELECT count(*),count(*) FILTER (WHERE excel_excluded)
		FROM ai_assistant_plan_recipients WHERE plan_id=$1 AND excel_batch_content_revision=$2`, planID, revision).Scan(&total, &excluded); err != nil {
		return ai.Plan{}, err
	}
	if total < 1 {
		return ai.Plan{}, ErrConflict
	}
	return scanPlan(tx.QueryRow(ctx, `UPDATE ai_assistant_plans
		SET state='pending_review',version=version+1,target_count=$2,pending_count=$3,approved_count=0,rejected_count=$4,ineligible_count=0,needs_attention_count=0,rejected_reason=NULL,updated_at=$5
		WHERE id=$1 RETURNING `+planColumns, planID, total, total-excluded, excluded, now.UTC()))
}

func (r *Repository) ExcelBatchSummary(ctx context.Context, planID ai.PlanID, revision int) (ai.ExcelBatchSummary, error) {
	tx, err := platform.RequireTransaction(ctx)
	if err != nil {
		return ai.ExcelBatchSummary{}, err
	}
	var summary ai.ExcelBatchSummary
	err = tx.QueryRow(ctx, `SELECT count(*),count(*) FILTER (WHERE excel_excluded),
		count(*) FILTER (WHERE COALESCE(content.content_payload->1->'excel_card'->>'title','')=''),
		count(*) FILTER (WHERE NOT excel_excluded AND COALESCE(content.content_payload->1->'excel_card'->>'title','')<>'')
		FROM ai_assistant_plan_recipients recipient
		JOIN ai_assistant_content_versions content ON content.id=recipient.current_content_version_id
		WHERE recipient.plan_id=$1 AND recipient.excel_batch_content_revision=$2`, planID, revision).Scan(&summary.TotalRows, &summary.ExcludedRows, &summary.EmptyTitleRows, &summary.ExpectedTasks)
	if err != nil {
		return ai.ExcelBatchSummary{}, err
	}
	return summary, nil
}

func (r *Repository) ListOperationExcelBatchVersions(ctx context.Context, planID ai.PlanID, limit int) ([]ai.ExcelBatchVersion, error) {
	tx, err := platform.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	if planID < 1 || limit < 1 || limit > 100 {
		return nil, ErrInvalid
	}
	rows, err := tx.Query(ctx, `SELECT version.plan_id,version.content_revision,version.file_digest,
		COALESCE((SELECT cover.cover_digest FROM ai_assistant_excel_batch_version_covers cover
		 WHERE cover.plan_id=version.plan_id AND cover.content_revision=version.content_revision
			 ORDER BY cover.created_at DESC,cover.cover_digest DESC LIMIT 1),version.cover_digest),
		COALESCE((SELECT cover.cover_image_id FROM ai_assistant_excel_batch_version_covers cover
		 WHERE cover.plan_id=version.plan_id AND cover.content_revision=version.content_revision
			 ORDER BY cover.created_at DESC,cover.cover_digest DESC LIMIT 1),version.cover_image_id),version.created_by,version.created_at
		FROM ai_assistant_excel_batch_versions version WHERE version.plan_id=$1
		ORDER BY version.content_revision DESC LIMIT $2`, planID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ai.ExcelBatchVersion{}
	for rows.Next() {
		var item ai.ExcelBatchVersion
		if err = rows.Scan(&item.PlanID, &item.ContentRevision, &item.FileDigest, &item.CoverDigest, &item.CoverImageID, &item.CreatedBy, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *Repository) ListOperationExcelBatchRecipients(ctx context.Context, planID ai.PlanID, revision int, afterID int64, limit int) ([]ai.Recipient, error) {
	tx, err := platform.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	if planID < 1 || revision < 1 || afterID < 0 || limit < 1 || limit > 50 {
		return nil, ErrInvalid
	}
	rows, err := tx.Query(ctx, `SELECT r.deferred_target,r.id,r.plan_id,r.customer_id,r.staff_id,r.review_state,r.execution_state,r.version,r.current_content_version_id,COALESCE(b.external_effect_id,''),r.updated_at
		FROM ai_assistant_plan_recipients r LEFT JOIN ai_assistant_effect_bindings b ON b.recipient_id=r.id
		WHERE r.plan_id=$1 AND r.excel_batch_content_revision=$2 AND r.id>$3 ORDER BY r.id LIMIT $4`, planID, revision, afterID, limit+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ai.Recipient{}
	for rows.Next() {
		var recipient ai.Recipient
		if err = rows.Scan(&recipient.DeferredTarget, &recipient.ID, &recipient.PlanID, &recipient.CustomerID, &recipient.StaffID, &recipient.ReviewState, &recipient.ExecutionState, &recipient.Version, &recipient.ContentVersionID, &recipient.EffectID, &recipient.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, recipient)
	}
	return items, rows.Err()
}

// AppendOperationExcelCover records the effective cover separately from the
// immutable upload row. One content revision can legitimately have several
// cover edits; no historical read has to inspect mutable current content.
func (r *Repository) AppendOperationExcelCover(ctx context.Context, planID ai.PlanID, revision int, cover effect.Digest, actor int64, now time.Time) error {
	return r.AppendOperationExcelMediaCover(ctx, planID, revision, 0, cover, actor, now)
}

// AppendOperationExcelMediaCover records an opaque Media image ID with the
// exact byte digest. Legacy Python covers deliberately remain image_id=0.
func (r *Repository) AppendOperationExcelMediaCover(ctx context.Context, planID ai.PlanID, revision int, imageID int64, cover effect.Digest, actor int64, now time.Time) error {
	tx, err := platform.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	if planID < 1 || revision < 1 || imageID < 0 || actor < 1 || now.IsZero() || !effect.ValidDigest(cover) {
		return ErrInvalid
	}
	_, err = tx.Exec(ctx, `INSERT INTO ai_assistant_excel_batch_version_covers(plan_id,content_revision,cover_digest,cover_image_id,created_by,created_at)
		VALUES($1,$2,$3,$4,$5,$6)`, planID, revision, string(cover), imageID, actor, now.UTC())
	return err
}

func (r *Repository) ListUnlinkedExcelPlans(ctx context.Context, limit int) ([]ai.Plan, error) {
	tx, err := platform.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	if limit < 1 || limit > 100 {
		return nil, ErrInvalid
	}
	rows, err := tx.Query(ctx, `SELECT p.id,p.name,p.source_kind,p.source_digest,p.state,p.version,p.target_count,p.pending_count,p.approved_count,p.rejected_count,p.ineligible_count,p.needs_attention_count,p.created_by,p.created_actor_kind,p.created_actor_ref,p.created_at,p.updated_at FROM ai_assistant_plans p
		JOIN ai_assistant_excel_imports i ON i.plan_id=p.id
		WHERE i.operation_cycle_strategy_key IS NULL ORDER BY p.created_at DESC,p.id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ai.Plan{}
	for rows.Next() {
		value, scanErr := scanPlan(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, value)
	}
	return items, rows.Err()
}

func (r *Repository) ReplaceOperationExcelBatch(ctx context.Context, plan ai.Plan, batch ai.ExcelBatchMeta, scope string, nextDigest effect.Digest, rows []ai.ExcelBatchRow, actor int64, now time.Time) (ai.Plan, error) {
	tx, err := platform.RequireTransaction(ctx)
	if err != nil {
		return ai.Plan{}, err
	}
	if plan.ID < 1 || batch.PlanID != plan.ID || batch.Revision < 1 || !strings.HasPrefix(scope, "wechat-open-platform:") || !effect.ValidDigest(nextDigest) || len(rows) == 0 || len(rows) > ai.MaxRecipients || actor < 1 || now.IsZero() {
		return ai.Plan{}, ErrInvalid
	}
	var cover string
	err = tx.QueryRow(ctx, `SELECT COALESCE((content.content_payload->1->'excel_card'->>'cover_digest'), '')
		FROM ai_assistant_plan_recipients recipient
		JOIN ai_assistant_content_versions content ON content.id=recipient.current_content_version_id
		WHERE recipient.plan_id=$1 AND recipient.excel_batch_content_revision=$2 ORDER BY recipient.id LIMIT 1`, plan.ID, batch.Revision).Scan(&cover)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return ai.Plan{}, err
	}
	for index := range rows {
		rows[index].Card.CoverDigest = effect.Digest(cover)
		rows[index].Card.CoverImageID = batch.CoverImageID
	}
	if err = insertExcelRows(ctx, tx, plan.ID, batch.Revision+1, scope, rows, actor, now); err != nil {
		return ai.Plan{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE ai_assistant_excel_imports SET content_revision=content_revision+1,file_digest=$2
		WHERE plan_id=$1 AND content_revision=$3`, plan.ID, string(nextDigest), batch.Revision); err != nil {
		return ai.Plan{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO ai_assistant_excel_batch_versions(plan_id,content_revision,file_digest,cover_digest,cover_image_id,created_by,created_at)
		VALUES($1,$2,$3,$4,$5,$6,$7)`, plan.ID, batch.Revision+1, string(nextDigest), cover, batch.CoverImageID, actor, now.UTC()); err != nil {
		return ai.Plan{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO ai_assistant_excel_batch_version_covers(plan_id,content_revision,cover_digest,cover_image_id,created_by,created_at)
		VALUES($1,$2,$3,$4,$5,$6)`, plan.ID, batch.Revision+1, cover, batch.CoverImageID, actor, now.UTC()); err != nil {
		return ai.Plan{}, err
	}
	digest, err := digestBytes(nextDigest)
	if err != nil {
		return ai.Plan{}, err
	}
	updated, err := scanPlan(tx.QueryRow(ctx, `UPDATE ai_assistant_plans
		SET source_digest=$2,state='pending_review',version=version+1,target_count=$3,pending_count=$3,approved_count=0,rejected_count=0,ineligible_count=0,needs_attention_count=0,rejected_reason=NULL,updated_at=$4
		WHERE id=$1 AND version=$5
		RETURNING `+planColumns, plan.ID, digest, len(rows), now.UTC(), plan.Version))
	if err != nil {
		return ai.Plan{}, err
	}
	return updated, nil
}

func insertExcelRows(ctx context.Context, tx pgx.Tx, planID ai.PlanID, revision int, scope string, rows []ai.ExcelBatchRow, actor int64, now time.Time) error {
	for _, row := range rows {
		if !row.Valid() {
			return ErrInvalid
		}
		var recipientID ai.RecipientID
		if err := tx.QueryRow(ctx, `INSERT INTO ai_assistant_plan_recipients(plan_id,customer_id,staff_id,deferred_target,excel_batch_content_revision,excel_segment,created_at,updated_at)
			VALUES($1,0,0,$2,$3,$4,$5,$5) RETURNING id`, planID, ai.DeferredTarget{UnionID: row.UnionID, Scope: scope, SenderUserID: row.SenderUserID}, revision, row.Segment, now.UTC()).Scan(&recipientID); err != nil {
			return err
		}
		payload, digest, err := aiassistantdomain.FreezeContent([]ai.ContentBlock{{Kind: ai.ContentText, Text: row.Text}, {Kind: ai.ContentMiniProgram, ExcelCard: &row.Card}})
		if err != nil {
			return ErrInvalid
		}
		digestRaw, err := digestBytes(digest)
		if err != nil {
			return err
		}
		var contentID ai.ContentVersionID
		if err = tx.QueryRow(ctx, `INSERT INTO ai_assistant_content_versions(recipient_id,version,content_digest,content_payload,created_by,created_at)
			VALUES($1,1,$2,$3::jsonb,$4,$5) RETURNING id`, recipientID, digestRaw, payload, actor, now.UTC()).Scan(&contentID); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE ai_assistant_plan_recipients SET current_content_version_id=$2 WHERE id=$1`, recipientID, contentID); err != nil {
			return err
		}
	}
	return nil
}
func (r *Repository) LoadDeferredTarget(ctx context.Context, ref string) (ai.DeferredTarget, error) {
	parts := strings.Split(ref, ":")
	if len(parts) != 4 || parts[0] != "aiassistant" {
		return ai.DeferredTarget{}, ErrInvalid
	}
	ids := make([]int64, 3)
	for i := range ids {
		n, e := strconv.ParseInt(parts[i+1], 10, 64)
		if e != nil || n < 1 {
			return ai.DeferredTarget{}, ErrInvalid
		}
		ids[i] = n
	}
	var t *ai.DeferredTarget
	err := r.pool.QueryRow(ctx, `SELECT deferred_target FROM ai_assistant_plan_recipients WHERE plan_id=$1 AND id=$2 AND current_content_version_id=$3 AND review_state='approved' AND execution_state<>'not_accepted'`, ids[0], ids[1], ids[2]).Scan(&t)
	if err != nil || t == nil || !t.Valid() {
		return ai.DeferredTarget{}, ErrInvalid
	}
	return *t, nil
}
func (r *Repository) ExcelPlans(ctx context.Context) ([]ai.PlanID, error) {
	rows, err := r.pool.Query(ctx, `SELECT plan_id FROM ai_assistant_excel_imports ORDER BY plan_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ai.PlanID{}
	for rows.Next() {
		var id ai.PlanID
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
func (r *Repository) SaveExcelSnapshot(ctx context.Context, id ai.PlanID, key string) error {
	tx, err := platform.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE ai_assistant_excel_imports SET approved_snapshot=$2 WHERE plan_id=$1 AND approved_snapshot=''`, id, key)
	return err
}
func (r *Repository) ExcelApproval(ctx context.Context, id ai.PlanID) (string, time.Time, error) {
	var key string
	var at time.Time
	err := r.pool.QueryRow(ctx, `SELECT i.approved_snapshot,COALESCE(min(d.occurred_at),p.created_at) FROM ai_assistant_excel_imports i JOIN ai_assistant_plans p ON p.id=i.plan_id LEFT JOIN ai_assistant_review_decisions d ON d.plan_id=p.id AND d.recipient_id IS NULL AND d.decision='approved' WHERE i.plan_id=$1 GROUP BY i.approved_snapshot,p.created_at`, id).Scan(&key, &at)
	return key, at, err
}

func (r *Repository) RecordExcelDelivery(ctx context.Context, recipient ai.Recipient, v outbound.PrivateMessageDelivery) error {
	if recipient.DeferredTarget == nil || v.Status == nil || *v.Status == 0 {
		return ErrInvalid
	}
	return r.uow.Within(ctx, func(tx context.Context) error {
		items, err := r.ListEffectBindings(tx, recipient.PlanID)
		if err != nil {
			return err
		}
		for _, binding := range items {
			if binding.RecipientID == recipient.ID {
				if binding.DeliveryProven {
					return nil
				}
				state := ai.ExecutionFinalFailed
				proof := false
				if *v.Status == 1 {
					if v.SentAt == nil {
						return ErrInvalid
					}
					state = ai.ExecutionDeliveryProven
					proof = true
				}
				receipt := effect.Hash("excel.delivery", v.MessageID, v.SenderUserID, v.ExternalUserID, strconv.Itoa(*v.Status))
				return r.CompleteExternalEffect(tx, binding.EffectID, state, true, proof, receipt, binding.AttemptCount, binding.Generation, binding.Fence, time.Now().UTC())
			}
		}
		return ErrNotFound
	})
}
