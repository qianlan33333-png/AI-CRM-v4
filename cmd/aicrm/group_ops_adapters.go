package main

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accessport "github.com/qianlan33333-png/AI-CRM-v3/internal/access/port"
	externaleffects "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	groupopsapp "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/app"
	groupopsport "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/port"
	groupopsmaterial "github.com/qianlan33333-png/AI-CRM-v3/internal/media/groupopsmaterial"
	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

// providerDisabledGroupOpsDirectory is a concrete id-dev source adapter. It
// makes the missing real directory provider explicit at composition time;
// local directory reads and opaque owner selection still use Group Ops' own
// projection, while refresh never fabricates a provider snapshot.
type providerDisabledGroupOpsDirectory struct{}

func (providerDisabledGroupOpsDirectory) ListOwnedGroups(context.Context, int64, int32) (groupopsport.GroupDirectorySnapshot, error) {
	return groupopsport.GroupDirectorySnapshot{}, groupopsapp.ErrProviderDisabled
}

func (providerDisabledGroupOpsDirectory) RefreshOperationMembers(context.Context, int32) ([]groupopsport.OperationMember, error) {
	return nil, groupopsapp.ErrProviderDisabled
}

// providerDisabledGroupOpsEvidence is deliberately non-nil in Composition.
// It refuses to turn an HTTP digest into delivery evidence until an approved
// owner-side Provider receipt verifier is installed.
type providerDisabledGroupOpsEvidence struct{}

func (providerDisabledGroupOpsEvidence) VerifyReconciliationEvidence(context.Context, groupopsport.ReconciliationEvidence) (groupopsport.ReconciliationEvidenceResult, error) {
	return groupopsport.ReconciliationEvidenceResult{}, groupopsapp.ErrProviderDisabled
}

var _ groupopsport.GroupDirectorySource = providerDisabledGroupOpsDirectory{}
var _ groupopsport.ReconciliationEvidenceVerifier = providerDisabledGroupOpsEvidence{}

// wecomGroupOpsEvidence performs its provider pagination outside every UoW,
// then returns only a digest-bound observation. A msgid alone is task
// acceptance; delivery is true only for a matching sender/chat result status.
type wecomGroupOpsEvidence struct {
	uow interface {
		Within(context.Context, func(context.Context) error) error
	}
	receipts groupopsport.GroupMessageReceiptReader
	reader   wecomport.GroupMessageTaskReader
}

func (adapter wecomGroupOpsEvidence) VerifyReconciliationEvidence(ctx context.Context, input groupopsport.ReconciliationEvidence) (groupopsport.ReconciliationEvidenceResult, error) {
	if adapter.uow == nil || adapter.receipts == nil || adapter.reader == nil {
		return groupopsport.ReconciliationEvidenceResult{}, groupopsapp.ErrProviderDisabled
	}
	var receipt groupopsport.GroupMessageReceipt
	var found bool
	err := adapter.uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		receipt, found, readErr = adapter.receipts.FindGroupMessageReceipt(tx, input)
		return readErr
	})
	if err != nil || !found {
		if err != nil {
			return groupopsport.ReconciliationEvidenceResult{}, err
		}
		return groupopsport.ReconciliationEvidenceResult{}, groupopsapp.ErrProviderDisabled
	}
	cursor := ""
	seen := map[string]struct{}{}
	for {
		page, readErr := adapter.reader.GetGroupMessageSendResult(ctx, receipt.MessageID, receipt.SenderUserID, cursor, 100)
		if readErr != nil {
			return groupopsport.ReconciliationEvidenceResult{}, readErr
		}
		for _, item := range page.Items {
			if item.SenderUserID != receipt.SenderUserID || item.ChatID != receipt.ChatID {
				continue
			}
			digest := string(effectport.Hash("group-ops.wecom-delivery.v1", receipt.MessageID, item.SenderUserID, item.ChatID, strconv.Itoa(item.Status)))
			if digest != input.EvidenceDigest {
				return groupopsport.ReconciliationEvidenceResult{}, groupopsapp.ErrConflict
			}
			return groupopsport.ReconciliationEvidenceResult{DeliveryProven: item.Status == 1, EvidenceDigest: digest}, nil
		}
		if page.NextCursor == "" {
			break
		}
		if _, duplicate := seen[page.NextCursor]; duplicate {
			return groupopsport.ReconciliationEvidenceResult{}, groupopsapp.ErrConflict
		}
		seen[page.NextCursor] = struct{}{}
		cursor = page.NextCursor
	}
	return groupopsport.ReconciliationEvidenceResult{}, groupopsapp.ErrProviderDisabled
}

