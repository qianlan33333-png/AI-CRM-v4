package wecom

import (
	"context"
	"testing"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	platformoutbox "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/outbox"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

type candidateSyncStore struct {
	CustomerSyncStore
	item             SyncItem
	itemInserted     bool
	profileRuns      map[customerdomain.CustomerID]int64
	ownerCustomers   []customerdomain.CustomerID
	activated        int64
	alreadyLinked    int64
	conflicts        int64
	terminalFailed   int64
	projected        int64
	version          int64
	staleIDs         []customerdomain.CustomerID
	reconciled       bool
	primaryRefreshed bool
	completed        bool
}

func (store *candidateSyncStore) InsertItem(_ context.Context, _ int64, _ string, item SyncItem) (bool, error) {
	store.item, store.itemInserted = item, true
	return true, nil
}
func (store *candidateSyncStore) UpsertProfile(_ context.Context, runID int64, _ string, provision identityport.ProvisionResult, _ wecomport.ExternalContact, _ [32]byte, _ time.Time) error {
	if store.profileRuns == nil {
		store.profileRuns = make(map[customerdomain.CustomerID]int64)
	}
	store.profileRuns[provision.CustomerID] = runID
	return nil
}
func (store *candidateSyncStore) UpsertProfileObservations(_ context.Context, _ int64, _ string, customerID customerdomain.CustomerID, _ []wecomport.ExternalContactFollowInfo, _ time.Time) error {
	store.ownerCustomers = append(store.ownerCustomers, customerID)
	return nil
}
func (store *candidateSyncStore) AddCountsAndAdvance(_ context.Context, _ int64, version int64, activated, linked, conflicts, terminal, projected int64, _ int, _ string, _ CustomerSyncStatus) error {
	store.activated += activated
	store.alreadyLinked += linked
	store.conflicts += conflicts
	store.terminalFailed += terminal
	store.projected += projected
	store.version = version + 1
	return nil
}
func (store *candidateSyncStore) StaleCustomers(_ context.Context, runID int64) ([]customerdomain.CustomerID, error) {
	store.staleIDs = nil
	for customerID, lastSeenRun := range store.profileRuns {
		if lastSeenRun != runID {
			store.staleIDs = append(store.staleIDs, customerID)
		}
	}
	return append([]customerdomain.CustomerID(nil), store.staleIDs...), nil
}
func (store *candidateSyncStore) ReconcileProfileObservations(context.Context, int64, time.Time) error {
	store.reconciled = true
	return nil
}
func (store *candidateSyncStore) RefreshProfilePrimaryOwners(context.Context, int64, time.Time) error {
	store.primaryRefreshed = true
	return nil
}
func (store *candidateSyncStore) Complete(_ context.Context, _ int64, _ int64, _ int64) error {
	if store.activated+store.alreadyLinked+store.conflicts+store.terminalFailed != 1 || store.projected != store.activated+store.alreadyLinked {
		return ErrSyncCAS
	}
	store.completed = true
	return nil
}

type candidateSyncProjection struct {
	customerport.ProjectionWriter
	customers []customerdomain.CustomerID
	staled    []customerdomain.CustomerID
}

func (projection *candidateSyncProjection) UpsertDirectoryProjection(_ context.Context, row customerport.DirectoryProjection) error {
	projection.customers = append(projection.customers, row.CustomerID)
	return nil
}
func (projection *candidateSyncProjection) MarkDirectoryStale(_ context.Context, ids []customerdomain.CustomerID, _ time.Time) (int64, error) {
	projection.staled = append([]customerdomain.CustomerID(nil), ids...)
	return int64(len(ids)), nil
}

type candidateSyncTimeline struct{ customerport.TimelineWriter }

func (candidateSyncTimeline) AppendTimeline(context.Context, customerport.TimelineEvent) error {
	return nil
}

type candidateSyncOutbox struct{ platformoutbox.Service }

func (candidateSyncOutbox) Append(_ context.Context, event platformoutbox.Event) (platformoutbox.Event, error) {
	return event, nil
}
func (candidateSyncOutbox) PendingForSyncRun(context.Context, int64) (int64, error) { return 0, nil }

type candidateSyncAudit struct{ events int }

func (audit *candidateSyncAudit) Append(_ context.Context, event platformaudit.Event) (platformaudit.Event, error) {
	audit.events++
	return event, nil
}

func TestCustomerSyncKeepsCrossRootCandidateProfileActiveAndCompletesRun(t *testing.T) {
	const (
		externalCustomerID customerdomain.CustomerID = 6
		payerCustomerID    customerdomain.CustomerID = 26
		openPlatformID                               = "platform-1"
		runID                                        = int64(88)
	)
	identity := newMemoryLifecycleIdentity()
	identity.byKey[memoryIdentityKey("wecom-corp:corp-1", "external-1")] = identityport.ProvisionResult{CustomerID: externalCustomerID, IdentityID: 61}
	identity.byKey[memoryIdentityKey("wechat-open-platform:"+openPlatformID, "union-payer")] = identityport.ProvisionResult{CustomerID: payerCustomerID, IdentityID: 261}
	store := &candidateSyncStore{profileRuns: map[customerdomain.CustomerID]int64{externalCustomerID: runID - 1}}
	projection := &candidateSyncProjection{}
	audit := &candidateSyncAudit{}
	service := CustomerSyncService{
		CorpID: "corp-1", UnionIDOpenPlatformID: openPlatformID,
		Identity: identity, IdentityResolver: identity, IdentityLinker: identity,
		Projection: projection, Timeline: candidateSyncTimeline{}, Store: store, Outbox: candidateSyncOutbox{}, Audit: audit, UOW: directUOW{},
	}
	contact := wecomport.ExternalContact{ExternalUserID: "external-1", UnionID: "union-payer",
		FollowInfo: []wecomport.ExternalContactFollowInfo{{EmployeeID: "staff-1"}}}
	started := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	if err := service.ingestPage(context.Background(), CustomerSyncRun{ID: runID, CorpScope: "wecom-corp:corp-1", Trigger: "daily", Version: 1, StaffIDs: []string{"staff-1"}}, "staff-1",
		wecomport.ExternalContactPage{Contacts: []wecomport.ExternalContact{contact}}, started); err != nil {
		t.Fatal(err)
	}
	if !store.itemInserted || store.item.Outcome != "conflict" || store.item.ErrorCode != "verified_identity_link_unresolved" || store.item.CustomerID != externalCustomerID {
		t.Fatalf("sync item=%+v", store.item)
	}
	if len(store.ownerCustomers) != 1 || store.ownerCustomers[0] != externalCustomerID || store.profileRuns[externalCustomerID] != runID {
		t.Fatalf("external-root observations=%v profile runs=%v", store.ownerCustomers, store.profileRuns)
	}
	if len(projection.customers) != 1 || projection.customers[0] != externalCustomerID {
		t.Fatalf("projected customer roots=%v", projection.customers)
	}
	if _, projectedPayer := store.profileRuns[payerCustomerID]; projectedPayer {
		t.Fatal("directory profile was projected to the unresolved payer root")
	}
	if store.conflicts != 1 || store.projected != 0 {
		t.Fatalf("identity accounting changed: conflict=%d projected=%d", store.conflicts, store.projected)
	}

	completedRun := CustomerSyncRun{ID: runID, Status: SyncReconciling, CorpScope: "wecom-corp:corp-1", Version: store.version,
		Discovered: 1, Conflict: 1, Projected: 0}
	if err := service.reconcile(context.Background(), completedRun); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(store.staleIDs) != 0 || len(projection.staled) != 0 || !store.reconciled || !store.primaryRefreshed || !store.completed {
		t.Fatalf("reconciliation stale=%v projection_stale=%v observations=%t owners=%t complete=%t", store.staleIDs, projection.staled, store.reconciled, store.primaryRefreshed, store.completed)
	}
	if identity.byKey[memoryIdentityKey("wecom-corp:corp-1", "external-1")].CustomerID != externalCustomerID ||
		identity.byKey[memoryIdentityKey("wechat-open-platform:"+openPlatformID, "union-payer")].CustomerID != payerCustomerID {
		t.Fatal("candidate confirmation was bypassed by directory reconciliation")
	}
	if audit.events != 2 {
		t.Fatalf("expected committed page and success audit events, got %d", audit.events)
	}
}
