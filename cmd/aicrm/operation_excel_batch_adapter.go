package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	operationport "github.com/qianlan33333-png/AI-CRM-v3/internal/operationcycle/port"
)

// operationCycleExcelStrategyAdapter is deliberately a composition-root
// adapter. AI Assistant sees only this stable read Port and never imports an
// OperationCycle store or table. The underlying repository receives the
// caller's already-bound transaction, so association validation cannot split
// from the Excel batch write.
type operationCycleExcelStrategyAdapter struct {
	read interface {
		GetStrategy(context.Context, string) (map[string]any, error)
	}
}

func (a operationCycleExcelStrategyAdapter) OperationCycleStrategy(ctx context.Context, key string) (operationport.Strategy, error) {
	if a.read == nil {
		return operationport.Strategy{}, fmt.Errorf("operation-cycle strategy reader unavailable")
	}
	value, err := a.read.GetStrategy(ctx, key)
	if err != nil {
		return operationport.Strategy{}, err
	}
	strategyKey, _ := value["strategy_key"].(string)
	title, _ := value["title"].(string)
	status, _ := value["status"].(string)
	version := 0
	switch raw := value["version"].(type) {
	case int:
		version = raw
	case int32:
		version = int(raw)
	case int64:
		version = int(raw)
	case float64:
		version = int(raw)
	}
	definition, definitionErr := json.Marshal(value["definition"])
	snapshot, snapshotErr := json.Marshal(value["snapshot"])
	if strategyKey != key || title == "" || status == "" || version < 1 || definitionErr != nil || snapshotErr != nil {
		return operationport.Strategy{}, fmt.Errorf("operation-cycle strategy projection invalid")
	}
	return operationport.Strategy{Key: strategyKey, Title: title, Status: status, Version: version, Definition: definition, Snapshot: snapshot}, nil
}

var _ operationport.StrategyReader = operationCycleExcelStrategyAdapter{}

// operationCycleExcelStrategyPageAdapter opens an Operation Cycle read through
// its application service. It differs from operationCycleExcelStrategyAdapter:
// the latter deliberately receives the already-bound UoW only for Excel batch
// creation, while this page query must own its independent read transaction.
type operationCycleExcelStrategyPageAdapter struct {
	read interface {
		ListStrategies(context.Context, int32, int32) (map[string]any, error)
	}
}

var _ operationport.StrategyPageReader = operationCycleExcelStrategyPageAdapter{}

// ListOperationCycleStrategies adapts the local Operation Cycle projection at
// the composition root. AI Assistant receives only the stable port types and
// never reaches across into an Operation Cycle table or store.
func (a operationCycleExcelStrategyPageAdapter) ListOperationCycleStrategies(ctx context.Context, limit, offset int32) (operationport.StrategyPage, error) {
	if a.read == nil {
		return operationport.StrategyPage{}, fmt.Errorf("operation-cycle strategy reader unavailable")
	}
	value, err := a.read.ListStrategies(ctx, limit, offset)
	if err != nil {
		return operationport.StrategyPage{}, err
	}
	rawItems, ok := value["items"].([]map[string]any)
	if !ok {
		return operationport.StrategyPage{}, fmt.Errorf("operation-cycle strategy page projection invalid")
	}
	total, ok := operationCyclePageInt(value["total"])
	if !ok || total < 0 {
		return operationport.StrategyPage{}, fmt.Errorf("operation-cycle strategy page total invalid")
	}
	items := make([]operationport.Strategy, 0, len(rawItems))
	for _, item := range rawItems {
		strategy, decodeErr := operationCycleStrategyProjection(item)
		if decodeErr != nil {
			return operationport.StrategyPage{}, decodeErr
		}
		items = append(items, strategy)
	}
	return operationport.StrategyPage{Items: items, Total: total, Limit: limit, Offset: offset}, nil
}

func operationCycleStrategyProjection(value map[string]any) (operationport.Strategy, error) {
	key, _ := value["strategy_key"].(string)
	title, _ := value["title"].(string)
	status, _ := value["status"].(string)
	version, versionOK := operationCyclePageInt(value["version"])
	definition, definitionErr := json.Marshal(value["definition"])
	snapshot, snapshotErr := json.Marshal(value["snapshot"])
	rawUpdatedAt, updatedOK := value["updated_at"].(string)
	updatedAt, updatedErr := time.Parse(time.RFC3339Nano, rawUpdatedAt)
	if key == "" || title == "" || status == "" || !versionOK || version < 1 || definitionErr != nil || snapshotErr != nil || !updatedOK || updatedErr != nil || !json.Valid(definition) || !json.Valid(snapshot) {
		return operationport.Strategy{}, fmt.Errorf("operation-cycle strategy projection invalid")
	}
	return operationport.Strategy{Key: key, Title: title, Status: status, Version: version, Definition: definition, Snapshot: snapshot, UpdatedAt: updatedAt.UTC()}, nil
}

func operationCyclePageInt(value any) (int, bool) {
	switch current := value.(type) {
	case int:
		return current, true
	case int32:
		return int(current), true
	case int64:
		return int(current), int64(int(current)) == current
	case float64:
		return int(current), current == float64(int(current))
	default:
		return 0, false
	}
}