func (adapter wecomGroupOpsEvidence) ReadProviderDelivery(ctx context.Context, input groupopsport.ReconciliationEvidence) (groupopsport.GroupMessageReceipt, bool, error) {
	if adapter.uow == nil || adapter.receipts == nil || adapter.reader == nil || input.ExecutionID < 1 || input.ExternalEffectID == "" {
		return groupopsport.GroupMessageReceipt{}, false, groupopsapp.ErrProviderDisabled
	}
	var receipt groupopsport.GroupMessageReceipt
	var found bool
	if err := adapter.uow.Within(ctx, func(tx context.Context) error {
		var err error
		receipt, found, err = adapter.receipts.FindGroupMessageReceipt(tx, input)
		return err
	}); err != nil || !found {
		return groupopsport.GroupMessageReceipt{}, false, err
	}
	cursor, seen := "", map[string]struct{}{}
	for {
		page, err := adapter.reader.GetGroupMessageSendResult(ctx, receipt.MessageID, receipt.SenderUserID, cursor, 100)
		if err != nil {
			return groupopsport.GroupMessageReceipt{}, false, err
		}
		for _, item := range page.Items {
			if item.SenderUserID == receipt.SenderUserID && item.ChatID == receipt.ChatID {
				status := item.Status
				receipt.DeliveryStatus = &status
				receipt.DeliveryEvidenceDigest = string(effectport.Hash("group-ops.wecom-delivery.v1", receipt.MessageID, item.SenderUserID, item.ChatID, strconv.Itoa(item.Status)))
				return receipt, true, nil
			}
		}
		if page.NextCursor == "" {
			return groupopsport.GroupMessageReceipt{}, false, nil
		}
		if _, duplicate := seen[page.NextCursor]; duplicate {
			return groupopsport.GroupMessageReceipt{}, false, groupopsapp.ErrConflict
		}
		seen[page.NextCursor] = struct{}{}
		cursor = page.NextCursor
	}
}

var _ groupopsport.ReconciliationEvidenceVerifier = wecomGroupOpsEvidence{}
var _ groupopsport.ProviderDeliveryReader = wecomGroupOpsEvidence{}

// wecomGroupOpsDirectory performs every provider read before RuntimeService
// opens its persistence transaction. A full snapshot is required before a
// replacement is allowed, so failed pagination cannot erase the prior group
// projection.
type wecomGroupOpsDirectory struct {
	uow interface {
		Within(context.Context, func(context.Context) error) error
	}
	enabled  bool
	groups   wecomport.GroupChatReader
	staffs   wecomport.DirectoryProvider
	profiles wecomport.ContactStaffProfileReader
	staff    groupOpsStaffAdapter
	now      func() time.Time
}

func (adapter *wecomGroupOpsDirectory) ListOwnedGroups(ctx context.Context, ownerID int64, _ int32) (groupopsport.GroupDirectorySnapshot, error) {
	if adapter == nil || !adapter.enabled || adapter.uow == nil || adapter.groups == nil || adapter.staff.access == nil || ownerID < 1 {
		return groupopsport.GroupDirectorySnapshot{}, groupopsapp.ErrProviderDisabled
	}
	var owner accessdomain.User
	err := adapter.uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		owner, readErr = adapter.staff.access.UserByID(tx, ownerID, false)
		return readErr
	})
	if err != nil {
		if errors.Is(err, accessdomain.ErrNotFound) {
			return groupopsport.GroupDirectorySnapshot{}, groupopsapp.ErrProviderDisabled
		}
		return groupopsport.GroupDirectorySnapshot{}, err
	}
	if !owner.Active || !validGroupOpsSenderID(owner.WeComUserID) {
		return groupopsport.GroupDirectorySnapshot{}, groupopsapp.ErrProviderDisabled
	}
	cursor := ""
	seenCursor := map[string]struct{}{}
	seenChat := map[string]struct{}{}
	items := make([]groupopsport.GroupDirectoryItem, 0)
	now := time.Now().UTC()
	if adapter.now != nil {
		now = adapter.now().UTC()
	}
	for {
		page, pageErr := adapter.groups.ListGroupChats(ctx, owner.WeComUserID, cursor, 100)
		if pageErr != nil {
			return groupopsport.GroupDirectorySnapshot{}, groupOpsDirectoryFailure("list", pageErr)
		}
		for _, summary := range page.Items {
			if summary.Status != 0 {
				continue
			}
			if _, duplicate := seenChat[summary.ChatID]; duplicate {
				return groupopsport.GroupDirectorySnapshot{}, groupopsapp.NewGroupDirectoryReadError("pagination", "provider_response_invalid")
			}
			seenChat[summary.ChatID] = struct{}{}
			detail, detailErr := adapter.groups.GetGroupChat(ctx, summary.ChatID)
			if detailErr != nil {
				return groupopsport.GroupDirectorySnapshot{}, groupOpsDirectoryFailure("detail", detailErr)
			}
			if detail.ChatID != summary.ChatID {
				return groupopsport.GroupDirectorySnapshot{}, groupopsapp.NewGroupDirectoryReadError("detail", "provider_response_invalid")
			}
			if detail.OwnerUserID != owner.WeComUserID {
				return groupopsport.GroupDirectorySnapshot{}, groupopsapp.NewGroupDirectoryReadError("detail", "owner_mismatch")
			}
			items = append(items, groupopsport.GroupDirectoryItem{ChatReference: detail.ChatID, OwnerStaffID: ownerID, DisplayName: detail.Name, MemberCount: int32(detail.MemberCount), ExternalMemberCount: detail.ExternalMemberCount, RefreshedAt: now})
		}
		if page.NextCursor == "" {
			break
		}
		if _, repeated := seenCursor[page.NextCursor]; repeated {
			return groupopsport.GroupDirectorySnapshot{}, groupopsapp.NewGroupDirectoryReadError("pagination", "provider_response_invalid")
		}
		seenCursor[page.NextCursor] = struct{}{}
		cursor = page.NextCursor
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].ChatReference < items[j].ChatReference })
	return groupopsport.GroupDirectorySnapshot{Items: items, Complete: true}, nil
}

