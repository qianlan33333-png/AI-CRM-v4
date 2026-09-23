package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerstore "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/store"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	identitysecure "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/secure"
	identitystore "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/store"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	wecomprovider "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/provider"
)

func TestPhoneImportApplyAndRowReplayPostgreSQL(t *testing.T) {
	databaseURL, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("database URL not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	random := make([]byte, 8)
	if _, err = rand.Read(random); err != nil {
		t.Fatal(err)
	}
	schema := "phone_import_" + hex.EncodeToString(random)
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(context.Background(), "DROP SCHEMA "+identifier+" CASCADE")
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	native, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join("..", "..")
	for _, name := range []string{"0001_platform.sql", "0002_identity.sql", "0009_customer_activation.sql", "0022_customer_profile_sections.sql"} {
		raw, readErr := os.ReadFile(filepath.Join(root, "migrations", name))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, err = native.Exec(ctx, string(raw)); err != nil {
			t.Fatalf("migration %s: %v", name, err)
		}
	}
	if _, err = native.Exec(ctx, `CREATE TABLE survey_submissions(id BIGINT PRIMARY KEY); CREATE TABLE survey_submission_answers(id BIGINT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "migrations", "0038_survey_oauth_phone_vault.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, string(raw)); err != nil {
		t.Fatalf("migration 0030: %v", err)
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
	key := make([]byte, 32)
	if _, err = rand.Read(key); err != nil {
		t.Fatal(err)
	}
	vault, err := identitysecure.NewPhoneVault(base64.RawStdEncoding.EncodeToString(key))
	if err != nil {
		t.Fatal(err)
	}
	oneID := identityapp.OneIDService{Store: identitystore.NewPostgresStore(vault)}
	projection := customerstore.NewPostgreSQL()
	fact, err := wecomprovider.VerifiedExternalContact("corp", "external-integration", "wecom.directory_sync")
	if err != nil {
		t.Fatal(err)
	}
	var customerID int64
	if err = uow.Within(ctx, func(txContext context.Context) error {
		result, provisionErr := oneID.ProvisionVerifiedIdentity(txContext, identityport.ProvisionCommand{Fact: fact, IdempotencyKey: "phone-import-test-provision"})
		if provisionErr != nil {
			return provisionErr
		}
		customerID = int64(result.CustomerID)
		return projection.ActivateDirectoryCustomer(txContext, result.CustomerID, "phone_import_test", time.Now().UTC())
	}); err != nil {
		t.Fatal(err)
	}
	manifestDigest := sha256.Sum256([]byte("manifest"))
	runID, applied, err := createRun(ctx, uow, "phone-import:integration", manifestDigest, 1)
	if err != nil || applied {
		t.Fatalf("run=%d applied=%t err=%v", runID, applied, err)
	}
	rowDigest := sha256.Sum256([]byte("source-row"))
	item := &classifiedRow{receiptRowID: "source-row-1", digest: rowDigest, phone: "13812345678", customerID: customerdomain.CustomerID(customerID), outcome: "attached"}
	if err = applyRows(ctx, uow, oneID, projection, runID, []*classifiedRow{item}); err != nil {
		t.Fatal(err)
	}
	item.outcome = "attached"
	if err = applyRows(ctx, uow, oneID, projection, runID, []*classifiedRow{item}); err != nil {
		t.Fatalf("row replay: %v", err)
	}
	if item.outcome != "attached" {
		t.Fatalf("replay outcome=%s", item.outcome)
	}
	if err = finishRun(ctx, uow, runID, summarize([]*classifiedRow{item})); err != nil {
		t.Fatal(err)
	}
	if err = reconcile(ctx, uow, "phone-import:integration"); err != nil {
		t.Fatal(err)
	}
	var receipts, identities, secrets, plaintext, masked int
	if err = pool.Native().QueryRow(ctx, `SELECT
		(SELECT count(*) FROM identity_phone_import_receipts WHERE run_id=$1),
		(SELECT count(*) FROM customer_identities WHERE customer_id=$2 AND kind='phone' AND assurance='declared' AND status='active'),
		(SELECT count(*) FROM identity_phone_secrets secrets JOIN customer_identities identities ON identities.id=secrets.identity_id WHERE identities.customer_id=$2),
		(SELECT count(*) FROM customer_identities WHERE customer_id=$2 AND kind='phone' AND normalized_value<>''),
		(SELECT count(*) FROM customer_directory_projection WHERE customer_id=$2 AND phone_masked='138****5678' AND phone_assurance='declared')`, runID, customerID).Scan(&receipts, &identities, &secrets, &plaintext, &masked); err != nil {
		t.Fatal(err)
	}
	if receipts != 1 || identities != 1 || secrets != 1 || plaintext != 0 || masked != 1 {
		t.Fatalf("receipts=%d identities=%d secrets=%d plaintext=%d masked=%d", receipts, identities, secrets, plaintext, masked)
	}

	invalidRun, _, err := createRun(ctx, uow, "phone-import:invalid-replay", sha256.Sum256([]byte("invalid-manifest")), 1)
	if err != nil {
		t.Fatal(err)
	}
	invalid := &classifiedRow{receiptRowID: "invalid-row", digest: sha256.Sum256([]byte("invalid-row")), outcome: "invalid", errorCode: "invalid_phone"}
	for range 2 {
		if err = uow.Within(ctx, func(txContext context.Context) error { return insertReceipt(txContext, invalidRun, invalid) }); err != nil {
			t.Fatalf("invalid receipt replay: %v", err)
		}
	}
	drift := *invalid
	drift.digest = sha256.Sum256([]byte("payload-drift"))
	if err = uow.Within(ctx, func(txContext context.Context) error { return insertReceipt(txContext, invalidRun, &drift) }); !errors.Is(err, identityapp.ErrDeclaredPayloadMismatch) {
		t.Fatalf("payload drift err=%v", err)
	}
}
