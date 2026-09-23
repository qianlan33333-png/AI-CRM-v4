package app

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	segmentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/domain"
	segmentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/store"
)

type webhookStoreStub struct {
	WebhookStore
	saved *segmentstore.WebhookReceipt
	facts int
}

func (*webhookStoreStub) PackageIDByCode(context.Context, string) (int64, error) { return 3, nil }
func (s *webhookStoreStub) RecordWebhook(_ context.Context, in segmentstore.WebhookReceipt) (segmentstore.WebhookReceipt, bool, error) {
	if s.saved != nil {
		if s.saved.PayloadDigest != in.PayloadDigest {
			return segmentstore.WebhookReceipt{}, false, segmentstore.ErrConflict
		}
		return *s.saved, false, nil
	}
	in.ID = 11
	s.saved = &in
	return in, true, nil
}
func (s *webhookStoreStub) AppendMutationFacts(context.Context, segmentstore.MutationFact) (int64, error) {
	s.facts++
	return 1, nil
}

type audienceIdentityStub struct {
	result identityport.AudienceResolution
}

func (s audienceIdentityStub) ResolveAudienceFact(context.Context, identitydomain.VerifiedFact) (identityport.AudienceResolution, error) {
	return s.result, nil
}

type atomicRefreshStub struct{ calls int }

func (s *atomicRefreshStub) AcceptRefreshWithin(context.Context, RefreshCommand) (segmentdomain.RefreshRun, error) {
	s.calls++
	return segmentdomain.RefreshRun{ID: 19}, nil
}
func inboundFact(t *testing.T, payload byte) VerifiedInboundFact {
	t.Helper()
	fact, err := identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{Kind: identitydomain.KindWeComExternalUserID, Scope: "wecom-corp:corp", Value: "external-private", Source: "signed-source"})
	if err != nil {
		t.Fatal(err)
	}
	return VerifiedInboundFact{PackageKey: "package", EventID: "event", PayloadDigest: sha256.Sum256([]byte{payload}), Identity: fact, OccurredAt: time.Unix(1000, 0).UTC()}
}
func TestWebhookResolvedFactRequestsOneDurableRefreshAndReplays(t *testing.T) {
	store := &webhookStoreStub{}
	refresh := &atomicRefreshStub{}
	service, _ := NewWebhookService(directUOW{}, store, audienceIdentityStub{identityport.AudienceResolution{Status: identityport.AudienceResolved, CustomerID: 8, IdentityID: 9}}, refresh)
	service.now = func() time.Time { return time.Unix(1001, 0).UTC() }
	first, err := service.Ingest(context.Background(), inboundFact(t, 1))
	if err != nil || first.Replayed || first.Receipt.RefreshRunID != 19 || refresh.calls != 1 || store.facts != 1 {
		t.Fatalf("first=%+v refresh=%d facts=%d err=%v", first, refresh.calls, store.facts, err)
	}
	second, err := service.Ingest(context.Background(), inboundFact(t, 1))
	if err != nil || !second.Replayed || refresh.calls != 2 || store.facts != 1 {
		t.Fatalf("second=%+v refresh=%d facts=%d err=%v", second, refresh.calls, store.facts, err)
	}
}
func TestWebhookChangedPayloadConflictsAndUnresolvedNeverRefreshes(t *testing.T) {
	store := &webhookStoreStub{}
	refresh := &atomicRefreshStub{}
	service, _ := NewWebhookService(directUOW{}, store, audienceIdentityStub{identityport.AudienceResolution{Status: identityport.AudienceUnresolved}}, refresh)
	service.now = func() time.Time { return time.Unix(1001, 0).UTC() }
	if _, err := service.Ingest(context.Background(), inboundFact(t, 1)); err != nil {
		t.Fatal(err)
	}
	if refresh.calls != 0 || store.saved.CustomerID != 0 {
		t.Fatalf("refresh=%d receipt=%+v", refresh.calls, store.saved)
	}
	_, err := service.Ingest(context.Background(), inboundFact(t, 2))
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err=%v", err)
	}
}
