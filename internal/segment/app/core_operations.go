package app

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	segmentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/domain"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
	segmentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/store"
)

type CoreOperationsStore interface {
	PublishCoreAssignments(context.Context, int64, string, time.Time) error
	CoreProducts(context.Context) ([]segmentport.CoreProduct, error)
	PutCoreProduct(context.Context, segmentport.CoreProduct, int64) (segmentport.CoreProduct, error)
	CorePrompt(context.Context) (segmentport.CorePrompt, error)
	SaveCorePrompt(context.Context, string, int64, int64, bool, time.Time) (segmentport.CorePrompt, error)
	CorePromptHistory(context.Context) ([]segmentport.CorePromptVersion, error)
	CoreAssignment(context.Context, int64) (segmentport.CoreAssignment, error)
	ChangeCoreAssignment(context.Context, segmentport.CoreAssignment, int64, bool) (segmentport.CoreAssignment, error)
	RecordCorePush(context.Context, segmentport.CorePush, time.Time) (segmentport.CorePush, error)
	CoreMemberDetail(context.Context, int64, int64, int64, int) (segmentport.CoreMemberDetail, error)
}
type CoreOperations struct {
	reevaluate interface {
		ReevaluateWithin(context.Context, int64, string) error
	}
	effects       effectport.TransactionalAccepter
	contextReader automationport.GenerationContextReader
	policy        automationport.GenerationModelPolicyReader
	service       *Service
	store         CoreOperationsStore
	canonical     segmentport.CanonicalCustomerResolver
	refresh       AtomicRefreshRequester
}

func NewCoreOperations(s *Service, store CoreOperationsStore, canonical segmentport.CanonicalCustomerResolver, refresh AtomicRefreshRequester) *CoreOperations {
	return &CoreOperations{service: s, store: store, canonical: canonical, refresh: refresh}
}

type CoreProductCommand struct {
	Product         segmentport.CoreProduct `json:"product"`
	ExpectedVersion int64                   `json:"expected_version"`
	Actor           int64                   `json:"-"`
	IdempotencyKey  string                  `json:"-"`
}
type CorePromptCommand struct {
	Body            string `json:"body"`
	ExpectedVersion int64  `json:"expected_version"`
	Publish         bool   `json:"publish"`
	Actor           int64  `json:"-"`
	IdempotencyKey  string `json:"-"`
}
type CoreAssignmentCommand struct {
	CustomerID           int64  `json:"customer_id"`
	CoreProductID        int64  `json:"core_product_id"`
	ExpectedAssignmentID int64  `json:"expected_assignment_id"`
	Reason               string `json:"reason"`
	Purchase             bool   `json:"purchase"`
	Actor                int64  `json:"-"`
	IdempotencyKey       string `json:"-"`
}
type CorePushCommand struct {
	Push           segmentport.CorePush      `json:"push"`
	Actor          segmentport.MutationActor `json:"-"`
	IdempotencyKey string                    `json:"-"`
}

