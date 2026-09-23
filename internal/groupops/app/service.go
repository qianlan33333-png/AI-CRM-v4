// Package app implements local-only Group Ops plan administration.
package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	groupopsdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/domain"
	groupopsport "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

const (
	DefaultLimit  int32 = 50
	MaximumLimit  int32 = 100
	MaximumOffset int32 = 1_000_000
)

var (
	ErrInvalid       = errors.New("invalid group ops command")
	ErrNotFound      = errors.New("group ops plan not found")
	ErrConflict      = errors.New("group ops command conflict")
	ErrStateConflict = errors.New("group ops plan state conflict")
	ErrUnavailable   = errors.New("group ops local storage unavailable")
)

type Reservation struct {
	ActorScope    string
	KeyDigest     [32]byte
	PayloadDigest [32]byte
	CreatedAt     time.Time
}

type Receipt struct {
	ID             int64
	Operation      string
	ActorScope     string
	KeyDigest      [32]byte
	PayloadDigest  [32]byte
	State          string
	ResultSnapshot json.RawMessage
}

// Store is deliberately local. It has no provider, River, webhook client, or
// runtime method. Save, receipt, and event append share the UnitOfWork passed
// to the service.
type Store interface {
	List(context.Context, int32, int32) ([]groupopsport.PlanListItem, error)
	Count(context.Context) (int64, error)
	Get(context.Context, int64) (groupopsport.Detail, error)
	Lock(context.Context, int64) (groupopsport.Detail, error)
	Create(context.Context, groupopsport.Plan) (int64, error)
	Save(context.Context, groupopsport.Detail) error
	Reserve(context.Context, string, Reservation) (Receipt, bool, error)
	Complete(context.Context, int64, json.RawMessage, time.Time) (Receipt, error)
}

type Service struct {
	uow    platformport.UnitOfWork
	store  Store
	staff  groupopsport.ActiveStaffReader
	events groupopsport.EventAppender
	now    func() time.Time
}

func NewService(uow platformport.UnitOfWork, store Store, staff groupopsport.ActiveStaffReader, events groupopsport.EventAppender) *Service {
	return &Service{uow: uow, store: store, staff: staff, events: events, now: time.Now}
}

func (s *Service) List(ctx context.Context, limit, offset int32) (groupopsport.PlanPage, error) {
	if !ready(s) || !validPage(limit, offset) {
		return groupopsport.PlanPage{}, invalidOrUnavailable(s)
	}
	result := groupopsport.PlanPage{Limit: limit, Offset: offset, Items: []groupopsport.PlanListItem{}, Safety: groupopsport.LocalSafety()}
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var err error
		result.Items, err = s.store.List(tx, limit, offset)
		if err == nil {
			result.Total, err = s.store.Count(tx)
		}
		return err
	})
	if err != nil {
		return groupopsport.PlanPage{}, classify(err)
	}
	if result.Total < 0 || len(result.Items) > int(limit) || int64(offset) > result.Total && len(result.Items) != 0 || !validPlanList(result.Items) {
		return groupopsport.PlanPage{}, ErrUnavailable
	}
	result.HasMore = int64(offset)+int64(len(result.Items)) < result.Total
	return clonePlanPage(result), nil
}

func (s *Service) Detail(ctx context.Context, planID int64) (groupopsport.Detail, error) {
	if !ready(s) {
		return groupopsport.Detail{}, ErrUnavailable
	}
	return s.readDetail(ctx, planID)
}

func (s *Service) Create(ctx context.Context, command groupopsport.CreatePlanCommand) (groupopsport.Detail, error) {
	if !ready(s) || !validCreate(command) {
		return groupopsport.Detail{}, invalidOrUnavailable(s)
	}
	now := s.nowUTC()
	if now.IsZero() {
		return groupopsport.Detail{}, ErrUnavailable
	}
	return s.withReceipt(ctx, "plan_create", command.Actor, command.IdempotencyKey, command, now, func(tx context.Context) (groupopsport.Detail, error) {
		id, err := s.store.Create(tx, groupopsport.Plan{Name: strings.TrimSpace(command.Name), Status: groupopsport.PlanDraft, Revision: 1, CreatedBy: command.Actor, UpdatedBy: command.Actor, CreatedAt: now, UpdatedAt: now})
		if err != nil || id < 1 {
			return groupopsport.Detail{}, err
		}
		return s.strictRead(tx, id)
	})
}

func (s *Service) Update(ctx context.Context, command groupopsport.UpdatePlanCommand) (groupopsport.Detail, error) {
	if !ready(s) || !validUpdate(command) {
		return groupopsport.Detail{}, invalidOrUnavailable(s)
	}
	if command.OwnerStaffIDSet && s.staff == nil {
		return groupopsport.Detail{}, ErrUnavailable
	}
	return s.mutate(ctx, "plan_update", command.PlanID, command.ExpectedRevision, command.Actor, command.IdempotencyKey, command, func(tx context.Context, detail *groupopsport.Detail, now time.Time) error {
		if command.OwnerStaffIDSet {
			active, err := s.staff.IsActiveStaff(tx, command.OwnerStaffID)
			if err != nil {
				return ErrUnavailable
			}
			if !active {
				return ErrInvalid
			}
			// The frozen standard form has one responsible operator. Save replaces
			// the owned member set together with the plan, receipt, and audit event
			// in this one Unit of Work; it never exposes an add/delete half-state.
			detail.Members = []groupopsport.Member{{StaffID: command.OwnerStaffID}}
		}
		detail.Plan.Name = strings.TrimSpace(command.Name)
		if command.PlanType != "" {
			detail.Plan.Type = command.PlanType
		}
		return nil
	})
}

