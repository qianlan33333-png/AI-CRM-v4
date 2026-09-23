// Package segment registers the Segment/Audience domain in the composition root.
package segment

import (
	"context"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	accessport "github.com/qianlan33333-png/AI-CRM-v3/internal/access/port"
	segmenthttp "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/http"
)

type ModuleRegistration struct{}
type HTTPBindings struct {
	Audience http.Handler
	Handler  *segmenthttp.Handler
}

func NewModuleRegistration() *ModuleRegistration { return &ModuleRegistration{} }

func (m *ModuleRegistration) Bind(service segmenthttp.ConfigurationApplication, security segmenthttp.RequestSecurity) (HTTPBindings, error) {
	if m == nil {
		return HTTPBindings{}, errors.New("segment module is required")
	}
	handler, err := segmenthttp.NewHandler(service, security)
	return HTTPBindings{Audience: handler, Handler: handler}, err
}

func (m *ModuleRegistration) BindRuntime(service segmenthttp.ConfigurationApplication, snapshots segmenthttp.SnapshotApplication, security segmenthttp.RequestSecurity) (HTTPBindings, error) {
	return m.BindRuntimeWithOwners(service, snapshots, security, nil)
}
func (m *ModuleRegistration) BindRuntimeWithOwners(service segmenthttp.ConfigurationApplication, snapshots segmenthttp.SnapshotApplication, security segmenthttp.RequestSecurity, owners accessport.AudienceOwnerResolver) (HTTPBindings, error) {
	return m.BindRuntimeWithOwnerReferences(service, snapshots, security, owners, nil)
}
func (m *ModuleRegistration) BindRuntimeWithOwnerReferences(service segmenthttp.ConfigurationApplication, snapshots segmenthttp.SnapshotApplication, security segmenthttp.RequestSecurity, owners accessport.AudienceOwnerResolver, references accessport.AudienceOwnerReferenceReader) (HTTPBindings, error) {
	if m == nil {
		return HTTPBindings{}, errors.New("segment module is required")
	}
	handler, err := segmenthttp.NewRuntimeHandlerWithOwnerReferences(service, snapshots, security, owners, references)
	return HTTPBindings{Audience: handler, Handler: handler}, err
}

func (m *ModuleRegistration) Readiness(ctx context.Context, pool *pgxpool.Pool) error {
	if m == nil || pool == nil {
		return errors.New("segment module dependencies are required")
	}
	var ready bool
	err := pool.QueryRow(ctx, `SELECT NOT EXISTS (
		SELECT 1 FROM unnest(ARRAY[
			'segment_core_products',
 'segment_core_prompt_state',
 'segment_core_assignments',
 'segment_core_pushes',
 'segment_core_recommendations',
 'segment_audience_groups',
			'segment_audience_packages',
			'segment_audience_configuration_versions',
			'segment_audience_operation_receipts',
			'segment_audience_audit_events',
			'segment_audience_outbox',
			'segment_audience_refresh_runs',
			'segment_audience_snapshots',
			'segment_audience_snapshot_members',
			'segment_audience_refresh_batches',
			'segment_audience_webhook_receipts',
			'segment_audience_automation_binding_versions',
			'segment_audience_sender_sets',
			'segment_audience_sender_set_members',
			'segment_audience_member_events',
			'segment_audience_schedule_states'
		]) AS required(name)
		WHERE to_regclass(current_schema() || '.' || required.name) IS NULL
	) AND NOT EXISTS (
		SELECT 1 FROM (VALUES
			('segment_audience_configuration_versions'::text, 'refresh_mode'::text),
			('segment_audience_schedule_states'::text, 'schedule_kind'::text),
			('segment_audience_refresh_runs'::text, 'refresh_kind'::text),
			('segment_audience_groups'::text, 'created_actor_kind'::text),
			('segment_audience_groups'::text, 'updated_actor_kind'::text),
			('segment_audience_packages'::text, 'created_actor_kind'::text),
			('segment_audience_packages'::text, 'updated_actor_kind'::text),
			('segment_audience_configuration_versions'::text, 'created_actor_kind'::text),
			('segment_audience_automation_binding_versions'::text, 'created_actor_kind'::text),
			('segment_audience_sender_sets'::text, 'created_actor_kind'::text),
			('segment_audience_operation_receipts'::text, 'actor_kind'::text),
			('segment_audience_audit_events'::text, 'actor_kind'::text)
		) AS required(table_name,column_name)
		WHERE NOT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_schema=current_schema()
				AND table_name=required.table_name
				AND column_name=required.column_name
		)
	)`).Scan(&ready)
	if err != nil {
		return err
	}
	if !ready {
		return errors.New("segment audience schema is not ready")
	}
	return nil
}
