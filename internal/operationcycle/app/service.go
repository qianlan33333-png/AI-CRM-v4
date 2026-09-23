// Package app implements the frozen operation-cycle commands.  It records
// local facts only; no method may enqueue a send or invoke an external API.
package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"sort"
	"strings"
	"time"

	operationdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/operationcycle/domain"
	operationport "github.com/qianlan33333-png/AI-CRM-v3/internal/operationcycle/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

var (
	ErrInvalid           = errors.New("invalid operation-cycle input")
	ErrNotFound          = errors.New("operation-cycle fact not found")
	ErrConflict          = errors.New("operation-cycle conflict")
	ErrUnavailable       = errors.New("operation-cycle dependency unavailable")
	ErrLeaseInvalid      = errors.New("operation-cycle action lease invalid")
	ErrActionUnavailable = errors.New("operation-cycle action unavailable")
)

const (
	DefaultLimit       = int32(50)
	MaximumLimit       = operationport.StrategyPageMaximumLimit
	MaximumOffset      = operationport.StrategyPageMaximumOffset
	RunnerOfflineAfter = 45 * time.Second
	ActionLease        = 60 * time.Second
)

type ReportCommand struct {
	Snapshot       map[string]any
	IdempotencyKey string
	ReporterID     string
	ClientID       string
}

type StartCommand struct {
	StrategyKey    string
	ActionKey      string
	RunKey         string
	ParentRequest  string
	IdempotencyKey string
	ActorID        string
}

type ActionEventCommand struct {
	RequestID   string
	EventID     string
	EventType   string
	LeaseToken  string
	ThreadID    string
	TurnID      string
	Result      map[string]any
	FailureCode string
}

type ActionLeaseRenewalCommand struct {
	RequestID  string
	LeaseToken string
}

type RunnerHeartbeatCommand struct {
	RunnerID            string
	ConnectorVersion    string
	CodexVersion        string
	AppServerProtocol   string
	CompatibilityStatus string
	BindingKeys         []string
	PrincipalID         string
}

type ProposalCommand struct {
	Payload        map[string]any
	IdempotencyKey string
	ActorID        string
}

// Store is transaction-bound.  Methods receive the UoW context supplied by
// Service and must never start their own transaction or perform I/O.
type Store interface {
	Report(context.Context, ReportCommand, time.Time) (map[string]any, bool, error)
	ListStrategies(context.Context, int32, int32) (map[string]any, error)
	GetStrategy(context.Context, string) (map[string]any, error)
	ListRuns(context.Context, string, int32, int32) (map[string]any, error)
	GetRun(context.Context, string) (map[string]any, error)
	GetRunByOrdinal(context.Context, int32) (map[string]any, error)
	Start(context.Context, StartCommand, time.Time) (map[string]any, bool, error)
	CurrentAction(context.Context, string) (map[string]any, error)
	GetActionResult(context.Context, string) (map[string]any, error)
	Claim(context.Context, string, string, time.Time, time.Duration) (map[string]any, bool, error)
	RecordActionEvent(context.Context, ActionEventCommand, time.Time) (map[string]any, bool, error)
	RenewActionLease(context.Context, ActionLeaseRenewalCommand, time.Time, time.Duration) (map[string]any, error)
	Heartbeat(context.Context, RunnerHeartbeatCommand, time.Time) (map[string]any, error)
	ContextIndex(context.Context, int32, int32) (map[string]any, error)
	StrategyContext(context.Context, string, string, int32, int32, map[string]string) (map[string]any, error)
	CreateProposal(context.Context, ProposalCommand, time.Time) (map[string]any, bool, error)
	ListProposals(context.Context, string, int32, int32) (map[string]any, error)
	DecideProposal(context.Context, string, string, string, time.Time) (map[string]any, error)
	CreateStrategy(context.Context, CreateStrategyCommand, time.Time) (map[string]any, bool, error)
	UpdateStrategy(context.Context, UpdateStrategyCommand, time.Time) (map[string]any, bool, error)
	TransitionStrategy(context.Context, TransitionStrategyCommand, time.Time) (map[string]any, bool, error)
	ListStrategyVersions(context.Context, string, int32, int32) (map[string]any, error)
	ListRunVersions(context.Context, string, int32, int32) (map[string]any, error)
}

