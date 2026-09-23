package main

import (
	"context"
	"testing"
	"time"

	archiveapp "github.com/qianlan33333-png/AI-CRM-v3/internal/messagearchive/app"
	archivestore "github.com/qianlan33333-png/AI-CRM-v3/internal/messagearchive/store"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/wecom"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

func TestArchiveCallbackProcessingDoesNotDelayWelcomeRuntime(t *testing.T) {
	fixture := newChannelWelcomeRuntimeFixture(t)
	defer fixture.close()
	applyMessageArchiveJourneyMigrations(t, fixture.ctx, fixture.native)

	reader := &blockingArchiveReader{started: make(chan struct{}), release: make(chan struct{})}
	archive := archiveapp.Service{
		Enabled: true, CorpScope: "wecom-corp:wx-corp", Reader: reader,
		Identity: archiveJourneyIdentity{}, Lineage: archiveJourneyLineage{}, Staff: archiveJourneyStaff{id: 1},
		Store: archivestore.NewPostgreSQL(), UOW: fixture.unit, PageLimit: 100, PageBudget: 1,
	}
	archiveProcessor := wecom.ArchiveInboxProcessor{Enabled: true, Inbox: fixture.dispatch.Inbox, UOW: fixture.unit, Archive: archive}
	router := wecom.CallbackEventDispatcher{
		ExternalContact: fixture.dispatch,
		Archive:         wecom.ArchiveCallbackDispatcher{Inbox: fixture.dispatch.Inbox, UOW: fixture.unit, ExpectedAgentID: 1000002},
	}
	archivePlaintext := []byte(`<xml><ToUserName>wx-corp</ToUserName><FromUserName>sys</FromUserName><CreateTime>1788336000</CreateTime><MsgType>event</MsgType><AgentID>1000002</AgentID><Event>msgaudit_notify</Event></xml>`)
	archiveKey := mustChannelWelcomeKey(t, "wecom:message-archive:joint-callback-0001")
	if err := router.DispatchDecryptedEvent(fixture.ctx, wecom.DecryptedCallbackEvent{CorpID: "wx-corp", CallbackKey: archiveKey, Plaintext: archivePlaintext, ReceivedAt: fixture.now}); err != nil {
		t.Fatal(err)
	}
	archiveDone := make(chan error, 1)
	go func() {
		_, err := archiveProcessor.ProcessOnce(fixture.ctx, "welcome-archive-joint", 1)
		archiveDone <- err
	}()
	select {
	case <-reader.started:
	case <-time.After(5 * time.Second):
		t.Fatal("archive processor did not enter provider read")
	}

	welcomeKey := mustChannelWelcomeKey(t, "wecom:external-contact:welcome-archive-joint-0001")
	welcomeInput := wecom.DecryptedCallbackEvent{CorpID: "wx-corp", CallbackKey: welcomeKey, Plaintext: channelWelcomePlaintext(fixture.ready.rawState), ReceivedAt: fixture.now}
	acceptedAt := time.Now()
	if err := router.DispatchDecryptedEvent(fixture.ctx, welcomeInput); err != nil {
		close(reader.release)
		t.Fatal(err)
	}
	writer := &runtimeWelcomeWriter{called: make(chan runtimeWelcomeCall, 1)}
	_, stopWelcome := fixture.startRuntime(t, writer, nil, true)
	select {
	case call := <-writer.called:
		if elapsed := time.Since(acceptedAt); elapsed > 5*time.Second || !call.at.Before(call.deadline) {
			t.Fatalf("welcome call=%+v elapsed=%s", call, elapsed)
		}
	case <-time.After(5 * time.Second):
		close(reader.release)
		stopWelcome()
		t.Fatal("blocked archive processing delayed welcome execution")
	}
	fixture.waitEffect(t, welcomeInput, "executed")
	stopWelcome()

	var externalInboxes, archiveInboxes int
	if err := fixture.native.QueryRow(fixture.ctx, `SELECT count(*) FILTER (WHERE provider='wecom.external_contact'),count(*) FILTER (WHERE provider='wecom.message_archive') FROM webhook_inbox`).Scan(&externalInboxes, &archiveInboxes); err != nil {
		t.Fatal(err)
	}
	if externalInboxes != 1 || archiveInboxes != 1 || writer.calls() != 1 {
		t.Fatalf("external inboxes=%d archive inboxes=%d welcome calls=%d", externalInboxes, archiveInboxes, writer.calls())
	}
	close(reader.release)
	select {
	case err := <-archiveDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("archive processor did not finish after provider release")
	}
}

type blockingArchiveReader struct {
	started chan struct{}
	release chan struct{}
}

func (*blockingArchiveReader) ArchiveHealth(context.Context) (wecomport.ArchiveHealth, error) {
	return wecomport.ArchiveHealth{}, nil
}
func (reader *blockingArchiveReader) GetChatData(context.Context, uint64, uint32) ([]wecomport.EncryptedArchiveRecord, error) {
	close(reader.started)
	<-reader.release
	return nil, nil
}
func (*blockingArchiveReader) DecryptArchiveData(context.Context, []wecomport.EncryptedArchiveRecord) ([]wecomport.PlainArchiveRecord, error) {
	return nil, nil
}
func (*blockingArchiveReader) GetArchiveMedia(context.Context, wecomport.ArchiveMediaRequest) (wecomport.ArchiveMediaChunk, error) {
	return wecomport.ArchiveMediaChunk{}, archiveapp.ErrProviderPage
}
