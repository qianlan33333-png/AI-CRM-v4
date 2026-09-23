package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	segmentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/domain"
)

func (r *Repository) ScheduledConfigurations(ctx context.Context, limit int) ([]segmentdomain.ScheduledConfiguration, error) {
	if limit < 1 || limit > 10000 {
		return nil, ErrInvalid
	}
	database, err := tx(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := database.Query(ctx, `SELECT p.id,c.id,scheduled.cron_utc,scheduled.kind,c.created_by,c.created_actor_kind,c.created_actor_ref,c.created_at,s.next_due_at,COALESCE(s.version,0)
		FROM segment_audience_packages p
		JOIN segment_audience_configuration_versions c ON c.id=p.current_configuration_version_id AND c.package_id=p.id
		CROSS JOIN LATERAL (
			SELECT 'legacy'::text AS kind,c.refresh_cron_utc AS cron_utc WHERE c.refresh_mode='legacy_custom' AND c.refresh_cron_utc IS NOT NULL
			UNION ALL SELECT 'incremental','*/3 * * * *' WHERE c.refresh_mode IN ('every_3m','every_3m_plus_daily_0200')
			UNION ALL SELECT 'daily','0 18 * * *' WHERE c.refresh_mode IN ('daily_0200','every_3m_plus_daily_0200')
		) scheduled
		LEFT JOIN segment_audience_schedule_states s ON s.configuration_version_id=c.id AND s.schedule_kind=scheduled.kind
		WHERE p.lifecycle='active'
		ORDER BY p.id,scheduled.kind LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []segmentdomain.ScheduledConfiguration{}
	for rows.Next() {
		var item segmentdomain.ScheduledConfiguration
		var actorID *int64
		if err = rows.Scan(&item.PackageID, &item.ConfigurationVersionID, &item.CronUTC, &item.Kind, &actorID, &item.ActorKind, &item.ActorReference, &item.ConfigurationCreatedAt, &item.NextDueAt, &item.ScheduleVersion); err != nil {
			return nil, err
		}
		if actorID != nil {
			item.Actor = *actorID
		}
		if actor, actorErr := storedActor(item.ActorKind, item.ActorReference, item.Actor); actorErr != nil || actor.Reference != item.ActorReference {
			return nil, ErrInvalid
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// ClaimScheduledOccurrence advances one configuration's durable cursor. The
// caller accepts the refresh in the same UoW; any acceptance failure rolls the
// cursor back with it.
func (r *Repository) ClaimScheduledOccurrence(ctx context.Context, item segmentdomain.ScheduledConfiguration, occurrence, next, now time.Time) (bool, error) {
	actor, actorErr := storedActor(item.ActorKind, item.ActorReference, item.Actor)
	if item.PackageID < 1 || item.ConfigurationVersionID < 1 || actorErr != nil || (item.ActorReference != "" && actor.Reference != item.ActorReference) || item.CronUTC == "" || (item.Kind != "legacy" && item.Kind != "incremental" && item.Kind != "daily") || occurrence.IsZero() || next.IsZero() || !next.After(occurrence) || now.IsZero() {
		return false, ErrInvalid
	}
	database, err := tx(ctx)
	if err != nil {
		return false, err
	}
	var currentCron, currentMode string
	var lifecycle string
	if err = database.QueryRow(ctx, `SELECT COALESCE(c.refresh_cron_utc,''),c.refresh_mode,p.lifecycle FROM segment_audience_packages p JOIN segment_audience_configuration_versions c ON c.id=p.current_configuration_version_id AND c.package_id=p.id WHERE p.id=$1 AND c.id=$2 FOR UPDATE OF p`, item.PackageID, item.ConfigurationVersionID).Scan(&currentCron, &currentMode, &lifecycle); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if lifecycle != "active" || !currentScheduleMatches(currentMode, currentCron, item.Kind, item.CronUTC) {
		return false, nil
	}
	if item.NextDueAt == nil {
		command, err := database.Exec(ctx, `INSERT INTO segment_audience_schedule_states(configuration_version_id,package_id,schedule_kind,next_due_at,last_dispatched_at,version,updated_at) VALUES($1,$2,$3,$4,$5,1,$6) ON CONFLICT(configuration_version_id,schedule_kind) DO NOTHING`, item.ConfigurationVersionID, item.PackageID, item.Kind, next, occurrence, now)
		return err == nil && command.RowsAffected() == 1, err
	}
	command, err := database.Exec(ctx, `UPDATE segment_audience_schedule_states SET next_due_at=$4,last_dispatched_at=$5,version=version+1,updated_at=$6 WHERE configuration_version_id=$1 AND package_id=$2 AND schedule_kind=$3 AND next_due_at=$7 AND version=$8`, item.ConfigurationVersionID, item.PackageID, item.Kind, next, occurrence, now, item.NextDueAt.UTC(), item.ScheduleVersion)
	return err == nil && command.RowsAffected() == 1, err
}

func currentScheduleMatches(mode, legacyCron, kind, cron string) bool {
	switch kind {
	case "legacy":
		return mode == "legacy_custom" && legacyCron == cron
	case "incremental":
		return (mode == "every_3m" || mode == "every_3m_plus_daily_0200") && cron == "*/3 * * * *"
	case "daily":
		return (mode == "daily_0200" || mode == "every_3m_plus_daily_0200") && cron == "0 18 * * *"
	default:
		return false
	}
}
