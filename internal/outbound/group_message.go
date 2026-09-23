package outbound

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	aiassistantport "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/port"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	groupopsport "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/port"
	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

var ErrGroupMessageProviderDisabled = errors.New("Group Ops message provider is disabled")

// GroupMessageProvider is the composition-time carrier for the bounded Group
// Ops outbound adapter. Its disabled mode is deterministic and makes no
// network call. The preparation writer is a stable Media port reserved for an
// explicitly enabled, approved Provider adapter; disabled mode can never use
// it to manufacture a preparation receipt.
type GroupMessageProvider struct {
	enabled           bool
	preparationWriter mediaport.GroupOpsMaterialPreparationWriter
	executions        groupopsport.DispatchExecutionReader
	materials         groupopsport.MaterialReadinessVerifier
	frozenSources     groupopsport.FrozenMaterialSourceVerifier
	writer            wecomport.GroupMessageSender
	sources           outboundport.MaterialSourceReader
	preparer          outboundport.MaterialPreparer
	scopeDigest       string
}

type GroupMessageProviderConfig struct {
	Enabled           bool
	PreparationWriter mediaport.GroupOpsMaterialPreparationWriter
	Executions        groupopsport.DispatchExecutionReader
	Materials         groupopsport.MaterialReadinessVerifier
	FrozenSources     groupopsport.FrozenMaterialSourceVerifier
	Writer            wecomport.GroupMessageSender
	Sources           outboundport.MaterialSourceReader
	Preparer          outboundport.MaterialPreparer
	ScopeDigest       string
}

func NewGroupMessageProvider(config GroupMessageProviderConfig) (*GroupMessageProvider, error) {
	return &GroupMessageProvider{enabled: config.Enabled, preparationWriter: config.PreparationWriter, executions: config.Executions, materials: config.Materials, frozenSources: config.FrozenSources, writer: config.Writer, sources: config.Sources, preparer: config.Preparer, scopeDigest: config.ScopeDigest}, nil
}

// RecordPreparedMaterials is the only preparation write seam exposed to a
// future approved adapter. It is intentionally unavailable in the current
// provider-disabled composition and delegates persistence to Media ownership.
func (p *GroupMessageProvider) RecordPreparedMaterials(ctx context.Context, command mediaport.GroupOpsMaterialPreparationCommand) (mediaport.GroupOpsMaterialPreparationReceipt, error) {
	if p == nil || !p.enabled || p.preparationWriter == nil {
		return mediaport.GroupOpsMaterialPreparationReceipt{}, ErrGroupMessageProviderDisabled
	}
	return p.preparationWriter.RecordPreparedGroupOpsMaterials(ctx, command)
}

// Preflight prepares every Media-backed source before EER records an attempt.
// A Group Ops receipt remains the immutable business evidence, while Outbound
// owns the provider credential cache and returns only fresh Media IDs here.
func (p *GroupMessageProvider) Preflight(ctx context.Context, envelope effectport.Envelope, effectID string) (bool, time.Duration, error) {
	if p == nil || !p.enabled || envelope.Kind != effectport.KindGroupMessage || !envelope.Valid() {
		return true, 0, nil
	}
	execution, err := p.loadExecution(ctx, envelope, effectID)
	if err != nil {
		// Execute repeats the same current dispatch check and projects a local
		// final failure without crossing the Provider boundary. Let it do so
		// instead of leaving an accepted intent queued forever on a revision,
		// sender, or target-binding change.
		return true, 0, nil
	}
	_, err = p.readyGroupMedia(ctx, execution)
	if err == nil {
		return true, 0, nil
	}
	var pending outboundport.MediaPreparationPendingError
	if errors.As(err, &pending) {
		return false, pending.RetryAfter(), nil
	}
	var terminal outboundport.MediaPreparationTerminalError
	if errors.As(err, &terminal) || errors.Is(err, outboundport.ErrMaterialSourceChanged) || errors.Is(err, outboundport.ErrMaterialPreparationNotFound) {
		return true, 0, nil
	}
	return false, 0, err
}

func (p *GroupMessageProvider) loadExecution(ctx context.Context, envelope effectport.Envelope, effectID string) (groupopsport.DispatchExecution, error) {
	if p == nil || p.executions == nil || effectID == "" {
		return groupopsport.DispatchExecution{}, errors.New("group dispatch unavailable")
	}
	execution, err := p.executions.LoadDispatchExecution(ctx, effectID)
	if err != nil || execution.ExternalEffectID != effectID || execution.State != groupopsport.ExecutionAccepted || execution.DeliveryProven ||
		execution.SourceRefDigest != string(envelope.SourceRefDigest) || execution.TargetRefDigest != string(envelope.TargetRefDigest) || execution.PayloadDigest != string(envelope.PayloadDigest) || execution.PolicyVersionHash != string(envelope.PolicyVersionHash) {
		return groupopsport.DispatchExecution{}, errors.New("group dispatch unavailable")
	}
	return execution, nil
}

