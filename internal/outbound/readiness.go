package outbound

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Readiness verifies Outbound-owned immutable-content, material-refresh, and
// Survey completion endpoint schema before a worker can claim an automatic
// message. Missing physical structure must fail before it becomes a retryable
// runtime error after an effect has already been accepted.
func Readiness(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return errors.New("outbound readiness requires PostgreSQL")
	}
	var ready bool
	err := pool.QueryRow(ctx, `SELECT
		EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='outbound_message_intents' AND column_name='content_snapshot')
		AND EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='outbound_message_intents' AND column_name='content_snapshot_digest')
		AND EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='outbound_message_intents'::regclass AND conname='outbound_message_intents_content_snapshot_shape')
		AND to_regclass(current_schema() || '.outbound_commerce_push_intents') IS NOT NULL
 AND EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='outbound_commerce_push_intents' AND column_name='payload_mode')
		AND to_regclass(current_schema() || '.outbound_commerce_push_endpoints') IS NOT NULL
		AND to_regclass(current_schema() || '.outbound_commerce_push_history_batches') IS NOT NULL
		AND to_regclass(current_schema() || '.outbound_commerce_push_history_rows') IS NOT NULL
		AND to_regclass(current_schema() || '.outbound_commerce_push_history_batch_rows') IS NOT NULL
		AND to_regclass(current_schema() || '.outbound_material_refresh_items') IS NOT NULL
		AND EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='outbound_material_refresh_items'::regclass AND conname='outbound_material_refresh_items_source_count_check' AND pg_get_constraintdef(oid) LIKE '%source_count >= 0%')
		AND EXISTS(SELECT 1 FROM pg_attrdef d JOIN pg_attribute a ON a.attrelid=d.adrelid AND a.attnum=d.adnum WHERE d.adrelid='outbound_material_refresh_items'::regclass AND a.attname='source_count' AND pg_get_expr(d.adbin,d.adrelid)='0')
		AND to_regclass(current_schema() || '.outbound_survey_completion_endpoints') IS NOT NULL
		AND EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='outbound_survey_completion_endpoints' AND column_name='questionnaire_id' AND data_type='bigint' AND is_nullable='NO')
		AND EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='outbound_survey_completion_endpoints' AND column_name='configuration_reference' AND data_type='text' AND is_nullable='NO')
		AND EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='outbound_survey_completion_endpoints' AND column_name='template_reference' AND data_type='text' AND is_nullable='NO')
		AND EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='outbound_survey_completion_endpoints' AND column_name='endpoint' AND data_type='text' AND is_nullable='NO')
		AND EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='outbound_survey_completion_endpoints' AND column_name='configuration_metadata' AND data_type='jsonb' AND is_nullable='NO')
		AND EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='outbound_survey_completion_endpoints' AND column_name='revision' AND data_type='bigint' AND is_nullable='NO')
		AND EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='outbound_survey_completion_endpoints' AND column_name='updated_at' AND data_type='timestamp with time zone' AND is_nullable='NO')
		AND EXISTS(SELECT 1 FROM pg_constraint c WHERE c.conrelid=to_regclass(current_schema() || '.outbound_survey_completion_endpoints') AND c.contype='p' AND pg_get_constraintdef(c.oid) LIKE 'PRIMARY KEY (questionnaire_id)%')
		AND EXISTS(SELECT 1 FROM pg_constraint c WHERE c.conrelid=to_regclass(current_schema() || '.outbound_survey_completion_endpoints') AND c.contype='u' AND pg_get_constraintdef(c.oid) LIKE 'UNIQUE (configuration_reference)%')
		AND EXISTS(SELECT 1 FROM pg_constraint c WHERE c.conrelid=to_regclass(current_schema() || '.outbound_survey_completion_endpoints') AND c.contype='c' AND pg_get_constraintdef(c.oid) LIKE '%questionnaire_id > 0%')
		AND EXISTS(SELECT 1 FROM pg_constraint c WHERE c.conrelid=to_regclass(current_schema() || '.outbound_survey_completion_endpoints') AND c.contype='c' AND pg_get_constraintdef(c.oid) LIKE '%length(template_reference) >= 1%' AND pg_get_constraintdef(c.oid) LIKE '%length(template_reference) <= 128%')
		AND EXISTS(SELECT 1 FROM pg_constraint c WHERE c.conrelid=to_regclass(current_schema() || '.outbound_survey_completion_endpoints') AND c.contype='c' AND pg_get_constraintdef(c.oid) LIKE '%length(endpoint) >= 1%' AND pg_get_constraintdef(c.oid) LIKE '%length(endpoint) <= 4096%')
		AND EXISTS(SELECT 1 FROM pg_constraint c WHERE c.conrelid=to_regclass(current_schema() || '.outbound_survey_completion_endpoints') AND c.contype='c' AND pg_get_constraintdef(c.oid) LIKE '%jsonb_typeof(configuration_metadata) = ''object''%')
		AND EXISTS(SELECT 1 FROM pg_constraint c WHERE c.conrelid=to_regclass(current_schema() || '.outbound_survey_completion_endpoints') AND c.contype='c' AND pg_get_constraintdef(c.oid) LIKE '%revision > 0%')
		AND EXISTS(SELECT 1 FROM pg_attrdef d JOIN pg_attribute a ON a.attrelid=d.adrelid AND a.attnum=d.adnum WHERE d.adrelid=to_regclass(current_schema() || '.outbound_survey_completion_endpoints') AND a.attname='configuration_metadata' AND pg_get_expr(d.adbin,d.adrelid)='''{}''::jsonb')
		AND EXISTS(SELECT 1 FROM pg_attrdef d JOIN pg_attribute a ON a.attrelid=d.adrelid AND a.attnum=d.adnum WHERE d.adrelid=to_regclass(current_schema() || '.outbound_survey_completion_endpoints') AND a.attname='revision' AND pg_get_expr(d.adbin,d.adrelid)='1')
		AND EXISTS(SELECT 1 FROM pg_attrdef d JOIN pg_attribute a ON a.attrelid=d.adrelid AND a.attnum=d.adnum WHERE d.adrelid=to_regclass(current_schema() || '.outbound_survey_completion_endpoints') AND a.attname='updated_at' AND pg_get_expr(d.adbin,d.adrelid) LIKE '%now()%')`).Scan(&ready)
	if err != nil {
		return err
	}
	if !ready {
		return errors.New("outbound content snapshot schema is not ready")
	}
	return nil
}
