package wecom

import (
	"context"
	"crypto/sha256"
	"errors"
	"sync"
	"testing"
	"time"

	channeldomain "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/domain"
	channelport "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/port"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
)

func TestExternalContactLifecycleCreatesCustomerAndKeepsUnboundEmployeeRelationship(t *testing.T) {
	identity := newMemoryLifecycleIdentity()
	relationships := &lifecycleRelationships{}
	receipts := &lifecycleReceipts{}
	service := lifecycleFor(identity, relationships, &lifecycleStates{}, receipts)

	result, err := service.ProcessWithin(context.Background(), lifecycleFact(t, ChangeAddExternalContact, "callback-add-1", "external-1", "unbound-employee", ""))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := result.Outcomes, []CallbackOutcome{OutcomeCustomerCreated, OutcomeRelationshipActivated, OutcomeChannelUnmatched}; !sameOutcomes(got, want) {
		t.Fatalf("outcomes=%v want=%v", got, want)
	}
	if result.CustomerID < 1 || identity.CustomerCount() != 1 || !relationships.active("wx-corp", "unbound-employee", result.CustomerID) {
		t.Fatalf("customer=%d count=%d relationships=%+v", result.CustomerID, identity.CustomerCount(), relationships.values)
	}
	// No staff/account resolver exists in this use case: the unbound WeCom
	// UserID itself is retained as the follow-relationship key.
	if len(receipts.values) != 1 || receipts.values[0].Resolution.Status != channeldomain.StateUnmatched {
		t.Fatalf("receipts=%+v", receipts.values)
	}
}

func TestExternalContactLifecycleStateResultsNeverBlockOneID(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		state   string
		resolve channeldomain.StateResolution
		outcome CallbackOutcome
	}{
		{name: "zero", state: "missing", resolve: channeldomain.StateResolution{Status: channeldomain.StateUnmatched}, outcome: OutcomeChannelUnmatched},
		{name: "multiple", state: "duplicate", resolve: channeldomain.StateResolution{Status: channeldomain.StateAmbiguous}, outcome: OutcomeChannelAmbiguous},
		{name: "one", state: "unique", resolve: channeldomain.StateResolution{Status: channeldomain.StateAttributed, Asset: channeldomain.AcquisitionAsset{ChannelID: 4, Kind: "qrcode", AssetVersion: 3}}, outcome: OutcomeChannelAttributed},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			identity := newMemoryLifecycleIdentity()
			receipts := &lifecycleReceipts{}
			service := lifecycleFor(identity, &lifecycleRelationships{}, &lifecycleStates{byState: map[[32]byte]channeldomain.StateResolution{stateDigest(testCase.state): testCase.resolve}}, receipts)
			result, err := service.ProcessWithin(context.Background(), lifecycleFact(t, ChangeAddHalfExternalContact, "callback-state-"+testCase.name, "external-"+testCase.name, "employee", testCase.state))
			if err != nil {
				t.Fatal(err)
			}
			if result.CustomerID < 1 || identity.CustomerCount() != 1 || result.Outcomes[len(result.Outcomes)-1] != testCase.outcome {
				t.Fatalf("result=%+v customers=%d", result, identity.CustomerCount())
			}
			if len(receipts.values) != 1 || receipts.values[0].Resolution.Status != testCase.resolve.Status {
				t.Fatalf("receipt=%+v", receipts.values)
			}
		})
	}
}

func TestExternalContactLifecycleEditRepairsAndDeletesNeverProvision(t *testing.T) {
	identity := newMemoryLifecycleIdentity()
	relationships := &lifecycleRelationships{}
	service := lifecycleFor(identity, relationships, &lifecycleStates{}, &lifecycleReceipts{})

	edited, err := service.ProcessWithin(context.Background(), lifecycleFact(t, ChangeEditExternalContact, "callback-edit", "external-edit", "employee", ""))
	if err != nil || !sameOutcomes(edited.Outcomes, []CallbackOutcome{OutcomeCustomerCreated, OutcomeRelationshipActivated}) {
		t.Fatalf("edit=%+v err=%v", edited, err)
	}
	deleteFact := lifecycleFact(t, ChangeDelExternalContact, "callback-del", "external-edit", "employee", "")
	deleteFact.OccurredAt = time.Unix(1_788_336_001, 0).UTC()
	deleted, err := service.ProcessWithin(context.Background(), deleteFact)
	if err != nil || !sameOutcomes(deleted.Outcomes, []CallbackOutcome{OutcomeCustomerResolved, OutcomeRelationshipDeactivated}) || deleted.CustomerID != edited.CustomerID {
		t.Fatalf("delete=%+v edit=%+v err=%v", deleted, edited, err)
	}
	missing, err := service.ProcessWithin(context.Background(), lifecycleFact(t, ChangeDelFollowUser, "callback-missing", "not-known", "employee", ""))
	if err != nil || !sameOutcomes(missing.Outcomes, []CallbackOutcome{OutcomeIgnored}) || identity.CustomerCount() != 1 {
		t.Fatalf("missing delete=%+v customers=%d err=%v", missing, identity.CustomerCount(), err)
	}
	if relationships.active("wx-corp", "employee", edited.CustomerID) {
		t.Fatal("delete should retain but deactivate the employee relationship")
	}
}