func (p *GroupMessageProvider) readyGroupMedia(ctx context.Context, execution groupopsport.DispatchExecution) (map[string]string, error) {
	if emptyGroupMessageMaterialSources(execution.MaterialSourceSnapshot) || (len(execution.MaterialSourceSnapshot) == 0 && emptyGroupMessageMaterial(execution.MaterialSnapshot)) {
		// Preserve read-only compatibility for historical text-only executions.
		if p.materials != nil {
			if err := p.materials.VerifyMaterialReady(ctx, execution.MaterialSnapshot, execution.MaterialSourceSnapshot, execution.MaterialSourceDigest, time.Now().UTC()); err != nil {
				return nil, err
			}
		}
		return map[string]string{}, nil
	}
	if p.frozenSources == nil || p.sources == nil || p.preparer == nil || !effectport.ValidDigest(effectport.Digest(p.scopeDigest)) {
		return nil, errors.New("material preparation unavailable")
	}
	// This verifies the Group Ops receipt/frozen source evidence without
	// consulting an old provider-media lease. Generic preparation below owns
	// current credential validity and refresh.
	if err := p.frozenSources.VerifyFrozenMaterialSources(ctx, execution.MaterialSnapshot, execution.MaterialSourceSnapshot, execution.MaterialSourceDigest); err != nil {
		return nil, err
	}
	frozen, err := groupMessageMaterialSources(execution.MaterialSourceSnapshot)
	if err != nil {
		return nil, errors.New("invalid group material source snapshot")
	}
	if err := validateGroupMaterialSequence(execution.MaterialSnapshot, frozen); err != nil {
		return nil, err
	}
	prepared := make(map[string]string, len(frozen.References))
	for _, reference := range frozen.References {
		if reference.Reference.Kind == "group_invite" {
			continue
		}
		sourceRef, expected := groupMaterialSourceRef(reference)
		if sourceRef == "" || !effectport.ValidDigest(effectport.Digest(expected)) {
			return nil, errors.New("invalid frozen media source")
		}
		source, err := p.sources.GetSourceSnapshot(ctx, sourceRef)
		if err != nil {
			return nil, err
		}
		if groupMaterialSourceDigest(source) != expected {
			return nil, errors.New("frozen media source drift")
		}
		result, err := p.preparer.ReadyForSend(ctx, outboundport.MaterialRequest{MaterialSourceSnapshot: source, CorpScopeDigest: p.scopeDigest, ValidThrough: time.Now().UTC().Add(30 * time.Second)})
		if err != nil {
			return nil, err
		}
		if result.State != "ready" || strings.TrimSpace(result.MediaID) == "" {
			code := strings.TrimSpace(result.FailureCode)
			if code == "" {
				code = "media_preparation_pending"
			}
			return nil, outboundport.MediaPreparationPendingError{Code: code, After: time.Second}
		}
		prepared[groupMaterialKey(reference.Reference)] = result.MediaID
	}
	return prepared, nil
}

func groupMaterialSourceRef(reference mediaport.GroupOpsMaterialSourceReference) (string, string) {
	switch reference.Reference.Kind {
	case "image":
		return "image:" + strconv.FormatInt(reference.Reference.ID, 10), reference.SourceDigest
	case "attachment":
		return "attachment:" + strconv.FormatInt(reference.Reference.ID, 10), reference.SourceDigest
	case "miniprogram":
		return "image:" + strconv.FormatInt(reference.ThumbnailImageID, 10), reference.ThumbnailSourceDigest
	default:
		return "", ""
	}
}
func groupMaterialKey(reference mediaport.GroupOpsMaterialReference) string {
	return reference.Kind + ":" + strconv.FormatInt(reference.ID, 10)
}
func groupMaterialSourceDigest(source outboundport.MaterialSourceSnapshot) string {
	return "sha256:" + hex.EncodeToString(source.ContentDigest[:])
}

func validateGroupMaterialSequence(raw json.RawMessage, sources mediaport.GroupOpsMaterialSourceSnapshot) error {
	attachments, err := groupMaterialSnapshotAttachments(raw)
	if err != nil || len(attachments) != len(sources.References) {
		return errors.New("invalid Group Ops material/source sequence")
	}
	for index, source := range sources.References {
		want := source.Reference.Kind
		if want == "attachment" {
			want = "file"
		}
		if want == "miniprogram" {
			want = "miniprogram"
		}
		if want == "group_invite" {
			want = "link"
		}
		if attachments[index].MsgType != want {
			return errors.New("Group Ops material/source order mismatch")
		}
	}
	return nil
}