type Service struct {
	uow        platformport.UnitOfWork
	store      Store
	events     operationport.EventAppender
	deliveries operationport.DeliveryAcceptor
	now        func() time.Time
}

func NewService(uow platformport.UnitOfWork, store Store, events operationport.EventAppender, deliveries operationport.DeliveryAcceptor) *Service {
	return &Service{uow: uow, store: store, events: events, deliveries: deliveries, now: time.Now}
}

func (s *Service) Report(ctx context.Context, command ReportCommand) (map[string]any, error) {
	if s == nil || s.uow == nil || s.store == nil || s.events == nil || s.deliveries == nil {
		return nil, ErrInvalid
	}
	projection, err := projectReport(command)
	if err != nil {
		return nil, ErrInvalid
	}
	command.Snapshot = projection
	var result map[string]any
	err = s.uow.Within(ctx, func(txCtx context.Context) error {
		var reused bool
		var err error
		result, reused, err = s.store.Report(txCtx, command, s.now().UTC())
		if err != nil || reused {
			return err
		}
		return s.append(txCtx, "operation_cycle.reported", command.IdempotencyKey, result)
	})
	return result, err
}

func (s *Service) ListStrategies(ctx context.Context, limit, offset int32) (map[string]any, error) {
	if s == nil || s.uow == nil || s.store == nil || !validPage(limit, offset) {
		return nil, ErrInvalid
	}
	return s.read(ctx, func(txCtx context.Context) (map[string]any, error) {
		return s.store.ListStrategies(txCtx, limit, offset)
	})
}
func (s *Service) GetStrategy(ctx context.Context, key string) (map[string]any, error) {
	if s == nil || s.uow == nil || s.store == nil || !validKey(key, 120) {
		return nil, ErrInvalid
	}
	return s.read(ctx, func(txCtx context.Context) (map[string]any, error) { return s.store.GetStrategy(txCtx, key) })
}
func (s *Service) ListRuns(ctx context.Context, key string, limit, offset int32) (map[string]any, error) {
	if s == nil || s.uow == nil || s.store == nil || !validKey(key, 120) || !validPage(limit, offset) {
		return nil, ErrInvalid
	}
	return s.read(ctx, func(txCtx context.Context) (map[string]any, error) {
		return s.store.ListRuns(txCtx, key, limit, offset)
	})
}
func (s *Service) GetRun(ctx context.Context, key string) (map[string]any, error) {
	if s == nil || s.uow == nil || s.store == nil || !validKey(key, 160) {
		return nil, ErrInvalid
	}
	return s.read(ctx, func(txCtx context.Context) (map[string]any, error) { return s.store.GetRun(txCtx, key) })
}
func (s *Service) GetRunByOrdinal(ctx context.Context, ordinal int32) (map[string]any, error) {
	if s == nil || s.uow == nil || s.store == nil || ordinal < 1 || ordinal > 999999999 {
		return nil, ErrInvalid
	}
	return s.read(ctx, func(txCtx context.Context) (map[string]any, error) {
		return s.store.GetRunByOrdinal(txCtx, ordinal)
	})
}

