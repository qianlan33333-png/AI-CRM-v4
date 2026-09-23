package store

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	configport "github.com/qianlan33333-png/AI-CRM-v3/internal/config/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func (r *Repository) ActiveRuntimeRevision(ctx context.Context, lock bool) (int64, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return 0, err
	}
	query := `SELECT COALESCE(release_id,0) FROM config_runtime_active_release WHERE singleton=TRUE`
	if lock {
		query += ` FOR UPDATE`
	}
	var out int64
	if err = tx.QueryRow(ctx, query).Scan(&out); errors.Is(err, pgx.ErrNoRows) {
		return 0, configport.ErrRuntimeReleaseConflict
	}
	return out, err
}

// ActiveRuntimeRelease reads the singleton pointer and the selected immutable
// release in one SQL statement. It avoids a READ COMMITTED gap where a later
// lookup sees the just-superseded state after the pointer was read.
func (r *Repository) ActiveRuntimeRelease(ctx context.Context) (configport.RuntimeRelease, bool, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return configport.RuntimeRelease{}, false, err
	}
	row := tx.QueryRow(ctx, `SELECT r.id,r.state,r.base_revision,r.rollback_of_release_id,encode(r.checksum,'hex'),r.validation_errors,r.created_by,r.created_at,r.validated_at,COALESCE(r.published_by,''),r.published_at
		FROM config_runtime_active_release active
		LEFT JOIN config_runtime_releases r ON r.id=active.release_id
		WHERE active.singleton=TRUE`)
	var id *int64
	var state *string
	var base *int64
	var rollback *int64
	var checksum *string
	var validation []byte
	var createdBy *string
	var createdAt *time.Time
	var validatedAt *time.Time
	var publishedBy *string
	var publishedAt *time.Time
	if err = row.Scan(&id, &state, &base, &rollback, &checksum, &validation, &createdBy, &createdAt, &validatedAt, &publishedBy, &publishedAt); err != nil {
		return configport.RuntimeRelease{}, false, err
	}
	if id == nil {
		return configport.RuntimeRelease{}, false, nil
	}
	release := configport.RuntimeRelease{ID: *id, State: configport.RuntimeReleaseState(*state), BaseRevision: *base, RollbackOfReleaseID: rollback, Checksum: *checksum, ValidationErrors: []configport.RuntimeValidationIssue{}, CreatedBy: *createdBy, CreatedAt: createdAt.UTC(), ValidatedAt: validatedAt, PublishedBy: *publishedBy, PublishedAt: publishedAt}
	if err = json.Unmarshal(validation, &release.ValidationErrors); err != nil {
		return configport.RuntimeRelease{}, false, err
	}
	release.Settings, err = runtimeSettings(ctx, tx, release.ID)
	return release, err == nil, err
}

func (r *Repository) GetRuntimeRelease(ctx context.Context, id int64, lock bool) (configport.RuntimeRelease, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return configport.RuntimeRelease{}, err
	}
	query := `SELECT id,state,base_revision,rollback_of_release_id,encode(checksum,'hex'),validation_errors,created_by,created_at,validated_at,COALESCE(published_by,''),published_at FROM config_runtime_releases WHERE id=$1`
	if lock {
		query += ` FOR UPDATE`
	}
	out, err := scanRuntimeRelease(tx.QueryRow(ctx, query, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return configport.RuntimeRelease{}, configport.ErrRuntimeReleaseNotFound
	}
	if err != nil {
		return configport.RuntimeRelease{}, err
	}
	out.Settings, err = runtimeSettings(ctx, tx, out.ID)
	return out, err
}

