package migration

import (
	"context"
	"crypto/sha256"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgreSQLRuns struct{ Pool *pgxpool.Pool }

var ErrRunConflict = errors.New("commerce migration run conflicts with persisted manifest")

func (store PostgreSQLRuns) Begin(ctx context.Context, runKey string, digest [32]byte, input int64) error {
	schema := sha256.Sum256([]byte(SchemaVersion))
	result, err := store.Pool.Exec(ctx, `INSERT INTO order_import_runs(run_key,source_manifest_digest,source_schema_digest,status,input_count) VALUES($1,$2,$3,'applying',$4) ON CONFLICT(run_key) DO UPDATE SET status='applying',input_count=EXCLUDED.input_count,completed_at=NULL WHERE order_import_runs.source_manifest_digest=EXCLUDED.source_manifest_digest AND order_import_runs.source_schema_digest=EXCLUDED.source_schema_digest`, runKey, digest[:], schema[:], input)
	if err == nil && result.RowsAffected() != 1 {
		return ErrRunConflict
	}
	return err
}

func (store PostgreSQLRuns) Complete(ctx context.Context, runKey string, imported int64) error {
	result, err := store.Pool.Exec(ctx, `UPDATE order_import_runs SET status='applied',imported_count=$2,completed_at=clock_timestamp() WHERE run_key=$1 AND status='applying'`, runKey, imported)
	if err == nil && result.RowsAffected() != 1 {
		return ErrRunConflict
	}
	return err
}

func (store PostgreSQLRuns) CompleteOrders(ctx context.Context, runKey string, processed int64) error {
	result, err := store.Pool.Exec(ctx, `UPDATE order_import_runs run SET status='applied',imported_count=(SELECT count(*) FROM order_import_receipts WHERE run_id=run.id AND outcome='imported'),replayed_count=(SELECT count(*) FROM order_import_receipts WHERE run_id=run.id AND outcome='replayed'),completed_at=clock_timestamp() WHERE run_key=$1 AND status='applying' AND (SELECT count(*) FROM order_import_receipts WHERE run_id=run.id AND outcome IN ('imported','replayed'))=$2`, runKey, processed)
	if err == nil && result.RowsAffected() != 1 {
		return ErrRunConflict
	}
	return err
}

func (store PostgreSQLRuns) ReconcileOrders(ctx context.Context, manifest Manifest) (OrderOnlyReconciliation, error) {
	result, err := store.VerifyOrderOnly(ctx, manifest)
	if err != nil {
		return result, err
	}
	if err = store.MarkReconciled(ctx, manifest); err != nil {
		return result, err
	}
	return result, nil
}

// MarkReconciled changes only the Order-owned run ledger after every Owner's
// read-only verifier has accepted the approved snapshot. The composition root
// calls this method rather than writing order_import_runs itself.
func (store PostgreSQLRuns) MarkReconciled(ctx context.Context, manifest Manifest) error {
	if store.Pool == nil {
		return errors.New("order migration store is not configured")
	}
	command, err := store.Pool.Exec(ctx, `UPDATE order_import_runs SET status='reconciled',completed_at=clock_timestamp() WHERE run_key=$1 AND source_manifest_digest=$2 AND status IN ('applied','reconciled')`, manifest.RunKey, manifest.Digest[:])
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrRunConflict
	}
	return nil
}
