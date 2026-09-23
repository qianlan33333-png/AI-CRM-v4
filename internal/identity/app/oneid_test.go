package app

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
)

type provisionedCustomerObserverStub struct {
	customerID customerdomain.CustomerID
	source     string
	calls      int
	err        error
}

func (stub *provisionedCustomerObserverStub) ObserveProvisionedCustomer(_ context.Context, customerID customerdomain.CustomerID, source string) error {
	stub.customerID, stub.source = customerID, source
	stub.calls++
	return stub.err
}

func TestResolveDoesNotProvisionCustomer(t *testing.T) {
	store := NewMemoryStore()
	service := OneIDService{Store: store}
	reference := identitydomain.Reference{
		Kind: identitydomain.KindWeComExternalUserID, Scope: "wecom-corp:main", Value: "external-1",
		Assurance: identitydomain.AssuranceDeclared, Source: "http.body",
	}
	result, err := service.Resolve(context.Background(), reference)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != identityport.ResolveNotFound || store.CustomerCount() != 0 || store.ActiveIdentityCount() != 0 {
		t.Fatalf("Resolve()=%+v customers=%d identities=%d", result, store.CustomerCount(), store.ActiveIdentityCount())
	}
}

func TestProvisionRequiresOpaqueVerifiedFact(t *testing.T) {
	store := NewMemoryStore()
	service := OneIDService{Store: store}
	if _, err := service.ProvisionCustomerFromVerifiedIdentity(context.Background(), identitydomain.VerifiedFact{}); err == nil {
		t.Fatal("zero VerifiedFact must be rejected")
	}
	if _, err := identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{
		Kind: identitydomain.KindWeComExternalUserID, Scope: "wecom-corp:", Value: "external-1", Source: "provider.adapter",
	}); err == nil {
		t.Fatal("invalid provider input must not construct a verified fact")
	}
	phoneFact := verifiedFact(t, identitydomain.KindPhone, "phone:cn11", "13800138000")
	if _, err := service.ProvisionCustomerFromVerifiedIdentity(context.Background(), phoneFact); !errors.Is(err, identitydomain.ErrInvalidReference) {
		t.Fatalf("verified phone provision error=%v", err)
	}
	if store.CustomerCount() != 0 {
		t.Fatal("phone identity provisioned a Customer")
	}
}

func TestProvisionObservesOnlyNewCanonicalCustomer(t *testing.T) {
	store := NewMemoryStore()
	observer := &provisionedCustomerObserverStub{}
	service := OneIDService{Store: store, ProvisionedCustomer: observer}
	fact := verifiedFact(t, identitydomain.KindOAOpenID, "wechat-app:main", "observed-openid")
	first, err := service.ProvisionCustomerFromVerifiedIdentity(context.Background(), fact)
	if err != nil || !first.Created || observer.calls != 1 || observer.customerID != first.CustomerID || observer.source != fact.Reference().Source {
		t.Fatalf("first=%+v observer=%+v err=%v", first, observer, err)
	}
	second, err := service.ProvisionCustomerFromVerifiedIdentity(context.Background(), fact)
	if err != nil || second.Created || second.CustomerID != first.CustomerID || observer.calls != 1 {
		t.Fatalf("second=%+v observer=%+v err=%v", second, observer, err)
	}
}

func TestProvisionFailsWhenMinimumCustomerProjectionFails(t *testing.T) {
	store := NewMemoryStore()
	observer := &provisionedCustomerObserverStub{err: errors.New("directory unavailable")}
	service := OneIDService{Store: store, ProvisionedCustomer: observer}
	_, err := service.ProvisionCustomerFromVerifiedIdentity(context.Background(), verifiedFact(t, identitydomain.KindOAOpenID, "wechat-app:main", "failed-observer-openid"))
	if err == nil || observer.calls != 1 {
		t.Fatalf("observer=%+v err=%v", observer, err)
	}
}

