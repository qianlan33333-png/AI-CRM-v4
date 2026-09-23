package app_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	customer "github.com/qianlan33333-png/AI-CRM-v3/internal/customer"
	customerapp "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/app"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformoutbox "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/outbox"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

type ownerHandoffPGResolver struct {
	candidate customerport.OwnerHandoffCandidate
}

type ownerHandoffPGStaff map[int64]accessdomain.User

func (staff ownerHandoffPGStaff) UserByID(_ context.Context, id int64, _ bool) (accessdomain.User, error) {
	user, found := staff[id]
	if !found {
		return accessdomain.User{}, accessdomain.ErrNotFound
	}
	return user, nil
}

func (r ownerHandoffPGResolver) ResolveOwnerHandoffCandidates(_ context.Context, _ customerport.OwnerHandoffMode, _, _ int64, _ string, ids []customerdomain.CustomerID) ([]customerport.OwnerHandoffCandidate, error) {
	if len(ids) != 1 || ids[0] != r.candidate.CustomerID {
		return nil, customer.ErrOwnerHandoffConflict
	}
	return []customerport.OwnerHandoffCandidate{r.candidate}, nil
}

func TestPostgreSQLOwnerHandoffLocalOnlyPreviewConfirmIsAtomic(t *testing.T) {
	databaseURL, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured; skipping owner-handoff PostgreSQL journey")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, cleanup := ownerHandoffAppPool(t, ctx, databaseURL)
	defer cleanup()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	var source, target int64
	var customerID customerdomain.CustomerID
	if err = uow.Within(ctx, func(txctx context.Context) error {
		tx, e := platformpostgres.RequireTransaction(txctx)
		if e != nil {
			return e
		}
		if e = tx.QueryRow(ctx, `INSERT INTO customers(status) VALUES('active') RETURNING id`).Scan(&customerID); e != nil {
			return e
		}
		if e = tx.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name,wecom_userid,is_active) VALUES('source-a','$argon2id$fixture','Source','source-a',false) RETURNING id`).Scan(&source); e != nil {
			return e
		}
		return tx.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name,wecom_userid,is_active) VALUES('target-b','$argon2id$fixture','Target','target-b',true) RETURNING id`).Scan(&target)
	}); err != nil {
		t.Fatal(err)
	}
	candidate := customerport.OwnerHandoffCandidate{CustomerID: customerID, RelationshipDigest: [32]byte{1}, State: "ready"}
	service, err := customerapp.NewOwnerHandoffService(uow, customer.NewPostgreSQLOwnerHandoffStore(), ownerHandoffPGStaff{source: {ID: source, WeComUserID: "source-a", Active: false}, target: {ID: target, WeComUserID: "target-b", Active: true}}, ownerHandoffPGResolver{candidate: candidate}, mustOwnerHandoffAudit(t), platformoutbox.NewPostgreSQL())
	if err != nil {
		t.Fatal(err)
	}
	if err = service.SetBatchEnqueuer(ownerHandoffPGEnqueuer{}); err != nil {
		t.Fatal(err)
	}
	preview, err := service.PreviewOwnerHandoff(ctx, customerport.OwnerHandoffPreviewCommand{ActorAdminUserID: source, Mode: customerport.OwnerHandoffLocalOnly, SourceStaffID: source, TargetStaffID: target, CorpScope: "wecom-corp:fixture", CustomerIDs: []customerdomain.CustomerID{customerID}, ConfirmationPhrase: "CONFIRM", IdempotencyKey: "preview-owner-handoff"})
	if err != nil {
		t.Fatal(err)
	}
	batch, err := service.ConfirmOwnerHandoff(ctx, customerport.OwnerHandoffConfirmCommand{ActorAdminUserID: source, PreviewID: preview.ID, PreviewHash: preview.Hash, ConfirmationPhrase: "CONFIRM", IdempotencyKey: "confirm-owner-handoff"})
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Lines) != 1 || batch.Lines[0].State != "queued" {
		t.Fatalf("accepted batch=%+v", batch)
	}
	if err = service.ProcessOwnerHandoffBatch(ctx, batch.ID, 0); err != nil {
		t.Fatal(err)
	}
	if err = uow.Within(ctx, func(txctx context.Context) error {
		owner, found, e := customer.NewPostgreSQLOwnerHandoffStore().LocalOwner(txctx, customerID, false)
		if e != nil {
			return e
		}
		if !found || owner.StaffID != target || owner.Source != "owner_handoff_local_only" {
			t.Fatalf("owner=%+v found=%t", owner, found)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = service.ConfirmOwnerHandoff(ctx, customerport.OwnerHandoffConfirmCommand{ActorAdminUserID: source, PreviewID: preview.ID, PreviewHash: preview.Hash, ConfirmationPhrase: "CONFIRM", IdempotencyKey: "confirm-owner-other-key"}); !errors.Is(err, customerapp.ErrOwnerHandoffDrift) {
		t.Fatalf("second key for one preview=%v", err)
	}
	if err = uow.Within(ctx, func(txctx context.Context) error {
		tx, e := platformpostgres.RequireTransaction(txctx)
		if e != nil {
			return e
		}
		var batches int
		if e = tx.QueryRow(txctx, `SELECT count(*) FROM customer_owner_handoff_batches WHERE preview_id=$1`, preview.ID).Scan(&batches); e != nil {
			return e
		}
		if batches != 1 {
			t.Fatalf("one preview created %d batches", batches)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func ownerHandoffAppPool(t *testing.T, ctx context.Context, databaseURL string) (*platformpostgres.Pool, func()) {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.NewWithConfig(ctx, cfg.Copy())
	if err != nil {
		t.Fatal(err)
	}
	raw := make([]byte, 8)
	if _, err = rand.Read(raw); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	schema := "aicrm_owner_handoff_app_" + hex.EncodeToString(raw)
	ident := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+ident); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	testCfg := cfg.Copy()
	testCfg.ConnConfig.RuntimeParams["search_path"] = schema
	native, err := pgxpool.NewWithConfig(ctx, testCfg)
	if err != nil {
		admin.Close()
		t.Fatal(err)
	}
	root := filepath.Clean(filepath.Join("..", "..", ".."))
	for _, name := range []string{"0001_platform.sql", "0002_identity.sql", "0003_access.sql", "0004_wecom.sql", "0005_external_effects.sql", "0006_wecom_callback_channel_acquisition.sql", "0007_media.sql", "0008_tag_catalog.sql", "0009_customer_activation.sql", "0092_customer_owner_handoff.sql"} {
		body, e := os.ReadFile(filepath.Join(root, "migrations", name))
		if e != nil {
			t.Fatal(e)
		}
		if _, e = native.Exec(ctx, string(body)); e != nil {
			t.Fatalf("apply %s: %v", name, e)
		}
	}
	pool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return pool, func() {
		pool.Close()
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = admin.Exec(cleanup, "DROP SCHEMA "+ident+" CASCADE")
		admin.Close()
	}
}

func mustOwnerHandoffAudit(t *testing.T) *platformaudit.Service {
	t.Helper()
	value, err := platformaudit.NewService(platformaudit.NewPostgreSQLStore())
	if err != nil {
		t.Fatal(err)
	}
	return value
}

type ownerHandoffPGEnqueuer struct{}

func (ownerHandoffPGEnqueuer) EnqueueOwnerHandoffBatchWithin(context.Context, string, int64) error {
	return nil
}

type failingOwnerHandoffAudit struct{}

func (failingOwnerHandoffAudit) Append(context.Context, platformaudit.Event) (platformaudit.Event, error) {
	return platformaudit.Event{}, errors.New("audit unavailable")
}

func TestPostgreSQLOwnerHandoffLocalOnlyRollsBackWhenAuditFails(t *testing.T) {
	databaseURL, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured; skipping owner-handoff PostgreSQL journey")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, cleanup := ownerHandoffAppPool(t, ctx, databaseURL)
	defer cleanup()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	var source, target int64
	var customerID customerdomain.CustomerID
	if err = uow.Within(ctx, func(txctx context.Context) error {
		tx, e := platformpostgres.RequireTransaction(txctx)
		if e != nil {
			return e
		}
		if e = tx.QueryRow(ctx, `INSERT INTO customers(status) VALUES('active') RETURNING id`).Scan(&customerID); e != nil {
			return e
		}
		if e = tx.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name,wecom_userid) VALUES('source-x','$argon2id$fixture','Source','source-x') RETURNING id`).Scan(&source); e != nil {
			return e
		}
		return tx.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name,wecom_userid) VALUES('target-y','$argon2id$fixture','Target','target-y') RETURNING id`).Scan(&target)
	}); err != nil {
		t.Fatal(err)
	}
	service, err := customerapp.NewOwnerHandoffService(uow, customer.NewPostgreSQLOwnerHandoffStore(), ownerHandoffPGStaff{source: {ID: source, WeComUserID: "source-x", Active: true}, target: {ID: target, WeComUserID: "target-y", Active: true}}, ownerHandoffPGResolver{candidate: customerport.OwnerHandoffCandidate{CustomerID: customerID, RelationshipDigest: [32]byte{9}, State: "ready"}}, failingOwnerHandoffAudit{}, platformoutbox.NewPostgreSQL())
	if err != nil {
		t.Fatal(err)
	}
	if err = service.SetBatchEnqueuer(ownerHandoffPGEnqueuer{}); err != nil {
		t.Fatal(err)
	}
	preview, err := service.PreviewOwnerHandoff(ctx, customerport.OwnerHandoffPreviewCommand{ActorAdminUserID: source, Mode: customerport.OwnerHandoffLocalOnly, SourceStaffID: source, TargetStaffID: target, CorpScope: "wecom-corp:fixture", CustomerIDs: []customerdomain.CustomerID{customerID}, ConfirmationPhrase: "CONFIRM", IdempotencyKey: "preview-owner-failure"})
	if err != nil {
		t.Fatal(err)
	}
	batch, err := service.ConfirmOwnerHandoff(ctx, customerport.OwnerHandoffConfirmCommand{ActorAdminUserID: source, PreviewID: preview.ID, PreviewHash: preview.Hash, ConfirmationPhrase: "CONFIRM", IdempotencyKey: "confirm-owner-failure"})
	if err != nil {
		t.Fatalf("accept batch: %v", err)
	}
	if err = service.ProcessOwnerHandoffBatch(ctx, batch.ID, 0); err == nil {
		t.Fatal("expected worker audit failure")
	}
	if err = uow.Within(ctx, func(txctx context.Context) error {
		tx, e := platformpostgres.RequireTransaction(txctx)
		if e != nil {
			return e
		}
		var owners, batches, outbox int
		if e = tx.QueryRow(ctx, `SELECT count(*) FROM customer_local_owners WHERE customer_id=$1`, customerID).Scan(&owners); e != nil {
			return e
		}
		if e = tx.QueryRow(ctx, `SELECT count(*) FROM customer_owner_handoff_batches`).Scan(&batches); e != nil {
			return e
		}
		if e = tx.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE aggregate_id=$1`, fmt.Sprintf("%d", customerID)).Scan(&outbox); e != nil {
			return e
		}
		if owners != 0 || batches != 1 || outbox != 0 {
			t.Fatalf("worker rollback leaked owners=%d batches=%d outbox=%d", owners, batches, outbox)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// TestPostgreSQLOwnerHandoffRejectsNewPreviewAfterUnknownTransfer proves that
// an independently generated preview cannot turn an outcome_unknown provider
// write into a second transfer_customer attempt while the local owner remains
// unchanged. The rejection happens before any EER acceptance.
func TestPostgreSQLOwnerHandoffRejectsNewPreviewAfterUnknownTransfer(t *testing.T) {
	databaseURL, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured; skipping owner-handoff PostgreSQL journey")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, cleanup := ownerHandoffAppPool(t, ctx, databaseURL)
	defer cleanup()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	var source, target int64
	var customerID customerdomain.CustomerID
	if err = uow.Within(ctx, func(txctx context.Context) error {
		tx, e := platformpostgres.RequireTransaction(txctx)
		if e != nil {
			return e
		}
		if e = tx.QueryRow(txctx, `INSERT INTO customers(status) VALUES('active') RETURNING id`).Scan(&customerID); e != nil {
			return e
		}
		if e = tx.QueryRow(txctx, `INSERT INTO admin_users(username,password_hash,display_name,wecom_userid,is_active) VALUES('unknown-source','$argon2id$fixture','Source','unknown-source',true) RETURNING id`).Scan(&source); e != nil {
			return e
		}
		if e = tx.QueryRow(txctx, `INSERT INTO admin_users(username,password_hash,display_name,wecom_userid,is_active) VALUES('unknown-target','$argon2id$fixture','Target','unknown-target',true) RETURNING id`).Scan(&target); e != nil {
			return e
		}
		const priorPreview, priorBatch = "unknown-preview", "unknown-batch"
		digest := make([]byte, 32)
		if _, e = tx.Exec(txctx, `INSERT INTO customer_owner_handoff_previews(id,actor_admin_user_id,mode,source_staff_id,target_staff_id,corp_scope,request_digest,confirmation_phrase,expires_at) VALUES($1,$2,'wecom_then_crm',$3,$4,'wecom-corp:fixture',$5,'CONFIRM',clock_timestamp()+interval '1 hour')`, priorPreview, source, source, target, digest); e != nil {
			return e
		}
		if _, e = tx.Exec(txctx, `INSERT INTO customer_owner_handoff_batches(id,preview_id,actor_admin_user_id,idempotency_key,request_digest,mode,source_staff_id,target_staff_id,corp_scope,state) VALUES($1,$2,$3,'prior-unknown-key',$4,'wecom_then_crm',$5,$6,'wecom-corp:fixture','needs_attention')`, priorBatch, priorPreview, source, digest, source, target); e != nil {
			return e
		}
		_, e = tx.Exec(txctx, `INSERT INTO customer_owner_handoff_lines(batch_id,line_no,customer_id,mode,source_staff_id,target_staff_id,relation_digest,effect_id,state) VALUES($1,1,$2,'wecom_then_crm',$3,$4,$5,'eer_prior_unknown','outcome_unknown')`, priorBatch, customerID, source, target, digest)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	cipher, err := customer.NewOwnerHandoffCipher("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	if err != nil {
		t.Fatal(err)
	}
	store := customer.NewPostgreSQLOwnerHandoffStoreWithCipher(cipher)
	candidate := customerport.OwnerHandoffCandidate{CustomerID: customerID, RelationshipDigest: [32]byte{7}, State: "ready", SourceUserID: "unknown-source", TargetUserID: "unknown-target", ExternalUserID: "external-unknown"}
	service, err := customerapp.NewOwnerHandoffService(uow, store, ownerHandoffPGStaff{source: {ID: source, WeComUserID: "unknown-source", Active: true}, target: {ID: target, WeComUserID: "unknown-target", Active: true}}, ownerHandoffPGResolver{candidate: candidate}, mustOwnerHandoffAudit(t), platformoutbox.NewPostgreSQL())
	if err != nil {
		t.Fatal(err)
	}
	if err = service.SetBatchEnqueuer(ownerHandoffPGEnqueuer{}); err != nil {
		t.Fatal(err)
	}
	service.SetWeComProviderEnabled(true)
	preview, err := service.PreviewOwnerHandoff(ctx, customerport.OwnerHandoffPreviewCommand{ActorAdminUserID: source, Mode: customerport.OwnerHandoffWeComThenCRM, SourceStaffID: source, TargetStaffID: target, CorpScope: "wecom-corp:fixture", CustomerIDs: []customerdomain.CustomerID{customerID}, ConfirmationPhrase: "CONFIRM", IdempotencyKey: "new-preview-after-unknown"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.ConfirmOwnerHandoff(ctx, customerport.OwnerHandoffConfirmCommand{ActorAdminUserID: source, PreviewID: preview.ID, PreviewHash: preview.Hash, ConfirmationPhrase: "CONFIRM", IdempotencyKey: "new-confirm-after-unknown"}); !errors.Is(err, customer.ErrOwnerHandoffConflict) {
		t.Fatalf("expected unknown transfer conflict, got %v", err)
	}
	if err = uow.Within(ctx, func(txctx context.Context) error {
		tx, e := platformpostgres.RequireTransaction(txctx)
		if e != nil {
			return e
		}
		var effects, batches int
		if e = tx.QueryRow(txctx, `SELECT count(*) FROM external_effects WHERE kind='customer_owner_handoff'`).Scan(&effects); e != nil {
			return e
		}
		if e = tx.QueryRow(txctx, `SELECT count(*) FROM customer_owner_handoff_batches`).Scan(&batches); e != nil {
			return e
		}
		if effects != 0 || batches != 1 {
			t.Fatalf("unknown guard accepted a new transfer: effects=%d batches=%d", effects, batches)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
