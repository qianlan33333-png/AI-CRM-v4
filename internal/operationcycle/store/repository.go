// Package store persists operation-cycle facts through generated SQLC queries.
package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	operationapp "github.com/qianlan33333-png/AI-CRM-v3/internal/operationcycle/app"
	operationcycledb "github.com/qianlan33333-png/AI-CRM-v3/internal/operationcycle/store/generated"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// Repository is transaction-bound. Its methods never start transactions and
// never perform an external call.
type Repository struct{}

var _ operationapp.Store = (*Repository)(nil)

func NewRepository() *Repository { return &Repository{} }

func (repository *Repository) Report(ctx context.Context, command operationapp.ReportCommand, now time.Time) (map[string]any, bool, error) {
	queries, err := operationCycleQueries(ctx)
	if err != nil {
		return nil, false, storeError(err)
	}
	payloadDigest, err := operationapp.Digest(command.Snapshot)
	if err != nil {
		return nil, false, operationapp.ErrInvalid
	}
	keyDigest := sha256.Sum256([]byte(command.IdempotencyKey))
	actorScope := command.ReporterID + ":" + command.ClientID
	existing, err := queries.GetOperationCycleReportReceipt(ctx, operationcycledb.GetOperationCycleReportReceiptParams{
		ActorScope: actorScope, KeyDigest: keyDigest[:],
	})
	if err == nil {
		if !bytes.Equal(existing.PayloadDigest, payloadDigest[:]) {
			return nil, false, operationapp.ErrConflict
		}
		return receiptResult(existing.StrategyKey, existing.RunKey, existing.AcceptedRevision, existing.ProjectionMade), true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, storeError(err)
	}

	strategyKey := stringValue(command.Snapshot, "strategy_key")
	runKey := stringValue(command.Snapshot, "run_key")
	revision, ok := int32Value(command.Snapshot["revision"])
	if !ok || revision < 1 {
		revision = 1
	}
	version, ok := int32Value(command.Snapshot["strategy_version"])
	if !ok || version < 1 {
		version = revision
	}
	status := stringValue(command.Snapshot, "status")
	if status == "" {
		status = "active"
	}
	if status != "active" && status != "paused" && status != "archived" {
		return nil, false, operationapp.ErrInvalid
	}
	title := stringValue(command.Snapshot, "title")
	if title == "" {
		title = strategyKey
	}
	snapshot, err := json.Marshal(command.Snapshot)
	if err != nil {
		return nil, false, operationapp.ErrInvalid
	}
	definition, err := json.Marshal(map[string]any{"external_effects": "none"})
	if err != nil {
		return nil, false, operationapp.ErrUnavailable
	}
	// A report may advance a display-only strategy. Once an Owner-managed
	// executable definition exists, however, reports carry live run facts only:
	// they must not reconstruct the title, lifecycle or task material from a
	// later source snapshot and erase what Start needs to freeze.
	recordStrategyVersion := true
	stored, storedErr := queries.GetOperationCycleStrategy(ctx, strategyKey)
	if storedErr == nil {
		var storedDefinition operationapp.StrategyDefinition
		if json.Unmarshal(stored.Definition, &storedDefinition) == nil && storedDefinition.Execution != nil {
			title, status, version, definition = stored.Title, stored.Status, stored.Version, stored.Definition
			recordStrategyVersion = false
		}
	} else if !errors.Is(storedErr, pgx.ErrNoRows) {
		return nil, false, storeError(storedErr)
	}
	pgNow := pgTime(now)
	if err = queries.UpsertOperationCycleStrategy(ctx, operationcycledb.UpsertOperationCycleStrategyParams{
		StrategyKey: strategyKey, Title: title, Status: status, Version: version,
		Definition: definition, Snapshot: snapshot, UpdatedAt: pgNow,
	}); err != nil {
		return nil, false, storeError(err)
	}
	if err = queries.UpsertOperationCycleRun(ctx, operationcycledb.UpsertOperationCycleRunParams{
		RunKey: runKey, StrategyKey: strategyKey, SnapshotRevision: revision, Snapshot: snapshot, ReceivedAt: pgNow,
	}); err != nil {
		return nil, false, storeError(err)
	}
	if err = refreshCurrentStrategySnapshot(ctx, strategyKey, version); err != nil {
		return nil, false, err
	}
	if err = recordReportHistory(ctx, command.Snapshot, strategyKey, runKey, title, status, version, revision, definition, snapshot, recordStrategyVersion, now); err != nil {
		return nil, false, err
	}
	receiptID, err := operationapp.NewID("ocrep_")
	if err != nil {
		return nil, false, operationapp.ErrUnavailable
	}
	reserved, err := queries.ReserveOperationCycleReportReceipt(ctx, operationcycledb.ReserveOperationCycleReportReceiptParams{
		ID: receiptID, ActorScope: actorScope, KeyDigest: keyDigest[:], PayloadDigest: payloadDigest[:],
		StrategyKey: strategyKey, RunKey: runKey, AcceptedRevision: revision, ProjectionMade: true,
	})
	if err != nil {
		return nil, false, storeError(err)
	}
	if !bytes.Equal(reserved.PayloadDigest, payloadDigest[:]) {
		return nil, false, operationapp.ErrConflict
	}
	if !reserved.Inserted {
		return receiptResult(reserved.StrategyKey, reserved.RunKey, reserved.AcceptedRevision, reserved.ProjectionMade), true, nil
	}
	return receiptResult(strategyKey, runKey, revision, true), false, nil
}

func refreshCurrentStrategySnapshot(ctx context.Context, strategyKey string, version int32) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return storeError(err)
	}
	_, err = tx.Exec(ctx, `UPDATE operation_cycle_strategies AS strategy
		SET snapshot=latest.snapshot, updated_at=GREATEST(strategy.updated_at, latest.received_at)
		FROM (
			SELECT snapshot,received_at FROM operation_cycle_runs
			WHERE strategy_key=$1 ORDER BY received_at DESC,run_key DESC LIMIT 1
		) AS latest
		WHERE strategy.strategy_key=$1 AND strategy.version=$2`, strategyKey, version)
	return storeError(err)
}

