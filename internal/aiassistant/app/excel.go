package app

import (
	"context"
	"encoding/json"
	"fmt"
	domain "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/domain"
	ai "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/port"
	effect "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	"strings"
	"time"
)

type ExcelImportStore interface {
	ExcelPlan(context.Context, string) (ai.PlanID, error)
	BindExcelPlan(context.Context, string, string, ai.PlanID) error
}

type OperationExcelBatchStore interface {
	ExcelImportStore
	BindOperationExcelBatch(context.Context, string, string, string, ai.PlanID, int64, time.Time, []ai.ExcelBatchRow) error
	ExcelBatch(context.Context, ai.PlanID, bool) (ai.ExcelBatchMeta, error)
	ExcelBatchByKey(context.Context, string) (ai.ExcelBatchMeta, error)
	LinkExcelBatch(context.Context, ai.PlanID, string) error
	ReplaceOperationExcelBatch(context.Context, ai.Plan, ai.ExcelBatchMeta, string, effect.Digest, []ai.ExcelBatchRow, int64, time.Time) (ai.Plan, error)
	ExcelBatchRowAttributes(context.Context, ai.PlanID, ai.RecipientID, bool) (ai.ExcelBatchRowAttributes, error)
	PatchExcelBatchRowAttributes(context.Context, ai.PlanID, ai.RecipientID, int, string, bool, time.Time) error
	ResetOperationExcelReview(context.Context, ai.PlanID, int, time.Time) (ai.Plan, error)
	ExcelBatchSummary(context.Context, ai.PlanID, int) (ai.ExcelBatchSummary, error)
	AppendOperationExcelCover(context.Context, ai.PlanID, int, effect.Digest, int64, time.Time) error
	ListOperationExcelBatchVersions(context.Context, ai.PlanID, int) ([]ai.ExcelBatchVersion, error)
	ListOperationExcelBatchRecipients(context.Context, ai.PlanID, int, int64, int) ([]ai.Recipient, error)
}

type OperationExcelBatchOverviewStore interface {
	ListLatestOperationExcelBatchOverviews(context.Context, []string) ([]ai.ExcelBatchOverview, error)
}

// LatestOperationExcelBatchOverviews reads newest batch facts for one
// already-bounded Strategy page in a single AI Assistant read transaction.
// It is intentionally a read model and does not touch Excel content, cover,
// approval, or outbound behavior.
func (s *Service) LatestOperationExcelBatchOverviews(ctx context.Context, strategyKeys []string) ([]ai.ExcelBatchOverview, error) {
	if s == nil || len(strategyKeys) > 100 {
		return nil, ErrInvalid
	}
	if len(strategyKeys) == 0 {
		return []ai.ExcelBatchOverview{}, nil
	}
	seen := make(map[string]struct{}, len(strategyKeys))
	for _, key := range strategyKeys {
		if strings.TrimSpace(key) == "" {
			return nil, ErrInvalid
		}
		if _, duplicate := seen[key]; duplicate {
			return nil, ErrInvalid
		}
		seen[key] = struct{}{}
	}
	store, ok := s.store.(OperationExcelBatchOverviewStore)
	if !ok {
		return nil, ErrUnavailable
	}
	var result []ai.ExcelBatchOverview
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		result, readErr = store.ListLatestOperationExcelBatchOverviews(tx, strategyKeys)
		return readErr
	})
	return result, classify(err)
}

