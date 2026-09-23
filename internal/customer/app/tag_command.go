package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/idempotency"
	platformoutbox "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/outbox"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

type TagCommandStore interface {
	FindTagCommand(context.Context, string, string, string) (customerport.TagCommandResult, [32]byte, bool, error)
	LockTagCommandTargets(context.Context, []customerport.TagCommandTarget) error
	EnsureTagCommandTargetsIdle(context.Context, []customerport.TagCommandTarget) error
	CreateTagCommand(context.Context, customerport.TagCommand, [32]byte) (int64, error)
	SetTagCommandState(context.Context, int64, string) error
	CreateRejectedTagCommandLine(context.Context, int64, customerport.TagCommandTarget, string) (customerport.TagCommandLine, error)
	CreateTagCommandLine(context.Context, int64, customerport.FrozenTagCommandTarget, string, effectport.Projection, effectport.Receipt) (customerport.TagCommandLine, error)
}

type TagCommandService struct {
	uow     platformport.UnitOfWork
	store   TagCommandStore
	effects effectport.TransactionalAccepter
	gate    customerport.TagCommandTargetGate
	audit   interface {
		Append(context.Context, platformaudit.Event) (platformaudit.Event, error)
	}
	outbox platformoutbox.Appender
	now    func() time.Time
}

func NewTagCommandService(uow platformport.UnitOfWork, store TagCommandStore, effects effectport.TransactionalAccepter, gate customerport.TagCommandTargetGate, audit interface {
	Append(context.Context, platformaudit.Event) (platformaudit.Event, error)
}, outbox platformoutbox.Appender) (*TagCommandService, error) {
	if uow == nil || store == nil || effects == nil || gate == nil || audit == nil || outbox == nil {
		return nil, customerport.ErrTagCommandUnavailable
	}
	return &TagCommandService{uow: uow, store: store, effects: effects, gate: gate, audit: audit, outbox: outbox, now: time.Now}, nil
}
func (s *TagCommandService) PreviewTagCommand(ctx context.Context, c customerport.TagCommand) (customerport.TagCommandResult, error) {
	if s == nil || s.gate == nil || !validTagCommand(c) {
		return customerport.TagCommandResult{}, customerport.ErrTagCommandInvalid
	}
	result := customerport.TagCommandResult{State: "preview", Lines: make([]customerport.TagCommandLine, 0, len(c.Targets))}
	for _, target := range canonicalTargets(c.Targets) {
		frozen, err := s.gate.FreezeTagCommandTarget(ctx, target)
		if err != nil {
			result.Lines = append(result.Lines, customerport.TagCommandLine{CustomerID: target.CustomerID, StaffID: target.StaffID, AddTagIDs: append([]int64(nil), target.AddTagIDs...), RemoveTagIDs: append([]int64(nil), target.RemoveTagIDs...), State: "rejected", RejectReason: "target_unavailable"})
			continue
		}
		result.Lines = append(result.Lines, customerport.TagCommandLine{CustomerID: frozen.CustomerID, StaffID: frozen.StaffID, AddTagIDs: append([]int64(nil), frozen.AddTagIDs...), RemoveTagIDs: append([]int64(nil), frozen.RemoveTagIDs...), BindingDigest: frozen.BindingDigest, TargetDigest: frozen.TargetDigest, State: "eligible"})
	}
	return result, nil
}

