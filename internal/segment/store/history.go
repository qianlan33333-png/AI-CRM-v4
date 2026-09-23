package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	segmentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/domain"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
)

var _ segmentport.HistoricalImporter = (*Repository)(nil)

// ImportHistorical writes only Segment-owned tables under the caller's UoW.
// The ordinary receipt is also the migration ledger: all source maps, immutable
// encrypted evidence, static snapshots and safe audit facts commit together.
func (r *Repository) ImportHistorical(ctx context.Context, in segmentport.HistoricalImport) (out segmentport.HistoricalImportResult, err error) {
	if !in.Actor.Valid() || in.Source == "" || len(in.Source) > 120 || in.CapturedAt.IsZero() || len(in.EncryptedEvidence) < 32 || in.Digest == (segmentport.Digest{}) {
		return out, ErrInvalid
	}

	// The source-row ledger must account for every materialized group/package/member.
	indexed := map[string]map[int64]bool{"group": {}, "package": {}, "version": {}, "member": {}}
	for _, row := range in.Rows {
		bucket, ok := indexed[row.Kind]
		if !ok || row.SourceID < 1 || bucket[row.SourceID] {
			return out, ErrInvalid
		}
		bucket[row.SourceID] = true
	}
	check := func(kind string, id int64) bool {
		if !indexed[kind][id] {
			return false
		}
		delete(indexed[kind], id)
		return true
	}
	for _, g := range in.Groups {
		if !check("group", g.SourceID) {
			return out, ErrInvalid
		}
	}
	for _, p := range in.Packages {
		if !check("package", p.SourceID) {
			return out, ErrInvalid
		}
		for _, m := range p.Members {
			if !check("member", m.SourceID) {
				return out, ErrInvalid
			}
		}
	}
	if len(indexed["group"])+len(indexed["package"])+len(indexed["member"]) != 0 {
		return out, ErrInvalid
	}
	t, err := tx(ctx)
	if err != nil {
		return out, err
	}
	// Serialize imports for the same source, including different snapshot digests.
	if _, err = t.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "segment-history:"+in.Source); err != nil {
		return out, err
	}
	now := time.Now().UTC()
	receipt, owned, err := r.Reserve(ctx, Reservation{Operation: "historical_import.v1", ActorScope: in.Actor.Reference, ActorKind: string(in.Actor.Kind), ActorRef: in.Actor.Reference, KeyDigest: sha256.Sum256([]byte(in.Source + ":" + hex.EncodeToString(in.Digest[:]))), PayloadDigest: [32]byte(in.Digest), CreatedAt: now})
	if err != nil {
		return out, err
	}
	if !owned {
		if receipt.State != "completed" {
			return out, ErrConflict
		}
		err = json.Unmarshal(receipt.ResultSnapshot, &out)
		out.Replayed = true
		return out, err
	}
	// A different operator cannot accidentally repeat the same source envelope.
	var existing bool
	if err = t.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM segment_audience_history_batches WHERE source=$1 AND digest=$2)`, in.Source, in.Digest[:]).Scan(&existing); err != nil {
		return out, err
	}
	if existing {
		return out, ErrConflict
	}
	var latestID int64
	var latestCaptured, latestImported time.Time
	var latestDigest, latestSourceDigest []byte
	latestErr := t.QueryRow(ctx, `SELECT id,captured_at,imported_at,digest,resolution_source_digest FROM segment_audience_history_batches WHERE source=$1 ORDER BY id DESC LIMIT 1`, in.Source).Scan(&latestID, &latestCaptured, &latestImported, &latestDigest, &latestSourceDigest)
	if latestErr != nil && !errors.Is(latestErr, pgx.ErrNoRows) {
		return out, latestErr
	}
	derived := in.ResolutionSourceDigest != (segmentport.Digest{})
	if derived != !in.ResolutionDerivedAt.IsZero() {
		return out, ErrInvalid
	}
	if derived && in.ResolutionDerivedAt.Before(in.CapturedAt) {
		return out, ErrInvalid
	}
	if latestErr == nil && !in.CapturedAt.After(latestCaptured) {
		if !in.CapturedAt.Equal(latestCaptured) || !derived || !in.ResolutionDerivedAt.After(latestImported) || !bytes.Equal(latestDigest, in.ResolutionParentDigest[:]) {
			return out, ErrConflict
		}
		if len(latestSourceDigest) > 0 && !bytes.Equal(latestSourceDigest, in.ResolutionSourceDigest[:]) {
			return out, ErrConflict
		}
		var count int
		if err = t.QueryRow(ctx, `SELECT count(*) FROM segment_audience_history_rows WHERE batch_id=$1`, latestID).Scan(&count); err != nil {
			return out, err
		}
		if count != len(in.Rows) {
			return out, ErrConflict
		}
		for _, row := range in.Rows {
			var prior []byte
			if err = t.QueryRow(ctx, `SELECT digest FROM segment_audience_history_rows WHERE batch_id=$1 AND kind=$2 AND source_id=$3`, latestID, row.Kind, row.SourceID).Scan(&prior); err != nil || !bytes.Equal(prior, row.Digest[:]) {
				return out, ErrConflict
			}
		}
		for _, pkg := range in.Packages {
			for _, member := range pkg.Members {
				var priorCustomer *int64
				var priorDisposition string
				if err = t.QueryRow(ctx, `SELECT customer_id,disposition FROM segment_audience_history_members WHERE batch_id=$1 AND source_id=$2`, latestID, member.SourceID).Scan(&priorCustomer, &priorDisposition); err != nil {
					return out, ErrConflict
				}
				if priorDisposition == "resolved" && (priorCustomer == nil || member.Reason != "resolved" || *priorCustomer != member.CustomerID) {
					return out, ErrConflict
				}
				if (priorDisposition == "exited") != (!member.Active) {
					return out, ErrConflict
				}
			}
		}
	} else if in.ResolutionParentDigest != (segmentport.Digest{}) {
		return out, ErrConflict
	}
	var batch int64
	err = t.QueryRow(ctx, `INSERT INTO segment_audience_history_batches(source,digest,captured_at,encrypted_evidence,imported_at,resolution_source_digest,resolution_parent_digest,resolution_derived_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`, in.Source, in.Digest[:], in.CapturedAt, in.EncryptedEvidence, now, optionalHistoryDigest(in.ResolutionSourceDigest), optionalHistoryDigest(in.ResolutionParentDigest), optionalHistoryTime(in.ResolutionDerivedAt)).Scan(&batch)
	if err != nil {
		return out, err
	}
	for _, row := range in.Rows {
		if row.SourceID < 1 || row.Digest == (segmentport.Digest{}) {
			return out, ErrInvalid
		}
		if _, err = t.Exec(ctx, `INSERT INTO segment_audience_history_rows(batch_id,kind,source_id,digest) VALUES($1,$2,$3,$4)`, batch, row.Kind, row.SourceID, row.Digest[:]); err != nil {
			return out, err
		}
	}
	out.SourceRows = len(in.Rows)
	groups := map[int64]int64{}
	for _, g := range in.Groups {
		if g.SourceID < 1 || strings.TrimSpace(g.Name) == "" {
			return out, ErrInvalid
		}
		var id, expectedVersion int64
		err = t.QueryRow(ctx, `SELECT target_id,target_version FROM segment_audience_history_groups WHERE source=$1 AND source_id=$2`, in.Source, g.SourceID).Scan(&id, &expectedVersion)
		if errors.Is(err, pgx.ErrNoRows) {
			err = t.QueryRow(ctx, `INSERT INTO segment_audience_groups(name,created_by,updated_by,created_actor_kind,created_actor_ref,updated_actor_kind,updated_actor_ref,created_at,updated_at) VALUES($1,NULLIF($2,0),NULLIF($2,0),$3,$4,$3,$4,$5,$5) RETURNING id`, g.Name, in.Actor.StaffID, string(in.Actor.Kind), in.Actor.Reference, now).Scan(&id)
			if err == nil {
				_, err = t.Exec(ctx, `INSERT INTO segment_audience_history_groups(source,source_id,target_id) VALUES($1,$2,$3)`, in.Source, g.SourceID, id)
			}
		} else if err == nil {
			tag, updateErr := t.Exec(ctx, `UPDATE segment_audience_groups SET name=$2,version=version+1,updated_by=NULLIF($3,0),updated_actor_kind=$4,updated_actor_ref=$5,updated_at=$6 WHERE id=$1 AND version=$7`, id, g.Name, in.Actor.StaffID, string(in.Actor.Kind), in.Actor.Reference, now, expectedVersion)
			err = updateErr
			if err == nil && tag.RowsAffected() != 1 {
				return out, ErrConflict
			}
			if err == nil {
				_, err = t.Exec(ctx, `UPDATE segment_audience_history_groups SET target_version=target_version+1 WHERE source=$1 AND source_id=$2`, in.Source, g.SourceID)
			}
		}
		if err != nil {
			return out, err
		}
		groups[g.SourceID] = id
		out.Groups++
	}
	for _, p := range in.Packages {
		if p.SourceID < 1 || strings.TrimSpace(p.Name) == "" {
			return out, ErrInvalid
		}
		var group any
		if p.GroupSourceID != 0 {
			id, ok := groups[p.GroupSourceID]
			if !ok {
				return out, ErrInvalid
			}
			group = id
		}
		lifecycle := "paused"
		var archived any
		if p.Archived {
			lifecycle = "archived"
			archived = now
		}
		var id, expectedVersion int64
		err = t.QueryRow(ctx, `SELECT target_id,target_version FROM segment_audience_history_packages WHERE source=$1 AND source_id=$2`, in.Source, p.SourceID).Scan(&id, &expectedVersion)
		if errors.Is(err, pgx.ErrNoRows) {
			codeSum := sha256.Sum256([]byte(fmt.Sprintf("%s:%d", in.Source, p.SourceID)))
			err = t.QueryRow(ctx, `INSERT INTO segment_audience_packages(group_id,code,name,lifecycle,created_by,updated_by,created_actor_kind,created_actor_ref,updated_actor_kind,updated_actor_ref,created_at,updated_at,archived_at,historical_import) VALUES($1,$2,$3,$4,NULLIF($5,0),NULLIF($5,0),$6,$7,$6,$7,$8,$8,$9,true) RETURNING id`, group, "hist_"+hex.EncodeToString(codeSum[:]), p.Name, lifecycle, in.Actor.StaffID, string(in.Actor.Kind), in.Actor.Reference, now, archived).Scan(&id)
			if err == nil {
				_, err = t.Exec(ctx, `INSERT INTO segment_audience_history_packages(source,source_id,target_id) VALUES($1,$2,$3)`, in.Source, p.SourceID, id)
			}
		} else if err == nil {
			tag, updateErr := t.Exec(ctx, `UPDATE segment_audience_packages SET group_id=$2,name=$3,lifecycle=$4,archived_at=$5,version=version+1,updated_at=$6 WHERE id=$1 AND historical_import=true AND version=$7`, id, group, p.Name, lifecycle, archived, now, expectedVersion)
			err = updateErr
			if err == nil && tag.RowsAffected() != 1 {
				return out, ErrConflict
			}
			if err == nil {
				_, err = t.Exec(ctx, `UPDATE segment_audience_history_packages SET target_version=target_version+1 WHERE source=$1 AND source_id=$2`, in.Source, p.SourceID)
			}
		}
		if err != nil {
			return out, err
		}
		// This closed marker is deliberately rejected by the runtime DSL compiler.
		// Original SQL/templates stay exclusively in the encrypted source envelope.
		definition := []byte(`{"schema_version":1,"historical_snapshot":true}`)
		defDigest := sha256.Sum256(definition)
		var configuration int64
		err = t.QueryRow(ctx, `INSERT INTO segment_audience_configuration_versions(package_id,version,schema_version,definition,refresh_mode,digest,created_by,created_actor_kind,created_actor_ref,created_at) SELECT $1,COALESCE(max(version),0)+1,1,$2::jsonb,'manual',$3,NULLIF($4,0),$5,$6,$7 FROM segment_audience_configuration_versions WHERE package_id=$1 RETURNING id`, id, definition, defDigest[:], in.Actor.StaffID, string(in.Actor.Kind), in.Actor.Reference, now).Scan(&configuration)
		if err != nil {
			return out, err
		}
		members := map[int64]time.Time{}
		for _, m := range p.Members {
			reason := m.Reason
			var customer any
			if !m.Active {
				reason = "exited"
				out.Exited++
			} else if reason == "resolved" && m.CustomerID > 0 && !m.EnteredAt.IsZero() {
				customer = m.CustomerID
				out.ResolvedActive++
				if old, ok := members[m.CustomerID]; !ok || m.EnteredAt.Before(old) {
					members[m.CustomerID] = m.EnteredAt
				}
			} else {
				if reason == "resolved" {
					return out, ErrInvalid
				}
				out.Quarantined++
			}
			if _, err = t.Exec(ctx, `INSERT INTO segment_audience_history_members(batch_id,source_id,package_id,customer_id,disposition) VALUES($1,$2,$3,$4,$5)`, batch, m.SourceID, id, customer, reason); err != nil {
				return out, err
			}
			out.SourceMembers++
		}
		if len(members) > segmentport.MaximumEvaluationMembers {
			return out, ErrInvalid
		}
		ids := make([]int64, 0, len(members))
		for cid := range members {
			ids = append(ids, cid)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		canonicalIDs := make([]customerdomain.CustomerID, len(ids))
		for i, id := range ids {
			canonicalIDs[i] = customerdomain.CustomerID(id)
		}
		memberDigest := segmentdomain.DigestMembers(canonicalIDs)
		var run, snap int64
		err = t.QueryRow(ctx, `INSERT INTO segment_audience_refresh_runs(package_id,configuration_version_id,source_key_digest,reference_time,state,created_at,updated_at,completed_at,refresh_kind) VALUES($1,$2,$3,$4,'published',$5,$5,$5,'manual') RETURNING id`, id, configuration, in.Digest[:], in.CapturedAt, now).Scan(&run)
		if err != nil {
			return out, err
		}
		err = t.QueryRow(ctx, `INSERT INTO segment_audience_snapshots(package_id,configuration_version_id,refresh_run_id,state,reference_time,member_count,member_digest,source_watermark_digest,created_at,published_at) VALUES($1,$2,$3,'published',$4,$5,$6,$7,$8,$8) RETURNING id`, id, configuration, run, in.CapturedAt, len(ids), memberDigest[:], in.Digest[:], now).Scan(&snap)
		if err != nil {
			return out, err
		}
		for _, cid := range ids {
			if _, err = t.Exec(ctx, `INSERT INTO segment_audience_snapshot_members(snapshot_id,customer_id,entered_at,identity_disposition) VALUES($1,$2,$3,'resolved')`, snap, cid, members[cid]); err != nil {
				return out, err
			}
		}
		if _, err = t.Exec(ctx, `UPDATE segment_audience_packages SET current_configuration_version_id=$2,published_snapshot_id=$3 WHERE id=$1`, id, configuration, snap); err != nil {
			return out, err
		}
		if _, err = t.Exec(ctx, `INSERT INTO segment_audience_history_snapshots(batch_id,package_id,snapshot_id) VALUES($1,$2,$3)`, batch, id, snap); err != nil {
			return out, err
		}
		// Explicit historical fact; no member entered/exited event or queue acceptance.
		payload, _ := json.Marshal(map[string]any{"snapshot_id": snap, "members": len(ids), "historical": true})
		_, err = r.AppendMutationFacts(ctx, MutationFact{ResourceKind: "package", ResourceID: id, Operation: "historical_import.v1", EventType: "segment.historical_snapshot_imported.v1", ActorID: in.Actor.StaffID, ActorKind: string(in.Actor.Kind), ActorRef: in.Actor.Reference, Payload: payload, IdempotencyKey: fmt.Sprintf("history:%s:%x:%d", in.Source, in.Digest, p.SourceID), OccurredAt: now})
		if err != nil {
			return out, err
		}
		out.Packages++
		out.PublishedMembers += len(ids)
	}
	if out.SourceMembers != out.ResolvedActive+out.Exited+out.Quarantined {
		return out, ErrConflict
	}
	result, _ := json.Marshal(out)
	_, err = r.Complete(ctx, receipt.ID, result, now)
	return out, err
}

// VerifyHistorical counts all source rows and compares exact canonical member sets,
// not merely the package total. Older imported snapshots remain independently readable.
func (r *Repository) VerifyHistorical(ctx context.Context, source string, digest segmentport.Digest) (out segmentport.HistoricalImportResult, err error) {
	t, err := tx(ctx)
	if err != nil {
		return out, err
	}
	var batch int64
	err = t.QueryRow(ctx, `SELECT id FROM segment_audience_history_batches WHERE source=$1 AND digest=$2`, source, digest[:]).Scan(&batch)
	if err != nil {
		return out, err
	}
	err = t.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE kind='group'),count(*) FILTER(WHERE kind='package') FROM segment_audience_history_rows WHERE batch_id=$1`, batch).Scan(&out.SourceRows, &out.Groups, &out.Packages)
	if err != nil {
		return out, err
	}
	err = t.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE disposition='resolved'),count(*) FILTER(WHERE disposition='exited'),count(*) FILTER(WHERE disposition NOT IN('resolved','exited')) FROM segment_audience_history_members WHERE batch_id=$1`, batch).Scan(&out.SourceMembers, &out.ResolvedActive, &out.Exited, &out.Quarantined)
	if err != nil {
		return out, err
	}
	var mismatch int
	err = t.QueryRow(ctx, `SELECT count(*) FROM (
 (SELECT h.package_id,m.customer_id FROM segment_audience_history_snapshots h JOIN segment_audience_snapshot_members m ON m.snapshot_id=h.snapshot_id WHERE h.batch_id=$1 EXCEPT SELECT package_id,customer_id FROM segment_audience_history_members WHERE batch_id=$1 AND disposition='resolved')
 UNION ALL
 (SELECT package_id,customer_id FROM segment_audience_history_members WHERE batch_id=$1 AND disposition='resolved' EXCEPT SELECT h.package_id,m.customer_id FROM segment_audience_history_snapshots h JOIN segment_audience_snapshot_members m ON m.snapshot_id=h.snapshot_id WHERE h.batch_id=$1)) x`, batch).Scan(&mismatch)
	if err != nil {
		return out, err
	}
	if mismatch != 0 {
		return out, ErrConflict
	}
	var packages, sourceMembers int
	err = t.QueryRow(ctx, `SELECT count(*),COALESCE(sum(s.member_count),0) FROM segment_audience_history_snapshots h JOIN segment_audience_snapshots s ON s.id=h.snapshot_id JOIN segment_audience_packages p ON p.id=h.package_id WHERE h.batch_id=$1 AND s.state='published' AND p.historical_import AND p.lifecycle<>'active'`, batch).Scan(&packages, &out.PublishedMembers)
	if err != nil {
		return out, err
	}
	err = t.QueryRow(ctx, `SELECT count(*) FROM segment_audience_history_rows WHERE batch_id=$1 AND kind='member'`, batch).Scan(&sourceMembers)
	if err != nil {
		return out, err
	}
	if packages != out.Packages || sourceMembers != out.SourceMembers {
		return out, ErrConflict
	}
	return out, nil
}

func optionalHistoryDigest(d segmentport.Digest) any {
	if d == (segmentport.Digest{}) {
		return nil
	}
	return d[:]
}
func optionalHistoryTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}