func (repository *Repository) ListStrategies(ctx context.Context, limit, offset int32) (map[string]any, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, storeError(err)
	}
	var total int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM operation_cycle_strategies`).Scan(&total); err != nil {
		return nil, storeError(err)
	}
	rows, err := tx.Query(ctx, `SELECT strategy.strategy_key,strategy.title,strategy.status,strategy.version,
		strategy.definition,strategy.snapshot,strategy.updated_at,ordinal.ordinal
		FROM operation_cycle_strategies AS strategy
		LEFT JOIN operation_cycle_run_ordinals AS ordinal
		  ON ordinal.run_key=NULLIF(strategy.snapshot->>'run_key','')
		ORDER BY strategy.updated_at DESC,strategy.strategy_key DESC LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, storeError(err)
	}
	defer rows.Close()
	items := make([]map[string]any, 0, limit)
	for rows.Next() {
		var row operationcycledb.OperationCycleStrategy
		var ordinal pgtype.Int4
		if err = rows.Scan(&row.StrategyKey, &row.Title, &row.Status, &row.Version, &row.Definition, &row.Snapshot, &row.UpdatedAt, &ordinal); err != nil {
			return nil, storeError(err)
		}
		var snapshot struct {
			RunKey string `json:"run_key"`
		}
		if json.Unmarshal(row.Snapshot, &snapshot) != nil || (snapshot.RunKey != "" && !ordinal.Valid) {
			return nil, operationapp.ErrUnavailable
		}
		item := strategyResult(row)
		if ordinal.Valid {
			item["run_ordinal"] = ordinal.Int32
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return nil, storeError(err)
	}
	return map[string]any{"items": items, "total": total, "limit": limit, "offset": offset}, nil
}

func (repository *Repository) GetStrategy(ctx context.Context, key string) (map[string]any, error) {
	queries, err := operationCycleQueries(ctx)
	if err != nil {
		return nil, storeError(err)
	}
	row, err := queries.GetOperationCycleStrategy(ctx, key)
	if err != nil {
		return nil, storeError(err)
	}
	return strategyResult(row), nil
}

func (repository *Repository) ListRuns(ctx context.Context, key string, limit, offset int32) (map[string]any, error) {
	queries, err := operationCycleQueries(ctx)
	if err != nil {
		return nil, storeError(err)
	}
	rows, err := queries.ListOperationCycleRuns(ctx, operationcycledb.ListOperationCycleRunsParams{StrategyKey: key, Limit: limit, Offset: offset})
	if err != nil {
		return nil, storeError(err)
	}
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		items = append(items, runResult(row))
	}
	return map[string]any{"items": items, "limit": limit, "offset": offset}, nil
}

func (repository *Repository) GetRun(ctx context.Context, key string) (map[string]any, error) {
	queries, err := operationCycleQueries(ctx)
	if err != nil {
		return nil, storeError(err)
	}
	row, err := queries.GetOperationCycleRun(ctx, key)
	if err != nil {
		return nil, storeError(err)
	}
	return runResult(row), nil
}

func (repository *Repository) GetRunByOrdinal(ctx context.Context, ordinal int32) (map[string]any, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, storeError(err)
	}
	var row operationcycledb.OperationCycleRun
	err = tx.QueryRow(ctx, `SELECT run.run_key,run.strategy_key,run.snapshot_revision,run.snapshot,run.received_at
		FROM operation_cycle_run_ordinals AS ordinal
		JOIN operation_cycle_runs AS run ON run.run_key=ordinal.run_key
		WHERE ordinal.ordinal=$1`, ordinal).Scan(&row.RunKey, &row.StrategyKey, &row.SnapshotRevision, &row.Snapshot, &row.ReceivedAt)
	if err != nil {
		return nil, storeError(err)
	}
	result := runResult(row)
	result["run_ordinal"] = ordinal
	return result, nil
}

