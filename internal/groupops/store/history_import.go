package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	groupopsport "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/port"
)

var _ groupopsport.HistoricalImporter = (*Repository)(nil)
var _ groupopsport.HistoricalStore = (*Repository)(nil)

// ApplyHistoricalImport writes only immutable V1 projections and their
// append-only provenance. It is called inside the import command's one target
// transaction and deliberately does not touch current plans, runtime, River,
// External Effects, Access, or Provider adapters.

func (r *Repository) PreflightHistoricalImport(ctx context.Context, batch groupopsport.HistoricalImportBatch) error {
	if !validHistoryBatch(batch) {
		return groupopsport.ErrHistoryInvalid
	}
	tx, err := transaction(ctx)
	if err != nil {
		return err
	}
	var digest []byte
	err = tx.QueryRow(ctx, `SELECT snapshot_digest FROM group_ops_v1_history_import_batches WHERE source_system=$1 AND source_revision=$2`, batch.SourceSystem, batch.SourceRevision).Scan(&digest)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil || len(digest) != sha256.Size || string(digest) != string(batch.SnapshotDigest[:]) {
		return groupopsport.ErrHistoryConflict
	}
	return nil
}

func (r *Repository) ApplyHistoricalImport(ctx context.Context, batch groupopsport.HistoricalImportBatch, records []groupopsport.HistoricalImportRecord) (out groupopsport.HistoricalImportResult, err error) {
	tx, err := transaction(ctx)
	if err != nil || !validHistoryBatch(batch) || !validHistoryRecords(records) {
		return out, ErrInvalid
	}
	var prior []byte
	var status string
	err = tx.QueryRow(ctx, `SELECT id,snapshot_digest,status FROM group_ops_v1_history_import_batches WHERE source_system=$1 AND source_revision=$2 FOR UPDATE`, batch.SourceSystem, batch.SourceRevision).Scan(&out.BatchID, &prior, &status)
	if err == nil {
		if len(prior) != sha256.Size || string(prior) != string(batch.SnapshotDigest[:]) {
			return out, groupopsport.ErrHistoryConflict
		}
		if status != "applied" && status != "verified" {
			return out, groupopsport.ErrHistoryConflict
		}
		out, err = r.verifyHistoricalRows(ctx, tx, out.BatchID, records, false)
		out.NoOp = err == nil
		return out, err
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return out, err
	}
	if err = tx.QueryRow(ctx, `INSERT INTO group_ops_v1_history_import_batches(source_system,source_revision,snapshot_digest,manifest,status) VALUES($1,$2,$3,$4::jsonb,'applying') RETURNING id`, batch.SourceSystem, batch.SourceRevision, batch.SnapshotDigest[:], batch.Manifest).Scan(&out.BatchID); err != nil {
		return out, err
	}
	for _, record := range records {
		if record.QuarantineReason != "" {
			if err = r.recordHistoricalRow(ctx, tx, out.BatchID, record, "", 0, [sha256.Size]byte{}); err != nil {
				return out, err
			}
			out.Quarantined++
			continue
		}
		table, targetID, digest, importErr := r.createHistoricalTarget(ctx, record)
		if importErr != nil {
			return out, importErr
		}
		if err = r.recordHistoricalRow(ctx, tx, out.BatchID, record, table, targetID, digest); err != nil {
			return out, err
		}
		out.Imported++
	}
	counts, marshalErr := json.Marshal(map[string]int64{"imported": out.Imported, "quarantined": out.Quarantined})
	if marshalErr != nil {
		return out, marshalErr
	}
	if _, err = tx.Exec(ctx, `UPDATE group_ops_v1_history_import_batches SET status='applied',imported_counts=$2::jsonb,applied_at=clock_timestamp(),updated_at=clock_timestamp() WHERE id=$1 AND status='applying'`, out.BatchID, counts); err != nil {
		return out, err
	}
	return out, nil
}