// CreateOperationExcelBatch accepts only component-parsed rows. It validates
// the long-plan association and writes the association, immutable import
// version, review plan, audit and idempotency facts in one PostgreSQL UoW.
// It never approves or emits a Provider task.
func (s *Service) CreateOperationExcelBatch(ctx context.Context, command ai.ExcelBatchCommand) (ai.CreatePlanResult, error) {
	var result ai.CreatePlanResult
	if s == nil {
		return result, ErrInvalid
	}
	store, ok := s.store.(OperationExcelBatchStore)
	if !ok || s.excelStrategies == nil || !command.Valid() {
		return result, ErrInvalid
	}
	err := s.uow.Within(ctx, func(tx context.Context) error {
		receipt, owned, err := s.store.Reserve(tx, reservation("excel_batch_create", command.Actor, command.IdempotencyKey, excelBatchCreateDigest(command), command.OccurredAt))
		if err != nil {
			return err
		}
		if !owned {
			planID := receiptPlanID(receipt.ResultSnapshot)
			if planID < 1 {
				return ErrConflict
			}
			result.Plan, err = s.store.GetPlan(tx, planID, false)
			result.Replayed = err == nil
			return err
		}
		if _, err := s.excelStrategies.OperationCycleStrategy(tx, command.StrategyKey); err != nil {
			return err
		}
		found, err := store.ExcelPlan(tx, command.BatchKey)
		if err != nil {
			return err
		}
		if found > 0 {
			// Matching request keys replay from excel_batch_create above. Any
			// other request key that reaches this point is a duplicate file.
			return ErrConflict
		}
		candidates := excelCandidates(command.Scope, command.Rows)
		create := ai.CreatePlanCommand{Actor: command.Actor, IdempotencyKey: "excel-import-" + command.BatchKey, Name: command.Name, SourceKind: "excel_batch", SourceDigest: command.FileDigest, Recipients: candidates, OccurredAt: command.OccurredAt}
		if err = s.createWithin(tx, create, candidates, &result); err != nil {
			return err
		}
		if err = store.BindOperationExcelBatch(tx, command.BatchKey, string(command.FileDigest), command.StrategyKey, result.Plan.ID, command.Actor.ID, command.OccurredAt, command.Rows); err != nil {
			return err
		}
		snapshot, _ := json.Marshal(map[string]any{"plan_id": result.Plan.ID})
		_, err = s.store.Complete(tx, receipt.ID, snapshot, command.OccurredAt)
		return err
	})
	return result, classify(err)
}

// excelBatchCreateDigest excludes server timestamps and display decoration so
// the browser can retry the same business command with its original key.
func excelBatchCreateDigest(command ai.ExcelBatchCommand) [32]byte {
	return digestJSON(struct {
		BatchKey, StrategyKey, Scope string
		FileDigest                   effect.Digest
		Rows                         []ai.ExcelBatchRow
	}{command.BatchKey, command.StrategyKey, command.Scope, command.FileDigest, command.Rows})
}

func excelCandidates(scope string, rows []ai.ExcelBatchRow) []ai.RecipientCandidate {
	items := make([]ai.RecipientCandidate, 0, len(rows))
	for _, row := range rows {
		card := row.Card
		items = append(items, ai.RecipientCandidate{DeferredTarget: &ai.DeferredTarget{UnionID: row.UnionID, Scope: scope, SenderUserID: row.SenderUserID}, Content: []ai.ContentBlock{{Kind: ai.ContentText, Text: row.Text}, {Kind: ai.ContentMiniProgram, ExcelCard: &card}}})
	}
	return items
}

// OperationExcelBatch is a read-only facade for the controlled Host. Keeping
// the transaction here means HTTP adapters never touch an AI Assistant store
// without the same PostgreSQL UoW discipline as commands.
func (s *Service) OperationExcelBatch(ctx context.Context, planID ai.PlanID) (ai.ExcelBatchMeta, error) {
	if s == nil || planID < 1 {
		return ai.ExcelBatchMeta{}, ErrInvalid
	}
	store, ok := s.store.(OperationExcelBatchStore)
	if !ok {
		return ai.ExcelBatchMeta{}, ErrUnavailable
	}
	var result ai.ExcelBatchMeta
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		result, readErr = store.ExcelBatch(tx, planID, false)
		return readErr
	})
	return result, classify(err)
}

func (s *Service) OperationExcelBatchByKey(ctx context.Context, batchKey string) (ai.ExcelBatchMeta, error) {
	if s == nil || strings.TrimSpace(batchKey) == "" {
		return ai.ExcelBatchMeta{}, ErrInvalid
	}
	store, ok := s.store.(OperationExcelBatchStore)
	if !ok {
		return ai.ExcelBatchMeta{}, ErrUnavailable
	}
	var result ai.ExcelBatchMeta
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		result, readErr = store.ExcelBatchByKey(tx, batchKey)
		return readErr
	})
	return result, classify(err)
}