func TestExternalContactLifecycleOlderAddDoesNotReactivateNewerDeleteOrReportIt(t *testing.T) {
	identity := newMemoryLifecycleIdentity()
	relationships := &lifecycleRelationships{}
	service := lifecycleFor(identity, relationships, &lifecycleStates{}, &lifecycleReceipts{})

	added := lifecycleFact(t, ChangeAddExternalContact, "callback-add-new", "external-ordered", "employee", "")
	added.OccurredAt = time.Unix(300, 0).UTC()
	addedResult, err := service.ProcessWithin(context.Background(), added)
	if err != nil {
		t.Fatal(err)
	}
	deleted := lifecycleFact(t, ChangeDelFollowUser, "callback-delete-newer", "external-ordered", "employee", "")
	deleted.OccurredAt = time.Unix(400, 0).UTC()
	if result, err := service.ProcessWithin(context.Background(), deleted); err != nil || !sameOutcomes(result.Outcomes, []CallbackOutcome{OutcomeCustomerResolved, OutcomeRelationshipDeactivated}) {
		t.Fatalf("newer delete=%+v err=%v", result, err)
	}
	olderAdd := lifecycleFact(t, ChangeAddExternalContact, "callback-add-old", "external-ordered", "employee", "")
	olderAdd.OccurredAt = time.Unix(350, 0).UTC()
	result, err := service.ProcessWithin(context.Background(), olderAdd)
	if err != nil {
		t.Fatal(err)
	}
	if !sameOutcomes(result.Outcomes, []CallbackOutcome{OutcomeIgnored}) || result.CustomerID != 0 {
		t.Fatalf("older add must be ignored before OneID/channel effects: %+v", result)
	}
	if relationships.active("wx-corp", "employee", addedResult.CustomerID) {
		t.Fatal("older add reactivated relationship after newer delete")
	}
}

func TestExternalContactLifecycleDeleteExternalContactOnlyDeactivatesNamedEmployee(t *testing.T) {
	identity := newMemoryLifecycleIdentity()
	relationships := &lifecycleRelationships{}
	service := lifecycleFor(identity, relationships, &lifecycleStates{}, &lifecycleReceipts{})

	first := lifecycleFact(t, ChangeAddExternalContact, "callback-add-first", "external-full-delete", "employee-1", "")
	first.OccurredAt = time.Unix(300, 0).UTC()
	second := lifecycleFact(t, ChangeAddExternalContact, "callback-add-second", "external-full-delete", "employee-2", "")
	second.OccurredAt = time.Unix(301, 0).UTC()
	for _, fact := range []ExternalContactLifecycleFact{first, second} {
		if _, err := service.ProcessWithin(context.Background(), fact); err != nil {
			t.Fatal(err)
		}
	}
	deleted := lifecycleFact(t, ChangeDelExternalContact, "callback-delete-one", "external-full-delete", "employee-1", "")
	deleted.OccurredAt = time.Unix(400, 0).UTC()
	result, err := service.ProcessWithin(context.Background(), deleted)
	if err != nil || !sameOutcomes(result.Outcomes, []CallbackOutcome{OutcomeCustomerResolved, OutcomeRelationshipDeactivated}) {
		t.Fatalf("delete employee=%+v err=%v", result, err)
	}
	if relationships.active("wx-corp", "employee-1", result.CustomerID) || !relationships.active("wx-corp", "employee-2", result.CustomerID) {
		t.Fatalf("only named employee relationship must be inactive: %+v", relationships.values)
	}
}

