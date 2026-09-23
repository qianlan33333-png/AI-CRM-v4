package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"

	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
)

type OAuthProvider interface {
	Enabled() bool
	AuthorizationURL(string) string
	Exchange(context.Context, string) (identitydomain.VerifiedFact, error)
}
type OAuthState struct {
	Slug, Redirect string
	ExpiresAt      time.Time
}
type OAuthStore interface {
	GetPublishedBySlug(context.Context, string) (surveyport.Questionnaire, error)
	CreateOAuthState(context.Context, [32]byte, OAuthState, time.Time) error
	ConsumeOAuthState(context.Context, [32]byte, time.Time) (OAuthState, error)
	CreateIdentitySession(context.Context, [32]byte, surveyport.SubmissionIdentity, time.Time, time.Time) error
	ReadIdentitySession(context.Context, [32]byte, time.Time) (surveyport.SubmissionIdentity, error)
}
type OAuthService struct {
	uow      platformport.UnitOfWork
	store    OAuthStore
	provider OAuthProvider
	identity surveyport.IdentityCoordinator
	now      func() time.Time
}

func NewOAuthService(uow platformport.UnitOfWork, store OAuthStore, provider OAuthProvider, identity surveyport.IdentityCoordinator) *OAuthService {
	return &OAuthService{uow: uow, store: store, provider: provider, identity: identity, now: time.Now}
}
func (s *OAuthService) Enabled() bool { return s != nil && s.provider != nil && s.provider.Enabled() }
func (s *OAuthService) Start(ctx context.Context, slug string) (string, error) {
	if !s.Enabled() || !validSlug(slug) {
		return "", surveyport.ErrUnavailable
	}
	token, err := randomToken()
	if err != nil {
		return "", surveyport.ErrUnavailable
	}
	digest := sha256.Sum256([]byte(token))
	now := s.now().UTC()
	state := OAuthState{Slug: slug, ExpiresAt: now.Add(10 * time.Minute)}
	if err = s.uow.Within(ctx, func(tx context.Context) error {
		questionnaire, readErr := s.store.GetPublishedBySlug(tx, slug)
		if readErr != nil {
			return readErr
		}
		display := "all"
		if questionnaire.AnswerDisplayMode == surveyport.DisplayOneByOne {
			display = "one"
		}
		state.Redirect = "/h5/" + display + ".html?slug=" + slug
		return s.store.CreateOAuthState(tx, digest, state, now)
	}); err != nil {
		if errors.Is(err, surveyport.ErrNotFound) {
			return "", surveyport.ErrNotFound
		}
		return "", surveyport.ErrUnavailable
	}
	return s.provider.AuthorizationURL(token), nil
}
func (s *OAuthService) Complete(ctx context.Context, stateToken, code string) (string, string, error) {
	if !s.Enabled() || !validPublicKey(stateToken) {
		return "", "", surveyport.ErrInvalid
	}
	digest := sha256.Sum256([]byte(stateToken))
	now := s.now().UTC()
	var state OAuthState
	if err := s.uow.Within(ctx, func(tx context.Context) error {
		var e error
		state, e = s.store.ConsumeOAuthState(tx, digest, now)
		return e
	}); err != nil {
		return "", "", surveyport.ErrInvalid
	}
	fact, err := s.provider.Exchange(ctx, code)
	if err != nil {
		return "", "", surveyport.ErrUnavailable
	}
	reference := fact.Reference()
	declared := identitydomain.Reference{Kind: reference.Kind, Scope: reference.Scope, Value: reference.NormalizedValue, Assurance: identitydomain.AssuranceVerified, Source: reference.Source}
	evidence := sha256.Sum256([]byte(string(reference.Kind) + "\x00" + reference.Scope + "\x00" + reference.NormalizedValue))
	identity := surveyport.SubmissionIdentity{State: surveyport.IdentityUnresolved, EvidenceDigest: hex.EncodeToString(evidence[:])}
	session, err := randomToken()
	if err != nil {
		return "", "", surveyport.ErrUnavailable
	}
	sessionDigest := sha256.Sum256([]byte(session))
	err = s.uow.Within(ctx, func(tx context.Context) error {
		resolved, e := s.identity.Resolve(tx, declared)
		if e != nil {
			return e
		}
		switch resolved.Status {
		case identityport.ResolveFound:
			id := resolved.CustomerID
			identity.State = surveyport.IdentityResolved
			identity.CustomerID = &id
		case identityport.ResolveConflict:
			identity.State = surveyport.IdentityConflict
		case identityport.ResolveNotFound:
			provisioned, e := s.identity.ProvisionVerifiedIdentity(tx, identityport.ProvisionCommand{Fact: fact, IdempotencyKey: "survey-oauth:" + hex.EncodeToString(evidence[:])})
			if e != nil {
				return e
			}
			id := provisioned.CustomerID
			identity.State = surveyport.IdentityResolved
			identity.CustomerID = &id
		}
		return s.store.CreateIdentitySession(tx, sessionDigest, identity, now.Add(30*time.Minute), now)
	})
	if err != nil {
		return "", "", surveyport.ErrUnavailable
	}
	return session, state.Redirect, nil
}
func (s *OAuthService) ResolveSession(ctx context.Context, token string) (surveyport.SubmissionIdentity, error) {
	if !validPublicKey(token) {
		return surveyport.SubmissionIdentity{State: surveyport.IdentityAnonymous}, nil
	}
	digest := sha256.Sum256([]byte(token))
	var identity surveyport.SubmissionIdentity
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var e error
		identity, e = s.store.ReadIdentitySession(tx, digest, s.now().UTC())
		return e
	})
	if err != nil {
		return surveyport.SubmissionIdentity{State: surveyport.IdentityAnonymous}, nil
	}
	return identity, nil
}
func randomToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