func (repository *Repository) Start(ctx context.Context, command operationapp.StartCommand, now time.Time) (map[string]any, bool, error) {
	queries, err := operationCycleQueries(ctx)
	if err != nil {
		return nil, false, storeError(err)
	}
	keyDigest := operationActionKeyDigest(command.ActorID, command.IdempotencyKey)
	existing, err := queries.FindOperationCycleActionByKey(ctx, keyDigest[:])
	if err == nil {
		if !sameActionCommand(existing.StrategyKey, existing.RunKey, existing.ActionKey, pgTextValue(existing.ParentRequestID), existing.CreatedBy, command) {
			return nil, false, operationapp.ErrConflict
		}
		return actionResult(existing), true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, storeError(err)
	}
	if active, activeErr := queries.FindActiveOperationCycleAction(ctx, command.StrategyKey); activeErr == nil {
		if sameKey, keyErr := queries.FindOperationCycleActionByKey(ctx, keyDigest[:]); keyErr == nil {
			if !sameActionCommand(sameKey.StrategyKey, sameKey.RunKey, sameKey.ActionKey, pgTextValue(sameKey.ParentRequestID), sameKey.CreatedBy, command) {
				return nil, false, operationapp.ErrConflict
			}
			return actionResult(sameKey), true, nil
		} else if !errors.Is(keyErr, pgx.ErrNoRows) {
			return nil, false, storeError(keyErr)
		}
		return actionResult(active), false, operationapp.ErrConflict
	} else if !errors.Is(activeErr, pgx.ErrNoRows) {
		return nil, false, storeError(activeErr)
	}
	strategy, err := queries.GetOperationCycleStrategy(ctx, command.StrategyKey)
	if err != nil {
		return nil, false, storeError(err)
	}
	if strategy.Status != "active" {
		return nil, false, operationapp.ErrActionUnavailable
	}
	// Freeze task material from the immutable strategy version and run revision
	// before reserving the action. A later admin edit must never rewrite the
	// work that this request asks Codex to perform.
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, false, storeError(err)
	}
	var definitionJSON []byte
	if err = tx.QueryRow(ctx, `SELECT definition FROM operation_cycle_strategy_versions WHERE strategy_key=$1 AND version=$2`, command.StrategyKey, strategy.Version).Scan(&definitionJSON); err != nil {
		return nil, false, storeError(err)
	}
	var definition operationapp.StrategyDefinition
	if json.Unmarshal(definitionJSON, &definition) != nil || definition.Execution == nil || command.ActionKey != definition.PrimaryAction {
		return nil, false, operationapp.ErrActionUnavailable
	}
	run, err := queries.GetOperationCycleRun(ctx, command.RunKey)
	if err != nil {
		return nil, false, storeError(err)
	}
	if run.StrategyKey != command.StrategyKey {
		return nil, false, operationapp.ErrConflict
	}
	var runSnapshotJSON []byte
	if err = tx.QueryRow(ctx, `SELECT snapshot FROM operation_cycle_run_versions WHERE run_key=$1 AND snapshot_revision=$2`, command.RunKey, run.SnapshotRevision).Scan(&runSnapshotJSON); err != nil {
		return nil, false, storeError(err)
	}
	var runSnapshot map[string]any
	if json.Unmarshal(runSnapshotJSON, &runSnapshot) != nil {
		return nil, false, operationapp.ErrActionUnavailable
	}
	contextJSON, err := reportStrategySnapshot(runSnapshot)
	if err != nil {
		return nil, false, operationapp.ErrActionUnavailable
	}
	var contextSummary map[string]any
	if json.Unmarshal(contextJSON, &contextSummary) != nil {
		return nil, false, operationapp.ErrUnavailable
	}
	contextSummary["run_key"] = command.RunKey
	contextSummary["snapshot_revision"] = run.SnapshotRevision
	execution := map[string]any{"schema_version": "operation_cycle_execution.v1", "strategy_key": command.StrategyKey, "strategy_version": strategy.Version, "action_key": command.ActionKey, "title": definition.Execution.Title, "objective": definition.Execution.Objective, "codex_prompt": definition.Execution.CodexPrompt, "required_local_bindings": definition.Execution.RequiredLocalBindings, "result_schema": definition.Execution.ResultSchema}
	executionDigest, err := operationapp.Digest(execution)
	if err != nil {
		return nil, false, operationapp.ErrInvalid
	}
	contextDigest, err := operationapp.Digest(contextSummary)
	if err != nil {
		return nil, false, operationapp.ErrInvalid
	}
	runners, err := queries.ListFreshOperationCycleRunners(ctx, pgTime(now.Add(-operationapp.RunnerOfflineAfter)))
	if err != nil {
		return nil, false, storeError(err)
	}
	matchedRunners := make([]operationcycledb.OperationCycleRunner, 0, len(runners))
	for _, runner := range runners {
		// A runner is eligible only when its heartbeat advertises every
		// frozen execution binding. The strategy key is business identity,
		// not a local workspace capability, so it must not select a runner.
		if runnerHasRequiredBindings(runner.BindingKeys, definition.Execution.RequiredLocalBindings) {
			matchedRunners = append(matchedRunners, runner)
		}
	}
	if len(matchedRunners) != 1 {
		return nil, false, operationapp.ErrActionUnavailable
	}
	requestID, err := operationapp.NewID("ocact_")
	if err != nil {
		return nil, false, operationapp.ErrUnavailable
	}
	reserved, err := queries.ReserveOperationCycleAction(ctx, operationcycledb.ReserveOperationCycleActionParams{
		RequestID: requestID, StrategyKey: command.StrategyKey, RunKey: command.RunKey, ActionKey: command.ActionKey,
		ActionTitle: definition.Execution.Title, StrategyVersion: strategy.Version, RunnerID: matchedRunners[0].RunnerID,
		Column8: command.ParentRequest, CreatedBy: command.ActorID, CreatedAt: pgTime(now), IdempotencyKeyDigest: keyDigest[:],
	})
	if err != nil {
		return nil, false, storeError(err)
	}
	if !reserved.Inserted && !sameActionCommand(reserved.StrategyKey, reserved.RunKey, reserved.ActionKey, pgTextValue(reserved.ParentRequestID), reserved.CreatedBy, command) {
		return nil, false, operationapp.ErrConflict
	}
	if reserved.Inserted {
		executionJSON, marshalErr := json.Marshal(execution)
		if marshalErr != nil {
			return nil, false, operationapp.ErrInvalid
		}
		contextStoredJSON, marshalErr := json.Marshal(contextSummary)
		if marshalErr != nil {
			return nil, false, operationapp.ErrInvalid
		}
		if _, err = tx.Exec(ctx, `INSERT INTO operation_cycle_action_execution_snapshots(request_id,execution,context_summary,execution_hash,context_hash,created_at) VALUES($1,$2,$3,$4,$5,$6)`, reserved.RequestID, executionJSON, contextStoredJSON, executionDigest[:], contextDigest[:], now); err != nil {
			return nil, false, storeError(err)
		}
	}
	return actionResult(reserved), !reserved.Inserted, nil
}

func operationActionKeyDigest(actorID, key string) [sha256.Size]byte {
	return sha256.Sum256([]byte(actorID + "\x00" + key))
}

func sameActionCommand(strategyKey, runKey, actionKey, parentRequest, actorID string, command operationapp.StartCommand) bool {
	return strategyKey == command.StrategyKey && runKey == command.RunKey && actionKey == command.ActionKey && parentRequest == command.ParentRequest && actorID == command.ActorID
}

