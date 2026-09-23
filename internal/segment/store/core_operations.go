package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	segmentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/domain"
	"reflect"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
)

const coreProductColumns = `id,package_id,name,description,ai_context,product_reference,enabled,version,updated_at`

func scanCoreProduct(row pgx.Row) (p segmentport.CoreProduct, err error) {
	err = row.Scan(&p.ID, &p.PackageID, &p.Name, &p.Description, &p.AIContext, &p.ProductReference, &p.Enabled, &p.Version, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	return
}
func (r *Repository) CoreProducts(ctx context.Context) ([]segmentport.CoreProduct, error) {
	t, e := tx(ctx)
	if e != nil {
		return nil, e
	}
	rows, e := t.Query(ctx, `SELECT `+coreProductColumns+` FROM segment_core_products ORDER BY id`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []segmentport.CoreProduct{}
	for rows.Next() {
		p, e := scanCoreProduct(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func (r *Repository) PutCoreProduct(ctx context.Context, p segmentport.CoreProduct, expected int64) (segmentport.CoreProduct, error) {
	t, e := tx(ctx)
	if e != nil {
		return p, e
	}
	// Fixed five slots and a single lock make capacity and binding checks atomic.
	if _, e = t.Exec(ctx, `SELECT singleton FROM segment_core_prompt_state WHERE singleton FOR UPDATE`); e != nil {
		return p, e
	}
	var available bool
	e = t.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM segment_audience_packages WHERE id=$1 AND archived_at IS NULL)`, p.PackageID).Scan(&available)
	if e != nil {
		return p, e
	}
	if !available {
		return p, ErrInvalid
	}
	if expected == 0 {
		return scanCoreProduct(t.QueryRow(ctx, `INSERT INTO segment_core_products(`+coreProductColumns+`) VALUES($1,$2,$3,$4,$5,$6,$7,1,$8) ON CONFLICT DO NOTHING RETURNING `+coreProductColumns, p.ID, p.PackageID, p.Name, p.Description, p.AIContext, p.ProductReference, p.Enabled, p.UpdatedAt))
	}
	// Rebinding an operating direction would silently move historical meaning.
	p, e = scanCoreProduct(t.QueryRow(ctx, `UPDATE segment_core_products SET name=$3,description=$4,ai_context=$5,product_reference=$6,enabled=$7,version=version+1,updated_at=$8 WHERE id=$1 AND package_id=$2 AND version=$9 RETURNING `+coreProductColumns, p.ID, p.PackageID, p.Name, p.Description, p.AIContext, p.ProductReference, p.Enabled, p.UpdatedAt, expected))
	if errors.Is(e, ErrNotFound) {
		e = ErrConflict
	}
	return p, e
}
func (r *Repository) CorePrompt(ctx context.Context) (p segmentport.CorePrompt, e error) {
	t, e := tx(ctx)
	if e != nil {
		return p, e
	}
	e = t.QueryRow(ctx, `SELECT s.draft,COALESCE(s.published_id,0),COALESCE(v.body,''),s.version FROM segment_core_prompt_state s LEFT JOIN segment_core_prompt_versions v ON v.id=s.published_id WHERE singleton`).Scan(&p.Draft, &p.PublishedID, &p.PublishedBody, &p.Version)
	return
}
func (r *Repository) SaveCorePrompt(ctx context.Context, body string, expected, actor int64, publish bool, now time.Time) (segmentport.CorePrompt, error) {
	t, e := tx(ctx)
	if e != nil {
		return segmentport.CorePrompt{}, e
	}
	var version int64
	e = t.QueryRow(ctx, `SELECT version FROM segment_core_prompt_state WHERE singleton FOR UPDATE`).Scan(&version)
	if e != nil {
		return segmentport.CorePrompt{}, e
	}
	if version != expected {
		return segmentport.CorePrompt{}, ErrConflict
	}
	var publishedID *int64
	if publish {
		var id int64
		e = t.QueryRow(ctx, `INSERT INTO segment_core_prompt_versions(body,created_by,created_at) VALUES($1,$2,$3) RETURNING id`, body, actor, now).Scan(&id)
		if e != nil {
			return segmentport.CorePrompt{}, e
		}
		publishedID = &id
	}
	_, e = t.Exec(ctx, `UPDATE segment_core_prompt_state SET draft=$1,published_id=COALESCE($2,published_id),version=version+1,updated_at=$3 WHERE singleton`, body, publishedID, now)
	if e != nil {
		return segmentport.CorePrompt{}, e
	}
	return r.CorePrompt(ctx)
}
func (r *Repository) CorePromptHistory(ctx context.Context) ([]segmentport.CorePromptVersion, error) {
	t, e := tx(ctx)
	if e != nil {
		return nil, e
	}
	rows, e := t.Query(ctx, `SELECT id,body,created_at FROM segment_core_prompt_versions ORDER BY id DESC LIMIT 100`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []segmentport.CorePromptVersion{}
	for rows.Next() {
		var p segmentport.CorePromptVersion
		if e = rows.Scan(&p.ID, &p.Body, &p.CreatedAt); e != nil {
			return nil, e
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

const coreAssignmentColumns = `id,customer_id,core_product_id,package_id,source,reason,evidence,COALESCE(prompt_version,0),entered_at,ended_at,end_reason`

func scanCoreAssignment(row pgx.Row) (a segmentport.CoreAssignment, e error) {
	e = row.Scan(&a.ID, &a.CustomerID, &a.CoreProductID, &a.PackageID, &a.Source, &a.Reason, &a.Evidence, &a.PromptVersion, &a.EnteredAt, &a.EndedAt, &a.EndReason)
	if errors.Is(e, pgx.ErrNoRows) {
		e = ErrNotFound
	}
	return
}
func (r *Repository) CoreAssignment(ctx context.Context, customer int64) (segmentport.CoreAssignment, error) {
	t, e := tx(ctx)
	if e != nil {
		return segmentport.CoreAssignment{}, e
	}
	return scanCoreAssignment(t.QueryRow(ctx, `SELECT `+coreAssignmentColumns+` FROM segment_core_assignments WHERE customer_id=$1 AND ended_at IS NULL`, customer))
}
func (r *Repository) ChangeCoreAssignment(ctx context.Context, a segmentport.CoreAssignment, expected int64, purchase bool) (segmentport.CoreAssignment, error) {
	t, e := tx(ctx)
	if e != nil {
		return a, e
	}
	// Serialize assignment and snapshot publication across all five bound packages.
	if _, e = t.Exec(ctx, `SELECT singleton FROM segment_core_prompt_state WHERE singleton FOR UPDATE`); e != nil {
		return a, e
	}
	// Serializes absent as well as present assignments without provisioning customer roots.
	if _, e = t.Exec(ctx, `SELECT pg_advisory_xact_lock(178,$1::int)`, int32(a.CustomerID%2147483647)); e != nil {
		return a, e
	}
	current, e := r.CoreAssignment(ctx, a.CustomerID)
	if e != nil && !errors.Is(e, ErrNotFound) {
		return a, e
	}
	if current.ID != expected {
		return a, ErrConflict
	}
	if purchase {
		_, e = t.Exec(ctx, `INSERT INTO segment_core_purchases(customer_id,core_product_id,recorded_at) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, a.CustomerID, a.CoreProductID, a.EnteredAt)
		if e != nil {
			return a, e
		}
		if current.ID == 0 || current.CoreProductID != a.CoreProductID {
			return current, nil
		}
		_, e = t.Exec(ctx, `UPDATE segment_core_assignments SET ended_at=$2,end_reason='purchased' WHERE id=$1`, current.ID, a.EnteredAt)
		current.EndedAt = &a.EnteredAt
		current.EndReason = "purchased"
		return current, e
	}
	var packageID int64
	e = t.QueryRow(ctx, `SELECT p.package_id FROM segment_core_products p JOIN segment_audience_packages a ON a.id=p.package_id WHERE p.id=$1 AND p.enabled AND a.archived_at IS NULL AND NOT EXISTS(SELECT 1 FROM segment_core_purchases WHERE customer_id=$2 AND core_product_id=p.id) FOR SHARE OF p`, a.CoreProductID, a.CustomerID).Scan(&packageID)
	if errors.Is(e, pgx.ErrNoRows) {
		return a, ErrConflict
	}
	if e != nil {
		return a, e
	}
	a.PackageID = packageID
	if current.ID != 0 {
		if _, e = t.Exec(ctx, `UPDATE segment_core_assignments SET ended_at=$2,end_reason='transferred' WHERE id=$1`, current.ID, a.EnteredAt); e != nil {
			return a, e
		}
	}
	return scanCoreAssignment(t.QueryRow(ctx, `INSERT INTO segment_core_assignments(customer_id,core_product_id,package_id,source,reason,evidence,prompt_version,entered_at) VALUES($1,$2,$3,$4,$5,$6,NULLIF($7,0),$8) RETURNING `+coreAssignmentColumns, a.CustomerID, a.CoreProductID, a.PackageID, a.Source, a.Reason, a.Evidence, a.PromptVersion, a.EnteredAt))
}

const corePushColumns = `id,source,push_id,customer_id,package_id,COALESCE(assignment_id,0),materials,occurred_at,status,status_version`

func scanCorePush(row pgx.Row) (p segmentport.CorePush, e error) {
	var raw []byte
	e = row.Scan(&p.ID, &p.Source, &p.PushID, &p.CustomerID, &p.PackageID, &p.AssignmentID, &raw, &p.OccurredAt, &p.Status, &p.StatusVersion)
	if errors.Is(e, pgx.ErrNoRows) {
		return p, ErrNotFound
	}
	if e == nil {
		e = json.Unmarshal(raw, &p.Materials)
	}
	return
}
func (r *Repository) RecordCorePush(ctx context.Context, p segmentport.CorePush, now time.Time) (segmentport.CorePush, error) {
	t, e := tx(ctx)
	if e != nil {
		return p, e
	}
	raw, e := json.Marshal(p.Materials)
	if e != nil {
		return p, e
	}
	// Assignment is resolved at the business event time, not callback arrival.
	var assignment *int64
	e = t.QueryRow(ctx, `SELECT id FROM segment_core_assignments WHERE customer_id=$1 AND package_id=$2 AND entered_at <= $3 AND (ended_at IS NULL OR $3 < ended_at) ORDER BY id DESC LIMIT 1`, p.CustomerID, p.PackageID, p.OccurredAt).Scan(&assignment)
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		return p, e
	}
	created, e := scanCorePush(t.QueryRow(ctx, `INSERT INTO segment_core_pushes(source,push_id,customer_id,package_id,assignment_id,materials,occurred_at,status,status_version,recorded_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT(source,push_id,customer_id) DO NOTHING RETURNING `+corePushColumns, p.Source, p.PushID, p.CustomerID, p.PackageID, assignment, raw, p.OccurredAt, p.Status, p.StatusVersion, now))
	if e == nil {
		return created, nil
	}
	if !errors.Is(e, ErrNotFound) {
		return p, e
	}
	old, e := scanCorePush(t.QueryRow(ctx, `SELECT `+corePushColumns+` FROM segment_core_pushes WHERE source=$1 AND push_id=$2 AND customer_id=$3 FOR UPDATE`, p.Source, p.PushID, p.CustomerID))
	if e != nil {
		return p, e
	}
	if old.PackageID != p.PackageID || !old.OccurredAt.Equal(p.OccurredAt) || !reflect.DeepEqual(old.Materials, p.Materials) {
		return p, ErrConflict
	}
	if p.StatusVersion < old.StatusVersion {
		return old, nil
	}
	if p.StatusVersion == old.StatusVersion {
		if old.Status != p.Status {
			return p, ErrConflict
		}
		return old, nil
	}
	return scanCorePush(t.QueryRow(ctx, `UPDATE segment_core_pushes SET status=$2,status_version=$3 WHERE id=$1 RETURNING `+corePushColumns, old.ID, p.Status, p.StatusVersion))
}
func (r *Repository) CoreMemberDetail(ctx context.Context, packageID, customerID, before int64, limit int) (segmentport.CoreMemberDetail, error) {
	out := segmentport.CoreMemberDetail{Assignments: []segmentport.CoreAssignment{}, Pushes: []segmentport.CorePush{}, Stats: segmentport.CoreMemberStats{VisitState: "not_connected"}}
	t, e := tx(ctx)
	if e != nil {
		return out, e
	}
	if e = t.QueryRow(ctx, `SELECT max(created_at) FROM segment_core_recommendations WHERE customer_id=$1`, customerID).Scan(&out.Stats.LastEvaluationAt); e != nil {
		return out, e
	}
	history, e := r.CoreAssignmentHistory(ctx, packageID, customerID, 0, 100)
	if e != nil {
		return out, e
	}
	out.Assignments = history.Items
	out.AssignmentNextCursor = history.NextCursor
	e = t.QueryRow(ctx, `SELECT count(*),max(occurred_at),COALESCE((array_agg(status ORDER BY occurred_at DESC,id DESC))[1],'') FROM segment_core_pushes WHERE customer_id=$1 AND package_id=$2`, customerID, packageID).Scan(&out.Stats.PushCount, &out.Stats.LastPushAt, &out.Stats.LastPushStatus)
	if e != nil {
		return out, e
	}
	rows, e := t.Query(ctx, `SELECT `+corePushColumns+` FROM segment_core_pushes WHERE customer_id=$1 AND package_id=$2 AND ($3::bigint=0 OR id<$3) ORDER BY id DESC LIMIT $4`, customerID, packageID, before, limit+1)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		p, e := scanCorePush(rows)
		if e != nil {
			return out, e
		}
		out.Pushes = append(out.Pushes, p)
	}
	if len(out.Pushes) > limit {
		out.Pushes = out.Pushes[:limit]
		out.NextCursor = strconv.FormatInt(out.Pushes[limit-1].ID, 10)
	}
	return out, rows.Err()
}

