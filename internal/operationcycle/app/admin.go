package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// StrategyStage is the complete, local-only stage definition exposed to an
// administrator. It intentionally contains no customer or Provider scope.
type StrategyStage struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Color string `json:"color"`
	State string `json:"state"`
}

type StrategyDefinition struct {
	Schedule       string             `json:"schedule"`
	IndicatorColor string             `json:"indicator_color"`
	PrimaryAction  string             `json:"primary_action"`
	Stages         []StrategyStage    `json:"stages"`
	Execution      *StrategyExecution `json:"execution,omitempty"`
}

// StrategyExecution is the Owner-managed task material that may be frozen
// into a newly created action. It is optional for legacy display-only
// strategies, but an action cannot start without it.
type StrategyExecution struct {
	Title                 string         `json:"title"`
	Objective             string         `json:"objective"`
	CodexPrompt           string         `json:"codex_prompt"`
	RequiredLocalBindings []string       `json:"required_local_bindings"`
	ResultSchema          map[string]any `json:"result_schema"`
}

type CreateStrategyCommand struct {
	StrategyKey    string
	Title          string
	Definition     StrategyDefinition
	IdempotencyKey string
	ActorID        string
}

type UpdateStrategyCommand struct {
	StrategyKey     string
	ExpectedVersion int32
	Title           string
	Definition      StrategyDefinition
	IdempotencyKey  string
	ActorID         string
}

type TransitionStrategyCommand struct {
	StrategyKey     string
	ExpectedVersion int32
	Status          string
	IdempotencyKey  string
	ActorID         string
}

func (s *Service) CreateStrategy(ctx context.Context, command CreateStrategyCommand) (map[string]any, error) {
	if !s.validAdmin() || !validStrategyIdentity(command.StrategyKey, command.Title, command.IdempotencyKey, command.ActorID) || !validDefinition(command.Definition) {
		return nil, ErrInvalid
	}
	return s.adminMutation(ctx, "operation_cycle.strategy_created", command.ActorID, command.IdempotencyKey, func(txCtx context.Context) (map[string]any, bool, error) {
		return s.store.CreateStrategy(txCtx, command, s.now().UTC())
	})
}

func (s *Service) UpdateStrategy(ctx context.Context, command UpdateStrategyCommand) (map[string]any, error) {
	if !s.validAdmin() || command.ExpectedVersion < 1 || !validStrategyIdentity(command.StrategyKey, command.Title, command.IdempotencyKey, command.ActorID) || !validDefinition(command.Definition) {
		return nil, ErrInvalid
	}
	return s.adminMutation(ctx, "operation_cycle.strategy_updated", command.ActorID, command.IdempotencyKey, func(txCtx context.Context) (map[string]any, bool, error) {
		return s.store.UpdateStrategy(txCtx, command, s.now().UTC())
	})
}

func (s *Service) TransitionStrategy(ctx context.Context, command TransitionStrategyCommand) (map[string]any, error) {
	if !s.validAdmin() || command.ExpectedVersion < 1 || !validStrategyKey(command.StrategyKey) || !validKey(command.IdempotencyKey, 200) || containsForbidden([]any{command.StrategyKey, command.IdempotencyKey}) || !validKey(command.ActorID, 240) || (command.Status != "active" && command.Status != "paused" && command.Status != "archived") {
		return nil, ErrInvalid
	}
	return s.adminMutation(ctx, "operation_cycle.strategy_"+command.Status, command.ActorID, command.IdempotencyKey, func(txCtx context.Context) (map[string]any, bool, error) {
		return s.store.TransitionStrategy(txCtx, command, s.now().UTC())
	})
}

func (s *Service) ListStrategyVersions(ctx context.Context, key string, limit, offset int32) (map[string]any, error) {
	if s == nil || s.uow == nil || s.store == nil || !validKey(key, 120) || !validPage(limit, offset) {
		return nil, ErrInvalid
	}
	return s.read(ctx, func(txCtx context.Context) (map[string]any, error) {
		return s.store.ListStrategyVersions(txCtx, key, limit, offset)
	})
}

func (s *Service) ListRunVersions(ctx context.Context, key string, limit, offset int32) (map[string]any, error) {
	if s == nil || s.uow == nil || s.store == nil || !validKey(key, 160) || !validPage(limit, offset) {
		return nil, ErrInvalid
	}
	return s.read(ctx, func(txCtx context.Context) (map[string]any, error) {
		return s.store.ListRunVersions(txCtx, key, limit, offset)
	})
}

func (s *Service) validAdmin() bool {
	return s != nil && s.uow != nil && s.store != nil && s.events != nil && s.deliveries != nil
}