func groupOpsDirectoryFailure(stage string, err error) error {
	code := "provider_unavailable"
	var failure wecomport.DirectoryFailure
	if errors.As(err, &failure) {
		code = failure.DirectoryFailureCode()
	}
	if errors.Is(err, context.DeadlineExceeded) {
		code = "provider_timeout"
	}
	return groupopsapp.NewGroupDirectoryReadError(stage, code)
}

func (adapter *wecomGroupOpsDirectory) RefreshOperationMembers(ctx context.Context, pageSize int32) ([]groupopsport.OperationMember, error) {
	if adapter == nil || !adapter.enabled || adapter.uow == nil || adapter.staffs == nil || adapter.staff.access == nil || !adapter.staffs.DirectoryReady() {
		return nil, groupopsapp.ErrProviderDisabled
	}
	var local []groupopsport.OperationMember
	err := adapter.uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		local, readErr = adapter.staff.ListEligibleStaff(tx)
		return readErr
	})
	if err != nil {
		return nil, err
	}
	providerIDs, err := adapter.staffs.ListContactStaff(ctx)
	if err != nil {
		return nil, err
	}
	allowed := make(map[string]struct{}, len(providerIDs))
	for _, id := range providerIDs {
		if validGroupOpsSenderID(id) {
			allowed[id] = struct{}{}
		}
	}
	items := make([]groupopsport.OperationMember, 0, len(local))
	for _, item := range local {
		if _, found := allowed[item.SenderUserID]; found {
			item.Active = true
			item.NameSource = "local_fallback"
			item.ProfileReadState = "unavailable"
			item.ProfileReadErrorCode = "provider_profile_unavailable"
			items = append(items, item)
		}
	}
	if len(items) > int(pageSize) {
		return nil, errors.New("WeCom operation-member snapshot exceeds requested page")
	}
	if len(items) == 0 {
		return items, nil
	}
	if adapter.profiles == nil {
		for index := range items {
			items[index].ProfileReadErrorCode = "profile_reader_unavailable"
		}
		return items, nil
	}
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.SenderUserID)
	}
	profiles, profileErr := adapter.profiles.ReadContactStaffProfiles(ctx, ids)
	state, code := profiles.ProfileReadState, profiles.ProfileErrorCode
	if profileErr != nil {
		state, code = "unavailable", groupOpsProfileFailureCode(profileErr)
	}
	if state != "ready" {
		state = "unavailable"
		if code == "" {
			code = "provider_profile_unavailable"
		}
	}
	names := make(map[string]string, len(profiles.Items))
	for _, profile := range profiles.Items {
		if validGroupOpsSenderID(profile.UserID) && validGroupOpsDisplayName(profile.DisplayName) {
			names[profile.UserID] = profile.DisplayName
		}
	}
	now := time.Now().UTC()
	if adapter.now != nil {
		now = adapter.now().UTC()
	}
	for index := range items {
		items[index].ProfileReadState, items[index].ProfileReadErrorCode = state, code
		if name, found := names[items[index].SenderUserID]; found {
			items[index].DisplayName = name
			items[index].NameSource = "wecom_profile"
			items[index].ProfileRefreshedAt = &now
		}
	}
	return items, nil
}

func groupOpsProfileFailureCode(err error) string {
	var failure wecomport.DirectoryFailure
	if errors.As(err, &failure) && failure.DirectoryFailureCode() != "" {
		return failure.DirectoryFailureCode()
	}
	return "provider_profile_unavailable"
}

func validGroupOpsDisplayName(value string) bool {
	return value != "" && value == strings.TrimSpace(value) && len([]rune(value)) <= 160 && !strings.ContainsAny(value, "\x00\r\n")
}

var _ groupopsport.GroupDirectorySource = (*wecomGroupOpsDirectory)(nil)

// groupOpsDispatchReader rechecks the current target owner/sender under the
// Group Ops UoW immediately before outbound crosses the provider boundary.
// A paused plan, changed group binding, or ineligible sender is therefore a
// deterministic pre-dispatch failure, not a send attempt.
type groupOpsDispatchReader struct {
	uow interface {
		Within(context.Context, func(context.Context) error) error
	}
	execution groupopsport.DispatchExecutionReader
	senders   groupopsport.ExecutionSenderResolver
}