func (s *Service) ListOperationExcelBatches(ctx context.Context, strategyKey string, limit int) ([]ai.ExcelBatchMeta, error) {
	if s == nil || strings.TrimSpace(strategyKey) == "" || limit < 1 || limit > 100 {
		return nil, ErrInvalid
	}
	store, ok := s.store.(interface {
		ListOperationExcelBatches(context.Context, string, int) ([]ai.ExcelBatchMeta, error)
	})
	if !ok {
		return nil, ErrUnavailable
	}
	var result []ai.ExcelBatchMeta
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		result, readErr = store.ListOperationExcelBatches(tx, strategyKey, limit)
		return readErr
	})
	return result, classify(err)
}

func (s *Service) ListUnlinkedOperationExcelPlans(ctx context.Context, limit int) ([]ai.Plan, error) {
	if s == nil || limit < 1 || limit > 100 {
		return nil, ErrInvalid
	}
	store, ok := s.store.(interface {
		ListUnlinkedExcelPlans(context.Context, int) ([]ai.Plan, error)
	})
	if !ok {
		return nil, ErrUnavailable
	}
	var result []ai.Plan
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		result, readErr = store.ListUnlinkedExcelPlans(tx, limit)
		return readErr
	})
	return result, classify(err)
}

func (s *Service) OperationExcelBatchRowAttributes(ctx context.Context, planID ai.PlanID, recipientID ai.RecipientID) (ai.ExcelBatchRowAttributes, error) {
	if s == nil || planID < 1 || recipientID < 1 {
		return ai.ExcelBatchRowAttributes{}, ErrInvalid
	}
	store, ok := s.store.(OperationExcelBatchStore)
	if !ok {
		return ai.ExcelBatchRowAttributes{}, ErrUnavailable
	}
	var result ai.ExcelBatchRowAttributes
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		result, readErr = store.ExcelBatchRowAttributes(tx, planID, recipientID, false)
		return readErr
	})
	return result, classify(err)
}

func (s *Service) OperationExcelBatchSummary(ctx context.Context, planID ai.PlanID, revision int) (ai.ExcelBatchSummary, error) {
	if s == nil || planID < 1 || revision < 1 {
		return ai.ExcelBatchSummary{}, ErrInvalid
	}
	store, ok := s.store.(OperationExcelBatchStore)
	if !ok {
		return ai.ExcelBatchSummary{}, ErrUnavailable
	}
	var result ai.ExcelBatchSummary
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		result, readErr = store.ExcelBatchSummary(tx, planID, revision)
		return readErr
	})
	return result, classify(err)
}

func (s *Service) OperationExcelBatchVersions(ctx context.Context, planID ai.PlanID, limit int) ([]ai.ExcelBatchVersion, error) {
	if s == nil || planID < 1 || limit < 1 || limit > 100 {
		return nil, ErrInvalid
	}
	store, ok := s.store.(OperationExcelBatchStore)
	if !ok {
		return nil, ErrUnavailable
	}
	var result []ai.ExcelBatchVersion
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		result, readErr = store.ListOperationExcelBatchVersions(tx, planID, limit)
		return readErr
	})
	return result, classify(err)
}

func (s *Service) OperationExcelBatchRecipients(ctx context.Context, planID ai.PlanID, revision int, cursor string, limit int) (ai.ExcelBatchRecipientPage, error) {
	if s == nil || planID < 1 || revision < 1 || limit < 1 || limit > MaximumPageSize {
		return ai.ExcelBatchRecipientPage{}, ErrInvalid
	}
	afterID, err := decodeIDCursor(cursor)
	if err != nil {
		return ai.ExcelBatchRecipientPage{}, ErrInvalid
	}
	store, ok := s.store.(OperationExcelBatchStore)
	if !ok {
		return ai.ExcelBatchRecipientPage{}, ErrUnavailable
	}
	var items []ai.Recipient
	err = s.uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		items, readErr = store.ListOperationExcelBatchRecipients(tx, planID, revision, afterID, limit)
		return readErr
	})
	if err != nil {
		return ai.ExcelBatchRecipientPage{}, classify(err)
	}
	page := ai.ExcelBatchRecipientPage{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		page.NextCursor = encodeIDCursor(int64(page.Items[len(page.Items)-1].ID))
	}
	return page, nil
}