func (s *Service) adminMutation(ctx context.Context, eventType, actor, idempotencyKey string, mutate func(context.Context) (map[string]any, bool, error)) (map[string]any, error) {
	var result map[string]any
	err := s.uow.Within(ctx, func(txCtx context.Context) error {
		var reused bool
		var err error
		result, reused, err = mutate(txCtx)
		if err != nil || reused {
			return err
		}
		auditValue := make(map[string]any, len(result)+1)
		for key, value := range result {
			auditValue[key] = value
		}
		auditValue["actor_id"] = actor
		return s.append(txCtx, eventType, adminEventKey(eventType, actor, idempotencyKey), auditValue)
	})
	return result, err
}

func adminEventKey(eventType, actor, idempotencyKey string) string {
	digest := sha256.Sum256([]byte(eventType + "\x00" + actor + "\x00" + idempotencyKey))
	return "ocadmin_" + hex.EncodeToString(digest[:])
}

func validStrategyIdentity(key, title, idempotencyKey, actor string) bool {
	return validStrategyKey(key) && strings.TrimSpace(title) == title && len(title) >= 1 && len(title) <= 200 && validKey(idempotencyKey, 200) && validKey(actor, 240) && !containsForbidden([]any{key, title, idempotencyKey})
}

func validStrategyKey(value string) bool {
	if len(value) < 1 || len(value) > 120 || !asciiAlphaNumeric(value[0]) {
		return false
	}
	for index := 1; index < len(value); index++ {
		if !asciiAlphaNumeric(value[index]) && !strings.ContainsRune("_.:-", rune(value[index])) {
			return false
		}
	}
	return true
}

func asciiAlphaNumeric(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9'
}

func validDefinition(definition StrategyDefinition) bool {
	if strings.TrimSpace(definition.Schedule) != definition.Schedule || len(definition.Schedule) < 1 || len(definition.Schedule) > 200 || containsForbidden(definition.Schedule) || !validColor(definition.IndicatorColor) || (definition.PrimaryAction != "start_review" && definition.PrimaryAction != "view_progress") || len(definition.Stages) < 1 || len(definition.Stages) > 12 {
		return false
	}
	seen := make(map[string]struct{}, len(definition.Stages))
	current := 0
	for _, stage := range definition.Stages {
		if !validKey(stage.Key, 80) || strings.TrimSpace(stage.Label) != stage.Label || len(stage.Label) < 1 || len(stage.Label) > 80 || containsForbidden([]any{stage.Key, stage.Label}) || !validColor(stage.Color) || (stage.State != "completed" && stage.State != "current" && stage.State != "pending") {
			return false
		}
		if _, exists := seen[stage.Key]; exists {
			return false
		}
		seen[stage.Key] = struct{}{}
		if stage.State == "current" {
			current++
		}
	}
	if definition.Execution != nil && !validExecution(*definition.Execution) {
		return false
	}
	return current <= 1
}

func validExecution(value StrategyExecution) bool {
	if strings.TrimSpace(value.Title) != value.Title || len(value.Title) < 1 || len(value.Title) > 160 ||
		strings.TrimSpace(value.Objective) != value.Objective || len(value.Objective) < 1 || len(value.Objective) > 2000 ||
		strings.TrimSpace(value.CodexPrompt) != value.CodexPrompt || len(value.CodexPrompt) < 1 || len(value.CodexPrompt) > 80000 ||
		len(value.RequiredLocalBindings) < 1 || len(value.RequiredLocalBindings) > 50 || containsForbidden([]any{value.Title, value.Objective, value.CodexPrompt, value.ResultSchema}) {
		return false
	}
	seen := make(map[string]struct{}, len(value.RequiredLocalBindings))
	for _, key := range value.RequiredLocalBindings {
		if !validKey(key, 160) {
			return false
		}
		if _, exists := seen[key]; exists {
			return false
		}
		seen[key] = struct{}{}
	}
	_, err := Digest(value.ResultSchema)
	return err == nil
}

func validColor(value string) bool {
	if len(value) != 7 || value[0] != '#' {
		return false
	}
	for _, character := range value[1:] {
		if !strings.ContainsRune("0123456789abcdefABCDEF", character) {
			return false
		}
	}
	return true
}

// StrategySnapshot returns only the typed donor list projection. It is safe to
// merge into a current run-backed snapshot without changing its immutable run.
func StrategySnapshot(title string, definition StrategyDefinition) map[string]any {
	steps := make([]map[string]any, 0, len(definition.Stages))
	for _, stage := range definition.Stages {
		steps = append(steps, map[string]any{"key": stage.Key, "label": stage.Label, "color": stage.Color, "state": stage.State, "dim": stage.State == "pending"})
	}
	action := "开始复盘"
	if definition.PrimaryAction == "view_progress" {
		action = "查看进度"
	}
	return map[string]any{"schema_version": "operation_cycle_snapshot.v1", "name": title, "cron": definition.Schedule, "dot": definition.IndicatorColor, "action": action, "action_key": definition.PrimaryAction, "steps": steps}
}
