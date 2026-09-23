package session

import (
	"context"
	"errors"
	"testing"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
)

type testUOW struct{}

func (testUOW) Within(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }

type provisionerStub struct {
	result identityport.ProvisionResult
	calls  int
}

func (stub *provisionerStub) ProvisionVerifiedIdentity(_ context.Context, command identityport.ProvisionCommand) (identityport.ProvisionResult, error) {
	stub.calls++
	if !command.Fact.Valid() {
		return identityport.ProvisionResult{}, errors.New("unverified")
	}
	return stub.result, nil
}

type memoryStore struct{ records map[[32]byte]Record }

type profileWriterStub struct {
	customerID  customerdomain.CustomerID
	observation customerport.ProviderProfileObservation
	err         error
}

func (stub *profileWriterStub) ObserveProviderProfile(_ context.Context, customerID customerdomain.CustomerID, observation customerport.ProviderProfileObservation) error {
	stub.customerID, stub.observation = customerID, observation
	return stub.err
}

func (store *memoryStore) Insert(_ context.Context, record Record) (Record, error) {
	if store.records == nil {
		store.records = make(map[[32]byte]Record)
	}
	record.ID = int64(len(store.records) + 1)
	store.records[record.TokenDigest] = record
	return record, nil
}

func (store *memoryStore) Consume(_ context.Context, digest [32]byte, now time.Time) (Record, error) {
	record, ok := store.records[digest]
	if !ok || record.ConsumedAt != nil || !record.ExpiresAt.After(now) {
		return Record{}, ErrExpired
	}
	record.ConsumedAt = &now
	store.records[digest] = record
	return record, nil
}

func (store *memoryStore) Lookup(_ context.Context, digest [32]byte, now time.Time) (Record, error) {
	record, ok := store.records[digest]
	if !ok || !record.ExpiresAt.After(now) {
		return Record{}, ErrExpired
	}
	return record, nil
}

func (store *memoryStore) SelectPayerSelf(_ context.Context, digest [32]byte, now time.Time) (Record, error) {
	record, ok := store.records[digest]
	if !ok || !record.ExpiresAt.After(now) {
		return Record{}, ErrExpired
	}
	if record.BeneficiarySelection == "payer_self" && record.BeneficiaryCustomerID == record.PayerCustomerID {
		return record, nil
	}
	if record.BeneficiarySelection != "unresolved" || record.BeneficiaryCustomerID != 0 || record.ConsumedAt != nil {
		return Record{}, ErrInvalid
	}
	record.BeneficiaryCustomerID = record.PayerCustomerID
	record.BeneficiarySelection = "payer_self"
	record.BeneficiarySelectedAt = &now
	store.records[digest] = record
	return record, nil
}

func verifiedFact(t *testing.T) identitydomain.VerifiedFact {
	t.Helper()
	fact, err := identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{
		Kind: identitydomain.KindMPOpenID, Scope: "wechat-app:wx-test", Value: "openid-test", Source: "wechat-oauth",
	})
	if err != nil {
		t.Fatal(err)
	}
	return fact
}

