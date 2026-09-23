package channel

import (
	"context"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	platformoutbox "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/outbox"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"strings"
	"testing"
	"time"
)

func TestCustomerChannelActivitiesPostgreSQLHistoryAndRuntime(t *testing.T) {
	pool, cleanup := channelIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	admin := insertChannelAdmin(t, ctx, pool)
	customer := insertChannelCustomer(t, ctx, pool)
	other := insertChannelCustomer(t, ctx, pool)
	audit, _ := platformaudit.NewService(platformaudit.NewPostgreSQLStore())
	events, _ := NewChannelCatalogEventAppender(audit, platformoutbox.NewPostgreSQL())
	catalog := NewPostgreSQLCatalogStore()
	service := NewCatalogService(uow, catalog, catalog, events, nil, nil, fixedCatalogStaffReader{actorID: admin})
	create := validCatalogCreate()
	create.Config.Assignment.Assignees[0].StaffID = admin
	ch, err := service.Create(ctx, CatalogMutation{ActorID: admin, IdempotencyKey: "activity-channel-0001", Create: create})
	if err != nil {
		t.Fatal(err)
	}
	var binding int64
	err = pool.Native().QueryRow(ctx, `INSERT INTO channel_acquisition_state_bindings(corp_id,digest_key_version,state_digest,channel_id,asset_kind,asset_version,binding_digest,active_from)
 VALUES('activity',1,decode(repeat('01',32),'hex'),$1,'contact_way_qrcode',1,decode(repeat('02',32),'hex'),$2) RETURNING id`, ch.ID, now.Add(-time.Hour)).Scan(&binding)
	if err != nil {
		t.Fatal(err)
	}
	for i, at := range []time.Time{now.Add(-30 * time.Minute), now.Add(-time.Minute)} {
		inbox := insertChannelInbox(t, ctx, pool, "activity-inbox-"+at.Format("150405"))
		_, err = pool.Native().Exec(ctx, `INSERT INTO channel_acquisition_entrant_receipts(callback_id,inbox_id,corp_id,input_digest,command_digest,change_type,status,binding_id,customer_id,occurred_at)
 VALUES($1,$2,'activity',decode(repeat('03',32),'hex'),decode(repeat('04',32),'hex'),'add_external_contact','channel_attributed',$3,$4,$5)`, "activity-callback-"+at.Format("150405"), inbox, binding, customer, at)
		if err != nil {
			t.Fatalf("receipt %d: %v", i, err)
		}
	}
	// Re-imports of one source contact collapse to the latest snapshot. A null
	// historical identity is visible only after an explicit reconciliation.
	var historyID int64
	for i, snapshot := range []string{"activity-history-0001", "activity-history-0002"} {
		var run int64
		err = pool.Native().QueryRow(ctx, `INSERT INTO channel_history_import_runs(snapshot_id,source_host_digest,snapshot_timestamp,manifest_digest,state,completed_at)
 VALUES($1,decode(repeat('05',32),'hex'),$2,decode(repeat('06',32),'hex'),'completed',$2) RETURNING id`, snapshot, now.Add(time.Duration(i-10)*time.Minute)).Scan(&run)
		if err != nil {
			t.Fatal(err)
		}
		err = pool.Native().QueryRow(ctx, `INSERT INTO channel_history_contacts(import_run_id,channel_id,source_contact_id,customer_id,first_entered_at,last_entered_at,enter_count)
 VALUES($1,$2,99,NULL,$3,$4,2) RETURNING id`, run, ch.ID, now.Add(-time.Hour), now.Add(-20*time.Minute)).Scan(&historyID)
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err = pool.Native().Exec(ctx, `INSERT INTO channel_history_contact_reconciliations(history_contact_id,customer_id,evidence_digest,reconciled_at) VALUES($1,$2,decode(repeat('07',32),'hex'),$3)`, historyID, customer, now.Add(-5*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	store := NewPostgreSQLStore()
	q := customerport.PageQuery{Limit: 1, Watermark: now}
	var first, second, empty customerport.TimelinePage
	err = uow.Within(ctx, func(tx context.Context) error {
		var e error
		first, e = store.CustomerChannelActivities(tx, customer, q)
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 1 || !strings.Contains(first.Items[0].Title, "Autumn campaign") || first.Items[0].EventType != "channel.entered" {
		t.Fatalf("first=%+v", first)
	}
	q.AfterAt = first.Items[0].OccurredAt
	q.AfterID = first.Items[0].ID
	err = uow.Within(ctx, func(tx context.Context) error {
		var e error
		second, e = store.CustomerChannelActivities(tx, customer, q)
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 1 || second.Items[0].EventType != "channel.history_entered" {
		t.Fatalf("second=%+v", second)
	}
	q.AfterAt = second.Items[0].OccurredAt
	q.AfterID = second.Items[0].ID
	err = uow.Within(ctx, func(tx context.Context) error {
		var e error
		empty, e = store.CustomerChannelActivities(tx, customer, q)
		return e
	})
	if err != nil || len(empty.Items) != 0 {
		t.Fatalf("history double count %+v %v", empty, err)
	}
	q.AfterAt = time.Time{}
	q.AfterID = 0
	err = uow.Within(ctx, func(tx context.Context) error {
		var e error
		empty, e = store.CustomerChannelActivities(tx, other, q)
		return e
	})
	if err != nil || len(empty.Items) != 0 {
		t.Fatalf("identity leak %+v %v", empty, err)
	}
}
