// Package store owns Coupon rule PostgreSQL tables only.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	couponapp "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/app"
	couponport "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

type Repository struct {
	pool *pgxpool.Pool
	uow  platformport.UnitOfWork
}

func NewPostgreSQL(pool *pgxpool.Pool, uow platformport.UnitOfWork) (*Repository, error) {
	if pool == nil || uow == nil {
		return nil, errors.New("coupon store dependencies are required")
	}
	return &Repository{pool, uow}, nil
}
func (r *Repository) Within(ctx context.Context, fn func(context.Context) error) error {
	return r.uow.Within(ctx, fn)
}

const couponColumns = `id,name,discount_amount_total,currency,status,total_issue_limit,per_user_issue_limit,issued_count,claim_starts_at,claim_ends_at,validity_mode,use_starts_at,use_ends_at,relative_validity_days,instructions,created_by,updated_by,version,created_at,updated_at`

func scanCoupon(row pgx.Row) (couponport.Coupon, error) {
	var c couponport.Coupon
	var mode string
	err := row.Scan(&c.ID, &c.Name, &c.DiscountAmountTotal, &c.Currency, &c.Status, &c.TotalIssueLimit, &c.PerUserIssueLimit, &c.IssuedCount, &c.ClaimStartsAt, &c.ClaimEndsAt, &mode, &c.UseStartsAt, &c.UseEndsAt, &c.RelativeValidityDays, &c.Instructions, &c.CreatedBy, &c.UpdatedBy, &c.Version, &c.CreatedAt, &c.UpdatedAt)
	c.ValidityMode = couponport.ValidityMode(mode)
	return c, err
}
func (r *Repository) targets(ctx context.Context, tx pgx.Tx, id couponport.ID) ([]string, error) {
	rows, e := tx.Query(ctx, `SELECT target_ref FROM coupon_rule_targets WHERE coupon_id=$1 ORDER BY position`, id)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var x string
		if e = rows.Scan(&x); e != nil {
			return nil, e
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
func (r *Repository) get(ctx context.Context, tx pgx.Tx, id couponport.ID, lock bool) (couponport.Coupon, error) {
	suffix := ""
	if lock {
		suffix = " FOR UPDATE"
	}
	c, e := scanCoupon(tx.QueryRow(ctx, `SELECT `+couponColumns+` FROM coupon_rules WHERE id=$1`+suffix, id))
	if errors.Is(e, pgx.ErrNoRows) {
		return couponport.Coupon{}, couponapp.ErrNotFound
	}
	if e != nil {
		return couponport.Coupon{}, e
	}
	c.TargetRefs, e = r.targets(ctx, tx, id)
	return c, e
}
func (r *Repository) List(ctx context.Context, limit, offset int32, search, status string) ([]couponport.Coupon, error) {
	tx, e := platformpostgres.RequireTransaction(ctx)
	if e != nil {
		return nil, e
	}
	args := []any{}
	where := []string{"TRUE"}
	if search != "" {
		args = append(args, "%"+search+"%")
		where = append(where, "name ILIKE $"+itoa(len(args)))
	}
	if status != "" {
		args = append(args, status)
		where = append(where, "status=$"+itoa(len(args)))
	} else {
		// The admin's ordinary management list is a live-definition surface.
		// Archived rules remain readable by ID for claims and audit history, but
		// must not look selectable for a new issue or public share.
		where = append(where, "status<>'archived'")
	}
	args = append(args, limit, offset)
	rows, e := tx.Query(ctx, `SELECT `+couponColumns+` FROM coupon_rules WHERE `+strings.Join(where, " AND ")+` ORDER BY updated_at DESC,id DESC LIMIT $`+itoa(len(args)-1)+` OFFSET $`+itoa(len(args)), args...)
	if e != nil {
		return nil, e
	}
	out := []couponport.Coupon{}
	for rows.Next() {
		c, e := scanCoupon(rows)
		if e != nil {
			rows.Close()
			return nil, e
		}
		out = append(out, c)
	}
	if e = rows.Err(); e != nil {
		rows.Close()
		return nil, e
	}
	rows.Close()
	// pgx transactions use one connection. Finish consuming and close the
	// list cursor before issuing the per-rule target queries on that same
	// connection; otherwise any non-empty result fails with "conn busy".
	for index := range out {
		out[index].TargetRefs, e = r.targets(ctx, tx, out[index].ID)
		if e != nil {
			return nil, e
		}
	}
	return out, nil
}
func (r *Repository) Count(ctx context.Context, search, status string) (int64, error) {
	tx, e := platformpostgres.RequireTransaction(ctx)
	if e != nil {
		return 0, e
	}
	args := []any{}
	where := []string{"TRUE"}
	if search != "" {
		args = append(args, "%"+search+"%")
		where = append(where, "name ILIKE $"+itoa(len(args)))
	}
	if status != "" {
		args = append(args, status)
		where = append(where, "status=$"+itoa(len(args)))
	} else {
		where = append(where, "status<>'archived'")
	}
	var n int64
	e = tx.QueryRow(ctx, `SELECT count(*) FROM coupon_rules WHERE `+strings.Join(where, " AND "), args...).Scan(&n)
	return n, e
}
func (r *Repository) Get(ctx context.Context, id couponport.ID) (couponport.Coupon, error) {
	tx, e := platformpostgres.RequireTransaction(ctx)
	if e != nil {
		return couponport.Coupon{}, e
	}
	return r.get(ctx, tx, id, false)
}
func (r *Repository) Lock(ctx context.Context, id couponport.ID) (couponport.Coupon, error) {
	tx, e := platformpostgres.RequireTransaction(ctx)
	if e != nil {
		return couponport.Coupon{}, e
	}
	return r.get(ctx, tx, id, true)
}
func (r *Repository) Create(ctx context.Context, cmd couponport.UpsertCommand, _ []int64, now time.Time) (couponport.Coupon, error) {
	tx, e := platformpostgres.RequireTransaction(ctx)
	if e != nil {
		return couponport.Coupon{}, e
	}
	var id couponport.ID
	e = tx.QueryRow(ctx, `INSERT INTO coupon_rules(name,discount_amount_total,currency,status,total_issue_limit,per_user_issue_limit,claim_starts_at,claim_ends_at,validity_mode,use_starts_at,use_ends_at,relative_validity_days,instructions,created_by,updated_by,created_at,updated_at) VALUES($1,$2,'CNY','draft',$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$12,$13,$13) RETURNING id`, cmd.Name, cmd.DiscountAmountTotal, cmd.TotalIssueLimit, cmd.PerUserIssueLimit, cmd.ClaimStartsAt, cmd.ClaimEndsAt, cmd.ValidityMode, cmd.UseStartsAt, cmd.UseEndsAt, cmd.RelativeValidityDays, cmd.Instructions, cmd.Actor, now).Scan(&id)
	if e != nil {
		return couponport.Coupon{}, e
	}
	if e = r.replaceTargets(ctx, tx, id, cmd.TargetRefs); e != nil {
		return couponport.Coupon{}, e
	}
	return r.get(ctx, tx, id, false)
}
func (r *Repository) Update(ctx context.Context, cmd couponport.UpsertCommand, _ []int64, now time.Time) (couponport.Coupon, error) {
	tx, e := platformpostgres.RequireTransaction(ctx)
	if e != nil {
		return couponport.Coupon{}, e
	}
	tag, e := tx.Exec(ctx, `UPDATE coupon_rules SET name=$2,discount_amount_total=$3,total_issue_limit=$4,per_user_issue_limit=$5,claim_starts_at=$6,claim_ends_at=$7,validity_mode=$8,use_starts_at=$9,use_ends_at=$10,relative_validity_days=$11,instructions=$12,updated_by=$13,updated_at=$14,version=version+1 WHERE id=$1`, cmd.ID, cmd.Name, cmd.DiscountAmountTotal, cmd.TotalIssueLimit, cmd.PerUserIssueLimit, cmd.ClaimStartsAt, cmd.ClaimEndsAt, cmd.ValidityMode, cmd.UseStartsAt, cmd.UseEndsAt, cmd.RelativeValidityDays, cmd.Instructions, cmd.Actor, now)
	if e != nil {
		return couponport.Coupon{}, e
	}
	if tag.RowsAffected() != 1 {
		return couponport.Coupon{}, couponapp.ErrNotFound
	}
	if e = r.replaceTargets(ctx, tx, cmd.ID, cmd.TargetRefs); e != nil {
		return couponport.Coupon{}, e
	}
	return r.get(ctx, tx, cmd.ID, false)
}
func (r *Repository) replaceTargets(ctx context.Context, tx pgx.Tx, id couponport.ID, refs []string) error {
	if _, e := tx.Exec(ctx, `DELETE FROM coupon_rule_targets WHERE coupon_id=$1`, id); e != nil {
		return e
	}
	for pos, ref := range refs {
		if _, e := tx.Exec(ctx, `INSERT INTO coupon_rule_targets(coupon_id,target_ref,position) VALUES($1,$2,$3)`, id, ref, pos); e != nil {
			return e
		}
	}
	return nil
}
func (r *Repository) SetStatus(ctx context.Context, id couponport.ID, status string, actor int64, now time.Time) (couponport.Coupon, error) {
	tx, e := platformpostgres.RequireTransaction(ctx)
	if e != nil {
		return couponport.Coupon{}, e
	}
	tag, e := tx.Exec(ctx, `UPDATE coupon_rules SET status=$2,updated_by=$3,updated_at=$4,version=version+1 WHERE id=$1`, id, status, actor, now)
	if e != nil {
		return couponport.Coupon{}, e
	}
	if tag.RowsAffected() != 1 {
		return couponport.Coupon{}, couponapp.ErrNotFound
	}
	return r.get(ctx, tx, id, false)
}
func (r *Repository) DeleteDraft(ctx context.Context, id couponport.ID) error {
	tx, e := platformpostgres.RequireTransaction(ctx)
	if e != nil {
		return e
	}
	tag, e := tx.Exec(ctx, `DELETE FROM coupon_rules WHERE id=$1 AND status='draft' AND issued_count=0`, id)
	if e != nil {
		return e
	}
	if tag.RowsAffected() != 1 {
		return couponapp.ErrConflict
	}
	return nil
}
func (r *Repository) Reserve(ctx context.Context, x couponapp.Reservation) (couponapp.Receipt, bool, error) {
	tx, e := platformpostgres.RequireTransaction(ctx)
	if e != nil {
		return couponapp.Receipt{}, false, e
	}
	var id int64
	e = tx.QueryRow(ctx, `INSERT INTO coupon_operation_receipts(operation,actor_scope,key_digest,payload_digest,state,created_at) VALUES($1,$2,$3,$4,'in_progress',$5) ON CONFLICT(operation,actor_scope,key_digest) DO NOTHING RETURNING id`, x.Operation, x.ActorScope, x.KeyDigest[:], x.PayloadDigest[:], x.CreatedAt).Scan(&id)
	if e == nil {
		return couponapp.Receipt{ID: id, Operation: x.Operation, ActorScope: x.ActorScope, State: "in_progress", KeyDigest: x.KeyDigest, PayloadDigest: x.PayloadDigest}, true, nil
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return couponapp.Receipt{}, false, e
	}
	var out couponapp.Receipt
	var key, payload []byte
	e = tx.QueryRow(ctx, `SELECT id,operation,actor_scope,state,key_digest,payload_digest,COALESCE(result_snapshot::text,'null') FROM coupon_operation_receipts WHERE operation=$1 AND actor_scope=$2 AND key_digest=$3`, x.Operation, x.ActorScope, x.KeyDigest[:]).Scan(&out.ID, &out.Operation, &out.ActorScope, &out.State, &key, &payload, &out.ResultSnapshot)
	if e != nil {
		return couponapp.Receipt{}, false, e
	}
	copy(out.KeyDigest[:], key)
	copy(out.PayloadDigest[:], payload)
	return out, false, nil
}
func (r *Repository) Complete(ctx context.Context, id int64, snapshot json.RawMessage, now time.Time) (couponapp.Receipt, error) {
	tx, e := platformpostgres.RequireTransaction(ctx)
	if e != nil {
		return couponapp.Receipt{}, e
	}
	tag, e := tx.Exec(ctx, `UPDATE coupon_operation_receipts SET state='completed',result_snapshot=$2::jsonb,completed_at=$3 WHERE id=$1 AND state='in_progress'`, id, snapshot, now)
	if e != nil {
		return couponapp.Receipt{}, e
	}
	if tag.RowsAffected() != 1 {
		return couponapp.Receipt{}, couponapp.ErrConflict
	}
	var out couponapp.Receipt
	var key, payload []byte
	e = tx.QueryRow(ctx, `SELECT operation,actor_scope,state,key_digest,payload_digest,result_snapshot FROM coupon_operation_receipts WHERE id=$1`, id).Scan(&out.Operation, &out.ActorScope, &out.State, &key, &payload, &out.ResultSnapshot)
	out.ID = id
	copy(out.KeyDigest[:], key)
	copy(out.PayloadDigest[:], payload)
	return out, e
}
func (r *Repository) Append(ctx context.Context, event couponport.Event) (couponport.EventID, error) {
	tx, e := platformpostgres.RequireTransaction(ctx)
	if e != nil {
		return 0, e
	}
	var payload struct {
		CouponID couponport.ID `json:"coupon_id"`
		Actor    int64         `json:"actor"`
		Status   string        `json:"status"`
	}
	if e = json.Unmarshal(event.Payload, &payload); e != nil || payload.CouponID < 1 || payload.Actor < 1 {
		return 0, couponapp.ErrConflict
	}
	var auditID int64
	e = tx.QueryRow(ctx, `INSERT INTO coupon_audit_events(event_type,coupon_id,actor_admin_user_id,payload,occurred_at) VALUES($1,$2,$3,$4::jsonb,$5) RETURNING id`, event.Type, payload.CouponID, payload.Actor, event.Payload, event.OccurredAt).Scan(&auditID)
	if e != nil {
		return 0, e
	}
	_, e = tx.Exec(ctx, `INSERT INTO coupon_outbox(event_type,idempotency_key,aggregate_id,payload,occurred_at) VALUES($1,$2,$3,$4::jsonb,$5)`, event.Type, event.IdempotencyKey, payload.CouponID, event.Payload, event.OccurredAt)
	if e != nil {
		return 0, e
	}
	return couponport.EventID(auditID), nil
}
func itoa(v int) string { return strconv.Itoa(v) }

var _ couponapp.Store = (*Repository)(nil)
var _ couponport.EventAppender = (*Repository)(nil)