func (r *Repository) VerifyHistoricalImport(ctx context.Context, batch groupopsport.HistoricalImportBatch, records []groupopsport.HistoricalImportRecord) (out groupopsport.HistoricalImportResult, err error) {
	tx, err := transaction(ctx)
	if err != nil || !validHistoryBatch(batch) || !validHistoryRecords(records) {
		return out, ErrInvalid
	}
	var prior []byte
	var status string
	if err = tx.QueryRow(ctx, `SELECT id,snapshot_digest,status FROM group_ops_v1_history_import_batches WHERE source_system=$1 AND source_revision=$2 FOR UPDATE`, batch.SourceSystem, batch.SourceRevision).Scan(&out.BatchID, &prior, &status); err != nil {
		return out, err
	}
	if len(prior) != sha256.Size || string(prior) != string(batch.SnapshotDigest[:]) || (status != "applied" && status != "verified") {
		return out, groupopsport.ErrHistoryConflict
	}
	out, err = r.verifyHistoricalRows(ctx, tx, out.BatchID, records, true)
	if err != nil || status == "verified" {
		return out, err
	}
	if _, err = tx.Exec(ctx, `UPDATE group_ops_v1_history_import_batches SET status='verified',verified_at=clock_timestamp(),updated_at=clock_timestamp() WHERE id=$1 AND status='applied'`, out.BatchID); err != nil {
		return out, err
	}
	return out, nil
}

func (r *Repository) verifyHistoricalRows(ctx context.Context, tx pgx.Tx, batchID int64, records []groupopsport.HistoricalImportRecord, requireApplied bool) (out groupopsport.HistoricalImportResult, err error) {
	out.BatchID = batchID
	for _, record := range records {
		var outcome, table, reason string
		var targetID pgtype.Int8
		var sourceDigest, targetDigest []byte
		err = tx.QueryRow(ctx, `SELECT outcome,COALESCE(target_table,''),target_id,source_digest,target_digest,COALESCE(reason_code,'') FROM group_ops_v1_history_import_rows WHERE batch_id=$1 AND source_kind=$2 AND source_key=$3`, batchID, record.SourceKind, record.SourceKey).Scan(&outcome, &table, &targetID, &sourceDigest, &targetDigest, &reason)
		if err != nil || len(sourceDigest) != sha256.Size || string(sourceDigest) != string(record.SourceDigest[:]) {
			return out, groupopsport.ErrHistoryConflict
		}
		if record.QuarantineReason != "" {
			if outcome != "quarantined" || reason != record.QuarantineReason {
				return out, groupopsport.ErrHistoryConflict
			}
			out.Quarantined++
			continue
		}
		if outcome != "imported" || !targetID.Valid || len(targetDigest) != sha256.Size {
			return out, groupopsport.ErrHistoryConflict
		}
		actual, actualErr := r.readHistoricalTarget(ctx, table, targetID.Int64)
		if actualErr != nil || string(actual[:]) != string(targetDigest) {
			return out, groupopsport.ErrHistoryConflict
		}
		out.Imported++
	}
	if requireApplied && out.Imported+out.Quarantined != int64(len(records)) {
		return out, groupopsport.ErrHistoryConflict
	}
	return out, nil
}