func (repository *Repository) CurrentAction(ctx context.Context, strategyKey string) (map[string]any, error) {
	queries, err := operationCycleQueries(ctx)
	if err != nil {
		return nil, storeError(err)
	}
	row, err := queries.FindActiveOperationCycleAction(ctx, strategyKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return map[string]any{"current_action": nil}, nil
	}
	if err != nil {
		return nil, storeError(err)
	}
	return map[string]any{"current_action": actionResult(row)}, nil
}

func (repository *Repository) GetActionResult(ctx context.Context, requestID string) (map[string]any, error) {
	queries, err := operationCycleQueries(ctx)
	if err != nil {
		return nil, storeError(err)
	}
	row, err := queries.GetOperationCycleAction(ctx, requestID)
	if err != nil {
		return nil, storeError(err)
	}
	result := actionResult(row)
	return map[string]any{"request_id": row.RequestID, "status": row.Status, "result": result["final_result"], "failure_code": optionalString(row.FailureCode)}, nil
}

func (repository *Repository) Claim(ctx context.Context, runnerID, principalID string, now time.Time, lease time.Duration) (map[string]any, bool, error) {
	queries, err := operationCycleQueries(ctx)
	if err != nil {
		return nil, false, storeError(err)
	}
	runner, err := queries.GetOperationCycleRunner(ctx, runnerID)
	if err != nil {
		return nil, false, storeError(err)
	}
	if runner.PrincipalID != principalID || runner.CompatibilityStatus != "ready" || !runner.LastHeartbeatAt.Valid || runner.LastHeartbeatAt.Time.Before(now.Add(-operationapp.RunnerOfflineAfter)) {
		return nil, false, operationapp.ErrActionUnavailable
	}
	leaseToken, err := operationapp.NewID("lease_")
	if err != nil {
		return nil, false, operationapp.ErrUnavailable
	}
	leaseDigest := sha256.Sum256([]byte(leaseToken))
	leaseExpiresAt := now.Add(lease)

	// A restarted runner must continue an existing Codex thread/turn. Reclaim
	// only its own expired active action, under the row lock, before taking a
	// new queued action. A live lease is never displaced.
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, false, storeError(err)
	}
	var reclaimedID string
	err = tx.QueryRow(ctx, `WITH candidate AS (
		SELECT request_id FROM operation_cycle_action_requests
		WHERE runner_id=$1 AND status IN ('claimed','thread_bound','turn_started')
		  AND lease_expires_at IS NOT NULL AND lease_expires_at <= $2
		ORDER BY updated_at, request_id LIMIT 1 FOR UPDATE SKIP LOCKED
	)
	UPDATE operation_cycle_action_requests AS action
	SET lease_token_hash=$3, lease_expires_at=$4, updated_at=$2
	FROM candidate WHERE action.request_id=candidate.request_id
	RETURNING action.request_id`, runnerID, now, leaseDigest[:], leaseExpiresAt).Scan(&reclaimedID)
	if err == nil {
		action, getErr := queries.GetOperationCycleAction(ctx, reclaimedID)
		if getErr != nil {
			return nil, false, storeError(getErr)
		}
		result, snapshotErr := actionClaimResult(ctx, action.RequestID, actionResult(action))
		if snapshotErr != nil {
			return nil, false, snapshotErr
		}
		result["lease_token"] = leaseToken
		result["lease_expires_at"] = leaseExpiresAt.UTC().Format(time.RFC3339Nano)
		result["claimed"] = true
		result["recovered"] = true
		return result, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, storeError(err)
	}

	action, err := queries.GetQueuedOperationCycleActionForRunner(ctx, runnerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return map[string]any{"claimed": false}, false, nil
	}
	if err != nil {
		return nil, false, storeError(err)
	}
	updated, err := queries.ClaimOperationCycleAction(ctx, operationcycledb.ClaimOperationCycleActionParams{
		RequestID: action.RequestID, LeaseTokenHash: leaseDigest[:], LeaseExpiresAt: pgTime(leaseExpiresAt), UpdatedAt: pgTime(now),
	})
	if err != nil {
		return nil, false, storeError(err)
	}
	if updated != 1 {
		return map[string]any{"claimed": false}, false, nil
	}
	result, snapshotErr := actionClaimResult(ctx, action.RequestID, actionResult(action))
	if snapshotErr != nil {
		return nil, false, snapshotErr
	}
	result["status"] = "claimed"
	result["lease_token"] = leaseToken
	result["lease_expires_at"] = leaseExpiresAt.UTC().Format(time.RFC3339Nano)
	result["claimed"] = true
	return result, true, nil
}

// actionClaimResult supplies the only execution material a local runner may
// use. It is read from the action's immutable snapshot, never reconstructed
// from the current strategy or run. Pre-0104 actions intentionally remain
// claimable only so the runner can record a visible manual-review stop fact.
func actionClaimResult(ctx context.Context, requestID string, result map[string]any) (map[string]any, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, storeError(err)
	}
	var executionJSON, contextJSON, executionHash, contextHash []byte
	err = tx.QueryRow(ctx, `SELECT execution,context_summary,execution_hash,context_hash
		FROM operation_cycle_action_execution_snapshots WHERE request_id=$1`, requestID).Scan(&executionJSON, &contextJSON, &executionHash, &contextHash)
	if errors.Is(err, pgx.ErrNoRows) {
		result["blocked_code"] = "missing_execution_snapshot"
		return result, nil
	}
	if err != nil {
		return nil, storeError(err)
	}
	var execution, contextSummary map[string]any
	if json.Unmarshal(executionJSON, &execution) != nil || json.Unmarshal(contextJSON, &contextSummary) != nil || len(executionHash) != sha256.Size || len(contextHash) != sha256.Size {
		return nil, operationapp.ErrUnavailable
	}
	result["execution"] = execution
	result["context_summary"] = contextSummary
	result["execution_hash"] = hex.EncodeToString(executionHash)
	result["context_hash"] = hex.EncodeToString(contextHash)
	return result, nil
}