func (s *Service) Activate(ctx context.Context, command groupopsport.TransitionCommand) (groupopsport.Detail, error) {
	// The frozen Group Ops list uses the same enable action for a draft and a
	// previously paused plan. Both transitions re-run the complete local
	// definition validation. Archived plans remain terminal.
	if !ready(s) || !validTransition(command) {
		return groupopsport.Detail{}, invalidOrUnavailable(s)
	}
	return s.mutate(ctx, "plan_activate", command.PlanID, command.ExpectedRevision, command.Actor, command.IdempotencyKey, command, func(_ context.Context, detail *groupopsport.Detail, _ time.Time) error {
		if detail.Plan.Status != groupopsport.PlanDraft && detail.Plan.Status != groupopsport.PlanPaused {
			return ErrStateConflict
		}
		if !contentValidation(*detail).Valid {
			return ErrStateConflict
		}
		detail.Plan.Status = groupopsport.PlanActive
		return nil
	})
}
func (s *Service) Pause(ctx context.Context, command groupopsport.TransitionCommand) (groupopsport.Detail, error) {
	return s.transition(ctx, "plan_pause", command, groupopsport.PlanActive, groupopsport.PlanPaused, false)
}
func (s *Service) Archive(ctx context.Context, command groupopsport.TransitionCommand) (groupopsport.Detail, error) {
	if !ready(s) || !validTransition(command) {
		return groupopsport.Detail{}, invalidOrUnavailable(s)
	}
	return s.mutate(ctx, "plan_archive", command.PlanID, command.ExpectedRevision, command.Actor, command.IdempotencyKey, command, func(_ context.Context, detail *groupopsport.Detail, _ time.Time) error {
		if detail.Plan.Status == groupopsport.PlanArchived {
			return ErrNotFound
		}
		detail.Plan.Status = groupopsport.PlanArchived
		return nil
	})
}

func (s *Service) transition(ctx context.Context, operation string, command groupopsport.TransitionCommand, from, to groupopsport.PlanStatus, requireValid bool) (groupopsport.Detail, error) {
	if !ready(s) || !validTransition(command) {
		return groupopsport.Detail{}, invalidOrUnavailable(s)
	}
	return s.mutate(ctx, operation, command.PlanID, command.ExpectedRevision, command.Actor, command.IdempotencyKey, command, func(_ context.Context, detail *groupopsport.Detail, _ time.Time) error {
		if detail.Plan.Status == groupopsport.PlanArchived {
			return ErrNotFound
		}
		if detail.Plan.Status != from {
			return ErrStateConflict
		}
		if requireValid && !contentValidation(*detail).Valid {
			return ErrStateConflict
		}
		detail.Plan.Status = to
		return nil
	})
}

func (s *Service) ListMembers(ctx context.Context, planID int64, limit, offset int32) (groupopsport.MemberPage, error) {
	if !ready(s) || !validPage(limit, offset) {
		return groupopsport.MemberPage{}, invalidOrUnavailable(s)
	}
	detail, err := s.readDetail(ctx, planID)
	if err != nil {
		return groupopsport.MemberPage{}, err
	}
	return memberPage(detail.Members, limit, offset), nil
}
func (s *Service) AddMember(ctx context.Context, command groupopsport.MemberCommand) (groupopsport.Detail, error) {
	if !ready(s) || s.staff == nil {
		return groupopsport.Detail{}, ErrUnavailable
	}
	if !validMemberCommand(command) {
		return groupopsport.Detail{}, invalidOrUnavailable(s)
	}
	return s.mutate(ctx, "member_add", command.PlanID, command.ExpectedRevision, command.Actor, command.IdempotencyKey, command, func(tx context.Context, detail *groupopsport.Detail, _ time.Time) error {
		active, err := s.staff.IsActiveStaff(tx, command.StaffID)
		if err != nil {
			return ErrUnavailable
		}
		if !active {
			return ErrInvalid
		}
		for _, member := range detail.Members {
			if member.StaffID == command.StaffID {
				return ErrConflict
			}
		}
		detail.Members = append(detail.Members, groupopsport.Member{StaffID: command.StaffID})
		sort.Slice(detail.Members, func(i, j int) bool { return detail.Members[i].StaffID < detail.Members[j].StaffID })
		return nil
	})
}
func (s *Service) RemoveMember(ctx context.Context, command groupopsport.MemberCommand) (groupopsport.Detail, error) {
	if !ready(s) || !validMemberCommand(command) {
		return groupopsport.Detail{}, invalidOrUnavailable(s)
	}
	return s.mutate(ctx, "member_remove", command.PlanID, command.ExpectedRevision, command.Actor, command.IdempotencyKey, command, func(_ context.Context, detail *groupopsport.Detail, _ time.Time) error {
		for i, member := range detail.Members {
			if member.StaffID == command.StaffID {
				detail.Members = append(detail.Members[:i], detail.Members[i+1:]...)
				return nil
			}
		}
		return ErrNotFound
	})
}

