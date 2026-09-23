package store

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
	"time"
)

const recommendationColumns = `id,customer_id,expected_epoch,actor_id,preview,prompt_version,products,dispatch,COALESCE(effect_id,''),state,COALESCE(failure_code,''),reason,evidence,COALESCE(chosen_product_id,0),created_at,completed_at`

func scanRecommendation(row pgx.Row) (v segmentport.CoreRecommendation, e error) {
	var products []byte
	e = row.Scan(&v.ID, &v.CustomerID, &v.ExpectedEpoch, &v.ActorID, &v.Preview, &v.PromptVersion, &products, &v.Dispatch, &v.EffectID, &v.State, &v.FailureCode, &v.Reason, &v.Evidence, &v.ChosenProductID, &v.CreatedAt, &v.CompletedAt)
	if errors.Is(e, pgx.ErrNoRows) {
		return v, ErrNotFound
	}
	if e == nil {
		e = json.Unmarshal(products, &v.Products)
	}
	return
}
func (r *Repository) CoreCustomerEpoch(ctx context.Context, id int64) (epoch int64, assigned bool, e error) {
	t, e := tx(ctx)
	if e != nil {
		return
	}
	e = t.QueryRow(ctx, `SELECT COALESCE(max(id),0),COALESCE(bool_or(ended_at IS NULL),false) FROM segment_core_assignments WHERE customer_id=$1`, id).Scan(&epoch, &assigned)
	return
}
func (r *Repository) CreateCoreRecommendation(ctx context.Context, v segmentport.CoreRecommendation) (segmentport.CoreRecommendation, error) {
	t, e := tx(ctx)
	if e != nil {
		return v, e
	}
	raw, _ := json.Marshal(v.Products)
	return scanRecommendation(t.QueryRow(ctx, `INSERT INTO segment_core_recommendations(customer_id,expected_epoch,actor_id,preview,prompt_version,products,dispatch,state,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING `+recommendationColumns, v.CustomerID, v.ExpectedEpoch, v.ActorID, v.Preview, v.PromptVersion, raw, v.Dispatch, v.State, v.CreatedAt))
}
func (r *Repository) BindCoreRecommendation(ctx context.Context, id int64, effectID string) error {
	t, e := tx(ctx)
	if e != nil {
		return e
	}
	tag, e := t.Exec(ctx, `UPDATE segment_core_recommendations SET effect_id=$2,state='queued' WHERE id=$1 AND state='accepted' AND effect_id IS NULL`, id, effectID)
	if e == nil && tag.RowsAffected() != 1 {
		return ErrConflict
	}
	return e
}
func (r *Repository) CoreRecommendationByEffect(ctx context.Context, effectID string, lock bool) (segmentport.CoreRecommendation, error) {
	t, e := tx(ctx)
	if e != nil {
		return segmentport.CoreRecommendation{}, e
	}
	q := `SELECT ` + recommendationColumns + ` FROM segment_core_recommendations WHERE effect_id=$1`
	if lock {
		q += ` FOR UPDATE`
	}
	return scanRecommendation(t.QueryRow(ctx, q, effectID))
}
func (r *Repository) CoreRecommendation(ctx context.Context, id int64) (segmentport.CoreRecommendation, error) {
	t, e := tx(ctx)
	if e != nil {
		return segmentport.CoreRecommendation{}, e
	}
	return scanRecommendation(t.QueryRow(ctx, `SELECT `+recommendationColumns+` FROM segment_core_recommendations WHERE id=$1`, id))
}
func (r *Repository) SetCoreRecommendationResult(ctx context.Context, id int64, state, failureCode, reason, evidence string, product int64, now time.Time) error {
	t, e := tx(ctx)
	if e != nil {
		return e
	}
	_, e = t.Exec(ctx, `UPDATE segment_core_recommendations SET state=$2,failure_code=$3,reason=$4,evidence=$5,chosen_product_id=NULLIF($6,0),completed_at=CASE WHEN $2 IN ('retryable_failed','outcome_unknown') THEN NULL ELSE $7::timestamptz END WHERE id=$1`, id, state, failureCode, reason, evidence, product, now)
	return e
}
func (r *Repository) LockCoreAssignments(ctx context.Context) error {
	t, e := tx(ctx)
	if e != nil {
		return e
	}
	_, e = t.Exec(ctx, `SELECT singleton FROM segment_core_prompt_state WHERE singleton FOR UPDATE`)
	return e
}

func (r *Repository) CorePromptAuthor(ctx context.Context, id int64) (actor int64, e error) {
	t, e := tx(ctx)
	if e != nil {
		return
	}
	e = t.QueryRow(ctx, `SELECT created_by FROM segment_core_prompt_versions WHERE id=$1`, id).Scan(&actor)
	return
}

func (r *Repository) PurchasedCoreProducts(ctx context.Context, customer int64) ([]int64, error) {
	t, e := tx(ctx)
	if e != nil {
		return nil, e
	}
	rows, e := t.Query(ctx, `SELECT core_product_id FROM segment_core_purchases WHERE customer_id=$1`, customer)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []int64{}
	for rows.Next() {
		var id int64
		if e = rows.Scan(&id); e != nil {
			return nil, e
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
