package store

import (
	"context"
	"encoding/json"
	segmentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/domain"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
)

// PrepareRefreshSources holds the same package lock as publication. Only an
// existing snapshot created with the former source is calibrated; new packages
// retain normal initial-member semantics. Replayed calibration stays quiet.
func (r *Repository) PrepareRefreshSources(ctx context.Context, runID int64, watermarks []segmentport.SourceWatermark) (bool, error) {
	t, err := tx(ctx)
	if err != nil {
		return false, err
	}
	var packageID int64
	if err = t.QueryRow(ctx, `SELECT package_id FROM segment_audience_refresh_runs WHERE id=$1`, runID).Scan(&packageID); err != nil {
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
	rebase := false
	if unified && previous != nil {
		var old bool
		if err = t.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM segment_audience_snapshots s CROSS JOIN LATERAL jsonb_array_elements(s.source_watermarks) w WHERE s.id=$1 AND w->>'source'='wecom.directory.published.v2')`, *previous).Scan(&old); err != nil {
			return false, err
		}
		rebase = !old
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