func (adapter groupOpsDispatchReader) LoadDispatchExecution(ctx context.Context, effectID string) (groupopsport.DispatchExecution, error) {
	if adapter.uow == nil || adapter.execution == nil || adapter.senders == nil || effectID == "" {
		return groupopsport.DispatchExecution{}, errors.New("Group Ops dispatch reader is unavailable")
	}
	var execution groupopsport.DispatchExecution
	err := adapter.uow.Within(ctx, func(tx context.Context) error {
		value, loadErr := adapter.execution.LoadDispatchExecution(tx, effectID)
		if loadErr != nil {
			return loadErr
		}
		sender, found, senderErr := adapter.senders.ResolveExecutionSender(tx, value.TargetReference)
		if senderErr != nil || !found || sender != value.SenderUserID {
			return errors.New("Group Ops sender eligibility changed")
		}
		execution = value
		return nil
	})
	return execution, err
}

var _ groupopsport.DispatchExecutionReader = groupOpsDispatchReader{}

// groupOpsExternalReconciler is the only Composition Root bridge from the
// Group Ops domain to EER control. It keeps the EER operation and the Group Ops
// execution projection inside the caller's transaction; Group Ops never
// imports EER store/app/http/worker packages.
type groupOpsExternalReconciler struct {
	repository *externaleffects.Repository
}

func (adapter groupOpsExternalReconciler) ReconcileExternalEffect(ctx context.Context, command groupopsport.ExternalReconcileCommand) error {
	if adapter.repository == nil || command.ActorID < 1 || command.EffectID == "" || command.Generation < 1 || command.Fence < 1 || command.LeaseExpiresAt.IsZero() || !externaleffects.ValidDigest(externaleffects.Digest(command.EvidenceDigest)) {
		return errors.New("Group Ops external reconciliation is unavailable")
	}
	// The HTTP idempotency key is intentionally not used as an EER digest. The
	// adapter derives a stable opaque receipt key so raw protocol/user keys do
	// not cross the EER boundary or enter structured logs.
	receiptKey := externaleffects.Hash("group-ops.execution-reconcile", command.EffectID, command.ReceiptKey, command.EvidenceDigest)
	return adapter.repository.ReconcileWithin(ctx, externaleffects.ControlCommand{
		EffectID:         command.EffectID,
		ReceiptKey:       receiptKey,
		EvidenceDigest:   externaleffects.Digest(command.EvidenceDigest),
		ActorAdminUserID: command.ActorID,
		Generation:       command.Generation,
		Fence:            command.Fence,
		LeaseExpiresAt:   command.LeaseExpiresAt,
	})
}

var _ groupopsport.ExternalReconciler = groupOpsExternalReconciler{}

// groupOpsMaterialAdapter is a narrow Composition Root adapter. Media owns
// both ports and therefore owns source locking/preparation; Group Ops receives
// only the frozen JSON snapshot and its digest. In particular, this adapter
// does not derive a digest from a kind/id pair or reopen a mutable package.
type groupOpsMaterialAdapter struct {
	capturer            mediaport.GroupOpsMaterialSourceCapturer
	freezer             mediaport.GroupOpsMaterialSnapshotFreezer
	sources             outboundport.MaterialSourceReader
	preparer            outboundport.MaterialPreparer
	scopeDigest         string
	webhookMiniPrograms mediaport.WebhookMiniProgramResolver
}

// groupOpsMaterialReadinessAdapter repeats Media's capture/read boundary
// immediately before a write. It never prepares or uploads material: changed
// source content, a changed receipt digest, or an expired ReadyUntil fails
// before the outbound adapter crosses the Provider boundary.
type groupOpsMaterialReadinessAdapter struct {
	uow interface {
		Within(context.Context, func(context.Context) error) error
	}
	capturer mediaport.GroupOpsMaterialSourceCapturer
	freezer  mediaport.GroupOpsMaterialSnapshotFreezer
}

func (adapter groupOpsMaterialReadinessAdapter) VerifyFrozenMaterialSources(ctx context.Context, snapshotRaw, factsRaw json.RawMessage, factsDigest string) error {
	canonicalSnapshot, snapshotErr := canonicalGroupOpsJSON(snapshotRaw)
	canonicalFacts, factsErr := canonicalGroupOpsJSON(factsRaw)
	if adapter.uow == nil || adapter.capturer == nil || snapshotErr != nil || factsErr != nil || !effectport.ValidDigest(effectport.Digest(factsDigest)) || factsDigest != string(effectport.Hash("group-ops.material.intent.v1", string(canonicalFacts))) {
		return errors.New("Group Ops frozen material sources unavailable")
	}
	if emptyGroupOpsMaterialIntent(canonicalSnapshot, canonicalFacts) {
		return nil
	}
	var facts struct {
		SchemaVersion int                                      `json:"schema_version"`
		Sources       mediaport.GroupOpsMaterialSourceSnapshot `json:"sources"`
	}
	if json.Unmarshal(canonicalFacts, &facts) != nil || facts.SchemaVersion != 1 || mediaport.ValidateGroupOpsMaterialSourceSnapshot(facts.Sources) != nil {
		return errors.New("invalid frozen Group Ops material facts")
	}
	plan := mediaport.GroupOpsMaterialPlan{References: make([]mediaport.GroupOpsMaterialReference, len(facts.Sources.References))}
	for i, source := range facts.Sources.References {
		plan.References[i] = source.Reference
	}
	return adapter.uow.Within(ctx, func(tx context.Context) error {
		current, err := adapter.capturer.CaptureGroupOpsMaterialSources(tx, plan)
		if err != nil {
			return err
		}
		currentRaw, err := json.Marshal(current)
		frozenRaw, frozenErr := json.Marshal(facts.Sources)
		if err != nil || frozenErr != nil || string(currentRaw) != string(frozenRaw) {
			return errors.New("Group Ops material source changed")
		}
		return nil
	})
}