func (s *Service) Start(ctx context.Context, command StartCommand) (map[string]any, error) {
	if s == nil || s.uow == nil || s.store == nil || s.events == nil || s.deliveries == nil || !validStart(command) {
		return nil, ErrInvalid
	}
	var result map[string]any
	err := s.uow.Within(ctx, func(txCtx context.Context) error {
		var reused bool
		var err error
		result, reused, err = s.store.Start(txCtx, command, s.now().UTC())
		if err != nil || reused {
			return err
		}
		return s.append(txCtx, "operation_cycle.action_queued", command.IdempotencyKey, result)
	})
	return result, err
}
func (s *Service) CurrentAction(ctx context.Context, key string) (map[string]any, error) {
	if s == nil || s.uow == nil || s.store == nil || !validKey(key, 120) {
		return nil, ErrInvalid
	}
	return s.read(ctx, func(txCtx context.Context) (map[string]any, error) { return s.store.CurrentAction(txCtx, key) })
}
func (s *Service) GetActionResult(ctx context.Context, requestID string) (map[string]any, error) {
	if s == nil || s.uow == nil || s.store == nil || !validKey(requestID, 64) {
		return nil, ErrInvalid
	}
	return s.read(ctx, func(txCtx context.Context) (map[string]any, error) { return s.store.GetActionResult(txCtx, requestID) })
}
func (s *Service) Claim(ctx context.Context, runnerID, principalID string) (map[string]any, error) {
	if s == nil || s.uow == nil || s.store == nil || s.events == nil || s.deliveries == nil || !validKey(runnerID, 160) || !validKey(principalID, 240) {
		return nil, ErrInvalid
	}
	var result map[string]any
	err := s.uow.Within(ctx, func(txCtx context.Context) error {
		var claimed bool
		var err error
		result, claimed, err = s.store.Claim(txCtx, runnerID, principalID, s.now().UTC(), ActionLease)
		if err != nil || !claimed {
			return err
		}
		// A recovery issues another fenced lease for the same request. The
		// audit/outbox key therefore includes a digest of that transient lease,
		// while the raw token never enters the persisted local fact.
		leaseDigest, digestErr := Digest(resultString(result, "lease_token"))
		if digestErr != nil {
			return ErrUnavailable
		}
		fact := make(map[string]any, len(result))
		for key, value := range result {
			if key != "lease_token" {
				fact[key] = value
			}
		}
		return s.append(txCtx, "operation_cycle.action_claimed", resultString(result, "request_id")+":"+hex.EncodeToString(leaseDigest[:8]), fact)
	})
	return result, err
}
func (s *Service) RecordActionEvent(ctx context.Context, command ActionEventCommand) (map[string]any, error) {
	if s == nil || s.uow == nil || s.store == nil || s.events == nil || s.deliveries == nil || !validActionEvent(command) {
		return nil, ErrInvalid
	}
	var result map[string]any
	err := s.uow.Within(ctx, func(txCtx context.Context) error {
		var reused bool
		var err error
		result, reused, err = s.store.RecordActionEvent(txCtx, command, s.now().UTC())
		if err != nil || reused {
			return err
		}
		return s.append(txCtx, "operation_cycle.action_"+command.EventType, command.RequestID+":"+command.EventID, result)
	})
	return result, err
}
func (s *Service) RenewActionLease(ctx context.Context, command ActionLeaseRenewalCommand) (map[string]any, error) {
	if s == nil || s.uow == nil || s.store == nil || !validActionLeaseRenewal(command) {
		return nil, ErrInvalid
	}
	var result map[string]any
	err := s.uow.Within(ctx, func(txCtx context.Context) error {
		var err error
		result, err = s.store.RenewActionLease(txCtx, command, s.now().UTC(), ActionLease)
		return err
	})
	return result, err
}

