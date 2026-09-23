package source

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const ProductionSourceSystem = "ai-crm-production:150.158.82.186/openclaw_wecom"

var extractionQueries = map[string]string{
	"products": `SELECT COALESCE(jsonb_agg(to_jsonb(x) ORDER BY id),'[]'::jsonb) FROM (
		SELECT id,product_code,name,COALESCE(metadata_json->>'description','') AS description,
		amount_total::bigint AS price_minor,currency,status,enabled,cta_text AS buy_button_text,
		require_mobile,lead_qr_title,lead_qr_subtitle,created_at,updated_at
		FROM public.wechat_pay_products) x`,
	"service_periods": `SELECT COALESCE(jsonb_agg(to_jsonb(x) ORDER BY id),'[]'::jsonb) FROM (
		SELECT id,trade_product_id,duration_days,created_at,updated_at
		FROM public.service_period_products WHERE deleted=FALSE) x`,
	"coupons": `SELECT COALESCE(jsonb_agg(to_jsonb(x) ORDER BY id),'[]'::jsonb) FROM (
		SELECT id,name,discount_amount_total::bigint, currency,status,total_issue_limit::bigint,
		per_user_issue_limit::bigint,claim_starts_at,claim_ends_at,validity_mode,use_starts_at,
		use_ends_at,relative_validity_days,instructions,COALESCE(created_by,'') AS source_created_by,
		COALESCE(updated_by,'') AS source_updated_by,created_at,updated_at FROM public.commerce_coupons) x`,
	"coupon_bindings": `SELECT COALESCE(jsonb_agg(to_jsonb(x) ORDER BY id),'[]'::jsonb) FROM (
		SELECT id,coupon_id,trade_product_id,created_at FROM public.commerce_coupon_product_bindings) x`,
	"group_plans": `SELECT COALESCE(jsonb_agg(to_jsonb(x) ORDER BY id),'[]'::jsonb) FROM (
		SELECT id,plan_code,plan_name AS name,plan_type,status,description,
		COALESCE(created_by,'') AS source_created_by,COALESCE(updated_by,'') AS source_updated_by,created_at,updated_at
		FROM public.automation_group_ops_plans) x`,
	"group_nodes": `SELECT COALESCE(jsonb_agg(to_jsonb(x) ORDER BY plan_id,day_index,trigger_time_label,sort_order,id),'[]'::jsonb) FROM (
		SELECT id,plan_id,day_index,trigger_time_label,action_title,text_content,sort_order,created_at,updated_at
		FROM public.automation_group_ops_plan_nodes WHERE status='active') x`,
	"group_assets": `SELECT COALESCE(jsonb_agg(to_jsonb(x) ORDER BY plan_id,id),'[]'::jsonb) FROM (
		SELECT id,plan_id,chat_id,created_at FROM public.automation_group_ops_plan_groups WHERE status='active') x`,
	"agents": `SELECT COALESCE(jsonb_agg(to_jsonb(x) ORDER BY id),'[]'::jsonb) FROM (
		SELECT id,agent_code,agent_name,status,automation_type,draft_role_prompt,draft_task_prompt,
		published_role_prompt,published_task_prompt,draft_version::bigint,published_version::bigint,
		COALESCE(fixed_content_package_json->>'content_text','') AS fixed_content_text,
		need_human_review,created_at,updated_at,archived_at FROM public.automation_agent_runtime_config) x`,
}

// SourceTables returns the logical table keys in deterministic extraction
// order.  The returned slice is a copy and may be modified by the caller.
func SourceTables() []string { return append([]string(nil), tableOrder...) }

// ExtractionQueries returns a copy of the fixed allowlisted SELECT map.  It is
// useful to inspection tooling and tests; callers cannot mutate the reader's
// query set.
func ExtractionQueries() map[string]string {
	out := make(map[string]string, len(extractionQueries))
	for name, query := range extractionQueries {
		out[name] = query
	}
	return out
}

// Queries is kept as a short alias for operator inspection code.
func Queries() map[string]string { return ExtractionQueries() }

// TxBeginner is the small transaction seam used by the source reader.  It is
// intentionally limited to BeginTx so tests can prove the transaction mode
// without opening a database; pgxpool.Pool satisfies it directly.
type TxBeginner interface {
	BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error)
}

// Reader captures configuration definitions from one repeatable, read-only
// source transaction.  It never writes to the source database.
type Reader struct {
	DB             TxBeginner
	SourceRevision string
}

func NewReader(db TxBeginner, sourceRevision string) *Reader {
	return &Reader{DB: db, SourceRevision: sourceRevision}
}

