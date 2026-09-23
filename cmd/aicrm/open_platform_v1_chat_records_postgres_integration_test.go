package main

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accessstore "github.com/qianlan33333-png/AI-CRM-v3/internal/access/store"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identityquery "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/query"
	archiveapp "github.com/qianlan33333-png/AI-CRM-v3/internal/messagearchive/app"
	archiveport "github.com/qianlan33333-png/AI-CRM-v3/internal/messagearchive/port"
	archivestore "github.com/qianlan33333-png/AI-CRM-v3/internal/messagearchive/store"
	openplatformport "github.com/qianlan33333-png/AI-CRM-v3/internal/openplatform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// TestV1ChatRecordsResolvesLocalStaffSelectorPostgreSQL exercises the Host
// and both Owner Ports with a real database. The selector is an existing local
// Access mapping; it does not invoke a WeCom Provider and it cannot widen a
// private conversation to another staff member.
func TestV1ChatRecordsResolvesLocalStaffSelectorPostgreSQL(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	databaseURL, cleanup := openPlatformMachineTestDatabase(t, ctx)
	defer cleanup()
	native, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer native.Close()
	if err = openPlatformV1ReadMigrate(ctx, native); err != nil {
		t.Fatal(err)
	}
	pool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}

	var customerID, selectedStaffID, otherStaffID int64
	if err = native.QueryRow(ctx, `INSERT INTO customers(status) VALUES('active') RETURNING id`).Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name,wecom_userid,is_active,login_enabled) VALUES('chat-selector-staff','$argon2id$fixture','Selected staff','follow-user-selected',true,false) RETURNING id`).Scan(&selectedStaffID); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name,wecom_userid,is_active,login_enabled) VALUES('chat-other-staff','$argon2id$fixture','Other staff','follow-user-other',true,false) RETURNING id`).Scan(&otherStaffID); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 13, 13, 0, 0, 0, time.UTC)
	if _, err = native.Exec(ctx, `INSERT INTO message_archive_sync_state(corp_scope) VALUES('wecom-corp:chat-selector')`); err != nil {
		t.Fatal(err)
	}
	insert := func(sequence, staffID int64, msgID string, at time.Time) {
		t.Helper()
		var archiveID int64
		if err = native.QueryRow(ctx, `INSERT INTO message_archive_messages(corp_scope,seq,msgid,msgtype,conversation_type,msgtime_ms,occurred_at,content_text,normalized_payload) VALUES('wecom-corp:chat-selector',$1,$2,'text','private',$3,$4,$5,'{}'::jsonb) RETURNING id`, sequence, msgID, at.UnixMilli(), at, "safe").Scan(&archiveID); err != nil {
			t.Fatal(err)
		}
		if _, err = native.Exec(ctx, `INSERT INTO message_archive_participants(message_id,participant_role,actor_type,provider_value,provider_value_digest,customer_id_at_ingest,resolution_status,resolution_reason,resolved_at) VALUES($1,'recipient','external_customer','',decode(repeat('01',32),'hex'),$2,'found','trusted_snapshot',$3),($1,'sender','staff','',decode(repeat('02',32),'hex'),NULL,'not_applicable','',NULL)`, archiveID, customerID, at); err != nil {
			t.Fatal(err)
		}
		if _, err = native.Exec(ctx, `UPDATE message_archive_participants SET staff_user_id=$2 WHERE message_id=$1 AND actor_type='staff'`, archiveID, staffID); err != nil {
			t.Fatal(err)
		}
	}
	// The other staff message is newer. A selector leak would return it first.
	insert(1, selectedStaffID, "selected-private", now.Add(-time.Minute))
	insert(2, otherStaffID, "other-private", now.Add(-30*time.Second))

	archive := archiveapp.Service{ReadEnabled: true, Store: archivestore.NewPostgreSQL(), UOW: uow, Lineage: identityquery.NewPostgreSQL(), Staff: accessstore.NewPostgreSQL(), StaffDirectory: accessstore.NewPostgreSQL()}
	executor := &openPlatformExecutor{archive: archive, activityNow: func() time.Time { return now }, v1ExternalCursorKey: []byte("open-platform-chat-selector-key-32")}
	if _, err = executor.v1ChatRecords(ctx, accessdomain.MachinePrincipal{}, []byte(fmt.Sprintf(`{"customer_id":%d}`, customerID))); openplatformport.ErrorCodeOf(err) != openplatformport.ErrorValidation {
		t.Fatalf("missing private selector error=%v", err)
	}
	resolvedStaffID, resolveErr := archive.V1ChatStaffID(ctx, "follow-user-selected")
	if resolveErr != nil || resolvedStaffID != selectedStaffID {
		t.Fatalf("local staff resolution id=%d err=%v", resolvedStaffID, resolveErr)
	}
	page, readErr := archive.V1ChatRecords(ctx, archiveport.V1ChatRecordQuery{CustomerID: customerdomain.CustomerID(customerID), ChatType: "private", StaffUserID: selectedStaffID, EndAt: now, Limit: 20})
	if readErr != nil || len(page.Items) != 1 || page.Items[0].MessageID != "selected-private" {
		t.Fatalf("archive scoped read page=%+v err=%v", page, readErr)
	}
	result, err := executor.v1ChatRecords(ctx, accessdomain.MachinePrincipal{}, []byte(fmt.Sprintf(`{"customer_id":%d,"staff_wecom_userid":"follow-user-selected"}`, customerID)))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(result.Data)
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		CustomerID string `json:"customer_id"`
		Items      []struct {
			MessageID string `json:"message_id"`
			Staff     []struct {
				ID string `json:"staff_id"`
			} `json:"staff"`
		} `json:"items"`
	}
	if err = json.Unmarshal(raw, &response); err != nil {
		t.Fatal(err)
	}
	if response.CustomerID != fmt.Sprint(customerID) || len(response.Items) != 1 || response.Items[0].MessageID != "selected-private" || len(response.Items[0].Staff) != 1 || response.Items[0].Staff[0].ID != fmt.Sprint(selectedStaffID) {
		t.Fatalf("selected chat response=%s", raw)
	}
}
