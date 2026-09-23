package outbound

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

type contactDescriptionTestEffects struct{}

func (contactDescriptionTestEffects) AcceptAndQueueWithin(ctx context.Context, command effectport.AcceptCommand) (effectport.Projection, effectport.Receipt, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return effectport.Projection{}, effectport.Receipt{}, err
	}
	var id int64
	if err = tx.QueryRow(ctx, `INSERT INTO contact_description_test_effects(receipt_key,envelope_fingerprint) VALUES($1,$2) RETURNING id`, command.ReceiptKey, command.Envelope.Fingerprint()).Scan(&id); err != nil {
		return effectport.Projection{}, effectport.Receipt{}, err
	}
	return effectport.Projection{ID: fmt.Sprintf("eer_%d", id), QueueJobID: id, State: effectport.StateQueued}, effectport.Receipt{QueueReceiptID: fmt.Sprintf("queue_%d", id)}, nil
}

func TestPostgreSQLContactDescriptionIntentMigrationUOWNoopAndRunStats(t *testing.T) {
	ctx := context.Background()
	native, cleanup := contactDescriptionTestDatabase(t, ctx)
	defer cleanup()
	wrapped, err := platformpostgres.Wrap(native, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewContactDescriptionIntentStore(native, contactDescriptionTestEffects{})
	if err != nil {
		t.Fatal(err)
	}
	sink, err := NewContactDescriptionCompletionSink(store)
	if err != nil {
		t.Fatal(err)
	}
	var customerID int64
	if err = native.QueryRow(ctx, `INSERT INTO customers(status) VALUES('active') RETURNING id`).Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	command := contactDescriptionIntentCommand(customerID, "manual-description", 101)
	var accepted outboundport.ContactDescriptionIntentResult
	if err = uow.Within(ctx, func(txContext context.Context) error {
		var acceptErr error
		accepted, acceptErr = store.WriteContactDescriptionIntentWithin(txContext, command)
		return acceptErr
	}); err != nil {
		t.Fatal(err)
	}
	if accepted.IntentID < 1 || accepted.EffectID == "" || accepted.Replayed {
		t.Fatalf("accepted=%+v", accepted)
	}

	// A duplicate relation from another full run reuses the exact immutable
	// intent. The run association is still recorded for honest run statistics.
	replayCommand := command
	replayCommand.SourceRunID = 202
	if err = uow.Within(ctx, func(txContext context.Context) error {
		replay, acceptErr := store.WriteContactDescriptionIntentWithin(txContext, replayCommand)
		if acceptErr != nil {
			return acceptErr
		}
		if !replay.Replayed || replay.IntentID != accepted.IntentID || replay.EffectID != accepted.EffectID {
			return fmt.Errorf("unexpected replay %+v", replay)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	payload := []byte(`{"status":"already_present","readback":"not_requested"}`)
	artifact := effectport.ResultArtifact{Kind: "wecom.contact.description.result.v1", Payload: payload, Digest: effectport.Hash("external-effect.artifact.v1", "wecom.contact.description.result.v1", string(payload))}
	result := effectport.AdapterResult{Completion: effectport.StateExecuted, ReceiptDigest: effectport.Hash("contact-description-noop", accepted.EffectID), CallAttempted: true, RealExternalCallExecuted: false, Artifact: artifact}
	if err = uow.Within(ctx, func(txContext context.Context) error {
		return sink.CompleteEffect(txContext, accepted.EffectID, effectport.Envelope{Kind: effectport.KindWeComContactDescription}, effectport.Attempt{}, result)
	}); err != nil {
		t.Fatal(err)
	}
	stats, err := store.ContactDescriptionRunStats(ctx, 202)
	if err != nil || stats.Discovered != 1 || stats.AlreadyPresent != 1 || stats.BackfillCompleted != 1 || stats.Written != 0 {
		t.Fatalf("stats=%+v err=%v", stats, err)
	}

	// A known Provider rejection stores only its strict numeric error code with
	// the whitelisted status. Raw adapter text must never reach this row.
	var rejected outboundport.ContactDescriptionIntentResult
	if err = uow.Within(ctx, func(txContext context.Context) error {
		var acceptErr error
		rejected, acceptErr = store.WriteContactDescriptionIntentWithin(txContext, contactDescriptionIntentCommand(customerID, "provider-reject", 404))
		return acceptErr
	}); err != nil {
		t.Fatal(err)
	}
	final := contactDescriptionFinal("provider_rejected", 40003, effectport.Hash("contact-description-provider-rejected", rejected.EffectID))
	if err = uow.Within(ctx, func(txContext context.Context) error {
		return sink.CompleteEffect(txContext, rejected.EffectID, effectport.Envelope{Kind: effectport.KindWeComContactDescription}, effectport.Attempt{}, final)
	}); err != nil {
		t.Fatal(err)
	}
	var status string
	var providerCode int64
	if err = native.QueryRow(ctx, `SELECT result_status,provider_error_code FROM outbound_wecom_contact_description_intents WHERE id=$1`, rejected.IntentID).Scan(&status, &providerCode); err != nil || status != "provider_rejected" || providerCode != 40003 {
		t.Fatalf("provider rejection status=%q code=%d err=%v", status, providerCode, err)
	}

	// A failed intent insert after accepting its effect rolls both records back
	// under the caller's one PostgreSQL UoW; it cannot leave an orphan intent.
	var effectsBefore, intentsBefore int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM contact_description_test_effects`).Scan(&effectsBefore); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM outbound_wecom_contact_description_intents`).Scan(&intentsBefore); err != nil {
		t.Fatal(err)
	}
	bad := contactDescriptionIntentCommand(customerID+9999, "rollback-description", 303)
	if err = uow.Within(ctx, func(txContext context.Context) error {
		_, acceptErr := store.WriteContactDescriptionIntentWithin(txContext, bad)
		return acceptErr
	}); pgErrorCodeContactDescription(err) != "23503" {
		t.Fatalf("foreign-key rollback err=%v code=%s", err, pgErrorCodeContactDescription(err))
	}
	var effectsAfter, intentsAfter int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM contact_description_test_effects`).Scan(&effectsAfter); err != nil || effectsAfter != effectsBefore {
		t.Fatalf("effects before=%d after=%d err=%v", effectsBefore, effectsAfter, err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM outbound_wecom_contact_description_intents`).Scan(&intentsAfter); err != nil || intentsAfter != intentsBefore {
		t.Fatalf("intents before=%d after=%d err=%v", intentsBefore, intentsAfter, err)
	}

	// 0170 permits only completion fields. It must also reject destructive
	// delete and truncate operations, as the row remains the audit snapshot.
	if _, err = native.Exec(ctx, `UPDATE outbound_wecom_contact_description_intents SET customer_id=$2 WHERE id=$1`, accepted.IntentID, customerID+1); pgErrorCodeContactDescription(err) != "P0001" {
		t.Fatalf("immutable update err=%v code=%s", err, pgErrorCodeContactDescription(err))
	}
	if _, err = native.Exec(ctx, `DELETE FROM outbound_wecom_contact_description_intents WHERE id=$1`, accepted.IntentID); pgErrorCodeContactDescription(err) != "P0001" {
		t.Fatalf("immutable delete err=%v code=%s", err, pgErrorCodeContactDescription(err))
	}
	if _, err = native.Exec(ctx, `TRUNCATE outbound_wecom_contact_description_intents CASCADE`); pgErrorCodeContactDescription(err) != "P0001" {
		t.Fatalf("immutable truncate err=%v code=%s", err, pgErrorCodeContactDescription(err))
	}
}

func contactDescriptionIntentCommand(customerID int64, description string, runID int64) outboundport.ContactDescriptionIntentCommand {
	external := effectport.Hash("contact-description-test-external", description)
	observed := outboundport.ContactDescriptionObservedDigest(description)
	return outboundport.ContactDescriptionIntentCommand{
		CustomerID:                customerdomain.CustomerID(customerID),
		EmployeeUserID:            "employee-test",
		SourceDigest:              effectport.Hash("contact-description-test-source", description),
		TargetDigest:              outboundport.ContactDescriptionTargetDigest("employee-test", "external-test"),
		ObservedDescriptionDigest: observed,
		PayloadDigest:             outboundport.ContactDescriptionPayloadDigest(observed),
		ReceiptKey:                outboundport.ContactDescriptionRelationshipKey("wecom-corp:test", "employee-test", external),
		SourceRunID:               runID,
		Operation:                 outboundport.ContactDescriptionOperationWrite,
	}
}

func pgErrorCodeContactDescription(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}

func contactDescriptionTestDatabase(t *testing.T, ctx context.Context) (*pgxpool.Pool, func()) {
	t.Helper()
	dsn, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL not configured")
	}
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	random := make([]byte, 8)
	if _, err = rand.Read(random); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	schema := "contact_description_" + hex.EncodeToString(random)
	ident := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+ident); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		admin.Close()
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	native, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		admin.Close()
		t.Fatal(err)
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		native.Close()
		admin.Close()
		t.Fatal("locate contact description migration")
	}
	for _, name := range []string{"0001_platform.sql", "0002_identity.sql", "0005_external_effects.sql", "0170_wecom_contact_description_effect.sql"} {
		raw, readErr := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "migrations", name))
		if readErr != nil {
			native.Close()
			admin.Close()
			t.Fatal(readErr)
		}
		if _, execErr := native.Exec(ctx, string(raw)); execErr != nil {
			native.Close()
			admin.Close()
			t.Fatalf("apply %s: %v", name, execErr)
		}
	}
	if _, err = native.Exec(ctx, `CREATE TABLE contact_description_test_effects(id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,receipt_key TEXT NOT NULL,envelope_fingerprint TEXT NOT NULL)`); err != nil {
		native.Close()
		admin.Close()
		t.Fatal(err)
	}
	return native, func() {
		native.Close()
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+ident+" CASCADE")
		admin.Close()
	}
}