// Extract retains the original pool-based entry point and accepts an optional
// source revision for callers that do not maintain a git/database revision
// label.  The revision is still validated before any database work begins.
func Extract(ctx context.Context, pool *pgxpool.Pool, revisions ...string) (Snapshot, error) {
	if len(revisions) != 1 {
		return Snapshot{}, ErrInvalidSnapshot
	}
	return ExtractFrom(ctx, pool, revisions[0])
}

// ExtractFrom is the transaction-seam variant used by tests and composition
// code that owns a pgx-compatible pool wrapper.
func ExtractFrom(ctx context.Context, db TxBeginner, sourceRevision string) (Snapshot, error) {
	return extractScope(ctx, db, sourceRevision, false)
}

// ExtractCommerceFrom captures only the explicitly selected commerce definition
// tables. Group/agent tables are never queried. Cardinality is manifest-driven.
func ExtractCommerceFrom(ctx context.Context, db TxBeginner, sourceRevision string) (Snapshot, error) {
	return extractScope(ctx, db, sourceRevision, true)
}

func extractScope(ctx context.Context, db TxBeginner, sourceRevision string, commerce bool) (Snapshot, error) {
	if db == nil || !validRevision.MatchString(sourceRevision) {
		return Snapshot{}, ErrInvalidSnapshot
	}
	tx, err := db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return Snapshot{}, errors.New("begin read-only source snapshot")
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SET LOCAL statement_timeout='15s'`); err != nil {
		return Snapshot{}, errors.New("set source statement timeout")
	}
	var snapshot Snapshot
	if commerce {
		snapshot.Manifest.Scope = "commerce-only"
	}
	if err = tx.QueryRow(ctx, `SELECT transaction_timestamp()`).Scan(&snapshot.Manifest.SnapshotAt); err != nil {
		return Snapshot{}, errors.New("read source snapshot timestamp")
	}
	if err = extractRows(ctx, tx, "products", &snapshot.Products); err != nil {
		return Snapshot{}, err
	}
	if err = extractRows(ctx, tx, "service_periods", &snapshot.ServicePeriods); err != nil {
		return Snapshot{}, err
	}
	if commerce {
		query := strings.Replace(extractionQueries["coupons"], "SELECT id,name,", "SELECT id,COALESCE(public_slug,'') AS public_slug,issued_count::bigint,name,", 1)
		var raw []byte
		if err = tx.QueryRow(ctx, query).Scan(&raw); err != nil {
			return Snapshot{}, errors.New("extract commerce coupon facts")
		}
		if json.Unmarshal(raw, &snapshot.Coupons) != nil {
			return Snapshot{}, ErrInvalidSnapshot
		}
	} else if err = extractRows(ctx, tx, "coupons", &snapshot.Coupons); err != nil {
		return Snapshot{}, err
	}
	if err = extractRows(ctx, tx, "coupon_bindings", &snapshot.CouponBindings); err != nil {
		return Snapshot{}, err
	}
	if !commerce {
		if err = extractRows(ctx, tx, "group_plans", &snapshot.GroupPlans); err != nil {
			return Snapshot{}, err
		}
		if err = extractRows(ctx, tx, "group_nodes", &snapshot.GroupNodes); err != nil {
			return Snapshot{}, err
		}
		if err = extractRows(ctx, tx, "group_assets", &snapshot.GroupAssets); err != nil {
			return Snapshot{}, err
		}
		if err = extractRows(ctx, tx, "agents", &snapshot.Agents); err != nil {
			return Snapshot{}, err
		}
	}
	if err = PopulateManifest(&snapshot, ProductionSourceSystem, sourceRevision, snapshot.Manifest.SnapshotAt); err != nil {
		return Snapshot{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Snapshot{}, errors.New("finish read-only source snapshot")
	}
	return snapshot, nil
}

func (r *Reader) Extract(ctx context.Context) (Snapshot, error) {
	if r == nil {
		return Snapshot{}, ErrInvalidSnapshot
	}
	return ExtractFrom(ctx, r.DB, r.SourceRevision)
}

func extractRows(ctx context.Context, tx pgx.Tx, name string, destination any) error {
	query, ok := extractionQueries[name]
	if !ok {
		return ErrInvalidSnapshot
	}
	var raw []byte
	if err := tx.QueryRow(ctx, query).Scan(&raw); err != nil {
		return fmt.Errorf("extract %s: %w", name, err)
	}
	if !json.Valid(raw) || json.Unmarshal(raw, destination) != nil {
		return fmt.Errorf("decode %s: %w", name, ErrInvalidSnapshot)
	}
	return nil
}
