package outbound

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	effect "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	groupopsport "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/port"
	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

func groupMessageEnvelope() effect.Envelope {
	return effect.Envelope{
		Owner:             effect.OwnerOutbound,
		Kind:              effect.KindGroupMessage,
		SourceRefDigest:   effect.Hash("group-ops.run", "1"),
		TargetRefDigest:   effect.Hash("group-ops.target", "group-a"),
		PayloadDigest:     effect.Hash("group-ops.payload", "message"),
		PolicyVersionHash: effect.Hash("group-ops.policy", "v1"),
	}
}

func TestDisabledGroupMessageProviderIsDeterministicAndNeverExecutes(t *testing.T) {
	provider := NewDisabledGroupMessageProvider()
	envelope := groupMessageEnvelope()
	first, err := provider.Execute(context.Background(), envelope, effect.Attempt{Number: 1, Generation: 1, Fence: 1})
	if err != nil || first.Completion != effect.StateFinalFailed || !effect.ValidDigest(first.ReceiptDigest) || first.CallAttempted || first.RealExternalCallExecuted {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	second, err := provider.Execute(context.Background(), envelope, effect.Attempt{Number: 99, Generation: 7, Fence: 8})
	if err != nil || second.ReceiptDigest != first.ReceiptDigest || second.Completion != effect.StateFinalFailed || second.CallAttempted || second.RealExternalCallExecuted {
		t.Fatalf("second=%+v err=%v", second, err)
	}
	invalid, err := provider.Execute(context.Background(), effect.Envelope{Kind: effect.KindGroupMessage}, effect.Attempt{})
	if err != nil || invalid.Completion != effect.StateFinalFailed || !effect.ValidDigest(invalid.ReceiptDigest) {
		t.Fatalf("invalid=%+v err=%v", invalid, err)
	}
}

type preparationWriterStub struct {
	called bool
}

func (writer *preparationWriterStub) RecordPreparedGroupOpsMaterials(_ context.Context, _ mediaport.GroupOpsMaterialPreparationCommand) (mediaport.GroupOpsMaterialPreparationReceipt, error) {
	writer.called = true
	return mediaport.GroupOpsMaterialPreparationReceipt{ID: 1}, nil
}

func TestGroupMessageProviderKeepsPreparationWriteDisabled(t *testing.T) {
	writer := &preparationWriterStub{}
	provider, err := NewGroupMessageProvider(GroupMessageProviderConfig{PreparationWriter: writer})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.RecordPreparedMaterials(context.Background(), mediaport.GroupOpsMaterialPreparationCommand{}); !errors.Is(err, ErrGroupMessageProviderDisabled) || writer.called {
		t.Fatalf("disabled preparation write err=%v called=%t", err, writer.called)
	}
	result, err := provider.Execute(context.Background(), groupMessageEnvelope(), effect.Attempt{Number: 1, Generation: 1, Fence: 1})
	if err != nil || result.Completion != effect.StateFinalFailed || result.CallAttempted || result.RealExternalCallExecuted {
		t.Fatalf("disabled execution=%+v err=%v", result, err)
	}

	enabled, err := NewGroupMessageProvider(GroupMessageProviderConfig{Enabled: true, PreparationWriter: writer})
	if err != nil {
		t.Fatal(err)
	}
	if receipt, err := enabled.RecordPreparedMaterials(context.Background(), mediaport.GroupOpsMaterialPreparationCommand{}); err != nil || receipt.ID != 1 || !writer.called {
		t.Fatalf("approved preparation write receipt=%+v err=%v called=%t", receipt, err, writer.called)
	}
	result, err = enabled.Execute(context.Background(), groupMessageEnvelope(), effect.Attempt{Number: 1, Generation: 1, Fence: 1})
	if err != nil || result.Completion != effect.StateFinalFailed || result.CallAttempted || result.RealExternalCallExecuted {
		t.Fatalf("unconfigured execution=%+v err=%v", result, err)
	}
}

func TestEnabledGroupMessageProviderWithoutLeafReturnsNotConfigured(t *testing.T) {
	provider, err := NewGroupMessageProvider(GroupMessageProviderConfig{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	envelope := groupMessageEnvelope()
	result, err := provider.Execute(context.Background(), envelope, effect.Attempt{EffectID: "eer_11", Number: 1, Generation: 1, Fence: 1})
	if err != nil {
		t.Fatal(err)
	}
	base := effect.Hash("group-ops.provider.v1", string(envelope.Fingerprint()), "eer_11", "1", "1", "1")
	if result.Completion != effect.StateFinalFailed || result.ReceiptDigest != effect.Hash(string(base), "not-configured") || result.CallAttempted || result.RealExternalCallExecuted {
		t.Fatalf("enabled but unconfigured provider=%+v", result)
	}
}

type groupDispatchReaderStub struct {
	value groupopsport.DispatchExecution
	err   error
}

func (s groupDispatchReaderStub) LoadDispatchExecution(_ context.Context, effectID string) (groupopsport.DispatchExecution, error) {
	if s.err != nil {
		return groupopsport.DispatchExecution{}, s.err
	}
	if effectID != s.value.ExternalEffectID {
		return groupopsport.DispatchExecution{}, errors.New("unexpected effect id")
	}
	return s.value, nil
}

type groupMessageSenderStub struct {
	request   wecomport.GroupMessageRequest
	attempted bool
	receipt   wecomport.GroupMessageReceipt
	err       error
	calls     int
}

type materialReadinessStub struct{ err error }

func (stub materialReadinessStub) VerifyMaterialReady(context.Context, json.RawMessage, json.RawMessage, string, time.Time) error {
	return stub.err
}

func (s *groupMessageSenderStub) SendGroupMessage(_ context.Context, request wecomport.GroupMessageRequest) (wecomport.GroupMessageReceipt, bool, error) {
	s.calls++
	s.request = request
	return s.receipt, s.attempted, s.err
}

func TestGroupMessageProviderDoesNotCallProviderWhenMaterialReadinessFails(t *testing.T) {
	reader := groupDispatchReaderStub{value: groupopsport.DispatchExecution{ExternalEffectID: "eer_77", ExecutionID: 77, State: groupopsport.ExecutionAccepted, TargetReference: "chat-77", SenderUserID: "owner-77", ContentSnapshot: []byte(`{"schema_version":1,"kind":"message","message_text":"hello"}`), ContentDigest: string(effect.Hash("group-ops.content.snapshot.v1", `{"schema_version":1,"kind":"message","message_text":"hello"}`)), MaterialSnapshot: []byte(`{"schema_version":1,"references":[]}`), MaterialDigest: string(effect.Hash("group-ops.material.snapshot.v1", `{"schema_version":1,"references":[]}`)), SourceRefDigest: string(effect.Hash("group-ops.run", "1")), TargetRefDigest: string(effect.Hash("group-ops.target", "group-a")), PayloadDigest: string(effect.Hash("group-ops.payload", "message")), PolicyVersionHash: string(effect.Hash("group-ops.policy", "v1"))}}
	sender := &groupMessageSenderStub{}
	provider, err := NewGroupMessageProvider(GroupMessageProviderConfig{Enabled: true, Executions: reader, Materials: materialReadinessStub{err: errors.New("expired preparation")}, Writer: sender})
	if err != nil {
		t.Fatal(err)
	}
	result, err := provider.Execute(context.Background(), groupMessageEnvelope(), effect.Attempt{EffectID: "eer_77", Number: 1, Generation: 1, Fence: 1})
	if err != nil || result.Completion != effect.StateFinalFailed || result.CallAttempted || sender.calls != 0 {
		t.Fatalf("result=%+v err=%v calls=%d", result, err, sender.calls)
	}
}

func TestGroupMessageProviderUsesEffectBoundSnapshotAndExactChat(t *testing.T) {
	content, err := json.Marshal(map[string]any{"schema_version": 1, "node_id": 4, "position": 2, "kind": "message", "message_text": "hello"})
	if err != nil {
		t.Fatal(err)
	}
	material := []byte(`{"schema_version":1,"references":[]}`)
	effectID := "eer_42"
	envelope := groupMessageEnvelope()
	reader := groupDispatchReaderStub{value: groupopsport.DispatchExecution{
		ExecutionID: 42, ExternalEffectID: effectID, State: groupopsport.ExecutionAccepted, TargetReference: "chat-42", SenderUserID: "owner-42",
		ContentSnapshot: content, ContentDigest: string(effect.Hash("group-ops.content.snapshot.v1", string(content))),
		MaterialSnapshot: material, MaterialDigest: func() string {
			normalized, _ := canonicalGroupMessageJSON(material)
			return string(effect.Hash("group-ops.material.snapshot.v1", string(normalized)))
		}(),
		SourceRefDigest: string(envelope.SourceRefDigest), TargetRefDigest: string(envelope.TargetRefDigest), PayloadDigest: string(envelope.PayloadDigest), PolicyVersionHash: string(envelope.PolicyVersionHash),
	}}
	if _, requestErr := groupMessageRequest(reader.value); requestErr != nil {
		t.Fatalf("request err=%v", requestErr)
	}
	sender := &groupMessageSenderStub{attempted: true, receipt: wecomport.GroupMessageReceipt{MessageID: "msg-42"}}
	provider, err := NewGroupMessageProvider(GroupMessageProviderConfig{Enabled: true, Executions: reader, Materials: materialReadinessStub{}, Writer: sender})
	if err != nil {
		t.Fatal(err)
	}
	result, err := provider.Execute(context.Background(), envelope, effect.Attempt{EffectID: effectID, Number: 1, Generation: 1, Fence: 9})
	if err != nil || result.Completion != effect.StateExecuted || !result.CallAttempted || !result.RealExternalCallExecuted {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if sender.request.SenderUserID != "owner-42" || sender.request.Text != "hello" || len(sender.request.ChatIDs) != 1 || sender.request.ChatIDs[0] != "chat-42" {
		t.Fatalf("exact WeCom request=%+v", sender.request)
	}
}

func TestGroupMessageProviderSendsPureTextWebhookSnapshot(t *testing.T) {
	content := []byte(`{"schema_version":2,"kind":"webhook_message","message_text":"动态话术","attachment_order":[]}`)
	material := []byte(`{"schema_version":1,"references":[]}`)
	envelope := groupMessageEnvelope()
	canonicalContent, err := canonicalGroupMessageJSON(content)
	if err != nil {
		t.Fatal(err)
	}
	canonicalMaterial, err := canonicalGroupMessageJSON(material)
	if err != nil {
		t.Fatal(err)
	}
	effectID := "eer_webhook_text"
	execution := groupopsport.DispatchExecution{
		ExecutionID: 74, ExternalEffectID: effectID, State: groupopsport.ExecutionAccepted, TargetReference: "chat-74", SenderUserID: "owner-74",
		ContentSnapshot: content, ContentDigest: string(effect.Hash("group-ops.content.snapshot.v1", string(canonicalContent))),
		MaterialSnapshot: material, MaterialDigest: string(effect.Hash("group-ops.material.snapshot.v1", string(canonicalMaterial))),
		SourceRefDigest: string(envelope.SourceRefDigest), TargetRefDigest: string(envelope.TargetRefDigest), PayloadDigest: string(envelope.PayloadDigest), PolicyVersionHash: string(envelope.PolicyVersionHash),
	}
	sender := &groupMessageSenderStub{attempted: true, receipt: wecomport.GroupMessageReceipt{MessageID: "msg-webhook-text"}}
	provider, err := NewGroupMessageProvider(GroupMessageProviderConfig{Enabled: true, Executions: groupDispatchReaderStub{value: execution}, Materials: materialReadinessStub{}, Writer: sender})
	if err != nil {
		t.Fatal(err)
	}
	result, err := provider.Execute(context.Background(), envelope, effect.Attempt{EffectID: effectID, Number: 1, Generation: 1, Fence: 1})
	if err != nil || result.Completion != effect.StateExecuted || !result.CallAttempted || !result.RealExternalCallExecuted || sender.calls != 1 {
		t.Fatalf("result=%+v err=%v sender_calls=%d", result, err, sender.calls)
	}
	if sender.request.SenderUserID != "owner-74" || len(sender.request.ChatIDs) != 1 || sender.request.ChatIDs[0] != "chat-74" || sender.request.Text != "动态话术" || len(sender.request.Attachments) != 0 {
		t.Fatalf("unexpected dynamic text writer request=%+v", sender.request)
	}
}

func TestGroupMessageRequestCanonicalizesJSONBSnapshotsWithoutWeakeningDigestChecks(t *testing.T) {
	content := []byte(` { "message_text" : "hello", "kind":"message", "schema_version":1 } `)
	material := []byte(` { "references": [ ], "schema_version": 1 } `)
	canonicalContent, err := canonicalGroupMessageJSON(content)
	if err != nil {
		t.Fatal(err)
	}
	canonicalMaterial, err := canonicalGroupMessageJSON(material)
	if err != nil {
		t.Fatal(err)
	}
	execution := groupopsport.DispatchExecution{ExecutionID: 7, TargetReference: "chat-7", SenderUserID: "owner-7", ContentSnapshot: content, ContentDigest: string(effect.Hash("group-ops.content.snapshot.v1", string(canonicalContent))), MaterialSnapshot: material, MaterialDigest: string(effect.Hash("group-ops.material.snapshot.v1", string(canonicalMaterial)))}
	if _, err = groupMessageRequest(execution); err != nil {
		t.Fatalf("JSONB equivalent snapshots rejected: %v", err)
	}
	execution.ContentSnapshot = []byte(`{"schema_version":1,"kind":"message","message_text":"changed"}`)
	if _, err = groupMessageRequest(execution); err == nil {
		t.Fatal("semantic content change bypassed digest check")
	}
}

func TestGroupMessageRequestAcceptsWebhookTextThenFrozenAttachmentOrder(t *testing.T) {
	content := []byte(`{"schema_version":2,"kind":"webhook_message","message_text":"今日话术","attachment_order":[{"kind":"image","id":8},{"kind":"attachment","id":9}]}`)
	material := []byte(`{"schema_version":2,"node_kind":"message","attachments":[{"msgtype":"image","media_id":"image-8"},{"msgtype":"file","media_id":"file-9"}]}`)
	sources := []byte(`{"schema_version":1,"references":[{"reference":{"kind":"image","id":8},"source_digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},{"reference":{"kind":"attachment","id":9},"source_digest":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}]}`)
	canonicalContent, err := canonicalGroupMessageJSON(content)
	if err != nil {
		t.Fatal(err)
	}
	canonicalMaterial, err := canonicalGroupMessageJSON(material)
	if err != nil {
		t.Fatal(err)
	}
	execution := groupopsport.DispatchExecution{ExecutionID: 73, TargetReference: "chat-73", SenderUserID: "owner-73", ContentSnapshot: content, ContentDigest: string(effect.Hash("group-ops.content.snapshot.v1", string(canonicalContent))), MaterialSnapshot: material, MaterialDigest: string(effect.Hash("group-ops.material.snapshot.v1", string(canonicalMaterial))), MaterialSourceSnapshot: sources}
	request, err := groupMessageRequest(execution)
	if err != nil || request.Text != "今日话术" || len(request.Attachments) != 2 || request.Attachments[0].MsgType != "image" || request.Attachments[1].MsgType != "file" {
		t.Fatalf("request=%+v err=%v", request, err)
	}
	execution.ContentSnapshot = []byte(`{"schema_version":2,"kind":"webhook_message","message_text":"今日话术","attachment_order":[{"kind":"attachment","id":9},{"kind":"image","id":8}]}`)
	canonicalContent, _ = canonicalGroupMessageJSON(execution.ContentSnapshot)
	execution.ContentDigest = string(effect.Hash("group-ops.content.snapshot.v1", string(canonicalContent)))
	if _, err = groupMessageRequest(execution); err == nil {
		t.Fatal("reordered webhook attachments were accepted")
	}
}

type groupMessageProjectionStub struct {
	effectRef        string
	state            groupopsport.ExecutionState
	providerAccepted bool
	deliveryProven   bool
	receiptDigest    string
	attempt          int32
	completedAt      time.Time
}

func (s *groupMessageProjectionStub) CompleteEffect(_ context.Context, effectRef string, state groupopsport.ExecutionState, providerAccepted, deliveryProven bool, receiptDigest string, attempt int32, completedAt time.Time) error {
	s.effectRef = effectRef
	s.state = state
	s.providerAccepted = providerAccepted
	s.deliveryProven = deliveryProven
	s.receiptDigest = receiptDigest
	s.attempt = attempt
	s.completedAt = completedAt
	return nil
}

func TestGroupMessageCompletionSinkProjectsTerminalAndUnknownStates(t *testing.T) {
	projector := &groupMessageProjectionStub{}
	sink, err := NewGroupMessageCompletionSink(projector)
	if err != nil {
		t.Fatal(err)
	}
	envelope := groupMessageEnvelope()
	receipt := effect.Hash("provider", "receipt")
	if err := sink.CompleteEffect(context.Background(), "eer_7", envelope, effect.Attempt{Number: 2}, effect.AdapterResult{Completion: effect.StateFinalFailed, ReceiptDigest: receipt}); err != nil {
		t.Fatal(err)
	}
	if projector.effectRef != "eer_7" || projector.state != groupopsport.ExecutionFinalFailed || projector.providerAccepted || projector.deliveryProven || projector.receiptDigest != string(receipt) || projector.attempt != 2 || projector.completedAt.IsZero() {
		t.Fatalf("terminal projection=%+v", projector)
	}
	if err := sink.CompleteEffect(context.Background(), "eer_7", envelope, effect.Attempt{Number: 3}, effect.AdapterResult{Completion: effect.StateUnknown, ReceiptDigest: receipt, CallAttempted: true}); err != nil {
		t.Fatal(err)
	}
	if projector.state != groupopsport.ExecutionOutcomeUnknown || projector.providerAccepted || projector.deliveryProven || projector.attempt != 3 {
		t.Fatalf("unknown projection=%+v", projector)
	}
}

type groupMessageReceiptWriterStub struct {
	task groupopsport.GroupMessageReceipt
	err  error
}

func (s *groupMessageReceiptWriterStub) RecordGroupMessageTask(_ context.Context, task groupopsport.GroupMessageReceipt) error {
	s.task = task
	return s.err
}

func TestGroupMessageCompletionSinkPersistsOnlyValidatedTaskArtifact(t *testing.T) {
	projector := &groupMessageProjectionStub{}
	writer := &groupMessageReceiptWriterStub{}
	sink, err := NewGroupMessageCompletionSink(projector, writer)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"execution_id":"42","effect_id":"eer_42","msgid":"msg-42","sender":"owner-42","chat_id":"chat-42"}`)
	artifact := effect.ResultArtifact{Kind: "group-ops.wecom-task.v1", Payload: payload}
	artifact.Digest = effect.Hash("external-effect.artifact.v1", artifact.Kind, string(payload))
	if err = sink.CompleteEffect(context.Background(), "eer_42", groupMessageEnvelope(), effect.Attempt{Number: 1}, effect.AdapterResult{Completion: effect.StateExecuted, ReceiptDigest: effect.Hash("receipt", "42"), CallAttempted: true, RealExternalCallExecuted: true, Artifact: artifact}); err != nil {
		t.Fatal(err)
	}
	if writer.task.ExecutionID != 42 || writer.task.MessageID != "msg-42" || writer.task.ExternalEffectID != "eer_42" || writer.task.TaskEvidenceDigest != string(artifact.Digest) {
		t.Fatalf("task=%+v", writer.task)
	}
	if projector.state != groupopsport.ExecutionProviderAccepted || !projector.providerAccepted || projector.deliveryProven {
		t.Fatalf("projection=%+v", projector)
	}
}

type groupMaterialSourceStub struct {
	source outboundport.MaterialSourceSnapshot
}

func (s groupMaterialSourceStub) GetSourceSnapshot(_ context.Context, ref string) (outboundport.MaterialSourceSnapshot, error) {
	if ref != s.source.SourceRef {
		return outboundport.MaterialSourceSnapshot{}, errors.New("wrong source ref")
	}
	return s.source, nil
}
func (groupMaterialSourceStub) ListEnabledSourceSnapshots(context.Context, outboundport.MaterialSnapshotPageRequest) (outboundport.MaterialSnapshotPage, error) {
	return outboundport.MaterialSnapshotPage{}, errors.New("not used")
}
func (groupMaterialSourceStub) ReadSourceBytes(context.Context, outboundport.MaterialSourceSnapshot) (outboundport.MaterialSourceContent, error) {
	return outboundport.MaterialSourceContent{}, errors.New("must not read bytes")
}

type frozenGroupSourceStub struct {
	err   error
	calls int
}

func (s *frozenGroupSourceStub) VerifyFrozenMaterialSources(context.Context, json.RawMessage, json.RawMessage, string) error {
	s.calls++
	return s.err
}

type groupMaterialPreparerStub struct {
	calls  int
	result outboundport.MaterialResult
	err    error
}

func (s *groupMaterialPreparerStub) ReadyForSend(_ context.Context, request outboundport.MaterialRequest) (outboundport.MaterialResult, error) {
	s.calls++
	if request.SourceRef != "image:8" {
		return outboundport.MaterialResult{}, errors.New("unexpected source")
	}
	return s.result, s.err
}
func (*groupMaterialPreparerStub) Prepare(context.Context, outboundport.MaterialRequest) (outboundport.MaterialResult, error) {
	return outboundport.MaterialResult{}, errors.New("not used")
}

func TestGroupMessageProviderPreflightsUnifiedMediaAndExecuteUsesFreshID(t *testing.T) {
	digest := [32]byte{1}
	digestText := "sha256:0100000000000000000000000000000000000000000000000000000000000000"
	content := []byte(`{"schema_version":1,"kind":"message","message_text":"hello"}`)
	material := []byte(`{"schema_version":2,"node_kind":"message","attachments":[{"msgtype":"image","media_id":"obsolete-media-id"}]}`)
	sources := []byte(`{"schema_version":1,"references":[{"reference":{"kind":"image","id":8},"source_digest":"sha256:0100000000000000000000000000000000000000000000000000000000000000"}]}`)
	envelope := groupMessageEnvelope()
	canonicalContent, _ := canonicalGroupMessageJSON(content)
	canonicalMaterial, _ := canonicalGroupMessageJSON(material)
	execution := groupopsport.DispatchExecution{ExecutionID: 8, ExternalEffectID: "eer_8", State: groupopsport.ExecutionAccepted, TargetReference: "chat-8", SenderUserID: "owner-8", ContentSnapshot: content, ContentDigest: string(effect.Hash("group-ops.content.snapshot.v1", string(canonicalContent))), MaterialSnapshot: material, MaterialDigest: string(effect.Hash("group-ops.material.snapshot.v1", string(canonicalMaterial))), MaterialSourceSnapshot: sources, MaterialSourceDigest: string(effect.Hash("group-ops.material.source.snapshot.v1", string(sources))), SourceRefDigest: string(envelope.SourceRefDigest), TargetRefDigest: string(envelope.TargetRefDigest), PayloadDigest: string(envelope.PayloadDigest), PolicyVersionHash: string(envelope.PolicyVersionHash)}
	preparer := &groupMaterialPreparerStub{result: outboundport.MaterialResult{State: "ready", MediaID: "fresh-media-id"}}
	frozenSources := &frozenGroupSourceStub{}
	sender := &groupMessageSenderStub{attempted: true, receipt: wecomport.GroupMessageReceipt{MessageID: "msg-8"}}
	provider, err := NewGroupMessageProvider(GroupMessageProviderConfig{Enabled: true, Executions: groupDispatchReaderStub{value: execution}, Materials: materialReadinessStub{err: errors.New("expired legacy receipt")}, FrozenSources: frozenSources, Writer: sender, Sources: groupMaterialSourceStub{source: outboundport.MaterialSourceSnapshot{SourceRef: "image:8", SourceType: "image", ContentDigest: digest, FileName: "image.png", MediaType: "image/png", SizeBytes: 3, SnapshotVersion: 1}}, Preparer: preparer, ScopeDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"})
	if err != nil {
		t.Fatal(err)
	}
	ready, after, err := provider.Preflight(context.Background(), envelope, "eer_8")
	if err != nil || !ready || after != 0 || preparer.calls != 1 {
		t.Fatalf("ready=%v after=%s calls=%d err=%v", ready, after, preparer.calls, err)
	}
	if request, requestErr := groupMessageRequestWithPreparedMedia(execution, map[string]string{"image:8": "fresh-media-id"}); requestErr != nil {
		t.Fatalf("prepared request err=%v", requestErr)
	} else if len(request.Attachments) != 1 {
		t.Fatalf("prepared request=%+v", request)
	}
	result, err := provider.Execute(context.Background(), envelope, effect.Attempt{EffectID: "eer_8", Number: 1, Generation: 1, Fence: 1})
	if err != nil || result.Completion != effect.StateExecuted || sender.calls != 1 {
		t.Fatalf("result=%+v calls=%d err=%v", result, sender.calls, err)
	}
	if len(sender.request.Attachments) != 1 || sender.request.Attachments[0].MediaID != "fresh-media-id" || sender.request.Attachments[0].MediaID == "obsolete-media-id" {
		t.Fatalf("request=%+v", sender.request)
	}
	if digestText == "" {
		t.Fatal("digest fixture unexpectedly empty")
	}
}

func TestGroupMessageProviderPreflightSnoozesPendingUnifiedMedia(t *testing.T) {
	digest := [32]byte{1}
	content := []byte(`{"schema_version":1,"kind":"message","message_text":"hello"}`)
	material := []byte(`{"schema_version":2,"node_kind":"message","attachments":[{"msgtype":"image","media_id":"obsolete-media-id"}]}`)
	sources := []byte(`{"schema_version":1,"references":[{"reference":{"kind":"image","id":8},"source_digest":"sha256:0100000000000000000000000000000000000000000000000000000000000000"}]}`)
	envelope := groupMessageEnvelope()
	canonicalContent, _ := canonicalGroupMessageJSON(content)
	canonicalMaterial, _ := canonicalGroupMessageJSON(material)
	execution := groupopsport.DispatchExecution{ExecutionID: 8, ExternalEffectID: "eer_8", State: groupopsport.ExecutionAccepted, TargetReference: "chat-8", SenderUserID: "owner-8", ContentSnapshot: content, ContentDigest: string(effect.Hash("group-ops.content.snapshot.v1", string(canonicalContent))), MaterialSnapshot: material, MaterialDigest: string(effect.Hash("group-ops.material.snapshot.v1", string(canonicalMaterial))), MaterialSourceSnapshot: sources, SourceRefDigest: string(envelope.SourceRefDigest), TargetRefDigest: string(envelope.TargetRefDigest), PayloadDigest: string(envelope.PayloadDigest), PolicyVersionHash: string(envelope.PolicyVersionHash)}
	provider, _ := NewGroupMessageProvider(GroupMessageProviderConfig{Enabled: true, Executions: groupDispatchReaderStub{value: execution}, Materials: materialReadinessStub{}, FrozenSources: &frozenGroupSourceStub{}, Writer: &groupMessageSenderStub{}, Sources: groupMaterialSourceStub{source: outboundport.MaterialSourceSnapshot{SourceRef: "image:8", SourceType: "image", ContentDigest: digest, FileName: "image.png", MediaType: "image/png", SizeBytes: 3, SnapshotVersion: 1}}, Preparer: &groupMaterialPreparerStub{result: outboundport.MaterialResult{State: "queued"}}, ScopeDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"})
	ready, after, err := provider.Preflight(context.Background(), envelope, "eer_8")
	if err != nil || ready || after != time.Second {
		t.Fatalf("ready=%v after=%s err=%v", ready, after, err)
	}
}

func TestGroupMessagePreparedRequestRejectsReorderedMaterialSources(t *testing.T) {
	material := []byte(`{"schema_version":2,"node_kind":"message","attachments":[{"msgtype":"image","media_id":"old-image"},{"msgtype":"file","media_id":"old-file"}]}`)
	sources := []byte(`{"schema_version":1,"references":[{"reference":{"kind":"attachment","id":2},"source_digest":"sha256:0200000000000000000000000000000000000000000000000000000000000000"},{"reference":{"kind":"image","id":1},"source_digest":"sha256:0100000000000000000000000000000000000000000000000000000000000000"}]}`)
	var frozen mediaport.GroupOpsMaterialSourceSnapshot
	if err := json.Unmarshal(sources, &frozen); err != nil {
		t.Fatal(err)
	}
	if err := validateGroupMaterialSequence(material, frozen); err == nil {
		t.Fatal("reordered source sequence accepted")
	}
}

func TestGroupMessagePreflightAllowsTerminalPreparationToFinalizeLocally(t *testing.T) {
	digest := [32]byte{1}
	content := []byte(`{"schema_version":1,"kind":"message","message_text":"hello"}`)
	material := []byte(`{"schema_version":2,"node_kind":"message","attachments":[{"msgtype":"image","media_id":"obsolete"}]}`)
	sources := []byte(`{"schema_version":1,"references":[{"reference":{"kind":"image","id":8},"source_digest":"sha256:0100000000000000000000000000000000000000000000000000000000000000"}]}`)
	envelope := groupMessageEnvelope()
	canonicalContent, _ := canonicalGroupMessageJSON(content)
	canonicalMaterial, _ := canonicalGroupMessageJSON(material)
	execution := groupopsport.DispatchExecution{ExecutionID: 8, ExternalEffectID: "eer_8", State: groupopsport.ExecutionAccepted, TargetReference: "chat-8", SenderUserID: "owner-8", ContentSnapshot: content, ContentDigest: string(effect.Hash("group-ops.content.snapshot.v1", string(canonicalContent))), MaterialSnapshot: material, MaterialDigest: string(effect.Hash("group-ops.material.snapshot.v1", string(canonicalMaterial))), MaterialSourceSnapshot: sources, SourceRefDigest: string(envelope.SourceRefDigest), TargetRefDigest: string(envelope.TargetRefDigest), PayloadDigest: string(envelope.PayloadDigest), PolicyVersionHash: string(envelope.PolicyVersionHash)}
	sender := &groupMessageSenderStub{}
	provider, _ := NewGroupMessageProvider(GroupMessageProviderConfig{Enabled: true, Executions: groupDispatchReaderStub{value: execution}, Materials: materialReadinessStub{}, FrozenSources: &frozenGroupSourceStub{}, Writer: sender, Sources: groupMaterialSourceStub{source: outboundport.MaterialSourceSnapshot{SourceRef: "image:8", SourceType: "image", ContentDigest: digest, FileName: "image.png", MediaType: "image/png", SizeBytes: 3, SnapshotVersion: 1}}, Preparer: &groupMaterialPreparerStub{err: outboundport.MediaPreparationTerminalError{Code: "not_supported", State: "final_failed"}}, ScopeDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"})
	ready, after, err := provider.Preflight(context.Background(), envelope, "eer_8")
	if err != nil || !ready || after != 0 {
		t.Fatalf("ready=%v after=%s err=%v", ready, after, err)
	}
	result, err := provider.Execute(context.Background(), envelope, effect.Attempt{EffectID: "eer_8", Number: 1, Generation: 1, Fence: 1})
	if err != nil || result.Completion != effect.StateFinalFailed || sender.calls != 0 {
		t.Fatalf("result=%+v calls=%d err=%v", result, sender.calls, err)
	}
}