func (r *Repository) ListRuntimeReleases(ctx context.Context, limit int) ([]configport.RuntimeRelease, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT id,state,base_revision,rollback_of_release_id,encode(checksum,'hex'),validation_errors,created_by,created_at,validated_at,COALESCE(published_by,''),published_at FROM config_runtime_releases ORDER BY id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	out := []configport.RuntimeRelease{}
	for rows.Next() {
		release, scanErr := scanRuntimeRelease(rows)
		if scanErr != nil {
			rows.Close()
			return nil, scanErr
		}
		out = append(out, release)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	return attachRuntimeSettings(ctx, tx, out)
}

// attachRuntimeSettings reads the values only after the release result set is
// closed. pgx permits one active query per transaction connection.
func attachRuntimeSettings(ctx context.Context, tx pgx.Tx, releases []configport.RuntimeRelease) ([]configport.RuntimeRelease, error) {
	if len(releases) == 0 {
		return releases, nil
	}
	ids := make([]int64, 0, len(releases))
	byID := make(map[int64]int, len(releases))
	for i := range releases {
		ids = append(ids, releases[i].ID)
		byID[releases[i].ID] = i
		releases[i].Settings = []configport.RuntimeSetting{}
	}
	rows, err := tx.Query(ctx, `SELECT release_id,setting_key,value FROM config_runtime_release_values WHERE release_id=ANY($1::bigint[]) ORDER BY release_id,setting_key`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var releaseID int64
		var key configport.RuntimeSettingKey
		var value json.RawMessage
		if err = rows.Scan(&releaseID, &key, &value); err != nil {
			return nil, err
		}
		i, found := byID[releaseID]
		if !found {
			return nil, configport.ErrRuntimeReleaseConflict
		}
		releases[i].Settings = append(releases[i].Settings, configport.RuntimeSetting{Key: key, Value: append(json.RawMessage(nil), value...)})
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	for _, release := range releases {
		if len(release.Settings) == 0 {
			return nil, configport.ErrRuntimeReleaseConflict
		}
	}
	return releases, nil
}

func (r *Repository) InsertRuntimeRelease(ctx context.Context, release configport.RuntimeRelease) (configport.RuntimeRelease, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return release, err
	}
	checksum, err := hex.DecodeString(release.Checksum)
	if err != nil || len(checksum) != 32 {
		return release, configport.ErrRuntimeReleaseInvalid
	}
	issues := []byte("[]")
	if len(release.ValidationErrors) != 0 {
		issues, err = json.Marshal(release.ValidationErrors)
		if err != nil {
			return release, configport.ErrRuntimeReleaseInvalid
		}
	}
	var rollback any
	if release.RollbackOfReleaseID != nil {
		rollback = *release.RollbackOfReleaseID
	}
	var validated any
	if release.ValidatedAt != nil {
		validated = release.ValidatedAt.UTC()
	}
	row := tx.QueryRow(ctx, `INSERT INTO config_runtime_releases(state,base_revision,rollback_of_release_id,checksum,validation_errors,created_by,created_at,validated_at) VALUES($1,$2,$3,$4,$5::jsonb,$6,$7,$8) RETURNING id,state,base_revision,rollback_of_release_id,encode(checksum,'hex'),validation_errors,created_by,created_at,validated_at,COALESCE(published_by,''),published_at`, release.State, release.BaseRevision, rollback, checksum, issues, release.CreatedBy, release.CreatedAt.UTC(), validated)
	out, err := scanRuntimeRelease(row)
	if err != nil {
		return release, err
	}
	for _, setting := range release.Settings {
		if _, err = tx.Exec(ctx, `INSERT INTO config_runtime_release_values(release_id,setting_key,value) VALUES($1,$2,$3::jsonb)`, out.ID, setting.Key, setting.Value); err != nil {
			return release, err
		}
	}
	out.Settings = append([]configport.RuntimeSetting(nil), release.Settings...)
	return out, nil
}

func (r *Repository) SetRuntimeReleaseValidation(ctx context.Context, id int64, state configport.RuntimeReleaseState, issues []configport.RuntimeValidationIssue, at time.Time) (configport.RuntimeRelease, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return configport.RuntimeRelease{}, err
	}
	encoded, err := json.Marshal(issues)
	if err != nil {
		return configport.RuntimeRelease{}, configport.ErrRuntimeReleaseInvalid
	}
	out, err := scanRuntimeRelease(tx.QueryRow(ctx, `UPDATE config_runtime_releases SET state=$2,validation_errors=$3::jsonb,validated_at=$4 WHERE id=$1 AND state IN ('draft','validated','validation_failed') RETURNING id,state,base_revision,rollback_of_release_id,encode(checksum,'hex'),validation_errors,created_by,created_at,validated_at,COALESCE(published_by,''),published_at`, id, state, encoded, at.UTC()))
	if errors.Is(err, pgx.ErrNoRows) {
		return configport.RuntimeRelease{}, configport.ErrRuntimeReleaseConflict
	}
	if err != nil {
		return configport.RuntimeRelease{}, err
	}
	out.Settings, err = runtimeSettings(ctx, tx, out.ID)
	return out, err
}