func (s *TagCommandService) SubmitTagCommand(ctx context.Context, c customerport.TagCommand) (customerport.TagCommandResult, error) {
	var r customerport.TagCommandResult
	err := s.uow.Within(ctx, func(tx context.Context) error { var e error; r, e = s.SubmitTagCommandWithin(tx, c); return e })
	return r, err
}
func (s *TagCommandService) SubmitTagCommandWithin(ctx context.Context, c customerport.TagCommand) (customerport.TagCommandResult, error) {
	if s == nil || s.store == nil || s.effects == nil || s.gate == nil || !validTagCommand(c) {
		return customerport.TagCommandResult{}, customerport.ErrTagCommandInvalid
	}
	c.OccurredAt = c.OccurredAt.UTC()
	canonical := canonicalTargets(c.Targets)
	c.Targets = canonical
	raw, _ := json.Marshal(struct {
		Source, SourceRef, Key string
		Actor                  int64
		Targets                []customerport.TagCommandTarget
	}{c.Source, c.SourceRef, c.IdempotencyKey, c.ActorAdminUserID, c.Targets})
	digest := sha256.Sum256(raw)
	prior, priorDigest, found, err := s.store.FindTagCommand(ctx, c.Source, c.SourceRef, c.IdempotencyKey)
	if err != nil {
		return customerport.TagCommandResult{}, err
	}
	if found {
		if priorDigest != digest {
			return customerport.TagCommandResult{}, customerport.ErrTagCommandConflict
		}
		return prior, nil
	}
	// Lock each canonical Customer row before accepting a different command.
	// This makes concurrent opposite add/remove requests deterministic and
	// keeps an outcome_unknown effect bound to its original key until terminal.
	if err = s.store.LockTagCommandTargets(ctx, canonical); err != nil {
		return customerport.TagCommandResult{}, err
	}
	// A same-key concurrent caller may have committed while this transaction
	// waited for the Customer locks. Re-read its receipt before creating one.
	prior, priorDigest, found, err = s.store.FindTagCommand(ctx, c.Source, c.SourceRef, c.IdempotencyKey)
	if err != nil {
		return customerport.TagCommandResult{}, err
	}
	if found {
		if priorDigest != digest {
			return customerport.TagCommandResult{}, customerport.ErrTagCommandConflict
		}
		return prior, nil
	}
	if err = s.store.EnsureTagCommandTargetsIdle(ctx, canonical); err != nil {
		return customerport.TagCommandResult{}, err
	}
	id, err := s.store.CreateTagCommand(ctx, c, digest)
	if err != nil {
		prior, priorDigest, found, readErr := s.store.FindTagCommand(ctx, c.Source, c.SourceRef, c.IdempotencyKey)
		if readErr == nil && found {
			if priorDigest != digest {
				return customerport.TagCommandResult{}, customerport.ErrTagCommandConflict
			}
			return prior, nil
		}
		return customerport.TagCommandResult{}, err
	}
	result := customerport.TagCommandResult{ID: id, State: "queued", Lines: make([]customerport.TagCommandLine, 0, len(c.Targets))}
	for index, target := range c.Targets {
		frozen, freezeErr := s.gate.FreezeTagCommandTarget(ctx, target)
		if freezeErr != nil {
			line, lineErr := s.store.CreateRejectedTagCommandLine(ctx, id, target, "target_unavailable")
			if lineErr != nil {
				return customerport.TagCommandResult{}, lineErr
			}
			result.Lines = append(result.Lines, line)
			continue
		}
		source := effectport.Hash("customer.tag.command.source.v1", c.Source, c.SourceRef, c.IdempotencyKey, strconv.Itoa(index))
		projection, receipt, acceptErr := s.effects.AcceptAndQueueWithin(ctx, effectport.AcceptCommand{ReceiptKey: effectport.Hash("customer.tag.command.accept.v1", c.Source, c.SourceRef, c.IdempotencyKey, strconv.Itoa(index)), Envelope: effectport.Envelope{Owner: effectport.OwnerOutbound, Kind: effectport.KindCustomerTagCommand, SourceRefDigest: source, TargetRefDigest: effectport.Digest(frozen.TargetDigest), PayloadDigest: effectport.Hash("customer.tag.command.payload.v1", joinIDs(target.AddTagIDs), joinIDs(target.RemoveTagIDs), frozen.BindingDigest), PolicyVersionHash: effectport.Hash("customer.tag.command.policy.v1")}})
		if acceptErr != nil {
			return customerport.TagCommandResult{}, acceptErr
		}
		line, lineErr := s.store.CreateTagCommandLine(ctx, id, frozen, string(source), projection, receipt)
		if lineErr != nil {
			return customerport.TagCommandResult{}, lineErr
		}
		result.Lines = append(result.Lines, line)
	}
	result.State = commandState(result.Lines)
	if err = s.store.SetTagCommandState(ctx, result.ID, result.State); err != nil {
		return customerport.TagCommandResult{}, err
	}
	if err = s.appendFacts(ctx, c, result); err != nil {
		return customerport.TagCommandResult{}, err
	}
	return result, nil
}
func (s *TagCommandService) appendFacts(ctx context.Context, c customerport.TagCommand, r customerport.TagCommandResult) error {
	payload, _ := json.Marshal(map[string]any{"command_id": r.ID, "line_count": len(r.Lines), "state": r.State, "source": c.Source})
	key := "customer-tag-command:" + c.Source + ":" + c.SourceRef + ":" + c.IdempotencyKey
	actor := "system"
	if c.ActorAdminUserID > 0 {
		actor = strconv.FormatInt(c.ActorAdminUserID, 10)
	}
	if _, err := s.audit.Append(ctx, platformaudit.Event{IdempotencyKey: idempotency.Key(key), Action: "customer.tag_command.accepted", ActorType: "admin", ActorID: actor, ResourceType: "customer_tag_command", ResourceID: strconv.FormatInt(r.ID, 10), Payload: payload, OccurredAt: c.OccurredAt}); err != nil && !errors.Is(err, platformaudit.ErrDuplicateEvent) {
		return err
	}
	_, err := s.outbox.Append(ctx, platformoutbox.Event{AggregateType: "customer_tag_command", AggregateID: strconv.FormatInt(r.ID, 10), Type: "customer.tag_command.accepted.v1", Version: 1, IdempotencyKey: key, Payload: payload, OccurredAt: c.OccurredAt})
	return err
}
func validTagCommand(c customerport.TagCommand) bool {
	if c.Source != strings.TrimSpace(c.Source) || c.Source == "" || len(c.Source) > 64 || c.SourceRef != strings.TrimSpace(c.SourceRef) || c.SourceRef == "" || len(c.SourceRef) > 160 || c.IdempotencyKey != strings.TrimSpace(c.IdempotencyKey) || c.IdempotencyKey == "" || len(c.IdempotencyKey) > 160 || c.OccurredAt.IsZero() || len(c.Targets) == 0 || len(c.Targets) > 1000 {
		return false
	}
	seen := map[int64]struct{}{}
	for _, t := range c.Targets {
		if t.CustomerID < 1 || t.StaffID < 0 || len(t.AddTagIDs)+len(t.RemoveTagIDs) == 0 || len(t.AddTagIDs) > 100 || len(t.RemoveTagIDs) > 100 || len(t.AddTagIDs)+len(t.RemoveTagIDs) > 100 {
			return false
		}
		if _, ok := seen[int64(t.CustomerID)]; ok {
			return false
		}
		seen[int64(t.CustomerID)] = struct{}{}
		if !validIDSet(t.AddTagIDs) || !validIDSet(t.RemoveTagIDs) || setsOverlap(t.AddTagIDs, t.RemoveTagIDs) {
			return false
		}
	}
	return true
}
func canonicalTargets(v []customerport.TagCommandTarget) []customerport.TagCommandTarget {
	out := append([]customerport.TagCommandTarget(nil), v...)
	for i := range out {
		out[i].AddTagIDs = canonicalIDs(out[i].AddTagIDs)
		out[i].RemoveTagIDs = canonicalIDs(out[i].RemoveTagIDs)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CustomerID < out[j].CustomerID })
	return out
}
func canonicalIDs(v []int64) []int64 {
	out := append([]int64(nil), v...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
func validIDSet(v []int64) bool {
	seen := map[int64]bool{}
	for _, id := range v {
		if id < 1 || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}
func setsOverlap(a, b []int64) bool {
	m := map[int64]bool{}
	for _, id := range a {
		m[id] = true
	}
	for _, id := range b {
		if m[id] {
			return true
		}
	}
	return false
}
func joinIDs(v []int64) string {
	parts := make([]string, len(v))
	for i, id := range v {
		parts[i] = strconv.FormatInt(id, 10)
	}
	return strings.Join(parts, ",")
}
func commandState(lines []customerport.TagCommandLine) string {
	if len(lines) == 0 {
		return "rejected"
	}
	allRejected := true
	for _, line := range lines {
		if line.State == "outcome_unknown" {
			return "outcome_unknown"
		}
		if line.State == "queued" {
			return "queued"
		}
		allRejected = allRejected && line.State == "rejected"
	}
	if allRejected {
		return "rejected"
	}
	return "partial"
}

var _ customerport.TagCommandSubmitter = (*TagCommandService)(nil)
var _ customerport.TagCommandPreviewer = (*TagCommandService)(nil)