func (adapter groupOpsMaterialReadinessAdapter) VerifyMaterialReady(ctx context.Context, snapshotRaw, factsRaw json.RawMessage, factsDigest string, now time.Time) error {
	canonicalSnapshot, snapshotErr := canonicalGroupOpsJSON(snapshotRaw)
	canonicalFacts, factsCanonicalErr := canonicalGroupOpsJSON(factsRaw)
	if adapter.uow == nil || adapter.capturer == nil || adapter.freezer == nil || snapshotErr != nil || factsCanonicalErr != nil || !effectport.ValidDigest(effectport.Digest(factsDigest)) || factsDigest != string(effectport.Hash("group-ops.material.intent.v1", string(canonicalFacts))) || now.IsZero() {
		return errors.New("Group Ops material readiness unavailable")
	}
	// A text-only message has no Media-owned source to recapture or provider
	// preparation to re-read. RuntimeService persists this canonical empty
	// intent itself, so accepting it here preserves the same frozen-facts
	// boundary without inventing a Media record merely to send text.
	if emptyGroupOpsMaterialIntent(canonicalSnapshot, canonicalFacts) {
		return nil
	}
	var facts struct {
		SchemaVersion int                                      `json:"schema_version"`
		Sources       mediaport.GroupOpsMaterialSourceSnapshot `json:"sources"`
		Preparations  []groupopsmaterial.PreparedMaterial      `json:"preparations"`
	}
	if json.Unmarshal(canonicalFacts, &facts) != nil || facts.SchemaVersion != 1 || mediaport.ValidateGroupOpsMaterialSourceSnapshot(facts.Sources) != nil {
		return errors.New("invalid frozen Group Ops material facts")
	}
	plan := mediaport.GroupOpsMaterialPlan{References: make([]mediaport.GroupOpsMaterialReference, len(facts.Sources.References))}
	for i, source := range facts.Sources.References {
		plan.References[i] = source.Reference
	}
	return adapter.uow.Within(ctx, func(tx context.Context) error {
		current, err := adapter.capturer.CaptureGroupOpsMaterialSources(tx, plan)
		if err != nil {
			return err
		}
		currentRaw, err := json.Marshal(current)
		frozenRaw, frozenErr := json.Marshal(facts.Sources)
		if err != nil || frozenErr != nil || string(currentRaw) != string(frozenRaw) {
			return errors.New("Group Ops material source changed")
		}
		withFacts, ok := adapter.freezer.(interface {
			FreezeGroupOpsMaterialWithFacts(context.Context, mediaport.GroupOpsMaterialSourceSnapshot, time.Time) (mediaport.GroupOpsMaterialSnapshot, []groupopsmaterial.PreparedMaterial, error)
		})
		if !ok {
			return errors.New("Group Ops material preparation facts unavailable")
		}
		snapshot, prepared, err := withFacts.FreezeGroupOpsMaterialWithFacts(tx, current, now.UTC())
		if err != nil {
			return err
		}
		actualSnapshot, marshalErr := json.Marshal(snapshot)
		actualFacts, factsErr := json.Marshal(struct {
			SchemaVersion int                                      `json:"schema_version"`
			Sources       mediaport.GroupOpsMaterialSourceSnapshot `json:"sources"`
			Preparations  []groupopsmaterial.PreparedMaterial      `json:"preparations"`
		}{1, current, prepared})
		actualSnapshot, marshalErr = canonicalGroupOpsJSON(actualSnapshot)
		actualFacts, factsErr = canonicalGroupOpsJSON(actualFacts)
		if marshalErr != nil || factsErr != nil || string(actualSnapshot) != string(canonicalSnapshot) || string(actualFacts) != string(canonicalFacts) {
			return errors.New("Group Ops material preparation changed or expired")
		}
		return nil
	})
}

func emptyGroupOpsMaterialIntent(snapshotRaw, factsRaw []byte) bool {
	var snapshot struct {
		SchemaVersion int               `json:"schema_version"`
		References    []json.RawMessage `json:"references"`
	}
	var facts struct {
		SchemaVersion int `json:"schema_version"`
		Sources       struct {
			SchemaVersion int               `json:"schema_version"`
			References    []json.RawMessage `json:"references"`
		} `json:"sources"`
		Preparations []json.RawMessage `json:"preparations"`
	}
	return json.Unmarshal(snapshotRaw, &snapshot) == nil && snapshot.SchemaVersion == 1 && snapshot.References != nil && len(snapshot.References) == 0 &&
		json.Unmarshal(factsRaw, &facts) == nil && facts.SchemaVersion == 1 && facts.Sources.SchemaVersion == 1 && facts.Sources.References != nil && len(facts.Sources.References) == 0 && facts.Preparations != nil && len(facts.Preparations) == 0
}