func (repository *Repository) RenewActionLease(ctx context.Context, command operationapp.ActionLeaseRenewalCommand, now time.Time, lease time.Duration) (map[string]any, error) {
	if lease <= 0 {
		return nil, operationapp.ErrInvalid
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, storeError(err)
	}
	digest := sha256.Sum256([]byte(command.LeaseToken))
	expiresAt := now.Add(lease)
	var requestID string
	err = tx.QueryRow(ctx, `UPDATE operation_cycle_action_requests
		SET lease_expires_at=$3, updated_at=$2
		WHERE request_id=$1 AND status IN ('claimed','thread_bound','turn_started')
		  AND lease_token_hash=$4 AND lease_expires_at > $2
		RETURNING request_id`, command.RequestID, now, expiresAt, digest[:]).Scan(&requestID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, operationapp.ErrLeaseInvalid
	}
	if err != nil {
		return nil, storeError(err)
	}
	return map[string]any{"request_id": requestID, "lease_expires_at": expiresAt.UTC().Format(time.RFC3339Nano)}, nil
}

func (repository *Repository) RecordActionEvent(ctx context.Context, command operationapp.ActionEventCommand, now time.Time) (map[string]any, bool, error) {
	queries, err := operationCycleQueries(ctx)
	if err != nil {
		return nil, false, storeError(err)
	}
	payload := map[string]any{
		"event_type": command.EventType, "thread_id": command.ThreadID, "turn_id": command.TurnID,
		"result": command.Result, "failure_code": command.FailureCode,
	}
	payloadDigest, err := operationapp.Digest(payload)
	if err != nil {
		return nil, false, operationapp.ErrInvalid
	}
	existingDigest, err := queries.GetOperationCycleActionEvent(ctx, operationcycledb.GetOperationCycleActionEventParams{RequestID: command.RequestID, EventID: command.EventID})
	if err == nil {
		if !bytes.Equal(existingDigest, payloadDigest[:]) {
			return nil, false, operationapp.ErrConflict
		}
		action, getErr := queries.GetOperationCycleAction(ctx, command.RequestID)
		if getErr != nil {
			return nil, false, storeError(getErr)
		}
		return actionResult(action), true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, storeError(err)
	}
	action, err := queries.GetOperationCycleActionForUpdate(ctx, command.RequestID)
	if err != nil {
		return nil, false, storeError(err)
	}
	if !validLease(action.LeaseTokenHash, action.LeaseExpiresAt, command.LeaseToken, now) {
		return nil, false, operationapp.ErrLeaseInvalid
	}
	if !validActionTransition(action.Status, command.EventType) {
		return nil, false, operationapp.ErrConflict
	}
	finalResult := action.FinalResult
	if command.EventType == "completed" || command.EventType == "failed" {
		finalResult, err = json.Marshal(command.Result)
		if err != nil {
			return nil, false, operationapp.ErrInvalid
		}
	}
	threadID, turnID := action.ThreadID.String, action.TurnID.String
	if command.EventType == "thread_bound" || command.EventType == "turn_started" {
		threadID = command.ThreadID
	}
	if command.EventType == "turn_started" {
		turnID = command.TurnID
	}
	completedAt := pgtype.Timestamptz{}
	if command.EventType == "completed" || command.EventType == "failed" {
		completedAt = pgTime(now)
	}
	if _, err = queries.UpdateOperationCycleAction(ctx, operationcycledb.UpdateOperationCycleActionParams{
		RequestID: action.RequestID, Status: command.EventType, Column3: threadID, Column4: turnID,
		FinalResult: finalResult, Column6: command.FailureCode, CompletedAt: completedAt, UpdatedAt: pgTime(now),
	}); err != nil {
		return nil, false, storeError(err)
	}
	if err = queries.InsertOperationCycleActionEvent(ctx, operationcycledb.InsertOperationCycleActionEventParams{
		RequestID: action.RequestID, EventID: command.EventID, EventType: command.EventType, PayloadDigest: payloadDigest[:], OccurredAt: pgTime(now),
	}); err != nil {
		return nil, false, storeError(err)
	}
	updated, err := queries.GetOperationCycleAction(ctx, action.RequestID)
	if err != nil {
		return nil, false, storeError(err)
	}
	return actionResult(updated), false, nil
}

func (repository *Repository) Heartbeat(ctx context.Context, command operationapp.RunnerHeartbeatCommand, now time.Time) (map[string]any, error) {
	queries, err := operationCycleQueries(ctx)
	if err != nil {
		return nil, storeError(err)
	}
	bindingKeys, err := json.Marshal(command.BindingKeys)
	if err != nil {
		return nil, operationapp.ErrInvalid
	}
	if err = queries.UpsertOperationCycleRunner(ctx, operationcycledb.UpsertOperationCycleRunnerParams{
		RunnerID: command.RunnerID, PrincipalID: command.PrincipalID, ConnectorVersion: command.ConnectorVersion,
		CodexVersion: command.CodexVersion, CompatibilityStatus: command.CompatibilityStatus,
		BindingKeys: bindingKeys, LastHeartbeatAt: pgTime(now),
	}); err != nil {
		return nil, storeError(err)
	}
	return map[string]any{"runner_id": command.RunnerID, "compatibility_status": command.CompatibilityStatus, "heartbeat_at": now.UTC().Format(time.RFC3339Nano)}, nil
}

func (repository *Repository) ContextIndex(ctx context.Context, limit, offset int32) (map[string]any, error) {
	strategies, err := repository.ListStrategies(ctx, limit, offset)
	if err != nil {
		return nil, err
	}
	strategies["schema_version"] = "operation_cycle_context_index.v1"
	return strategies, nil
}