func TestTrustedSessionUsesOneIDAndIsSingleUse(t *testing.T) {
	provisioner := &provisionerStub{result: identityport.ProvisionResult{IdentityID: 8, CustomerID: 21}}
	store := &memoryStore{}
	profiles := &profileWriterStub{}
	service, err := NewService(testUOW{}, provisioner, profiles, store, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 3, 2, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	issued, err := service.IssueTrusted(context.Background(), IssueCommand{Fact: verifiedFact(t), IdempotencyKey: "oauth-callback-0001"})
	if err != nil || issued.Token == "" || issued.PayerIdentityID != 8 || issued.PayerCustomerID != 21 || issued.BeneficiaryCustomerID != 0 || issued.BeneficiarySelection != "unresolved" || provisioner.calls != 1 {
		t.Fatalf("issued=%+v calls=%d err=%v", issued, provisioner.calls, err)
	}
	if profiles.customerID != 21 || profiles.observation.DisplayName != "" || profiles.observation.Source != "wechat-oauth" || !profiles.observation.ObservedAt.Equal(now) {
		t.Fatalf("profile=%+v customer=%d", profiles.observation, profiles.customerID)
	}
	actor, err := service.SelectPayerSelfWithin(context.Background(), issued.Token, now)
	if err != nil || actor.PayerCustomerID != 21 || actor.BeneficiaryCustomerID != 21 || actor.BeneficiarySelection != "payer_self" {
		t.Fatalf("actor=%+v err=%v", actor, err)
	}
	record, err := service.Consume(context.Background(), issued.Token)
	if err != nil || record.PayerIdentityID != 8 || record.ConsumedAt == nil {
		t.Fatalf("record=%+v err=%v", record, err)
	}
	if _, err = service.Consume(context.Background(), issued.Token); !errors.Is(err, ErrExpired) {
		t.Fatalf("replay err=%v", err)
	}
	actor, err = service.LookupWithin(context.Background(), issued.Token, now)
	if err != nil || actor.PayerIdentityID != 8 || actor.PayerCustomerID != 21 {
		t.Fatalf("lookup actor=%+v err=%v", actor, err)
	}
}

func TestTrustedSessionRejectsUnverifiedOrUnauthorizedBeneficiary(t *testing.T) {
	provisioner := &provisionerStub{result: identityport.ProvisionResult{IdentityID: 8, CustomerID: 21}}
	service, _ := NewService(testUOW{}, provisioner, &profileWriterStub{}, &memoryStore{}, 5*time.Minute)
	if _, err := service.IssueTrusted(context.Background(), IssueCommand{BeneficiaryCustomerID: 42, Fact: verifiedFact(t), IdempotencyKey: "oauth-callback-0002"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("beneficiary err=%v", err)
	}
	if _, err := service.IssueTrusted(context.Background(), IssueCommand{BeneficiaryCustomerID: 42, AdminAssisted: true, Fact: verifiedFact(t), IdempotencyKey: "oauth-callback-0003"}); err != nil {
		t.Fatalf("admin-assisted err=%v", err)
	}
	if _, err := service.IssueTrusted(context.Background(), IssueCommand{IdempotencyKey: "oauth-callback-0004"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unverified err=%v", err)
	}
}

func TestTrustedSessionKeepsAdminAssistedBeneficiaryServerPrebound(t *testing.T) {
	provisioner := &provisionerStub{result: identityport.ProvisionResult{IdentityID: 8, CustomerID: 21}}
	service, _ := NewService(testUOW{}, provisioner, &profileWriterStub{}, &memoryStore{}, 5*time.Minute)
	issued, err := service.IssueTrusted(context.Background(), IssueCommand{BeneficiaryCustomerID: 42, AdminAssisted: true, Fact: verifiedFact(t), IdempotencyKey: "oauth-callback-0006"})
	if err != nil || issued.BeneficiaryCustomerID != 42 || issued.BeneficiarySelection != "admin_assisted" {
		t.Fatalf("issued=%+v err=%v", issued, err)
	}
	if _, err := service.IssueTrusted(context.Background(), IssueCommand{AdminAssisted: true, Fact: verifiedFact(t), IdempotencyKey: "oauth-callback-0007"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("missing prebound beneficiary err=%v", err)
	}
}

func TestSessionExpires(t *testing.T) {
	service, _ := NewService(testUOW{}, &provisionerStub{result: identityport.ProvisionResult{IdentityID: 8, CustomerID: 21}}, &profileWriterStub{}, &memoryStore{}, time.Minute)
	now := time.Date(2026, 9, 3, 2, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	issued, err := service.IssueTrusted(context.Background(), IssueCommand{Fact: verifiedFact(t), IdempotencyKey: "oauth-callback-0005"})
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now.Add(time.Minute) }
	if _, err = service.Consume(context.Background(), issued.Token); !errors.Is(err, ErrExpired) {
		t.Fatalf("expiry err=%v", err)
	}
}

func (stub *provisionerStub) ProvisionVerifiedOAuthSubject(_ context.Context, c identityport.OAuthSubjectCommand) (identityport.OAuthSubjectResult, error) {
	stub.calls++
	if !c.OpenID.Valid() || !c.UnionID.Valid() {
		return identityport.OAuthSubjectResult{}, ErrInvalid
	}
	return identityport.OAuthSubjectResult{ProvisionResult: stub.result}, nil
}
func TestH5SessionsRequireFreshUnionIDProof(t *testing.T) {
	ctx := context.Background()
	p := &provisionerStub{result: identityport.ProvisionResult{CustomerID: 11, IdentityID: 4}}
	store := &memoryStore{}
	profiles := &profileWriterStub{}
	service, _ := NewService(testUOW{}, p, profiles, store, 10*time.Minute)
	oa, _ := identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{Kind: identitydomain.KindOAOpenID, Scope: "wechat-app:oa", Value: "oa-id", Source: "provider.userinfo"})
	union, _ := identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{Kind: identitydomain.KindUnionID, Scope: "wechat-open-platform:platform", Value: "union-id", Source: "provider.userinfo"})
	if _, err := service.IssueTrusted(ctx, IssueCommand{Fact: oa, IdempotencyKey: "oauth-missing-union-001"}); err == nil || p.calls != 0 {
		t.Fatal("issued OpenID-only session")
	}
	issued, err := service.IssueTrusted(ctx, IssueCommand{Fact: oa, UnionID: union, DisplayName: "微信昵称", AvatarURL: "https://thirdwx.qlogo.cn/avatar", IdempotencyKey: "oauth-verified-union-01"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.LookupWithin(ctx, issued.Token, time.Now()); err != nil {
		t.Fatal(err)
	}
	if profiles.customerID != 11 || profiles.observation.DisplayName != "微信昵称" || profiles.observation.AvatarURL != "https://thirdwx.qlogo.cn/avatar" || profiles.observation.Source != "provider.userinfo" {
		t.Fatalf("profile customer=%d observation=%+v", profiles.customerID, profiles.observation)
	}
	for digest, record := range store.records {
		record.UnionIDVerified = false
		store.records[digest] = record
	}
	if _, err = service.LookupWithin(ctx, issued.Token, time.Now()); err == nil {
		t.Fatal("old OpenID session bypassed proof")
	}
}

func TestTrustedSessionDoesNotPersistWhenCustomerProfileFails(t *testing.T) {
	store := &memoryStore{}
	profiles := &profileWriterStub{err: errors.New("profile unavailable")}
	service, err := NewService(testUOW{}, &provisionerStub{result: identityport.ProvisionResult{IdentityID: 8, CustomerID: 21}}, profiles, store, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.IssueTrusted(context.Background(), IssueCommand{Fact: verifiedFact(t), IdempotencyKey: "oauth-profile-failure-01"}); err == nil {
		t.Fatal("profile failure issued a payment session")
	}
	if len(store.records) != 0 {
		t.Fatalf("stored sessions=%d", len(store.records))
	}
}