func groupMaterialSnapshotAttachments(raw json.RawMessage) ([]mediaport.GroupOpsProviderReadyAttachment, error) {
	var intent mediaport.GroupOpsMaterialIntentSnapshot
	if json.Unmarshal(raw, &intent) == nil && mediaport.ValidateGroupOpsMaterialIntentSnapshot(intent) == nil {
		return intent.Attachments, nil
	}
	var ready mediaport.GroupOpsMaterialSnapshot
	if json.Unmarshal(raw, &ready) == nil && mediaport.ValidateGroupOpsMaterialSnapshot(ready) == nil {
		return ready.Attachments, nil
	}
	return nil, errors.New("invalid Group Ops material snapshot")
}

func (p *GroupMessageProvider) Execute(ctx context.Context, envelope effectport.Envelope, attempt effectport.Attempt) (effectport.AdapterResult, error) {
	if p == nil || envelope.Kind != effectport.KindGroupMessage || !envelope.Valid() {
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash("group-ops-provider-disabled", "invalid-envelope")}, nil
	}
	if !p.enabled {
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash("group-ops-provider-disabled", string(envelope.Fingerprint())), CallAttempted: false, RealExternalCallExecuted: false}, nil
	}
	base := effectport.Hash("group-ops.provider.v1", string(envelope.Fingerprint()), attempt.EffectID, strconv.Itoa(int(attempt.Number)), strconv.FormatInt(attempt.Generation, 10), strconv.FormatInt(attempt.Fence, 10))
	if p.executions == nil || p.materials == nil || p.writer == nil || attempt.EffectID == "" {
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash(string(base), "not-configured")}, nil
	}
	execution, err := p.loadExecution(ctx, envelope, attempt.EffectID)
	if err != nil {
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash(string(base), "dispatch-unavailable")}, nil
	}
	prepared, err := p.readyGroupMedia(ctx, execution)
	if err != nil {
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash(string(base), "material-not-ready"), CallAttempted: false, RealExternalCallExecuted: false}, nil
	}
	request, err := groupMessageRequestWithPreparedMedia(execution, prepared)
	if err != nil {
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash(string(base), "snapshot-invalid")}, nil
	}
	receipt, attempted, err := p.writer.SendGroupMessage(ctx, request)
	if err != nil {
		state := effectport.StateRetryable
		if attempted {
			state = effectport.StateUnknown
			if rejected, ok := err.(wecomport.GroupMessageSendError); ok && !rejected.OutcomeUnknown() {
				state = effectport.StateFinalFailed
			}
		}
		return effectport.AdapterResult{Completion: state, ReceiptDigest: effectport.Hash(string(base), "provider-error"), CallAttempted: attempted, RealExternalCallExecuted: attempted}, nil
	}
	if !attempted || strings.TrimSpace(receipt.MessageID) == "" {
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash(string(base), "provider-rejected")}, nil
	}
	artifactPayload, artifactErr := json.Marshal(struct {
		ExecutionID string `json:"execution_id"`
		EffectID    string `json:"effect_id"`
		MessageID   string `json:"msgid"`
		Sender      string `json:"sender"`
		ChatID      string `json:"chat_id"`
	}{strconv.FormatInt(execution.ExecutionID, 10), attempt.EffectID, receipt.MessageID, request.SenderUserID, request.ChatIDs[0]})
	if artifactErr != nil {
		return effectport.AdapterResult{Completion: effectport.StateUnknown, ReceiptDigest: effectport.Hash(string(base), "receipt-artifact-unavailable"), CallAttempted: true, RealExternalCallExecuted: true}, nil
	}
	artifact := effectport.ResultArtifact{Kind: "group-ops.wecom-task.v1", Payload: artifactPayload}
	artifact.Digest = effectport.Hash("external-effect.artifact.v1", artifact.Kind, string(artifact.Payload))
	return effectport.AdapterResult{Completion: effectport.StateExecuted, ReceiptDigest: effectport.Hash(string(base), "provider-accepted", receipt.MessageID), CallAttempted: true, RealExternalCallExecuted: true, Artifact: artifact}, nil
}

func canonicalGroupMessageJSON(raw []byte) ([]byte, error) {
	var value any
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil {
		return nil, errors.New("invalid Group Ops snapshot JSON")
	}
	return json.Marshal(value)
}

func emptyGroupMessageMaterial(raw []byte) bool {
	var value struct {
		SchemaVersion int               `json:"schema_version"`
		References    []json.RawMessage `json:"references"`
	}
	return json.Unmarshal(raw, &value) == nil && value.SchemaVersion == 1 && len(value.References) == 0
}

