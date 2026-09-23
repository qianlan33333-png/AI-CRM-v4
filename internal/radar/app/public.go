package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/radar"
	radarport "github.com/qianlan33333-png/AI-CRM-v3/internal/radar/port"
)

type PublicAccessService struct {
	uow        platformport.UnitOfWork
	repository radarport.Repository
	public     radarport.PublicRepository
	query      radarport.QueryService
	provider   radarport.OAuthProvider
	identity   radarport.IdentityCoordinator
	content    radarport.ContentReader
	now        func() time.Time
}

func NewPublicAccessService(uow platformport.UnitOfWork, repository radarport.Repository, public radarport.PublicRepository, query radarport.QueryService, provider radarport.OAuthProvider, identity radarport.IdentityCoordinator, content radarport.ContentReader) (*PublicAccessService, error) {
	if uow == nil || repository == nil || public == nil || query == nil || provider == nil || identity == nil || content == nil {
		return nil, radarport.ErrUnavailable
	}
	return &PublicAccessService{uow: uow, repository: repository, public: public, query: query, provider: provider, identity: identity, content: content, now: time.Now}, nil
}

func (s *PublicAccessService) Open(ctx context.Context, code radar.PublicCode, token string) (radarport.PublicAccess, error) {
	if s == nil || !code.Valid() {
		return radarport.PublicAccess{}, radar.ErrInvalidArgument
	}
	now := s.now().UTC()
	var link radar.Link
	var existing radarport.ViewSession
	if err := s.uow.Within(ctx, func(tx context.Context) error {
		var e error
		link, e = s.repository.GetByPublicCode(tx, code)
		if e != nil {
			return e
		}
		if link.Status != radar.StatusEnabled {
			return radarport.ErrGone
		}
		if validToken(token) {
			existing, e = s.public.ReadSession(tx, sha256.Sum256([]byte(token)), link.ID, link.Version, now)
			if e != nil && !errors.Is(e, radarport.ErrNotFound) {
				return e
			}
		}
		return nil
	}); err != nil {
		return radarport.PublicAccess{}, classify(err)
	}
	if existing.ID > 0 && (link.AuthPolicy == radar.AuthPolicyAnonymous || existing.Attribution == radarport.AttributionResolved) {
		return s.authorized(ctx, link, token, existing, now)
	}
	if link.AuthPolicy == radar.AuthPolicyUnionIDRequired {
		if !s.provider.Enabled() {
			return radarport.PublicAccess{}, radarport.ErrUnavailable
		}
		stateToken, e := secureToken()
		if e != nil {
			return radarport.PublicAccess{}, radarport.ErrUnavailable
		}
		stateDigest := sha256.Sum256([]byte(stateToken))
		e = s.uow.Within(ctx, func(tx context.Context) error {
			return s.public.CreateOAuthState(tx, stateDigest, radarport.OAuthState{RadarID: link.ID, Version: link.Version, Path: "/r/" + string(code), Expires: now.Add(10 * time.Minute)}, now)
		})
		if e != nil {
			return radarport.PublicAccess{}, classify(e)
		}
		return radarport.PublicAccess{Action: radarport.PublicOAuthRedirect, Location: s.provider.AuthorizationURL(stateToken), Link: link}, nil
	}
	sessionToken, e := secureToken()
	if e != nil {
		return radarport.PublicAccess{}, radarport.ErrUnavailable
	}
	digest := sha256.Sum256([]byte(sessionToken))
	session := radarport.ViewSession{RadarID: link.ID, Version: link.Version, Attribution: radarport.AttributionAnonymous, ExpiresAt: now.Add(30 * time.Minute)}
	e = s.uow.Within(ctx, func(tx context.Context) error {
		var err error
		session, err = s.public.CreateSession(tx, digest, session, [32]byte{}, now)
		if err != nil {
			return err
		}
		_, _, err = s.append(tx, session, radarport.EventLanding, "", now)
		return err
	})
	if e != nil {
		return radarport.PublicAccess{}, classify(e)
	}
	return s.authorized(ctx, link, sessionToken, session, now)
}