func (r *Repository) PublishRuntimeRelease(ctx context.Context, id, active int64, actor string, at time.Time) (configport.RuntimeRelease, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return configport.RuntimeRelease{}, err
	}
	if active > 0 {
		result, e := tx.Exec(ctx, `UPDATE config_runtime_releases SET state='superseded' WHERE id=$1 AND state='published'`, active)
		if e != nil {
			return configport.RuntimeRelease{}, e
		}
		if result.RowsAffected() != 1 {
			return configport.RuntimeRelease{}, configport.ErrRuntimeReleaseConflict
		}
	}
	out, err := scanRuntimeRelease(tx.QueryRow(ctx, `UPDATE config_runtime_releases SET state='published',published_by=$2,published_at=$3 WHERE id=$1 AND state='validated' RETURNING id,state,base_revision,rollback_of_release_id,encode(checksum,'hex'),validation_errors,created_by,created_at,validated_at,COALESCE(published_by,''),published_at`, id, actor, at.UTC()))
	if errors.Is(err, pgx.ErrNoRows) {
		return configport.RuntimeRelease{}, configport.ErrRuntimeReleaseConflict
	}
	if err != nil {
		return configport.RuntimeRelease{}, err
	}
	out.Settings, err = runtimeSettings(ctx, tx, out.ID)
	return out, err
}

func (r *Repository) SetActiveRuntimeRelease(ctx context.Context, id int64, at time.Time) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	result, err := tx.Exec(ctx, `UPDATE config_runtime_active_release SET release_id=$1,updated_at=$2 WHERE singleton=TRUE`, id, at.UTC())
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return configport.ErrRuntimeReleaseConflict
	}
	return nil
}

func (r *Repository) AppendRuntimeReleaseAudit(ctx context.Context, releaseID int64, action, actor, requestID string, at time.Time) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO config_runtime_release_audits(release_id,action,actor,request_id,created_at) VALUES($1,$2,$3,$4,$5)`, releaseID, action, actor, requestID, at.UTC())
	return err
}

func (r *Repository) ReserveRuntimeReleaseCommand(ctx context.Context, action, actor, key string, digest []byte, at time.Time) (configport.RuntimeReleaseReceipt, bool, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return configport.RuntimeReleaseReceipt{}, false, err
	}
	var out configport.RuntimeReleaseReceipt
	err = tx.QueryRow(ctx, `INSERT INTO config_runtime_release_command_receipts(action,actor,idempotency_key,payload_digest,state,created_at) VALUES($1,$2,$3,$4,'reserved',$5) ON CONFLICT(action,actor,idempotency_key) DO NOTHING RETURNING id,payload_digest,COALESCE(release_id,0),state`, action, actor, key, digest, at.UTC()).Scan(&out.ID, &out.PayloadDigest, &out.ReleaseID, &out.State)
	if err == nil {
		return out, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return configport.RuntimeReleaseReceipt{}, false, err
	}
	err = tx.QueryRow(ctx, `SELECT id,payload_digest,COALESCE(release_id,0),state FROM config_runtime_release_command_receipts WHERE action=$1 AND actor=$2 AND idempotency_key=$3`, action, actor, key).Scan(&out.ID, &out.PayloadDigest, &out.ReleaseID, &out.State)
	return out, false, err
}

func (r *Repository) CompleteRuntimeReleaseCommand(ctx context.Context, id, releaseID int64, at time.Time) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	result, err := tx.Exec(ctx, `UPDATE config_runtime_release_command_receipts SET state='completed',release_id=$2,completed_at=$3 WHERE id=$1 AND state='reserved'`, id, releaseID, at.UTC())
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return configport.ErrRuntimeReleaseConflict
	}
	return nil
}

func (r *Repository) InsertRuntimeUsage(ctx context.Context, use configport.RuntimeUsage) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO config_runtime_usage(revision,source,consumer,role,operation,subject_kind,subject_id,used_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(revision,source,consumer,role,operation,subject_kind,subject_id) DO NOTHING`, use.Snapshot.Revision, use.Snapshot.Source, use.Consumer, use.Role, use.Operation, use.SubjectKind, use.SubjectID, use.UsedAt.UTC())
	return err
}

