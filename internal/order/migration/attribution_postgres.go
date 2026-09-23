package migration

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgreSQLAttributionRuns struct{ Pool *pgxpool.Pool }

func (store PostgreSQLAttributionRuns) BeginAttribution(ctx context.Context, manifest AttributionManifest, scope string) (int64, error) {
	if store.Pool == nil {
		return 0, errors.New("attribution run store is not configured")
	}
	schema := sha256.Sum256([]byte(AttributionSchemaVersion))
	var id int64
	err := store.Pool.QueryRow(ctx, `INSERT INTO order_history_attribution_runs(run_key,source_manifest_digest,source_schema_digest,identity_scope,snapshot_at,status,input_count)
VALUES($1,$2,$3,$4,$5,'applying',$6)
ON CONFLICT(run_key) DO UPDATE SET status='applying',completed_at=NULL
WHERE order_history_attribution_runs.source_manifest_digest=EXCLUDED.source_manifest_digest
  AND order_history_attribution_runs.source_schema_digest=EXCLUDED.source_schema_digest
  AND order_history_attribution_runs.identity_scope=EXCLUDED.identity_scope
  AND order_history_attribution_runs.snapshot_at=EXCLUDED.snapshot_at
RETURNING id`, manifest.RunKey, manifest.Digest[:], schema[:], scope, manifest.SnapshotAt.UTC(), len(manifest.Rows)).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrRunConflict
	}
	return id, err
}

func (store PostgreSQLAttributionRuns) CompleteAttribution(ctx context.Context, runID, replayed int64) (AttributionRunResult, error) {
	result, err := store.result(ctx, runID)
	if err != nil {
		return result, err
	}
	result.Replayed = replayed
	if result.Input != result.Linked+result.AlreadyLinked+result.Quarantined {
		return result, errors.New("order attribution silent row loss")
	}
	command, err := store.Pool.Exec(ctx, `UPDATE order_history_attribution_runs SET status='applied',linked_count=$2,already_linked_count=$3,quarantined_count=$4,replayed_count=$5,completed_at=clock_timestamp() WHERE id=$1 AND status='applying'`, runID, result.Linked, result.AlreadyLinked, result.Quarantined, replayed)
	if err != nil || command.RowsAffected() != 1 {
		if err != nil {
			return result, err
		}
		return result, ErrRunConflict
	}
	result.Matched = true
	return result, nil
}

func (store PostgreSQLAttributionRuns) FailAttribution(ctx context.Context, runID int64) error {
	if store.Pool == nil || runID < 1 {
		return errors.New("attribution run store is not configured")
	}
	_, err := store.Pool.Exec(ctx, `UPDATE order_history_attribution_runs SET status='failed',completed_at=clock_timestamp() WHERE id=$1 AND status='applying'`, runID)
	return err
}

func (store PostgreSQLAttributionRuns) ReconcileAttribution(ctx context.Context, manifest AttributionManifest, scope string) (AttributionRunResult, error) {
	if store.Pool == nil {
		return AttributionRunResult{}, errors.New("attribution run store is not configured")
	}
	schema := sha256.Sum256([]byte(AttributionSchemaVersion))
	var runID int64
	var digest, schemaDigest []byte
	err := store.Pool.QueryRow(ctx, `SELECT id,source_manifest_digest,source_schema_digest FROM order_history_attribution_runs WHERE run_key=$1 AND identity_scope=$2 AND status IN ('applied','reconciled')`, manifest.RunKey, scope).Scan(&runID, &digest, &schemaDigest)
	if err != nil {
		return AttributionRunResult{}, err
	}
	if len(digest) != sha256.Size || len(schemaDigest) != sha256.Size || subtle.ConstantTimeCompare(digest, manifest.Digest[:]) != 1 || subtle.ConstantTimeCompare(schemaDigest, schema[:]) != 1 {
		return AttributionRunResult{}, ErrRunConflict
	}
	result, err := store.result(ctx, runID)
	if err != nil {
		return result, err
	}
	if result.Input != int64(len(manifest.Rows)) || result.Input != result.Linked+result.AlreadyLinked+result.Quarantined || result.WrongBindings != 0 || result.EffectEligible != 0 {
		return result, errors.New("order attribution reconciliation mismatch")
	}
	command, err := store.Pool.Exec(ctx, `UPDATE order_history_attribution_runs SET status='reconciled',completed_at=COALESCE(completed_at,clock_timestamp()) WHERE id=$1 AND status IN ('applied','reconciled')`, runID)
	if err != nil || command.RowsAffected() != 1 {
		if err != nil {
			return result, err
		}
		return result, ErrRunConflict
	}
	result.Matched = true
	return result, nil
}

func (store PostgreSQLAttributionRuns) result(ctx context.Context, runID int64) (AttributionRunResult, error) {
	result := AttributionRunResult{RunID: runID}
	err := store.Pool.QueryRow(ctx, `SELECT run.input_count,
 count(receipt.id) FILTER(WHERE receipt.outcome='linked'),
 count(receipt.id) FILTER(WHERE receipt.outcome='already_linked'),
 count(receipt.id) FILTER(WHERE receipt.outcome NOT IN ('linked','already_linked')),
 count(receipt.id) FILTER(WHERE receipt.outcome IN ('linked','already_linked') AND (orders.id IS NULL OR orders.record_origin<>'history' OR orders.payer_customer_id IS DISTINCT FROM receipt.payer_customer_id)),
 count(receipt.id) FILTER(WHERE receipt.outcome IN ('linked','already_linked') AND orders.effect_eligible)
FROM order_history_attribution_runs run
LEFT JOIN order_history_attribution_receipts receipt ON receipt.run_id=run.id
LEFT JOIN orders ON orders.id=receipt.order_id
WHERE run.id=$1 GROUP BY run.id`, runID).Scan(&result.Input, &result.Linked, &result.AlreadyLinked, &result.Quarantined, &result.WrongBindings, &result.EffectEligible)
	return result, err
}