func (repository *Repository) StrategyContext(ctx context.Context, key, mode string, limit, offset int32, filters map[string]string) (map[string]any, error) {
	if len(filters) != 0 {
		return nil, operationapp.ErrInvalid
	}
	strategy, err := repository.GetStrategy(ctx, key)
	if err != nil {
		return nil, err
	}
	queries, err := operationCycleQueries(ctx)
	if err != nil {
		return nil, storeError(err)
	}
	actions, err := queries.ListOperationCycleActions(ctx, operationcycledb.ListOperationCycleActionsParams{StrategyKey: key, Limit: limit, Offset: offset})
	if err != nil {
		return nil, storeError(err)
	}
	items := make([]map[string]any, 0, len(actions))
	for _, action := range actions {
		items = append(items, actionResult(action))
	}
	return map[string]any{"strategy": strategy, "mode": mode, "actions": items, "limit": limit, "offset": offset}, nil
}

func (repository *Repository) CreateProposal(ctx context.Context, command operationapp.ProposalCommand, now time.Time) (map[string]any, bool, error) {
	queries, err := operationCycleQueries(ctx)
	if err != nil {
		return nil, false, storeError(err)
	}
	payloadDigest, err := operationapp.Digest(command.Payload)
	if err != nil {
		return nil, false, operationapp.ErrInvalid
	}
	keyDigest := sha256.Sum256([]byte(command.IdempotencyKey))
	existing, err := queries.GetOperationCycleProposalByActorKey(ctx, operationcycledb.GetOperationCycleProposalByActorKeyParams{CreatedBy: command.ActorID, IdempotencyKeyDigest: keyDigest[:]})
	if err == nil {
		existingDigest, digestErr := operationapp.Digest(decodedValue(existing.Proposal))
		if digestErr != nil || !bytes.Equal(existingDigest[:], payloadDigest[:]) {
			return nil, false, operationapp.ErrConflict
		}
		return proposalResult(existing), true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, storeError(err)
	}
	strategyKey := stringValue(command.Payload, "strategy_key")
	strategy, err := queries.GetOperationCycleStrategy(ctx, strategyKey)
	if err != nil {
		return nil, false, storeError(err)
	}
	baseVersion, ok := int32Value(command.Payload["base_strategy_version"])
	if !ok || baseVersion != strategy.Version {
		return nil, false, operationapp.ErrConflict
	}
	payload, err := json.Marshal(command.Payload)
	if err != nil {
		return nil, false, operationapp.ErrInvalid
	}
	proposalID, err := operationapp.NewID("ocprop_")
	if err != nil {
		return nil, false, operationapp.ErrUnavailable
	}
	reserved, err := queries.ReserveOperationCycleProposal(ctx, operationcycledb.ReserveOperationCycleProposalParams{
		ProposalID: proposalID, StrategyKey: strategyKey, BaseStrategyVersion: baseVersion, Proposal: payload,
		CreatedBy: command.ActorID, CreatedAt: pgTime(now), IdempotencyKeyDigest: keyDigest[:],
	})
	if err != nil {
		return nil, false, storeError(err)
	}
	reservedDigest, digestErr := operationapp.Digest(decodedValue(reserved.Proposal))
	if digestErr != nil || !bytes.Equal(reservedDigest[:], payloadDigest[:]) {
		return nil, false, operationapp.ErrConflict
	}
	return proposalResult(reserved), !reserved.Inserted, nil
}

func (repository *Repository) ListProposals(ctx context.Context, key string, limit, offset int32) (map[string]any, error) {
	queries, err := operationCycleQueries(ctx)
	if err != nil {
		return nil, storeError(err)
	}
	rows, err := queries.ListOperationCycleProposals(ctx, operationcycledb.ListOperationCycleProposalsParams{StrategyKey: key, Limit: limit, Offset: offset})
	if err != nil {
		return nil, storeError(err)
	}
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		items = append(items, proposalResult(row))
	}
	return map[string]any{"items": items, "limit": limit, "offset": offset}, nil
}

func (repository *Repository) DecideProposal(ctx context.Context, proposalID, decision, actorID string, now time.Time) (map[string]any, error) {
	queries, err := operationCycleQueries(ctx)
	if err != nil {
		return nil, storeError(err)
	}
	proposal, err := queries.GetOperationCycleProposalForUpdate(ctx, proposalID)
	if err != nil {
		return nil, storeError(err)
	}
	if proposal.Status != "pending" {
		return nil, operationapp.ErrConflict
	}
	status := "accepted"
	if decision == "reject" {
		status = "rejected"
	}
	updated, err := queries.DecideOperationCycleProposal(ctx, operationcycledb.DecideOperationCycleProposalParams{
		ProposalID: proposalID, Status: status, DecidedBy: pgtype.Text{String: actorID, Valid: true}, DecidedAt: pgTime(now),
	})
	if err != nil {
		return nil, storeError(err)
	}
	if updated != 1 {
		return nil, operationapp.ErrConflict
	}
	result, err := queries.GetOperationCycleProposal(ctx, proposalID)
	if err != nil {
		return nil, storeError(err)
	}
	return proposalResult(result), nil
}

func operationCycleQueries(ctx context.Context) (*operationcycledb.Queries, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	return operationcycledb.New(tx), nil
}

