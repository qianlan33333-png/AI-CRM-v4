package outbound

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestReadinessRequiresMaterialRefreshSourceCountMigrationPostgreSQL(t *testing.T) {
	ctx := context.Background()
	native, cleanup := materialTestDatabase(t, ctx)
	defer cleanup()
	if err := seedOutboundReadinessSchema(ctx, native); err != nil {
		t.Fatal(err)
	}
	if err := Readiness(ctx, native); err != nil {
		t.Fatalf("0149 schema should be ready: %v", err)
	}
	if _, err := native.Exec(ctx, `ALTER TABLE outbound_material_refresh_items ALTER COLUMN source_count SET DEFAULT 1`); err != nil {
		t.Fatal(err)
	}
	if err := Readiness(ctx, native); err == nil {
		t.Fatal("readiness accepted the pre-0149 source_count default")
	}
}

func TestReadinessRequiresSurveyCompletionEndpointStructurePostgreSQL(t *testing.T) {
	ctx := context.Background()
	for _, test := range []struct {
		name  string
		alter string
	}{
		{name: "table", alter: `DROP TABLE outbound_survey_completion_endpoints`},
		{name: "questionnaire ID column", alter: `ALTER TABLE outbound_survey_completion_endpoints DROP COLUMN questionnaire_id`},
		{name: "configuration reference column", alter: `ALTER TABLE outbound_survey_completion_endpoints DROP COLUMN configuration_reference`},
		{name: "template reference column", alter: `ALTER TABLE outbound_survey_completion_endpoints DROP COLUMN template_reference`},
		{name: "endpoint column", alter: `ALTER TABLE outbound_survey_completion_endpoints DROP COLUMN endpoint`},
		{name: "configuration metadata column", alter: `ALTER TABLE outbound_survey_completion_endpoints DROP COLUMN configuration_metadata`},
		{name: "revision column", alter: `ALTER TABLE outbound_survey_completion_endpoints DROP COLUMN revision`},
		{name: "updated at column", alter: `ALTER TABLE outbound_survey_completion_endpoints DROP COLUMN updated_at`},
		{name: "questionnaire primary key", alter: `ALTER TABLE outbound_survey_completion_endpoints DROP CONSTRAINT survey_completion_endpoint_questionnaire_primary`},
		{name: "configuration reference unique", alter: `ALTER TABLE outbound_survey_completion_endpoints DROP CONSTRAINT survey_completion_endpoint_configuration_reference_unique`},
		{name: "questionnaire positive", alter: `ALTER TABLE outbound_survey_completion_endpoints DROP CONSTRAINT survey_completion_endpoint_questionnaire_positive`},
		{name: "template reference length", alter: `ALTER TABLE outbound_survey_completion_endpoints DROP CONSTRAINT survey_completion_endpoint_template_length`},
		{name: "endpoint length", alter: `ALTER TABLE outbound_survey_completion_endpoints DROP CONSTRAINT survey_completion_endpoint_endpoint_length`},
		{name: "metadata shape", alter: `ALTER TABLE outbound_survey_completion_endpoints DROP CONSTRAINT survey_completion_endpoint_metadata_shape`},
		{name: "revision positive", alter: `ALTER TABLE outbound_survey_completion_endpoints DROP CONSTRAINT survey_completion_endpoint_revision_positive`},
		{name: "metadata default", alter: `ALTER TABLE outbound_survey_completion_endpoints ALTER COLUMN configuration_metadata DROP DEFAULT`},
		{name: "revision default", alter: `ALTER TABLE outbound_survey_completion_endpoints ALTER COLUMN revision DROP DEFAULT`},
		{name: "updated at default", alter: `ALTER TABLE outbound_survey_completion_endpoints ALTER COLUMN updated_at DROP DEFAULT`},
	} {
		t.Run(test.name, func(t *testing.T) {
			native, cleanup := materialTestDatabase(t, ctx)
			defer cleanup()
			if err := seedOutboundReadinessSchema(ctx, native); err != nil {
				t.Fatal(err)
			}
			if err := Readiness(ctx, native); err != nil {
				t.Fatalf("0177 endpoint schema should be ready: %v", err)
			}
			if _, err := native.Exec(ctx, test.alter); err != nil {
				t.Fatal(err)
			}
			if err := Readiness(ctx, native); err == nil {
				t.Fatal("readiness accepted incomplete 0177 Survey completion endpoint structure")
			}
		})
	}
}

func seedOutboundReadinessSchema(ctx context.Context, native *pgxpool.Pool) error {
	_, err := native.Exec(ctx, `
CREATE TABLE outbound_message_intents(content_snapshot JSONB,content_snapshot_digest TEXT,CONSTRAINT outbound_message_intents_content_snapshot_shape CHECK(TRUE));
CREATE TABLE outbound_commerce_push_intents(payload_mode TEXT);
CREATE TABLE outbound_commerce_push_endpoints();
CREATE TABLE outbound_commerce_push_history_batches();
CREATE TABLE outbound_commerce_push_history_rows();
CREATE TABLE outbound_commerce_push_history_batch_rows();
CREATE TABLE outbound_survey_completion_endpoints (
    questionnaire_id BIGINT CONSTRAINT survey_completion_endpoint_questionnaire_primary PRIMARY KEY CONSTRAINT survey_completion_endpoint_questionnaire_positive CHECK (questionnaire_id > 0),
    configuration_reference TEXT NOT NULL CONSTRAINT survey_completion_endpoint_configuration_reference_unique UNIQUE,
    template_reference TEXT NOT NULL CONSTRAINT survey_completion_endpoint_template_length CHECK (length(template_reference) BETWEEN 1 AND 128),
    endpoint TEXT NOT NULL CONSTRAINT survey_completion_endpoint_endpoint_length CHECK (length(endpoint) BETWEEN 1 AND 4096),
    configuration_metadata JSONB NOT NULL DEFAULT '{}'::jsonb CONSTRAINT survey_completion_endpoint_metadata_shape CHECK (jsonb_typeof(configuration_metadata) = 'object'),
    revision BIGINT NOT NULL DEFAULT 1 CONSTRAINT survey_completion_endpoint_revision_positive CHECK (revision > 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);`)
	return err
}
