package wecom

import (
	"context"
	"testing"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	platformoutbox "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/outbox"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

type descriptionGapSyncStore struct {
	CustomerSyncStore
	item       SyncItem
	inserted   int
	profile    int
	observed   int
	pageCounts int
}

func (store *descriptionGapSyncStore) InsertItem(_ context.Context, _ int64, _ string, item SyncItem) (bool, error) {
	store.item = item
	store.inserted++
	return true, nil
}
func (store *descriptionGapSyncStore) UpsertProfile(_ context.Context, _ int64, _ string, _ identityport.ProvisionResult, _ wecomport.ExternalContact, _ [32]byte, _ time.Time) error {
	store.profile++
	return nil
}
func (store *descriptionGapSyncStore) UpsertProfileObservations(_ context.Context, _ int64, _ string, _ customerdomain.CustomerID, _ []wecomport.ExternalContactFollowInfo, _ time.Time) error {
	store.observed++
	return nil
}
func (store *descriptionGapSyncStore) AddCountsAndAdvance(context.Context, int64, int64, int64, int64, int64, int64, int64, int, string, CustomerSyncStatus) error {
	store.pageCounts++
	return nil
}

type descriptionGapProjection struct{ customerport.ProjectionWriter }

func (descriptionGapProjection) UpsertDirectoryProjection(context.Context, customerport.DirectoryProjection) error {
	return nil
}

type descriptionGapTimeline struct{ customerport.TimelineWriter }

func (descriptionGapTimeline) AppendTimeline(context.Context, customerport.TimelineEvent) error {
	return nil
}

type descriptionGapOutbox struct{ platformoutbox.Service }

func (descriptionGapOutbox) Append(_ context.Context, event platformoutbox.Event) (platformoutbox.Event, error) {
	return event, nil
}

type descriptionGapWriter struct {
	err   error
	calls int
}

func (writer *descriptionGapWriter) WriteContactDescriptionIntentWithin(context.Context, port.ContactDescriptionIntentCommand) (port.ContactDescriptionIntentResult, error) {
	writer.calls++
	return port.ContactDescriptionIntentResult{}, writer.err
}

type descriptionGapAudit struct{ events []platformaudit.Event }

func (audit *descriptionGapAudit) Append(_ context.Context, event platformaudit.Event) (platformaudit.Event, error) {
	audit.events = append(audit.events, event)
	return event, nil
}

func TestFullDirectorySyncNeverSubmitsDescriptionWrites(t *testing.T) {
	for _, trigger := range []string{"manual", "daily", "unionid_refresh"} {
		t.Run(trigger, func(t *testing.T) {
			store := &descriptionGapSyncStore{}
			writer := &descriptionGapWriter{err: port.ErrContactDescriptionInFlight}
			audit := &descriptionGapAudit{}
			identity := newMemoryLifecycleIdentity()
			service := CustomerSyncService{CorpID: "corp-1", Identity: identity, IdentityResolver: identity, IdentityLinker: identity, Projection: descriptionGapProjection{}, Timeline: descriptionGapTimeline{}, Store: store, Outbox: descriptionGapOutbox{}, DescriptionIntents: writer, Audit: audit, UOW: directUOW{}}
			description := "existing note"
			err := service.ingestPage(context.Background(), CustomerSyncRun{ID: 44, CorpScope: "corp-1", Trigger: trigger, Version: 3}, "staff-1", wecomport.ExternalContactPage{Contacts: []wecomport.ExternalContact{{ExternalUserID: "external-1", FollowInfo: []wecomport.ExternalContactFollowInfo{{EmployeeID: "staff-1", Description: &description, DescriptionProjected: true}}}}}, time.Now().UTC())
			if err != nil || store.inserted != 1 || writer.calls != 0 || len(audit.events) != 1 {
				t.Fatalf("err=%v inserted=%d writes=%d audits=%d", err, store.inserted, writer.calls, len(audit.events))
			}
		})
	}
}