func TestExternalContactLifecycleUnknownDeleteSuppressesOlderDelayedAdd(t *testing.T) {
	identity := newMemoryLifecycleIdentity()
	relationships := &lifecycleRelationships{}
	service := lifecycleFor(identity, relationships, &lifecycleStates{}, &lifecycleReceipts{})

	deleted := lifecycleFact(t, ChangeDelFollowUser, "callback-delete-newer", "external-late", "employee-1", "")
	deleted.OccurredAt = time.Unix(400, 0).UTC()
	result, err := service.ProcessWithin(context.Background(), deleted)
	if err != nil || !sameOutcomes(result.Outcomes, []CallbackOutcome{OutcomeIgnored}) {
		t.Fatalf("unknown delete=%+v err=%v", result, err)
	}
	olderAdd := lifecycleFact(t, ChangeAddExternalContact, "callback-add-older", "external-late", "employee-1", "")
	olderAdd.OccurredAt = time.Unix(300, 0).UTC()
	result, err = service.ProcessWithin(context.Background(), olderAdd)
	if err != nil || !sameOutcomes(result.Outcomes, []CallbackOutcome{OutcomeIgnored}) || result.CustomerID != 0 {
		t.Fatalf("older add=%+v err=%v", result, err)
	}
	if identity.CustomerCount() != 0 {
		t.Fatalf("older add after unknown delete created %d ghost customers", identity.CustomerCount())
	}
}

func TestExternalContactLifecycleRejectsPresentStateWithZeroDigest(t *testing.T) {
	fact := lifecycleFact(t, ChangeAddExternalContact, "callback-zero-digest", "external-zero-digest", "employee", "")
	fact.HasState = true
	if fact.Valid() {
		t.Fatal("present State must not use an all-zero digest")
	}
}

func TestCallbackEventRecognizesEveryLifecycleChangeType(t *testing.T) {
	for _, changeType := range []string{
		ChangeAddExternalContact,
		ChangeAddHalfExternalContact,
		ChangeEditExternalContact,
		ChangeDelFollowUser,
		ChangeDelExternalContact,
	} {
		event := CallbackEvent{Event: "change_external_contact", ChangeType: changeType}
		if !event.supported() {
			t.Fatalf("change type %q was not admitted to the lifecycle", changeType)
		}
	}
}

