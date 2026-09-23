package outbound

import (
	"context"
	"testing"

	effect "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	groupopsport "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/port"
	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
)

func TestAcceptedGroupMessageWithSourceOnlyMaterialWaitsThenUsesPreparedID(t *testing.T) {
	envelope := groupMessageEnvelope()
	content := []byte(`{"schema_version":1,"kind":"message","message_text":"hello"}`)
	material := []byte(`{"schema_version":2,"node_kind":"message","attachments":[{"msgtype":"image"}]}`)
	sources := []byte(`{"schema_version":1,"sources":{"schema_version":1,"references":[{"reference":{"kind":"image","id":8},"source_digest":"sha256:0100000000000000000000000000000000000000000000000000000000000000"}]},"preparations":[]}`)
	canonicalContent, _ := canonicalGroupMessageJSON(content)
	canonicalMaterial, _ := canonicalGroupMessageJSON(material)
	execution := groupopsport.DispatchExecution{ExecutionID: 8, ExternalEffectID: "eer_8", State: groupopsport.ExecutionAccepted, TargetReference: "chat-8", SenderUserID: "owner-8", ContentSnapshot: content, ContentDigest: string(effect.Hash("group-ops.content.snapshot.v1", string(canonicalContent))), MaterialSnapshot: material, MaterialDigest: string(effect.Hash("group-ops.material.snapshot.v1", string(canonicalMaterial))), MaterialSourceSnapshot: sources, MaterialSourceDigest: string(effect.Hash("group-ops.material.source.snapshot.v1", string(sources))), SourceRefDigest: string(envelope.SourceRefDigest), TargetRefDigest: string(envelope.TargetRefDigest), PayloadDigest: string(envelope.PayloadDigest), PolicyVersionHash: string(envelope.PolicyVersionHash)}
	preparer := &groupMaterialPreparerStub{result: outboundport.MaterialResult{State: "queued", EffectID: "eer_media_1"}}
	sender := &groupMessageSenderStub{attempted: true}
	provider, err := NewGroupMessageProvider(GroupMessageProviderConfig{Enabled: true, Executions: groupDispatchReaderStub{value: execution}, Materials: materialReadinessStub{}, FrozenSources: &frozenGroupSourceStub{}, Writer: sender, Sources: groupMaterialSourceStub{source: outboundport.MaterialSourceSnapshot{SourceRef: "image:8", SourceType: "image", ContentDigest: [32]byte{1}, FileName: "image.png", MediaType: "image/png", SizeBytes: 3, SnapshotVersion: 1}}, Preparer: preparer, ScopeDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"})
	if err != nil {
		t.Fatal(err)
	}
	ready, after, err := provider.Preflight(context.Background(), envelope, "eer_8")
	if err != nil || ready || after <= 0 || sender.calls != 0 {
		t.Fatalf("pending ready=%v after=%s sends=%d err=%v", ready, after, sender.calls, err)
	}
	preparer.result = outboundport.MaterialResult{State: "ready", MediaID: "fresh-media-id", CredentialUsable: true}
	ready, _, err = provider.Preflight(context.Background(), envelope, "eer_8")
	if err != nil || !ready {
		t.Fatalf("prepared ready=%v err=%v", ready, err)
	}
}