func (r *Repository) recordHistoricalRow(ctx context.Context, tx pgx.Tx, batchID int64, record groupopsport.HistoricalImportRecord, table string, targetID int64, digest [sha256.Size]byte) error {
	if record.QuarantineReason != "" {
		_, err := tx.Exec(ctx, `INSERT INTO group_ops_v1_history_import_rows(batch_id,source_kind,source_key,source_digest,outcome,reason_code) VALUES($1,$2,$3,$4,'quarantined',$5)`, batchID, record.SourceKind, record.SourceKey, record.SourceDigest[:], record.QuarantineReason)
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO group_ops_v1_history_import_rows(batch_id,source_kind,source_key,source_digest,outcome,target_table,target_id,target_digest) VALUES($1,$2,$3,$4,'imported',$5,$6,$7)`, batchID, record.SourceKind, record.SourceKey, record.SourceDigest[:], table, targetID, digest[:])
	return err
}

func (r *Repository) createHistoricalTarget(ctx context.Context, record groupopsport.HistoricalImportRecord) (string, int64, [sha256.Size]byte, error) {
	if record.Plan != nil {
		item := *record.Plan
		actual, err := r.CreateHistoricalPlan(ctx, item)
		return "group_ops_v1_history_plans", item.PlanID, historicalDigest(actual), err
	}
	if record.Directory != nil {
		item := *record.Directory
		actual, err := r.CreateHistoricalDirectory(ctx, item)
		return "group_ops_v1_history_directory", actual.ID, historicalDigest(actual), err
	}
	if record.Group != nil {
		item := *record.Group
		actual, err := r.CreateHistoricalGroup(ctx, item)
		return "group_ops_v1_history_groups", actual.ID, historicalDigest(actual), err
	}
	item := *record.Node
	actual, err := r.CreateHistoricalNode(ctx, item)
	return "group_ops_v1_history_nodes", actual.ID, historicalDigest(actual), err
}

func (r *Repository) readHistoricalTarget(ctx context.Context, table string, id int64) ([sha256.Size]byte, error) {
	switch table {
	case "group_ops_v1_history_plans":
		item, err := r.getHistoricalPlan(ctx, id)
		return historicalDigest(item), err
	case "group_ops_v1_history_directory":
		item, err := r.getHistoricalDirectory(ctx, id)
		return historicalDigest(item), err
	case "group_ops_v1_history_groups":
		item, err := r.getHistoricalGroup(ctx, id)
		return historicalDigest(item), err
	case "group_ops_v1_history_nodes":
		item, err := r.getHistoricalNode(ctx, id)
		return historicalDigest(item), err
	default:
		return [sha256.Size]byte{}, ErrInvalid
	}
}

// historicalDigest includes protected source references even though the frozen
// read page deliberately omits them. A NULL source fact and a source fact that
// was explicitly the empty string must therefore produce different digests.
func historicalDigest(value any) [sha256.Size]byte {
	var canonical any
	switch item := value.(type) {
	case groupopsport.HistoricalPlan:
		canonical = struct {
			Plan    groupopsport.HistoricalPlan `json:"plan"`
			Created *string                     `json:"source_created_by_reference"`
			Updated *string                     `json:"source_updated_by_reference"`
			Owner   *string                     `json:"source_owner_reference"`
		}{item, item.SourceCreatedByReference, item.SourceUpdatedByReference, item.SourceOwnerReference}
	case groupopsport.HistoricalDirectory:
		canonical = struct {
			Directory groupopsport.HistoricalDirectory `json:"directory"`
			Owner     *string                          `json:"source_owner_reference"`
		}{item, item.SourceOwnerReference}
	case groupopsport.HistoricalGroup:
		canonical = struct {
			Group groupopsport.HistoricalGroup `json:"group"`
			Owner *string                      `json:"source_owner_reference"`
		}{item, item.SourceOwnerReference}
	default:
		canonical = value
	}
	raw, _ := json.Marshal(canonical)
	return sha256.Sum256(raw)
}
func nullableSource(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func validHistoryBatch(value groupopsport.HistoricalImportBatch) bool {
	return len(value.SourceSystem) > 0 && len(value.SourceSystem) <= 160 && len(value.SourceRevision) == 40 && json.Valid(value.Manifest)
}
func validHistoryRecords(items []groupopsport.HistoricalImportRecord) bool {
	seen := map[string]bool{}
	for _, item := range items {
		if item.SourceKind == "" || item.SourceKey == "" || len(item.SourceKind) > 120 || len(item.SourceKey) > 240 || seen[item.SourceKind+"\x00"+item.SourceKey] {
			return false
		}
		seen[item.SourceKind+"\x00"+item.SourceKey] = true
		facts := 0
		if item.Plan != nil {
			facts++
		}
		if item.Directory != nil {
			facts++
		}
		if item.Group != nil {
			facts++
		}
		if item.Node != nil {
			facts++
		}
		if (item.QuarantineReason == "" && facts != 1) || (item.QuarantineReason != "" && facts != 0) {
			return false
		}
	}
	return true
}
func validHistoricalPlan(x groupopsport.HistoricalPlan) bool {
	return x.PlanID > 0 && x.SourcePlanID > 0 && x.Name != "" && x.Name == strings.TrimSpace(x.Name) && x.Status == groupopsport.PlanArchived && x.Revision == 1 && x.CreatedAt.IsZero() == false && x.UpdatedAt.Before(x.CreatedAt) == false && x.PlanType != "" && x.OriginalStatus != ""
}
func validHistoricalDirectory(x groupopsport.HistoricalDirectory) bool {
	return (x.SourceKind == "group_chats" || x.SourceKind == "wecom_group_chat_snapshots") && x.ChatReference != "" && !x.RecordedAt.IsZero()
}
func validHistoricalGroup(x groupopsport.HistoricalGroup) bool {
	return x.SourceGroupID > 0 && x.SourcePlanID > 0 && x.PlanID > 0 && x.ChatReference != "" && x.DisplayName != "" && x.InternalMemberCount >= 0 && x.ExternalMemberCount >= 0 && x.OriginalStatus != "" && !x.CreatedAt.IsZero()
}
func validHistoricalNode(x groupopsport.HistoricalNode) bool {
	return x.SourceNodeID > 0 && x.SourcePlanID > 0 && x.PlanID > 0 && x.DayIndex >= 0 && x.SortOrder >= 0 && x.OriginalStatus != "" && json.Valid(x.ContentPackage) && !x.CreatedAt.IsZero() && !x.UpdatedAt.Before(x.CreatedAt)
}

func (r *Repository) CreateHistoricalPlan(ctx context.Context, item groupopsport.HistoricalPlan) (groupopsport.HistoricalPlan, error) {
	if !validHistoricalPlan(item) {
		return groupopsport.HistoricalPlan{}, ErrInvalid
	}
	tx, err := transaction(ctx)
	if err != nil {
		return groupopsport.HistoricalPlan{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO group_ops_v1_history_plans(plan_id,name,status,revision,created_by,updated_by,created_at,updated_at,source_plan_id,source_code,plan_type,original_status,owner_staff_id,archived_at,source_created_by_reference,source_updated_by_reference,source_owner_reference) VALUES($1,$2,'archived',1,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`, item.PlanID, item.Name, item.CreatedBy, item.UpdatedBy, item.CreatedAt, item.UpdatedAt, item.SourcePlanID, item.SourceCode, item.PlanType, item.OriginalStatus, item.OwnerStaffID, item.ArchivedAt, nullableSource(item.SourceCreatedByReference), nullableSource(item.SourceUpdatedByReference), nullableSource(item.SourceOwnerReference)); err != nil {
		return groupopsport.HistoricalPlan{}, err
	}
	return r.getHistoricalPlan(ctx, item.PlanID)
}

