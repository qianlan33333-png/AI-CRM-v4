package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	aiassistantapp "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/app"
	aiassistantport "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/port"
	aiassistantstore "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/store"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityquery "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/query"
	identitystore "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/store"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// TestAIAssistantIntakeRequiresStoredVerifiedIdentity runs at the composition
// boundary because it combines the AI Assistant application with the concrete
// Identity PostgreSQL reader. A Resolver result alone must never be proof that
// a signed inbound value is verified.
func TestAIAssistantIntakeRequiresStoredVerifiedIdentity(t *testing.T) {
	native, cleanup := aiAssistantIdentityPool(t)
	defer cleanup()
	wrapped, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := aiassistantstore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	installAIAssistantIdentity(t, native, "declared")
	service, err := aiassistantapp.NewService(uow, repository, aiAssistantIdentityCustomers{}, aiAssistantIdentityStaff{}, aiAssistantIdentityMaterials{}, identityapp.OneIDService{Store: identitystore.NewPostgresStore()}, identityquery.NewPostgreSQL())
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 9, 5, 1, 2, 3, 0, time.UTC)
	declared := aiAssistantIdentityCommand(at, "declared-only-intake", "1234567890abcdef")
	result, err := service.CreatePlanFromIdentities(context.Background(), declared)
	if err != nil || result.Plan.ID != 0 || result.Unverified != 1 || len(result.Dispositions) != 1 || result.Dispositions[0].Status != "unverified" {
		t.Fatalf("declared result=%+v err=%v", result, err)
	}
	wrongScope := aiAssistantIdentityCommand(at.Add(30*time.Second), "wrong-scope-intake", "1234567890abcdef-scope")
	wrongScope.Targets[0].Reference.Scope = "wecom-corp:other"
	scoped, err := service.CreatePlanFromIdentities(context.Background(), wrongScope)
	if err != nil || scoped.Plan.ID != 0 || scoped.NotFound != 1 || len(scoped.Dispositions) != 1 || scoped.Dispositions[0].Status != "not_found" {
		t.Fatalf("wrong scope=%+v err=%v", scoped, err)
	}
	if _, err = native.Exec(context.Background(), `UPDATE customer_identities SET assurance='verified',verified_at=clock_timestamp() WHERE customer_id=91`); err != nil {
		t.Fatal(err)
	}
	verified := aiAssistantIdentityCommand(at.Add(time.Minute), "verified-intake", "1234567890abcdef-verified")
	accepted, err := service.CreatePlanFromIdentities(context.Background(), verified)
	if err != nil || accepted.Plan.ID < 1 || accepted.Found != 1 || accepted.Unverified != 0 {
		t.Fatalf("verified result=%+v err=%v", accepted, err)
	}
	verified.Nonce = "1234567890abcdef-replay"
	verified.OccurredAt = verified.OccurredAt.Add(time.Second)
	verified.ExpiresAt = verified.ExpiresAt.Add(time.Second)
	replayed, err := service.CreatePlanFromIdentities(context.Background(), verified)
	if err != nil || !replayed.Replayed || replayed.Plan.ID != accepted.Plan.ID {
		t.Fatalf("replayed=%+v err=%v", replayed, err)
	}
	changed := verified
	changed.Nonce = "1234567890abcdef-conflict"
	changed.Targets[0].Content = []aiassistantport.ContentBlock{{Kind: aiassistantport.ContentText, Text: "changed"}}
	if _, err = service.CreatePlanFromIdentities(context.Background(), changed); !errors.Is(err, aiassistantapp.ErrIdempotencyConflict) {
		t.Fatalf("changed semantic command err=%v", err)
	}
	var customers, identities, plans, recipients int
	if err = native.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM customers),(SELECT count(*) FROM customer_identities),(SELECT count(*) FROM ai_assistant_plans),(SELECT count(*) FROM ai_assistant_plan_recipients)`).Scan(&customers, &identities, &plans, &recipients); err != nil || customers != 1 || identities != 1 || plans != 1 || recipients != 1 {
		t.Fatalf("customers=%d identities=%d plans=%d recipients=%d err=%v", customers, identities, plans, recipients, err)
	}
}

func aiAssistantIdentityCommand(at time.Time, key, nonce string) aiassistantapp.IdentityPlanCommand {
	return aiassistantapp.IdentityPlanCommand{
		Actor: aiassistantport.Actor{Kind: aiassistantport.ActorService, ID: 7}, IdempotencyKey: key,
		Name: "identity plan", SourceKind: "automation", SourceDigest: effectport.Hash("identity-plan"),
		Targets: []aiassistantapp.IdentityTarget{{
			Reference: identitydomain.Reference{Kind: identitydomain.KindWeComExternalUserID, Scope: "wecom-corp:corp-1", Value: "external-1", Assurance: identitydomain.AssuranceDeclared, Source: "test"},
			StaffID:   21, Content: []aiassistantport.ContentBlock{{Kind: aiassistantport.ContentText, Text: "hello"}},
		}},
		OccurredAt: at, IntegrationKey: "integration-key", Nonce: nonce, ExpiresAt: at.Add(5 * time.Minute),
	}
}

type aiAssistantIdentityCustomers struct{}

func (aiAssistantIdentityCustomers) CustomerSnapshot(_ context.Context, id customerdomain.CustomerID) (aiassistantapp.CustomerSnapshot, error) {
	return aiassistantapp.CustomerSnapshot{CanonicalID: id, Status: customerdomain.StatusActive, DisplayName: "customer", OneIDLabel: "OneID"}, nil
}

type aiAssistantIdentityStaff struct{}

func (aiAssistantIdentityStaff) StaffSnapshot(_ context.Context, id int64) (aiassistantapp.StaffSnapshot, error) {
	return aiassistantapp.StaffSnapshot{ID: id, DisplayName: "staff", Active: true}, nil
}

func (aiAssistantIdentityStaff) StaffByWeComUserID(_ context.Context, value string) (aiassistantapp.StaffSnapshot, error) {
	if value == "" {
		return aiassistantapp.StaffSnapshot{}, errors.New("missing staff")
	}
	return aiassistantapp.StaffSnapshot{ID: 21, DisplayName: "staff", Active: true}, nil
}

type aiAssistantIdentityMaterials struct{}

func (aiAssistantIdentityMaterials) ResolveMaterial(_ context.Context, block aiassistantport.ContentBlock) (aiassistantport.ContentBlock, error) {
	return block, nil
}

func (aiAssistantIdentityMaterials) RegisterMaterialReference(context.Context, aiassistantport.ContentBlock, effectport.Digest) error {
	return nil
}

func installAIAssistantIdentity(t *testing.T, native *pgxpool.Pool, assurance string) {
	t.Helper()
	if _, err := native.Exec(context.Background(), `INSERT INTO customers(id,status) OVERRIDING SYSTEM VALUE VALUES(91,'active')`); err != nil {
		t.Fatal(err)
	}
	query := `INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,status,verified_at) VALUES(91,'wecom_external_userid','wecom-corp:corp-1','external-1',$1,'test',1,'active',CASE WHEN $1='verified' THEN clock_timestamp() ELSE NULL END)`
	if _, err := native.Exec(context.Background(), query, assurance); err != nil {
		t.Fatal(err)
	}
}

func aiAssistantIdentityPool(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	raw, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("DATABASE_URL is not configured; skipping AI Assistant Identity PostgreSQL integration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := pgx.Connect(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	var random [8]byte
	if _, err = rand.Read(random[:]); err != nil {
		admin.Close(ctx)
		t.Fatal(err)
	}
	schema := "aicrm_ai_identity_" + hex.EncodeToString(random[:])
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		admin.Close(ctx)
		t.Fatal(err)
	}
	config, err := pgxpool.ParseConfig(raw)
	if err != nil {
		admin.Close(ctx)
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		admin.Close(ctx)
		t.Fatal(err)
	}
	for _, name := range []string{"0002_identity.sql", "0036_ai_assistant_review.sql", "0100_ai_assistant_machine_actor.sql", "0120_excel_batches.sql", "0124_operation_excel_batch_lifecycle.sql"} {
		if err = applyAIAssistantIdentityMigration(ctx, pool, name); err != nil {
			pool.Close()
			admin.Close(ctx)
			t.Fatal(err)
		}
	}
	return pool, func() {
		pool.Close()
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_, _ = admin.Exec(cleanup, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close(cleanup)
	}
}

func applyAIAssistantIdentityMigration(ctx context.Context, pool *pgxpool.Pool, name string) error {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return os.ErrNotExist
	}
	sql, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "migrations", name))
	if err != nil {
		return err
	}
	_, err = pool.Exec(ctx, string(sql))
	return err
}
