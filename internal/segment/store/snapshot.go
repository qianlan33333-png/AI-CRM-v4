package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	segmentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/domain"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
)

const refreshColumns = `id,package_id,configuration_version_id,source_key_digest,reference_time,refresh_kind,state,river_job_id,error_code,created_at,updated_at,completed_at`

func scanRefresh(row pgx.Row) (segmentdomain.RefreshRun, error) {
	var run segmentdomain.RefreshRun
	var digest []byte
	var state string
	var errorCode *string
	err := row.Scan(&run.ID, &run.PackageID, &run.ConfigurationVersionID, &digest, &run.ReferenceTime, &run.RefreshKind, &state, &run.RiverJobID, &errorCode, &run.CreatedAt, &run.UpdatedAt, &run.CompletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return run, ErrNotFound
	}
	if err != nil {
		return run, err
	}
	if len(digest) != sha256.Size {
		return run, ErrConflict
	}
	copy(run.SourceKeyDigest[:], digest)
	run.State = segmentdomain.RefreshState(state)
	if errorCode != nil {
		run.ErrorCode = *errorCode
	}
	return run, nil
}

func (r *Repository) ReserveRefresh(ctx context.Context, run segmentdomain.RefreshRun) (segmentdomain.RefreshRun, bool, error) {
	if !segmentdomain.ValidRefreshKind(run.RefreshKind) {
		return run, false, ErrInvalid
	}
	t, err := tx(ctx)
	if err != nil {
		return run, false, err
	}
	query := `INSERT INTO segment_audience_refresh_runs(package_id,configuration_version_id,source_key_digest,reference_time,refresh_kind,state,created_at,updated_at)
		VALUES($1,$2,$3,$4,$5,'accepted',$6,$6) ON CONFLICT(package_id,source_key_digest) DO NOTHING RETURNING ` + refreshColumns
	created, err := scanRefresh(t.QueryRow(ctx, query, run.PackageID, run.ConfigurationVersionID, run.SourceKeyDigest[:], run.ReferenceTime, run.RefreshKind, run.CreatedAt))
	if err == nil {
		return created, true, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return run, false, err
	}
	existing, err := scanRefresh(t.QueryRow(ctx, `SELECT `+refreshColumns+` FROM segment_audience_refresh_runs WHERE package_id=$1 AND source_key_digest=$2 FOR UPDATE`, run.PackageID, run.SourceKeyDigest[:]))
	if err != nil {
		return run, false, err
	}
	if existing.ConfigurationVersionID != run.ConfigurationVersionID || !existing.ReferenceTime.Equal(run.ReferenceTime) {
		return run, false, ErrConflict
	}
	// A daily request may join an accepted/queued incremental occurrence, but
	// must never replace a run once its partial result has started.
	if run.RefreshKind == segmentdomain.RefreshDaily && existing.RefreshKind != segmentdomain.RefreshDaily {
		if existing.State != segmentdomain.RefreshAccepted && existing.State != segmentdomain.RefreshQueued {
			return run, false, ErrConflict
		}
		existing, err = scanRefresh(t.QueryRow(ctx, `UPDATE segment_audience_refresh_runs SET refresh_kind='daily',updated_at=$2 WHERE id=$1 RETURNING `+refreshColumns, existing.ID, run.UpdatedAt))
		if err != nil {
			return run, false, err
		}
	}
	return existing, false, nil
}

func (r *Repository) AttachRefreshJob(ctx context.Context, runID, jobID int64, now time.Time) (segmentdomain.RefreshRun, error) {
	t, err := tx(ctx)
	if err != nil {
		return segmentdomain.RefreshRun{}, err
	}
	query := `UPDATE segment_audience_refresh_runs SET river_job_id=$2,state='queued',updated_at=$3
		WHERE id=$1 AND state='accepted' AND river_job_id IS NULL RETURNING ` + refreshColumns
	run, err := scanRefresh(t.QueryRow(ctx, query, runID, jobID, now))
	if errors.Is(err, ErrNotFound) {
		return segmentdomain.RefreshRun{}, ErrConflict
	}
	return run, err
}