func (r *Repository) GetHistoricalPlan(ctx context.Context, id int64) (groupopsport.HistoricalPlan, error) {
	return r.getHistoricalPlan(ctx, id)
}

func (r *Repository) CreateHistoricalDirectory(ctx context.Context, item groupopsport.HistoricalDirectory) (groupopsport.HistoricalDirectory, error) {
	if !validHistoricalDirectory(item) {
		return groupopsport.HistoricalDirectory{}, ErrInvalid
	}
	tx, err := transaction(ctx)
	if err != nil {
		return groupopsport.HistoricalDirectory{}, err
	}
	var id int64
	if err = tx.QueryRow(ctx, `INSERT INTO group_ops_v1_history_directory(source_kind,source_id,chat_reference,display_name,owner_staff_id,owner_name,member_count,internal_member_count,external_member_count,original_status,recorded_at,source_owner_reference) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) RETURNING id`, item.SourceKind, item.SourceID, item.ChatReference, item.DisplayName, item.OwnerStaffID, item.OwnerName, item.MemberCount, item.InternalMemberCount, item.ExternalMemberCount, item.OriginalStatus, item.RecordedAt, nullableSource(item.SourceOwnerReference)).Scan(&id); err != nil {
		return groupopsport.HistoricalDirectory{}, err
	}
	return r.getHistoricalDirectory(ctx, id)
}

func (r *Repository) GetHistoricalDirectory(ctx context.Context, id int64) (groupopsport.HistoricalDirectory, error) {
	return r.getHistoricalDirectory(ctx, id)
}

