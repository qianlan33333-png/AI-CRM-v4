package main

import (
	"testing"
	"time"

	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	wecom "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom"
)

// TestWelcomeVirtualAcceptance is the channel half of the controlled staging
// acceptance. It uses an isolated PostgreSQL schema and a local Provider fake.
func TestWelcomeVirtualAcceptance(t *testing.T) {
	fixture := newChannelWelcomeRuntimeFixtureWithWelcomeMessage(t, "欢迎加入测试渠道")
	defer fixture.close()
	input, deadline := fixture.acceptWelcome(t, "welcome-virtual-acceptance-0001", fixture.now)
	var effectRef string
	var intentCount, effectCount int
	if err := fixture.native.QueryRow(fixture.ctx, `SELECT effect_ref,(SELECT count(*) FROM channel_welcome_intents),(SELECT count(*) FROM external_effects) FROM channel_welcome_intents WHERE callback_id=$1`, input.CallbackKey).Scan(&effectRef, &intentCount, &effectCount); err != nil {
		t.Fatal(err)
	}
	if effectRef == "" || intentCount != 1 || effectCount != 1 {
		t.Fatalf("new valid relationship intent=%d effect=%d ref=%q", intentCount, effectCount, effectRef)
	}
	t.Logf("phase=callback_accepted intents=%d effects=%d", intentCount, effectCount)
	if err := fixture.dispatch.DispatchDecryptedEvent(fixture.ctx, wecom.DecryptedCallbackEvent{CorpID: input.CorpID, CallbackKey: input.CallbackKey, Plaintext: input.Plaintext, ReceivedAt: fixture.now}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.native.QueryRow(fixture.ctx, `SELECT count(*) FROM channel_welcome_intents WHERE callback_id=$1`, input.CallbackKey).Scan(&intentCount); err != nil || intentCount != 1 {
		t.Fatalf("callback replay created another intent count=%d err=%v", intentCount, err)
	}
	writer := &runtimeWelcomeWriter{called: make(chan runtimeWelcomeCall, 1)}
	_, stop := fixture.startRuntime(t, writer, nil, true)
	defer stop()
	select {
	case call := <-writer.called:
		if call.message != "欢迎加入测试渠道" || call.code == "" || !call.at.Before(deadline) {
			t.Fatalf("virtual Provider call invalid: message=%q code_present=%t before_deadline=%t", call.message, call.code != "", call.at.Before(deadline))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("virtual Provider did not receive welcome")
	}
	fixture.waitEffect(t, input, effectport.StateExecuted)
	fixture.assertWelcomeOutcome(t, input, effectport.StateExecuted, "sent", true)
	if writer.calls() != 1 {
		t.Fatalf("welcome Provider calls=%d, want 1", writer.calls())
	}
	t.Log("phase=provider_executed result=one_call terminal_readback=executed")
}