func (r *Repository) Refresh(ctx context.Context, runID int64) (segmentdomain.RefreshRun, error) {
	t, err := tx(ctx)
	if err != nil {
		return segmentdomain.RefreshRun{}, err
	}
	return scanRefresh(t.QueryRow(ctx, `SELECT `+refreshColumns+` FROM segment_audience_refresh_runs WHERE id=$1`, runID))
}

func (r *Repository) BeginRefresh(ctx context.Context, runID int64, now time.Time) (segmentdomain.RefreshRun, segmentdomain.Snapshot, error) {
	t, err := tx(ctx)
	if err != nil {
		return segmentdomain.RefreshRun{}, segmentdomain.Snapshot{}, err
	}
	run, err := scanRefresh(t.QueryRow(ctx, `UPDATE segment_audience_refresh_runs SET state='evaluating',updated_at=$2,error_code=NULL
		WHERE id=$1 AND state IN ('queued','evaluating','staging') RETURNING `+refreshColumns, runID, now))
	if err != nil {
		return run, segmentdomain.Snapshot{}, err
	}
	_, err = t.Exec(ctx, `INSERT INTO segment_audience_snapshots(package_id,configuration_version_id,refresh_run_id,state,reference_time,created_at)
		VALUES($1,$2,$3,'preparing',$4,$5) ON CONFLICT(refresh_run_id) DO NOTHING`, run.PackageID, run.ConfigurationVersionID, run.ID, run.ReferenceTime, now)
	if err != nil {
		return run, segmentdomain.Snapshot{}, err
	}
	snapshot, err := scanSnapshot(t.QueryRow(ctx, `SELECT `+snapshotColumns+` FROM segment_audience_snapshots WHERE refresh_run_id=$1`, runID))
	return run, snapshot, err
}

const snapshotColumns = `id,package_id,configuration_version_id,refresh_run_id,state,reference_time,member_count,member_digest,source_watermark_digest,created_at,published_at`

func scanSnapshot(row pgx.Row) (segmentdomain.Snapshot, error) {
	var item segmentdomain.Snapshot
	var memberDigest, watermarkDigest []byte
	err := row.Scan(&item.ID, &item.PackageID, &item.ConfigurationVersionID, &item.RefreshRunID, &item.State, &item.ReferenceTime, &item.MemberCount, &memberDigest, &watermarkDigest, &item.CreatedAt, &item.PublishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return item, ErrNotFound
	}
	if err != nil {
		return item, err
	}
	if memberDigest != nil {
		if len(memberDigest) != sha256.Size {
			return item, ErrConflict
		}
		copy(item.MemberDigest[:], memberDigest)
	}
	if watermarkDigest != nil {
		if len(watermarkDigest) != sha256.Size {
			return item, ErrConflict
		}
		copy(item.SourceWatermarkDigest[:], watermarkDigest)
	}
	return item, nil
}