func (r *Repository) CreateHistoricalGroup(ctx context.Context, item groupopsport.HistoricalGroup) (groupopsport.HistoricalGroup, error) {
	if !validHistoricalGroup(item) {
		return groupopsport.HistoricalGroup{}, ErrInvalid
	}
	tx, err := transaction(ctx)
	if err != nil {
		return groupopsport.HistoricalGroup{}, err
	}
	var id int64
	if err = tx.QueryRow(ctx, `INSERT INTO group_ops_v1_history_groups(source_group_id,source_plan_id,plan_id,chat_reference,display_name,owner_staff_id,internal_member_count,external_member_count,original_status,created_at,removed_at,source_owner_reference) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) RETURNING id`, item.SourceGroupID, item.SourcePlanID, item.PlanID, item.ChatReference, item.DisplayName, item.OwnerStaffID, item.InternalMemberCount, item.ExternalMemberCount, item.OriginalStatus, item.CreatedAt, item.RemovedAt, nullableSource(item.SourceOwnerReference)).Scan(&id); err != nil {
		return groupopsport.HistoricalGroup{}, err
	}
	return r.getHistoricalGroup(ctx, id)
}

func (r *Repository) GetHistoricalGroup(ctx context.Context, id int64) (groupopsport.HistoricalGroup, error) {
	return r.getHistoricalGroup(ctx, id)
}

func (r *Repository) CreateHistoricalNode(ctx context.Context, item groupopsport.HistoricalNode) (groupopsport.HistoricalNode, error) {
	if !validHistoricalNode(item) {
		return groupopsport.HistoricalNode{}, ErrInvalid
	}
	tx, err := transaction(ctx)
	if err != nil {
		return groupopsport.HistoricalNode{}, err
	}
	var id int64
	if err = tx.QueryRow(ctx, `INSERT INTO group_ops_v1_history_nodes(source_node_id,source_plan_id,plan_id,day_index,trigger_time,sort_order,original_status,content_package,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8::jsonb,$9,$10) RETURNING id`, item.SourceNodeID, item.SourcePlanID, item.PlanID, item.DayIndex, item.TriggerTime, item.SortOrder, item.OriginalStatus, item.ContentPackage, item.CreatedAt, item.UpdatedAt).Scan(&id); err != nil {
		return groupopsport.HistoricalNode{}, err
	}
	return r.getHistoricalNode(ctx, id)
}

func (r *Repository) GetHistoricalNode(ctx context.Context, id int64) (groupopsport.HistoricalNode, error) {
	return r.getHistoricalNode(ctx, id)
}