func (s *PublicAccessService) authorized(ctx context.Context, link radar.Link, token string, session radarport.ViewSession, now time.Time) (radarport.PublicAccess, error) {
	stage := radarport.EventContentOpened
	action := radarport.PublicViewer
	location := ""
	if link.Content.Type == radar.ContentTypeLink {
		stage = radarport.EventRedirected
		action = radarport.PublicLinkRedirect
		location = link.Content.DestinationURL
	}
	err := s.uow.Within(ctx, func(tx context.Context) error { _, _, e := s.append(tx, session, stage, "", now); return e })
	if err != nil {
		return radarport.PublicAccess{}, classify(err)
	}
	return radarport.PublicAccess{Action: action, Location: location, SessionToken: token, Link: link}, nil
}

func (s *PublicAccessService) CompleteOAuth(ctx context.Context, stateToken, code string) (string, string, error) {
	if s == nil || !s.provider.Enabled() || !validToken(stateToken) || strings.TrimSpace(code) != code || code == "" || len(code) > 512 {
		return "", "", radar.ErrInvalidArgument
	}
	fact, err := s.provider.Exchange(ctx, code)
	if err != nil {
		return "", "", radarport.ErrUnavailable
	}
	ref := fact.Reference()
	if !fact.Valid() || ref.Kind != identitydomain.KindUnionID || !strings.HasPrefix(ref.Scope, "wechat-open-platform:") {
		return "", "", radarport.ErrUnavailable
	}
	now := s.now().UTC()
	token, err := secureToken()
	if err != nil {
		return "", "", radarport.ErrUnavailable
	}
	sessionDigest := sha256.Sum256([]byte(token))
	stateDigest := sha256.Sum256([]byte(stateToken))
	evidence := sha256.Sum256([]byte(string(ref.Kind) + "\x00" + ref.Scope + "\x00" + ref.NormalizedValue))
	var state radarport.OAuthState
	err = s.uow.Within(ctx, func(tx context.Context) error {
		var e error
		state, e = s.public.ConsumeOAuthState(tx, stateDigest, now)
		if e != nil {
			return e
		}
		link, e := s.repository.Get(tx, state.RadarID)
		if e != nil {
			return e
		}
		if link.Status != radar.StatusEnabled || link.Version != state.Version || link.AuthPolicy != radar.AuthPolicyUnionIDRequired {
			return radarport.ErrConflict
		}
		declared := identitydomain.Reference{Kind: ref.Kind, Scope: ref.Scope, Value: ref.NormalizedValue, Assurance: identitydomain.AssuranceVerified, Source: ref.Source}
		resolved, e := s.identity.Resolve(tx, declared)
		if e != nil {
			return e
		}
		session := radarport.ViewSession{RadarID: state.RadarID, Version: state.Version, Attribution: radarport.AttributionResolved, ExpiresAt: now.Add(30 * time.Minute)}
		switch resolved.Status {
		case identityport.ResolveFound:
			session.IdentityID = resolved.IdentityID
			session.CustomerID = resolved.CustomerID
		case identityport.ResolveNotFound:
			provisioned, pe := s.identity.ProvisionVerifiedIdentity(tx, identityport.ProvisionCommand{Fact: fact, IdempotencyKey: "radar-oauth:" + hex.EncodeToString(evidence[:])})
			if pe != nil {
				return pe
			}
			session.IdentityID = provisioned.IdentityID
			session.CustomerID = provisioned.CustomerID
		case identityport.ResolveConflict:
			return radarport.ErrConflict
		default:
			return radarport.ErrUnavailable
		}
		session, e = s.public.CreateSession(tx, sessionDigest, session, evidence, now)
		if e != nil {
			return e
		}
		if _, _, e = s.append(tx, session, radarport.EventLanding, "", now); e != nil {
			return e
		}
		if _, _, e = s.append(tx, session, radarport.EventOAuthVerified, "", now); e != nil {
			return e
		}
		_, _, e = s.append(tx, session, radarport.EventIdentityResolved, "", now)
		return e
	})
	if err != nil {
		return "", "", classify(err)
	}
	return token, state.Path, nil
}

