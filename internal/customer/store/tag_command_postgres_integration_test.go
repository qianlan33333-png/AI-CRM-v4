package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestTagCommandMigration0093RetainsHigherVersionCommerceEffectFacts(t *testing.T) {
	url, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	pool, clean := tagCommandPGPool(t, ctx, url)
	defer clean()
	native := pool.Native()
	insertEffect := func(owner, kind, fingerprint string) error {
		_, insertErr := native.Exec(ctx, `INSERT INTO external_effects(owner,kind,source_ref_digest,target_ref_digest,payload_digest,policy_version_hash,envelope_fingerprint,state)
			VALUES($1,$2,'sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb','sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc','sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd',$3,'accepted')`, owner, kind, fingerprint)
		return insertErr
	}
	// Simulate an already-applied higher migration's commerce effect fact.
	if err = insertEffect("outbound", "commerce_product_push", "sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"); err != nil {
		t.Fatal(err)
	}
	root := filepath.Clean(filepath.Join("..", "..", ".."))
	sql, err := os.ReadFile(filepath.Join(root, "migrations", "0093_customer_tag_commands.sql"))
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(sql), "ALTER TABLE external_effects DROP CONSTRAINT")
	end := strings.Index(string(sql), "ALTER TABLE IF EXISTS channel_entrant_actions")
	if start < 0 || end <= start {
		t.Fatal("locate 0093 external_effects compatibility constraints")
	}
	// Apply the exact lower-numbered migration fragment after the fact exists.
	if _, err = native.Exec(ctx, string(sql)[start:end]); err != nil {
		t.Fatalf("0093 narrowed a higher-version commerce effect fact: %v", err)
	}
	var count int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM external_effects WHERE kind='commerce_product_push'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("commerce effect facts=%d err=%v", count, err)
	}
	if err = insertEffect("outbound", "not_a_registered_kind", "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"); err == nil {
		t.Fatal("unregistered effect kind was accepted")
	}
	if err = insertEffect("payment", "commerce_product_push", "sha256:1111111111111111111111111111111111111111111111111111111111111111"); err == nil {
		t.Fatal("invalid owner/kind pairing was accepted")
	}
}

func TestTagCommandPostgreSQLCompletionFenceAndReplay(t *testing.T) {
	url, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	pool, clean := tagCommandPGPool(t, ctx, url)
	defer clean()
	native := pool.Native()
	if _, err = native.Exec(ctx, `INSERT INTO admin_users(username,password_hash,display_name,is_active,session_version) VALUES('tag-admin','$argon2id$test','Tag admin',true,1); INSERT INTO customers(id,status) OVERRIDING SYSTEM VALUE VALUES(1,'active'); INSERT INTO tag_groups(group_name,sort_order) VALUES('g',1); INSERT INTO tag_catalog_tags(group_id,tag_name,sort_order) VALUES(1,'t',1)`); err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	store := TagCommandPostgreSQL{}
	var commandID int64
	err = uow.Within(ctx, func(tx context.Context) error {
		var e error
		commandID, e = store.CreateTagCommand(tx, customerport.TagCommand{ActorAdminUserID: 1, Source: "admin_customer_ui", SourceRef: "tag-command-replay", IdempotencyKey: "tag-command-replay", OccurredAt: time.Now()}, [32]byte{1})
		if e != nil {
			return e
		}
		_, e = store.CreateTagCommandLine(tx, commandID, customerport.FrozenTagCommandTarget{TagCommandTarget: customerport.TagCommandTarget{CustomerID: customerdomain.CustomerID(1), StaffID: 1, AddTagIDs: []int64{1}}, BindingDigest: string(effectport.Hash("binding")), TargetDigest: string(effectport.Hash("target"))}, string(effectport.Hash("source")), effectport.Projection{ID: "eer_1", State: effectport.StateQueued}, effectport.Receipt{ID: "eerop_1", QueueReceiptID: "eerop_2"})
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	complete := customerport.TagCommandCompletion{EffectRef: "eer_1", State: "executed", ResultDigest: string(effectport.Hash("done")), Attempt: 1, Generation: 1, Fence: 1, CompletedAt: time.Now()}
	if err = uow.Within(ctx, func(tx context.Context) error { return store.CompleteTagCommand(tx, complete) }); err != nil {
		t.Fatal(err)
	}
	if err = uow.Within(ctx, func(tx context.Context) error { return store.CompleteTagCommand(tx, complete) }); err != nil {
		t.Fatalf("same completion replay: %v", err)
	}
	stale := complete
	stale.Fence = 0
	if err = uow.Within(ctx, func(tx context.Context) error { return store.CompleteTagCommand(tx, stale) }); err == nil {
		t.Fatal("stale completion must reject")
	}
	var state string
	if err = native.QueryRow(ctx, `SELECT state FROM customer_tag_commands WHERE id=$1`, commandID).Scan(&state); err != nil || state != "executed" {
		t.Fatalf("state=%q err=%v", state, err)
	}
}
func tagCommandPGPool(t *testing.T, ctx context.Context, url string) (*platformpostgres.Pool, func()) {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.NewWithConfig(ctx, cfg.Copy())
	if err != nil {
		t.Fatal(err)
	}
	raw := make([]byte, 8)
	_, _ = rand.Read(raw)
	schema := "aicrm_tag_command_" + hex.EncodeToString(raw)
	id := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+id); err != nil {
		t.Fatal(err)
	}
	testCfg := cfg.Copy()
	testCfg.ConnConfig.RuntimeParams["search_path"] = schema
	native, err := pgxpool.NewWithConfig(ctx, testCfg)
	if err != nil {
		t.Fatal(err)
	}
	migrator, err := rivermigrate.New(riverpgxv5.New(native), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		t.Fatal(err)
	}
	root := filepath.Clean(filepath.Join("..", "..", ".."))
	for _, name := range []string{"0001_platform.sql", "0002_identity.sql", "0003_access.sql", "0004_wecom.sql", "0005_external_effects.sql", "0008_tag_catalog.sql", "0009_customer_activation.sql", "0093_customer_tag_commands.sql"} {
		raw, readErr := os.ReadFile(filepath.Join(root, "migrations", name))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, execErr := native.Exec(ctx, string(raw)); execErr != nil {
			t.Fatalf("apply %s: %v", name, execErr)
		}
	}
	pool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return pool, func() {
		pool.Close()
		native.Close()
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+id+" CASCADE")
		admin.Close()
	}
}