func emptyGroupMessageMaterialSources(raw []byte) bool {
	var direct struct {
		SchemaVersion int               `json:"schema_version"`
		References    []json.RawMessage `json:"references"`
	}
	if json.Unmarshal(raw, &direct) == nil && direct.SchemaVersion == 1 && direct.References != nil {
		return len(direct.References) == 0
	}
	var facts struct {
		SchemaVersion int `json:"schema_version"`
		Sources       struct {
			SchemaVersion int               `json:"schema_version"`
			References    []json.RawMessage `json:"references"`
		} `json:"sources"`
		Preparations []json.RawMessage `json:"preparations"`
	}
	return json.Unmarshal(raw, &facts) == nil && facts.SchemaVersion == 1 && facts.Sources.SchemaVersion == 1 &&
		facts.Sources.References != nil && len(facts.Sources.References) == 0 && facts.Preparations != nil && len(facts.Preparations) == 0
}

func groupMessageMaterialSources(raw []byte) (mediaport.GroupOpsMaterialSourceSnapshot, error) {
	var direct mediaport.GroupOpsMaterialSourceSnapshot
	if json.Unmarshal(raw, &direct) == nil && mediaport.ValidateGroupOpsMaterialSourceSnapshot(direct) == nil {
		return direct, nil
	}
	var facts struct {
		SchemaVersion int                                      `json:"schema_version"`
		Sources       mediaport.GroupOpsMaterialSourceSnapshot `json:"sources"`
	}
	if json.Unmarshal(raw, &facts) != nil || facts.SchemaVersion != 1 || mediaport.ValidateGroupOpsMaterialSourceSnapshot(facts.Sources) != nil {
		return mediaport.GroupOpsMaterialSourceSnapshot{}, errors.New("invalid group material source facts")
	}
	return facts.Sources, nil
}

func groupMessageRequest(execution groupopsport.DispatchExecution) (wecomport.GroupMessageRequest, error) {
	return groupMessageRequestWithPreparedMedia(execution, nil)
}

// groupMessageRequestWithPreparedMedia accepts only IDs returned by the
// unified preparation port for uploadable Media. Frozen Group Ops fields still
// supply card/link semantics; their historical receipt IDs are never reused.
func groupMessageRequestWithPreparedMedia(execution groupopsport.DispatchExecution, prepared map[string]string) (wecomport.GroupMessageRequest, error) {
	if execution.ExecutionID < 1 || execution.TargetReference == "" || strings.TrimSpace(execution.SenderUserID) != execution.SenderUserID || execution.SenderUserID == "" || !effectport.ValidDigest(effectport.Digest(execution.ContentDigest)) || !effectport.ValidDigest(effectport.Digest(execution.MaterialDigest)) {
		return wecomport.GroupMessageRequest{}, errors.New("invalid Group Ops dispatch execution")
	}
	var content struct {
		SchemaVersion   int                                   `json:"schema_version"`
		Kind            string                                `json:"kind"`
		MessageText     string                                `json:"message_text"`
		AttachmentOrder []mediaport.GroupOpsMaterialReference `json:"attachment_order"`
	}
	if json.Unmarshal(execution.ContentSnapshot, &content) != nil || ((content.SchemaVersion != 1 || content.Kind != "message") && (content.SchemaVersion != 2 || content.Kind != "webhook_message")) || strings.TrimSpace(content.MessageText) != content.MessageText {
		return wecomport.GroupMessageRequest{}, errors.New("invalid Group Ops content snapshot")
	}
	if content.SchemaVersion == 1 && content.AttachmentOrder != nil {
		return wecomport.GroupMessageRequest{}, errors.New("invalid legacy Group Ops content snapshot")
	}
	canonicalContent, canonicalErr := canonicalGroupMessageJSON(execution.ContentSnapshot)
	if canonicalErr != nil || string(effectport.Hash("group-ops.content.snapshot.v1", string(canonicalContent))) != execution.ContentDigest {
		return wecomport.GroupMessageRequest{}, errors.New("Group Ops content digest mismatch")
	}
	canonicalMaterial, canonicalErr := canonicalGroupMessageJSON(execution.MaterialSnapshot)
	if canonicalErr != nil || string(effectport.Hash("group-ops.material.snapshot.v1", string(canonicalMaterial))) != execution.MaterialDigest {
		return wecomport.GroupMessageRequest{}, errors.New("invalid Group Ops material snapshot")
	}
	attachments := []wecomport.GroupMessageAttachment{}
	var webhookSources mediaport.GroupOpsMaterialSourceSnapshot
	if content.SchemaVersion == 2 && len(content.AttachmentOrder) > 0 {
		var sourceErr error
		webhookSources, sourceErr = groupMessageMaterialSources(execution.MaterialSourceSnapshot)
		if sourceErr != nil || !webhookAttachmentOrderMatches(content.AttachmentOrder, webhookSources) {
			return wecomport.GroupMessageRequest{}, errors.New("invalid Group Ops webhook attachment order")
		}
	}
	if !emptyGroupMessageMaterial(canonicalMaterial) {
		var materialAttachments []mediaport.GroupOpsProviderReadyAttachment
		var sources mediaport.GroupOpsMaterialSourceSnapshot
		if prepared != nil {
			materialAttachments, canonicalErr = groupMaterialSnapshotAttachments(canonicalMaterial)
			if canonicalErr == nil {
				sources, canonicalErr = groupMessageMaterialSources(execution.MaterialSourceSnapshot)
			}
			if canonicalErr != nil || validateGroupMaterialSequence(canonicalMaterial, sources) != nil {
				return wecomport.GroupMessageRequest{}, errors.New("invalid Group Ops material source snapshot")
			}
		} else {
			var ready mediaport.GroupOpsMaterialSnapshot
			if json.Unmarshal(canonicalMaterial, &ready) != nil || mediaport.ValidateGroupOpsMaterialSnapshot(ready) != nil {
				return wecomport.GroupMessageRequest{}, errors.New("invalid Group Ops material snapshot")
			}
			materialAttachments = ready.Attachments
		}
		attachments = make([]wecomport.GroupMessageAttachment, len(materialAttachments))
		for index, attachment := range materialAttachments {
			mediaID := attachment.MediaID
			if prepared != nil && attachment.MsgType != "link" {
				mediaID = prepared[groupMaterialKey(sources.References[index].Reference)]
				if strings.TrimSpace(mediaID) == "" {
					return wecomport.GroupMessageRequest{}, errors.New("prepared group media missing")
				}
			}
			attachments[index] = wecomport.GroupMessageAttachment{MsgType: attachment.MsgType, MediaID: mediaID, AppID: attachment.AppID, PagePath: attachment.PagePath, Title: attachment.Title, URL: attachment.URL, Description: attachment.Description, PicURL: attachment.PicURL}
		}
	}
	if content.SchemaVersion == 2 && len(content.AttachmentOrder) != len(attachments) {
		return wecomport.GroupMessageRequest{}, errors.New("Group Ops webhook attachment count mismatch")
	}
	if content.MessageText == "" && len(attachments) == 0 {
		return wecomport.GroupMessageRequest{}, errors.New("Group Ops message is empty")
	}
	return wecomport.GroupMessageRequest{SenderUserID: execution.SenderUserID, ChatIDs: []string{execution.TargetReference}, Text: content.MessageText, Attachments: attachments}, nil
}