func (s *PublicAccessService) Content(ctx context.Context, code radar.PublicCode, token string) (radarport.Content, error) {
	if !code.Valid() || !validToken(token) {
		return radarport.Content{}, radar.ErrInvalidArgument
	}
	now := s.now().UTC()
	var link radar.Link
	var session radarport.ViewSession
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var e error
		link, e = s.repository.GetByPublicCode(tx, code)
		if e != nil {
			return e
		}
		if link.Status != radar.StatusEnabled {
			return radarport.ErrGone
		}
		if link.Content.Type == radar.ContentTypeLink {
			return radarport.ErrNotFound
		}
		session, e = s.public.ReadSession(tx, sha256.Sum256([]byte(token)), link.ID, link.Version, now)
		if e != nil {
			return e
		}
		if link.AuthPolicy == radar.AuthPolicyUnionIDRequired && session.Attribution != radarport.AttributionResolved {
			return radarport.ErrNotFound
		}
		return nil
	})
	if err != nil {
		return radarport.Content{}, classify(err)
	}
	content, err := s.content.ReadRadarContent(ctx, link.Content.Type, link.Content.MediaID)
	if err != nil {
		return radarport.Content{}, radarport.ErrUnavailable
	}
	return content, nil
}

func (s *PublicAccessService) Record(ctx context.Context, code radar.PublicCode, token string, stage radarport.EventStage, extra string) (radarport.EventProjection, bool, error) {
	if !code.Valid() || !validToken(token) || (stage != radarport.EventImageLoaded && stage != radarport.EventPDFOpened) {
		return radarport.EventProjection{}, false, radar.ErrInvalidArgument
	}
	if len(extra) > 1024 {
		return radarport.EventProjection{}, false, radar.ErrInvalidArgument
	}
	now := s.now().UTC()
	var out radarport.EventProjection
	var replay bool
	err := s.uow.Within(ctx, func(tx context.Context) error {
		link, e := s.repository.GetByPublicCode(tx, code)
		if e != nil {
			return e
		}
		if link.Status != radar.StatusEnabled {
			return radarport.ErrGone
		}
		session, e := s.public.ReadSession(tx, sha256.Sum256([]byte(token)), link.ID, link.Version, now)
		if e != nil {
			return e
		}
		if link.AuthPolicy == radar.AuthPolicyUnionIDRequired && session.Attribution != radarport.AttributionResolved {
			return radarport.ErrNotFound
		}
		out, replay, e = s.append(tx, session, stage, extra, now)
		return e
	})
	return out, replay, classify(err)
}

func (s *PublicAccessService) append(ctx context.Context, session radarport.ViewSession, stage radarport.EventStage, extra string, now time.Time) (radarport.EventProjection, bool, error) {
	payload := sha256.Sum256([]byte(string(stage) + "\x00" + extra))
	key := sha256.Sum256([]byte("radar-event\x00" + hex.EncodeToString(payload[:]) + "\x00" + time.Unix(0, int64(session.ID)).UTC().Format(time.RFC3339Nano)))
	receiptRaw := sha256.Sum256(append(key[:], payload[:]...))
	return s.public.AppendEvent(ctx, radarport.EventRecord{ReceiptID: "rre_" + hex.EncodeToString(receiptRaw[:16]), SessionID: session.ID, RadarID: session.RadarID, Version: session.Version, Stage: stage, Attribution: session.Attribution, IdentityID: session.IdentityID, CustomerID: session.CustomerID, KeyDigest: key, PayloadDigest: payload, OccurredAt: now}, now)
}

func secureToken() (string, error) {
	raw := make([]byte, 32)
	if _, e := rand.Read(raw); e != nil {
		return "", e
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
func validToken(v string) bool { return len(v) == 43 && strings.TrimSpace(v) == v }