func TestExternalContactLifecycleConflictCannotAttributeOrWriteRelationship(t *testing.T) {
	relationships := &lifecycleRelationships{}
	receipts := &lifecycleReceipts{}
	states := &lifecycleStates{byState: map[[32]byte]channeldomain.StateResolution{
		stateDigest("unique"): {Status: channeldomain.StateAttributed, Asset: channeldomain.AcquisitionAsset{ChannelID: 1, Kind: "link", AssetVersion: 1}},
	}}
	service := lifecycleFor(conflictingLifecycleIdentity{}, relationships, states, receipts)

	result, err := service.ProcessWithin(context.Background(), lifecycleFact(t, ChangeAddExternalContact, "callback-conflict", "external-conflict", "employee", "unique"))
	if err != nil || !sameOutcomes(result.Outcomes, []CallbackOutcome{OutcomeIdentityConflict}) {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if len(relationships.values) != 0 || len(receipts.values) != 1 || receipts.values[0].Status != channelport.EntrantReceiptIdentityConflict || receipts.values[0].CustomerID != 0 {
		t.Fatalf("conflict must write only an isolated entrant receipt: relationships=%+v receipts=%+v", relationships.values, receipts.values)
	}
	if states.lastDigest() != ([32]byte{}) {
		t.Fatal("identity conflict must not resolve or attribute State")
	}
}

func TestExternalContactLifecycleUsesCanonicalCustomerFromIdentityPort(t *testing.T) {
	relationships := &lifecycleRelationships{}
	service := lifecycleFor(canonicalLifecycleIdentity{customerID: 99}, relationships, &lifecycleStates{}, &lifecycleReceipts{})
	result, err := service.ProcessWithin(context.Background(), lifecycleFact(t, ChangeEditExternalContact, "callback-canonical", "external-canonical", "employee", ""))
	if err != nil || result.CustomerID != 99 || !sameOutcomes(result.Outcomes, []CallbackOutcome{OutcomeCustomerResolved, OutcomeRelationshipActivated}) {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if !relationships.active("wx-corp", "employee", 99) {
		t.Fatalf("canonical root was not used: %+v", relationships.values)
	}
}

func TestExternalContactLifecycleUsesCallersTransactionContextForEveryPort(t *testing.T) {
	identity := newMemoryLifecycleIdentity()
	relationships := &lifecycleRelationships{}
	states := &lifecycleStates{byState: map[[32]byte]channeldomain.StateResolution{
		stateDigest("unique"): {Status: channeldomain.StateAttributed, Asset: channeldomain.AcquisitionAsset{ChannelID: 1, Kind: "qrcode", AssetVersion: 1}},
	}}
	receipts := &lifecycleReceipts{}
	service := lifecycleFor(identity, relationships, states, receipts)

	txContext := context.WithValue(context.Background(), lifecycleTransactionMarker{}, "delivery-tx-17")
	_, err := service.ProcessWithin(txContext, lifecycleFact(t, ChangeAddExternalContact, "callback-tx", "external-tx", "employee", "unique"))
	if err != nil {
		t.Fatal(err)
	}
	for name, marker := range map[string]string{
		"identity":        identity.lastMarker(),
		"relationships":   relationships.lastMarker(),
		"state resolver":  states.lastMarker(),
		"entrant receipt": receipts.lastMarker(),
	} {
		if marker != "delivery-tx-17" {
			t.Fatalf("%s saw transaction marker %q", name, marker)
		}
	}
	if got, want := states.lastDigest(), stateDigest("unique"); got != want {
		t.Fatalf("state resolver received digest %x, want %x", got, want)
	}
}

func TestExternalContactLifecycleConcurrentFirstAddsCreateOneCustomer(t *testing.T) {
	identity := newMemoryLifecycleIdentity()
	service := lifecycleFor(identity, &lifecycleRelationships{}, &lifecycleStates{}, &lifecycleReceipts{})

	const workers = 24
	results := make(chan ExternalContactLifecycleResult, workers)
	errs := make(chan error, workers)
	var group sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			result, err := service.ProcessWithin(context.Background(), lifecycleFact(t, ChangeAddExternalContact, "callback-race-"+string(rune('a'+index)), "external-race", "employee", ""))
			if err != nil {
				errs <- err
				return
			}
			results <- result
		}(worker)
	}
	group.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	var canonical customerdomain.CustomerID
	for result := range results {
		if result.CustomerID < 1 {
			t.Fatalf("unexpected result=%+v", result)
		}
		if canonical == 0 {
			canonical = result.CustomerID
		}
		if result.CustomerID != canonical {
			t.Fatalf("customer roots diverged: got=%d want=%d", result.CustomerID, canonical)
		}
	}
	if identity.CustomerCount() != 1 || identity.IdentityCount() != 1 {
		t.Fatalf("customers=%d identities=%d", identity.CustomerCount(), identity.IdentityCount())
	}
}

func lifecycleFor(identity ExternalContactIdentity, relationships CallbackFollowRelationshipStore, states channelport.StateResolver, receipts channelport.EntrantReceiptRecorder) ExternalContactLifecycle {
	return ExternalContactLifecycle{Identity: identity, Relationships: relationships, States: states, Entrants: receipts}
}

