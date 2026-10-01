package store

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	segmentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/domain"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
)

// ReserveSourceCalibrationReference replaces a queued historical reference
// once, before staging the first unified-source evaluation. Retries retain the
// reserved clock, and concurrent reservations remain silent calibrations.
func (r *Repository) ReserveSourceCalibrationReference(ctx context.Context, runID int64, watermarks []segmentport.SourceWatermark, actor Actor, now time.Time) (time.Time, error) {
	unified := false
	for _, w := range watermarks {
		unified = unified || w.Source == "wecom.directory.published.v2"
	}
	if !unified {
		return time.Time{}, nil
	}
	if !actor.Valid() || now.IsZero() {
		return time.Time{}, ErrInvalid
	}
	t, err := tx(ctx)
	if err != nil {
		return time.Time{}, err
	}
	var packageID int64
	if err = t.QueryRow(ctx, `SELECT package_id FROM segment_audience_refresh_runs WHERE id=$1`, runID).Scan(&packageID); err != nil {
		return time.Time{}, err
	}
	var previous *int64
	var configuration int64
	if err = t.QueryRow(ctx, `SELECT published_snapshot_id,current_configuration_version_id FROM segment_audience_packages WHERE id=$1 FOR UPDATE`, packageID).Scan(&previous, &configuration); err != nil {
		return time.Time{}, err
	}
	var reference time.Time
	var kind segmentdomain.RefreshKind
	var state segmentdomain.RefreshState
	var runConfiguration int64
	if err = t.QueryRow(ctx, `SELECT reference_time,refresh_kind,state,configuration_version_id FROM segment_audience_refresh_runs WHERE id=$1 FOR UPDATE`, runID).Scan(&reference, &kind, &state, &runConfiguration); err != nil {
		return time.Time{}, err
	}
	if runConfiguration != configuration || (state != segmentdomain.RefreshEvaluating && state != segmentdomain.RefreshStaging) {
		return time.Time{}, ErrConflict
	}
	if kind == segmentdomain.RefreshSourceRebase {
		return reference, nil
	}
	if previous == nil {
		return time.Time{}, nil
	}
	var oldUnified bool
	if err = t.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM segment_audience_snapshots s CROSS JOIN LATERAL jsonb_array_elements(s.source_watermarks) w WHERE s.id=$1 AND w->>'source'='wecom.directory.published.v2')`, *previous).Scan(&oldUnified); err != nil {
		return time.Time{}, err
	}
	if oldUnified {
		return time.Time{}, nil
	}
	if _, err = t.Exec(ctx, `UPDATE segment_audience_refresh_runs SET reference_time=$2,refresh_kind='source_rebase',updated_at=$2 WHERE id=$1 AND state IN ('evaluating','staging')`, runID, now.UTC()); err != nil {
		return time.Time{}, err
	}
	// BeginRefresh has already allocated the preparing snapshot. Its reference
	// must move with the run before any calibration members are staged.
	if _, err = t.Exec(ctx, `UPDATE segment_audience_snapshots SET reference_time=$2 WHERE refresh_run_id=$1 AND state='preparing'`, runID, now.UTC()); err != nil {
		return time.Time{}, err
	}
	payload, err := json.Marshal(map[string]any{"previous_reference_time": reference, "calibration_reference_time": now.UTC(), "reason": "unified_directory_initial_calibration"})
	if err != nil {
		return time.Time{}, err
	}
	_, err = r.AppendMutationFacts(ctx, MutationFact{ResourceKind: "refresh_run", ResourceID: runID, Operation: "calibrate_source", EventType: "audience.source_calibration.reserved.v1", ActorID: actor.StaffID, ActorKind: actor.Kind, ActorRef: actor.Reference, Payload: payload, IdempotencyKey: "source-calibration-reference:" + strconv.FormatInt(runID, 10), OccurredAt: now.UTC()})
	return now.UTC(), err
}

// PrepareRefreshSources holds the same package lock as publication. Only an
// existing snapshot created with the former source is calibrated; new packages
// retain normal initial-member semantics. Replayed calibration stays quiet.
func (r *Repository) PrepareRefreshSources(ctx context.Context, runID int64, watermarks []segmentport.SourceWatermark) (bool, error) {
	t, err := tx(ctx)
	if err != nil {
		return false, err
	}
	var packageID int64
	var kind segmentdomain.RefreshKind
	if err = t.QueryRow(ctx, `SELECT package_id,refresh_kind FROM segment_audience_refresh_runs WHERE id=$1`, runID).Scan(&packageID, &kind); err != nil {
		return false, err
	}
	var previous *int64
	if err = t.QueryRow(ctx, `SELECT published_snapshot_id FROM segment_audience_packages WHERE id=$1 FOR UPDATE`, packageID).Scan(&previous); err != nil {
		return false, err
	}
	unified := false
	for _, w := range watermarks {
		unified = unified || w.Source == "wecom.directory.published.v2"
	}
	if kind == segmentdomain.RefreshSourceRebase && !unified {
		return false, ErrConflict
	}
	rebase := kind == segmentdomain.RefreshSourceRebase
	if unified && previous != nil {
		var old bool
		if err = t.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM segment_audience_snapshots s CROSS JOIN LATERAL jsonb_array_elements(s.source_watermarks) w WHERE s.id=$1 AND w->>'source'='wecom.directory.published.v2')`, *previous).Scan(&old); err != nil {
			return false, err
		}
		rebase = rebase || !old
	}
	raw, err := json.Marshal(watermarks)
	if err != nil {
		return false, err
	}
	_, err = t.Exec(ctx, `UPDATE segment_audience_refresh_runs SET source_watermarks=$2,refresh_kind=CASE WHEN $3 THEN 'source_rebase' ELSE refresh_kind END WHERE id=$1 AND state IN ('evaluating','staging')`, runID, raw, rebase)
	return rebase, err
}

// Calibrate existing packages once even when paused/manual. Later directory
// changes refresh only active automatic packages. Core assignments keep their
// independent facts and are never recalibrated through the WeCom source.
func (r *Repository) DirectoryRefreshConfigurations(ctx context.Context, limit int) ([]segmentdomain.ScheduledConfiguration, error) {
	if limit < 1 || limit > 10000 {
		return nil, ErrInvalid
	}
	t, err := tx(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := t.Query(ctx, `SELECT p.id,c.id,c.created_by,c.created_actor_kind,c.created_actor_ref,c.created_at
 FROM segment_audience_packages p JOIN segment_audience_configuration_versions c ON c.id=p.current_configuration_version_id AND c.package_id=p.id
 LEFT JOIN segment_audience_snapshots s ON s.id=p.published_snapshot_id
 WHERE p.lifecycle<>'archived' AND c.definition->>'template_key' IN ('hxc_registration','wecom_contact_registration','questionnaire_submissions','questionnaire_choice_answers','paid_order','channel_entry','radar_first_click_elapsed','member_usage_status','member_excluding_group_paid')
 AND ((p.lifecycle='active' AND c.refresh_mode<>'manual') OR (s.id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM jsonb_array_elements(s.source_watermarks) w WHERE w->>'source'='wecom.directory.published.v2')))
 ORDER BY p.id LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []segmentdomain.ScheduledConfiguration{}
	for rows.Next() {
		var c segmentdomain.ScheduledConfiguration
		var actor *int64
		if err = rows.Scan(&c.PackageID, &c.ConfigurationVersionID, &actor, &c.ActorKind, &c.ActorReference, &c.ConfigurationCreatedAt); err != nil {
			return nil, err
		}
		if actor != nil {
			c.Actor = *actor
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
