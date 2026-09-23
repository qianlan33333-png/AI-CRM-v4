package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	operationapp "github.com/qianlan33333-png/AI-CRM-v3/internal/operationcycle/app"
	operationdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/operationcycle/domain"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func (repository *Repository) CreateStrategy(ctx context.Context, command operationapp.CreateStrategyCommand, now time.Time) (map[string]any, bool, error) {
	payload := map[string]any{"operation": "create", "strategy_key": command.StrategyKey, "title": command.Title, "definition": command.Definition}
	tx, keyDigest, payloadDigest, replay, reused, err := adminMutation(ctx, command.ActorID, command.IdempotencyKey, payload)
	if err != nil || reused {
		return replay, reused, err
	}
	definition, snapshot, err := strategyDocuments(command.Title, command.Definition)
	if err != nil {
		return nil, false, operationapp.ErrInvalid
	}
	if _, err = tx.Exec(ctx, `INSERT INTO operation_cycle_strategies
		(strategy_key,title,status,version,definition,snapshot,updated_at)
		VALUES ($1,$2,'draft',1,$3,$4,$5)`, command.StrategyKey, command.Title, definition, snapshot, now); err != nil {
		return nil, false, storeError(err)
	}
	result := strategyMap(command.StrategyKey, command.Title, "draft", 1, definition, snapshot, now)
	if err = insertStrategyVersion(ctx, tx, result, command.ActorID, now); err != nil {
		return nil, false, err
	}
	if err = saveAdminReceipt(ctx, tx, command.ActorID, keyDigest, payloadDigest, result, now); err != nil {
		return nil, false, err
	}
	return result, false, nil
}

func (repository *Repository) UpdateStrategy(ctx context.Context, command operationapp.UpdateStrategyCommand, now time.Time) (map[string]any, bool, error) {
	payload := map[string]any{"operation": "update", "strategy_key": command.StrategyKey, "expected_version": command.ExpectedVersion, "title": command.Title, "definition": command.Definition}
	tx, keyDigest, payloadDigest, replay, reused, err := adminMutation(ctx, command.ActorID, command.IdempotencyKey, payload)
	if err != nil || reused {
		return replay, reused, err
	}
	current, err := lockStrategy(ctx, tx, command.StrategyKey)
	if err != nil {
		return nil, false, err
	}
	if current.version != command.ExpectedVersion || current.status == "archived" {
		return nil, false, operationapp.ErrConflict
	}
	definition, patch, err := strategyDocuments(command.Title, command.Definition)
	if err != nil {
		return nil, false, operationapp.ErrInvalid
	}
	merged, err := mergeSnapshot(current.snapshot, patch)
	if err != nil {
		return nil, false, operationapp.ErrUnavailable
	}
	version := current.version + 1
	if _, err = tx.Exec(ctx, `UPDATE operation_cycle_strategies SET title=$2,version=$3,definition=$4,snapshot=$5,updated_at=$6 WHERE strategy_key=$1`, command.StrategyKey, command.Title, version, definition, merged, now); err != nil {
		return nil, false, storeError(err)
	}
	result := strategyMap(command.StrategyKey, command.Title, current.status, version, definition, merged, now)
	if err = insertStrategyVersion(ctx, tx, result, command.ActorID, now); err != nil {
		return nil, false, err
	}
	if err = saveAdminReceipt(ctx, tx, command.ActorID, keyDigest, payloadDigest, result, now); err != nil {
		return nil, false, err
	}
	return result, false, nil
}

func (repository *Repository) TransitionStrategy(ctx context.Context, command operationapp.TransitionStrategyCommand, now time.Time) (map[string]any, bool, error) {
	payload := map[string]any{"operation": "status", "strategy_key": command.StrategyKey, "expected_version": command.ExpectedVersion, "status": command.Status}
	tx, keyDigest, payloadDigest, replay, reused, err := adminMutation(ctx, command.ActorID, command.IdempotencyKey, payload)
	if err != nil || reused {
		return replay, reused, err
	}
	current, err := lockStrategy(ctx, tx, command.StrategyKey)
	if err != nil {
		return nil, false, err
	}
	if current.version != command.ExpectedVersion || !operationdomain.CanTransitionStrategyStatus(current.status, command.Status) {
		return nil, false, operationapp.ErrConflict
	}
	version := current.version + 1
	if _, err = tx.Exec(ctx, `UPDATE operation_cycle_strategies SET status=$2,version=$3,updated_at=$4 WHERE strategy_key=$1`, command.StrategyKey, command.Status, version, now); err != nil {
		return nil, false, storeError(err)
	}
	result := strategyMap(command.StrategyKey, current.title, command.Status, version, current.definition, current.snapshot, now)
	if err = insertStrategyVersion(ctx, tx, result, command.ActorID, now); err != nil {
		return nil, false, err
	}
	if err = saveAdminReceipt(ctx, tx, command.ActorID, keyDigest, payloadDigest, result, now); err != nil {
		return nil, false, err
	}
	return result, false, nil
}

func (repository *Repository) ListStrategyVersions(ctx context.Context, key string, limit, offset int32) (map[string]any, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, storeError(err)
	}
	rows, err := tx.Query(ctx, `SELECT strategy_key,title,status,version,definition,snapshot,created_by,created_at
		FROM operation_cycle_strategy_versions WHERE strategy_key=$1 ORDER BY version DESC LIMIT $2 OFFSET $3`, key, limit, offset)
	if err != nil {
		return nil, storeError(err)
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var strategyKey, title, status, actor string
		var version int32
		var definition, snapshot []byte
		var createdAt time.Time
		if err = rows.Scan(&strategyKey, &title, &status, &version, &definition, &snapshot, &actor, &createdAt); err != nil {
			return nil, storeError(err)
		}
		item := strategyMap(strategyKey, title, status, version, definition, snapshot, createdAt)
		item["created_by"] = actor
		item["created_at"] = createdAt.UTC().Format(time.RFC3339Nano)
		items = append(items, item)
	}
	return map[string]any{"items": items, "limit": limit, "offset": offset}, storeError(rows.Err())
}

