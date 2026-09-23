package app

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"testing"
	"time"

	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
)

type oauthUOW struct{}

func (oauthUOW) Within(ctx context.Context, run func(context.Context) error) error { return run(ctx) }

type oauthStore struct {
	state                      OAuthState
	stateDigest, sessionDigest [32]byte
	identity                   surveyport.SubmissionIdentity
	consumed                   bool
	getErr                     error
}

func (s *oauthStore) GetPublishedBySlug(_ context.Context, slug string) (surveyport.Questionnaire, error) {
	if s.getErr != nil {
		return surveyport.Questionnaire{}, s.getErr
	}
	return surveyport.Questionnaire{Slug: slug, AnswerDisplayMode: surveyport.DisplayAllInOne}, nil
}

func (s *oauthStore) CreateOAuthState(_ context.Context, digest [32]byte, state OAuthState, _ time.Time) error {
	s.stateDigest, s.state = digest, state
	return nil
}
func (s *oauthStore) ConsumeOAuthState(_ context.Context, digest [32]byte, _ time.Time) (OAuthState, error) {
	if s.consumed || digest != s.stateDigest {
		return OAuthState{}, surveyport.ErrNotFound
	}
	s.consumed = true
	return s.state, nil
}
func (s *oauthStore) CreateIdentitySession(_ context.Context, digest [32]byte, identity surveyport.SubmissionIdentity, _, _ time.Time) error {
	s.sessionDigest, s.identity = digest, identity
	return nil
}
func (s *oauthStore) ReadIdentitySession(_ context.Context, digest [32]byte, _ time.Time) (surveyport.SubmissionIdentity, error) {
	if digest != s.sessionDigest {
		return surveyport.SubmissionIdentity{}, surveyport.ErrNotFound
	}
	return s.identity, nil
}

type oauthProvider struct{ fact identitydomain.VerifiedFact }

func (oauthProvider) Enabled() bool { return true }
func (oauthProvider) AuthorizationURL(state string) string {
	return "https://provider.example/oauth?state=" + state
}
func (p oauthProvider) Exchange(context.Context, string) (identitydomain.VerifiedFact, error) {
	return p.fact, nil
}

type oauthIdentity struct{}

func (oauthIdentity) Resolve(context.Context, identitydomain.Reference) (identityport.ResolveResult, error) {
	return identityport.ResolveResult{Status: identityport.ResolveNotFound}, nil
}
func (oauthIdentity) ProvisionVerifiedIdentity(context.Context, identityport.ProvisionCommand) (identityport.ProvisionResult, error) {
	return identityport.ProvisionResult{CustomerID: 42, IdentityID: 9, Created: true}, nil
}

func TestOAuthConsumesStateAndProvisionsOnlyProviderVerifiedFact(t *testing.T) {
	fact, err := identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{Kind: identitydomain.KindUnionID, Scope: "wechat-open-platform:platform", Value: "provider-union", Source: "wechat.survey.oauth"})
	if err != nil {
		t.Fatal(err)
	}
	store := &oauthStore{}
	service := NewOAuthService(oauthUOW{}, store, oauthProvider{fact: fact}, oauthIdentity{})
	fixed := time.Date(2026, 9, 3, 8, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return fixed }
	location, err := service.Start(context.Background(), "growth-test")
	if err != nil || !strings.HasPrefix(location, "https://provider.example/") {
		t.Fatalf("start=%q err=%v", location, err)
	}
	state := strings.TrimPrefix(location, "https://provider.example/oauth?state=")
	if sha256.Sum256([]byte(state)) != store.stateDigest {
		t.Fatal("state digest not persisted")
	}
	session, redirect, err := service.Complete(context.Background(), state, "provider-code")
	if err != nil || redirect != "/h5/all.html?slug=growth-test" || len(session) != 43 || store.identity.State != surveyport.IdentityResolved || store.identity.CustomerID == nil || *store.identity.CustomerID != 42 || len(store.identity.EvidenceDigest) != 64 {
		t.Fatalf("complete redirect=%q identity=%+v err=%v", redirect, store.identity, err)
	}
	if _, _, err = service.Complete(context.Background(), state, "provider-code"); err == nil {
		t.Fatal("OAuth state replay accepted")
	}
}

func TestOAuthStartPreservesMissingQuestionnaireWithoutCreatingState(t *testing.T) {
	store := &oauthStore{getErr: surveyport.ErrNotFound}
	service := NewOAuthService(oauthUOW{}, store, oauthProvider{}, oauthIdentity{})
	if _, err := service.Start(context.Background(), "missing"); !errors.Is(err, surveyport.ErrNotFound) {
		t.Fatalf("start error=%v", err)
	}
	if store.stateDigest != ([32]byte{}) {
		t.Fatal("OAuth state was persisted for missing questionnaire")
	}
}