func (s *Service) ListGroupAssets(ctx context.Context, planID int64, limit, offset int32) (groupopsport.GroupAssetPage, error) {
	if !ready(s) || !validPage(limit, offset) {
		return groupopsport.GroupAssetPage{}, invalidOrUnavailable(s)
	}
	detail, err := s.readDetail(ctx, planID)
	if err != nil {
		return groupopsport.GroupAssetPage{}, err
	}
	return assetPage(detail.GroupAssets, limit, offset), nil
}
func (s *Service) AddGroupAsset(ctx context.Context, command groupopsport.GroupAssetCommand) (groupopsport.Detail, error) {
	if !ready(s) || !validAssetCommand(command) {
		return groupopsport.Detail{}, invalidOrUnavailable(s)
	}
	return s.mutate(ctx, "group_asset_add", command.PlanID, command.ExpectedRevision, command.Actor, command.IdempotencyKey, command, func(_ context.Context, detail *groupopsport.Detail, _ time.Time) error {
		for _, asset := range detail.GroupAssets {
			if asset.AssetRef == command.AssetRef {
				return ErrConflict
			}
		}
		detail.GroupAssets = append(detail.GroupAssets, groupopsport.GroupAsset{AssetRef: command.AssetRef})
		sort.Slice(detail.GroupAssets, func(i, j int) bool { return detail.GroupAssets[i].AssetRef < detail.GroupAssets[j].AssetRef })
		return nil
	})
}
func (s *Service) RemoveGroupAsset(ctx context.Context, command groupopsport.GroupAssetCommand) (groupopsport.Detail, error) {
	if !ready(s) || !validAssetCommand(command) {
		return groupopsport.Detail{}, invalidOrUnavailable(s)
	}
	return s.mutate(ctx, "group_asset_remove", command.PlanID, command.ExpectedRevision, command.Actor, command.IdempotencyKey, command, func(_ context.Context, detail *groupopsport.Detail, _ time.Time) error {
		for i, asset := range detail.GroupAssets {
			if asset.AssetRef == command.AssetRef {
				detail.GroupAssets = append(detail.GroupAssets[:i], detail.GroupAssets[i+1:]...)
				return nil
			}
		}
		return ErrNotFound
	})
}

func (s *Service) ListNodes(ctx context.Context, planID int64, limit, offset int32) (groupopsport.NodePage, error) {
	if !ready(s) || !validPage(limit, offset) {
		return groupopsport.NodePage{}, invalidOrUnavailable(s)
	}
	detail, err := s.readDetail(ctx, planID)
	if err != nil {
		return groupopsport.NodePage{}, err
	}
	return nodePage(detail.Nodes, limit, offset), nil
}
func (s *Service) AddNode(ctx context.Context, command groupopsport.NodeCreateCommand) (groupopsport.Detail, error) {
	if !ready(s) || !validNodeCreate(command) {
		return groupopsport.Detail{}, invalidOrUnavailable(s)
	}
	return s.mutate(ctx, "node_add", command.PlanID, command.ExpectedRevision, command.Actor, command.IdempotencyKey, command, func(_ context.Context, detail *groupopsport.Detail, _ time.Time) error {
		if command.Position > int32(len(detail.Nodes))+1 {
			return ErrConflict
		}
		node := nodeFromCreate(command)
		detail.Nodes = append(detail.Nodes, node)
		return normalizeNodes(detail.Nodes)
	})
}
func (s *Service) UpdateNode(ctx context.Context, command groupopsport.NodeUpdateCommand) (groupopsport.Detail, error) {
	if !ready(s) || !validNodeUpdate(command) {
		return groupopsport.Detail{}, invalidOrUnavailable(s)
	}
	return s.mutate(ctx, "node_update", command.PlanID, command.ExpectedRevision, command.Actor, command.IdempotencyKey, command, func(_ context.Context, detail *groupopsport.Detail, _ time.Time) error {
		for i := range detail.Nodes {
			if detail.Nodes[i].ID == command.NodeID {
				detail.Nodes[i] = groupopsport.Node{ID: command.NodeID, Position: command.Position, Kind: command.Kind, DayIndex: command.DayIndex, ScheduledTime: command.ScheduledTime, TriggerTimeLabel: command.TriggerTimeLabel, ActionTitle: strings.TrimSpace(command.ActionTitle), Status: command.Status, ScheduleSemantics: scheduleSemantics(command.DayIndex, command.ScheduledTime, command.TriggerTimeLabel, command.ActionTitle, command.Status), MessageText: strings.TrimSpace(command.MessageText), DelayMinutes: command.DelayMinutes, MaterialRef: command.MaterialRef, MaterialPlan: cloneMaterialPlan(command.MaterialPlan)}
				return normalizeNodes(detail.Nodes)
			}
		}
		return ErrNotFound
	})
}
func (s *Service) RemoveNode(ctx context.Context, command groupopsport.NodeDeleteCommand) (groupopsport.Detail, error) {
	if !ready(s) || !validNodeDelete(command) {
		return groupopsport.Detail{}, invalidOrUnavailable(s)
	}
	return s.mutate(ctx, "node_remove", command.PlanID, command.ExpectedRevision, command.Actor, command.IdempotencyKey, command, func(_ context.Context, detail *groupopsport.Detail, _ time.Time) error {
		for i, node := range detail.Nodes {
			if node.ID == command.NodeID {
				detail.Nodes = append(detail.Nodes[:i], detail.Nodes[i+1:]...)
				return normalizeNodes(detail.Nodes)
			}
		}
		return ErrNotFound
	})
}

