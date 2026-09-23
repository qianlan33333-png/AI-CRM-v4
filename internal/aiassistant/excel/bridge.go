package excel

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	access "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accessport "github.com/qianlan33333-png/AI-CRM-v3/internal/access/port"
	app "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/app"
	ai "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/port"
	effect "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	operationport "github.com/qianlan33333-png/AI-CRM-v3/internal/operationcycle/port"
	outbound "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type Security interface {
	Authenticate(context.Context, *http.Request) (access.Principal, error)
	AuthorizeCSRF(context.Context, *http.Request) (access.Principal, error)
}
type Repository interface {
	ExcelPlans(context.Context) ([]ai.PlanID, error)
	ExcelApproval(context.Context, ai.PlanID) (string, time.Time, error)
	RecordExcelDelivery(context.Context, ai.Recipient, outbound.PrivateMessageDelivery) error
}
type Bridge struct {
	Client     *Client
	App        *app.Service
	Repo       Repository
	Receipts   outbound.PrivateMessageDeliveryStore
	Provider   outbound.PrivateMessageDeliveryReader
	Security   Security
	Authorizer accessport.AIAssistantAuthorizer
	Scope      string
	Covers     mediaport.ExcelCoverLibrary
	Strategies operationport.StrategyPageReader
}
type row struct {
	ID       ai.RecipientID    `json:"id"`
	UnionID  string            `json:"unionid"`
	Sender   string            `json:"sender_userid"`
	Text     string            `json:"text"`
	Path     string            `json:"path"`
	Title    string            `json:"title"`
	Card     *ai.ExcelCard     `json:"card,omitempty"`
	Segment  string            `json:"segment"`
	Excluded bool              `json:"excluded"`
	State    string            `json:"delivery_state"`
	Reason   string            `json:"failure_reason"`
	SentAt   *time.Time        `json:"sent_at"`
	Version  int64             `json:"version"`
	Content  []ai.ContentBlock `json:"-"`
	Review   string            `json:"review_state"`
}

func deliveryState(value ai.ExecutionState) string {
	switch value {
	case ai.ExecutionNotAccepted:
		return "pending_submission"
	case ai.ExecutionAccepted, ai.ExecutionQueued, ai.ExecutionAttempted, ai.ExecutionProviderAccepted, ai.ExecutionReconciled:
		return "task_created_waiting_employee"
	case ai.ExecutionRetryableFailed, ai.ExecutionOutcomeUnknown:
		return "outcome_unknown"
	case ai.ExecutionFinalFailed:
		return "final_failed"
	case ai.ExecutionDeliveryProven:
		return "delivery_proven"
	default:
		return "outcome_unknown"
	}
}

type preparedImport struct {
	FileDigest effect.Digest `json:"file_digest"`
	Rows       []struct {
		UnionID string       `json:"unionid"`
		Text    string       `json:"text"`
		Sender  string       `json:"sender_userid"`
		Card    ai.ExcelCard `json:"card"`
		Segment string       `json:"segment"`
	} `json:"rows"`
}

func (p preparedImport) batchRows() []ai.ExcelBatchRow {
	items := make([]ai.ExcelBatchRow, 0, len(p.Rows))
	for _, item := range p.Rows {
		items = append(items, ai.ExcelBatchRow{UnionID: item.UnionID, SenderUserID: item.Sender, Text: item.Text, Card: item.Card, Segment: item.Segment})
	}
	return items
}

func batchJSON(plan ai.Plan, meta ai.ExcelBatchMeta, summary ai.ExcelBatchSummary) map[string]any {
	segmentSource := meta.SourceOrigin
	if segmentSource == "" {
		segmentSource = "legacy_snapshot"
	}
	return map[string]any{
		"id":                           plan.ID,
		"batch_key":                    meta.BatchKey,
		"operation_cycle_strategy_key": meta.StrategyKey,
		"state":                        plan.State,
		"version":                      plan.Version,
		"file_digest":                  meta.FileDigest,
		"cover_digest":                 meta.CoverDigest,
		"cover_image_id":               meta.CoverImageID,
		"current_content_version":      meta.Revision,
		"source_kind":                  plan.SourceKind,
		"segment_source":               segmentSource,
		"created_at":                   meta.CreatedAt.UTC().Format(time.RFC3339Nano),
		"summary":                      summary,
	}
}

func batchOverviewJSON(value ai.ExcelBatchOverview) map[string]any {
	return batchJSON(ai.Plan{ID: value.Meta.PlanID, State: value.State, Version: value.PlanVersion, SourceKind: value.SourceKind}, value.Meta, value.Summary)
}

const strategySummaryDefaultLimit = int32(20)

