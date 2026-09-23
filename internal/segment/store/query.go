package store

import (
	"context"
	"fmt"

	segmentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/domain"
)

func (r *Repository) ListGroups(ctx context.Context) ([]segmentdomain.Group, error) {
	t, err := tx(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := t.Query(ctx, `SELECT `+groupColumns+` FROM segment_audience_groups ORDER BY sort_order,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []segmentdomain.Group{}
	for rows.Next() {
		item, scanErr := scanGroup(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *Repository) ListPackages(ctx context.Context, limit, offset int, includeArchived bool) ([]segmentdomain.Package, error) {
	t, err := tx(ctx)
	if err != nil {
		return nil, err
	}
	query := `SELECT ` + packageColumns + ` FROM segment_audience_packages`
	if !includeArchived {
		query += ` WHERE archived_at IS NULL`
	}
	query += ` ORDER BY updated_at DESC,id DESC LIMIT $1 OFFSET $2`
	rows, err := t.Query(ctx, query, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []segmentdomain.Package{}
	for rows.Next() {
		item, scanErr := scanPackage(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *Repository) CountPackages(ctx context.Context, includeArchived bool) (int64, error) {
	t, err := tx(ctx)
	if err != nil {
		return 0, err
	}
	query := `SELECT count(*) FROM segment_audience_packages`
	if !includeArchived {
		query += ` WHERE archived_at IS NULL`
	}
	var count int64
	err = t.QueryRow(ctx, query).Scan(&count)
	return count, err
}

func (r *Repository) CurrentConfiguration(ctx context.Context, packageID int64) (segmentdomain.ConfigurationVersion, error) {
	t, err := tx(ctx)
	if err != nil {
		return segmentdomain.ConfigurationVersion{}, err
	}
	query := `SELECT c.id,c.package_id,c.version,c.schema_version,c.definition,COALESCE(c.refresh_cron_utc,''),c.refresh_mode,c.digest,c.created_by,c.created_actor_kind,c.created_actor_ref,c.created_at
		FROM segment_audience_packages p JOIN segment_audience_configuration_versions c
		ON c.id=p.current_configuration_version_id AND c.package_id=p.id WHERE p.id=$1`
	return scanConfiguration(t.QueryRow(ctx, query, packageID))
}

func (r *Repository) Configuration(ctx context.Context, id int64) (segmentdomain.ConfigurationVersion, error) {
	t, err := tx(ctx)
	if err != nil {
		return segmentdomain.ConfigurationVersion{}, err
	}
	return scanConfiguration(t.QueryRow(ctx, `SELECT `+configurationColumns+` FROM segment_audience_configuration_versions WHERE id=$1`, id))
}

func (r *Repository) NextConfigurationVersion(ctx context.Context, packageID int64) (int64, error) {
	t, err := tx(ctx)
	if err != nil {
		return 0, err
	}
	var next int64
	err = t.QueryRow(ctx, `SELECT COALESCE(max(version),0)+1 FROM segment_audience_configuration_versions WHERE package_id=$1`, packageID).Scan(&next)
	return next, err
}

func (r *Repository) NextCopyCode(ctx context.Context, base string) (string, error) {
	t, err := tx(ctx)
	if err != nil {
		return "", err
	}
	for suffix := 1; suffix <= 9999; suffix++ {
		candidate := fmt.Sprintf("%s-copy-%d", base, suffix)
		if len(candidate) > 120 {
			candidate = fmt.Sprintf("copy-%d", suffix)
		}
		var exists bool
		if err = t.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM segment_audience_packages WHERE code=$1)`, candidate).Scan(&exists); err != nil {
			return "", err
		}
		if !exists {
			return candidate, nil
		}
	}
	return "", ErrConflict
}