func validCoreText(s string, max int, required bool) bool {
	return utf8.ValidString(s) && utf8.RuneCountInString(s) <= max && (!required || strings.TrimSpace(s) != "") && !strings.ContainsRune(s, 0)
}
func ValidateCoreProduct(p segmentport.CoreProduct) error {
	if p.ID < 1 || p.ID > 5 || p.PackageID < 1 || !validCoreText(p.Name, 120, true) || !validCoreText(p.Description, 16000, true) || !validCoreText(p.AIContext, 16000, false) || !validCoreText(p.ProductReference, 120, false) {
		return ErrInvalid
	}
	return nil
}
func ValidateCorePush(p segmentport.CorePush, now time.Time) error {
	if p.CustomerID < 1 || p.PackageID < 1 || p.StatusVersion < 1 || !validCoreText(p.Source, 120, true) || !validCoreText(p.PushID, 128, true) || p.OccurredAt.IsZero() || p.OccurredAt.After(now.Add(5*time.Minute)) || len(p.Materials) < 1 || len(p.Materials) > 20 {
		return ErrInvalid
	}
	switch p.Status {
	case "reported", "success", "failed", "unknown":
	default:
		return ErrInvalid
	}
	seen := map[segmentport.CoreMaterialRef]bool{}
	for _, m := range p.Materials {
		if m.ID < 1 || seen[m] {
			return ErrInvalid
		}
		switch m.Kind {
		case "image", "miniprogram", "link", "attachment":
		default:
			return ErrInvalid
		}
		seen[m] = true
	}
	return nil
}
func (c *CoreOperations) ready() bool {
	return c != nil && c.service != nil && c.service.ready() && c.store != nil
}
func (c *CoreOperations) mutate(ctx context.Context, op string, actor segmentport.MutationActor, key string, input any, apply func(context.Context) (any, int64, error)) (json.RawMessage, error) {
	if !c.ready() {
		return nil, ErrNotReady
	}
	payload := mutationPayload(op, actor, input)
	out, e := c.service.mutate(ctx, op, actor, key, payload, func(tx context.Context) (any, segmentstore.MutationFact, error) {
		v, id, e := apply(tx)
		fact := segmentstore.MutationFact{ResourceKind: "core_operations", ResourceID: id, Operation: op, EventType: "audience.core." + op + ".v1", ActorID: actor.StaffID, ActorKind: string(actor.Kind), ActorRef: actor.Reference, Payload: json.RawMessage(`{"changed":true}`), IdempotencyKey: key, OccurredAt: c.service.now().UTC()}
		return v, fact, e
	})
	return out, classify(e)
}
func (c *CoreOperations) Products(ctx context.Context) (out []segmentport.CoreProduct, e error) {
	if !c.ready() {
		return nil, ErrNotReady
	}
	e = c.service.uow.Within(ctx, func(tx context.Context) error { out, e = c.store.CoreProducts(tx); return e })
	return out, classify(e)
}
func (c *CoreOperations) PutProduct(ctx context.Context, in CoreProductCommand) (out segmentport.CoreProduct, e error) {
	if e = ValidateCoreProduct(in.Product); e != nil || in.ExpectedVersion < 0 {
		return out, ErrInvalid
	}
	actor, e := mutationActor(in.Actor, segmentport.MutationActor{})
	if e != nil {
		return out, e
	}
	raw, e := c.mutate(ctx, "product_saved", actor, in.IdempotencyKey, in, func(tx context.Context) (any, int64, error) {
		in.Product.UpdatedAt = c.service.now().UTC()
		p, e := c.store.PutCoreProduct(tx, in.Product, in.ExpectedVersion)
		if errors.Is(e, segmentstore.ErrNotFound) {
			e = ErrConflict
		}
		if e == nil && in.ExpectedVersion == 0 {
			pkg, lockErr := c.service.store.LockPackage(tx, p.PackageID)
			if lockErr != nil {
				return p, p.ID, lockErr
			}
			definition, _ := json.Marshal(map[string]any{"schema_version": 1, "template_key": "core_ai_product", "parameters": map[string]any{"core_product_id": p.ID}})
			next, err := c.service.store.NextConfigurationVersion(tx, p.PackageID)
			if err != nil {
				return p, p.ID, err
			}
			cfg, err := segmentdomain.NewConfigurationVersion(p.PackageID, next, definition, "", "manual", in.Actor, p.UpdatedAt)
			if err != nil {
				return p, p.ID, err
			}
			cfg, err = c.service.store.CreateConfigurationVersion(tx, cfg)
			if err != nil {
				return p, p.ID, err
			}
			if _, err = c.service.setCurrentConfiguration(tx, p.PackageID, cfg.ID, pkg.Version, actor, p.UpdatedAt); err != nil {
				return p, p.ID, err
			}
			e = c.store.PublishCoreAssignments(tx, in.Actor, in.IdempotencyKey, p.UpdatedAt)
		}
		return p, p.ID, e
	})
	if e == nil {
		e = json.Unmarshal(raw, &out)
	}
	return
}
func (c *CoreOperations) Prompt(ctx context.Context) (out segmentport.CorePrompt, e error) {
	if !c.ready() {
		return out, ErrNotReady
	}
	e = c.service.uow.Within(ctx, func(tx context.Context) error { out, e = c.store.CorePrompt(tx); return e })
	return out, classify(e)
}
func (c *CoreOperations) PromptHistory(ctx context.Context) (out []segmentport.CorePromptVersion, e error) {
	if !c.ready() {
		return nil, ErrNotReady
	}
	e = c.service.uow.Within(ctx, func(tx context.Context) error { out, e = c.store.CorePromptHistory(tx); return e })
	return out, classify(e)
}
func (c *CoreOperations) SavePrompt(ctx context.Context, in CorePromptCommand) (out segmentport.CorePrompt, e error) {
	if !validCoreText(in.Body, 16000, in.Publish) || in.ExpectedVersion < 1 {
		return out, ErrInvalid
	}
	actor, e := mutationActor(in.Actor, segmentport.MutationActor{})
	if e != nil {
		return out, e
	}
	raw, e := c.mutate(ctx, "prompt_saved", actor, in.IdempotencyKey, in, func(tx context.Context) (any, int64, error) {
		p, e := c.store.SaveCorePrompt(tx, in.Body, in.ExpectedVersion, in.Actor, in.Publish, c.service.now().UTC())
		return p, 1, e
	})
	if e == nil {
		e = json.Unmarshal(raw, &out)
	}
	return
}
func (c *CoreOperations) canonicalCustomer(ctx context.Context, id int64) error {
	if c.canonical == nil || id < 1 {
		return ErrNotReady
	}
	ids, e := c.canonical.CanonicalCustomers(ctx, []customerdomain.CustomerID{customerdomain.CustomerID(id)})
	if e != nil || len(ids) != 1 || int64(ids[0]) != id {
		return ErrInvalid
	}
	return nil
}
func (c *CoreOperations) ChangeAssignment(ctx context.Context, in CoreAssignmentCommand) (out segmentport.CoreAssignment, e error) {
	if !c.ready() {
		return out, ErrNotReady
	}
	if in.CoreProductID < 1 || in.CoreProductID > 5 || in.ExpectedAssignmentID < 0 || !validCoreText(in.Reason, 8000, true) {
		return out, ErrInvalid
	}
	if e = c.canonicalCustomer(ctx, in.CustomerID); e != nil {
		return out, e
	}
	actor, e := mutationActor(in.Actor, segmentport.MutationActor{})
	if e != nil {
		return out, e
	}
	raw, e := c.mutate(ctx, "assignment_changed", actor, in.IdempotencyKey, in, func(tx context.Context) (any, int64, error) {
		a, e := c.store.ChangeCoreAssignment(tx, segmentport.CoreAssignment{CustomerID: in.CustomerID, CoreProductID: in.CoreProductID, Source: "manual", Reason: in.Reason, EnteredAt: c.service.now().UTC()}, in.ExpectedAssignmentID, in.Purchase)
		if e == nil {
			e = c.store.PublishCoreAssignments(tx, in.Actor, in.IdempotencyKey, c.service.now().UTC())
		}
		if e == nil && in.Purchase && a.EndReason == "purchased" && c.reevaluate != nil {
			e = c.reevaluate.ReevaluateWithin(tx, in.CustomerID, "purchase-"+in.IdempotencyKey)
		}
		return a, in.CustomerID, e
	})
	if e == nil {
		e = json.Unmarshal(raw, &out)
	}
	return
}
func (c *CoreOperations) RecordPush(ctx context.Context, in CorePushCommand) (out segmentport.CorePush, e error) {
	if !c.ready() {
		return out, ErrNotReady
	}
	if e = ValidateCorePush(in.Push, c.service.now().UTC()); e != nil {
		return out, e
	}
	if e = c.canonicalCustomer(ctx, in.Push.CustomerID); e != nil {
		return out, e
	}
	// The source comes from the authenticated node, not the JSON body.
	if !in.Actor.Valid() {
		return out, ErrInvalid
	}
	in.Push.Source = in.Actor.Reference
	in.Push.ID = 0
	in.Push.AssignmentID = 0
	in.Push.OccurredAt = in.Push.OccurredAt.UTC().Truncate(time.Microsecond)
	raw, e := c.mutate(ctx, "push_recorded", in.Actor, in.IdempotencyKey, in.Push, func(tx context.Context) (any, int64, error) {
		if _, e := c.service.store.GetPackage(tx, in.Push.PackageID); e != nil {
			return nil, 0, e
		}
		p, e := c.store.RecordCorePush(tx, in.Push, c.service.now().UTC())
		return p, p.ID, e
	})
	if e == nil {
		e = json.Unmarshal(raw, &out)
	}
	return
}
func (c *CoreOperations) MemberDetail(ctx context.Context, packageID, customerID int64, cursor string, limit int) (out segmentport.CoreMemberDetail, e error) {
	if !c.ready() {
		return out, ErrNotReady
	}
	before := int64(0)
	if cursor != "" {
		before, e = strconv.ParseInt(cursor, 10, 64)
		if e != nil || before < 1 {
			return out, ErrInvalid
		}
	}
	if packageID < 1 || customerID < 1 || limit < 1 || limit > 100 {
		return out, ErrInvalid
	}
	e = c.service.uow.Within(ctx, func(tx context.Context) error {
		out, e = c.store.CoreMemberDetail(tx, packageID, customerID, before, limit)
		return e
	})
	return out, classify(e)
}