func webhookAttachmentOrderMatches(order []mediaport.GroupOpsMaterialReference, sources mediaport.GroupOpsMaterialSourceSnapshot) bool {
	if len(order) != len(sources.References) {
		return false
	}
	for index, reference := range order {
		if reference != sources.References[index].Reference {
			return false
		}
	}
	return true
}

// DisabledGroupMessageProvider is the deterministic default adapter for the
// Group Ops outbound kind. It makes no network call and returns a valid
// receipt digest so the EER worker records an auditable final_failed outcome.
type DisabledGroupMessageProvider struct{}

func NewDisabledGroupMessageProvider() *DisabledGroupMessageProvider {
	return &DisabledGroupMessageProvider{}
}

func (p *DisabledGroupMessageProvider) Execute(_ context.Context, envelope effectport.Envelope, _ effectport.Attempt) (effectport.AdapterResult, error) {
	if envelope.Kind != effectport.KindGroupMessage || !envelope.Valid() {
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash("group-ops-provider-disabled", "invalid-envelope")}, nil
	}
	return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash("group-ops-provider-disabled", string(envelope.Fingerprint())), CallAttempted: false, RealExternalCallExecuted: false}, nil
}

// GroupMessageCompletionProjector is implemented by the Group Ops store. It
// is intentionally expressed as a local projection method so outbound never
// imports Group Ops app/store/http/worker packages.
type GroupMessageCompletionProjector interface {
	CompleteEffect(context.Context, string, groupopsport.ExecutionState, bool, bool, string, int32, time.Time) error
}

type GroupMessageCompletionSink struct {
	projector     GroupMessageCompletionProjector
	receipts      groupopsport.GroupMessageReceiptWriter
	continuations groupopsport.ExecutionContinuationEnqueuer
}

func NewGroupMessageCompletionSink(projector GroupMessageCompletionProjector, receipts ...groupopsport.GroupMessageReceiptWriter) (*GroupMessageCompletionSink, error) {
	if projector == nil || len(receipts) > 1 {
		return nil, errors.New("Group Ops completion projector is required")
	}
	sink := &GroupMessageCompletionSink{projector: projector}
	if len(receipts) == 1 {
		sink.receipts = receipts[0]
	}
	return sink, nil
}

func (s *GroupMessageCompletionSink) WithContinuation(enqueuer groupopsport.ExecutionContinuationEnqueuer) *GroupMessageCompletionSink {
	if s != nil {
		s.continuations = enqueuer
	}
	return s
}