func (r *Repository) getHistoricalPlan(ctx context.Context, id int64) (groupopsport.HistoricalPlan, error) {
	rows, err := transaction(ctx)
	if err != nil {
		return groupopsport.HistoricalPlan{}, err
	}
	row, err := rows.Query(ctx, `SELECT plan_id,name,status,revision,created_by,updated_by,created_at,updated_at,source_plan_id,source_code,plan_type,original_status,owner_staff_id,archived_at,source_created_by_reference,source_updated_by_reference,source_owner_reference FROM group_ops_v1_history_plans WHERE plan_id=$1`, id)
	if err != nil {
		return groupopsport.HistoricalPlan{}, err
	}
	defer row.Close()
	if !row.Next() {
		return groupopsport.HistoricalPlan{}, ErrNotFound
	}
	return scanHistoricalPlan(row)
}
func (r *Repository) getHistoricalDirectory(ctx context.Context, id int64) (groupopsport.HistoricalDirectory, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return groupopsport.HistoricalDirectory{}, err
	}
	var x groupopsport.HistoricalDirectory
	var sourceID, ownerID pgtype.Int8
	var displayName, ownerName, sourceOwnerReference pgtype.Text
	var memberCount, internalCount, externalCount pgtype.Int4
	var recordedAt pgtype.Timestamptz
	err = tx.QueryRow(ctx, `SELECT id,source_kind,source_id,chat_reference,display_name,owner_staff_id,owner_name,member_count,internal_member_count,external_member_count,original_status,recorded_at,source_owner_reference FROM group_ops_v1_history_directory WHERE id=$1`, id).Scan(&x.ID, &x.SourceKind, &sourceID, &x.ChatReference, &displayName, &ownerID, &ownerName, &memberCount, &internalCount, &externalCount, &x.OriginalStatus, &recordedAt, &sourceOwnerReference)
	if err != nil {
		return groupopsport.HistoricalDirectory{}, err
	}
	x.SourceID, x.DisplayName, x.OwnerStaffID, x.OwnerName = nullableInt64(sourceID), nullableText(displayName), nullableInt64(ownerID), nullableText(ownerName)
	x.MemberCount, x.InternalMemberCount, x.ExternalMemberCount, x.RecordedAt, x.SourceOwnerReference = nullableInt32(memberCount), nullableInt32(internalCount), nullableInt32(externalCount), recordedAt.Time, nullableText(sourceOwnerReference)
	return x, nil
}
func (r *Repository) getHistoricalGroup(ctx context.Context, id int64) (groupopsport.HistoricalGroup, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return groupopsport.HistoricalGroup{}, err
	}
	var x groupopsport.HistoricalGroup
	var owner pgtype.Int8
	var removed pgtype.Timestamptz
	var ref pgtype.Text
	err = tx.QueryRow(ctx, `SELECT id,source_group_id,source_plan_id,plan_id,chat_reference,display_name,owner_staff_id,internal_member_count,external_member_count,original_status,created_at,removed_at,source_owner_reference FROM group_ops_v1_history_groups WHERE id=$1`, id).Scan(&x.ID, &x.SourceGroupID, &x.SourcePlanID, &x.PlanID, &x.ChatReference, &x.DisplayName, &owner, &x.InternalMemberCount, &x.ExternalMemberCount, &x.OriginalStatus, &x.CreatedAt, &removed, &ref)
	x.OwnerStaffID, x.RemovedAt, x.SourceOwnerReference = nullableInt64(owner), nullableTime(removed), nullableText(ref)
	return x, err
}
func (r *Repository) getHistoricalNode(ctx context.Context, id int64) (groupopsport.HistoricalNode, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return groupopsport.HistoricalNode{}, err
	}
	var x groupopsport.HistoricalNode
	err = tx.QueryRow(ctx, `SELECT id,source_node_id,source_plan_id,plan_id,day_index,trigger_time,sort_order,original_status,content_package,created_at,updated_at FROM group_ops_v1_history_nodes WHERE id=$1`, id).Scan(&x.ID, &x.SourceNodeID, &x.SourcePlanID, &x.PlanID, &x.DayIndex, &x.TriggerTime, &x.SortOrder, &x.OriginalStatus, &x.ContentPackage, &x.CreatedAt, &x.UpdatedAt)
	if err != nil {
		return x, err
	}
	return x, hydrateHistoricalNodeContent(&x)
}

func hydrateHistoricalNodeContent(item *groupopsport.HistoricalNode) error {
	if item == nil || !json.Valid(item.ContentPackage) {
		return ErrInvalid
	}
	var wrapped struct {
		ActionTitle json.RawMessage `json:"source_action_title"`
		TextContent json.RawMessage `json:"source_text_content"`
		Attachments json.RawMessage `json:"source_attachments"`
	}
	if err := json.Unmarshal(item.ContentPackage, &wrapped); err != nil {
		return ErrInvalid
	}
	if len(wrapped.ActionTitle) > 0 && json.Unmarshal(wrapped.ActionTitle, &item.ActionTitle) != nil {
		return ErrInvalid
	}
	if len(wrapped.TextContent) > 0 && json.Unmarshal(wrapped.TextContent, &item.TextContent) != nil {
		return ErrInvalid
	}
	if len(wrapped.Attachments) > 0 {
		if !json.Valid(wrapped.Attachments) {
			return ErrInvalid
		}
		item.Attachments = append(json.RawMessage(nil), wrapped.Attachments...)
	} else {
		item.Attachments = json.RawMessage(`[]`)
	}
	return nil
}
