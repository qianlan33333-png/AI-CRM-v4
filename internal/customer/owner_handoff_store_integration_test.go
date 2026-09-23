package customer

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
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func TestPostgreSQLOwnerHandoffDoesNotOverwriteOwnerAddedAfterPreview(t *testing.T) {
	databaseURL, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured; skipping owner-handoff PostgreSQL integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, cleanup := ownerHandoffPool(t, ctx, databaseURL)
	defer cleanup()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	var customerID customerdomain.CustomerID
	var firstStaff, secondStaff int64
	if err = uow.Within(ctx, func(tx context.Context) error {
		native, transactionErr := platformpostgres.RequireTransaction(tx)
		if transactionErr != nil {
			return transactionErr
		}
		var insertErr error
		if insertErr = native.QueryRow(ctx, `INSERT INTO customers(status) VALUES('active') RETURNING id`).Scan(&customerID); insertErr != nil {
			return insertErr
		}
		if insertErr = native.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name,wecom_userid) VALUES('owner-a','$argon2id$fixture','Owner A','owner-a') RETURNING id`).Scan(&firstStaff); insertErr != nil {
			return insertErr
		}
		return native.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name,wecom_userid) VALUES('owner-b','$argon2id$fixture','Owner B','owner-b') RETURNING id`).Scan(&secondStaff)
	}); err != nil {
		t.Fatal(err)
	}

	store := NewPostgreSQLOwnerHandoffStore()
	at := time.Date(2026, 9, 6, 2, 0, 0, 0, time.UTC)
	// A preview that saw no customer_local_owners row freezes expected version
	// zero.  Another accepted command wins first; this confirmation must not
	// turn zero into a wildcard and replace that owner.
	if err = uow.Within(ctx, func(tx context.Context) error {
		_, assignErr := store.AssignLocalOwner(tx, customerID, firstStaff, 0, "owner_handoff_local_only", at)
		return assignErr
	}); err != nil {
		t.Fatalf("first assignment: %v", err)
	}
	if err = uow.Within(ctx, func(tx context.Context) error {
		_, assignErr := store.AssignLocalOwner(tx, customerID, secondStaff, 0, "owner_handoff_local_only", at.Add(time.Second))
		return assignErr
	}); !errors.Is(err, ErrOwnerHandoffConflict) {
		t.Fatalf("expected frozen-empty conflict, got %v", err)
	}
	if err = uow.Within(ctx, func(tx context.Context) error {
		owner, found, readErr := store.LocalOwner(tx, customerID, false)
		if readErr != nil {
			return readErr
		}
		if !found || owner.StaffID != firstStaff || owner.Version != 1 || owner.Source != "owner_handoff_local_only" {
			t.Fatalf("owner was overwritten: %+v found=%t", owner, found)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if err = uow.Within(ctx, func(tx context.Context) error {
		owner, assignErr := store.AssignLocalOwner(tx, customerID, secondStaff, 1, "owner_handoff_wecom_then_crm", at.Add(2*time.Second))
		if assignErr != nil {
			return assignErr
		}
		if owner.StaffID != secondStaff || owner.Version != 2 || owner.Source != "owner_handoff_wecom_then_crm" {
			t.Fatalf("cas update=%+v", owner)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func ownerHandoffPool(t *testing.T, ctx context.Context, databaseURL string) (*platformpostgres.Pool, func()) {
	t.Helper()
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.NewWithConfig(ctx, config.Copy())
	if err != nil {
		t.Fatal(err)
	}
	if err = admin.Ping(ctx); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	random := make([]byte, 8)
	if _, err = rand.Read(random); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	schema := "aicrm_owner_handoff_" + hex.EncodeToString(random)
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	testConfig := config.Copy()
	testConfig.ConnConfig.RuntimeParams["search_path"] = schema
	native, err := pgxpool.NewWithConfig(ctx, testConfig)
	if err != nil {
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close()
		t.Fatal(err)
	}
	root := filepath.Clean(filepath.Join("..", ".."))
	for _, name := range []string{"0001_platform.sql", "0002_identity.sql", "0003_access.sql", "0004_wecom.sql", "0005_external_effects.sql", "0092_customer_owner_handoff.sql"} {
		raw, readErr := os.ReadFile(filepath.Join(root, "migrations", name))
		if readErr != nil {
			native.Close()
			t.Fatal(readErr)
		}
		if _, execErr := native.Exec(ctx, string(raw)); execErr != nil {
			native.Close()
			t.Fatalf("apply %s: %v", name, execErr)
		}
	}
	pool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		native.Close()
		t.Fatal(err)
	}
	return pool, func() {
		pool.Close()
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = admin.Exec(cleanupCtx, "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close()
	}
}

func TestPostgreSQLOwnerHandoffExecutionUsesFrozenCiphertextAndFourDigests(t *testing.T) {
	databaseURL, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured; skipping owner-handoff PostgreSQL integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, cleanup := ownerHandoffPool(t, ctx, databaseURL)
	defer cleanup()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	key := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	cipher, err := NewOwnerHandoffCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	store := NewPostgreSQLOwnerHandoffStoreWithCipher(cipher)
	const batchID, effectID = "owner-batch-001", "eer_9001"
	var sourceStaff, targetStaff, customerID int64
	if err = uow.Within(ctx, func(txctx context.Context) error {
		tx, transactionErr := platformpostgres.RequireTransaction(txctx)
		if transactionErr != nil {
			return transactionErr
		}
		if transactionErr = tx.QueryRow(ctx, `INSERT INTO customers(status) VALUES('active') RETURNING id`).Scan(&customerID); transactionErr != nil {
			return transactionErr
		}
		if transactionErr = tx.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name,wecom_userid) VALUES('source-user','$argon2id$fixture','Source','source-user') RETURNING id`).Scan(&sourceStaff); transactionErr != nil {
			return transactionErr
		}
		if transactionErr = tx.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name,wecom_userid) VALUES('target-user','$argon2id$fixture','Target','target-user') RETURNING id`).Scan(&targetStaff); transactionErr != nil {
			return transactionErr
		}
		digest := make([]byte, 32)
		for index := range digest {
			digest[index] = byte(index + 1)
		}
		if _, transactionErr = tx.Exec(ctx, `INSERT INTO customer_owner_handoff_previews(id,actor_admin_user_id,mode,source_staff_id,target_staff_id,corp_scope,request_digest,confirmation_phrase,expires_at) VALUES('preview-001',$1,'wecom_then_crm',$1,$2,'wecom-corp:fixture',$3,'CONFIRM',clock_timestamp()+interval '30 minutes')`, sourceStaff, targetStaff, digest); transactionErr != nil {
			return transactionErr
		}
		if _, transactionErr = tx.Exec(ctx, `INSERT INTO customer_owner_handoff_batches(id,preview_id,actor_admin_user_id,idempotency_key,request_digest,mode,source_staff_id,target_staff_id,corp_scope,state) VALUES($1,'preview-001',$2,'owner-handoff-fixture-key',$3,'wecom_then_crm',$2,$4,'wecom-corp:fixture','accepted')`, batchID, sourceStaff, digest, targetStaff); transactionErr != nil {
			return transactionErr
		}
		sourceCipher, cipherErr := cipher.Seal("preview-001", 1, "source_userid", "source-user")
		if cipherErr != nil {
			return cipherErr
		}
		targetCipher, cipherErr := cipher.Seal("preview-001", 1, "target_userid", "target-user")
		if cipherErr != nil {
			return cipherErr
		}
		externalCipher, cipherErr := cipher.Seal("preview-001", 1, "external_userid", "external-1")
		if cipherErr != nil {
			return cipherErr
		}
		sourceDigest := ownerHandoffSnapshotDigest("source-userid", "source-user")
		targetDigest := ownerHandoffSnapshotDigest("target-userid", "target-user")
		externalDigest := ownerHandoffSnapshotDigest("external-userid", "external-1")
		payloadDigest := ownerHandoffSnapshotDigest("transfer-payload", "external-1", "")
		policyDigest := ownerHandoffSnapshotDigest("policy", "wecom_then_crm", "wecom-corp:fixture", int64Text(sourceStaff), int64Text(targetStaff))
		_, transactionErr = tx.Exec(ctx, `INSERT INTO customer_owner_handoff_lines(batch_id,line_no,customer_id,mode,source_staff_id,target_staff_id,relation_digest,source_userid_ciphertext,target_userid_ciphertext,external_identity_ciphertext,source_userid_digest,target_userid_digest,external_identity_digest,payload_digest,policy_digest,effect_id,state) VALUES($1,1,$2,'wecom_then_crm',$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,'queued')`, batchID, customerID, sourceStaff, targetStaff, digest, sourceCipher, targetCipher, externalCipher, sourceDigest[:], targetDigest[:], externalDigest[:], payloadDigest[:], policyDigest[:], effectID)
		if transactionErr != nil {
			return transactionErr
		}
		if _, transactionErr = tx.Exec(ctx, `INSERT INTO customer_owner_handoff_effects(batch_id,subbatch_ordinal,source_ref_digest,target_ref_digest,payload_digest,policy_digest,effect_id,effect_receipt_id) VALUES($1,1,$2,$3,$4,$5,$6,'eerop_9001')`, batchID, effectport.Hash("execution-source"), effectport.Hash("execution-target"), effectport.Hash("execution-payload"), effectport.Hash("execution-policy"), effectID); transactionErr != nil {
			return transactionErr
		}
		_, transactionErr = tx.Exec(ctx, `INSERT INTO customer_owner_handoff_effect_lines(batch_id,subbatch_ordinal,line_no) VALUES($1,1,1)`, batchID)
		return transactionErr
	}); err != nil {
		t.Fatal(err)
	}
	if err = uow.Within(ctx, func(txctx context.Context) error {
		if completeErr := store.CompleteOwnerHandoffEffect(txctx, customerport.OwnerHandoffCompletion{EffectID: effectID, State: string(effectport.StateExecuted), ResultDigest: string(effectport.Hash("owner-handoff.fixture.accepted")), Attempt: 1, Generation: 1, Fence: 1, Lines: []customerport.OwnerHandoffLineCompletion{{Line: 1, State: "provider_accepted", EvidenceDigest: string(effectport.Hash("owner-handoff.fixture.accepted.line"))}}}); completeErr != nil {
			return completeErr
		}
		var lineState string
		var localCount int
		tx, txErr := platformpostgres.RequireTransaction(txctx)
		if txErr != nil {
			return txErr
		}
		if txErr = tx.QueryRow(txctx, `SELECT state FROM customer_owner_handoff_lines WHERE effect_id=$1`, effectID).Scan(&lineState); txErr != nil {
			return txErr
		}
		if txErr = tx.QueryRow(txctx, `SELECT count(*) FROM customer_local_owners WHERE customer_id=$1`, customerID).Scan(&localCount); txErr != nil {
			return txErr
		}
		if lineState != "provider_accepted" || localCount != 1 {
			t.Fatalf("accepted transfer must CAS local owner once state=%s owners=%d", lineState, localCount)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// transfer_result is a Provider-read projection only: its pending/failed
	// status is recorded against the already accepted line and never re-runs
	// transfer_customer or changes the just-CASed local owner.
	if err = uow.Within(ctx, func(txctx context.Context) error {
		read, readErr := store.LoadOwnerHandoffTransferRead(txctx, sourceStaff, batchID)
		if readErr != nil {
			return readErr
		}
		if read.SourceUserID != "source-user" || read.TargetUserID != "target-user" || read.Cursor != "" {
			t.Fatalf("read=%+v", read)
		}
		batch, changed, recordErr := store.RecordOwnerHandoffTransferResult(txctx, read, "", []customerport.OwnerHandoffTransferObservation{{ExternalUserID: "external-1", Status: 2, TakeoverTime: 0}})
		if recordErr != nil {
			return recordErr
		}
		if changed != 1 || len(batch.Lines) != 1 || batch.Lines[0].State != "observed" || batch.Lines[0].TransferStatus != 2 || batch.Lines[0].TakeoverAt != nil {
			t.Fatalf("batch=%+v changed=%d", batch, changed)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err = uow.Within(ctx, func(txctx context.Context) error {
		read, readErr := store.LoadOwnerHandoffTransferRead(txctx, sourceStaff, batchID)
		if readErr != nil {
			return readErr
		}
		if read.Cursor != "" {
			t.Fatalf("cursor=%q", read.Cursor)
		}
		batch, changed, recordErr := store.RecordOwnerHandoffTransferResult(txctx, read, "", []customerport.OwnerHandoffTransferObservation{{ExternalUserID: "external-1", Status: 1, TakeoverTime: 100}})
		if recordErr != nil || changed != 1 || len(batch.Lines) != 1 || batch.Lines[0].TransferStatus != 1 || batch.Lines[0].TakeoverAt == nil {
			t.Fatalf("later final observation err=%v changed=%d batch=%+v", recordErr, changed, batch)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err = uow.Within(ctx, func(txctx context.Context) error {
		execution, readErr := store.ReadOwnerHandoffExecution(txctx, effectID)
		if readErr != nil {
			return readErr
		}
		if execution.EffectID != effectID || execution.SourceUserID != "source-user" || execution.TargetUserID != "target-user" || len(execution.Lines) != 1 || execution.Lines[0].ExternalUserID != "external-1" || execution.WelcomeMessage != "" || execution.SourceDigest != snapshotDigestText("source-userid", "source-user") {
			t.Fatalf("execution=%+v", execution)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err = uow.Within(ctx, func(txctx context.Context) error {
		tx, transactionErr := platformpostgres.RequireTransaction(txctx)
		if transactionErr != nil {
			return transactionErr
		}
		_, transactionErr = tx.Exec(ctx, `UPDATE customer_owner_handoff_lines SET target_userid_digest=decode(repeat('00',32),'hex') WHERE effect_id=$1`, effectID)
		return transactionErr
	}); err != nil {
		t.Fatal(err)
	}
	if err = uow.Within(ctx, func(txctx context.Context) error {
		_, readErr := store.ReadOwnerHandoffExecution(txctx, effectID)
		return readErr
	}); !errors.Is(err, ErrOwnerHandoffConflict) {
		t.Fatalf("tampered digest read err=%v", err)
	}
}

func int64Text(value int64) string {
	return fmt.Sprintf("%d", value)
}

func snapshotDigestText(label string, values ...string) string {
	digest := ownerHandoffSnapshotDigest(label, values...)
	return effectDigestString(digest[:])
}

func TestPostgreSQLOwnerHandoffCompletionPreservesAttentionAndReplaysReceipt(t *testing.T) {
	databaseURL, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured; skipping owner-handoff PostgreSQL integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, cleanup := ownerHandoffPool(t, ctx, databaseURL)
	defer cleanup()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	cipher, cipherErr := NewOwnerHandoffCipher("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	if cipherErr != nil {
		t.Fatal(cipherErr)
	}
	store := NewPostgreSQLOwnerHandoffStoreWithCipher(cipher)
	var source, target, firstCustomer, secondCustomer int64
	if err = uow.Within(ctx, func(txctx context.Context) error {
		tx, e := platformpostgres.RequireTransaction(txctx)
		if e != nil {
			return e
		}
		if e = tx.QueryRow(txctx, `INSERT INTO admin_users(username,password_hash,display_name,wecom_userid) VALUES('completion-source','$argon2id$fixture','Source','completion-source') RETURNING id`).Scan(&source); e != nil {
			return e
		}
		if e = tx.QueryRow(txctx, `INSERT INTO admin_users(username,password_hash,display_name,wecom_userid) VALUES('completion-target','$argon2id$fixture','Target','completion-target') RETURNING id`).Scan(&target); e != nil {
			return e
		}
		if e = tx.QueryRow(txctx, `INSERT INTO customers(status) VALUES('active') RETURNING id`).Scan(&firstCustomer); e != nil {
			return e
		}
		if e = tx.QueryRow(txctx, `INSERT INTO customers(status) VALUES('active') RETURNING id`).Scan(&secondCustomer); e != nil {
			return e
		}
		digest := make([]byte, 32)
		digest[0] = 9
		if _, e = tx.Exec(txctx, `INSERT INTO customer_owner_handoff_previews(id,actor_admin_user_id,mode,source_staff_id,target_staff_id,corp_scope,request_digest,confirmation_phrase,expires_at) VALUES('preview-completion',$1,'wecom_then_crm',$1,$2,'wecom-corp:fixture',$3,'CONFIRM',clock_timestamp()+interval '30 min')`, source, target, digest); e != nil {
			return e
		}
		if _, e = tx.Exec(txctx, `INSERT INTO customer_owner_handoff_batches(id,preview_id,actor_admin_user_id,idempotency_key,request_digest,mode,source_staff_id,target_staff_id,corp_scope,state) VALUES('batch-completion','preview-completion',$1,'completion-idempotency',$2,'wecom_then_crm',$1,$3,'wecom-corp:fixture','executing')`, source, digest, target); e != nil {
			return e
		}
		for line, customerID := range []int64{firstCustomer, secondCustomer} {
			if _, e = tx.Exec(txctx, `INSERT INTO customer_owner_handoff_lines(batch_id,line_no,customer_id,mode,source_staff_id,target_staff_id,relation_digest,effect_id,state) VALUES('batch-completion',$1,$2,'wecom_then_crm',$3,$4,$5,$6,'queued')`, line+1, customerID, source, target, digest, fmt.Sprintf("effect-completion-%d", line+1)); e != nil {
				return e
			}
			effectID := fmt.Sprintf("effect-completion-%d", line+1)
			if _, e = tx.Exec(txctx, `INSERT INTO customer_owner_handoff_effects(batch_id,subbatch_ordinal,source_ref_digest,target_ref_digest,payload_digest,policy_digest,effect_id,effect_receipt_id) VALUES('batch-completion',$1,$2,$3,$4,$5,$6,$7)`, line+1, effectport.Hash("completion-source", fmt.Sprint(line+1)), effectport.Hash("completion-target", fmt.Sprint(line+1)), effectport.Hash("completion-payload", fmt.Sprint(line+1)), effectport.Hash("completion-policy", fmt.Sprint(line+1)), effectID, fmt.Sprintf("eerop-completion-%d", line+1)); e != nil {
				return e
			}
			if _, e = tx.Exec(txctx, `INSERT INTO customer_owner_handoff_effect_lines(batch_id,subbatch_ordinal,line_no) VALUES('batch-completion',$1,$2)`, line+1, line+1); e != nil {
				return e
			}
		}
		sourceCipher, e := cipher.Seal("preview-completion", 2, "source_userid", "completion-source")
		if e != nil {
			return e
		}
		targetCipher, e := cipher.Seal("preview-completion", 2, "target_userid", "completion-target")
		if e != nil {
			return e
		}
		externalCipher, e := cipher.Seal("preview-completion", 2, "external_userid", "completion-external")
		if e != nil {
			return e
		}
		externalDigest := ownerHandoffSnapshotDigest("external-userid", "completion-external")
		if _, e = tx.Exec(txctx, `UPDATE customer_owner_handoff_lines SET source_userid_ciphertext=$1,target_userid_ciphertext=$2,external_identity_ciphertext=$3,external_identity_digest=$4 WHERE batch_id='batch-completion' AND line_no=2`, sourceCipher, targetCipher, externalCipher, externalDigest[:]); e != nil {
			return e
		}
		// A different local owner appears after preview, so the accepted
		// transfer is preserved as cas_conflict rather than overwriting it.
		if _, e = tx.Exec(txctx, `INSERT INTO customer_local_owners(customer_id,staff_id,version,source) VALUES($1,$2,1,'owner_handoff_local_only')`, secondCustomer, source); e != nil {
			return e
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	unknown := customerport.OwnerHandoffCompletion{EffectID: "effect-completion-1", State: string(effectport.StateUnknown), ResultDigest: string(effectport.Hash("completion", "unknown")), Attempt: 1, Generation: 1, Fence: 1}
	accepted := customerport.OwnerHandoffCompletion{EffectID: "effect-completion-2", State: string(effectport.StateExecuted), ResultDigest: string(effectport.Hash("completion", "accepted")), Attempt: 1, Generation: 1, Fence: 1, Lines: []customerport.OwnerHandoffLineCompletion{{Line: 2, State: "provider_accepted", EvidenceDigest: string(effectport.Hash("completion", "accepted", "2"))}}}
	if err = uow.Within(ctx, func(txctx context.Context) error { return store.CompleteOwnerHandoffEffect(txctx, unknown) }); err != nil {
		t.Fatal(err)
	}
	if err = uow.Within(ctx, func(txctx context.Context) error { return store.CompleteOwnerHandoffEffect(txctx, accepted) }); err != nil {
		t.Fatal(err)
	}
	if err = uow.Within(ctx, func(txctx context.Context) error {
		tx, e := platformpostgres.RequireTransaction(txctx)
		if e != nil {
			return e
		}
		var batchState, secondState string
		var ownerVersion int64
		if e = tx.QueryRow(txctx, `SELECT state FROM customer_owner_handoff_batches WHERE id='batch-completion'`).Scan(&batchState); e != nil {
			return e
		}
		if e = tx.QueryRow(txctx, `SELECT state FROM customer_owner_handoff_lines WHERE effect_id='effect-completion-2'`).Scan(&secondState); e != nil {
			return e
		}
		if e = tx.QueryRow(txctx, `SELECT version FROM customer_local_owners WHERE customer_id=$1`, secondCustomer).Scan(&ownerVersion); e != nil {
			return e
		}
		if batchState != "needs_attention" || secondState != "cas_conflict" || ownerVersion != 1 {
			t.Fatalf("batch=%s line=%s version=%d", batchState, secondState, ownerVersion)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// A transfer_result page remains readable for the accepted-but-local-CAS-
	// conflicted line. Its final observation does not erase that conflict or
	// perform another local assignment.
	if err = uow.Within(ctx, func(txctx context.Context) error {
		read, e := store.LoadOwnerHandoffTransferRead(txctx, source, "batch-completion")
		if e != nil {
			return e
		}
		batch, changed, e := store.RecordOwnerHandoffTransferResult(txctx, read, "", []customerport.OwnerHandoffTransferObservation{{ExternalUserID: "completion-external", Status: 1, TakeoverTime: 100}})
		if e != nil || changed != 1 || len(batch.Lines) != 2 || batch.Lines[1].State != "cas_conflict" || batch.Lines[1].TransferStatus != 1 {
			t.Fatalf("readback err=%v changed=%d batch=%+v", e, changed, batch)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// Same effect/receipt/attempt is a no-op replay; it must not perform a
	// second local CAS. A stale attempt is rejected rather than rewriting facts.
	if err = uow.Within(ctx, func(txctx context.Context) error { return store.CompleteOwnerHandoffEffect(txctx, accepted) }); err != nil {
		t.Fatalf("same receipt replay: %v", err)
	}
	stale := accepted
	stale.Fence = 2 // a completion from another EER lease must not project.
	if err = uow.Within(ctx, func(txctx context.Context) error { return store.CompleteOwnerHandoffEffect(txctx, stale) }); !errors.Is(err, ErrOwnerHandoffConflict) {
		t.Fatalf("stale completion err=%v", err)
	}
}

func TestPostgreSQLOwnerHandoffUnknownArtifactKeepsKnownRows(t *testing.T) {
	databaseURL, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured; skipping owner-handoff PostgreSQL integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, cleanup := ownerHandoffPool(t, ctx, databaseURL)
	defer cleanup()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	store := NewPostgreSQLOwnerHandoffStore()
	var source, target int64
	customers := make([]customerdomain.CustomerID, 0, 3)
	digest := make([]byte, 32)
	for index := range digest {
		digest[index] = byte(index + 1)
	}
	if err = uow.Within(ctx, func(txctx context.Context) error {
		tx, txErr := platformpostgres.RequireTransaction(txctx)
		if txErr != nil {
			return txErr
		}
		if txErr = tx.QueryRow(txctx, `INSERT INTO admin_users(username,password_hash,display_name,wecom_userid) VALUES('partial-source','$argon2id$fixture','Source','partial-source') RETURNING id`).Scan(&source); txErr != nil {
			return txErr
		}
		if txErr = tx.QueryRow(txctx, `INSERT INTO admin_users(username,password_hash,display_name,wecom_userid) VALUES('partial-target','$argon2id$fixture','Target','partial-target') RETURNING id`).Scan(&target); txErr != nil {
			return txErr
		}
		for range 3 {
			var customerID customerdomain.CustomerID
			if txErr = tx.QueryRow(txctx, `INSERT INTO customers(status) VALUES('active') RETURNING id`).Scan(&customerID); txErr != nil {
				return txErr
			}
			customers = append(customers, customerID)
		}
		if _, txErr = tx.Exec(txctx, `INSERT INTO customer_owner_handoff_previews(id,actor_admin_user_id,mode,source_staff_id,target_staff_id,corp_scope,request_digest,confirmation_phrase,expires_at) VALUES('partial-preview',$1,'wecom_then_crm',$1,$2,'wecom-corp:partial',$3,'CONFIRM',clock_timestamp()+interval '30 minutes')`, source, target, digest); txErr != nil {
			return txErr
		}
		if _, txErr = tx.Exec(txctx, `INSERT INTO customer_owner_handoff_batches(id,preview_id,actor_admin_user_id,idempotency_key,request_digest,mode,source_staff_id,target_staff_id,corp_scope,state) VALUES('partial-batch','partial-preview',$1,'partial-key',$2,'wecom_then_crm',$1,$3,'wecom-corp:partial','executing')`, source, digest, target); txErr != nil {
			return txErr
		}
		for index, customerID := range customers {
			if _, txErr = tx.Exec(txctx, `INSERT INTO customer_owner_handoff_lines(batch_id,line_no,customer_id,mode,source_staff_id,target_staff_id,relation_digest,effect_id,state) VALUES('partial-batch',$1,$2,'wecom_then_crm',$3,$4,$5,'partial-effect','queued')`, index+1, customerID, source, target, digest); txErr != nil {
				return txErr
			}
		}
		if _, txErr = tx.Exec(txctx, `INSERT INTO customer_owner_handoff_effects(batch_id,subbatch_ordinal,source_ref_digest,target_ref_digest,payload_digest,policy_digest,effect_id,effect_receipt_id) VALUES('partial-batch',1,$1,$2,$3,$4,'partial-effect','eerop-partial')`, effectport.Hash("partial-source"), effectport.Hash("partial-target"), effectport.Hash("partial-payload"), effectport.Hash("partial-policy")); txErr != nil {
			return txErr
		}
		_, txErr = tx.Exec(txctx, `INSERT INTO customer_owner_handoff_effect_lines(batch_id,subbatch_ordinal,line_no) VALUES('partial-batch',1,1),('partial-batch',1,2),('partial-batch',1,3)`)
		return txErr
	}); err != nil {
		t.Fatal(err)
	}
	completion := customerport.OwnerHandoffCompletion{
		EffectID:     "partial-effect",
		State:        string(effectport.StateUnknown),
		ResultDigest: string(effectport.Hash("partial-result")),
		Attempt:      1,
		Generation:   1,
		Fence:        1,
		Lines: []customerport.OwnerHandoffLineCompletion{
			{Line: 1, State: "provider_accepted", EvidenceDigest: string(effectport.Hash("partial-line", "1", "provider_accepted"))},
			{Line: 2, State: "final_failed", EvidenceDigest: string(effectport.Hash("partial-line", "2", "final_failed"))},
			{Line: 3, State: "outcome_unknown", EvidenceDigest: string(effectport.Hash("partial-line", "3", "outcome_unknown"))},
		},
	}
	if err = uow.Within(ctx, func(txctx context.Context) error { return store.CompleteOwnerHandoffEffect(txctx, completion) }); err != nil {
		t.Fatal(err)
	}
	if err = uow.Within(ctx, func(txctx context.Context) error {
		tx, txErr := platformpostgres.RequireTransaction(txctx)
		if txErr != nil {
			return txErr
		}
		rows, txErr := tx.Query(txctx, `SELECT state FROM customer_owner_handoff_lines WHERE batch_id='partial-batch' ORDER BY line_no`)
		if txErr != nil {
			return txErr
		}
		defer rows.Close()
		var states []string
		for rows.Next() {
			var state string
			if txErr = rows.Scan(&state); txErr != nil {
				return txErr
			}
			states = append(states, state)
		}
		if txErr = rows.Err(); txErr != nil {
			return txErr
		}
		var localOwners int
		if txErr = tx.QueryRow(txctx, `SELECT count(*) FROM customer_local_owners WHERE staff_id=$1 AND source='owner_handoff_wecom_then_crm'`, target).Scan(&localOwners); txErr != nil {
			return txErr
		}
		if fmt.Sprint(states) != "[provider_accepted final_failed outcome_unknown]" || localOwners != 1 {
			t.Fatalf("states=%v localOwners=%d", states, localOwners)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