func strategySummaryPageRequest(r *http.Request) (int32, int32, error) {
	for key, values := range r.URL.Query() {
		if len(values) != 1 || (key != "limit" && key != "offset") {
			return 0, 0, app.ErrInvalid
		}
	}
	limit, offset := strategySummaryDefaultLimit, int32(0)
	if values, present := r.URL.Query()["limit"]; present {
		value, err := strconv.ParseInt(values[0], 10, 32)
		if err != nil {
			return 0, 0, app.ErrInvalid
		}
		limit = int32(value)
	}
	if values, present := r.URL.Query()["offset"]; present {
		value, err := strconv.ParseInt(values[0], 10, 32)
		if err != nil {
			return 0, 0, app.ErrInvalid
		}
		offset = int32(value)
	}
	if limit < 1 || limit > operationport.StrategyPageMaximumLimit || offset < 0 || offset > operationport.StrategyPageMaximumOffset {
		return 0, 0, app.ErrInvalid
	}
	return limit, offset, nil
}

func (b *Bridge) strategySummaryPage(ctx context.Context, limit, offset int32) (map[string]any, error) {
	if b.Strategies == nil || b.App == nil {
		return nil, app.ErrUnavailable
	}
	page, err := b.Strategies.ListOperationCycleStrategies(ctx, limit, offset)
	if err != nil {
		return nil, err
	}
	if page.Limit != limit || page.Offset != offset || page.Total < 0 || len(page.Items) > int(limit) {
		return nil, app.ErrUnavailable
	}
	keys := make([]string, 0, len(page.Items))
	for _, item := range page.Items {
		if item.Key == "" {
			return nil, app.ErrUnavailable
		}
		keys = append(keys, item.Key)
	}
	// This call deliberately opens and closes its own AI Assistant read UoW.
	// A failed aggregate rolls that transaction back before this strategy page is
	// rendered, so a failed bulk query cannot poison a transaction used to keep
	// the independently-owned strategy facts visible.
	overviews, overviewErr := b.App.LatestOperationExcelBatchOverviews(ctx, keys)
	byKey := make(map[string]ai.ExcelBatchOverview, len(overviews))
	if overviewErr == nil {
		for _, overview := range overviews {
			if overview.Meta.StrategyKey == "" {
				return nil, app.ErrUnavailable
			}
			byKey[overview.Meta.StrategyKey] = overview
		}
	}
	items := make([]map[string]any, 0, len(page.Items))
	for _, strategy := range page.Items {
		item := map[string]any{
			"strategy_key": strategy.Key,
			"title":        strategy.Title,
			"status":       strategy.Status,
			"version":      strategy.Version,
			"snapshot":     json.RawMessage(strategy.Snapshot),
		}
		if overviewErr != nil {
			item["latest_batch"] = nil
			item["latest_batch_status"] = "unavailable"
		} else {
			item["latest_batch_status"] = "ready"
			if overview, found := byKey[strategy.Key]; found {
				item["latest_batch"] = batchOverviewJSON(overview)
			} else {
				item["latest_batch"] = nil
			}
		}
		items = append(items, item)
	}
	// READ COMMITTED permits a strategy deletion between the count and page
	// statements. Do not manufacture a next offset that equals the current one
	// for an empty stale page; the host can then move the user back safely. The
	// shared Port accepts int32 offsets, so only emit a next offset which its
	// own request parser can replay without an integer wrap.
	next := int64(offset) + int64(len(items))
	hasMore := len(items) > 0 && next < int64(page.Total) && next <= int64(operationport.StrategyPageMaximumOffset)
	var nextOffset any
	if hasMore {
		nextOffset = int32(next)
	}
	return map[string]any{"items": items, "total": page.Total, "limit": limit, "offset": offset, "has_more": hasMore, "next_offset": nextOffset}, nil
}

// prepareImport delegates only syntax and workbook validation to the component.
// It deliberately uses /prepare: import rows, version history and deduplication
// are authoritative PostgreSQL facts owned by AI Assistant.
func (b *Bridge) prepareImport(ctx context.Context, raw []byte) (preparedImport, error) {
	var prepared preparedImport
	err := b.Client.Raw(ctx, http.MethodPost, "/prepare", "", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", raw, &prepared)
	if err != nil {
		return preparedImport{}, err
	}
	if !effect.ValidDigest(prepared.FileDigest) || len(prepared.Rows) == 0 {
		return preparedImport{}, app.ErrInvalid
	}
	return prepared, nil
}

func batchKeyForPrepared(prepared preparedImport, idempotencyKey string, explicitNew bool) string {
	seed := string(prepared.FileDigest)
	if explicitNew {
		seed += "\x00" + idempotencyKey
	}
	sum := sha256.Sum256([]byte(seed))
	return "xlsx-" + hex.EncodeToString(sum[:])
}

func validIdempotencyKey(key string) bool { return len(key) >= 8 && len(key) <= 200 }

func pageRequest(r *http.Request) (string, int, error) {
	limit := app.MaximumPageSize
	if raw := r.URL.Query().Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > app.MaximumPageSize {
			return "", 0, app.ErrInvalid
		}
		limit = value
	}
	return r.URL.Query().Get("cursor"), limit, nil
}

func fmtInt(n int64) string { return strconv.FormatInt(n, 10) }