// linkedOperationExcelBatch keeps generic AI Assistant commands from silently
// bypassing the controlled Excel command surface. Callers already hold the
// plan transaction when using it, so association and mutation remain atomic.
func (s *Service) linkedOperationExcelBatch(ctx context.Context, planID ai.PlanID) (bool, error) {
	store, ok := s.store.(OperationExcelBatchStore)
	if !ok {
		return false, nil
	}
	batch, err := store.ExcelBatch(ctx, planID, false)
	if err == ErrNotFound {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return batch.Linked(), nil
}

// ReplaceOperationExcelBatch retains the original review-plan/batch ID and
// cover bytes while adding a read-only upload revision. Previous recipients
// remain retained history; only the new revision participates in review.
func (s *Service) ReplaceOperationExcelBatch(ctx context.Context, command ai.ReplaceExcelBatchCommand) (ai.Plan, error) {
	var result ai.Plan
	if s == nil {
		return result, ErrInvalid
	}
	store, ok := s.store.(OperationExcelBatchStore)
	if !ok || !command.Valid() {
		return result, ErrInvalid
	}
	err := s.uow.Within(ctx, func(tx context.Context) error {
		receipt, owned, err := s.store.Reserve(tx, reservation("excel_batch_replace", command.Actor, command.IdempotencyKey, replaceExcelBatchDigest(command), command.OccurredAt))
		if err != nil {
			return err
		}
		if !owned {
			if receiptPlanID(receipt.ResultSnapshot) != command.PlanID {
				return ErrConflict
			}
			result, err = s.store.GetPlan(tx, command.PlanID, false)
			return err
		}
		plan, err := s.store.GetPlan(tx, command.PlanID, true)
		if err != nil {
			return err
		}
		if plan.SourceKind != "excel_batch" || plan.Version != command.ExpectedVersion || (plan.State != ai.PlanPendingReview && plan.State != ai.PlanPartiallyApproved) {
			return ErrConflict
		}
		batch, err := store.ExcelBatch(tx, command.PlanID, true)
		if err != nil || !batch.Linked() {
			return ErrConflict
		}
		result, err = store.ReplaceOperationExcelBatch(tx, plan, batch, command.Scope, command.FileDigest, command.Rows, command.Actor.ID, command.OccurredAt)
		if err != nil {
			return err
		}
		payload, _ := json.Marshal(map[string]any{"plan_id": command.PlanID, "content_revision": batch.Revision + 1})
		if err = s.store.AppendEvent(tx, ai.Event{Type: ai.EventContentUpdated, AggregateID: command.PlanID, ActorID: command.Actor.ID, IdempotencyKey: command.IdempotencyKey, Payload: payload, OccurredAt: command.OccurredAt}); err != nil {
			return err
		}
		snapshot, _ := json.Marshal(map[string]any{"plan_id": command.PlanID})
		_, err = s.store.Complete(tx, receipt.ID, snapshot, command.OccurredAt)
		return err
	})
	return result, classify(err)
}

func replaceExcelBatchDigest(command ai.ReplaceExcelBatchCommand) [32]byte {
	return digestJSON(struct {
		PlanID          ai.PlanID
		ExpectedVersion int64
		Scope           string
		FileDigest      effect.Digest
		Rows            []ai.ExcelBatchRow
	}{command.PlanID, command.ExpectedVersion, command.Scope, command.FileDigest, command.Rows})
}

// UpdateOperationExcelRow changes only controlled Excel row fields. It never
// accepts a customer identity, sender, AppID, approval state or Provider
// state. Any change resets the complete current review projection so a stale
// whole-batch preview cannot approve changed content.
func (s *Service) UpdateOperationExcelRow(ctx context.Context, command ai.UpdateExcelRowCommand) (ai.Plan, error) {
	var result ai.Plan
	if s == nil || !command.Valid() {
		return result, ErrInvalid
	}
	store, ok := s.store.(OperationExcelBatchStore)
	if !ok {
		return result, ErrUnavailable
	}
	err := s.uow.Within(ctx, func(tx context.Context) error {
		receipt, owned, err := s.store.Reserve(tx, reservation("excel_batch_row_update", command.Actor, command.IdempotencyKey, digestJSON(command), s.nowUTC()))
		if err != nil {
			return err
		}
		if !owned {
			if receiptPlanID(receipt.ResultSnapshot) != command.PlanID {
				return ErrConflict
			}
			result, err = s.store.GetPlan(tx, command.PlanID, false)
			return err
		}
		plan, err := s.store.GetPlan(tx, command.PlanID, true)
		if err != nil {
			return err
		}
		if plan.SourceKind != "excel_batch" || (plan.State != ai.PlanPendingReview && plan.State != ai.PlanPartiallyApproved) {
			return ErrConflict
		}
		batch, err := store.ExcelBatch(tx, command.PlanID, true)
		if err != nil || !batch.Linked() {
			return ErrConflict
		}
		attributes, err := store.ExcelBatchRowAttributes(tx, command.PlanID, command.RecipientID, true)
		if err != nil || attributes.ContentRevision != batch.Revision {
			return ErrConflict
		}
		recipient, previous, err := s.store.GetRecipient(tx, command.PlanID, command.RecipientID, true)
		if err != nil {
			return err
		}
		if recipient.Version != command.ExpectedVersion || recipient.DeferredTarget == nil || len(previous.Blocks) != 2 || previous.Blocks[0].Kind != ai.ContentText || previous.Blocks[1].ExcelCard == nil {
			return ErrConflict
		}
		card := *previous.Blocks[1].ExcelCard
		contentChanged := previous.Blocks[0].Text != command.Text || card.Path != command.Path || card.Title != command.Title
		card.Path, card.Title = command.Path, command.Title
		if contentChanged {
			blocks := []ai.ContentBlock{{Kind: ai.ContentText, Text: command.Text}, {Kind: ai.ContentMiniProgram, ExcelCard: &card}}
			payload, digest, err := domain.FreezeContent(blocks)
			if err != nil {
				return ErrInvalid
			}
			if _, _, err = s.store.UpdateContent(tx, command.PlanID, command.RecipientID, command.ExpectedVersion, payload, digest, command.Actor.ID, s.nowUTC()); err != nil {
				return err
			}
		}
		if err = store.PatchExcelBatchRowAttributes(tx, command.PlanID, command.RecipientID, batch.Revision, command.Segment, command.Excluded, s.nowUTC()); err != nil {
			return err
		}
		result, err = store.ResetOperationExcelReview(tx, command.PlanID, batch.Revision, s.nowUTC())
		if err != nil {
			return err
		}
		body, _ := json.Marshal(map[string]any{"plan_id": command.PlanID, "recipient_id": command.RecipientID, "content_revision": batch.Revision, "excluded": command.Excluded})
		if err = s.store.AppendEvent(tx, ai.Event{Type: ai.EventContentUpdated, AggregateID: command.PlanID, RecipientID: command.RecipientID, ActorID: command.Actor.ID, IdempotencyKey: command.IdempotencyKey, Payload: body, OccurredAt: s.nowUTC()}); err != nil {
			return err
		}
		snapshot, _ := json.Marshal(map[string]any{"plan_id": command.PlanID})
		_, err = s.store.Complete(tx, receipt.ID, snapshot, s.nowUTC())
		return err
	})
	return result, classify(err)
}

// LinkLegacyOperationExcelBatch is intentionally a protected, explicit
// migration command. It never guesses a strategy and cannot link twice.
func (s *Service) LinkLegacyOperationExcelBatch(ctx context.Context, actor ai.Actor, planID ai.PlanID, expectedVersion int64, strategyKey, key string) (ai.Plan, error) {
	var result ai.Plan
	if s == nil || !actor.Valid() || planID < 1 || expectedVersion < 1 || !validKey(key) || strings.TrimSpace(strategyKey) == "" || s.excelStrategies == nil {
		return result, ErrInvalid
	}
	store, ok := s.store.(OperationExcelBatchStore)
	if !ok {
		return result, ErrUnavailable
	}
	err := s.uow.Within(ctx, func(tx context.Context) error {
		input := struct {
			PlanID   ai.PlanID
			Version  int64
			Strategy string
		}{planID, expectedVersion, strategyKey}
		receipt, owned, err := s.store.Reserve(tx, reservation("excel_batch_legacy_link", actor, key, digestJSON(input), s.nowUTC()))
		if err != nil {
			return err
		}
		if !owned {
			if receiptPlanID(receipt.ResultSnapshot) != planID {
				return ErrConflict
			}
			result, err = s.store.GetPlan(tx, planID, false)
			return err
		}
		plan, err := s.store.GetPlan(tx, planID, true)
		if err != nil || plan.SourceKind != "excel_batch" || plan.Version != expectedVersion {
			return ErrConflict
		}
		if _, err = s.excelStrategies.OperationCycleStrategy(tx, strategyKey); err != nil {
			return err
		}
		meta, err := store.ExcelBatch(tx, planID, true)
		if err != nil || meta.StrategyKey != "" {
			return ErrConflict
		}
		if err = store.LinkExcelBatch(tx, planID, strategyKey); err != nil {
			return err
		}
		result = plan
		payload, _ := json.Marshal(map[string]any{"plan_id": planID, "operation_cycle_strategy_key": strategyKey})
		if err = s.store.AppendEvent(tx, ai.Event{Type: ai.EventPlanCreated, AggregateID: planID, ActorID: actor.ID, IdempotencyKey: key, Payload: payload, OccurredAt: s.nowUTC()}); err != nil {
			return err
		}
		snapshot, _ := json.Marshal(map[string]any{"plan_id": planID})
		_, err = s.store.Complete(tx, receipt.ID, snapshot, s.nowUTC())
		return err
	})
	return result, classify(err)
}

// CreateExcelPlan is a dedicated trusted import edge; ordinary intake cannot
// opt out of canonical identity validation by supplying deferred_target.
func (s *Service) CreateExcelPlan(ctx context.Context, batchKey string, command ai.CreatePlanCommand) (ai.CreatePlanResult, error) {
	var result ai.CreatePlanResult
	store, ok := s.store.(ExcelImportStore)
	if !ok || batchKey == "" || len(batchKey) > 128 || !command.Valid() || command.SourceKind != "excel_batch" {
		return result, ErrInvalid
	}
	seen := map[string]bool{}
	for _, r := range command.Recipients {
		if r.DeferredTarget == nil || !r.DeferredTarget.Valid() || len(r.Content) != 2 || r.Content[0].Kind != ai.ContentText || r.Content[1].ExcelCard == nil {
			return result, ErrInvalid
		}
		for _, b := range r.Content {
			if !b.Valid() {
				return result, ErrInvalid
			}
		}
		// One user per file prevents ambiguous distinct-user reporting and accidental double sends.
		if seen[r.DeferredTarget.UnionID] {
			return result, fmt.Errorf("%w: duplicate recipient", ErrInvalid)
		}
		seen[r.DeferredTarget.UnionID] = true
	}
	err := s.uow.Within(ctx, func(tx context.Context) error {
		id, err := store.ExcelPlan(tx, batchKey)
		if err != nil {
			return err
		}
		if id > 0 {
			result.Plan, err = s.store.GetPlan(tx, id, false)
			result.Replayed = true
			return err
		}
		command.IdempotencyKey = "excel-import-" + batchKey
		if command.OccurredAt.IsZero() {
			command.OccurredAt = time.Now().UTC()
		}
		if err = s.createWithin(tx, command, command.Recipients, &result); err != nil {
			return err
		}
		return store.BindExcelPlan(tx, batchKey, string(command.SourceDigest), result.Plan.ID)
	})
	return result, classify(err)
}

// ApplyExcelCover freezes one uploaded cover for every row in one PostgreSQL
// UoW. The immutable bytes are stored before this call; an interrupted upload
// may leave an unreferenced blob, never a partially updated review batch.
func (s *Service) ApplyExcelCover(ctx context.Context, actor ai.Actor, id ai.PlanID, version int64, key string, cover effect.Digest) (ai.Plan, error) {
	return s.applyExcelCover(ctx, actor, id, version, key, 0, cover, false)
}

// ApplyExcelMediaCover freezes both the stable Media image reference and its
// actual content digest. It accepts only controlled Excel batches, preserving
// legacy Python-cover plans and their historical bytes unchanged.
func (s *Service) ApplyExcelMediaCover(ctx context.Context, actor ai.Actor, id ai.PlanID, version int64, key string, imageID int64, cover effect.Digest) (ai.Plan, error) {
	return s.applyExcelCover(ctx, actor, id, version, key, imageID, cover, true)
}

func (s *Service) applyExcelCover(ctx context.Context, actor ai.Actor, id ai.PlanID, version int64, key string, imageID int64, cover effect.Digest, requireMediaBinding bool) (ai.Plan, error) {
	var result ai.Plan
	repo, ok := s.store.(interface {
		ExcelCoverRecipients(context.Context, ai.PlanID) ([]ai.Recipient, []ai.ContentVersion, error)
	})
	if !ok || !actor.Valid() || id < 1 || version < 1 || !validKey(key) || imageID < 0 || (requireMediaBinding && imageID < 1) || !effect.ValidDigest(cover) {
		return result, ErrInvalid
	}
	err := s.uow.Within(ctx, func(tx context.Context) error {
		input := struct {
			Actor   ai.Actor
			ID      ai.PlanID
			Version int64
			ImageID int64
			Cover   effect.Digest
		}{actor, id, version, imageID, cover}
		receipt, owned, err := s.store.Reserve(tx, reservation("excel_cover", actor, key, digestJSON(input), s.nowUTC()))
		if err != nil {
			return err
		}
		plan, err := s.store.GetPlan(tx, id, true)
		if err != nil {
			return err
		}
		if !owned {
			result = plan
			return nil
		}
		if plan.SourceKind != "excel_batch" || plan.Version != version || (plan.State != ai.PlanPendingReview && plan.State != ai.PlanPartiallyApproved) {
			return ErrConflict
		}
		if requireMediaBinding {
			batchStore, found := s.store.(interface {
				ExcelBatch(context.Context, ai.PlanID, bool) (ai.ExcelBatchMeta, error)
			})
			if !found {
				return ErrUnavailable
			}
			batch, batchErr := batchStore.ExcelBatch(tx, id, true)
			if batchErr != nil || batch.SourceOrigin != "excel" {
				return ErrConflict
			}
		}
		rows, contents, err := repo.ExcelCoverRecipients(tx, id)
		if err != nil {
			return err
		}
		for i, r := range rows {
			if r.DeferredTarget == nil || len(contents[i].Blocks) != 2 || contents[i].Blocks[1].ExcelCard == nil {
				return ErrInvalid
			}
			blocks := contents[i].Blocks
			card := *blocks[1].ExcelCard
			card.CoverDigest = cover
			card.CoverImageID = imageID
			blocks[1].ExcelCard = &card
			payload, digest, err := domain.FreezeContent(blocks)
			if err != nil {
				return ErrInvalid
			}
			_, content, err := s.store.UpdateContent(tx, id, r.ID, r.Version, payload, digest, actor.ID, s.nowUTC())
			if err != nil {
				return err
			}
			body, _ := json.Marshal(map[string]any{"plan_id": id, "recipient_id": r.ID, "content_version_id": content.ID, "content_digest": content.Digest, "reason": "batch_cover_updated"})
			if err = s.store.AppendEvent(tx, ai.Event{Type: ai.EventContentUpdated, AggregateID: id, RecipientID: r.ID, ActorID: actor.ID, IdempotencyKey: fmt.Sprintf("%s:%d", key, r.ID), Payload: body, OccurredAt: s.nowUTC()}); err != nil {
				return err
			}
		}
		if resetter, ok := s.store.(interface {
			ExcelBatch(context.Context, ai.PlanID, bool) (ai.ExcelBatchMeta, error)
			AppendOperationExcelCover(context.Context, ai.PlanID, int, effect.Digest, int64, time.Time) error
			AppendOperationExcelMediaCover(context.Context, ai.PlanID, int, int64, effect.Digest, int64, time.Time) error
			ResetOperationExcelReview(context.Context, ai.PlanID, int, time.Time) (ai.Plan, error)
		}); ok {
			batch, batchErr := resetter.ExcelBatch(tx, id, true)
			if batchErr != nil {
				return batchErr
			}
			if requireMediaBinding {
				batchErr = resetter.AppendOperationExcelMediaCover(tx, id, batch.Revision, imageID, cover, actor.ID, s.nowUTC())
			} else {
				batchErr = resetter.AppendOperationExcelCover(tx, id, batch.Revision, cover, actor.ID, s.nowUTC())
			}
			if batchErr != nil {
				return batchErr
			}
			result, err = resetter.ResetOperationExcelReview(tx, id, batch.Revision, s.nowUTC())
			if err != nil {
				return err
			}
		} else {
			result, err = s.store.GetPlan(tx, id, false)
			if err != nil {
				return err
			}
		}
		body, _ := json.Marshal(map[string]any{"plan_id": id})
		_, err = s.store.Complete(tx, receipt.ID, body, s.nowUTC())
		return err
	})
	return result, classify(err)
}
