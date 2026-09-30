package wecom

import (
	"context"
	"encoding/json"
	"errors"
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

func TestCustomerSyncKeepsDescriptionReplanGapWithoutBlockingIdentityProjection(t *testing.T) {
	tests := []struct {
		name       string
		writeErr   error
		wantErr    error
		wantCode   string
		wantInsert int
	}{
		{name: "immutable description plan is an auditable optional gap", writeErr: port.ErrContactDescriptionReplanRequired, wantCode: "contact_description_replan_required", wantInsert: 1},
		{name: "in flight plan failure remains fatal", writeErr: port.ErrContactDescriptionInFlight, wantErr: port.ErrContactDescriptionInFlight},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			store := &descriptionGapSyncStore{}
			writer := &descriptionGapWriter{err: testCase.writeErr}
			audit := &descriptionGapAudit{}
			identity := newMemoryLifecycleIdentity()
			service := CustomerSyncService{
				CorpID: "corp-1", Identity: identity, IdentityResolver: identity, IdentityLinker: identity,
				Projection: descriptionGapProjection{}, Timeline: descriptionGapTimeline{},
				Store: store, Outbox: descriptionGapOutbox{}, DescriptionIntents: writer, Audit: audit, UOW: directUOW{},
			}
			description := "existing note"
			contact := wecomport.ExternalContact{
				ExternalUserID: "external-1",
				FollowInfo:     []wecomport.ExternalContactFollowInfo{{EmployeeID: "staff-1", Description: &description, DescriptionProjected: true}},
			}
			err := service.ingestPage(context.Background(), CustomerSyncRun{ID: 44, CorpScope: "corp-1", Trigger: "scheduled", Version: 3}, "staff-1",
				wecomport.ExternalContactPage{Contacts: []wecomport.ExternalContact{contact}}, time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC))
			if testCase.wantErr != nil {
				if !errors.Is(err, testCase.wantErr) || store.inserted != 0 {
					t.Fatalf("err=%v inserted=%d want fatal %v", err, store.inserted, testCase.wantErr)
				}
				return
			}
			if err != nil || store.inserted != testCase.wantInsert || store.item.ErrorCode != testCase.wantCode {
				t.Fatalf("err=%v inserted=%d item=%+v", err, store.inserted, store.item)
			}
			if store.profile != 1 || store.observed != 1 || store.pageCounts != 1 {
				t.Fatalf("sync projection stopped at note conflict: profile=%d observations=%d page=%d", store.profile, store.observed, store.pageCounts)
			}
			if writer.calls != 1 || len(audit.events) != 1 {
				t.Fatalf("description writes=%d audit events=%d", writer.calls, len(audit.events))
			}
			var payload map[string]any
			if err := json.Unmarshal(audit.events[0].Payload, &payload); err != nil {
				t.Fatal(err)
			}
			if payload["contact_description_replan_required"] != float64(1) || payload["pii"] != false {
				t.Fatalf("page audit payload=%s", audit.events[0].Payload)
			}
		})
	}
}