func (s *Service) GetWebhookDescriptor(ctx context.Context, planID int64) (groupopsport.WebhookDescriptor, error) {
	detail, err := s.readDetail(ctx, planID)
	if err != nil {
		return groupopsport.WebhookDescriptor{}, err
	}
	return detail.WebhookDescriptor, nil
}
func (s *Service) PutWebhookDescriptor(ctx context.Context, command groupopsport.WebhookDescriptorCommand) (groupopsport.Detail, error) {
	if !ready(s) || !validWebhookCommand(command) {
		return groupopsport.Detail{}, invalidOrUnavailable(s)
	}
	return s.mutate(ctx, "webhook_descriptor_put", command.PlanID, command.ExpectedRevision, command.Actor, command.IdempotencyKey, command, func(_ context.Context, detail *groupopsport.Detail, _ time.Time) error {
		detail.WebhookDescriptor = descriptor(command.Reference)
		return nil
	})
}

func (s *Service) Preview(ctx context.Context, planID int64) (groupopsport.ContentValidation, error) {
	detail, err := s.readDetail(ctx, planID)
	if err != nil {
		return groupopsport.ContentValidation{}, err
	}
	// The donor detail page always reads content preview, including for active,
	// paused, and archived plans. This is read-only; lifecycle transitions still
	// enforce their own state and CAS rules.
	return contentValidation(detail), nil
}

func (s *Service) readDetail(ctx context.Context, planID int64) (groupopsport.Detail, error) {
	if planID < 1 {
		return groupopsport.Detail{}, ErrNotFound
	}
	var detail groupopsport.Detail
	err := s.uow.Within(ctx, func(tx context.Context) error { var err error; detail, err = s.store.Get(tx, planID); return err })
	if err != nil {
		return groupopsport.Detail{}, classify(err)
	}
	if !validDetail(detail, planID) {
		return groupopsport.Detail{}, ErrUnavailable
	}
	return cloneDetail(detail), nil
}

func (s *Service) mutate(ctx context.Context, operation string, planID, expectedRevision, actor int64, key string, payload any, change func(context.Context, *groupopsport.Detail, time.Time) error) (groupopsport.Detail, error) {
	if planID < 1 || expectedRevision < 1 || actor < 1 || !validKey(key) || change == nil {
		return groupopsport.Detail{}, ErrInvalid
	}
	now := s.nowUTC()
	if now.IsZero() {
		return groupopsport.Detail{}, ErrUnavailable
	}
	return s.withReceipt(ctx, operation, actor, key, payload, now, func(tx context.Context) (groupopsport.Detail, error) {
		detail, err := s.store.Lock(tx, planID)
		if err != nil {
			return groupopsport.Detail{}, err
		}
		if !validDetail(detail, planID) {
			return groupopsport.Detail{}, ErrUnavailable
		}
		if detail.Plan.Status == groupopsport.PlanArchived {
			return groupopsport.Detail{}, ErrNotFound
		}
		if detail.Plan.Revision != expectedRevision {
			return groupopsport.Detail{}, ErrConflict
		}
		// A paused plan can repair its basic definition without activating it.
		// Keep other configuration mutations draft-only and retain revision fences
		// so previously accepted executions cannot use the changed definition.
		if detail.Plan.Status != groupopsport.PlanDraft && operation != "plan_pause" && operation != "plan_archive" && !((operation == "plan_activate" || operation == "plan_update" || operation == "webhook_descriptor_put") && detail.Plan.Status == groupopsport.PlanPaused) {
			return groupopsport.Detail{}, ErrStateConflict
		}
		if err := change(tx, &detail, now); err != nil {
			return groupopsport.Detail{}, err
		}
		detail.Plan.Revision++
		detail.Plan.UpdatedBy = actor
		detail.Plan.UpdatedAt = now
		detail.Safety = groupopsport.LocalSafety()
		if err := s.store.Save(tx, detail); err != nil {
			return groupopsport.Detail{}, err
		}
		readback, err := s.strictRead(tx, planID)
		if err != nil || !sameSavedDetail(detail, readback) {
			return groupopsport.Detail{}, ErrUnavailable
		}
		return readback, nil
	})
}

func (s *Service) withReceipt(ctx context.Context, operation string, actor int64, key string, payload any, now time.Time, write func(context.Context) (groupopsport.Detail, error)) (result groupopsport.Detail, err error) {
	digest, err := digest(payload)
	if err != nil {
		return result, ErrInvalid
	}
	reservation := Reservation{ActorScope: fmt.Sprintf("admin:%d", actor), KeyDigest: sha256.Sum256([]byte(key)), PayloadDigest: digest, CreatedAt: now}
	err = s.uow.Within(ctx, func(tx context.Context) error {
		receipt, owned, reserveErr := s.store.Reserve(tx, operation, reservation)
		if reserveErr != nil {
			return reserveErr
		}
		if !receiptMatches(receipt, operation, reservation) {
			return ErrUnavailable
		}
		if subtle.ConstantTimeCompare(receipt.PayloadDigest[:], digest[:]) != 1 {
			return ErrConflict
		}
		if !owned {
			if receipt.State != "completed" || !decode(receipt.ResultSnapshot, &result) || !validDetail(result, result.Plan.ID) {
				return ErrUnavailable
			}
			return nil
		}
		result, reserveErr = write(tx)
		if reserveErr != nil {
			return reserveErr
		}
		if !validDetail(result, result.Plan.ID) {
			return ErrUnavailable
		}
		payload, marshalErr := json.Marshal(map[string]any{"plan_id": result.Plan.ID, "operation": operation, "actor": actor})
		if marshalErr != nil {
			return marshalErr
		}
		if _, eventErr := s.events.Append(tx, groupopsport.Event{Type: groupopsport.EvGroupOpsPlanUpdated, Payload: payload, OccurredAt: now, IdempotencyKey: key}); eventErr != nil {
			return eventErr
		}
		snapshot, marshalErr := json.Marshal(result)
		if marshalErr != nil {
			return marshalErr
		}
		completed, completeErr := s.store.Complete(tx, receipt.ID, snapshot, now)
		if completeErr != nil || !receiptMatches(completed, operation, reservation) || completed.State != "completed" || !jsonEqual(completed.ResultSnapshot, snapshot) {
			return ErrUnavailable
		}
		return nil
	})
	if err != nil {
		return groupopsport.Detail{}, classify(err)
	}
	return cloneDetail(result), nil
}