func canonicalGroupOpsJSON(raw []byte) ([]byte, error) {
	var value any
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil {
		return nil, errors.New("invalid Group Ops material JSON")
	}
	return json.Marshal(value)
}

var _ groupopsport.MaterialReadinessVerifier = groupOpsMaterialReadinessAdapter{}
var _ groupopsport.FrozenMaterialSourceVerifier = groupOpsMaterialReadinessAdapter{}

func (adapter groupOpsMaterialAdapter) ResolveMaterialSnapshot(ctx context.Context, plan groupopsport.MaterialPlan, requiredThrough time.Time) (json.RawMessage, string, error) {
	mediaPlan := mediaport.GroupOpsMaterialPlan{References: make([]mediaport.GroupOpsMaterialReference, len(plan.References))}
	for index, reference := range plan.References {
		mediaPlan.References[index] = mediaport.GroupOpsMaterialReference{Kind: reference.Kind, ID: reference.ID}
	}
	if adapter.capturer == nil || adapter.freezer == nil || mediaport.ValidateGroupOpsMaterialPlan(mediaPlan) != nil {
		return nil, "", errors.New("Group Ops material ports are unavailable")
	}
	sources, err := adapter.capturer.CaptureGroupOpsMaterialSources(ctx, mediaPlan)
	if err != nil {
		return nil, "", err
	}
	snapshot, err := adapter.freezer.FreezeGroupOpsMaterial(ctx, sources, requiredThrough)
	if err != nil || mediaport.ValidateGroupOpsMaterialSnapshot(snapshot) != nil {
		return nil, "", errors.New("Group Ops material is not ready")
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return nil, "", err
	}
	return raw, string(effectport.Hash("group-ops.material.snapshot.v1", string(raw))), nil
}

func (adapter groupOpsMaterialAdapter) ResolveMaterialIntentSnapshot(ctx context.Context, plan groupopsport.MaterialPlan, requiredThrough time.Time) (json.RawMessage, string, json.RawMessage, string, error) {
	if adapter.capturer == nil || adapter.freezer == nil || ctx == nil || requiredThrough.IsZero() {
		return nil, "", nil, "", errors.New("Group Ops material ports are unavailable")
	}
	mediaPlan := mediaport.GroupOpsMaterialPlan{References: make([]mediaport.GroupOpsMaterialReference, len(plan.References))}
	for index, reference := range plan.References {
		mediaPlan.References[index] = mediaport.GroupOpsMaterialReference{Kind: reference.Kind, ID: reference.ID}
	}
	if err := mediaport.ValidateGroupOpsMaterialPlan(mediaPlan); err != nil {
		return nil, "", nil, "", err
	}
	sources, err := adapter.capturer.CaptureGroupOpsMaterialSources(ctx, mediaPlan)
	if err != nil {
		return nil, "", nil, "", err
	}
	if mediaport.ValidateGroupOpsMaterialSourceSnapshot(sources) != nil {
		return nil, "", nil, "", errors.New("invalid Group Ops material source snapshot")
	}
	snapshot := groupOpsMaterialIntentSnapshot(sources)
	if mediaport.ValidateGroupOpsMaterialIntentSnapshot(snapshot) != nil {
		return nil, "", nil, "", errors.New("invalid Group Ops material intent snapshot")
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return nil, "", nil, "", err
	}
	raw, err = canonicalGroupOpsJSON(raw)
	if err != nil {
		return nil, "", nil, "", err
	}
	facts := struct {
		SchemaVersion int                                      `json:"schema_version"`
		Sources       mediaport.GroupOpsMaterialSourceSnapshot `json:"sources"`
		Preparations  []groupopsmaterial.PreparedMaterial      `json:"preparations"`
	}{SchemaVersion: 1, Sources: sources, Preparations: []groupopsmaterial.PreparedMaterial{}}
	factsRaw, err := json.Marshal(facts)
	if err != nil {
		return nil, "", nil, "", err
	}
	factsRaw, err = canonicalGroupOpsJSON(factsRaw)
	if err != nil {
		return nil, "", nil, "", err
	}
	return raw, string(effectport.Hash("group-ops.material.snapshot.v1", string(raw))), factsRaw, string(effectport.Hash("group-ops.material.intent.v1", string(factsRaw))), nil
}

func groupOpsMaterialIntentSnapshot(sources mediaport.GroupOpsMaterialSourceSnapshot) mediaport.GroupOpsMaterialIntentSnapshot {
	attachments := make([]mediaport.GroupOpsProviderReadyAttachment, len(sources.References))
	for index, source := range sources.References {
		switch source.Reference.Kind {
		case "image":
			attachments[index].MsgType = "image"
		case "attachment":
			attachments[index].MsgType = "file"
		case "miniprogram", "group_invite":
			attachments[index] = source.ProviderFields
		}
	}
	return mediaport.GroupOpsMaterialIntentSnapshot{SchemaVersion: 2, NodeKind: "message", Attachments: attachments}
}