// RowsPage is the Host's bounded projection. Detail endpoints must use this
// rather than building an unbounded JSON body for a 5,000-row workbook.
func (b *Bridge) RowsPage(ctx context.Context, id ai.PlanID, cursor string, limit int, poll bool) ([]row, string, error) {
	if limit < 1 || limit > app.MaximumPageSize {
		return nil, "", app.ErrInvalid
	}
	page, err := b.App.ListRecipients(ctx, ai.RecipientPageQuery{PlanID: id, Cursor: cursor, Limit: limit})
	if err != nil {
		return nil, "", err
	}
	items, err := b.rowsFromRecipients(ctx, id, page.Items, poll)
	return items, page.NextCursor, err
}

func (b *Bridge) RowsPageForRevision(ctx context.Context, id ai.PlanID, revision int, cursor string, limit int) ([]row, string, error) {
	if limit < 1 || limit > app.MaximumPageSize || revision < 1 {
		return nil, "", app.ErrInvalid
	}
	page, err := b.App.OperationExcelBatchRecipients(ctx, id, revision, cursor, limit)
	if err != nil {
		return nil, "", err
	}
	items, err := b.rowsFromRecipients(ctx, id, page.Items, false)
	return items, page.NextCursor, err
}

func (b *Bridge) Rows(ctx context.Context, id ai.PlanID, poll bool) ([]row, error) {
	result := []row{}
	cursor := ""
	for {
		items, next, err := b.RowsPage(ctx, id, cursor, app.MaximumPageSize, poll)
		if err != nil {
			return nil, err
		}
		result = append(result, items...)
		if next == "" {
			break
		}
		cursor = next
	}
	return result, nil
}