func (s *Service) strictRead(ctx context.Context, id int64) (groupopsport.Detail, error) {
	detail, err := s.store.Get(ctx, id)
	if err != nil {
		return groupopsport.Detail{}, err
	}
	if !validDetail(detail, id) {
		return groupopsport.Detail{}, ErrUnavailable
	}
	return detail, nil
}

func contentValidation(detail groupopsport.Detail) groupopsport.ContentValidation {
	result := groupopsport.ContentValidation{IssueCodes: []string{}, PreviewLines: []string{}, NodeCount: int32(len(detail.Nodes)), GroupAssetCount: int32(len(detail.GroupAssets)), Safety: groupopsport.LocalSafety()}
	if len(detail.GroupAssets) == 0 {
		result.IssueCodes = append(result.IssueCodes, "group_asset_required")
	}
	if len(detail.Members) == 0 {
		result.IssueCodes = append(result.IssueCodes, "member_required")
	}
	if detail.Plan.Type == groupopsport.PlanTypeWebhook {
		// Webhook requests bring their own immutable content and must select a
		// subset of these bound groups at acceptance. The existing responsible
		// operator requirement stays in force until sender qualification has a
		// separately reviewed product rule; only the node requirement is waived.
		if !detail.WebhookDescriptor.Configured {
			result.IssueCodes = append(result.IssueCodes, "webhook_descriptor_required")
		}
	} else {
		if len(detail.Nodes) == 0 {
			result.IssueCodes = append(result.IssueCodes, "node_required")
		}
	}
	// Nodes are not execution inputs for a Webhook plan. Keep structurally
	// valid legacy nodes readable, but do not let their retired schedule or
	// free-form material rules block an otherwise valid dynamic plan.
	if detail.Plan.Type != groupopsport.PlanTypeWebhook {
		for _, node := range detail.Nodes {
			if node.MaterialRef != "" {
				result.IssueCodes = append(result.IssueCodes, "legacy_material_reference_unsupported")
			}
			switch node.Kind {
			case groupopsport.NodeMessage:
				if node.MessageText != "" {
					result.PreviewLines = append(result.PreviewLines, "message: "+node.MessageText)
				} else {
					result.PreviewLines = append(result.PreviewLines, fmt.Sprintf("message: %d materials", len(node.MaterialPlan.References)))
				}
			case groupopsport.NodeDelay:
				result.PreviewLines = append(result.PreviewLines, fmt.Sprintf("delay: %d minutes", node.DelayMinutes))
			default:
				result.IssueCodes = append(result.IssueCodes, "invalid_node")
			}
		}
	}
	result.Valid = len(result.IssueCodes) == 0
	return result
}

func descriptor(reference string) groupopsport.WebhookDescriptor {
	if reference == "" {
		return groupopsport.WebhookDescriptor{Description: "not configured"}
	}
	path := strings.Replace(groupopsport.WebhookPathTemplate, "{webhook_key}", url.PathEscape(reference), 1)
	return groupopsport.WebhookDescriptor{
		Configured: true, Reference: reference, Path: path, URL: path,
		SignatureAlgorithm: groupopsport.WebhookSignatureAlgorithm,
		SignatureHeader:    groupopsport.WebhookSignatureHeader,
		TimestampHeader:    groupopsport.WebhookTimestampHeader,
		NonceHeader:        groupopsport.WebhookNonceHeader,
		ClientIDHeader:     groupopsport.WebhookClientIDHeader,
		ClientID:           groupopsport.WebhookClientID,
		Description:        "same-origin webhook endpoint; signing credentials are withheld",
	}
}

// WebhookDescriptor projects the persisted opaque reference into the public,
// non-secret integration contract used by the repository read path.
func WebhookDescriptor(reference string) groupopsport.WebhookDescriptor { return descriptor(reference) }