func (c *CoreOperations) RecordSupervisedPush(ctx context.Context, client, key string, push segmentport.CorePush) (segmentport.CorePush, error) {
	actor, e := segmentport.MachineMutationActor(client)
	if e != nil {
		return segmentport.CorePush{}, ErrInvalid
	}
	push.Source = actor.Reference
	return c.RecordPush(ctx, CorePushCommand{Push: push, Actor: actor, IdempotencyKey: key})
}

func (c *CoreOperations) BindReevaluation(queue interface {
	ReevaluateWithin(context.Context, int64, string) error
}) {
	c.reevaluate = queue
}

func (c *CoreOperations) CoreMembers(ctx context.Context, packageID int64, cursor string, limit int) (out segmentport.MemberPage, err error) {
	if !c.ready() {
		return out, ErrNotReady
	}
	if packageID < 1 || limit < 1 || limit > 100 {
		return out, ErrInvalid
	}
	reader, ok := c.store.(interface {
		PublishedSnapshot(context.Context, segmentport.PackageID) (segmentport.Snapshot, bool, error)
		Members(context.Context, segmentport.SnapshotID, string, int) (segmentport.MemberPage, error)
	})
	if !ok {
		return out, ErrNotReady
	}
	err = c.service.uow.Within(ctx, func(tx context.Context) error {
		snapshot, found, e := reader.PublishedSnapshot(tx, segmentport.PackageID(packageID))
		if e != nil {
			return e
		}
		if !found {
			return ErrNotReady
		}
		out, e = reader.Members(tx, snapshot.ID, cursor, limit)
		if e != nil {
			return e
		}
		for i := range out.Items {
			detail, e := c.store.CoreMemberDetail(tx, packageID, int64(out.Items[i].CustomerID), 0, 1)
			if e != nil {
				return e
			}
			out.Items[i].Operations = &detail
		}
		return nil
	})
	return out, classify(err)
}

func isCoreDefinition(raw json.RawMessage) bool {
	var v struct {
		Template string `json:"template_key"`
	}
	return json.Unmarshal(raw, &v) == nil && v.Template == "core_ai_product"
}

func (c *CoreOperations) MemberHistory(ctx context.Context, packageID, customerID int64, cursor string, limit int) (out segmentport.CoreAssignmentPage, e error) {
	if !c.ready() {
		return out, ErrNotReady
	}
	before := int64(0)
	if cursor != "" {
		before, e = strconv.ParseInt(cursor, 10, 64)
		if e != nil || before < 1 {
			return out, ErrInvalid
		}
	}
	if packageID < 1 || customerID < 1 || limit < 1 || limit > 100 {
		return out, ErrInvalid
	}
	reader, ok := c.store.(interface {
		CoreAssignmentHistory(context.Context, int64, int64, int64, int) (segmentport.CoreAssignmentPage, error)
	})
	if !ok {
		return out, ErrNotReady
	}
	e = c.service.uow.Within(ctx, func(tx context.Context) error {
		out, e = reader.CoreAssignmentHistory(tx, packageID, customerID, before, limit)
		return e
	})
	return out, classify(e)
}
