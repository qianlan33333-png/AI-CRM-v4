package main

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformruntime "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/runtime"
	survey "github.com/qianlan33333-png/AI-CRM-v3/internal/survey"
)

func TestPostgreSQLSurveyReadinessRequiresIdentitySubjectColumn(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := readinessIntegrationPool(t, ctx)
	defer cleanup()
	applyAllReadinessMigrations(t, ctx, pool)

	module := survey.NewModuleRegistration()
	handler, err := platformruntime.NewHandler(platformruntime.HandlerOptions{
		ReleaseSHA: "survey-subject-readiness-test",
		Readiness: platformruntime.ReadinessFunc(func(ctx context.Context) error {
			return module.Readiness(ctx, pool)
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	assertReadinessStatus(t, handler, http.StatusOK)
	if err := module.Readiness(ctx, pool); err != nil {
		t.Fatalf("complete Survey schema is not ready: %v", err)
	}

	// Preserve the applied ledger while removing only the runtime-required
	// column. The module gate must inspect its own actual structure as well as
	// the composition's migration ledger gate.
	if _, err := pool.Exec(ctx, `ALTER TABLE survey_identity_sessions DROP COLUMN provider_identity_id`); err != nil {
		t.Fatal(err)
	}
	t.Run("missing_column_not_ready", func(t *testing.T) {
		if err := module.Readiness(ctx, pool); err == nil || !strings.Contains(err.Error(), "provider_identity_id") {
			t.Errorf("missing Survey identity subject column readiness error=%v", err)
		}
		assertReadinessStatus(t, handler, http.StatusServiceUnavailable)
	})

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate Survey identity subject migration")
	}
	contents, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "migrations", "0223_survey_identity_session_subject.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(contents)); err != nil {
		t.Fatalf("restore actual Survey subject migration structure: %v", err)
	}
	if err := module.Readiness(ctx, pool); err != nil {
		t.Fatalf("restored Survey identity subject schema is not ready: %v", err)
	}
	assertReadinessStatus(t, handler, http.StatusOK)
}

func TestPostgreSQLSurveySubmissionSubjectReadiness(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := readinessIntegrationPool(t, ctx)
	defer cleanup()
	applyAllReadinessMigrations(t, ctx, pool)
	module := survey.NewModuleRegistration()
	if err := module.Readiness(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `ALTER TABLE survey_submissions DROP COLUMN provider_identity_id`); err != nil {
		t.Fatal(err)
	}
	if err := module.Readiness(ctx, pool); err == nil || !strings.Contains(err.Error(), "survey_submissions.provider_identity_id") {
		t.Fatalf("missing receipt subject column was ready: %v", err)
	}
	_, file, _, _ := runtime.Caller(0)
	contents, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "migrations", "0244_survey_submission_subject.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, string(contents)); err != nil {
		t.Fatal(err)
	}
	if err = module.Readiness(ctx, pool); err != nil {
		t.Fatal(err)
	}
	for _, config := range []platformconfig.Runtime{{}, readinessEnabledConfig()} {
		if !containsMigration(requiredCurrentReleaseMigrations(config), "0244") {
			t.Fatal("receipt subject migration omitted from the runtime-required ledger")
		}
		if _, err = pool.Exec(ctx, `DELETE FROM platform_schema_migrations WHERE version='0244'`); err != nil {
			t.Fatal(err)
		}
		if err = checkCurrentReleaseSchema(ctx, pool, config); err == nil || !strings.Contains(err.Error(), "0244") {
			t.Fatalf("missing receipt subject migration ledger accepted: %v", err)
		}
		insertReadinessMigration(t, ctx, pool, "0244")
	}
}

func TestPostgreSQLReleaseReadinessRequiresSurveyIdentitySubjectMigration(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		config platformconfig.Runtime
	}{
		{name: "providers_disabled", config: platformconfig.Runtime{}},
		{name: "optional_projections_enabled", config: readinessEnabledConfig()},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			pool, cleanup := readinessIntegrationPool(t, ctx)
			defer cleanup()
			createReadinessMigrationLedger(t, ctx, pool)
			if _, err := pool.Exec(ctx, `CREATE TABLE order_service_entitlements (alliance text)`); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `CREATE TABLE order_checkout_snapshots (post_purchase_action jsonb)`); err != nil {
				t.Fatal(err)
			}
			createMinimumCustomerReadinessTables(t, ctx, pool)
			required := requiredCurrentReleaseMigrations(scenario.config)
			for _, version := range required {
				insertReadinessMigration(t, ctx, pool, version)
			}
			if !containsMigration(required, "0223") {
				insertReadinessMigration(t, ctx, pool, "0223")
			}
			handler := currentReleaseReadinessHandler(t, pool, scenario.config)
			assertReadinessStatus(t, handler, http.StatusOK)

			if _, err := pool.Exec(ctx, `DELETE FROM platform_schema_migrations WHERE version='0223'`); err != nil {
				t.Fatal(err)
			}
			t.Run("missing_0223_not_ready", func(t *testing.T) {
				if !containsMigration(required, "0223") {
					t.Error("runtime-required migration list omitted Survey identity subject migration 0223")
				}
				if err := checkCurrentReleaseSchema(ctx, pool, scenario.config); err == nil || !strings.Contains(err.Error(), "0223") {
					t.Errorf("missing Survey subject migration readiness error=%v", err)
				}
				assertReadinessStatus(t, handler, http.StatusServiceUnavailable)
			})

			insertReadinessMigration(t, ctx, pool, "0223")
			if err := checkCurrentReleaseSchema(ctx, pool, scenario.config); err != nil {
				t.Fatalf("restored Survey subject migration ledger is not ready: %v", err)
			}
			assertReadinessStatus(t, handler, http.StatusOK)
		})
	}
}