func memberPage(items []groupopsport.Member, limit, offset int32) groupopsport.MemberPage {
	result := groupopsport.MemberPage{Items: page(items, limit, offset), Total: int64(len(items)), Limit: limit, Offset: offset, Safety: groupopsport.LocalSafety()}
	result.HasMore = int64(offset)+int64(len(result.Items)) < result.Total
	return result
}
func assetPage(items []groupopsport.GroupAsset, limit, offset int32) groupopsport.GroupAssetPage {
	result := groupopsport.GroupAssetPage{Items: page(items, limit, offset), Total: int64(len(items)), Limit: limit, Offset: offset, Safety: groupopsport.LocalSafety()}
	result.HasMore = int64(offset)+int64(len(result.Items)) < result.Total
	return result
}
func nodePage(items []groupopsport.Node, limit, offset int32) groupopsport.NodePage {
	result := groupopsport.NodePage{Items: page(items, limit, offset), Total: int64(len(items)), Limit: limit, Offset: offset, Safety: groupopsport.LocalSafety()}
	result.HasMore = int64(offset)+int64(len(result.Items)) < result.Total
	return result
}
func page[T any](items []T, limit, offset int32) []T {
	if int(offset) >= len(items) {
		return []T{}
	}
	end := int(offset + limit)
	if end > len(items) {
		end = len(items)
	}
	return append([]T{}, items[offset:end]...)
}

func normalizeNodes(nodes []groupopsport.Node) error {
	sort.SliceStable(nodes, func(i, j int) bool { return nodes[i].Position < nodes[j].Position })
	for index := range nodes {
		nodes[index].Position = int32(index + 1)
	}
	return nil
}
func nodeFromCreate(c groupopsport.NodeCreateCommand) groupopsport.Node {
	return groupopsport.Node{Position: c.Position, Kind: c.Kind, DayIndex: c.DayIndex, ScheduledTime: c.ScheduledTime, TriggerTimeLabel: c.TriggerTimeLabel, ActionTitle: strings.TrimSpace(c.ActionTitle), Status: c.Status, ScheduleSemantics: scheduleSemantics(c.DayIndex, c.ScheduledTime, c.TriggerTimeLabel, c.ActionTitle, c.Status), MessageText: strings.TrimSpace(c.MessageText), DelayMinutes: c.DelayMinutes, MaterialRef: c.MaterialRef, MaterialPlan: cloneMaterialPlan(c.MaterialPlan)}
}
func validCreate(c groupopsport.CreatePlanCommand) bool {
	return c.Actor > 0 && validKey(c.IdempotencyKey) && validName(c.Name)
}
func validUpdate(c groupopsport.UpdatePlanCommand) bool {
	return c.PlanID > 0 && c.ExpectedRevision > 0 && c.Actor > 0 && validKey(c.IdempotencyKey) && validName(c.Name) && (!c.OwnerStaffIDSet || c.OwnerStaffID > 0) && (c.PlanType == "" || c.PlanType == groupopsport.PlanTypeStandard || c.PlanType == groupopsport.PlanTypeWebhook)
}
func validTransition(c groupopsport.TransitionCommand) bool {
	return c.PlanID > 0 && c.ExpectedRevision > 0 && c.Actor > 0 && validKey(c.IdempotencyKey)
}
func validMemberCommand(c groupopsport.MemberCommand) bool {
	return validTransition(groupopsport.TransitionCommand{PlanID: c.PlanID, ExpectedRevision: c.ExpectedRevision, Actor: c.Actor, IdempotencyKey: c.IdempotencyKey}) && c.StaffID > 0
}
func validAssetCommand(c groupopsport.GroupAssetCommand) bool {
	return validTransition(groupopsport.TransitionCommand{PlanID: c.PlanID, ExpectedRevision: c.ExpectedRevision, Actor: c.Actor, IdempotencyKey: c.IdempotencyKey}) && opaque(c.AssetRef)
}
func validNodeCreate(c groupopsport.NodeCreateCommand) bool {
	return validTransition(groupopsport.TransitionCommand{PlanID: c.PlanID, ExpectedRevision: c.ExpectedRevision, Actor: c.Actor, IdempotencyKey: c.IdempotencyKey}) && c.Position > 0 && validNode(c.Kind, c.MessageText, c.DelayMinutes, c.MaterialRef, c.MaterialPlan) && validNodeSchedule(c.DayIndex, c.ScheduledTime, c.TriggerTimeLabel, c.ActionTitle, c.Status)
}
func validNodeUpdate(c groupopsport.NodeUpdateCommand) bool {
	return c.NodeID > 0 && validNodeCreate(groupopsport.NodeCreateCommand{PlanID: c.PlanID, ExpectedRevision: c.ExpectedRevision, Position: c.Position, Kind: c.Kind, DayIndex: c.DayIndex, ScheduledTime: c.ScheduledTime, TriggerTimeLabel: c.TriggerTimeLabel, ActionTitle: c.ActionTitle, Status: c.Status, MessageText: c.MessageText, DelayMinutes: c.DelayMinutes, MaterialRef: c.MaterialRef, MaterialPlan: c.MaterialPlan, Actor: c.Actor, IdempotencyKey: c.IdempotencyKey})
}
func validNodeDelete(c groupopsport.NodeDeleteCommand) bool {
	return c.NodeID > 0 && validTransition(groupopsport.TransitionCommand{PlanID: c.PlanID, ExpectedRevision: c.ExpectedRevision, Actor: c.Actor, IdempotencyKey: c.IdempotencyKey})
}
func validWebhookCommand(c groupopsport.WebhookDescriptorCommand) bool {
	return validTransition(groupopsport.TransitionCommand{PlanID: c.PlanID, ExpectedRevision: c.ExpectedRevision, Actor: c.Actor, IdempotencyKey: c.IdempotencyKey}) && (c.Reference == "" || opaque(c.Reference) && !sensitiveReference(c.Reference))
}
func validNode(kind groupopsport.NodeKind, message string, delay int32, material string, plan groupopsport.MaterialPlan) bool {
	if material != "" && !opaque(material) {
		return false
	}
	if kind == groupopsport.NodeMessage {
		return delay == 0 && validMaterialPlan(plan) && (message == "" || validMessage(message)) && (message != "" || len(plan.References) != 0)
	}
	return kind == groupopsport.NodeDelay && strings.TrimSpace(message) == "" && material == "" && len(plan.References) == 0 && delay >= 1 && delay <= 10080
}