func (s *Service) Heartbeat(ctx context.Context, command RunnerHeartbeatCommand) (map[string]any, error) {
	if s == nil || s.uow == nil || s.store == nil || s.events == nil || s.deliveries == nil || !validHeartbeat(command) {
		return nil, ErrInvalid
	}
	var result map[string]any
	err := s.uow.Within(ctx, func(txCtx context.Context) error {
		var err error
		result, err = s.store.Heartbeat(txCtx, command, s.now().UTC())
		if err != nil {
			return err
		}
		return s.append(txCtx, "operation_cycle.runner_heartbeat", command.RunnerID+":"+fmt.Sprint(s.now().UnixNano()), result)
	})
	return result, err
}
func (s *Service) ContextIndex(ctx context.Context, limit, offset int32) (map[string]any, error) {
	if s == nil || s.uow == nil || s.store == nil || !validPage(limit, offset) {
		return nil, ErrInvalid
	}
	return s.read(ctx, func(txCtx context.Context) (map[string]any, error) { return s.store.ContextIndex(txCtx, limit, offset) })
}
func (s *Service) StrategyContext(ctx context.Context, key, mode string, limit, offset int32, filters map[string]string) (map[string]any, error) {
	if s == nil || s.uow == nil || s.store == nil || !validKey(key, 120) || (mode != "execution" && mode != "review") || !validPage(limit, offset) || len(filters) > 3 || containsForbidden(filters) {
		return nil, ErrInvalid
	}
	return s.read(ctx, func(txCtx context.Context) (map[string]any, error) {
		return s.store.StrategyContext(txCtx, key, mode, limit, offset, filters)
	})
}
func (s *Service) CreateProposal(ctx context.Context, command ProposalCommand) (map[string]any, error) {
	if s == nil || s.uow == nil || s.store == nil || s.events == nil || s.deliveries == nil || !validProposal(command) {
		return nil, ErrInvalid
	}
	var result map[string]any
	err := s.uow.Within(ctx, func(txCtx context.Context) error {
		var reused bool
		var err error
		result, reused, err = s.store.CreateProposal(txCtx, command, s.now().UTC())
		if err != nil || reused {
			return err
		}
		return s.append(txCtx, "operation_cycle.proposal_created", command.IdempotencyKey, result)
	})
	return result, err
}
func (s *Service) ListProposals(ctx context.Context, key string, limit, offset int32) (map[string]any, error) {
	if s == nil || s.uow == nil || s.store == nil || !validKey(key, 120) || !validPage(limit, offset) {
		return nil, ErrInvalid
	}
	return s.read(ctx, func(txCtx context.Context) (map[string]any, error) {
		return s.store.ListProposals(txCtx, key, limit, offset)
	})
}
func (s *Service) DecideProposal(ctx context.Context, id, decision, actor string) (map[string]any, error) {
	if s == nil || s.uow == nil || s.store == nil || s.events == nil || s.deliveries == nil || !validKey(id, 64) || !validKey(actor, 240) || (decision != "accept" && decision != "reject") {
		return nil, ErrInvalid
	}
	var result map[string]any
	err := s.uow.Within(ctx, func(txCtx context.Context) error {
		var err error
		result, err = s.store.DecideProposal(txCtx, id, decision, actor, s.now().UTC())
		if err != nil {
			return err
		}
		return s.append(txCtx, "operation_cycle.proposal_"+decision+"ed", id, result)
	})
	return result, err
}

func (s *Service) append(ctx context.Context, eventType, idempotencyKey string, value map[string]any) error {
	payload, err := json.Marshal(map[string]any{"fact_type": eventType, "data": value})
	if err != nil {
		return ErrUnavailable
	}
	eventID, err := s.events.Append(ctx, operationport.Event{Type: operationport.EvOperationCycleFact, Payload: payload, OccurredAt: s.now().UTC(), IdempotencyKey: "operation_cycle:" + idempotencyKey})
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if err = s.deliveries.Accept(ctx, eventID, operationport.ConsumerOperationCycleFact); err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return nil
}

func (s *Service) read(ctx context.Context, read func(context.Context) (map[string]any, error)) (map[string]any, error) {
	var result map[string]any
	err := s.uow.Within(ctx, func(txCtx context.Context) error {
		var err error
		result, err = read(txCtx)
		return err
	})
	return result, err
}

func projectReport(command ReportCommand) (map[string]any, error) {
	if !validKey(command.IdempotencyKey, 200) || !validKey(command.ReporterID, 240) || !validKey(command.ClientID, 240) {
		return nil, ErrInvalid
	}
	projection, err := operationdomain.ProjectReportSnapshot(command.Snapshot)
	if err != nil {
		return nil, ErrInvalid
	}
	return projection, nil
}
func validStart(command StartCommand) bool {
	return validKey(command.StrategyKey, 120) && validKey(command.ActionKey, 120) && validKey(command.RunKey, 160) && validKey(command.IdempotencyKey, 200) && validKey(command.ActorID, 240) && (command.ParentRequest == "" || validKey(command.ParentRequest, 64))
}
func validActionEvent(command ActionEventCommand) bool {
	if !validKey(command.RequestID, 64) || !validKey(command.EventID, 200) || !validKey(command.LeaseToken, 200) {
		return false
	}
	switch command.EventType {
	case "thread_bound":
		return validKey(command.ThreadID, 200)
	case "turn_started":
		return validKey(command.ThreadID, 200) && validKey(command.TurnID, 200)
	case "completed", "failed":
		return command.Result != nil && !containsForbidden(command.Result)
	default:
		return false
	}
}
func validActionLeaseRenewal(command ActionLeaseRenewalCommand) bool {
	return validKey(command.RequestID, 64) && validKey(command.LeaseToken, 200)
}