func lifecycleFact(t *testing.T, changeType, callbackID, externalID, employeeID, state string) ExternalContactLifecycleFact {
	t.Helper()
	verified, err := identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{
		Kind: identitydomain.KindWeComExternalUserID, Scope: "wecom-corp:wx-corp", Value: externalID, Source: "wecom.callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	fact := ExternalContactLifecycleFact{CallbackID: callbackID, InboxID: 17, CorpID: "wx-corp", ChangeType: changeType, ExternalUserID: externalID, EmployeeUserID: employeeID, OccurredAt: time.Unix(1_788_336_000, 0).UTC(), VerifiedIdentity: verified}
	if state != "" {
		fact.HasState = true
		fact.StateDigest = stateDigest(state)
	}
	return fact
}

func sameOutcomes(actual, wanted []CallbackOutcome) bool {
	if len(actual) != len(wanted) {
		return false
	}
	for index := range actual {
		if actual[index] != wanted[index] {
			return false
		}
	}
	return true
}

type lifecycleRelationships struct {
	mu         sync.Mutex
	values     map[string]bool
	orders     map[string]lifecycleRelationshipOrder
	external   map[string]lifecycleRelationshipOrder
	targets    map[string]CallbackFollowRelationship
	lastTxMark string
}

type lifecycleRelationshipOrder struct {
	occurredAt time.Time
	callbackID string
	active     bool
}

func (store *lifecycleRelationships) AdmitExternalContactEvent(ctx context.Context, event CallbackExternalContactEvent) (ExternalContactEventAdmission, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.external == nil {
		store.external = make(map[string]lifecycleRelationshipOrder)
	}
	key := event.CorpID + "\x00" + event.EmployeeID + "\x00" + string(event.ExternalIdentityDigest[:])
	previous, found := store.external[key]
	if !found {
		store.external[key] = lifecycleRelationshipOrder{occurredAt: event.OccurredAt, callbackID: event.CallbackID, active: event.Active}
		store.lastTxMark = lifecycleMarker(ctx)
		return ExternalContactEventAdmission{Admitted: true, Advanced: true, Active: event.Active}, nil
	}
	if previous.callbackID == event.CallbackID {
		return ExternalContactEventAdmission{Active: previous.active}, nil
	}
	switch compareFollowEvent(event.OccurredAt, event.Active, previous.occurredAt, previous.active) {
	case followEventStale:
		return ExternalContactEventAdmission{Active: previous.active}, nil
	case followEventEquivalent:
		return ExternalContactEventAdmission{Admitted: true, Active: previous.active}, nil
	case followEventNewer:
		store.external[key] = lifecycleRelationshipOrder{occurredAt: event.OccurredAt, callbackID: event.CallbackID, active: event.Active}
		store.lastTxMark = lifecycleMarker(ctx)
		return ExternalContactEventAdmission{Admitted: true, Advanced: true, Active: event.Active}, nil
	default:
		return ExternalContactEventAdmission{}, ErrFollowRelationshipConflict
	}
}

func (store *lifecycleRelationships) ApplyCallbackEvent(ctx context.Context, relationship CallbackFollowRelationship) (FollowRelationshipApplication, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.values == nil {
		store.values = make(map[string]bool)
	}
	if store.orders == nil {
		store.orders = make(map[string]lifecycleRelationshipOrder)
	}
	if store.targets == nil {
		store.targets = make(map[string]CallbackFollowRelationship)
	}
	key := lifecycleRelationshipKey(relationship.CorpID, relationship.EmployeeID, relationship.CustomerID)
	if previous, found := store.orders[key]; found && compareFollowEvent(relationship.OccurredAt, relationship.Active, previous.occurredAt, previous.active) != followEventNewer {
		return FollowRelationshipApplication{Active: store.values[key]}, nil
	}
	store.values[key] = relationship.Active
	store.orders[key] = lifecycleRelationshipOrder{occurredAt: relationship.OccurredAt, callbackID: relationship.CallbackID, active: relationship.Active}
	store.targets[key] = relationship
	store.lastTxMark = lifecycleMarker(ctx)
	return FollowRelationshipApplication{Applied: true, Active: relationship.Active}, nil
}

func (store *lifecycleRelationships) IsActive(_ context.Context, corpID, employeeID string, customerID customerdomain.CustomerID) (bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.values[lifecycleRelationshipKey(corpID, employeeID, customerID)], nil
}

func (store *lifecycleRelationships) active(corpID, employeeID string, customerID customerdomain.CustomerID) bool {
	active, _ := store.IsActive(context.Background(), corpID, employeeID, customerID)
	return active
}

func (store *lifecycleRelationships) lastMarker() string {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.lastTxMark
}

func lifecycleRelationshipKey(corpID, employeeID string, customerID customerdomain.CustomerID) string {
	return corpID + "\x00" + employeeID + "\x00" + string(rune(customerID))
}

type lifecycleStates struct {
	mu             sync.Mutex
	byState        map[[32]byte]channeldomain.StateResolution
	err            error
	lastTxMark     string
	observedDigest [32]byte
}

func (resolver *lifecycleStates) ResolveStateDigest(ctx context.Context, _ string, digest [32]byte, _ time.Time) (channeldomain.StateResolution, error) {
	resolver.mu.Lock()
	defer resolver.mu.Unlock()
	resolver.lastTxMark = lifecycleMarker(ctx)
	resolver.observedDigest = digest
	if resolver.err != nil {
		return channeldomain.StateResolution{}, resolver.err
	}
	if resolution, found := resolver.byState[digest]; found {
		return resolution, nil
	}
	return channeldomain.StateResolution{Status: channeldomain.StateUnmatched}, nil
}

func (resolver *lifecycleStates) lastDigest() [32]byte {
	resolver.mu.Lock()
	defer resolver.mu.Unlock()
	return resolver.observedDigest
}

func (resolver *lifecycleStates) lastMarker() string {
	resolver.mu.Lock()
	defer resolver.mu.Unlock()
	return resolver.lastTxMark
}

type lifecycleReceipts struct {
	mu         sync.Mutex
	values     []channelport.EntrantReceipt
	err        error
	lastTxMark string
}

func (recorder *lifecycleReceipts) RecordEntrantReceipt(ctx context.Context, receipt channelport.EntrantReceipt) error {
	if recorder.err != nil {
		return recorder.err
	}
	if !receipt.Valid() {
		return errors.New("invalid receipt")
	}
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	recorder.values = append(recorder.values, receipt)
	recorder.lastTxMark = lifecycleMarker(ctx)
	return nil
}

func (recorder *lifecycleReceipts) lastMarker() string {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return recorder.lastTxMark
}

type conflictingLifecycleIdentity struct{}

func (conflictingLifecycleIdentity) Resolve(context.Context, identitydomain.Reference) (identityport.ResolveResult, error) {
	return identityport.ResolveResult{Status: identityport.ResolveConflict}, nil
}

func (conflictingLifecycleIdentity) ProvisionVerifiedIdentity(context.Context, identityport.ProvisionCommand) (identityport.ProvisionResult, error) {
	return identityport.ProvisionResult{}, errors.New("must not provision conflict")
}

type canonicalLifecycleIdentity struct{ customerID customerdomain.CustomerID }

func (identity canonicalLifecycleIdentity) Resolve(context.Context, identitydomain.Reference) (identityport.ResolveResult, error) {
	return identityport.ResolveResult{Status: identityport.ResolveFound, CustomerID: identity.customerID, IdentityID: 7}, nil
}

func (canonicalLifecycleIdentity) ProvisionVerifiedIdentity(context.Context, identityport.ProvisionCommand) (identityport.ProvisionResult, error) {
	return identityport.ProvisionResult{}, errors.New("must not provision existing identity")
}

// memoryLifecycleIdentity is a concurrency-safe port double. It models the
// identity key uniqueness contract here, while identity/app separately tests
// its PostgreSQL-equivalent OneID implementation. Keeping this local avoids a
// forbidden concrete cross-domain test import.
type memoryLifecycleIdentity struct {
	mu         sync.Mutex
	byKey      map[string]identityport.ProvisionResult
	nextUserID customerdomain.CustomerID
	lastTxMark string
}

func newMemoryLifecycleIdentity() *memoryLifecycleIdentity {
	return &memoryLifecycleIdentity{byKey: make(map[string]identityport.ProvisionResult)}
}

func (identity *memoryLifecycleIdentity) Resolve(ctx context.Context, reference identitydomain.Reference) (identityport.ResolveResult, error) {
	identity.mu.Lock()
	defer identity.mu.Unlock()
	result, found := identity.byKey[memoryIdentityKey(reference.Scope, reference.Value)]
	identity.lastTxMark = lifecycleMarker(ctx)
	if !found {
		return identityport.ResolveResult{Status: identityport.ResolveNotFound}, nil
	}
	return identityport.ResolveResult{Status: identityport.ResolveFound, CustomerID: result.CustomerID, IdentityID: result.IdentityID}, nil
}

func (identity *memoryLifecycleIdentity) ProvisionVerifiedIdentity(ctx context.Context, command identityport.ProvisionCommand) (identityport.ProvisionResult, error) {
	identity.mu.Lock()
	defer identity.mu.Unlock()
	reference := command.Fact.Reference()
	identity.lastTxMark = lifecycleMarker(ctx)
	key := memoryIdentityKey(reference.Scope, reference.NormalizedValue)
	if existing, found := identity.byKey[key]; found {
		existing.Created = false
		return existing, nil
	}
	identity.nextUserID++
	created := identityport.ProvisionResult{CustomerID: identity.nextUserID, IdentityID: int64(identity.nextUserID), Created: true}
	identity.byKey[key] = created
	return created, nil
}

func (identity *memoryLifecycleIdentity) CustomerCount() int {
	identity.mu.Lock()
	defer identity.mu.Unlock()
	return len(identity.byKey)
}

func (identity *memoryLifecycleIdentity) IdentityCount() int { return identity.CustomerCount() }

func (identity *memoryLifecycleIdentity) lastMarker() string {
	identity.mu.Lock()
	defer identity.mu.Unlock()
	return identity.lastTxMark
}

func memoryIdentityKey(scope, value string) string { return scope + "\x00" + value }

func stateDigest(value string) [32]byte { return sha256.Sum256([]byte(value)) }

type lifecycleTransactionMarker struct{}

func lifecycleMarker(ctx context.Context) string {
	value, _ := ctx.Value(lifecycleTransactionMarker{}).(string)
	return value
}