func recordReportHistory(ctx context.Context, report map[string]any, strategyKey, runKey, title, status string, version, revision int32, definition, snapshot []byte, recordStrategyVersion bool, now time.Time) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return storeError(err)
	}
	if recordStrategyVersion {
		strategySnapshot, err := reportStrategySnapshot(report)
		if err != nil {
			return operationapp.ErrInvalid
		}
		inserted, err := tx.Exec(ctx, `INSERT INTO operation_cycle_strategy_versions(strategy_key,version,title,status,definition,snapshot,created_by,created_at)
			VALUES($1,$2,$3,$4,$5,$6,'runner-report',$7) ON CONFLICT DO NOTHING`, strategyKey, version, title, status, definition, strategySnapshot, now)
		if err != nil {
			return storeError(err)
		}
		if inserted.RowsAffected() == 0 {
			var storedTitle, storedStatus string
			var storedDefinition, storedSnapshot []byte
			if err = tx.QueryRow(ctx, `SELECT title,status,definition,snapshot FROM operation_cycle_strategy_versions WHERE strategy_key=$1 AND version=$2`, strategyKey, version).Scan(&storedTitle, &storedStatus, &storedDefinition, &storedSnapshot); err != nil {
				return storeError(err)
			}
			if storedTitle != title || storedStatus != status || !jsonEqual(storedDefinition, definition) || !jsonEqual(storedSnapshot, strategySnapshot) {
				return operationapp.ErrConflict
			}
		}
	}
	inserted, err := tx.Exec(ctx, `INSERT INTO operation_cycle_run_versions(run_key,snapshot_revision,strategy_key,snapshot,received_at)
		VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`, runKey, revision, strategyKey, snapshot, now)
	if err != nil {
		return storeError(err)
	}
	if inserted.RowsAffected() == 0 {
		var storedStrategy string
		var storedSnapshot []byte
		if err = tx.QueryRow(ctx, `SELECT strategy_key,snapshot FROM operation_cycle_run_versions WHERE run_key=$1 AND snapshot_revision=$2`, runKey, revision).Scan(&storedStrategy, &storedSnapshot); err != nil {
			return storeError(err)
		}
		if storedStrategy != strategyKey || !jsonEqual(storedSnapshot, snapshot) {
			return operationapp.ErrConflict
		}
	}
	return nil
}

func reportStrategySnapshot(report map[string]any) ([]byte, error) {
	result := make(map[string]any)
	for _, key := range []string{"schema_version", "name", "cron", "dot", "action", "steps"} {
		if value, exists := report[key]; exists {
			result[key] = value
		}
	}
	result["action_key"] = "start_review"
	if stringValue(report, "action") == "查看进度" {
		result["action_key"] = "view_progress"
	}
	return json.Marshal(result)
}

func jsonEqual(left, right []byte) bool {
	var leftValue, rightValue any
	if json.Unmarshal(left, &leftValue) != nil || json.Unmarshal(right, &rightValue) != nil {
		return false
	}
	leftDigest, leftErr := operationapp.Digest(leftValue)
	rightDigest, rightErr := operationapp.Digest(rightValue)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftDigest[:], rightDigest[:])
}

func storeError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return operationapp.ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return operationapp.ErrConflict
	}
	return fmt.Errorf("%w: %v", operationapp.ErrUnavailable, err)
}

func validLease(stored []byte, expires pgtype.Timestamptz, provided string, now time.Time) bool {
	if len(stored) != sha256.Size || !expires.Valid || !now.Before(expires.Time) {
		return false
	}
	digest := sha256.Sum256([]byte(provided))
	return subtle.ConstantTimeCompare(stored, digest[:]) == 1
}

func validActionTransition(current, next string) bool {
	switch current {
	case "claimed":
		return next == "thread_bound"
	case "thread_bound":
		return next == "turn_started" || next == "completed" || next == "failed"
	case "turn_started":
		return next == "completed" || next == "failed"
	default:
		return false
	}
}

func runnerHasRequiredBindings(bindingKeys []byte, required []string) bool {
	var bindings []string
	if json.Unmarshal(bindingKeys, &bindings) != nil {
		return false
	}
	available := make(map[string]struct{}, len(bindings))
	for _, binding := range bindings {
		available[binding] = struct{}{}
	}
	for _, binding := range required {
		if _, ok := available[binding]; !ok {
			return false
		}
	}
	return true
}

func receiptResult(strategyKey, runKey string, revision int32, projectionMade bool) map[string]any {
	return map[string]any{"accepted": true, "strategy_key": strategyKey, "run_key": runKey, "accepted_revision": revision, "projection_updated": projectionMade}
}

func strategyResult(row operationcycledb.OperationCycleStrategy) map[string]any {
	return map[string]any{
		"strategy_key": row.StrategyKey, "title": row.Title, "status": row.Status, "version": row.Version,
		"definition": decodedValue(row.Definition), "snapshot": decodedValue(row.Snapshot), "updated_at": timeValue(row.UpdatedAt),
	}
}

func runResult(row operationcycledb.OperationCycleRun) map[string]any {
	return map[string]any{"run_key": row.RunKey, "strategy_key": row.StrategyKey, "snapshot_revision": row.SnapshotRevision, "snapshot": decodedValue(row.Snapshot), "received_at": timeValue(row.ReceivedAt)}
}

func actionResult(row interface {
	// The generated action query result structs deliberately share no named
	// interface. This marker prevents accidental cross-domain exposure.
}) map[string]any {
	switch value := row.(type) {
	case operationcycledb.FindActiveOperationCycleActionRow:
		return actionValues(value.RequestID, value.StrategyKey, value.RunKey, value.ActionKey, value.ActionTitle, value.StrategyVersion, value.RunnerID, value.Status, value.ParentRequestID, value.ThreadID, value.TurnID, value.FinalResult, value.FailureCode, value.CreatedBy, value.CreatedAt, value.UpdatedAt, value.CompletedAt)
	case operationcycledb.FindOperationCycleActionByKeyRow:
		return actionValues(value.RequestID, value.StrategyKey, value.RunKey, value.ActionKey, value.ActionTitle, value.StrategyVersion, value.RunnerID, value.Status, value.ParentRequestID, value.ThreadID, value.TurnID, value.FinalResult, value.FailureCode, value.CreatedBy, value.CreatedAt, value.UpdatedAt, value.CompletedAt)
	case operationcycledb.GetOperationCycleActionRow:
		return actionValues(value.RequestID, value.StrategyKey, value.RunKey, value.ActionKey, value.ActionTitle, value.StrategyVersion, value.RunnerID, value.Status, value.ParentRequestID, value.ThreadID, value.TurnID, value.FinalResult, value.FailureCode, value.CreatedBy, value.CreatedAt, value.UpdatedAt, value.CompletedAt)
	case operationcycledb.GetQueuedOperationCycleActionForRunnerRow:
		return actionValues(value.RequestID, value.StrategyKey, value.RunKey, value.ActionKey, value.ActionTitle, value.StrategyVersion, value.RunnerID, value.Status, value.ParentRequestID, value.ThreadID, value.TurnID, value.FinalResult, value.FailureCode, value.CreatedBy, value.CreatedAt, value.UpdatedAt, value.CompletedAt)
	case operationcycledb.GetOperationCycleActionForUpdateRow:
		return actionValues(value.RequestID, value.StrategyKey, value.RunKey, value.ActionKey, value.ActionTitle, value.StrategyVersion, value.RunnerID, value.Status, value.ParentRequestID, value.ThreadID, value.TurnID, value.FinalResult, value.FailureCode, value.CreatedBy, value.CreatedAt, value.UpdatedAt, value.CompletedAt)
	case operationcycledb.ListOperationCycleActionsRow:
		return actionValues(value.RequestID, value.StrategyKey, value.RunKey, value.ActionKey, value.ActionTitle, value.StrategyVersion, value.RunnerID, value.Status, value.ParentRequestID, value.ThreadID, value.TurnID, value.FinalResult, value.FailureCode, value.CreatedBy, value.CreatedAt, value.UpdatedAt, value.CompletedAt)
	case operationcycledb.ReserveOperationCycleActionRow:
		return actionValues(value.RequestID, value.StrategyKey, value.RunKey, value.ActionKey, value.ActionTitle, value.StrategyVersion, value.RunnerID, value.Status, value.ParentRequestID, value.ThreadID, value.TurnID, value.FinalResult, value.FailureCode, value.CreatedBy, value.CreatedAt, value.UpdatedAt, value.CompletedAt)
	default:
		return nil
	}
}