// validNodeSchedule preserves persisted legacy delay nodes, whose schedule
// fields were absent before migration 0101, while requiring every V3 standard
// action to carry an explicit day/time/title/status tuple. The public HTTP
// adapter only accepts the latter for new standard actions.
func validNodeSchedule(day int32, scheduled, trigger, title, status string) bool {
	if day == 0 && scheduled == "" && trigger == "" && title == "" && status == "" {
		return true
	}
	if day < 1 || day > 3650 || !validScheduledTime(scheduled) || trigger != scheduled || !validNodeStatus(status) {
		return false
	}
	return title == "" || validText(title, 200)
}

func scheduleSemantics(day int32, scheduled, trigger, title, status string) string {
	if day == 0 && scheduled == "" && trigger == "" && title == "" && status == "" {
		return "relative_delay"
	}
	return "calendar"
}

func validScheduledTime(value string) bool {
	if len(value) != 5 || value[2] != ':' || value[0] < '0' || value[0] > '2' || value[1] < '0' || value[1] > '9' || (value[3] != '0' && value[3] != '3') || value[4] != '0' {
		return false
	}
	hour := int(value[0]-'0')*10 + int(value[1]-'0')
	return hour >= 8 && hour <= 23
}

func validNodeStatus(value string) bool {
	return value == "active" || value == "draft" || value == "disabled"
}

func validMaterialPlan(plan groupopsport.MaterialPlan) bool {
	return groupopsdomain.ValidateMaterialPlan(plan) == nil
}

func cloneMaterialPlan(plan groupopsport.MaterialPlan) groupopsport.MaterialPlan {
	return groupopsport.MaterialPlan{References: append([]groupopsport.MaterialReference{}, plan.References...)}
}
func validName(value string) bool    { return validText(value, 128) }
func validMessage(value string) bool { return validText(value, 1000) }
func validText(value string, max int) bool {
	return utf8.ValidString(value) && strings.TrimSpace(value) == value && value != "" && utf8.RuneCountInString(value) <= max
}
func opaque(value string) bool {
	if !validText(value, 128) || strings.Contains(value, "://") {
		return false
	}
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_.:", r) {
			continue
		}
		return false
	}
	return true
}
func sensitiveReference(value string) bool {
	lower := strings.ToLower(value)
	return strings.Contains(lower, "secret") || strings.Contains(lower, "token") || strings.Contains(lower, "password") || strings.Contains(lower, "api_key")
}
func validKey(value string) bool { return validText(value, 128) && utf8.RuneCountInString(value) >= 16 }
func validPage(limit, offset int32) bool {
	return limit >= 1 && limit <= MaximumLimit && offset >= 0 && offset <= MaximumOffset
}
func validPlan(value groupopsport.Plan) bool {
	return groupopsdomain.ValidatePlan(value) == nil
}
func validPlanList(items []groupopsport.PlanListItem) bool {
	for i, item := range items {
		if !validPlan(item.Plan) || item.QueueCount < 0 || item.BoundGroupCount < 0 {
			return false
		}
		if i > 0 && (items[i-1].UpdatedAt.Before(item.UpdatedAt) || items[i-1].UpdatedAt.Equal(item.UpdatedAt) && items[i-1].ID <= item.ID) {
			return false
		}
	}
	return true
}
func validDetail(value groupopsport.Detail, id int64) bool {
	if value.Plan.ID != id || !validPlan(value.Plan) || value.ProviderExecutionEligible || value.RealExternalCallExecuted || !validMembers(value.Members) || !validAssets(value.GroupAssets) || !validNodes(value.Nodes) {
		return false
	}
	return value.WebhookDescriptor == descriptor(value.WebhookDescriptor.Reference) && (value.WebhookDescriptor.Reference == "" || !sensitiveReference(value.WebhookDescriptor.Reference))
}
func validMembers(items []groupopsport.Member) bool {
	for i, item := range items {
		if item.StaffID < 1 || i > 0 && items[i-1].StaffID >= item.StaffID {
			return false
		}
	}
	return items != nil
}
func validAssets(items []groupopsport.GroupAsset) bool {
	for i, item := range items {
		if item.ID < 1 || !opaque(item.AssetRef) || i > 0 && (items[i-1].AssetRef > item.AssetRef || items[i-1].AssetRef == item.AssetRef && items[i-1].ID >= item.ID) {
			return false
		}
	}
	return items != nil
}
func validNodes(items []groupopsport.Node) bool {
	for i, item := range items {
		if item.ID < 1 || item.Position != int32(i+1) || !validNode(item.Kind, item.MessageText, item.DelayMinutes, item.MaterialRef, item.MaterialPlan) || !validNodeSchedule(item.DayIndex, item.ScheduledTime, item.TriggerTimeLabel, item.ActionTitle, item.Status) {
			return false
		}
	}
	return items != nil
}
func sameSavedDetail(want, got groupopsport.Detail) bool {
	if !samePlan(want.Plan, got.Plan) || want.WebhookDescriptor != got.WebhookDescriptor || want.Safety != got.Safety || len(want.Members) != len(got.Members) || len(want.GroupAssets) != len(got.GroupAssets) || len(want.Nodes) != len(got.Nodes) {
		return false
	}
	for index := range want.Members {
		if want.Members[index] != got.Members[index] {
			return false
		}
	}
	for index := range want.GroupAssets {
		if got.GroupAssets[index].ID < 1 || want.GroupAssets[index].ID != 0 && want.GroupAssets[index].ID != got.GroupAssets[index].ID || want.GroupAssets[index].AssetRef != got.GroupAssets[index].AssetRef {
			return false
		}
	}
	for index := range want.Nodes {
		if got.Nodes[index].ID < 1 || want.Nodes[index].ID != 0 && want.Nodes[index].ID != got.Nodes[index].ID || want.Nodes[index].Position != got.Nodes[index].Position || want.Nodes[index].Kind != got.Nodes[index].Kind || !sameNodeSchedule(want.Nodes[index], got.Nodes[index]) || want.Nodes[index].MessageText != got.Nodes[index].MessageText || want.Nodes[index].DelayMinutes != got.Nodes[index].DelayMinutes || want.Nodes[index].MaterialRef != got.Nodes[index].MaterialRef || !reflect.DeepEqual(want.Nodes[index].MaterialPlan, got.Nodes[index].MaterialPlan) {
			return false
		}
	}
	return true
}
func sameNodeSchedule(want, got groupopsport.Node) bool {
	// Migration defaults make old in-memory commands observable as the
	// documented legacy 1/20:00 active tuple after their first PostgreSQL save.
	if want.DayIndex == 0 && want.ScheduledTime == "" && want.TriggerTimeLabel == "" && want.ActionTitle == "" && want.Status == "" {
		return (got.DayIndex == 0 && got.ScheduledTime == "" && got.TriggerTimeLabel == "" && got.ActionTitle == "" && got.Status == "") || (got.DayIndex == 1 && got.ScheduledTime == "20:00" && got.TriggerTimeLabel == "20:00" && got.ActionTitle == "" && got.Status == "active" && got.ScheduleSemantics == "relative_delay")
	}
	return want.DayIndex == got.DayIndex && want.ScheduledTime == got.ScheduledTime && want.TriggerTimeLabel == got.TriggerTimeLabel && want.ActionTitle == got.ActionTitle && want.Status == got.Status && want.ScheduleSemantics == got.ScheduleSemantics
}

