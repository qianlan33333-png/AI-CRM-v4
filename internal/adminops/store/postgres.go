// Package store owns the AdminOps historical projection tables.
package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	adminopsport "github.com/qianlan33333-png/AI-CRM-v3/internal/adminops/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

var ErrInvalid = errors.New("adminops projection store dependencies are required")

// ProjectionStore is the concrete PostgreSQL owner for the AdminOps
// projection tables.  The native pool is retained for composition/readiness
// validation; all business queries require the transaction supplied by UoW.
type ProjectionStore struct {
	pool *pgxpool.Pool
	uow  platformport.UnitOfWork
}

func NewProjectionPostgreSQL(pool *pgxpool.Pool, uow platformport.UnitOfWork) (*ProjectionStore, error) {
	if pool == nil || uow == nil {
		return nil, ErrInvalid
	}
	return &ProjectionStore{pool: pool, uow: uow}, nil
}

// Readiness verifies both tables owned by this store. It deliberately uses
// the native pool only for a schema probe; all business reads/writes below
// remain transaction-bound through UnitOfWork.
func (store *ProjectionStore) Readiness(ctx context.Context) error {
	if store == nil || store.pool == nil {
		return ErrInvalid
	}
	var ready bool
	if err := store.pool.QueryRow(ctx, `SELECT NOT EXISTS (
		SELECT 1 FROM unnest(ARRAY['adminops_release_projections','adminops_diagnostic_snapshots']) AS required(name)
		WHERE to_regclass(current_schema() || '.' || required.name) IS NULL
	)`).Scan(&ready); err != nil {
		return err
	}
	if !ready {
		return errors.New("adminops projection schema is not ready")
	}
	return nil
}

func (store *ProjectionStore) ListReleaseProjections(ctx context.Context) ([]adminopsport.ReleaseProjection, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT id,release_sha,status,observed_at
FROM adminops_release_projections
ORDER BY observed_at DESC,id DESC
LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]adminopsport.ReleaseProjection, 0, 100)
	for rows.Next() {
		var item adminopsport.ReleaseProjection
		if err = rows.Scan(&item.ID, &item.ReleaseSHA, &item.Status, &item.ObservedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (store *ProjectionStore) ListDiagnosticSnapshots(ctx context.Context) ([]adminopsport.DiagnosticSnapshot, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT id,diagnostic_key,status,observed_at
FROM adminops_diagnostic_snapshots
WHERE observed_at>=transaction_timestamp()-interval '30 days'
ORDER BY observed_at DESC,id DESC
LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]adminopsport.DiagnosticSnapshot, 0, 100)
	for rows.Next() {
		var item adminopsport.DiagnosticSnapshot
		if err = rows.Scan(&item.ID, &item.Key, &item.Status, &item.ObservedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (store *ProjectionStore) RecordReleaseProjection(ctx context.Context, item adminopsport.ReleaseProjection) (adminopsport.ReleaseProjection, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return adminopsport.ReleaseProjection{}, err
	}
	var result adminopsport.ReleaseProjection
	err = tx.QueryRow(ctx, `INSERT INTO adminops_release_projections(release_sha,status,observed_at,details)
VALUES($1,$2,COALESCE($3::timestamptz,clock_timestamp()),'{}'::jsonb)
RETURNING id,release_sha,status,observed_at`, item.ReleaseSHA, item.Status, nullableTime(item.ObservedAt)).Scan(&result.ID, &result.ReleaseSHA, &result.Status, &result.ObservedAt)
	return result, err
}

func (store *ProjectionStore) RecordDiagnosticSnapshot(ctx context.Context, item adminopsport.DiagnosticSnapshot) (adminopsport.DiagnosticSnapshot, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return adminopsport.DiagnosticSnapshot{}, err
	}
	var result adminopsport.DiagnosticSnapshot
	err = tx.QueryRow(ctx, `INSERT INTO adminops_diagnostic_snapshots(diagnostic_key,status,observed_at,details)
VALUES($1,$2,COALESCE($3::timestamptz,clock_timestamp()),'{}'::jsonb)
RETURNING id,diagnostic_key,status,observed_at`, item.Key, item.Status, nullableTime(item.ObservedAt)).Scan(&result.ID, &result.Key, &result.Status, &result.ObservedAt)
	return result, err
}

// pgx encodes a zero time as year one, which is valid but not useful as an
// observation timestamp.  NULL lets the table's clock_timestamp default be
// used without allowing caller-controlled arbitrary details.
func nullableTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value.UTC()
}

var _ adminopsport.ProjectionStore = (*ProjectionStore)(nil)