func (r *Repository) StageRefreshBatch(ctx context.Context, runID int64, ordinal int, ids []customerdomain.CustomerID, digest [32]byte, now time.Time) error {
	if runID < 1 || ordinal < 0 || len(ids) < 1 || len(ids) > 1000 || digest != segmentdomain.DigestMembers(ids) {
		return ErrInvalid
	}
	for i, id := range ids {
		if id < 1 || (i > 0 && ids[i-1] >= id) {
			return ErrInvalid
		}
	}
	t, err := tx(ctx)
	if err != nil {
		return err
	}
	var snapshotID int64
	err = t.QueryRow(ctx, `SELECT s.id FROM segment_audience_snapshots s JOIN segment_audience_refresh_runs r ON r.id=s.refresh_run_id
		WHERE r.id=$1 AND r.state IN ('evaluating','staging') AND s.state='preparing' FOR UPDATE OF r`, runID).Scan(&snapshotID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	var existingCount int
	var existingDigest []byte
	err = t.QueryRow(ctx, `SELECT member_count,member_digest FROM segment_audience_refresh_batches WHERE refresh_run_id=$1 AND batch_ordinal=$2`, runID, ordinal).Scan(&existingCount, &existingDigest)
	if err == nil {
		if existingCount != len(ids) || len(existingDigest) != sha256.Size || string(existingDigest) != string(digest[:]) {
			return ErrConflict
		}
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	memberIDs := make([]int64, len(ids))
	for index, id := range ids {
		memberIDs[index] = int64(id)
	}
	if _, err = t.Exec(ctx, `INSERT INTO segment_audience_snapshot_members(snapshot_id,customer_id,entered_at,identity_disposition)
		SELECT $1,member_id,$3,'resolved' FROM unnest($2::bigint[]) AS member_id`, snapshotID, memberIDs, now); err != nil {
		if unique(err) {
			return ErrConflict
		}
		return err
	}
	_, err = t.Exec(ctx, `INSERT INTO segment_audience_refresh_batches(refresh_run_id,batch_ordinal,first_customer_id,last_customer_id,member_count,member_digest,completed_at)
		VALUES($1,$2,$3,$4,$5,$6,$7)`, runID, ordinal, ids[0], ids[len(ids)-1], len(ids), digest[:], now)
	if err != nil {
		return err
	}
	_, err = t.Exec(ctx, `UPDATE segment_audience_refresh_runs SET state='staging',updated_at=$2 WHERE id=$1 AND state IN ('evaluating','staging')`, runID, now)
	return err
}

func (r *Repository) PublishRefresh(ctx context.Context, runID int64, expectedCount int64, expectedMemberDigest, watermarkDigest [32]byte, actor int64, now time.Time) (segmentdomain.PublishedRefresh, error) {
	return r.PublishRefreshWithActor(ctx, runID, expectedCount, expectedMemberDigest, watermarkDigest, adminActor(actor), now)
}

func (r *Repository) PublishRefreshWithActor(ctx context.Context, runID int64, expectedCount int64, expectedMemberDigest, watermarkDigest [32]byte, actor Actor, now time.Time) (segmentdomain.PublishedRefresh, error) {
	if runID < 1 || expectedCount < 0 || expectedCount > segmentport.MaximumEvaluationMembers || !actor.Valid() {
		return segmentdomain.PublishedRefresh{}, ErrInvalid
	}
	t, err := tx(ctx)
	if err != nil {
		return segmentdomain.PublishedRefresh{}, err
	}
	run, err := scanRefresh(t.QueryRow(ctx, `SELECT `+refreshColumns+` FROM segment_audience_refresh_runs WHERE id=$1 FOR UPDATE`, runID))
	if err != nil {
		return segmentdomain.PublishedRefresh{}, err
	}
	if run.State == segmentdomain.RefreshPublished {
		snapshot, queryErr := scanSnapshot(t.QueryRow(ctx, `SELECT `+snapshotColumns+` FROM segment_audience_snapshots WHERE refresh_run_id=$1 AND state='published'`, runID))
		return segmentdomain.PublishedRefresh{Snapshot: snapshot}, queryErr
	}
	if run.State != segmentdomain.RefreshEvaluating && run.State != segmentdomain.RefreshStaging {
		return segmentdomain.PublishedRefresh{}, ErrConflict
	}
	var currentConfiguration int64
	var previousID *int64
	var previousReference *time.Time
	if err = t.QueryRow(ctx, `SELECT current_configuration_version_id FROM segment_audience_packages WHERE id=$1 FOR UPDATE`, run.PackageID).Scan(&currentConfiguration); err != nil {
		return segmentdomain.PublishedRefresh{}, err
	}
	if currentConfiguration != run.ConfigurationVersionID {
		return segmentdomain.PublishedRefresh{}, ErrConflict
	}
	var currentID int64
	var currentReference time.Time
	if err = t.QueryRow(ctx, `SELECT s.id,s.reference_time FROM segment_audience_packages p JOIN segment_audience_snapshots s ON s.id=p.published_snapshot_id AND s.state='published' WHERE p.id=$1`, run.PackageID).Scan(&currentID, &currentReference); err == nil {
		previousID, previousReference = &currentID, &currentReference
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return segmentdomain.PublishedRefresh{}, err
	}
	if previousReference != nil && previousReference.After(run.ReferenceTime) {
		return segmentdomain.PublishedRefresh{}, ErrConflict
	}
	snapshot, err := scanSnapshot(t.QueryRow(ctx, `SELECT `+snapshotColumns+` FROM segment_audience_snapshots WHERE refresh_run_id=$1 AND state='preparing' FOR UPDATE`, runID))
	if err != nil {
		return segmentdomain.PublishedRefresh{}, err
	}
	// Verify the staged evaluation before an incremental run merges its additions
	// into the currently published audience.
	stagedRows, err := t.Query(ctx, `SELECT customer_id FROM segment_audience_snapshot_members WHERE snapshot_id=$1 ORDER BY customer_id`, snapshot.ID)
	if err != nil {
		return segmentdomain.PublishedRefresh{}, err
	}
	staged := []customerdomain.CustomerID{}
	for stagedRows.Next() {
		var id customerdomain.CustomerID
		if err = stagedRows.Scan(&id); err != nil {
			stagedRows.Close()
			return segmentdomain.PublishedRefresh{}, err
		}
		staged = append(staged, id)
	}
	stagedRows.Close()
	if err = stagedRows.Err(); err != nil || int64(len(staged)) != expectedCount || segmentdomain.DigestMembers(staged) != expectedMemberDigest {
		return segmentdomain.PublishedRefresh{}, ErrConflict
	}
	if !run.RefreshKind.IsComplete() && previousID != nil {
		if _, err = t.Exec(ctx, `INSERT INTO segment_audience_snapshot_members(snapshot_id,customer_id,entered_at,identity_disposition)
			SELECT $1,previous.customer_id,$2,'resolved' FROM segment_audience_snapshot_members previous
			WHERE previous.snapshot_id=$3 AND NOT EXISTS (SELECT 1 FROM segment_audience_snapshot_members added WHERE added.snapshot_id=$1 AND added.customer_id=previous.customer_id)`, snapshot.ID, now, *previousID); err != nil {
			return segmentdomain.PublishedRefresh{}, err
		}
	}
	rows, err := t.Query(ctx, `SELECT customer_id FROM segment_audience_snapshot_members WHERE snapshot_id=$1 ORDER BY customer_id`, snapshot.ID)
	if err != nil {
		return segmentdomain.PublishedRefresh{}, err
	}
	ids := []customerdomain.CustomerID{}
	for rows.Next() {
		var id customerdomain.CustomerID
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return segmentdomain.PublishedRefresh{}, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return segmentdomain.PublishedRefresh{}, err
	}
	if len(ids) > segmentport.MaximumEvaluationMembers {
		// An incremental run may retain the prior snapshot. Enforce the same
		// bounded audience contract after that merge, not only on its additions.
		return segmentdomain.PublishedRefresh{}, ErrConflict
	}
	memberDigest := segmentdomain.DigestMembers(ids)
	snapshot, err = scanSnapshot(t.QueryRow(ctx, `UPDATE segment_audience_snapshots SET state='published',member_count=$2,member_digest=$3,source_watermark_digest=$4,published_at=$5 WHERE id=$1 AND state='preparing' RETURNING `+snapshotColumns, snapshot.ID, len(ids), memberDigest[:], watermarkDigest[:], now))
	if err != nil {
		return segmentdomain.PublishedRefresh{}, err
	}
	var exited int64
	if run.RefreshKind == segmentdomain.RefreshDaily && previousID != nil {
		command, execErr := t.Exec(ctx, `INSERT INTO segment_audience_member_exit_events(event_id,package_id,snapshot_id,configuration_version_id,customer_id,occurred_at)
			SELECT 'audexit_' || ($1::bigint)::text || '_' || prior.customer_id::text,$2,$1,$3,prior.customer_id,$4
			FROM segment_audience_snapshot_members prior WHERE prior.snapshot_id=$5
			AND NOT EXISTS (SELECT 1 FROM segment_audience_snapshot_members current WHERE current.snapshot_id=$1 AND current.customer_id=prior.customer_id)
			ON CONFLICT(snapshot_id,customer_id) DO NOTHING`, snapshot.ID, run.PackageID, run.ConfigurationVersionID, now, *previousID)
		if execErr != nil {
			return segmentdomain.PublishedRefresh{}, execErr
		}
		exited = command.RowsAffected()
		if exited > 0 {
			payload := []byte(`{"snapshot_id":` + strconv.FormatInt(snapshot.ID, 10) + `,"package_id":` + strconv.FormatInt(snapshot.PackageID, 10) + `,"exited_count":` + strconv.FormatInt(exited, 10) + `}`)
			if _, err = r.AppendMutationFacts(ctx, MutationFact{ResourceKind: "member_exit_batch", ResourceID: snapshot.ID, Operation: "create", EventType: "audience.member_exited.batch.v1", ActorID: actor.StaffID, ActorKind: actor.Kind, ActorRef: actor.Reference, Payload: payload, IdempotencyKey: "member-exits:" + strconv.FormatInt(snapshot.ID, 10), OccurredAt: now}); err != nil {
				return segmentdomain.PublishedRefresh{}, err
			}
		}
	}
	if _, err = t.Exec(ctx, `UPDATE segment_audience_packages SET published_snapshot_id=$2,updated_at=$3 WHERE id=$1`, run.PackageID, snapshot.ID, now); err != nil {
		return segmentdomain.PublishedRefresh{}, err
	}
	if _, err = t.Exec(ctx, `UPDATE segment_audience_refresh_runs SET state='published',updated_at=$2,completed_at=$2 WHERE id=$1`, runID, now); err != nil {
		return segmentdomain.PublishedRefresh{}, err
	}
	payload := []byte(`{"snapshot_id":` + strconv.FormatInt(snapshot.ID, 10) + `,"package_id":` + strconv.FormatInt(snapshot.PackageID, 10) + `,"member_count":` + strconv.FormatInt(snapshot.MemberCount, 10) + `,"refresh_kind":"` + string(run.RefreshKind) + `"}`)
	if _, err = r.AppendMutationFacts(ctx, MutationFact{ResourceKind: "snapshot", ResourceID: snapshot.ID, Operation: "publish", EventType: "audience.snapshot.published.v1", ActorID: actor.StaffID, ActorKind: actor.Kind, ActorRef: actor.Reference, Payload: payload, IdempotencyKey: "snapshot-publish:" + strconv.FormatInt(runID, 10), OccurredAt: now}); err != nil {
		return segmentdomain.PublishedRefresh{}, err
	}
	return segmentdomain.PublishedRefresh{Snapshot: snapshot, PreviousSnapshotID: previousID, ExitedMemberCount: exited}, nil
}
func (r *Repository) FailRefresh(ctx context.Context, runID int64, code string, now time.Time) error {
	if runID < 1 || code == "" || len(code) > 100 {
		return ErrInvalid
	}
	t, err := tx(ctx)
	if err != nil {
		return err
	}
	command, err := t.Exec(ctx, `UPDATE segment_audience_refresh_runs SET state='failed',error_code=$2,updated_at=$3,completed_at=$3 WHERE id=$1 AND state NOT IN ('published','failed')`, runID, code, now)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return nil
	}
	_, err = t.Exec(ctx, `UPDATE segment_audience_snapshots SET state='failed' WHERE refresh_run_id=$1 AND state='preparing'`, runID)
	return err
}

func (r *Repository) PublishedSnapshot(ctx context.Context, packageID segmentport.PackageID) (segmentport.Snapshot, bool, error) {
	t, err := tx(ctx)
	if err != nil {
		return segmentport.Snapshot{}, false, err
	}
	row := t.QueryRow(ctx, `SELECT s.id,s.package_id,s.configuration_version_id,s.state,s.reference_time,s.member_count,s.member_digest,s.source_watermark_digest,s.published_at
		FROM segment_audience_packages p JOIN segment_audience_snapshots s ON s.id=p.published_snapshot_id AND s.package_id=p.id WHERE p.id=$1 AND s.state='published'`, packageID)
	return scanPortSnapshot(row)
}

func (r *Repository) Snapshot(ctx context.Context, snapshotID segmentport.SnapshotID) (segmentport.Snapshot, bool, error) {
	t, err := tx(ctx)
	if err != nil {
		return segmentport.Snapshot{}, false, err
	}
	return scanPortSnapshot(t.QueryRow(ctx, `SELECT id,package_id,configuration_version_id,state,reference_time,member_count,member_digest,source_watermark_digest,published_at FROM segment_audience_snapshots WHERE id=$1 AND state='published'`, snapshotID))
}

func scanPortSnapshot(row pgx.Row) (segmentport.Snapshot, bool, error) {
	var out segmentport.Snapshot
	var state string
	var member, watermark []byte
	err := row.Scan(&out.ID, &out.PackageID, &out.ConfigurationVersionID, &state, &out.ReferenceTime, &out.MemberCount, &member, &watermark, &out.PublishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, false, nil
	}
	if err != nil {
		return out, false, err
	}
	if len(member) != 32 || len(watermark) != 32 {
		return out, false, ErrConflict
	}
	copy(out.MemberDigest[:], member)
	copy(out.SourceWatermarkDigest[:], watermark)
	out.State = segmentport.SnapshotState(state)
	return out, true, nil
}

func (r *Repository) Members(ctx context.Context, snapshotID segmentport.SnapshotID, cursor string, limit int) (segmentport.MemberPage, error) {
	if limit < 1 || limit > 1000 {
		return segmentport.MemberPage{}, ErrInvalid
	}
	after := int64(0)
	var err error
	if cursor != "" {
		after, err = strconv.ParseInt(cursor, 10, 64)
		if err != nil || after < 1 {
			return segmentport.MemberPage{}, ErrInvalid
		}
	}
	t, err := tx(ctx)
	if err != nil {
		return segmentport.MemberPage{}, err
	}
	rows, err := t.Query(ctx, `SELECT m.customer_id,m.entered_at FROM segment_audience_snapshot_members m JOIN segment_audience_snapshots s ON s.id=m.snapshot_id WHERE m.snapshot_id=$1 AND s.state='published' AND m.customer_id>$2 ORDER BY m.customer_id LIMIT $3`, snapshotID, after, limit+1)
	if err != nil {
		return segmentport.MemberPage{}, err
	}
	defer rows.Close()
	page := segmentport.MemberPage{Items: []segmentport.Member{}}
	for rows.Next() {
		var item segmentport.Member
		item.SnapshotID = snapshotID
		item.Disposition = segmentport.IdentityResolved
		if err = rows.Scan(&item.CustomerID, &item.EnteredAt); err != nil {
			return page, err
		}
		page.Items = append(page.Items, item)
	}
	if err = rows.Err(); err != nil {
		return page, err
	}
	if len(page.Items) > limit {
		page.NextCursor = strconv.FormatInt(int64(page.Items[limit-1].CustomerID), 10)
		page.Items = page.Items[:limit]
	}
	return page, nil
}

func (r *Repository) CreateMemberEnteredEvents(ctx context.Context, snapshot segmentdomain.Snapshot, previousSnapshotID *int64, actor int64, occurredAt time.Time) (int64, error) {
	return r.CreateMemberEnteredEventsWithActor(ctx, snapshot, previousSnapshotID, adminActor(actor), occurredAt)
}

func (r *Repository) CreateMemberEnteredEventsWithActor(ctx context.Context, snapshot segmentdomain.Snapshot, previousSnapshotID *int64, actor Actor, occurredAt time.Time) (int64, error) {
	if snapshot.ID < 1 || snapshot.PackageID < 1 || snapshot.ConfigurationVersionID < 1 || snapshot.State != "published" || !actor.Valid() || occurredAt.IsZero() || (previousSnapshotID != nil && *previousSnapshotID < 1) {
		return 0, ErrInvalid
	}
	t, err := tx(ctx)
	if err != nil {
		return 0, err
	}
	result, err := t.Exec(ctx, `INSERT INTO segment_audience_member_events(event_id,package_id,snapshot_id,configuration_version_id,customer_id,occurred_at)
		SELECT 'audmem_' || ($1::bigint)::text || '_' || current.customer_id::text,$2::bigint,$1::bigint,$3::bigint,current.customer_id,$5::timestamptz
		FROM segment_audience_snapshot_members current
		WHERE current.snapshot_id=$1::bigint
		  AND ($4::bigint IS NULL OR NOT EXISTS (SELECT 1 FROM segment_audience_snapshot_members previous WHERE previous.snapshot_id=$4::bigint AND previous.customer_id=current.customer_id))
		ON CONFLICT(snapshot_id,customer_id) DO NOTHING`, snapshot.ID, snapshot.PackageID, snapshot.ConfigurationVersionID, previousSnapshotID, occurredAt.UTC())
	if err != nil {
		return 0, err
	}
	created := result.RowsAffected()
	if created == 0 {
		return 0, nil
	}
	payload := []byte(`{"snapshot_id":` + strconv.FormatInt(snapshot.ID, 10) + `,"package_id":` + strconv.FormatInt(snapshot.PackageID, 10) + `,"event_count":` + strconv.FormatInt(created, 10) + `}`)
	_, err = r.AppendMutationFacts(ctx, MutationFact{ResourceKind: "member_event_batch", ResourceID: snapshot.ID, Operation: "create", EventType: "audience.member_entered.batch.v1", ActorID: actor.StaffID, ActorKind: actor.Kind, ActorRef: actor.Reference, Payload: payload, IdempotencyKey: "member-events:" + strconv.FormatInt(snapshot.ID, 10), OccurredAt: occurredAt.UTC()})
	return created, err
}

func (r *Repository) MemberEvents(ctx context.Context, snapshotID segmentport.SnapshotID, cursor string, limit int) (segmentport.MemberEventPage, error) {
	if snapshotID < 1 || limit < 1 || limit > 1000 {
		return segmentport.MemberEventPage{}, ErrInvalid
	}
	after := int64(0)
	var err error
	if cursor != "" {
		after, err = strconv.ParseInt(cursor, 10, 64)
		if err != nil || after < 1 {
			return segmentport.MemberEventPage{}, ErrInvalid
		}
	}
	t, err := tx(ctx)
	if err != nil {
		return segmentport.MemberEventPage{}, err
	}
	rows, err := t.Query(ctx, `SELECT id,event_id,package_id,snapshot_id,configuration_version_id,customer_id,occurred_at FROM segment_audience_member_events WHERE snapshot_id=$1 AND id>$2 ORDER BY id LIMIT $3`, snapshotID, after, limit+1)
	if err != nil {
		return segmentport.MemberEventPage{}, err
	}
	defer rows.Close()
	page := segmentport.MemberEventPage{Items: []segmentport.MemberEnteredV1{}}
	ids := []int64{}
	for rows.Next() {
		var rowID int64
		var item segmentport.MemberEnteredV1
		if err = rows.Scan(&rowID, &item.EventID, &item.PackageID, &item.SnapshotID, &item.ConfigurationVersionID, &item.CustomerID, &item.OccurredAt); err != nil {
			return page, err
		}
		ids = append(ids, rowID)
		page.Items = append(page.Items, item)
	}
	if err = rows.Err(); err != nil {
		return page, err
	}
	if len(page.Items) > limit {
		page.NextCursor = strconv.FormatInt(ids[limit-1], 10)
		page.Items = page.Items[:limit]
	}
	return page, nil
}

var _ segmentport.SnapshotReader = (*Repository)(nil)
var _ segmentport.MemberEventReader = (*Repository)(nil)