func (r *Repository) ListRuntimeUsage(ctx context.Context, revision int64, limit int) ([]configport.RuntimeUsage, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT revision,source,consumer,role,operation,subject_kind,subject_id,used_at FROM config_runtime_usage WHERE used_at>=statement_timestamp()-interval '720 hours' AND ($1=0 OR revision=$1) ORDER BY used_at DESC,id DESC LIMIT $2`, revision, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []configport.RuntimeUsage{}
	for rows.Next() {
		var use configport.RuntimeUsage
		if err = rows.Scan(&use.Snapshot.Revision, &use.Snapshot.Source, &use.Consumer, &use.Role, &use.Operation, &use.SubjectKind, &use.SubjectID, &use.UsedAt); err != nil {
			return nil, err
		}
		out = append(out, use)
	}
	return out, rows.Err()
}

func (r *Repository) InsertRuntimeApplication(ctx context.Context, application configport.RuntimeApplication) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO config_runtime_applications(revision,source,role,release_sha,snapshot_checksum,applied_at)
VALUES($1,$2,$3,$4,$5,$6)
ON CONFLICT(revision,source,role,release_sha,snapshot_checksum) DO NOTHING`, application.Revision, application.Source, application.Role, application.ReleaseSHA, application.SnapshotChecksum, application.AppliedAt.UTC())
	return err
}

func (r *Repository) ListRuntimeApplications(ctx context.Context, limit int) ([]configport.RuntimeApplication, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT revision,source,role,release_sha,snapshot_checksum,applied_at
FROM config_runtime_applications ORDER BY applied_at DESC,revision DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []configport.RuntimeApplication{}
	for rows.Next() {
		var item configport.RuntimeApplication
		if err = rows.Scan(&item.Revision, &item.Source, &item.Role, &item.ReleaseSHA, &item.SnapshotChecksum, &item.AppliedAt); err != nil {
			return nil, err
		}
		item.AppliedAt = item.AppliedAt.UTC()
		items = append(items, item)
	}
	return items, rows.Err()
}

func scanRuntimeRelease(row pgx.Row) (configport.RuntimeRelease, error) {
	var out configport.RuntimeRelease
	var rollback *int64
	var validation []byte
	var validated, published *time.Time
	var state string
	if err := row.Scan(&out.ID, &state, &out.BaseRevision, &rollback, &out.Checksum, &validation, &out.CreatedBy, &out.CreatedAt, &validated, &out.PublishedBy, &published); err != nil {
		return out, err
	}
	out.State, out.RollbackOfReleaseID, out.ValidatedAt, out.PublishedAt = configport.RuntimeReleaseState(state), rollback, validated, published
	if err := json.Unmarshal(validation, &out.ValidationErrors); err != nil {
		return configport.RuntimeRelease{}, err
	}
	return out, nil
}
func runtimeSettings(ctx context.Context, tx pgx.Tx, releaseID int64) ([]configport.RuntimeSetting, error) {
	rows, err := tx.Query(ctx, `SELECT setting_key,value FROM config_runtime_release_values WHERE release_id=$1 ORDER BY setting_key`, releaseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []configport.RuntimeSetting{}
	for rows.Next() {
		var x configport.RuntimeSetting
		if err = rows.Scan(&x.Key, &x.Value); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

var _ interface {
	ActiveRuntimeRevision(context.Context, bool) (int64, error)
	GetRuntimeRelease(context.Context, int64, bool) (configport.RuntimeRelease, error)
	ListRuntimeReleases(context.Context, int) ([]configport.RuntimeRelease, error)
	InsertRuntimeRelease(context.Context, configport.RuntimeRelease) (configport.RuntimeRelease, error)
	SetRuntimeReleaseValidation(context.Context, int64, configport.RuntimeReleaseState, []configport.RuntimeValidationIssue, time.Time) (configport.RuntimeRelease, error)
	PublishRuntimeRelease(context.Context, int64, int64, string, time.Time) (configport.RuntimeRelease, error)
	SetActiveRuntimeRelease(context.Context, int64, time.Time) error
	AppendRuntimeReleaseAudit(context.Context, int64, string, string, string, time.Time) error
	CompleteRuntimeReleaseCommand(context.Context, int64, int64, time.Time) error
	InsertRuntimeUsage(context.Context, configport.RuntimeUsage) error
	ListRuntimeUsage(context.Context, int64, int) ([]configport.RuntimeUsage, error)
	InsertRuntimeApplication(context.Context, configport.RuntimeApplication) error
	ListRuntimeApplications(context.Context, int) ([]configport.RuntimeApplication, error)
} = (*Repository)(nil)