func TestProvisionHistoricalSubjectBindsMultipleVerifiedIdentitiesToOneRoot(t *testing.T) {
	store := NewMemoryStore()
	service := OneIDService{Store: store}
	result, err := service.ProvisionHistoricalSubject(context.Background(), identityport.HistoricalSubjectCommand{
		SubjectKey: "person-1",
		Facts: []identitydomain.VerifiedFact{
			verifiedFact(t, identitydomain.KindWeComExternalUserID, "wecom-corp:main", "history-wecom-1"),
			verifiedFact(t, identitydomain.KindMPOpenID, "wechat-app:main", "history-openid-1"),
		},
		SourceDigest: [32]byte{1},
	})
	if err != nil || result.CustomerID < 1 || len(result.IdentityIDs) != 2 || result.IdentityIDs[0] == result.IdentityIDs[1] {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if store.CustomerCount() != 1 || store.ActiveIdentityCount() != 2 {
		t.Fatalf("customers=%d identities=%d", store.CustomerCount(), store.ActiveIdentityCount())
	}
	replay, err := service.ProvisionHistoricalSubject(context.Background(), identityport.HistoricalSubjectCommand{
		SubjectKey: "person-1",
		Facts: []identitydomain.VerifiedFact{
			verifiedFact(t, identitydomain.KindWeComExternalUserID, "wecom-corp:main", "history-wecom-1"),
			verifiedFact(t, identitydomain.KindMPOpenID, "wechat-app:main", "history-openid-1"),
		},
		SourceDigest: [32]byte{1},
	})
	if err != nil || replay.CustomerID != result.CustomerID || store.CustomerCount() != 1 || store.ActiveIdentityCount() != 2 {
		t.Fatalf("replay=%+v err=%v", replay, err)
	}
}

func TestProvisionHistoricalSubjectRejectsPhoneIdentity(t *testing.T) {
	store := NewMemoryStore()
	service := OneIDService{Store: store}
	_, err := service.ProvisionHistoricalSubject(context.Background(), identityport.HistoricalSubjectCommand{
		SubjectKey: "phone-only",
		Facts: []identitydomain.VerifiedFact{
			verifiedFact(t, identitydomain.KindPhone, "phone:cn11", "13800138000"),
		},
		SourceDigest: [32]byte{1},
	})
	if !errors.Is(err, ErrHistoricalSubjectConflict) {
		t.Fatalf("err=%v", err)
	}
	if store.CustomerCount() != 0 || store.ActiveIdentityCount() != 0 {
		t.Fatalf("customers=%d identities=%d", store.CustomerCount(), store.ActiveIdentityCount())
	}
}

func TestProvisionHistoricalSubjectFailsClosedAcrossExistingRoots(t *testing.T) {
	store := NewMemoryStore()
	service := OneIDService{Store: store}
	leftFact := verifiedFact(t, identitydomain.KindWeComExternalUserID, "wecom-corp:main", "history-left")
	rightFact := verifiedFact(t, identitydomain.KindMPOpenID, "wechat-app:main", "history-right")
	left, err := service.ProvisionCustomerFromVerifiedIdentity(context.Background(), leftFact)
	if err != nil {
		t.Fatal(err)
	}
	right, err := service.ProvisionCustomerFromVerifiedIdentity(context.Background(), rightFact)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.ProvisionHistoricalSubject(context.Background(), identityport.HistoricalSubjectCommand{SubjectKey: "ambiguous", Facts: []identitydomain.VerifiedFact{leftFact, rightFact}, SourceDigest: [32]byte{2}}); !errors.Is(err, ErrHistoricalSubjectConflict) {
		t.Fatalf("err=%v", err)
	}
	if store.Root(left.CustomerID) != left.CustomerID || store.Root(right.CustomerID) != right.CustomerID {
		t.Fatal("historical subject conflict merged roots")
	}
}

func TestDeclaredPhoneAttachesOnlyToExistingCustomerAndReplays(t *testing.T) {
	store := NewMemoryStore()
	service := OneIDService{Store: store}
	first, err := service.ProvisionCustomerFromVerifiedIdentity(context.Background(), verifiedFact(t, identitydomain.KindWeComExternalUserID, "wecom-corp:main", "external-declared-1"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.ProvisionCustomerFromVerifiedIdentity(context.Background(), verifiedFact(t, identitydomain.KindWeComExternalUserID, "wecom-corp:main", "external-declared-2"))
	if err != nil {
		t.Fatal(err)
	}
	digest := [32]byte{1, 2, 3}
	command := identityport.DeclaredAttachCommand{CustomerID: first.CustomerID, Reference: identitydomain.Reference{Kind: identitydomain.KindPhone, Scope: "phone:e164", Value: "+8613812345678", Assurance: identitydomain.AssuranceDeclared, Source: "phone_import"}, ImportRunID: 1, SourceRowID: "row-1", SourceRowDigest: digest, IdempotencyKey: "phone-import:row-1"}
	result, err := service.AttachDeclaredIdentity(context.Background(), command)
	if err != nil || result.Status != identityport.DeclaredAttached {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	replay, err := service.AttachDeclaredIdentity(context.Background(), command)
	if err != nil || replay.Status != identityport.DeclaredReplayed || replay.ReplayOf != identityport.DeclaredAttached {
		t.Fatalf("replay=%+v err=%v", replay, err)
	}
	command.CustomerID = second.CustomerID
	command.SourceRowID = "row-2"
	command.SourceRowDigest = [32]byte{4, 5, 6}
	command.IdempotencyKey = "phone-import:row-2"
	conflict, err := service.AttachDeclaredIdentity(context.Background(), command)
	if err != nil || conflict.Status != identityport.DeclaredConflict {
		t.Fatalf("conflict=%+v err=%v", conflict, err)
	}
	invalid := command
	invalid.Reference.Assurance = identitydomain.AssuranceVerified
	if got, err := service.AttachDeclaredIdentity(context.Background(), invalid); err != nil || got.Status != identityport.DeclaredInvalid {
		t.Fatalf("invalid=%+v err=%v", got, err)
	}
}

func TestProvisionConcurrentScopedIdentityCreatesOneCustomer(t *testing.T) {
	store := NewMemoryStore()
	service := OneIDService{Store: store}
	fact := verifiedFact(t, identitydomain.KindWeComExternalUserID, "wecom-corp:main", "external-1")

	const workers = 20
	results := make(chan customerdomain.CustomerID, workers)
	errs := make(chan error, workers)
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			result, err := service.ProvisionCustomerFromVerifiedIdentity(context.Background(), fact)
			if err != nil {
				errs <- err
				return
			}
			results <- result.CustomerID
		}()
	}
	group.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	var customerID customerdomain.CustomerID
	for result := range results {
		if customerID == 0 {
			customerID = result
		}
		if result != customerID {
			t.Fatalf("concurrent provision returned customer %d after %d", result, customerID)
		}
	}
	if store.CustomerCount() != 1 || store.ActiveIdentityCount() != 1 {
		t.Fatalf("customers=%d identities=%d", store.CustomerCount(), store.ActiveIdentityCount())
	}
}

func TestSameValueDifferentScopesDoesNotResolveTogether(t *testing.T) {
	store := NewMemoryStore()
	service := OneIDService{Store: store}
	first, err := service.ProvisionCustomerFromVerifiedIdentity(context.Background(), verifiedFact(t, identitydomain.KindMPOpenID, "wechat-app:a", "same"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.ProvisionCustomerFromVerifiedIdentity(context.Background(), verifiedFact(t, identitydomain.KindMPOpenID, "wechat-app:b", "same"))
	if err != nil {
		t.Fatal(err)
	}
	if first.CustomerID == second.CustomerID || store.ActiveIdentityCount() != 2 {
		t.Fatalf("scope isolation failed: first=%+v second=%+v", first, second)
	}
}

func TestWeakLinkCreatesCandidateWithoutMerge(t *testing.T) {
	store, service, wecom, alipay, wecomFact := twoRoots(t)
	result, err := service.LinkVerifiedIdentity(context.Background(), LinkCommand{
		SourceCustomerID: alipay.CustomerID,
		Target:           wecomFact,
		Evidence:         evidence(identitydomain.EvidenceWeak),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != LinkCandidate || result.Candidate == nil || store.Root(alipay.CustomerID) != alipay.CustomerID || store.Root(wecom.CustomerID) != wecom.CustomerID {
		t.Fatalf("weak link=%+v", result)
	}
}

func TestWeakEvidenceCannotAttachANewIdentity(t *testing.T) {
	store := NewMemoryStore()
	service := OneIDService{Store: store}
	source, err := service.ProvisionCustomerFromVerifiedIdentity(context.Background(), verifiedFact(t, identitydomain.KindAlipayOAuthUserID, "alipay-app:main:production", "ali-1"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.LinkVerifiedIdentity(context.Background(), LinkCommand{
		SourceCustomerID: source.CustomerID,
		Target:           verifiedFact(t, identitydomain.KindWeComExternalUserID, "wecom-corp:main", "external-new"),
		Evidence:         evidence(identitydomain.EvidenceWeak),
	})
	if !errors.Is(err, ErrInsufficientLinkEvidence) {
		t.Fatalf("weak new-identity link error=%v", err)
	}
	if store.ActiveIdentityCount() != 1 {
		t.Fatalf("weak evidence attached an identity: identities=%d", store.ActiveIdentityCount())
	}
}

func TestConcurrentCrossRootLinksReuseOneOpenCandidate(t *testing.T) {
	_, service, _, alipay, wecomFact := twoRoots(t)

	const workers = 20
	results := make(chan LinkResult, workers)
	errs := make(chan error, workers)
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			result, err := service.LinkVerifiedIdentity(context.Background(), LinkCommand{
				SourceCustomerID: alipay.CustomerID,
				Target:           wecomFact,
				Evidence:         evidence(identitydomain.EvidenceStrong),
			})
			if err != nil {
				errs <- err
				return
			}
			results <- result
		}()
	}
	group.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	var candidateID int64
	for result := range results {
		if result.Status != LinkCandidate || result.Candidate == nil {
			t.Fatalf("link result=%+v", result)
		}
		if candidateID == 0 {
			candidateID = result.Candidate.ID
		}
		if result.Candidate.ID != candidateID {
			t.Fatalf("duplicate open candidates: first=%d got=%d", candidateID, result.Candidate.ID)
		}
	}
}

func TestStrongEvidenceUpgradesExistingWeakCandidate(t *testing.T) {
	_, service, _, alipay, wecomFact := twoRoots(t)
	weak, err := service.LinkVerifiedIdentity(context.Background(), LinkCommand{
		SourceCustomerID: alipay.CustomerID, Target: wecomFact, Evidence: evidence(identitydomain.EvidenceWeak),
	})
	if err != nil {
		t.Fatal(err)
	}
	strong, err := service.LinkVerifiedIdentity(context.Background(), LinkCommand{
		SourceCustomerID: alipay.CustomerID, Target: wecomFact, Evidence: evidence(identitydomain.EvidenceStrong),
	})
	if err != nil {
		t.Fatal(err)
	}
	if weak.Candidate == nil || strong.Candidate == nil || weak.Candidate.ID != strong.Candidate.ID ||
		strong.Candidate.Evidence.Strength != identitydomain.EvidenceStrong {
		t.Fatalf("candidate was not atomically upgraded: weak=%+v strong=%+v", weak, strong)
	}
}

func TestConfirmedStrongLinkPrefersWeComRootAndCanBeReverted(t *testing.T) {
	store, service, wecom, alipay, wecomFact := twoRoots(t)
	candidateResult, err := service.LinkVerifiedIdentity(context.Background(), LinkCommand{
		SourceCustomerID: alipay.CustomerID,
		Target:           wecomFact,
		Evidence:         evidence(identitydomain.EvidenceStrong),
	})
	if err != nil {
		t.Fatal(err)
	}
	if candidateResult.Status != LinkCandidate || candidateResult.Candidate == nil || store.Root(alipay.CustomerID) != alipay.CustomerID {
		t.Fatalf("cross-root link must remain candidate: %+v", candidateResult)
	}
	result, err := service.ConfirmMerge(context.Background(), ConfirmMergeCommand{
		CandidateID: candidateResult.Candidate.ID, SurvivorCustomerID: wecom.CustomerID, Operator: "admin-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != LinkMerged || result.Merge == nil || result.Merge.ToCustomerID != wecom.CustomerID || store.Root(alipay.CustomerID) != wecom.CustomerID {
		t.Fatalf("merge=%+v root=%d", result, store.Root(alipay.CustomerID))
	}
	reverted, err := service.RevertConfirmedMerge(context.Background(), result.Merge.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reverted.Reversed || store.Root(alipay.CustomerID) != alipay.CustomerID {
		t.Fatalf("revert=%+v root=%d", reverted, store.Root(alipay.CustomerID))
	}
}

func TestConfirmMergeNeverGuessesSurvivorOrOverridesWeComPriority(t *testing.T) {
	store, service, wecom, alipay, wecomFact := twoRoots(t)
	candidateResult, err := service.LinkVerifiedIdentity(context.Background(), LinkCommand{
		SourceCustomerID: alipay.CustomerID, Target: wecomFact, Evidence: evidence(identitydomain.EvidenceStrong),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ConfirmMerge(context.Background(), ConfirmMergeCommand{
		CandidateID: candidateResult.Candidate.ID, SurvivorCustomerID: alipay.CustomerID, Operator: "admin-1",
	}); err == nil {
		t.Fatal("operator must not select a non-WeCom survivor")
	}
	if store.Root(alipay.CustomerID) != alipay.CustomerID || store.Root(wecom.CustomerID) != wecom.CustomerID {
		t.Fatal("failed confirmation must not mutate roots")
	}
}

func TestConfirmMergeAcceptsExplicitHigherIDWhenNoPriorityRuleApplies(t *testing.T) {
	store := NewMemoryStore()
	service := OneIDService{Store: store}
	firstFact := verifiedFact(t, identitydomain.KindAlipayOAuthUserID, "alipay-app:main:production", "ali-first")
	secondFact := verifiedFact(t, identitydomain.KindFirstPartyMemberID, "first-party:main", "member-second")
	first, err := service.ProvisionCustomerFromVerifiedIdentity(context.Background(), firstFact)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.ProvisionCustomerFromVerifiedIdentity(context.Background(), secondFact)
	if err != nil {
		t.Fatal(err)
	}
	if second.CustomerID <= first.CustomerID {
		t.Fatalf("test requires a higher second id: first=%d second=%d", first.CustomerID, second.CustomerID)
	}
	candidate, err := service.LinkVerifiedIdentity(context.Background(), LinkCommand{
		SourceCustomerID: first.CustomerID, Target: secondFact, Evidence: evidence(identitydomain.EvidenceStrong),
	})
	if err != nil {
		t.Fatal(err)
	}
	merged, err := service.ConfirmMerge(context.Background(), ConfirmMergeCommand{
		CandidateID: candidate.Candidate.ID, SurvivorCustomerID: second.CustomerID, Operator: "admin-1",
	})
	if err != nil || merged.Status != LinkMerged || store.Root(first.CustomerID) != second.CustomerID {
		t.Fatalf("explicit higher-id survivor merge=%+v err=%v", merged, err)
	}
}

func TestConfirmMergeRejectsCandidateWhoseEndpointChanged(t *testing.T) {
	store := NewMemoryStore()
	service := OneIDService{Store: store}
	leftFact := verifiedFact(t, identitydomain.KindAlipayOAuthUserID, "alipay-app:main:production", "ali-left")
	rightFact := verifiedFact(t, identitydomain.KindFirstPartyMemberID, "first-party:main", "member-right")
	left, err := service.ProvisionCustomerFromVerifiedIdentity(context.Background(), leftFact)
	if err != nil {
		t.Fatal(err)
	}
	right, err := service.ProvisionCustomerFromVerifiedIdentity(context.Background(), rightFact)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := service.LinkVerifiedIdentity(context.Background(), LinkCommand{
		SourceCustomerID: left.CustomerID, Target: rightFact, Evidence: evidence(identitydomain.EvidenceStrong),
	})
	if err != nil {
		t.Fatal(err)
	}
	attached, err := service.LinkVerifiedIdentity(context.Background(), LinkCommand{
		SourceCustomerID: left.CustomerID,
		Target:           verifiedFact(t, identitydomain.KindWeComExternalUserID, "wecom-corp:main", "external-left"),
		Evidence:         evidence(identitydomain.EvidenceStrong),
	})
	if err != nil || attached.Status != LinkAttached {
		t.Fatalf("attach=%+v err=%v", attached, err)
	}
	rejected, err := service.ConfirmMerge(context.Background(), ConfirmMergeCommand{
		CandidateID: candidate.Candidate.ID, SurvivorCustomerID: left.CustomerID, Operator: "admin-1",
	})
	if err != nil || rejected.Status != LinkCandidateRejected || rejected.Candidate == nil ||
		rejected.Candidate.ID != candidate.Candidate.ID || rejected.Candidate.Status != "rejected" {
		t.Fatalf("stale candidate rejection=%+v err=%v", rejected, err)
	}
	if store.Root(left.CustomerID) != left.CustomerID || store.Root(right.CustomerID) != right.CustomerID {
		t.Fatal("stale candidate confirmation mutated customer roots")
	}
	if persisted := store.candidates[candidate.Candidate.ID]; persisted.Status != "rejected" {
		t.Fatalf("candidate status=%q", persisted.Status)
	}
	replacement, err := service.LinkVerifiedIdentity(context.Background(), LinkCommand{
		SourceCustomerID: left.CustomerID, Target: rightFact, Evidence: evidence(identitydomain.EvidenceStrong),
	})
	if err != nil || replacement.Status != LinkCandidate || replacement.Candidate == nil ||
		replacement.Candidate.ID == candidate.Candidate.ID || replacement.Candidate.Status != "open" {
		t.Fatalf("replacement candidate=%+v err=%v", replacement, err)
	}
}

func TestTwoWeComRootsCreateConflictAndNeverMerge(t *testing.T) {
	store := NewMemoryStore()
	service := OneIDService{Store: store}
	firstFact := verifiedFact(t, identitydomain.KindWeComExternalUserID, "wecom-corp:main", "external-a")
	secondFact := verifiedFact(t, identitydomain.KindWeComExternalUserID, "wecom-corp:main", "external-b")
	first, err := service.ProvisionCustomerFromVerifiedIdentity(context.Background(), firstFact)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.ProvisionCustomerFromVerifiedIdentity(context.Background(), secondFact)
	if err != nil {
		t.Fatal(err)
	}
	candidateResult, err := service.LinkVerifiedIdentity(context.Background(), LinkCommand{
		SourceCustomerID: first.CustomerID, Target: secondFact, Evidence: evidence(identitydomain.EvidenceStrong),
	})
	if err != nil {
		t.Fatal(err)
	}
	if candidateResult.Status != LinkCandidate || candidateResult.Candidate == nil {
		t.Fatalf("cross-root link must produce candidate: %+v", candidateResult)
	}
	result, err := service.ConfirmMerge(context.Background(), ConfirmMergeCommand{
		CandidateID: candidateResult.Candidate.ID, SurvivorCustomerID: first.CustomerID, Operator: "admin-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != LinkConflict || result.Conflict == nil || result.Conflict.Reason != "two_wecom_roots" ||
		store.Root(first.CustomerID) != first.CustomerID || store.Root(second.CustomerID) != second.CustomerID {
		t.Fatalf("double WeCom conflict=%+v", result)
	}
}

func TestSecondStrongWeComIdentityCannotAttachToSameRoot(t *testing.T) {
	store := NewMemoryStore()
	service := OneIDService{Store: store}
	first, err := service.ProvisionCustomerFromVerifiedIdentity(context.Background(), verifiedFact(t, identitydomain.KindWeComExternalUserID, "wecom-corp:main", "external-a"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.LinkVerifiedIdentity(context.Background(), LinkCommand{
		SourceCustomerID: first.CustomerID,
		Target:           verifiedFact(t, identitydomain.KindWeComExternalUserID, "wecom-corp:main", "external-b"),
		Evidence:         evidence(identitydomain.EvidenceStrong),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != LinkConflict || result.Conflict == nil ||
		result.Conflict.Reason != "two_wecom_identities_same_root" || store.ActiveIdentityCount() != 1 {
		t.Fatalf("same-root WeCom conflict=%+v identities=%d", result, store.ActiveIdentityCount())
	}
}

func TestLinkIntentIsSingleUseAndAttachesMissingIdentityWithoutSecondRoot(t *testing.T) {
	store := NewMemoryStore()
	service := OneIDService{Store: store}
	source, err := service.ProvisionCustomerFromVerifiedIdentity(context.Background(), verifiedFact(t, identitydomain.KindAlipayOAuthUserID, "alipay-app:main:production", "ali-1"))
	if err != nil {
		t.Fatal(err)
	}
	intent, err := service.CreateLinkIntent(context.Background(), LinkIntentCommand{
		SourceCustomerID: source.CustomerID, Purpose: LinkIntentBindWeCom,
		TargetKind: identitydomain.KindWeComExternalUserID, ExpectedScope: "wecom-corp:main",
		ExpiresAt: time.Now().Add(time.Minute), Source: "trusted.binding.entry",
	})
	if err != nil {
		t.Fatal(err)
	}
	if intent.Token == "" {
		t.Fatal("link intent must return token once")
	}
	result, err := service.ConsumeLinkIntent(context.Background(), ConsumeLinkIntentCommand{
		Token: intent.Token, Target: verifiedFact(t, identitydomain.KindWeComExternalUserID, "wecom-corp:main", "external-1"), Evidence: evidence(identitydomain.EvidenceStrong),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != LinkAttached || result.CustomerID != source.CustomerID || store.CustomerCount() != 1 {
		t.Fatalf("consume result=%+v customers=%d", result, store.CustomerCount())
	}
	replay, err := service.ConsumeLinkIntent(context.Background(), ConsumeLinkIntentCommand{
		Token: intent.Token, Target: verifiedFact(t, identitydomain.KindWeComExternalUserID, "wecom-corp:main", "external-1"), Evidence: evidence(identitydomain.EvidenceStrong),
	})
	if err != nil {
		t.Fatal(err)
	}
	if replay.Status != LinkIntentReplay {
		t.Fatalf("replay=%+v", replay)
	}
	if replay.ReplayOf != LinkAttached || replay.CustomerID != result.CustomerID || replay.IdentityID != result.IdentityID {
		t.Fatalf("replay lost the committed outcome: first=%+v replay=%+v", result, replay)
	}
	_, err = service.ConsumeLinkIntent(context.Background(), ConsumeLinkIntentCommand{
		Token: intent.Token, Target: verifiedFact(t, identitydomain.KindWeComExternalUserID, "wecom-corp:main", "external-drift"), Evidence: evidence(identitydomain.EvidenceStrong),
	})
	if !errors.Is(err, ErrLinkIntentPayloadMismatch) {
		t.Fatalf("payload-drift replay error=%v", err)
	}
}

func TestLinkIntentAcrossExistingRootsStillCreatesCandidate(t *testing.T) {
	store, service, wecom, alipay, wecomFact := twoRoots(t)
	intent, err := service.CreateLinkIntent(context.Background(), LinkIntentCommand{
		SourceCustomerID: alipay.CustomerID, Purpose: LinkIntentBindWeCom, TargetKind: identitydomain.KindWeComExternalUserID,
		ExpectedScope: "wecom-corp:main", ExpiresAt: time.Now().Add(time.Minute), Source: "trusted.binding.entry",
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.ConsumeLinkIntent(context.Background(), ConsumeLinkIntentCommand{
		Token: intent.Token, Target: wecomFact, Evidence: evidence(identitydomain.EvidenceStrong),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != LinkCandidate || result.Candidate == nil || store.Root(alipay.CustomerID) != alipay.CustomerID || store.Root(wecom.CustomerID) != wecom.CustomerID {
		t.Fatalf("link intent must not merge roots: %+v", result)
	}
}

func TestLinkIntentRejectsExpiredAndScopeMismatchedConsumption(t *testing.T) {
	store := NewMemoryStore()
	service := OneIDService{Store: store}
	source, err := service.ProvisionCustomerFromVerifiedIdentity(context.Background(), verifiedFact(t, identitydomain.KindAlipayOAuthUserID, "alipay-app:main:production", "ali-1"))
	if err != nil {
		t.Fatal(err)
	}
	expired, err := store.CreateLinkIntent(context.Background(), LinkIntentCommand{
		SourceCustomerID: source.CustomerID, Purpose: LinkIntentBindWeCom, TargetKind: identitydomain.KindWeComExternalUserID,
		ExpiresAt: time.Now().Add(-time.Second), Source: "trusted.binding.entry",
	})
	if err != nil {
		t.Fatal(err)
	}
	expiredResult, err := service.ConsumeLinkIntent(context.Background(), ConsumeLinkIntentCommand{
		Token: expired.Token, Target: verifiedFact(t, identitydomain.KindWeComExternalUserID, "wecom-corp:main", "external-expired"), Evidence: evidence(identitydomain.EvidenceStrong),
	})
	if err != nil || expiredResult.Status != LinkIntentExpired {
		t.Fatalf("expired consumption result=%+v err=%v", expiredResult, err)
	}
	expiredReplay, err := service.ConsumeLinkIntent(context.Background(), ConsumeLinkIntentCommand{
		Token: expired.Token, Target: verifiedFact(t, identitydomain.KindWeComExternalUserID, "wecom-corp:main", "external-expired"), Evidence: evidence(identitydomain.EvidenceStrong),
	})
	if err != nil || expiredReplay.Status != LinkIntentExpired {
		t.Fatalf("expired replay result=%+v err=%v", expiredReplay, err)
	}
	intent, err := service.CreateLinkIntent(context.Background(), LinkIntentCommand{
		SourceCustomerID: source.CustomerID, Purpose: LinkIntentBindWeCom, TargetKind: identitydomain.KindWeComExternalUserID,
		ExpectedScope: "wecom-corp:main", ExpiresAt: time.Now().Add(time.Minute), Source: "trusted.binding.entry",
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.ConsumeLinkIntent(context.Background(), ConsumeLinkIntentCommand{
		Token: intent.Token, Target: verifiedFact(t, identitydomain.KindWeComExternalUserID, "wecom-corp:other", "external-1"), Evidence: evidence(identitydomain.EvidenceStrong),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != LinkScopeMismatch || store.ActiveIdentityCount() != 1 {
		t.Fatalf("scope mismatch=%+v identities=%d", result, store.ActiveIdentityCount())
	}
}

func TestCreateLinkIntentRejectsInvalidNamespaceAndWeComPurposeDrift(t *testing.T) {
	store := NewMemoryStore()
	service := OneIDService{Store: store}
	source, err := service.ProvisionCustomerFromVerifiedIdentity(context.Background(), verifiedFact(t, identitydomain.KindAlipayOAuthUserID, "alipay-app:main:production", "ali-1"))
	if err != nil {
		t.Fatal(err)
	}
	base := LinkIntentCommand{
		SourceCustomerID: source.CustomerID,
		Purpose:          LinkIntentBindProviderIdentity,
		TargetKind:       identitydomain.KindMPOpenID,
		ExpectedScope:    "wecom-corp:wrong",
		ExpiresAt:        time.Now().Add(time.Minute),
		Source:           "trusted.binding.entry",
	}
	if _, err := service.CreateLinkIntent(context.Background(), base); !errors.Is(err, ErrInvalidLinkCommand) {
		t.Fatalf("wrong namespace error=%v", err)
	}
	base.Purpose = LinkIntentBindWeCom
	base.ExpectedScope = "wechat-app:main"
	if _, err := service.CreateLinkIntent(context.Background(), base); !errors.Is(err, ErrInvalidLinkCommand) {
		t.Fatalf("bind_wecom purpose drift error=%v", err)
	}
}

func TestLinkIntentIsInvalidatedWhenItsSourceRootWasMerged(t *testing.T) {
	store, service, wecom, alipay, wecomFact := twoRoots(t)
	intent, err := service.CreateLinkIntent(context.Background(), LinkIntentCommand{
		SourceCustomerID: alipay.CustomerID,
		Purpose:          LinkIntentBindProviderIdentity,
		TargetKind:       identitydomain.KindFirstPartyMemberID,
		ExpectedScope:    "first-party:main",
		ExpiresAt:        time.Now().Add(time.Minute),
		Source:           "trusted.binding.entry",
	})
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := service.LinkVerifiedIdentity(context.Background(), LinkCommand{
		SourceCustomerID: alipay.CustomerID, Target: wecomFact, Evidence: evidence(identitydomain.EvidenceStrong),
	})
	if err != nil {
		t.Fatal(err)
	}
	merged, err := service.ConfirmMerge(context.Background(), ConfirmMergeCommand{
		CandidateID: candidate.Candidate.ID, SurvivorCustomerID: wecom.CustomerID, Operator: "admin-1",
	})
	if err != nil || merged.Status != LinkMerged {
		t.Fatalf("merge=%+v err=%v", merged, err)
	}
	result, err := service.ConsumeLinkIntent(context.Background(), ConsumeLinkIntentCommand{
		Token:    intent.Token,
		Target:   verifiedFact(t, identitydomain.KindFirstPartyMemberID, "first-party:main", "member-after-merge"),
		Evidence: evidence(identitydomain.EvidenceStrong),
	})
	if err != nil || result.Status != LinkIntentInvalidated {
		t.Fatalf("stale intent result=%+v err=%v", result, err)
	}
	replay, err := service.ConsumeLinkIntent(context.Background(), ConsumeLinkIntentCommand{
		Token:    intent.Token,
		Target:   verifiedFact(t, identitydomain.KindFirstPartyMemberID, "first-party:main", "member-after-merge"),
		Evidence: evidence(identitydomain.EvidenceStrong),
	})
	if err != nil || replay.Status != LinkIntentInvalidated {
		t.Fatalf("stale intent replay=%+v err=%v", replay, err)
	}
	if store.ActiveIdentityCount() != 2 {
		t.Fatalf("stale intent attached an identity: identities=%d", store.ActiveIdentityCount())
	}
}

func TestRevertMergeLeavesLaterIdentityOnSurvivor(t *testing.T) {
	store, service, wecom, alipay, wecomFact := twoRoots(t)
	candidate, err := service.LinkVerifiedIdentity(context.Background(), LinkCommand{
		SourceCustomerID: alipay.CustomerID, Target: wecomFact, Evidence: evidence(identitydomain.EvidenceStrong),
	})
	if err != nil {
		t.Fatal(err)
	}
	merged, err := service.ConfirmMerge(context.Background(), ConfirmMergeCommand{
		CandidateID: candidate.Candidate.ID, SurvivorCustomerID: wecom.CustomerID, Operator: "admin-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	laterFact := verifiedFact(t, identitydomain.KindFirstPartyMemberID, "first-party:main", "member-later")
	attached, err := service.LinkVerifiedIdentity(context.Background(), LinkCommand{
		SourceCustomerID: wecom.CustomerID, Target: laterFact, Evidence: evidence(identitydomain.EvidenceStrong),
	})
	if err != nil || attached.Status != LinkAttached {
		t.Fatalf("later attach=%+v err=%v", attached, err)
	}
	if _, err := service.RevertConfirmedMerge(context.Background(), merged.Merge.ID); err != nil {
		t.Fatal(err)
	}
	resolved, err := service.Resolve(context.Background(), identitydomain.Reference{
		Kind: identitydomain.KindFirstPartyMemberID, Scope: "first-party:main", Value: "member-later",
		Assurance: identitydomain.AssuranceDeclared, Source: "admin.lookup",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.CustomerID != wecom.CustomerID || store.Root(alipay.CustomerID) != alipay.CustomerID {
		t.Fatalf("later identity moved during revert: resolved=%+v alipay_root=%d", resolved, store.Root(alipay.CustomerID))
	}
}

func TestRevertMergeRejectsAnyLaterRelatedMergeWithoutPartialMutation(t *testing.T) {
	store, service, wecom, alipay, wecomFact := twoRoots(t)
	firstCandidate, err := service.LinkVerifiedIdentity(context.Background(), LinkCommand{
		SourceCustomerID: alipay.CustomerID, Target: wecomFact, Evidence: evidence(identitydomain.EvidenceStrong),
	})
	if err != nil {
		t.Fatal(err)
	}
	firstMerge, err := service.ConfirmMerge(context.Background(), ConfirmMergeCommand{
		CandidateID: firstCandidate.Candidate.ID, SurvivorCustomerID: wecom.CustomerID, Operator: "admin-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	thirdFact := verifiedFact(t, identitydomain.KindFirstPartyMemberID, "first-party:main", "member-third")
	third, err := service.ProvisionCustomerFromVerifiedIdentity(context.Background(), thirdFact)
	if err != nil {
		t.Fatal(err)
	}
	secondCandidate, err := service.LinkVerifiedIdentity(context.Background(), LinkCommand{
		SourceCustomerID: third.CustomerID, Target: wecomFact, Evidence: evidence(identitydomain.EvidenceStrong),
	})
	if err != nil {
		t.Fatal(err)
	}
	secondMerge, err := service.ConfirmMerge(context.Background(), ConfirmMergeCommand{
		CandidateID: secondCandidate.Candidate.ID, SurvivorCustomerID: wecom.CustomerID, Operator: "admin-2",
	})
	if err != nil || secondMerge.Status != LinkMerged {
		t.Fatalf("second merge=%+v err=%v", secondMerge, err)
	}
	if _, err := service.RevertConfirmedMerge(context.Background(), firstMerge.Merge.ID); !errors.Is(err, ErrMergeNotReversible) {
		t.Fatalf("revert after later merge error=%v", err)
	}
	if store.Root(alipay.CustomerID) != wecom.CustomerID || store.Root(third.CustomerID) != wecom.CustomerID {
		t.Fatalf("failed revert partially mutated lineage: alipay=%d third=%d", store.Root(alipay.CustomerID), store.Root(third.CustomerID))
	}
}

func twoRoots(t *testing.T) (*MemoryStore, OneIDService, identityport.ProvisionResult, identityport.ProvisionResult, identitydomain.VerifiedFact) {
	t.Helper()
	store := NewMemoryStore()
	service := OneIDService{Store: store}
	wecomFact := verifiedFact(t, identitydomain.KindWeComExternalUserID, "wecom-corp:main", "external-1")
	wecom, err := service.ProvisionCustomerFromVerifiedIdentity(context.Background(), wecomFact)
	if err != nil {
		t.Fatal(err)
	}
	alipay, err := service.ProvisionCustomerFromVerifiedIdentity(context.Background(), verifiedFact(t, identitydomain.KindAlipayOAuthUserID, "alipay-app:main:production", "ali-1"))
	if err != nil {
		t.Fatal(err)
	}
	return store, service, wecom, alipay, wecomFact
}

func verifiedFact(t *testing.T, kind identitydomain.Kind, scope, value string) identitydomain.VerifiedFact {
	t.Helper()
	fact, err := identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{
		Kind: kind, Scope: scope, Value: value, Source: "provider.adapter",
	})
	if err != nil {
		t.Fatal(err)
	}
	return fact
}

func evidence(strength identitydomain.EvidenceStrength) identitydomain.LinkEvidence {
	return identitydomain.LinkEvidence{
		Type: "signed_link_intent", Strength: strength, Source: "provider.adapter", EventID: "event-1",
		Digest: "digest-only", PolicyVersion: "oneid-v1",
	}
}