func actionValues(requestID, strategyKey, runKey, actionKey, actionTitle string, version int32, runnerID, status string, parent, thread, turn pgtype.Text, finalResult []byte, failureCode pgtype.Text, createdBy string, createdAt, updatedAt, completedAt pgtype.Timestamptz) map[string]any {
	return map[string]any{
		"request_id": requestID, "strategy_key": strategyKey, "run_key": runKey, "action_key": actionKey, "action_title": actionTitle,
		"strategy_version": version, "runner_id": runnerID, "status": status, "parent_request_id": optionalString(parent),
		"thread_id": optionalString(thread), "turn_id": optionalString(turn), "final_result": decodedValue(finalResult),
		"failure_code": optionalString(failureCode), "created_by": createdBy, "created_at": timeValue(createdAt),
		"updated_at": timeValue(updatedAt), "completed_at": nullableTimeValue(completedAt),
	}
}

func proposalResult(row interface{}) map[string]any {
	switch value := row.(type) {
	case operationcycledb.GetOperationCycleProposalRow:
		return proposalValues(value.ProposalID, value.StrategyKey, value.BaseStrategyVersion, value.Status, value.Proposal, value.CreatedBy, value.DecidedBy, value.CreatedAt, value.DecidedAt)
	case operationcycledb.GetOperationCycleProposalByActorKeyRow:
		return proposalValues(value.ProposalID, value.StrategyKey, value.BaseStrategyVersion, value.Status, value.Proposal, value.CreatedBy, value.DecidedBy, value.CreatedAt, value.DecidedAt)
	case operationcycledb.GetOperationCycleProposalForUpdateRow:
		return proposalValues(value.ProposalID, value.StrategyKey, value.BaseStrategyVersion, value.Status, value.Proposal, value.CreatedBy, value.DecidedBy, value.CreatedAt, value.DecidedAt)
	case operationcycledb.ListOperationCycleProposalsRow:
		return proposalValues(value.ProposalID, value.StrategyKey, value.BaseStrategyVersion, value.Status, value.Proposal, value.CreatedBy, value.DecidedBy, value.CreatedAt, value.DecidedAt)
	case operationcycledb.ReserveOperationCycleProposalRow:
		return proposalValues(value.ProposalID, value.StrategyKey, value.BaseStrategyVersion, value.Status, value.Proposal, value.CreatedBy, value.DecidedBy, value.CreatedAt, value.DecidedAt)
	default:
		return nil
	}
}

func proposalValues(id, strategyKey string, baseVersion int32, status string, payload []byte, createdBy string, decidedBy pgtype.Text, createdAt, decidedAt pgtype.Timestamptz) map[string]any {
	return map[string]any{"proposal_id": id, "strategy_key": strategyKey, "base_strategy_version": baseVersion, "status": status, "proposal": decodedValue(payload), "created_by": createdBy, "decided_by": optionalString(decidedBy), "created_at": timeValue(createdAt), "decided_at": nullableTimeValue(decidedAt)}
}

func decodedValue(raw []byte) any {
	if len(raw) == 0 {
		return nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil
	}
	return value
}

func stringValue(value map[string]any, key string) string {
	result, _ := value[key].(string)
	return result
}

func int32Value(value any) (int32, bool) {
	switch number := value.(type) {
	case int:
		if number >= math.MinInt32 && number <= math.MaxInt32 {
			return int32(number), true
		}
	case int32:
		return number, true
	case int64:
		if number >= math.MinInt32 && number <= math.MaxInt32 {
			return int32(number), true
		}
	case float64:
		if number >= math.MinInt32 && number <= math.MaxInt32 && math.Trunc(number) == number {
			return int32(number), true
		}
	}
	return 0, false
}

func pgTime(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value.UTC(), Valid: true}
}
func timeValue(value pgtype.Timestamptz) string {
	if !value.Valid {
		return ""
	}
	return value.Time.UTC().Format(time.RFC3339Nano)
}
func nullableTimeValue(value pgtype.Timestamptz) any {
	if !value.Valid {
		return nil
	}
	return value.Time.UTC().Format(time.RFC3339Nano)
}
func optionalString(value pgtype.Text) any {
	if !value.Valid {
		return nil
	}
	return value.String
}

func pgTextValue(value pgtype.Text) string {
	if !value.Valid {
		return ""
	}
	return value.String
}
