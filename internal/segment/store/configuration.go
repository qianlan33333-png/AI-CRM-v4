package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	segmentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/domain"
)

const configurationColumns = `id,package_id,version,schema_version,definition,COALESCE(refresh_cron_utc,''),refresh_mode,digest,created_by,created_actor_kind,created_actor_ref,created_at`

func scanConfiguration(row pgx.Row) (segmentdomain.ConfigurationVersion, error) {
	var item segmentdomain.ConfigurationVersion
	var digest []byte
	var refreshCronUTC *string
	var createdBy *int64
	err := row.Scan(&item.ID, &item.PackageID, &item.Version, &item.SchemaVersion, &item.Definition, &refreshCronUTC, &item.RefreshMode, &digest, &createdBy, &item.CreatedActorKind, &item.CreatedActorRef, &item.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return segmentdomain.ConfigurationVersion{}, ErrNotFound
	}
	if err != nil {
		return segmentdomain.ConfigurationVersion{}, err
	}
	if createdBy != nil {
		item.CreatedBy = *createdBy
	}
	if len(digest) != sha256.Size {
		return segmentdomain.ConfigurationVersion{}, ErrConflict
	}
	if refreshCronUTC != nil {
		item.RefreshCronUTC = *refreshCronUTC
	}
	copy(item.Digest[:], digest)
	return item, nil
}

func (r *Repository) CreateConfigurationVersion(ctx context.Context, item segmentdomain.ConfigurationVersion) (segmentdomain.ConfigurationVersion, error) {
	t, err := tx(ctx)
	if err != nil {
		return segmentdomain.ConfigurationVersion{}, err
	}
	actor, actorErr := storedActor(item.CreatedActorKind, item.CreatedActorRef, item.CreatedBy)
	if actorErr != nil {
		return segmentdomain.ConfigurationVersion{}, ErrInvalid
	}
	item.CreatedBy, item.CreatedActorKind, item.CreatedActorRef = actor.StaffID, actor.Kind, actor.Reference
	query := `INSERT INTO segment_audience_configuration_versions(package_id,version,schema_version,definition,refresh_cron_utc,refresh_mode,digest,created_by,created_actor_kind,created_actor_ref,created_at)
		VALUES($1,$2,$3,$4::jsonb,NULLIF($5,''),$6,$7,NULLIF($8,0),$9,$10,$11) RETURNING ` + configurationColumns
	created, err := scanConfiguration(t.QueryRow(ctx, query, item.PackageID, item.Version, item.SchemaVersion, item.Definition, item.RefreshCronUTC, item.RefreshMode, item.Digest[:], item.CreatedBy, item.CreatedActorKind, item.CreatedActorRef, item.CreatedAt))
	if unique(err) {
		return segmentdomain.ConfigurationVersion{}, ErrConflict
	}
	return created, err
}

func (r *Repository) SetCurrentConfiguration(ctx context.Context, packageID, configurationID, expectedPackageVersion, actor int64, now time.Time) (segmentdomain.Package, error) {
	return r.SetCurrentConfigurationWithActor(ctx, packageID, configurationID, expectedPackageVersion, adminActor(actor), now)
}

func (r *Repository) SetCurrentConfigurationWithActor(ctx context.Context, packageID, configurationID, expectedPackageVersion int64, actor Actor, now time.Time) (segmentdomain.Package, error) {
	if !actor.Valid() {
		return segmentdomain.Package{}, ErrInvalid
	}
	t, err := tx(ctx)
	if err != nil {
		return segmentdomain.Package{}, err
	}
	query := `UPDATE segment_audience_packages SET current_configuration_version_id=$2,version=version+1,updated_by=NULLIF($4,0),updated_actor_kind=$5,updated_actor_ref=$6,updated_at=$7
		WHERE id=$1 AND version=$3 AND lifecycle='paused' RETURNING ` + packageColumns
	updated, err := scanPackage(t.QueryRow(ctx, query, packageID, configurationID, expectedPackageVersion, actor.StaffID, actor.Kind, actor.Reference, now))
	if errors.Is(err, ErrNotFound) {
		return segmentdomain.Package{}, ErrConflict
	}
	return updated, err
}
