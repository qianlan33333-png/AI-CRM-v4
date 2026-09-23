package externaleffects

import (
	"errors"
	"testing"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	platformjobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
)

func digestForTest(label string) Digest { return Hash("test", label) }
func envelopeForTest() Envelope {
	return Envelope{Owner: OwnerOutbound, Kind: KindOutboundMessage, SourceRefDigest: digestForTest("source"), TargetRefDigest: digestForTest("target"), PayloadDigest: digestForTest("payload"), PolicyVersionHash: digestForTest("policy")}
}

func TestChannelWelcomeUsesRegisteredDedicatedQueueOnly(t *testing.T) {
	if got := effectQueue(KindChannelWelcome, ""); got != platformjobqueue.OutboundWelcomeQueue {
		t.Fatalf("welcome queue=%q", got)
	}
	for _, kind := range []Kind{KindOutboundMessage, KindOutboundMedia, KindChannelEntryTag, KindGroupMessage} {
		if got := effectQueue(kind, ""); got != platformjobqueue.OutboundQueue {
			t.Fatalf("kind=%q queue=%q", kind, got)
		}
	}
}

func TestClosedDigestOnlyEnvelopeAndStates(t *testing.T) {
	if !envelopeForTest().Valid() {
		t.Fatal("valid digest-only outbound envelope rejected")
	}
	bad := envelopeForTest()
	bad.PayloadDigest = "13800000000"
	if bad.Valid() {
		t.Fatal("raw payload accepted")
	}
	for _, kind := range []Kind{KindOutboundMessage, KindOutboundMedia, KindWeComTagCatalog, KindWeComTagCatalogMutation, KindGroupMessage} {
		candidate := envelopeForTest()
		candidate.Kind = kind
		if !candidate.Valid() {
			t.Fatalf("frozen kind %q rejected", kind)
		}
	}
	if CanTransition(StateUnknown, StateQueued) || CanTransition(StateUnknown, StateCancelled) {
		t.Fatal("unknown result must require reconciliation")
	}
	if !CanTransition(StateUnknown, StateReconciled) {
		t.Fatal("unknown result must reconcile")
	}
}

func TestStaleAttemptCompletionProjectionKinds(t *testing.T) {
	for _, kind := range []Kind{KindOutboundMedia, KindWeComTagCatalog, KindWeComTagCatalogMutation, KindGroupMessage, KindChannelAsset, KindOutboundMessage, KindAutomationMessage, KindCommerceProductPush, KindAIAgentGenerate, port.KindSidebarJSSDKSend} {
		if !projectsStaleAttempt(kind) {
			t.Fatalf("stale attempted effect kind %q must project outcome_unknown to its owner", kind)
		}
	}
	for _, kind := range []Kind{KindChannelWelcome, KindChannelEntryTag, KindChannelLink} {
		if projectsStaleAttempt(kind) {
			t.Fatalf("stale attempted effect kind %q lacks an approved crash-recovery projection", kind)
		}
	}
}

func TestPaymentV1KindsUsePaymentOwnerWithoutRawPayload(t *testing.T) {
	for _, kind := range []Kind{KindWeChatPayPrepay, KindWeChatPayRefund, KindWeChatShopRefund} {
		intent := port.PaymentV1Intent{Kind: kind, ReceiptKey: digestForTest("payment-key"), SourceRefDigest: digestForTest("payment-source"), TargetRefDigest: digestForTest("payment-target"), PayloadDigest: digestForTest("payment-payload"), PolicyVersionHash: digestForTest("payment-policy")}
		command, ok := intent.AcceptCommand()
		if !ok || command.Envelope.Owner != OwnerPayment || command.Envelope.Kind != kind {
			t.Fatalf("payment intent kind=%q command=%+v ok=%v", kind, command, ok)
		}
	}
}

func TestControlDigestDetectsPayloadDrift(t *testing.T) {
	first := ControlCommand{EffectID: "eer_7", ReceiptKey: digestForTest("key"), EvidenceDigest: digestForTest("evidence"), ActorAdminUserID: 7}
	second := first
	second.EvidenceDigest = digestForTest("other")
	if first.Digest("reconcile") == second.Digest("reconcile") {
		t.Fatal("reconcile evidence omitted from command digest")
	}
	second = first
	second.ActorAdminUserID = 8
	if first.Digest("reconcile") == second.Digest("reconcile") {
		t.Fatal("control actor omitted from command digest")
	}
}

func TestParseEffectIDMatchesThePublicContractExactly(t *testing.T) {
	for _, value := range []string{"eer_1", "eer_42", "eer_9223372036854775807"} {
		if _, err := parseEffectID(value); err != nil {
			t.Fatalf("valid %q: %v", value, err)
		}
	}
	for _, value := range []string{"", "eer_", "eer_0", "eer_01", "eer_1junk", "eer_+1", "eer_-1", "eer_1.0", "eer_9223372036854775808"} {
		if _, err := parseEffectID(value); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid %q: %v", value, err)
		}
	}
}