var _ groupopsport.MaterialSnapshotResolver = groupOpsMaterialAdapter{}
var _ groupopsport.MaterialIntentSnapshotResolver = groupOpsMaterialAdapter{}
var _ mediaport.WebhookMiniProgramResolver = groupOpsMaterialAdapter{}

func (adapter groupOpsMaterialAdapter) PrepareWebhookMiniProgram(ctx context.Context, request mediaport.WebhookMiniProgramRequest) (mediaport.PreparedWebhookMiniProgram, error) {
	if adapter.webhookMiniPrograms == nil {
		return mediaport.PreparedWebhookMiniProgram{}, mediaport.ErrWebhookMiniProgramUnavailable
	}
	return adapter.webhookMiniPrograms.PrepareWebhookMiniProgram(ctx, request)
}

func (adapter groupOpsMaterialAdapter) MaterializeWebhookMiniProgramWithin(ctx context.Context, prepared mediaport.PreparedWebhookMiniProgram, command mediaport.WebhookMiniProgramMaterialization) (mediaport.GroupOpsMaterialReference, error) {
	if adapter.webhookMiniPrograms == nil {
		return mediaport.GroupOpsMaterialReference{}, mediaport.ErrWebhookMiniProgramUnavailable
	}
	return adapter.webhookMiniPrograms.MaterializeWebhookMiniProgramWithin(ctx, prepared, command)
}

func newGroupOpsMaterialAdapter(capturer mediaport.GroupOpsMaterialSourceCapturer, freezer mediaport.GroupOpsMaterialSnapshotFreezer, webhookMiniPrograms ...mediaport.WebhookMiniProgramResolver) (groupopsport.MaterialSnapshotResolver, error) {
	if capturer == nil || freezer == nil {
		return nil, errors.New("Media Group Ops material ports are unavailable")
	}
	adapter := groupOpsMaterialAdapter{capturer: capturer, freezer: freezer}
	if len(webhookMiniPrograms) > 0 {
		adapter.webhookMiniPrograms = webhookMiniPrograms[0]
	}
	return adapter, nil
}

func newUnifiedGroupOpsMaterialAdapter(capturer mediaport.GroupOpsMaterialSourceCapturer, freezer mediaport.GroupOpsMaterialSnapshotFreezer, sources outboundport.MaterialSourceReader, preparer outboundport.MaterialPreparer, scopeDigest string, webhookMiniPrograms ...mediaport.WebhookMiniProgramResolver) (groupopsport.MaterialSnapshotResolver, error) {
	if sources == nil || preparer == nil || !effectport.ValidDigest(effectport.Digest(scopeDigest)) {
		return nil, errors.New("Outbound Group Ops material preparation ports are unavailable")
	}
	base, err := newGroupOpsMaterialAdapter(capturer, freezer, webhookMiniPrograms...)
	if err != nil {
		return nil, err
	}
	adapter := base.(groupOpsMaterialAdapter)
	adapter.sources, adapter.preparer, adapter.scopeDigest = sources, preparer, scopeDigest
	if len(webhookMiniPrograms) > 0 {
		adapter.webhookMiniPrograms = webhookMiniPrograms[0]
	}
	return adapter, nil
}

// mediaPreparedPlanReader is the Composition Root adapter from Media's
// transaction-bound preparation port to the freezer's provider-neutral typed
// reader. Group invite links are already provider-ready facts from the real
// Media capture and therefore do not need a receipt/lease row. Image,
// attachment, and miniprogram items must come from Media's persisted
// preparation receipt with an unexpired lease; this adapter never derives a
// media ID or digest from kind/id.
type mediaPreparedPlanReader struct {
	reader      mediaport.GroupOpsMaterialPreparationReader // legacy test fixture field; production does not bind it
	sources     outboundport.MaterialSourceReader
	preparer    outboundport.MaterialStatusReader
	scopeDigest string
}