func samePlan(want, got groupopsport.Plan) bool {
	return want.ID == got.ID && want.Type == got.Type && want.Name == got.Name && want.Status == got.Status && want.Revision == got.Revision && want.CreatedBy == got.CreatedBy && want.UpdatedBy == got.UpdatedBy && want.CreatedAt.Equal(got.CreatedAt) && want.UpdatedAt.Equal(got.UpdatedAt)
}
func receiptMatches(value Receipt, operation string, reservation Reservation) bool {
	return value.ID > 0 && value.Operation == operation && value.ActorScope == reservation.ActorScope && subtle.ConstantTimeCompare(value.KeyDigest[:], reservation.KeyDigest[:]) == 1 && (value.State == "in_progress" || value.State == "completed")
}
func digest(value any) ([32]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(raw), nil
}
func decode(raw json.RawMessage, to any) bool {
	if len(raw) == 0 || to == nil {
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if decoder.Decode(to) != nil {
		return false
	}
	var extra any
	return errors.Is(decoder.Decode(&extra), io.EOF)
}
func jsonEqual(left, right json.RawMessage) bool {
	var a, b any
	return decode(left, &a) && decode(right, &b) && fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
}
func (s *Service) nowUTC() time.Time {
	if s == nil || s.now == nil {
		return time.Time{}
	}
	// PostgreSQL timestamptz stores microseconds. Normalizing before a strict
	// write/read comparison makes the persisted fact the exact service fact.
	return s.now().UTC().Truncate(time.Microsecond)
}
func ready(s *Service) bool {
	return s != nil && s.uow != nil && s.store != nil && s.events != nil && s.now != nil
}
func invalidOrUnavailable(s *Service) error {
	if !ready(s) {
		return ErrUnavailable
	}
	return ErrInvalid
}
func classify(err error) error {
	switch {
	case errors.Is(err, ErrInvalid), errors.Is(err, ErrNotFound), errors.Is(err, ErrConflict), errors.Is(err, ErrStateConflict),
		errors.Is(err, ErrProviderDisabled), errors.Is(err, ErrMiniProgramCoverUnsupported), errors.Is(err, ErrMiniProgramCoverResolverUnavailable):
		return err
	default:
		// Preserve the owner-side cause for operators and integration journeys;
		// HTTP still maps errors.Is(ErrUnavailable) to its stable public code.
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
}
func clonePlanPage(value groupopsport.PlanPage) groupopsport.PlanPage {
	value.Items = append([]groupopsport.PlanListItem{}, value.Items...)
	return value
}
func cloneDetail(value groupopsport.Detail) groupopsport.Detail {
	value.Members = append([]groupopsport.Member{}, value.Members...)
	value.GroupAssets = append([]groupopsport.GroupAsset{}, value.GroupAssets...)
	value.Nodes = append([]groupopsport.Node{}, value.Nodes...)
	for index := range value.Nodes {
		value.Nodes[index].MaterialPlan = cloneMaterialPlan(value.Nodes[index].MaterialPlan)
	}
	return value
}