func (s *GroupMessageCompletionSink) CompleteEffect(ctx context.Context, effectRef string, envelope effectport.Envelope, attempt effectport.Attempt, result effectport.AdapterResult) error {
	if s == nil || s.projector == nil || envelope.Kind != effectport.KindGroupMessage || !effectport.ValidDigest(result.ReceiptDigest) {
		return errors.New("invalid Group Ops completion")
	}
	state := groupopsport.ExecutionOutcomeUnknown
	providerAccepted := false
	deliveryProven := false
	switch result.Completion {
	case effectport.StateExecuted:
		state = groupopsport.ExecutionProviderAccepted
		providerAccepted = result.CallAttempted && result.RealExternalCallExecuted
	case effectport.StateFinalFailed:
		state = groupopsport.ExecutionFinalFailed
	case effectport.StateUnknown:
		state = groupopsport.ExecutionOutcomeUnknown
	case effectport.StateRetryable:
		state = groupopsport.ExecutionOutcomeUnknown
	default:
		return errors.New("invalid Group Ops completion state")
	}
	if result.Completion == effectport.StateExecuted {
		if s.receipts == nil {
			return errors.New("Group Ops task receipt writer is required")
		}
		var artifact struct {
			ExecutionID string `json:"execution_id"`
			EffectID    string `json:"effect_id"`
			MessageID   string `json:"msgid"`
			Sender      string `json:"sender"`
			ChatID      string `json:"chat_id"`
		}
		if result.Artifact.Kind != "group-ops.wecom-task.v1" || !result.Artifact.Valid() || json.Unmarshal(result.Artifact.Payload, &artifact) != nil || artifact.EffectID != effectRef || artifact.MessageID == "" || artifact.Sender == "" || artifact.ChatID == "" {
			return errors.New("invalid Group Ops task receipt artifact")
		}
		executionID, parseErr := strconv.ParseInt(artifact.ExecutionID, 10, 64)
		if parseErr != nil || executionID < 1 {
			return errors.New("invalid Group Ops task receipt execution")
		}
		if err := s.receipts.RecordGroupMessageTask(ctx, groupopsport.GroupMessageReceipt{ExecutionID: executionID, ExternalEffectID: effectRef, MessageID: artifact.MessageID, SenderUserID: artifact.Sender, ChatID: artifact.ChatID, TaskEvidenceDigest: string(result.Artifact.Digest)}); err != nil {
			return err
		}
	}
	if err := s.projector.CompleteEffect(ctx, effectRef, state, providerAccepted, deliveryProven, string(result.ReceiptDigest), attempt.Number, time.Now().UTC()); err != nil {
		return err
	}
	if result.Completion == effectport.StateExecuted && s.continuations != nil {
		return s.continuations.EnqueueGroupOpsContinuationWithin(ctx, effectRef)
	}
	return nil
}

// CompletionRouter keeps EER's single completion-sink slot while routing
// owner-specific projections by opaque envelope kind.
type CompletionRouter struct {
	invitationCode     effectport.CompletionSink
	sidebarMedia       effectport.CompletionSink
	tag                *TagCatalogCompletionSink
	tagMutation        effectport.CompletionSink
	group              *GroupMessageCompletionSink
	channel            *ChannelAssetCompletionSink
	entrant            *ChannelEntrantCompletionSink
	link               *ChannelLinkCompletionSink
	private            *PrivateMessageCompletionSink
	automation         effectport.CompletionSink
	sidebar            effectport.CompletionSink
	survey             effectport.CompletionSink
	customerTag        effectport.CompletionSink
	ownerHandoff       effectport.CompletionSink
	commerce           effectport.CompletionSink
	contactDescription effectport.CompletionSink
}

func NewCompletionRouterWithChannels(tag *TagCatalogCompletionSink, group *GroupMessageCompletionSink, channel *ChannelAssetCompletionSink) (*CompletionRouter, error) {
	if tag == nil && group == nil && channel == nil {
		return nil, errors.New("at least one completion sink is required")
	}
	return &CompletionRouter{tag: tag, group: group, channel: channel}, nil
}

func NewCompletionRouterWithChannelEntrants(tag *TagCatalogCompletionSink, group *GroupMessageCompletionSink, channel *ChannelAssetCompletionSink, entrant *ChannelEntrantCompletionSink) (*CompletionRouter, error) {
	if tag == nil && group == nil && channel == nil && entrant == nil {
		return nil, errors.New("at least one completion sink is required")
	}
	return &CompletionRouter{tag: tag, group: group, channel: channel, entrant: entrant}, nil
}
func NewCompletionRouterWithAllChannels(tag *TagCatalogCompletionSink, group *GroupMessageCompletionSink, channel *ChannelAssetCompletionSink, entrant *ChannelEntrantCompletionSink, link *ChannelLinkCompletionSink) (*CompletionRouter, error) {
	if tag == nil && group == nil && channel == nil && entrant == nil && link == nil {
		return nil, errors.New("at least one completion sink is required")
	}
	return &CompletionRouter{tag: tag, group: group, channel: channel, entrant: entrant, link: link}, nil
}