func (repository *Repository) ListRunVersions(ctx context.Context, key string, limit, offset int32) (map[string]any, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, storeError(err)
	}
	rows, err := tx.Query(ctx, `SELECT run_key,strategy_key,snapshot_revision,snapshot,received_at
		FROM operation_cycle_run_versions WHERE run_key=$1 ORDER BY snapshot_revision DESC LIMIT $2 OFFSET $3`, key, limit, offset)
	if err != nil {
		return nil, storeError(err)
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var runKey, strategyKey string
		var revision int32
		var snapshot []byte
		var receivedAt time.Time
		if err = rows.Scan(&runKey, &strategyKey, &revision, &snapshot, &receivedAt); err != nil {
			return nil, storeError(err)
		}
		items = append(items, map[string]any{"run_key": runKey, "strategy_key": strategyKey, "snapshot_revision": revision, "snapshot": decodedValue(snapshot), "received_at": receivedAt.UTC().Format(time.RFC3339Nano)})
	}
	return map[string]any{"items": items, "limit": limit, "offset": offset}, storeError(rows.Err())
}

type lockedStrategy struct {
	title      string
	status     string
	version    int32
	definition []byte
	snapshot   []byte
}

func lockStrategy(ctx context.Context, tx pgx.Tx, key string) (lockedStrategy, error) {
	var value lockedStrategy
	err := tx.QueryRow(ctx, `SELECT title,status,version,definition,snapshot FROM operation_cycle_strategies WHERE strategy_key=$1 FOR UPDATE`, key).Scan(&value.title, &value.status, &value.version, &value.definition, &value.snapshot)
	return value, storeError(err)
}

func adminMutation(ctx context.Context, actor, key string, payload any) (pgx.Tx, [32]byte, [32]byte, map[string]any, bool, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, [32]byte{}, [32]byte{}, nil, false, storeError(err)
	}
	keyDigest := sha256.Sum256([]byte(actor + "\x00" + key))
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, int64(binary.BigEndian.Uint64(keyDigest[:8]))); err != nil {
		return nil, [32]byte{}, [32]byte{}, nil, false, storeError(err)
	}
	payloadDigest, err := operationapp.Digest(payload)
	if err != nil {
		return nil, [32]byte{}, [32]byte{}, nil, false, operationapp.ErrInvalid
	}
	var storedDigest, response []byte
	err = tx.QueryRow(ctx, `SELECT payload_digest,response FROM operation_cycle_admin_receipts WHERE actor_id=$1 AND key_digest=$2`, actor, keyDigest[:]).Scan(&storedDigest, &response)
	if err == nil {
		if !bytes.Equal(storedDigest, payloadDigest[:]) {
			return nil, [32]byte{}, [32]byte{}, nil, false, operationapp.ErrConflict
		}
		var result map[string]any
		if json.Unmarshal(response, &result) != nil {
			return nil, [32]byte{}, [32]byte{}, nil, false, operationapp.ErrUnavailable
		}
		return tx, keyDigest, payloadDigest, result, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, [32]byte{}, [32]byte{}, nil, false, storeError(err)
	}
	return tx, keyDigest, payloadDigest, nil, false, nil
}

func saveAdminReceipt(ctx context.Context, tx pgx.Tx, actor string, keyDigest, payloadDigest [32]byte, response any, now time.Time) error {
	responseJSON, err := json.Marshal(response)
	if err != nil {
		return operationapp.ErrUnavailable
	}
	_, err = tx.Exec(ctx, `INSERT INTO operation_cycle_admin_receipts(actor_id,key_digest,payload_digest,response,created_at) VALUES($1,$2,$3,$4,$5)`, actor, keyDigest[:], payloadDigest[:], responseJSON, now)
	return storeError(err)
}

func strategyDocuments(title string, definition operationapp.StrategyDefinition) ([]byte, []byte, error) {
	definitionJSON, err := json.Marshal(definition)
	if err != nil {
		return nil, nil, err
	}
	snapshotJSON, err := json.Marshal(operationapp.StrategySnapshot(title, definition))
	return definitionJSON, snapshotJSON, err
}

func mergeSnapshot(current, patch []byte) ([]byte, error) {
	var base, update map[string]any
	if json.Unmarshal(current, &base) != nil || json.Unmarshal(patch, &update) != nil {
		return nil, errors.New("invalid strategy snapshot")
	}
	for key, value := range update {
		base[key] = value
	}
	return json.Marshal(base)
}

func strategyMap(key, title, status string, version int32, definition, snapshot []byte, updatedAt time.Time) map[string]any {
	return map[string]any{"strategy_key": key, "title": title, "status": status, "version": version, "definition": decodedValue(definition), "snapshot": decodedValue(snapshot), "updated_at": updatedAt.UTC().Format(time.RFC3339Nano)}
}

func insertStrategyVersion(ctx context.Context, tx pgx.Tx, value map[string]any, actor string, now time.Time) error {
	definition, err := json.Marshal(value["definition"])
	if err != nil {
		return operationapp.ErrUnavailable
	}
	snapshot, err := json.Marshal(value["snapshot"])
	if err != nil {
		return operationapp.ErrUnavailable
	}
	_, err = tx.Exec(ctx, `INSERT INTO operation_cycle_strategy_versions(strategy_key,version,title,status,definition,snapshot,created_by,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, value["strategy_key"], value["version"], value["title"], value["status"], definition, snapshot, actor, now)
	return storeError(err)
}