func (b *Bridge) rowsFromRecipients(ctx context.Context, id ai.PlanID, recipients []ai.Recipient, poll bool) ([]row, error) {
	result := make([]row, 0, len(recipients))
	for _, recipient := range recipients {
		if recipient.DeferredTarget == nil {
			return nil, app.ErrInvalid
		}
		r, content, err := b.App.GetRecipient(ctx, id, recipient.ID)
		if err != nil {
			return nil, err
		}
		attributes, attributesErr := b.App.OperationExcelBatchRowAttributes(ctx, id, r.ID)
		if attributesErr != nil {
			return nil, attributesErr
		}
		item := row{ID: r.ID, UnionID: r.DeferredTarget.UnionID, Sender: r.DeferredTarget.SenderUserID, State: deliveryState(r.ExecutionState), Version: r.Version, Content: content.Blocks, Review: string(r.ReviewState), Segment: attributes.Segment, Excluded: attributes.Excluded}
		for _, block := range content.Blocks {
			if block.Kind == ai.ContentText {
				item.Text = block.Text
			}
			if block.ExcelCard != nil {
				item.Path = block.ExcelCard.Path
				item.Title = block.ExcelCard.Title
				card := *block.ExcelCard
				item.Card = &card
			}
		}
		ref := fmt.Sprintf("aiassistant:%d:%d:%d", id, r.ID, content.ID)
		receipt, found, readErr := b.Receipts.PrivateMessageReceipt(ctx, ref)
		if readErr != nil {
			return nil, readErr
		}
		if poll && found && receipt.MessageID != "" && (r.ExecutionState == ai.ExecutionProviderAccepted || r.ExecutionState == ai.ExecutionOutcomeUnknown) && (receipt.Status == nil || *receipt.Status == 0) {
			updated, err := b.pollReceipt(ctx, receipt)
			if err == nil && updated.Status != nil {
				if err = b.Receipts.SavePrivateMessageDelivery(ctx, ref, updated); err == nil {
					receipt = updated
				}
			} // read failures leave prior truth intact
		}
		if found {
			item.Reason = receipt.Reason
			if receipt.Status != nil {
				if *receipt.Status == 1 && receipt.SentAt != nil {
					item.State = "delivery_proven"
					item.SentAt = receipt.SentAt
				} else if *receipt.Status > 1 {
					item.State = "final_failed"
					item.Reason = fmt.Sprintf("wecom_status_%d", *receipt.Status)
				}
				if poll && item.State != string(r.ExecutionState) && item.State != "provider_accepted" {
					if err = b.Repo.RecordExcelDelivery(ctx, r, receipt); err != nil {
						return nil, err
					}
				}
			}
		}
		result = append(result, item)
	}
	return result, nil
}
func (b *Bridge) pollReceipt(ctx context.Context, receipt outbound.PrivateMessageDelivery) (outbound.PrivateMessageDelivery, error) {
	return outbound.ReconcilePrivateMessageDelivery(ctx, b.Provider, receipt)
}
func (b *Bridge) PrepareSnapshot(ctx context.Context, id ai.PlanID, version int64) (string, error) {
	if b.Client == nil {
		return "", ErrUnavailable
	}
	batch, err := b.App.OperationExcelBatch(ctx, id)
	if err != nil {
		return "", err
	}
	rows, err := b.Rows(ctx, id, false)
	if err != nil {
		return "", err
	}
	// Rows are loaded in bounded pages. Reject rather than publish a mixed
	// snapshot when a replacement wins between two reads.
	after, err := b.App.OperationExcelBatch(ctx, id)
	if err != nil {
		return "", err
	}
	if batch.Revision != after.Revision || batch.FileDigest != after.FileDigest {
		return "", app.ErrConflict
	}
	var random [16]byte
	if _, err = rand.Read(random[:]); err != nil {
		return "", err
	}
	key := snapshotKey(id, version) + ":" + hex.EncodeToString(random[:])
	targets := make([]map[string]any, 0, len(rows))
	componentSource := "unavailable"
	hasSegments := false
	if batch.SourceOrigin == "excel" {
		componentSource = "excel"
		for _, item := range rows {
			if item.Segment != "" {
				hasSegments = true
				break
			}
		}
	}
	for _, r := range rows {
		targets = append(targets, map[string]any{"id": r.ID, "unionid": r.UnionID, "segment": r.Segment, "segment_source": componentSource, "excluded": r.Excluded})
	}
	err = b.Client.JSON(ctx, "/snapshots", map[string]any{"snapshot_key": key, "rows": targets, "segment_source": componentSource, "has_segments": hasSegments, "version": version}, nil)
	return key, err
}
func (b *Bridge) Refresh(ctx context.Context) error {
	if b.Client == nil {
		return nil
	}
	ids, err := b.Repo.ExcelPlans(ctx)
	if err != nil {
		return err
	}
	var first error
	for _, id := range ids {
		key, _, err := b.Repo.ExcelApproval(ctx, id)
		if err != nil {
			if first == nil {
				first = err
			}
			continue
		}
		rows, err := b.Rows(ctx, id, true)
		if err == nil {
			for i := range rows {
				rows[i].Content = nil
			}
			err = b.Client.JSON(ctx, "/observations", map[string]any{"plan_id": id, "snapshot_key": key, "rows": rows}, nil)
		}
		if err != nil && first == nil {
			first = err
		}
	}
	return first
}
func (b *Bridge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	action := accessport.AIAssistantRead
	if r.Method != "GET" {
		action = accessport.AIAssistantReview
	}
	// A per-recipient CSV is an export, rather than an ordinary page read.
	// Reuse the existing administrator review permission until Access owns a
	// distinct export action.
	if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/report.csv") {
		action = accessport.AIAssistantReview
	}
	if strings.HasSuffix(r.URL.Path, "/approve") {
		action = accessport.AIAssistantApprove
	}
	var actor access.Principal
	var err error
	if r.Method == "GET" {
		actor, err = b.Security.Authenticate(r.Context(), r)
	} else {
		actor, err = b.Security.AuthorizeCSRF(r.Context(), r)
	}
	if err != nil || b.Authorizer.AuthorizeAIAssistant(r.Context(), actor, action) != nil {
		respond(w, 403, map[string]any{"error": "permission_denied"})
		return
	}
	if r.Method != http.MethodGet && !validIdempotencyKey(r.Header.Get("Idempotency-Key")) {
		respond(w, http.StatusBadRequest, map[string]any{"error": "invalid_input", "message": "Idempotency-Key 必须为 8–200 字符"})
		return
	}
	mediaCoverMutation := r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/cover") && b.Covers != nil
	if b.Client == nil && !mediaCoverMutation && (r.Method != "GET" || strings.HasSuffix(r.URL.Path, "/report") || strings.Contains(r.URL.Path, "/covers/")) {
		respond(w, 503, map[string]any{"error": "component_disabled"})
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/operation-batches"), "/"), "/")
	var output any
	responseStatus := http.StatusOK
	switch {
	case r.Method == http.MethodGet && len(parts) == 1 && parts[0] == "strategy-summaries":
		limit, offset, pageErr := strategySummaryPageRequest(r)
		if pageErr != nil {
			err = pageErr
			break
		}
		output, err = b.strategySummaryPage(r.Context(), limit, offset)
	case r.Method == http.MethodGet && len(parts) == 1 && parts[0] == "legacy":
		var plans []ai.Plan
		plans, err = b.App.ListUnlinkedOperationExcelPlans(r.Context(), 100)
		if err == nil {
			items := make([]map[string]any, 0, len(plans))
			for _, plan := range plans {
				items = append(items, map[string]any{"id": plan.ID, "name": plan.Name, "state": plan.State, "created_at": plan.CreatedAt.UTC().Format(time.RFC3339Nano), "linkable": true})
			}
			output = map[string]any{"items": items}
		}
	case r.Method == http.MethodGet && len(parts) == 2 && parts[0] == "strategies":
		var items []ai.ExcelBatchMeta
		items, err = b.App.ListOperationExcelBatches(r.Context(), parts[1], 100)
		if err == nil {
			values := make([]map[string]any, 0, len(items))
			for _, item := range items {
				plan, readErr := b.App.GetPlan(r.Context(), item.PlanID)
				if readErr != nil {
					err = readErr
					break
				}
				summary, summaryErr := b.App.OperationExcelBatchSummary(r.Context(), item.PlanID, item.Revision)
				if summaryErr != nil {
					err = summaryErr
					break
				}
				values = append(values, batchJSON(plan, item, summary))
			}
			output = map[string]any{"strategy": map[string]any{"strategy_key": parts[1]}, "items": values}
		}
	case r.Method == http.MethodPost && len(parts) == 3 && parts[0] == "strategies" && parts[2] == "imports":
		raw, readErr := io.ReadAll(http.MaxBytesReader(w, r.Body, 8<<20))
		if readErr != nil {
			err = app.ErrInvalid
			break
		}
		explicitNew := r.URL.Query().Get("new") == "1"
		prepared, prepareErr := b.prepareImport(r.Context(), raw)
		if prepareErr != nil {
			err = prepareErr
			break
		}
		batchKey := batchKeyForPrepared(prepared, r.Header.Get("Idempotency-Key"), explicitNew)
		now := time.Now().UTC()
		created, createErr := b.App.CreateOperationExcelBatch(r.Context(), ai.ExcelBatchCommand{Actor: ai.Actor{Kind: ai.ActorAdmin, ID: actor.InternalID}, IdempotencyKey: r.Header.Get("Idempotency-Key"), BatchKey: batchKey, StrategyKey: parts[1], Name: "Excel 群发批次 " + now.In(time.FixedZone("CST", 8*3600)).Format("2006-01-02 15:04"), Scope: b.Scope, FileDigest: prepared.FileDigest, Rows: prepared.batchRows(), OccurredAt: now})
		if createErr != nil {
			if errors.Is(createErr, app.ErrIdempotencyConflict) {
				err = &ComponentError{Status: http.StatusConflict, Code: "idempotency_conflict", Body: json.RawMessage(`{"error":"idempotency_conflict"}`)}
				break
			}
			if errors.Is(createErr, app.ErrConflict) {
				if existing, lookupErr := b.App.OperationExcelBatchByKey(r.Context(), batchKey); lookupErr == nil {
					body, _ := json.Marshal(map[string]any{"error": "duplicate_file", "existing_batch": map[string]any{"id": existing.PlanID, "operation_cycle_strategy_key": existing.StrategyKey}})
					err = &ComponentError{Status: http.StatusConflict, Code: "duplicate_file", Body: body}
					break
				}
			}
			err = createErr
			break
		}
		meta, metaErr := b.App.OperationExcelBatch(r.Context(), created.Plan.ID)
		if metaErr != nil {
			err = metaErr
			break
		}
		summary, summaryErr := b.App.OperationExcelBatchSummary(r.Context(), created.Plan.ID, meta.Revision)
		if summaryErr != nil {
			err = summaryErr
			break
		}
		output = map[string]any{"batch": batchJSON(created.Plan, meta, summary), "replayed": created.Replayed}
		if !created.Replayed {
			responseStatus = http.StatusCreated
		}
	case r.Method == http.MethodPost && len(parts) == 3 && parts[0] == "legacy" && parts[2] == "link":
		planID, parseErr := strconv.ParseInt(parts[1], 10, 64)
		if parseErr != nil || planID < 1 {
			err = app.ErrInvalid
			break
		}
		var input struct {
			StrategyKey     string `json:"strategy_key"`
			ExpectedVersion int64  `json:"expected_version"`
		}
		if decodeErr := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&input); decodeErr != nil {
			err = app.ErrInvalid
			break
		}
		plan, linkErr := b.App.LinkLegacyOperationExcelBatch(r.Context(), ai.Actor{Kind: ai.ActorAdmin, ID: actor.InternalID}, ai.PlanID(planID), input.ExpectedVersion, input.StrategyKey, r.Header.Get("Idempotency-Key"))
		if linkErr != nil {
			err = linkErr
			break
		}
		meta, metaErr := b.App.OperationExcelBatch(r.Context(), plan.ID)
		if metaErr != nil {
			err = metaErr
			break
		}
		summary, summaryErr := b.App.OperationExcelBatchSummary(r.Context(), plan.ID, meta.Revision)
		if summaryErr != nil {
			err = summaryErr
			break
		}
		output = map[string]any{"batch": batchJSON(plan, meta, summary)}
	case r.Method == "POST" && len(parts) == 1 && parts[0] == "imports":
		// The former unscoped uploader created an Excel plan without a long-plan
		// association. Keep the path observable but make new writes impossible.
		respond(w, http.StatusGone, map[string]any{"error": "legacy_write_disabled", "message": "请从长期计划详情上传 Excel"})
		return
	case r.Method == "GET" && len(parts) == 1 && parts[0] == "":
		var ids []ai.PlanID
		ids, err = b.Repo.ExcelPlans(r.Context())
		plans := []ai.Plan{}
		for i := len(ids) - 1; i >= 0 && err == nil; i-- {
			var p ai.Plan
			p, err = b.App.GetPlan(r.Context(), ids[i])
			plans = append(plans, p)
		}
		output = map[string]any{"items": plans}
	case r.Method == "GET" && len(parts) == 2 && parts[0] == "covers":
		var raw []byte
		err = b.Client.Call(r.Context(), "GET", "/covers/"+parts[1], "", nil, &raw)
		if err == nil {
			w.Header().Set("Content-Type", http.DetectContentType(raw))
			w.Header().Set("Cache-Control", "private, max-age=3600")
			w.Write(raw)
			return
		}
	default:
		n, e := strconv.ParseInt(parts[0], 10, 64)
		if e != nil || n < 1 {
			err = app.ErrInvalid
			break
		}
		id := ai.PlanID(n)
		var plan ai.Plan
		plan, err = b.App.GetPlan(r.Context(), id)
		if err != nil {
			break
		}
		if plan.SourceKind != "excel_batch" {
			err = app.ErrInvalid
			break
		}
		switch {
		case r.Method == "GET" && len(parts) == 2 && parts[1] == "versions":
			versions, versionsErr := b.App.OperationExcelBatchVersions(r.Context(), id, 100)
			if versionsErr != nil {
				err = versionsErr
				break
			}
			output = map[string]any{"batch_id": id, "items": versions}
		case r.Method == "GET" && len(parts) == 3 && parts[1] == "versions":
			revision, parseErr := strconv.Atoi(parts[2])
			if parseErr != nil || revision < 1 {
				err = app.ErrInvalid
				break
			}
			versions, versionsErr := b.App.OperationExcelBatchVersions(r.Context(), id, 100)
			if versionsErr != nil {
				err = versionsErr
				break
			}
			var selected *ai.ExcelBatchVersion
			for index := range versions {
				if versions[index].ContentRevision == revision {
					selected = &versions[index]
					break
				}
			}
			if selected == nil {
				err = app.ErrNotFound
				break
			}
			cursor, limit, pageErr := pageRequest(r)
			if pageErr != nil {
				err = pageErr
				break
			}
			rows, nextCursor, rowsErr := b.RowsPageForRevision(r.Context(), id, revision, cursor, limit)
			if rowsErr != nil {
				err = rowsErr
				break
			}
			summary, summaryErr := b.App.OperationExcelBatchSummary(r.Context(), id, revision)
			if summaryErr != nil {
				err = summaryErr
				break
			}
			output = map[string]any{"batch_id": id, "content_version": selected, "summary": summary, "rows": rows, "next_cursor": nextCursor, "read_only": true}
		case r.Method == "GET" && len(parts) == 1:
			meta, metaErr := b.App.OperationExcelBatch(r.Context(), id)
			if metaErr != nil {
				err = metaErr
				break
			}
			cursor, limit, pageErr := pageRequest(r)
			if pageErr != nil {
				err = pageErr
				break
			}
			rows, nextCursor, rowsErr := b.RowsPage(r.Context(), id, cursor, limit, false)
			if rowsErr != nil {
				err = rowsErr
				break
			}
			after, afterErr := b.App.OperationExcelBatch(r.Context(), id)
			if afterErr != nil {
				err = afterErr
				break
			}
			if after.Revision != meta.Revision || after.FileDigest != meta.FileDigest {
				err = app.ErrConflict
				break
			}
			summary, summaryErr := b.App.OperationExcelBatchSummary(r.Context(), id, meta.Revision)
			if summaryErr != nil {
				err = summaryErr
				break
			}
			output = map[string]any{"batch": batchJSON(plan, meta, summary), "rows": rows, "next_cursor": nextCursor}
		case r.Method == "GET" && len(parts) == 2 && parts[1] == "receipts":
			meta, metaErr := b.App.OperationExcelBatch(r.Context(), id)
			if metaErr != nil {
				err = metaErr
				break
			}
			cursor, limit, pageErr := pageRequest(r)
			if pageErr != nil {
				err = pageErr
				break
			}
			rows, nextCursor, rowsErr := b.RowsPage(r.Context(), id, cursor, limit, true)
			if rowsErr != nil {
				err = rowsErr
				break
			}
			after, afterErr := b.App.OperationExcelBatch(r.Context(), id)
			if afterErr != nil {
				err = afterErr
				break
			}
			if after.Revision != meta.Revision || after.FileDigest != meta.FileDigest {
				err = app.ErrConflict
				break
			}
			output = map[string]any{"batch_id": id, "content_version": meta.Revision, "items": rows, "next_cursor": nextCursor}
		case r.Method == http.MethodPut && len(parts) == 2 && parts[1] == "import":
			version, parseErr := strconv.ParseInt(r.URL.Query().Get("expected_version"), 10, 64)
			if parseErr != nil || version < 1 {
				err = app.ErrInvalid
				break
			}
			raw, readErr := io.ReadAll(http.MaxBytesReader(w, r.Body, 8<<20))
			if readErr != nil {
				err = app.ErrInvalid
				break
			}
			prepared, prepareErr := b.prepareImport(r.Context(), raw)
			if prepareErr != nil {
				err = prepareErr
				break
			}
			updated, replaceErr := b.App.ReplaceOperationExcelBatch(r.Context(), ai.ReplaceExcelBatchCommand{Actor: ai.Actor{Kind: ai.ActorAdmin, ID: actor.InternalID}, PlanID: id, ExpectedVersion: version, IdempotencyKey: r.Header.Get("Idempotency-Key"), Scope: b.Scope, FileDigest: prepared.FileDigest, Rows: prepared.batchRows(), OccurredAt: time.Now().UTC()})
			if replaceErr != nil {
				err = replaceErr
				break
			}
			meta, metaErr := b.App.OperationExcelBatch(r.Context(), id)
			if metaErr != nil {
				err = metaErr
				break
			}
			summary, summaryErr := b.App.OperationExcelBatchSummary(r.Context(), id, meta.Revision)
			if summaryErr != nil {
				err = summaryErr
				break
			}
			output = map[string]any{"batch": batchJSON(updated, meta, summary)}
		case r.Method == "GET" && len(parts) == 2 && parts[1] == "report.csv":
			var csv []byte
			err = b.Client.Raw(r.Context(), http.MethodGet, "/reports/"+fmtInt(n)+".csv", "", "application/json", nil, &csv)
			if err == nil {
				w.Header().Set("Content-Type", "text/csv; charset=utf-8")
				w.Header().Set("Content-Disposition", "attachment; filename=operation-batch-"+fmtInt(n)+".csv")
				w.Header().Set("Cache-Control", "no-store")
				_, _ = w.Write(csv)
				return
			}
		case r.Method == "GET" && len(parts) == 2 && parts[1] == "report":
			var report json.RawMessage
			err = b.Client.Call(r.Context(), "GET", "/reports/"+fmtInt(n), "", nil, &report)
			output = report
		case r.Method == "POST" && len(parts) == 2 && parts[1] == "cover":
			version, e := strconv.ParseInt(r.URL.Query().Get("expected_version"), 10, 64)
			if e != nil || version < 1 {
				err = app.ErrInvalid
				break
			}
			if b.Covers == nil || !validIdempotencyKey(r.Header.Get("Idempotency-Key")) {
				err = app.ErrUnavailable
				break
			}
			var cover mediaport.ExcelCover
			if strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
				var selectInput struct {
					ImageID int64 `json:"cover_image_id"`
				}
				if decodeErr := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&selectInput); decodeErr != nil || selectInput.ImageID < 1 {
					err = app.ErrInvalid
					break
				}
				cover, err = b.Covers.SelectEnabledExcelCover(r.Context(), selectInput.ImageID)
			} else {
				raw, readErr := io.ReadAll(http.MaxBytesReader(w, r.Body, 2<<20))
				if readErr != nil {
					err = app.ErrInvalid
					break
				}
				imageInfo, imageFormat, imageErr := image.DecodeConfig(bytes.NewReader(raw))
				if imageErr != nil || (imageFormat != "png" && imageFormat != "jpeg") || imageInfo.Width < 1 || imageInfo.Height < 1 || int64(imageInfo.Width)*int64(imageInfo.Height) > 40000000 {
					err = &InputError{Message: "请上传有效的 PNG 或 JPEG 封面（不超过 2 MB）"}
					break
				}
				fileName, declaredType := "excel-cover.png", "image/png"
				if imageFormat == "jpeg" {
					fileName, declaredType = "excel-cover.jpg", "image/jpeg"
				}
				cover, err = b.Covers.CreateOrReuseExcelCover(r.Context(), mediaport.ExcelCoverUpload{Actor: actor.InternalID, IdempotencyKey: mediaCoverKey(actor.InternalID, r.Header.Get("Idempotency-Key")), FileName: fileName, DeclaredType: declaredType, Content: raw})
			}
			if err != nil {
				break
			}
			digest := effect.Digest("sha256:" + hex.EncodeToString(cover.ContentDigest[:]))
			plan, err = b.App.ApplyExcelMediaCover(r.Context(), ai.Actor{Kind: ai.ActorAdmin, ID: actor.InternalID}, id, version, r.Header.Get("Idempotency-Key"), cover.ImageID, digest)
			if err != nil {
				break
			}
			meta, metaErr := b.App.OperationExcelBatch(r.Context(), id)
			if metaErr != nil {
				err = metaErr
				break
			}
			summary, summaryErr := b.App.OperationExcelBatchSummary(r.Context(), id, meta.Revision)
			if summaryErr != nil {
				err = summaryErr
				break
			}
			output = map[string]any{"batch": batchJSON(plan, meta, summary), "cover_digest": digest, "cover_image_id": cover.ImageID}
		case r.Method == http.MethodPatch && len(parts) == 3 && parts[1] == "rows":
			rowID, parseErr := strconv.ParseInt(parts[2], 10, 64)
			if parseErr != nil || rowID < 1 {
				err = app.ErrInvalid
				break
			}
			var input struct {
				ExpectedVersion int64  `json:"expected_version"`
				Text            string `json:"text"`
				Path            string `json:"path"`
				Title           string `json:"title"`
				Segment         string `json:"segment"`
				Excluded        bool   `json:"excluded"`
			}
			if decodeErr := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&input); decodeErr != nil {
				err = app.ErrInvalid
				break
			}
			updated, updateErr := b.App.UpdateOperationExcelRow(r.Context(), ai.UpdateExcelRowCommand{Actor: ai.Actor{Kind: ai.ActorAdmin, ID: actor.InternalID}, PlanID: id, RecipientID: ai.RecipientID(rowID), ExpectedVersion: input.ExpectedVersion, IdempotencyKey: r.Header.Get("Idempotency-Key"), Text: input.Text, Path: input.Path, Title: input.Title, Segment: input.Segment, Excluded: input.Excluded})
			if updateErr != nil {
				err = updateErr
				break
			}
			meta, metaErr := b.App.OperationExcelBatch(r.Context(), id)
			if metaErr != nil {
				err = metaErr
				break
			}
			summary, summaryErr := b.App.OperationExcelBatchSummary(r.Context(), id, meta.Revision)
			if summaryErr != nil {
				err = summaryErr
				break
			}
			output = map[string]any{"batch": batchJSON(updated, meta, summary)}
		case r.Method == http.MethodPost && len(parts) == 2 && parts[1] == "preview-approval":
			var input struct {
				Version int64 `json:"expected_version"`
			}
			if decodeErr := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&input); decodeErr != nil {
				err = app.ErrInvalid
				break
			}
			preview, previewErr := b.App.PreviewOperationExcelBatch(r.Context(), ai.PreviewApprovalCommand{Actor: ai.Actor{Kind: ai.ActorAdmin, ID: actor.InternalID}, PlanID: id, ExpectedVersion: input.Version})
			if previewErr != nil {
				err = previewErr
				break
			}
			meta, metaErr := b.App.OperationExcelBatch(r.Context(), id)
			if metaErr != nil {
				err = metaErr
				break
			}
			summary, summaryErr := b.App.OperationExcelBatchSummary(r.Context(), id, meta.Revision)
			if summaryErr != nil {
				err = summaryErr
				break
			}
			output = map[string]any{"preview_digest": preview.PreviewDigest, "eligible_count": preview.EligibleCount, "summary": summary}
		case r.Method == "POST" && len(parts) == 2 && parts[1] == "approve":
			var input struct {
				Version       int64         `json:"expected_version"`
				PreviewDigest effect.Digest `json:"preview_digest"`
			}
			err = json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&input)
			if err != nil {
				break
			}
			who := ai.Actor{Kind: ai.ActorAdmin, ID: actor.InternalID}
			// ApproveOperationExcelBatch repeats the preview-digest validation
			// inside its receipt-owning UoW. Do not preflight here: an HTTP retry
			// after a committed approval must replay its receipt instead of being
			// rejected because the plan has already moved to dispatching.
			plan, err = b.App.ApproveOperationExcelBatch(r.Context(), ai.ApprovePlanCommand{Actor: who, PlanID: id, ExpectedVersion: input.Version, PreviewDigest: input.PreviewDigest, IdempotencyKey: r.Header.Get("Idempotency-Key")})
			if err != nil {
				break
			}
			meta, metaErr := b.App.OperationExcelBatch(r.Context(), id)
			if metaErr != nil {
				err = metaErr
				break
			}
			summary, summaryErr := b.App.OperationExcelBatchSummary(r.Context(), id, meta.Revision)
			if summaryErr != nil {
				err = summaryErr
				break
			}
			output = map[string]any{"batch": batchJSON(plan, meta, summary)}
		default:
			err = app.ErrNotFound
		}
	}
	if err != nil {
		var componentError *ComponentError
		if errors.As(err, &componentError) {
			if json.Valid(componentError.Body) {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Cache-Control", "no-store")
				w.WriteHeader(componentError.Status)
				_, _ = w.Write(componentError.Body)
				return
			}
			respond(w, componentError.Status, map[string]any{"error": componentError.Code, "message": componentError.Message})
			return
		}
		var inputError *InputError
		if errors.As(err, &inputError) {
			respond(w, 400, map[string]any{"error": "invalid_input", "message": inputError.Message})
			return
		}
		status := 503
		if errors.Is(err, app.ErrIdempotencyConflict) {
			respond(w, http.StatusConflict, map[string]any{"error": "idempotency_conflict"})
			return
		}
		if errors.Is(err, app.ErrInvalid) {
			status = 400
		}
		if errors.Is(err, app.ErrConflict) {
			status = 409
		}
		if errors.Is(err, app.ErrNotFound) {
			status = 404
		}
		respond(w, status, map[string]any{"error": "batch_request_failed"})
		return
	}
	respond(w, responseStatus, output)
}

func mediaCoverKey(actorID int64, clientKey string) string {
	sum := sha256.Sum256([]byte("excel-cover\x00" + strconv.FormatInt(actorID, 10) + "\x00" + clientKey))
	return "excel-cover-" + hex.EncodeToString(sum[:])
}
func respond(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