func (r *Repository) CoreCustomerIDs(ctx context.Context, productID int64) ([]customerdomain.CustomerID, error) {
	t, e := tx(ctx)
	if e != nil {
		return nil, e
	}
	rows, e := t.Query(ctx, `SELECT customer_id FROM segment_core_assignments WHERE core_product_id=$1 AND ended_at IS NULL ORDER BY customer_id`, productID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []customerdomain.CustomerID{}
	for rows.Next() {
		var id customerdomain.CustomerID
		if e = rows.Scan(&id); e != nil {
			return nil, e
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// PublishCoreAssignments atomically replaces the ordinary audience snapshots
// in the same local transaction as a transfer; no intermediate dual membership.
func (r *Repository) PublishCoreAssignments(ctx context.Context, actor int64, key string, now time.Time) error {
	products, e := r.CoreProducts(ctx)
	if e != nil {
		return e
	}
	t, e := tx(ctx)
	if e != nil {
		return e
	}
	for _, p := range products {
		ids, e := r.CoreCustomerIDs(ctx, p.ID)
		if e != nil {
			return e
		}
		if len(ids) > segmentport.MaximumEvaluationMembers {
			return ErrConflict
		}
		cfg, e := r.CurrentConfiguration(ctx, p.PackageID)
		if e != nil {
			return e
		}
		// Keep unchanged audience snapshots stable; only directions whose actual
		// membership/configuration changed need to be rebuilt in this transaction.
		previous, found, err := r.PublishedSnapshot(ctx, segmentport.PackageID(p.PackageID))
		if err != nil {
			return err
		}
		if found && int64(previous.ConfigurationVersionID) == cfg.ID && previous.MemberDigest == segmentport.Digest(segmentdomain.DigestMembers(ids)) {
			continue
		}
		digest := sha256.Sum256([]byte(key + ":" + strconv.FormatInt(p.ID, 10)))
		run, _, e := r.ReserveRefresh(ctx, segmentdomain.RefreshRun{PackageID: p.PackageID, ConfigurationVersionID: cfg.ID, SourceKeyDigest: digest, ReferenceTime: now, RefreshKind: segmentdomain.RefreshDaily, CreatedAt: now, UpdatedAt: now})
		if e != nil {
			return e
		}
		if _, e = t.Exec(ctx, `UPDATE segment_audience_refresh_runs SET state='evaluating' WHERE id=$1 AND state='accepted'`, run.ID); e != nil {
			return e
		}
		if _, _, e = r.BeginRefresh(ctx, run.ID, now); e != nil {
			return e
		}
		for offset := 0; offset < len(ids); offset += 1000 {
			end := offset + 1000
			if end > len(ids) {
				end = len(ids)
			}
			batch := ids[offset:end]
			if e = r.StageRefreshBatch(ctx, run.ID, offset/1000, batch, segmentdomain.DigestMembers(batch), now); e != nil {
				return e
			}
		}
		if _, e = r.PublishRefresh(ctx, run.ID, int64(len(ids)), segmentdomain.DigestMembers(ids), digest, actor, now); e != nil {
			return e
		}
	}
	return nil
}

func (r *Repository) ValidateCoreConfiguration(ctx context.Context, packageID int64, raw []byte) error {
	t, e := tx(ctx)
	if e != nil {
		return e
	}
	var definition struct {
		Template   string `json:"template_key"`
		Parameters struct {
			ID int64 `json:"core_product_id"`
		} `json:"parameters"`
	}
	if json.Unmarshal(raw, &definition) != nil {
		return ErrInvalid
	}
	var bound int64
	e = t.QueryRow(ctx, `SELECT id FROM segment_core_products WHERE package_id=$1`, packageID).Scan(&bound)
	if e != nil && e != pgx.ErrNoRows {
		return e
	}
	if bound != 0 {
		if definition.Template != "core_ai_product" || definition.Parameters.ID != bound {
			return ErrConflict
		}
	} else if definition.Template == "core_ai_product" {
		return ErrConflict
	}
	return nil
}

func (r *Repository) CoreAssignmentHistory(ctx context.Context, packageID, customerID, before int64, limit int) (out segmentport.CoreAssignmentPage, e error) {
	out.Items = []segmentport.CoreAssignment{}
	t, e := tx(ctx)
	if e != nil {
		return out, e
	}
	rows, e := t.Query(ctx, `SELECT `+coreAssignmentColumns+` FROM segment_core_assignments WHERE customer_id=$1 AND package_id=$2 AND ($3::bigint=0 OR id<$3) ORDER BY id DESC LIMIT $4`, customerID, packageID, before, limit+1)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		a, e := scanCoreAssignment(rows)
		if e != nil {
			return out, e
		}
		out.Items = append(out.Items, a)
	}
	if len(out.Items) > limit {
		out.Items = out.Items[:limit]
		out.NextCursor = strconv.FormatInt(out.Items[limit-1].ID, 10)
	}
	return out, rows.Err()
}
