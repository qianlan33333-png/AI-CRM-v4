package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	archiveport "github.com/qianlan33333-png/AI-CRM-v3/internal/messagearchive/port"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func TestPostgreSQLV1ChatRecordsScopeBeforeKeysetAndFreezeEnd(t *testing.T) {
	native, cleanup := archiveIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 9, 13, 11, 0, 0, 0, time.UTC)

	var customerID, otherCustomerID, staffID, otherStaffID int64
	if err := native.QueryRow(ctx, `INSERT INTO customers(status) VALUES('active') RETURNING id`).Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	if err := native.QueryRow(ctx, `INSERT INTO customers(status) VALUES('active') RETURNING id`).Scan(&otherCustomerID); err != nil {
		t.Fatal(err)
	}
	if err := native.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name) VALUES('archive-v1-staff','$argon2id$test','Archive V1 staff') RETURNING id`).Scan(&staffID); err != nil {
		t.Fatal(err)
	}
	if err := native.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name) VALUES('archive-v1-other-staff','$argon2id$test','Archive V1 other staff') RETURNING id`).Scan(&otherStaffID); err != nil {
		t.Fatal(err)
	}
	if _, err := native.Exec(ctx, `INSERT INTO message_archive_sync_state(corp_scope) VALUES('wecom-corp:archive-v1-test')`); err != nil {
		t.Fatal(err)
	}

	sequence := int64(1)
	insertMessage := func(customer, staff int64, chatType, messageType string, occurredAt time.Time, withReadyMedia bool) int64 {
		t.Helper()
		var messageID int64
		if err := native.QueryRow(ctx, `
			INSERT INTO message_archive_messages(corp_scope,seq,msgid,msgtype,conversation_type,roomid,msgtime_ms,occurred_at,content_text,normalized_payload)
			VALUES('wecom-corp:archive-v1-test',$1,$2,$3,$4,$5,$6,$7,$8,'{}'::jsonb)
			RETURNING id`, sequence, "archive-v1-message-"+hex.EncodeToString([]byte{byte(sequence)}), messageType, chatType, "room-projection", occurredAt.UnixMilli(), occurredAt, "safe archive content").Scan(&messageID); err != nil {
			t.Fatal(err)
		}
		sequence++
		customerDigest, staffDigest := make([]byte, 32), make([]byte, 32)
		customerDigest[0], staffDigest[0] = byte(sequence), byte(sequence+1)
		sequence += 2
		if _, err := native.Exec(ctx, `
			INSERT INTO message_archive_participants(message_id,participant_role,actor_type,provider_value,provider_value_digest,customer_id_at_ingest,resolution_status,resolution_reason,resolved_at)
			VALUES($1,'recipient','external_customer','',$2,$3,'found','trusted_snapshot',$4)`, messageID, customerDigest, customer, occurredAt); err != nil {
			t.Fatal(err)
		}
		if _, err := native.Exec(ctx, `
			INSERT INTO message_archive_participants(message_id,participant_role,actor_type,provider_value,provider_value_digest,staff_user_id,resolution_status)
			VALUES($1,'sender','staff','',$2,$3,'not_applicable')`, messageID, staffDigest, staff); err != nil {
			t.Fatal(err)
		}
		if withReadyMedia {
			mediaDigest := make([]byte, 32)
			mediaDigest[0] = byte(sequence)
			sequence++
			if _, err := native.Exec(ctx, `
				INSERT INTO message_archive_media(message_id,media_kind,provider_file_ref,provider_file_digest,status)
				VALUES($1,'image','test-only',$2,'ready')`, messageID, mediaDigest); err != nil {
				t.Fatal(err)
			}
		}
		return messageID
	}

	groupID := insertMessage(customerID, staffID, "group", "image", now.Add(-2*time.Minute), true)
	if _, err := native.Exec(ctx, `INSERT INTO message_archive_legacy_projections(message_id,historical_group_name,source_projection_digest) VALUES($1,'Archive group',decode(repeat('00',32),'hex'))`, groupID); err != nil {
		t.Fatal(err)
	}
	privateID := insertMessage(customerID, staffID, "private", "text", now.Add(-time.Minute), false)
	privateOlderID := insertMessage(customerID, staffID, "private", "text", now.Add(-3*time.Minute), false)
	_ = insertMessage(customerID, otherStaffID, "private", "text", now.Add(-30*time.Second), false)
	_ = insertMessage(otherCustomerID, staffID, "private", "text", now.Add(-30*time.Second), false)

	wrapped, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	read := func(query archiveport.V1ChatRecordQuery) (archiveport.V1ChatRecordPage, error) {
		var page archiveport.V1ChatRecordPage
		err := uow.Within(ctx, func(tx context.Context) error {
			var readErr error
			page, readErr = PostgreSQL{}.V1ChatRecords(tx, query)
			return readErr
		})
		return page, err
	}
	first, err := read(archiveport.V1ChatRecordQuery{CustomerIDs: []customerdomain.CustomerID{customerdomain.CustomerID(customerID)}, ChatType: "private", StaffUserID: staffID, EndAt: now, Limit: 1})
	if err != nil || len(first.Items) != 1 || first.Items[0].SourceRecordID != formatArchiveID(privateID) || first.Items[0].ChatType != "private" || first.Items[0].MediaAvailability != "not_applicable" || len(first.Items[0].StaffIDs) != 1 || first.Items[0].StaffIDs[0] != staffID || !first.HasMore {
		t.Fatalf("first=%+v err=%v", first, err)
	}

	// A newer record committed after the first page must not enter the frozen
	// [start,end) snapshot when the signed cursor requests the next page.
	_ = insertMessage(customerID, staffID, "private", "text", now.Add(time.Minute), false)
	second, err := read(archiveport.V1ChatRecordQuery{CustomerIDs: []customerdomain.CustomerID{customerdomain.CustomerID(customerID)}, ChatType: "private", StaffUserID: staffID, EndAt: now, BeforeOccurredAt: first.Items[0].OccurredAt, BeforeMessageID: privateID, Limit: 1})
	if err != nil || len(second.Items) != 1 || second.Items[0].SourceRecordID != formatArchiveID(privateOlderID) || second.Items[0].ChatType != "private" || second.HasMore {
		t.Fatalf("second=%+v err=%v", second, err)
	}

	group, err := read(archiveport.V1ChatRecordQuery{CustomerIDs: []customerdomain.CustomerID{customerdomain.CustomerID(customerID)}, ChatType: "group", EndAt: now, Limit: 20})
	if err != nil || len(group.Items) != 1 || group.Items[0].SourceRecordID != formatArchiveID(groupID) || group.Items[0].GroupName != "Archive group" || group.Items[0].MediaArchiveStatus != "available" || group.Items[0].MediaAvailability != "api_unavailable" {
		t.Fatalf("group=%+v err=%v", group, err)
	}
	exact, err := read(archiveport.V1ChatRecordQuery{CustomerIDs: []customerdomain.CustomerID{customerdomain.CustomerID(customerID)}, ChatType: "group", EndAt: now, SourceSystem: "message_archive", SourceRecordID: formatArchiveID(groupID), Limit: 20})
	if err != nil || len(exact.Items) != 1 || exact.Items[0].SourceRecordID != formatArchiveID(groupID) {
		t.Fatalf("exact=%+v err=%v", exact, err)
	}
	byMessageID, err := read(archiveport.V1ChatRecordQuery{CustomerIDs: []customerdomain.CustomerID{customerdomain.CustomerID(customerID)}, ChatType: "group", EndAt: now, MessageID: exact.Items[0].MessageID, Limit: 20})
	if err != nil || len(byMessageID.Items) != 1 || byMessageID.Items[0].SourceRecordID != formatArchiveID(groupID) {
		t.Fatalf("message id=%+v err=%v", byMessageID, err)
	}
}

func formatArchiveID(id int64) string {
	return strconv.FormatInt(id, 10)
}

func archiveIntegrationPool(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	databaseURL, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("DATABASE_URL is not configured; skipping MessageArchive PostgreSQL integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var random [8]byte
	if _, err = rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	schemaName := "aicrm_archive_test_" + hex.EncodeToString(random[:])
	admin, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	identifier := pgx.Identifier{schemaName}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		admin.Close(ctx)
		t.Fatal(err)
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		admin.Close(ctx)
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schemaName
	native, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		admin.Close(ctx)
		t.Fatal(err)
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate MessageArchive integration test")
	}
	for _, migrationName := range []string{"0002_identity.sql", "0003_access.sql", "0071_message_archive_core.sql", "0098_message_archive_historical_projection.sql"} {
		migration, readErr := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations", migrationName))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, execErr := native.Exec(ctx, string(migration)); execErr != nil {
			t.Fatalf("apply %s: %v", migrationName, execErr)
		}
	}
	return native, func() {
		native.Close()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_, _ = admin.Exec(cleanupCtx, "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close(cleanupCtx)
	}
}