func TestTagCommandPostgreSQLCompletionProjectsPartialAndRejected(t *testing.T) {
	url, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	pool, clean := tagCommandPGPool(t, ctx, url)
	defer clean()
	native := pool.Native()
	if _, err = native.Exec(ctx, `INSERT INTO admin_users(username,password_hash,display_name,is_active,session_version) VALUES('tag-admin-2','$argon2id$test','Tag admin',true,1); INSERT INTO customers(id,status) OVERRIDING SYSTEM VALUE VALUES(1,'active'),(2,'active'); INSERT INTO tag_groups(group_name,sort_order) VALUES('g',1); INSERT INTO tag_catalog_tags(group_id,tag_name,sort_order) VALUES(1,'t',1)`); err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	store := TagCommandPostgreSQL{}
	var commandID int64
	err = uow.Within(ctx, func(tx context.Context) error {
		var e error
		commandID, e = store.CreateTagCommand(tx, customerport.TagCommand{ActorAdminUserID: 1, Source: "admin_customer_ui", SourceRef: "partial-command", IdempotencyKey: "partial-command", OccurredAt: time.Now()}, [32]byte{2})
		if e != nil {
			return e
		}
		_, e = store.CreateTagCommandLine(tx, commandID, customerport.FrozenTagCommandTarget{TagCommandTarget: customerport.TagCommandTarget{CustomerID: 1, StaffID: 1, AddTagIDs: []int64{1}}, BindingDigest: string(effectport.Hash("binding-2")), TargetDigest: string(effectport.Hash("target-2"))}, string(effectport.Hash("source-2")), effectport.Projection{ID: "eer_2", State: effectport.StateQueued}, effectport.Receipt{ID: "eerop_2", QueueReceiptID: "eerop_3"})
		if e != nil {
			return e
		}
		_, e = store.CreateRejectedTagCommandLine(tx, commandID, customerport.TagCommandTarget{CustomerID: 2, StaffID: 1, AddTagIDs: []int64{1}}, "target_unavailable")
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	completion := customerport.TagCommandCompletion{EffectRef: "eer_2", State: "final_failed", ResultDigest: string(effectport.Hash("done-2")), ResultReason: "provider_rejected", Attempt: 1, Generation: 1, Fence: 1, CompletedAt: time.Now()}
	if err = uow.Within(ctx, func(tx context.Context) error { return store.CompleteTagCommand(tx, completion) }); err != nil {
		t.Fatal(err)
	}
	var state string
	if err = native.QueryRow(ctx, `SELECT state FROM customer_tag_commands WHERE id=$1`, commandID).Scan(&state); err != nil || state != "partial" {
		t.Fatalf("state=%q err=%v", state, err)
	}
	var resultReason string
	if err = native.QueryRow(ctx, `SELECT COALESCE(result_reason,'') FROM customer_tag_command_lines WHERE effect_ref='eer_2'`).Scan(&resultReason); err != nil || resultReason != "provider_rejected" {
		t.Fatalf("safe result reason=%q err=%v", resultReason, err)
	}
	var history []customerport.TagCommandResult
	if err = uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		history, readErr = store.ListTagCommands(tx, 1, 10)
		return readErr
	}); err != nil || len(history) != 1 || history[0].Lines[0].ResultReason != "provider_rejected" {
		t.Fatalf("history=%+v err=%v", history, err)
	}
	// Rejected-only commands never masquerade as executed.
	var rejectedID int64
	if err = uow.Within(ctx, func(tx context.Context) error {
		var e error
		rejectedID, e = store.CreateTagCommand(tx, customerport.TagCommand{ActorAdminUserID: 1, Source: "admin_customer_ui", SourceRef: "rejected-command", IdempotencyKey: "rejected-command", OccurredAt: time.Now()}, [32]byte{3})
		if e != nil {
			return e
		}
		if _, e = store.CreateRejectedTagCommandLine(tx, rejectedID, customerport.TagCommandTarget{CustomerID: 2, StaffID: 1, AddTagIDs: []int64{1}}, "target_unavailable"); e != nil {
			return e
		}
		return store.SetTagCommandState(tx, rejectedID, "rejected")
	}); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT state FROM customer_tag_commands WHERE id=$1`, rejectedID).Scan(&state); err != nil || state != "rejected" {
		t.Fatalf("rejected state=%q err=%v", state, err)
	}
}

func TestTagCommandPostgreSQLConcurrentLineCompletionLocksParentAndKeepsAggregate(t *testing.T) {
	url, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	pool, clean := tagCommandPGPool(t, ctx, url)
	defer clean()
	native := pool.Native()
	if _, err = native.Exec(ctx, `INSERT INTO admin_users(username,password_hash,display_name,is_active,session_version) VALUES('tag-admin-concurrent','$argon2id$test','Tag concurrent admin',true,1); INSERT INTO customers(id,status) OVERRIDING SYSTEM VALUE VALUES(1,'active'),(2,'active')`); err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	store := TagCommandPostgreSQL{}
	var commandID int64
	err = uow.Within(ctx, func(tx context.Context) error {
		var createErr error
		commandID, createErr = store.CreateTagCommand(tx, customerport.TagCommand{ActorAdminUserID: 1, Source: "admin_customer_ui", SourceRef: "concurrent-lines", IdempotencyKey: "concurrent-lines", OccurredAt: time.Now()}, [32]byte{8})
		if createErr != nil {
			return createErr
		}
		for index, customerID := range []customerdomain.CustomerID{1, 2} {
			_, createErr = store.CreateTagCommandLine(tx, commandID, customerport.FrozenTagCommandTarget{TagCommandTarget: customerport.TagCommandTarget{CustomerID: customerID, StaffID: 1, AddTagIDs: []int64{1}}, BindingDigest: string(effectport.Hash("concurrent-binding")), TargetDigest: string(effectport.Hash("concurrent-target"))}, string(effectport.Hash("concurrent-source", string(rune('a'+index)))), effectport.Projection{ID: "eer_" + strconv.Itoa(80+index), State: effectport.StateQueued}, effectport.Receipt{ID: "eerop_" + strconv.Itoa(80+index), QueueReceiptID: "eerop_" + strconv.Itoa(180+index)})
			if createErr != nil {
				return createErr
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	failures := make(chan error, 2)
	var wg sync.WaitGroup
	for index := range 2 {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			failures <- uow.Within(ctx, func(tx context.Context) error {
				return store.CompleteTagCommand(tx, customerport.TagCommandCompletion{EffectRef: "eer_" + strconv.Itoa(80+index), State: "executed", ResultDigest: string(effectport.Hash("concurrent-done", strconv.Itoa(index))), Attempt: 1, Generation: 1, Fence: 1, CompletedAt: time.Now()})
			})
		}(index)
	}
	close(start)
	wg.Wait()
	close(failures)
	for completionErr := range failures {
		if completionErr != nil {
			t.Fatalf("completion=%v", completionErr)
		}
	}
	var state string
	if err = native.QueryRow(ctx, `SELECT state FROM customer_tag_commands WHERE id=$1`, commandID).Scan(&state); err != nil || state != "executed" {
		t.Fatalf("state=%q err=%v", state, err)
	}
	if err = uow.Within(ctx, func(tx context.Context) error {
		_, inner := store.CreateRejectedTagCommandLine(tx, commandID, customerport.TagCommandTarget{CustomerID: 1, StaffID: 0, AddTagIDs: []int64{2}}, "target_unavailable")
		return inner
	}); err == nil {
		t.Fatal("same customer line must remain unique")
	}
	var nullableCommand int64
	if err = uow.Within(ctx, func(tx context.Context) error {
		var createErr error
		nullableCommand, createErr = store.CreateTagCommand(tx, customerport.TagCommand{ActorAdminUserID: 1, Source: "admin_customer_ui", SourceRef: "unresolved-staff", IdempotencyKey: "unresolved-staff", OccurredAt: time.Now()}, [32]byte{9})
		if createErr != nil {
			return createErr
		}
		_, createErr = store.CreateRejectedTagCommandLine(tx, nullableCommand, customerport.TagCommandTarget{CustomerID: 2, StaffID: 0, AddTagIDs: []int64{1}}, "target_unavailable")
		return createErr
	}); err != nil {
		t.Fatal(err)
	}
	var staffNull bool
	if err = native.QueryRow(ctx, `SELECT staff_id IS NULL FROM customer_tag_command_lines WHERE command_id=$1`, nullableCommand).Scan(&staffNull); err != nil || !staffNull {
		t.Fatalf("unresolved staff null=%t err=%v", staffNull, err)
	}
}