func (adapter mediaPreparedPlanReader) ReadPreparedGroupOpsPlan(ctx context.Context, sources mediaport.GroupOpsMaterialSourceSnapshot, requiredThrough time.Time) (groupopsmaterial.PreparedPlan, error) {
	if ctx == nil || requiredThrough.IsZero() || mediaport.ValidateGroupOpsMaterialSourceSnapshot(sources) != nil {
		return groupopsmaterial.PreparedPlan{}, groupopsmaterial.ErrUnavailable
	}
	items := make([]mediaport.GroupOpsMaterialPreparation, 0, len(sources.References))
	for _, frozen := range sources.References {
		if frozen.Reference.Kind == "group_invite" {
			items = append(items, mediaport.GroupOpsMaterialPreparation{Reference: frozen.Reference, SourceDigest: frozen.SourceDigest, Attachment: frozen.ProviderFields})
			continue
		}
		if adapter.sources == nil || adapter.preparer == nil || !effectport.ValidDigest(effectport.Digest(adapter.scopeDigest)) {
			return groupopsmaterial.PreparedPlan{}, groupopsmaterial.ErrUnavailable
		}
		sourceRef, expected := groupOpsUnifiedSourceRef(frozen)
		source, err := adapter.sources.GetSourceSnapshot(ctx, sourceRef)
		if err != nil || sourceSnapshotDigest(source) != expected {
			return groupopsmaterial.PreparedPlan{}, groupopsmaterial.ErrUnavailable
		}
		result, err := adapter.preparer.GetMaterialStatus(ctx, source, adapter.scopeDigest)
		if err != nil || !result.CredentialUsable || result.MediaID == "" || !result.ExpiresAt.After(requiredThrough) {
			return groupopsmaterial.PreparedPlan{}, groupopsmaterial.ErrUnavailable
		}
		attachment := frozen.ProviderFields
		attachment.MediaID = result.MediaID
		items = append(items, mediaport.GroupOpsMaterialPreparation{Reference: frozen.Reference, SourceDigest: frozen.SourceDigest, ReceiptDigest: string(effectport.Hash("groupops.unified-material.receipt.v1", result.EffectID, result.MediaID, result.ProviderCreatedAt.UTC().Format(time.RFC3339Nano))), ReadyUntil: result.ExpiresAt, Attachment: attachment})
	}
	if mediaport.ValidateGroupOpsMaterialPreparations(sources, items, requiredThrough) != nil {
		return groupopsmaterial.PreparedPlan{}, groupopsmaterial.ErrUnavailable
	}
	prepared := make([]groupopsmaterial.PreparedMaterial, len(items))
	for index, item := range items {
		prepared[index] = groupopsmaterial.PreparedMaterial{Reference: item.Reference, SourceDigest: item.SourceDigest, ReceiptDigest: item.ReceiptDigest, ReadyUntil: item.ReadyUntil, Attachment: item.Attachment}
	}
	return groupopsmaterial.PreparedPlan{Items: prepared}, nil
}

func groupOpsUnifiedSourceRef(source mediaport.GroupOpsMaterialSourceReference) (string, string) {
	switch source.Reference.Kind {
	case "image":
		return "image:" + strconv.FormatInt(source.Reference.ID, 10), source.SourceDigest
	case "attachment":
		return "attachment:" + strconv.FormatInt(source.Reference.ID, 10), source.SourceDigest
	case "miniprogram":
		return "image:" + strconv.FormatInt(source.ThumbnailImageID, 10), source.ThumbnailSourceDigest
	default:
		return "", ""
	}
}

var _ groupopsmaterial.PreparedPlanReader = mediaPreparedPlanReader{}

// groupOpsStaffAdapter is the Composition Root's only bridge from Group Ops
// to staff records. Group Ops stores only an opaque owner_staff_id and never
// reaches across to admin_users; Access remains the owner of staff identity,
// active state, and the verified WeCom sender identifier.
type groupOpsStaffAdapter struct {
	access accessport.Repository
	owners groupopsport.ExecutionTargetOwnerResolver
}

func (adapter groupOpsStaffAdapter) IsActiveStaff(ctx context.Context, staffID int64) (bool, error) {
	if adapter.access == nil || staffID < 1 {
		return false, errors.New("Group Ops staff access is unavailable")
	}
	user, err := adapter.access.UserByID(ctx, staffID, false)
	if errors.Is(err, accessdomain.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return user.Active, nil
}

func (adapter groupOpsStaffAdapter) ListEligibleStaff(ctx context.Context) ([]groupopsport.OperationMember, error) {
	if adapter.access == nil {
		return nil, errors.New("Group Ops staff access is unavailable")
	}
	users, err := adapter.access.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]groupopsport.OperationMember, 0, len(users))
	for _, user := range users {
		if !user.Active || user.ID < 1 || user.WeComUserID == "" {
			continue
		}
		if !validGroupOpsSenderID(user.WeComUserID) {
			// A malformed legacy binding is never exposed as a selectable
			// sender. It remains an Access-owned repair item instead.
			continue
		}
		items = append(items, groupopsport.OperationMember{StaffID: user.ID, SenderUserID: user.WeComUserID, DisplayName: user.DisplayName})
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].StaffID < items[j].StaffID })
	return items, nil
}

func (adapter groupOpsStaffAdapter) ResolveExecutionSender(ctx context.Context, target string) (string, bool, error) {
	if adapter.access == nil || adapter.owners == nil || target == "" {
		return "", false, errors.New("Group Ops sender access is unavailable")
	}
	owner, found, err := adapter.owners.ResolveExecutionOwner(ctx, target)
	if err != nil || !found || owner < 1 {
		return "", false, err
	}
	user, err := adapter.access.UserByID(ctx, owner, false)
	if errors.Is(err, accessdomain.ErrNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if !user.Active || user.WeComUserID == "" {
		return "", false, nil
	}
	if !validGroupOpsSenderID(user.WeComUserID) {
		return "", false, nil
	}
	return user.WeComUserID, true, nil
}

func validGroupOpsSenderID(value string) bool {
	if value == "" || len(value) > 128 || value != strings.TrimSpace(value) {
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

var _ groupopsport.ActiveStaffReader = groupOpsStaffAdapter{}
var _ groupopsport.EligibleStaffReader = groupOpsStaffAdapter{}
var _ groupopsport.ExecutionSenderResolver = groupOpsStaffAdapter{}
