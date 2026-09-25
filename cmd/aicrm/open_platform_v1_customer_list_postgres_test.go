package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accessstore "github.com/qianlan33333-png/AI-CRM-v3/internal/access/store"
	customerstore "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/store"
	archivestore "github.com/qianlan33333-png/AI-CRM-v3/internal/messagearchive/store"
	openplatformstore "github.com/qianlan33333-png/AI-CRM-v3/internal/openplatform/store"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/wecom"
)

func TestV4MachineContactWindowPostgreSQL(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
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
	_, source, _, _ := runtime.Caller(0)
	migration, err := os.ReadFile(filepath.Join(filepath.Dir(source), "..", "..", "migrations", "0208_openplatform_customer_windows.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	var runID int64
	if err = native.QueryRow(ctx, `INSERT INTO wecom_customer_sync_runs(run_key,trigger_type,status,corp_scope,completed_at) VALUES('v4-contact-run','manual','succeeded','wecom-corp:synthetic',$1) RETURNING id`, now).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	ids := make([]int64, 0, 2)
	for i := 0; i < 2; i++ {
		var id, identityID int64
		if err = native.QueryRow(ctx, `INSERT INTO customers DEFAULT VALUES RETURNING id`).Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
		if err = native.QueryRow(ctx, `INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,verified_at)
   VALUES($1,'wecom_external_userid','wecom-corp:synthetic',$2,'verified','synthetic',1,$3) RETURNING id`, id, fmt.Sprintf("synthetic-%d", i), now).Scan(&identityID); err != nil {
			t.Fatal(err)
		}
		activation := "active"
		var removed *time.Time
		if i == 1 {
			activation = "stale"
			at := now.Add(-time.Minute)
			removed = &at
		}
		if _, err = native.Exec(ctx, `INSERT INTO wecom_external_contact_profiles(customer_id,corp_scope,external_identity_id,activation_status,profile_digest,last_seen_run_id,fetched_at,stale_at,updated_at)
   VALUES($1,'wecom-corp:synthetic',$2,$3,decode(repeat('aa',32),'hex'),$4,$5,$6,$7)`, id, identityID, activation, runID, now, removed, now.Add(-time.Minute)); err != nil {
			t.Fatal(err)
		}
		if _, err = native.Exec(ctx, `INSERT INTO customer_directory_projection(customer_id,customer_status,updated_at) VALUES($1,'active',$2)`, id, now.Add(-time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = native.Exec(ctx, `INSERT INTO wecom_customer_sync_items(run_id,corp_scope,external_userid,external_userid_digest,staff_id_digest,payload_digest,outcome)
  VALUES($1,'wecom-corp:synthetic','synthetic-unresolved',decode(repeat('ba',32),'hex'),decode(repeat('bb',32),'hex'),decode(repeat('bc',32),'hex'),'conflict')`, runID); err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, `INSERT INTO message_archive_sync_state(corp_scope) VALUES('wecom-corp:synthetic')`); err != nil {
		t.Fatal(err)
	}
	var messageID int64
	touchAt := now.Add(-30 * time.Second)
	if err = native.QueryRow(ctx, `INSERT INTO message_archive_messages(corp_scope,seq,msgid,msgtype,conversation_type,msgtime_ms,occurred_at,content_text)
	VALUES('wecom-corp:synthetic',1,'synthetic-message-1','text','private',$1,$2,'private secret never returned') RETURNING id`, touchAt.UnixMilli(), touchAt).Scan(&messageID); err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, `INSERT INTO message_archive_participants(message_id,participant_role,actor_type,provider_value_digest,resolution_status)
	VALUES($1,'sender','staff',decode(repeat('31',32),'hex'),'not_applicable')`, messageID); err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, `INSERT INTO message_archive_participants(message_id,participant_role,actor_type,provider_value_digest,customer_id_at_ingest,resolution_status)
	VALUES($1,'recipient','external_customer',decode(repeat('32',32),'hex'),$2,'found')`, messageID, ids[0]); err != nil {
		t.Fatal(err)
	}
	pool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	readUOW, err := platformpostgres.NewReadOnlyRepeatableReadUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	writeUOW, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	executor := &openPlatformExecutor{v1ExternalCursorKey: []byte("v4-synthetic-window-cursor-key-32")}
	if err = executor.BindV1CustomerList(wecom.PostgreSQLCustomerSyncStore{}, customerstore.PostgreSQL{}, accessstore.NewPostgreSQL(), openplatformstore.CustomerWindows{}, readUOW, writeUOW); err != nil {
		t.Fatal(err)
	}
	if err = executor.BindV1ContactTouches(archivestore.NewPostgreSQL()); err != nil {
		t.Fatal(err)
	}
	principal := accessdomain.MachinePrincipal{ClientID: "v4-synthetic-client", CorpID: "synthetic", OwnerScope: accessdomain.OwnerScope{"corp_id": {"synthetic"}}}
	if _, err = executor.v1ListCustomers(ctx, principal, json.RawMessage(`{"limit":0}`)); err == nil {
		t.Fatal("zero limit was accepted")
	}
	first, err := executor.v1ListCustomers(ctx, principal, json.RawMessage(`{"limit":1}`))
	if err != nil {
		t.Fatal(err)
	}
	page := first.Data.(map[string]any)
	if page["item_count"] != 3 || len(page["items"].([]json.RawMessage)) != 1 {
		t.Fatalf("wrong frozen population: %d", page["item_count"])
	}
	var observed map[string]any
	if err = json.Unmarshal(page["items"].([]json.RawMessage)[0], &observed); err != nil {
		t.Fatal(err)
	}
	if observed["last_real_touch_at"] == nil || observed["safe_user_ref"] != fmt.Sprintf("CID-%d", ids[0]) || strings.Contains(string(page["items"].([]json.RawMessage)[0]), "private secret") {
		t.Fatalf("unsafe archived touch projection: %+v", observed)
	}
	if _, err = native.Exec(ctx, `UPDATE wecom_external_contact_profiles SET updated_at=clock_timestamp() WHERE customer_id=$1`, ids[1]); err != nil {
		t.Fatal(err)
	}
	cursor := page["next_cursor"].(string)
	if _, err = executor.v1ListCustomers(ctx, principal, json.RawMessage(fmt.Sprintf(`{"limit":1,"cursor":%q}`, cursor+"tampered"))); err == nil {
		t.Fatal("tampered cursor accepted")
	}
	scoped := principal
	scoped.OwnerScope = accessdomain.OwnerScope{"corp_id": {"synthetic"}, "customer_id": {"999999"}}
	if _, err = executor.v1ListCustomers(ctx, scoped, json.RawMessage(fmt.Sprintf(`{"limit":1,"cursor":%q}`, cursor))); err == nil {
		t.Fatal("grant drift accepted")
	}
	second, err := executor.v1ListCustomers(ctx, principal, json.RawMessage(fmt.Sprintf(`{"limit":1,"cursor":%q}`, cursor)))
	if err != nil {
		t.Fatal(err)
	}
	secondPage := second.Data.(map[string]any)
	var tombstone map[string]any
	if err = json.Unmarshal(secondPage["items"].([]json.RawMessage)[0], &tombstone); err != nil {
		t.Fatal(err)
	}
	if tombstone["deleted_at"] == nil || tombstone["binding_status"] != "unbound" {
		t.Fatalf("missing relationship tombstone: %+v", tombstone)
	}
	third, err := executor.v1ListCustomers(ctx, principal, json.RawMessage(fmt.Sprintf(`{"limit":1,"cursor":%q}`, secondPage["next_cursor"])))
	if err != nil {
		t.Fatal(err)
	}
	var unresolved map[string]any
	if err = json.Unmarshal(third.Data.(map[string]any)["items"].([]json.RawMessage)[0], &unresolved); err != nil {
		t.Fatal(err)
	}
	if unresolved["identity_status"] != "unresolved" || unresolved["safe_user_ref"] != nil || !strings.HasPrefix(unresolved["record_id"].(string), "v4:unresolved:") {
		t.Fatalf("unsafe unresolved record: %+v", unresolved)
	}
	for _, raw := range []json.RawMessage{secondPage["items"].([]json.RawMessage)[0], third.Data.(map[string]any)["items"].([]json.RawMessage)[0]} {
		for _, secret := range []string{"unionid", "external_userid", "phone", "content"} {
			if strings.Contains(string(raw), secret) {
				t.Fatalf("forbidden %s exposed", secret)
			}
		}
	}
	if _, err = native.Exec(ctx, `INSERT INTO wecom_customer_sync_runs(run_key,trigger_type,status,corp_scope) VALUES('v4-incomplete-run','manual','queued','wecom-corp:synthetic')`); err != nil {
		t.Fatal(err)
	}
	if _, err = executor.v1ListCustomers(ctx, principal, json.RawMessage(`{"limit":100}`)); err == nil {
		t.Fatal("incomplete sync became an empty page")
	}
}