type PrivateMessageCompletionSink struct {
	outbound PrivateMessageCompletionProjector
	ai       aiassistantport.EffectCompletionProjector
}

func NewPrivateMessageCompletionSink(outbound PrivateMessageCompletionProjector, ai aiassistantport.EffectCompletionProjector) (*PrivateMessageCompletionSink, error) {
	if outbound == nil || ai == nil {
		return nil, errors.New("private message completion projectors are required")
	}
	return &PrivateMessageCompletionSink{outbound: outbound, ai: ai}, nil
}

func (s *PrivateMessageCompletionSink) CompleteEffect(ctx context.Context, effectRef string, envelope effectport.Envelope, attempt effectport.Attempt, result effectport.AdapterResult) error {
	if s == nil || envelope.Kind != effectport.KindOutboundMessage || !effectport.ValidDigest(result.ReceiptDigest) {
		return errors.New("invalid private message completion")
	}
	state := aiassistantport.ExecutionOutcomeUnknown
	providerAccepted := false
	deliveryProven := false
	switch result.Completion {
	case effectport.StateExecuted:
		state = aiassistantport.ExecutionProviderAccepted
		providerAccepted = result.CallAttempted && result.RealExternalCallExecuted
	case effectport.StateFinalFailed:
		state = aiassistantport.ExecutionFinalFailed
	case effectport.StateUnknown:
		state = aiassistantport.ExecutionOutcomeUnknown
	case effectport.StateRetryable:
		state = aiassistantport.ExecutionRetryableFailed
	case effectport.StateReconciled:
		state = aiassistantport.ExecutionReconciled
	default:
		return errors.New("invalid private message completion state")
	}
	if err := s.outbound.CompletePrivateMessage(ctx, effectRef, string(state), time.Now().UTC()); err != nil {
		return err
	}
	return s.ai.CompleteExternalEffect(ctx, effectRef, state, providerAccepted, deliveryProven, result.ReceiptDigest, attempt.Number, attempt.Generation, attempt.Fence, time.Now().UTC())
}

func NewCompletionRouterWithPrivate(tag *TagCatalogCompletionSink, group *GroupMessageCompletionSink, private *PrivateMessageCompletionSink) (*CompletionRouter, error) {
	if tag == nil && group == nil && private == nil {
		return nil, errors.New("at least one completion sink is required")
	}
	return &CompletionRouter{tag: tag, group: group, private: private}, nil
}

func (r *CompletionRouter) WithPrivateMessage(private *PrivateMessageCompletionSink) *CompletionRouter {
	if r != nil {
		r.private = private
	}
	return r
}

func (r *CompletionRouter) WithAutomationMessage(message effectport.CompletionSink) *CompletionRouter {
	if r != nil {
		r.automation = message
	}
	return r
}

func (r *CompletionRouter) WithSidebarMedia(sink effectport.CompletionSink) *CompletionRouter {
	if r != nil {
		r.sidebarMedia = sink
	}
	return r
}

func (r *CompletionRouter) WithSidebarJSSDK(sink effectport.CompletionSink) *CompletionRouter {
	if r != nil {
		r.sidebar = sink
	}
	return r
}

func (r *CompletionRouter) WithSurveyCompletion(sink effectport.CompletionSink) *CompletionRouter {
	if r != nil {
		r.survey = sink
	}
	return r
}

// WithCustomerOwnerHandoff routes the transfer acceptance completion to the
// Customer-owned projection without exposing Customer tables to Outbound.
func (r *CompletionRouter) WithCustomerOwnerHandoff(sink effectport.CompletionSink) *CompletionRouter {
	if r != nil {
		r.ownerHandoff = sink
	}
	return r
}

func (r *CompletionRouter) WithCommercePush(sink effectport.CompletionSink) *CompletionRouter {
	if r != nil {
		r.commerce = sink
	}
	return r
}

func NewCompletionRouter(tag *TagCatalogCompletionSink, group *GroupMessageCompletionSink) (*CompletionRouter, error) {
	if tag == nil && group == nil {
		return nil, errors.New("at least one completion sink is required")
	}
	return &CompletionRouter{tag: tag, group: group}, nil
}

func NewCompletionRouterWithMessage(tag *TagCatalogCompletionSink, group *GroupMessageCompletionSink, message effectport.CompletionSink) (*CompletionRouter, error) {
	if tag == nil && group == nil && message == nil {
		return nil, errors.New("at least one completion sink is required")
	}
	return &CompletionRouter{tag: tag, group: group, automation: message}, nil
}

func (r *CompletionRouter) WithCustomerTag(sink effectport.CompletionSink) {
	if r != nil {
		r.customerTag = sink
	}
}

