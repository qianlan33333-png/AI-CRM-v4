package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	segmentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/domain"
	segmentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/store"
)

type WebhookStore interface {
	PackageIDByCode(context.Context, string) (int64, error)
	RecordWebhook(context.Context, segmentstore.WebhookReceipt) (segmentstore.WebhookReceipt, bool, error)
	AppendMutationFacts(context.Context, segmentstore.MutationFact) (int64, error)
}
type AtomicRefreshRequester interface {
	AcceptRefreshWithin(context.Context, RefreshCommand) (segmentdomain.RefreshRun, error)
}
type WebhookService struct {
	uow      platformport.UnitOfWork
	store    WebhookStore
	identity identityport.AudienceVerifiedResolver
	refresh  AtomicRefreshRequester
	now      func() time.Time
}
type InboundResult struct {
	Receipt  segmentstore.WebhookReceipt
	Replayed bool
}
type VerifiedInboundFact struct {
	PackageKey    string
	EventID       string
	PayloadDigest [32]byte
	Identity      identitydomain.VerifiedFact
	OccurredAt    time.Time
}

func NewWebhookService(uow platformport.UnitOfWork, store WebhookStore, identity identityport.AudienceVerifiedResolver, refresh AtomicRefreshRequester) (*WebhookService, error) {
	if uow == nil || store == nil || identity == nil || refresh == nil {
		return nil, ErrNotReady
	}
	return &WebhookService{uow, store, identity, refresh, time.Now}, nil
}
func (s *WebhookService) Ingest(ctx context.Context, in VerifiedInboundFact) (InboundResult, error) {
	if s == nil || in.PackageKey == "" || in.EventID == "" || !in.Identity.Valid() || in.OccurredAt.IsZero() {
		return InboundResult{}, ErrInvalid
	}
	accepted := s.now().UTC()
	eventDigest := sha256.Sum256([]byte(in.EventID))
	ref := in.Identity.Reference()
	scopeDigest := sha256.Sum256([]byte(ref.Scope))
	valueDigest := sha256.Sum256([]byte(ref.NormalizedValue))
	var output InboundResult
	err := s.uow.Within(ctx, func(tx context.Context) error {
		packageID, e := s.store.PackageIDByCode(tx, in.PackageKey)
		if e != nil {
			return e
		}
		resolution, e := s.identity.ResolveAudienceFact(tx, in.Identity)
		if e != nil {
			return e
		}
		disposition := string(resolution.Status)
		switch resolution.Status {
		case identityport.AudienceResolved, identityport.AudienceUnresolved, identityport.AudienceConflict, identityport.AudienceInvalid:
		default:
			disposition = string(identityport.AudienceInvalid)
			resolution.CustomerID, resolution.IdentityID = 0, 0
		}
		customerID, identityID, refreshID := int64(resolution.CustomerID), resolution.IdentityID, int64(0)
		if resolution.Status == identityport.AudienceResolved {
			run, refreshErr := s.refresh.AcceptRefreshWithin(tx, RefreshCommand{PackageID: packageID, Actor: 1, IdempotencyKey: hex.EncodeToString(eventDigest[:]), ReferenceTime: in.OccurredAt})
			if refreshErr != nil {
				return refreshErr
			}
			refreshID = run.ID
		}
		receipt, owned, e := s.store.RecordWebhook(tx, segmentstore.WebhookReceipt{PackageID: packageID, EventIDDigest: eventDigest, PayloadDigest: in.PayloadDigest, IdentityKind: string(ref.Kind), IdentityScopeDigest: scopeDigest, IdentityValueDigest: valueDigest, Disposition: disposition, CustomerID: customerID, IdentityID: identityID, OccurredAt: in.OccurredAt.UTC(), AcceptedAt: accepted, RefreshRunID: refreshID})
		if e != nil {
			return e
		}
		output = InboundResult{Receipt: receipt, Replayed: !owned}
		if !owned {
			return nil
		}
		payload, _ := json.Marshal(map[string]any{"receipt_id": receipt.ID, "package_id": packageID, "disposition": disposition, "customer_id": customerID, "refresh_run_id": refreshID})
		_, e = s.store.AppendMutationFacts(tx, segmentstore.MutationFact{ResourceKind: "webhook_receipt", ResourceID: receipt.ID, Operation: "ingest", EventType: "audience.identity_fact.ingested.v1", ActorID: 1, Payload: payload, IdempotencyKey: "webhook:" + hex.EncodeToString(eventDigest[:]), OccurredAt: accepted})
		return e
	})
	return output, classify(err)
}
