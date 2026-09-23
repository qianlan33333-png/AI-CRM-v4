package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"time"

	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
	distributionstore "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/store"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

const BrowserSessionTTL = 8 * time.Hour

// BrowserSessionService owns a Distribution-only access cookie. It is minted
// after a trusted Payment/WeChat session has been read by a composition
// bridge, but it never consumes that checkout session and never grants CRM
// employee authority or a checkout permission.
type BrowserSessionService struct {
	uow   platformport.UnitOfWork
	store interface {
		InsertBrowserSessionWithin(context.Context, distributionstore.BrowserSession) error
		ReadBrowserSessionWithin(context.Context, [32]byte, time.Time) (distributionstore.BrowserSession, error)
	}
	now func() time.Time
}

func NewBrowserSessionService(uow platformport.UnitOfWork, store interface {
	InsertBrowserSessionWithin(context.Context, distributionstore.BrowserSession) error
	ReadBrowserSessionWithin(context.Context, [32]byte, time.Time) (distributionstore.BrowserSession, error)
}) (*BrowserSessionService, error) {
	if uow == nil || store == nil {
		return nil, distributionport.ErrUnavailable
	}
	return &BrowserSessionService{uow: uow, store: store, now: time.Now}, nil
}

func (s *BrowserSessionService) Issue(ctx context.Context, actor distributionport.TrustedSessionActor) (string, time.Time, error) {
	if s == nil || s.uow == nil || s.store == nil || !actor.Valid() {
		return "", time.Time{}, distributionport.ErrUnauthorized
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", time.Time{}, distributionport.ErrUnavailable
	}
	token := "dist_" + base64.RawURLEncoding.EncodeToString(raw)
	digest := sha256.Sum256([]byte(token))
	now := s.now().UTC()
	if now.IsZero() {
		return "", time.Time{}, distributionport.ErrUnavailable
	}
	expiresAt := now.Add(BrowserSessionTTL)
	err := s.uow.Within(ctx, func(tx context.Context) error {
		return s.store.InsertBrowserSessionWithin(tx, distributionstore.BrowserSession{TokenDigest: digest, CustomerID: actor.CustomerID, IdentityID: actor.IdentityID, Channel: actor.Channel, AppID: actor.AppID, AppScope: actor.AppScope, ExpiresAt: expiresAt, CreatedAt: now})
	})
	if err != nil {
		return "", time.Time{}, err
	}
	return token, expiresAt, nil
}

func (s *BrowserSessionService) Resolve(ctx context.Context, token string) (distributionport.TrustedSessionActor, error) {
	if s == nil || s.uow == nil || s.store == nil || len(token) < 20 || len(token) > 100 {
		return distributionport.TrustedSessionActor{}, distributionport.ErrUnauthorized
	}
	digest := sha256.Sum256([]byte(token))
	now := s.now().UTC()
	var session distributionstore.BrowserSession
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var err error
		session, err = s.store.ReadBrowserSessionWithin(tx, digest, now)
		return err
	})
	if err != nil {
		return distributionport.TrustedSessionActor{}, err
	}
	actor := distributionport.TrustedSessionActor{CustomerID: session.CustomerID, IdentityID: session.IdentityID, Channel: session.Channel, AppID: session.AppID, AppScope: session.AppScope, OccurredAt: session.CreatedAt}
	if !actor.Valid() {
		return distributionport.TrustedSessionActor{}, distributionport.ErrUnauthorized
	}
	return actor, nil
}