func (r *CompletionRouter) WithTagCatalogMutation(sink effectport.CompletionSink) {
	if r != nil {
		r.tagMutation = sink
	}
}

func (r *CompletionRouter) WithContactDescription(sink effectport.CompletionSink) {
	if r != nil {
		r.contactDescription = sink
	}
}

func (r *CompletionRouter) CompleteEffect(ctx context.Context, effectRef string, envelope effectport.Envelope, attempt effectport.Attempt, result effectport.AdapterResult) error {
	if r == nil {
		return errors.New("completion router is unavailable")
	}
	switch envelope.Kind {
	case effectport.KindAutomationMessage:
		if r.automation == nil {
			return errors.New("message completion sink is unavailable")
		}
		return r.automation.CompleteEffect(ctx, effectRef, envelope, attempt, result)
	case effectport.KindWeComTagCatalog:
		if r.tag == nil {
			return errors.New("tag completion sink is unavailable")
		}
		return r.tag.CompleteEffect(ctx, effectRef, envelope, attempt, result)
	case effectport.KindWeComTagCatalogMutation:
		if r.tagMutation == nil {
			return errors.New("tag catalog mutation completion sink is unavailable")
		}
		return r.tagMutation.CompleteEffect(ctx, effectRef, envelope, attempt, result)
	case effectport.KindWeComContactDescription:
		if r.contactDescription == nil {
			return errors.New("contact description completion sink is unavailable")
		}
		return r.contactDescription.CompleteEffect(ctx, effectRef, envelope, attempt, result)
	case effectport.KindGroupMessage:
		if r.group == nil {
			return errors.New("Group Ops completion sink is unavailable")
		}
		return r.group.CompleteEffect(ctx, effectRef, envelope, attempt, result)
	case effectport.KindChannelAsset:
		if r.channel == nil {
			return errors.New("channel asset completion sink is unavailable")
		}
		return r.channel.CompleteEffect(ctx, effectRef, envelope, attempt, result)
	case effectport.KindChannelWelcome, effectport.KindChannelEntryTag:
		if r.entrant == nil {
			return errors.New("channel entrant completion sink is unavailable")
		}
		return r.entrant.CompleteEffect(ctx, effectRef, envelope, attempt, result)
	case effectport.KindInvitationCode:
		if r.invitationCode == nil {
			return errors.New("invitation completion unavailable")
		}
		return r.invitationCode.CompleteEffect(ctx, effectRef, envelope, attempt, result)
	case effectport.KindChannelLink:
		if r.link == nil {
			return errors.New("channel link completion sink is unavailable")
		}
		return r.link.CompleteEffect(ctx, effectRef, envelope, attempt, result)
	case effectport.KindOutboundMessage:
		if r.private == nil {
			return errors.New("private message completion sink is unavailable")
		}
		return r.private.CompleteEffect(ctx, effectRef, envelope, attempt, result)
	case effectport.KindOutboundMedia:
		if r.sidebarMedia == nil {
			return errors.New("sidebar media completion sink unavailable")
		}
		return r.sidebarMedia.CompleteEffect(ctx, effectRef, envelope, attempt, result)
	case effectport.KindSidebarJSSDKSend:
		if r.sidebar == nil {
			return errors.New("sidebar JSSDK completion sink is unavailable")
		}
		return r.sidebar.CompleteEffect(ctx, effectRef, envelope, attempt, result)
	case effectport.KindCustomerTagCommand:
		if r.customerTag == nil {
			return errors.New("customer tag completion sink is unavailable")
		}
		return r.customerTag.CompleteEffect(ctx, effectRef, envelope, attempt, result)
	case effectport.KindCommerceProductPush:
		if r.commerce == nil {
			return errors.New("commerce push completion sink is unavailable")
		}
		return r.commerce.CompleteEffect(ctx, effectRef, envelope, attempt, result)
	case effectport.KindSurveyCompletion:
		if r.survey == nil {
			return errors.New("survey completion sink is unavailable")
		}
		return r.survey.CompleteEffect(ctx, effectRef, envelope, attempt, result)
	case effectport.KindCustomerOwnerHandoff:
		if r.ownerHandoff == nil {
			return errors.New("owner handoff completion sink is unavailable")
		}
		return r.ownerHandoff.CompleteEffect(ctx, effectRef, envelope, attempt, result)
	default:
		return errors.New("unsupported completion kind")
	}
}

var _ effectport.ProviderAdapter = (*DisabledGroupMessageProvider)(nil)
var _ effectport.ProviderAdapter = (*GroupMessageProvider)(nil)
var _ effectport.CompletionSink = (*GroupMessageCompletionSink)(nil)
var _ effectport.CompletionSink = (*CompletionRouter)(nil)

func (r *CompletionRouter) WithInvitationCode(s effectport.CompletionSink) *CompletionRouter {
	r.invitationCode = s
	return r
}