func TestPostgreSQLReleaseReadinessRequiresOrdersOAuthReturnMigration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	pool, cleanup := readinessIntegrationPool(t, ctx)
	defer cleanup()
	createReadinessMigrationLedger(t, ctx, pool)
	if _, err := pool.Exec(ctx, `CREATE TABLE order_service_entitlements (alliance text)`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `CREATE TABLE order_checkout_snapshots (post_purchase_action jsonb)`); err != nil {
		t.Fatal(err)
	}
	createMinimumCustomerReadinessTables(t, ctx, pool)

	config := platformconfig.Runtime{}
	config.WeChatPay.H5OAuthEnabled = true
	required := requiredCurrentReleaseMigrations(config)
	if !containsMigration(required, "0225") {
		t.Fatal("H5 OAuth readiness omitted Payment orders return-path migration 0225")
	}
	for _, version := range required {
		insertReadinessMigration(t, ctx, pool, version)
	}
	handler := currentReleaseReadinessHandler(t, pool, config)
	assertReadinessStatus(t, handler, http.StatusOK)

	if _, err := pool.Exec(ctx, `DELETE FROM platform_schema_migrations WHERE version='0225'`); err != nil {
		t.Fatal(err)
	}
	if err := checkCurrentReleaseSchema(ctx, pool, config); err == nil || !strings.Contains(err.Error(), "0225") {
		t.Fatalf("missing orders OAuth return-path migration readiness error=%v", err)
	}
	assertReadinessStatus(t, handler, http.StatusServiceUnavailable)
	insertReadinessMigration(t, ctx, pool, "0225")
	if err := checkCurrentReleaseSchema(ctx, pool, config); err != nil {
		t.Fatalf("restored orders OAuth return-path migration readiness error=%v", err)
	}
	assertReadinessStatus(t, handler, http.StatusOK)

	if containsMigration(requiredCurrentReleaseMigrations(platformconfig.Runtime{}), "0225") {
		t.Fatal("disabled H5 OAuth unnecessarily requires the orders return-path migration")
	}
}