func validHeartbeat(command RunnerHeartbeatCommand) bool {
	return validKey(command.RunnerID, 160) && validKey(command.PrincipalID, 240) && validKey(command.ConnectorVersion, 120) && validKey(command.CodexVersion, 120) && (command.CompatibilityStatus == "ready" || command.CompatibilityStatus == "incompatible" || command.CompatibilityStatus == "unavailable") && len(command.BindingKeys) <= 32
}
func validProposal(command ProposalCommand) bool {
	return validKey(command.IdempotencyKey, 200) && validKey(command.ActorID, 240) && command.Payload != nil && resultString(command.Payload, "schema_version") == "operation_cycle_strategy_change_proposal.v1" && validKey(resultString(command.Payload, "strategy_key"), 120) && !containsForbidden(command.Payload)
}
func validPage(limit, offset int32) bool {
	return limit >= 1 && limit <= MaximumLimit && offset >= 0 && offset <= MaximumOffset
}
func validKey(value string, maximum int) bool {
	return operationdomain.ValidKey(value, maximum)
}
func resultString(value map[string]any, key string) string {
	raw, _ := value[key].(string)
	return strings.TrimSpace(raw)
}
func containsForbidden(value any) bool {
	return operationdomain.ContainsForbidden(value)
}

func Digest(value any) ([32]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return [32]byte{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var decoded any
	if err = decoder.Decode(&decoded); err != nil {
		return [32]byte{}, err
	}
	if err = decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return [32]byte{}, ErrInvalid
	}
	canonical, err := canonicalJSON(decoded)
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(canonical), nil
}

func canonicalJSON(value any) ([]byte, error) {
	var output bytes.Buffer
	if err := writeCanonicalJSON(&output, value); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func writeCanonicalJSON(output *bytes.Buffer, value any) error {
	switch item := value.(type) {
	case nil:
		output.WriteString("null")
	case bool:
		if item {
			output.WriteString("true")
		} else {
			output.WriteString("false")
		}
	case string:
		encoded, err := json.Marshal(item)
		if err != nil {
			return err
		}
		output.Write(encoded)
	case json.Number:
		canonical, err := canonicalNumber(item.String())
		if err != nil {
			return err
		}
		output.WriteString(canonical)
	case []any:
		output.WriteByte('[')
		for index, child := range item {
			if index > 0 {
				output.WriteByte(',')
			}
			if err := writeCanonicalJSON(output, child); err != nil {
				return err
			}
		}
		output.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(item))
		for key := range item {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		output.WriteByte('{')
		for index, key := range keys {
			if index > 0 {
				output.WriteByte(',')
			}
			encodedKey, err := json.Marshal(key)
			if err != nil {
				return err
			}
			output.Write(encodedKey)
			output.WriteByte(':')
			if err := writeCanonicalJSON(output, item[key]); err != nil {
				return err
			}
		}
		output.WriteByte('}')
	default:
		return ErrInvalid
	}
	return nil
}

func canonicalNumber(value string) (string, error) {
	rational, ok := new(big.Rat).SetString(value)
	if !ok {
		return "", ErrInvalid
	}
	if rational.IsInt() {
		return rational.Num().String(), nil
	}
	denominator := new(big.Int).Set(rational.Denom())
	two := big.NewInt(2)
	five := big.NewInt(5)
	remainder := new(big.Int)
	scaleTwo, scaleFive := 0, 0
	for {
		remainder.Mod(denominator, two)
		if remainder.Sign() != 0 {
			break
		}
		denominator.Div(denominator, two)
		scaleTwo++
	}
	for {
		remainder.Mod(denominator, five)
		if remainder.Sign() != 0 {
			break
		}
		denominator.Div(denominator, five)
		scaleFive++
	}
	if denominator.Cmp(big.NewInt(1)) != 0 {
		return "", ErrInvalid
	}
	scale := scaleTwo
	if scaleFive > scale {
		scale = scaleFive
	}
	return strings.TrimRight(strings.TrimRight(rational.FloatString(scale), "0"), "."), nil
}
func NewID(prefix string) (string, error) {
	var bytes [14]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(bytes[:]), nil
}

var _ = operationport.StatusQueued
