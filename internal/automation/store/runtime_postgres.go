package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	automationapp "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/app"
	automationdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/domain"
	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
)

const policyColumns = `id,code,name,lifecycle,version,current_version_id,created_by,updated_by,created_at,updated_at,archived_at`

func (r *Repository) ListPolicies(ctx context.Context) ([]automationdomain.Policy, error) {
	t, e := tx(ctx)
	if e != nil {
		return nil, e
	}
	rows, e := t.Query(ctx, `SELECT `+policyColumns+` FROM automation_policies ORDER BY updated_at DESC,id DESC`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []automationdomain.Policy{}
	for rows.Next() {
		p, scanErr := scanPolicy(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func (r *Repository) Policy(ctx context.Context, id int64) (automationdomain.Policy, error) {
	t, e := tx(ctx)
	if e != nil {
		return automationdomain.Policy{}, e
	}
	p, e := scanPolicy(t.QueryRow(ctx, `SELECT `+policyColumns+` FROM automation_policies WHERE id=$1`, id))
	if errors.Is(e, automationapp.ErrRuntimeNotFound) {
		return p, automationapp.ErrRuntimeNotFound
	}
	return p, e
}

func scanPolicy(row pgx.Row) (automationdomain.Policy, error) {
	var p automationdomain.Policy
	var lifecycle string
	err := row.Scan(&p.ID, &p.Code, &p.Name, &lifecycle, &p.Version, &p.CurrentVersionID, &p.CreatedBy, &p.UpdatedBy, &p.CreatedAt, &p.UpdatedAt, &p.ArchivedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, automationapp.ErrRuntimeNotFound
	}
	p.Lifecycle = automationdomain.PolicyLifecycle(lifecycle)
	return p, err
}
func (r *Repository) CreatePolicy(ctx context.Context, p automationdomain.Policy) (automationdomain.Policy, error) {
	t, e := tx(ctx)
	if e != nil {
		return p, e
	}
	out, e := scanPolicy(t.QueryRow(ctx, `INSERT INTO automation_policies(code,name,lifecycle,version,created_by,updated_by,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$7) RETURNING `+policyColumns, p.Code, p.Name, p.Lifecycle, p.Version, p.CreatedBy, p.UpdatedBy, p.CreatedAt))
	if unique(e) {
		return p, automationapp.ErrRuntimeConflict
	}
	return out, e
}
func (r *Repository) LockPolicy(ctx context.Context, id int64) (automationdomain.Policy, error) {
	t, e := tx(ctx)
	if e != nil {
		return automationdomain.Policy{}, e
	}
	return scanPolicy(t.QueryRow(ctx, `SELECT `+policyColumns+` FROM automation_policies WHERE id=$1 FOR UPDATE`, id))
}

const policyVersionColumns = `id,policy_id,version,package_id,trigger_kind,trigger_enabled,action_kind,action_config,quiet_hours,single_run_limit,approval_staff_id,digest,created_by,created_at`
const policyVersionJoinColumns = `v.id,v.policy_id,v.version,v.package_id,v.trigger_kind,v.trigger_enabled,v.action_kind,v.action_config,v.quiet_hours,v.single_run_limit,v.approval_staff_id,v.digest,v.created_by,v.created_at`

func scanPolicyVersion(row pgx.Row) (automationdomain.PolicyVersion, error) {
	var v automationdomain.PolicyVersion
	var trigger, action string
	var digest []byte
	err := row.Scan(&v.ID, &v.PolicyID, &v.Version, &v.PackageID, &trigger, &v.TriggerEnabled, &action, &v.ActionConfig, &v.QuietHours, &v.SingleRunLimit, &v.ApprovalStaffID, &digest, &v.CreatedBy, &v.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, automationapp.ErrRuntimeNotFound
	}
	if err != nil {
		return v, err
	}
	if len(digest) != 32 {
		return v, automationapp.ErrRuntimeConflict
	}
	copy(v.Digest[:], digest)
	v.TriggerKind = automationport.TriggerKind(trigger)
	v.ActionKind = automationport.ActionKind(action)
	return v, nil
}
func (r *Repository) NextPolicyVersion(ctx context.Context, policyID int64) (int64, error) {
	t, e := tx(ctx)
	if e != nil {
		return 0, e
	}
	var n int64
	e = t.QueryRow(ctx, `SELECT COALESCE(max(version),0)+1 FROM automation_policy_versions WHERE policy_id=$1`, policyID).Scan(&n)
	return n, e
}
func (r *Repository) CreatePolicyVersion(ctx context.Context, v automationdomain.PolicyVersion) (automationdomain.PolicyVersion, error) {
	t, e := tx(ctx)
	if e != nil {
		return v, e
	}
	return scanPolicyVersion(t.QueryRow(ctx, `INSERT INTO automation_policy_versions(policy_id,version,package_id,trigger_kind,trigger_enabled,action_kind,action_config,quiet_hours,single_run_limit,approval_staff_id,digest,created_by,created_at) VALUES($1,$2,$3,$4,$5,$6,$7::jsonb,$8::jsonb,$9,$10,$11,$12,$13) RETURNING `+policyVersionColumns, v.PolicyID, v.Version, v.PackageID, v.TriggerKind, v.TriggerEnabled, v.ActionKind, v.ActionConfig, v.QuietHours, v.SingleRunLimit, v.ApprovalStaffID, v.Digest[:], v.CreatedBy, v.CreatedAt))
}
func (r *Repository) SetCurrentPolicyVersion(ctx context.Context, policyID, versionID, expected, actor int64, now time.Time) (automationdomain.Policy, error) {
	t, e := tx(ctx)
	if e != nil {
		return automationdomain.Policy{}, e
	}
	p, e := scanPolicy(t.QueryRow(ctx, `UPDATE automation_policies SET current_version_id=$2,version=version+1,updated_by=$4,updated_at=$5 WHERE id=$1 AND version=$3 AND lifecycle='paused' RETURNING `+policyColumns, policyID, versionID, expected, actor, now))
	if errors.Is(e, automationapp.ErrRuntimeNotFound) {
		return p, automationapp.ErrRuntimeConflict
	}
	return p, e
}
func (r *Repository) CurrentPolicyVersion(ctx context.Context, policyID int64) (automationdomain.PolicyVersion, error) {
	t, e := tx(ctx)
	if e != nil {
		return automationdomain.PolicyVersion{}, e
	}
	return scanPolicyVersion(t.QueryRow(ctx, `SELECT `+policyVersionJoinColumns+` FROM automation_policies p JOIN automation_policy_versions v ON v.id=p.current_version_id AND v.policy_id=p.id WHERE p.id=$1`, policyID))
}
func (r *Repository) SetPolicyLifecycle(ctx context.Context, id, expected, actor int64, target automationdomain.PolicyLifecycle, now time.Time) (automationdomain.Policy, error) {
	t, e := tx(ctx)
	if e != nil {
		return automationdomain.Policy{}, e
	}
	p, e := scanPolicy(t.QueryRow(ctx, `UPDATE automation_policies SET lifecycle=$4,version=version+1,updated_by=$3,updated_at=$5::timestamptz,archived_at=CASE WHEN $4='archived' THEN $5::timestamptz ELSE NULL::timestamptz END WHERE id=$1 AND version=$2 AND lifecycle<>'archived' RETURNING `+policyColumns, id, expected, actor, target, now))
	if errors.Is(e, automationapp.ErrRuntimeNotFound) {
		return p, automationapp.ErrRuntimeConflict
	}
	return p, e
}
func (r *Repository) ActivePoliciesForPackage(ctx context.Context, packageID int64) ([]automationdomain.PolicyVersion, error) {
	t, e := tx(ctx)
	if e != nil {
		return nil, e
	}
	rows, e := t.Query(ctx, `SELECT `+policyVersionJoinColumns+` FROM automation_policies p JOIN automation_policy_versions v ON v.id=p.current_version_id AND v.policy_id=p.id WHERE p.lifecycle='active' AND v.package_id=$1 AND v.trigger_enabled ORDER BY p.id`, packageID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []automationdomain.PolicyVersion{}
	for rows.Next() {
		v, e := scanPolicyVersion(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// EnrollmentForSource returns the immutable enrollment frozen for one exact
// policy-version/member-event/customer tuple. It is intentionally a stable
// Automation store read; Config values never participate in this lookup.
func (r *Repository) EnrollmentForSource(ctx context.Context, policyVersionID int64, sourceEventDigest [32]byte, customerID int64) (automationdomain.Enrollment, bool, error) {
	t, err := tx(ctx)
	if err != nil {
		return automationdomain.Enrollment{}, false, err
	}
	var out automationdomain.Enrollment
	var digest, action, source []byte
	var kind string
	err = t.QueryRow(ctx, `SELECT id,policy_id,policy_version_id,source_event_digest,customer_id,action_kind,action_snapshot,action_digest,state,created_at FROM automation_enrollments WHERE policy_version_id=$1 AND source_event_digest=$2 AND customer_id=$3`, policyVersionID, sourceEventDigest[:], customerID).Scan(&out.ID, &out.PolicyID, &out.PolicyVersionID, &source, &out.CustomerID, &kind, &action, &digest, &out.State, &out.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return automationdomain.Enrollment{}, false, nil
	}
	if err != nil || len(source) != 32 || len(digest) != 32 {
		if err != nil {
			return automationdomain.Enrollment{}, false, err
		}
		return automationdomain.Enrollment{}, false, automationapp.ErrRuntimeConflict
	}
	copy(out.SourceEventDigest[:], source)
	copy(out.ActionDigest[:], digest)
	out.ActionKind = automationport.ActionKind(kind)
	out.ActionSnapshot = append([]byte(nil), action...)
	return out, true, nil
}

func (r *Repository) CreateEnrollment(ctx context.Context, e automationdomain.Enrollment) (automationdomain.Enrollment, bool, error) {
	t, err := tx(ctx)
	if err != nil {
		return e, false, err
	}
	var digest, action []byte
	var kind string
	err = t.QueryRow(ctx, `INSERT INTO automation_enrollments(policy_id,policy_version_id,source_event_digest,customer_id,action_kind,action_snapshot,action_digest,state,created_at) VALUES($1,$2,$3,$4,$5,$6::jsonb,$7,$8,$9) ON CONFLICT(policy_version_id,source_event_digest,customer_id) DO NOTHING RETURNING id,action_kind,action_snapshot,action_digest`, e.PolicyID, e.PolicyVersionID, e.SourceEventDigest[:], e.CustomerID, e.ActionKind, e.ActionSnapshot, e.ActionDigest[:], e.State, e.CreatedAt).Scan(&e.ID, &kind, &action, &digest)
	if err == nil {
		e.ActionKind = automationport.ActionKind(kind)
		e.ActionSnapshot = action
		return e, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return e, false, err
	}
	err = t.QueryRow(ctx, `SELECT id,action_kind,action_snapshot,action_digest,state,created_at FROM automation_enrollments WHERE policy_version_id=$1 AND source_event_digest=$2 AND customer_id=$3`, e.PolicyVersionID, e.SourceEventDigest[:], e.CustomerID).Scan(&e.ID, &kind, &action, &digest, &e.State, &e.CreatedAt)
	if err != nil {
		return e, false, err
	}
	if len(digest) != 32 {
		return e, false, automationapp.ErrRuntimeConflict
	}
	copy(e.ActionDigest[:], digest)
	e.ActionKind = automationport.ActionKind(kind)
	e.ActionSnapshot = action
	// The application validates immutable Segment fields after this concurrent
	// re-read. It must not compare the new action digest because that digest
	// intentionally includes the Config revision frozen only on first delivery.
	return e, false, nil
}
func (r *Repository) RuntimeReceipt(ctx context.Context, operation, actorScope string, keyDigest, payloadDigest [32]byte) (automationapp.RuntimeReceipt, bool, error) {
	t, err := tx(ctx)
	if err != nil {
		return automationapp.RuntimeReceipt{}, false, err
	}
	var out automationapp.RuntimeReceipt
	var key, payload []byte
	err = t.QueryRow(ctx, `SELECT id,operation,actor_scope,key_digest,payload_digest,state,result_snapshot FROM automation_runtime_operation_receipts WHERE operation=$1 AND actor_scope=$2 AND key_digest=$3`, operation, actorScope, keyDigest[:]).Scan(&out.ID, &out.Operation, &out.ActorScope, &key, &payload, &out.State, &out.Result)
	if errors.Is(err, pgx.ErrNoRows) {
		return automationapp.RuntimeReceipt{}, false, nil
	}
	if err != nil {
		return automationapp.RuntimeReceipt{}, false, err
	}
	copy(out.KeyDigest[:], key)
	copy(out.PayloadDigest[:], payload)
	if out.PayloadDigest != payloadDigest {
		return automationapp.RuntimeReceipt{}, false, automationapp.ErrRuntimeConflict
	}
	return out, true, nil
}

func (r *Repository) ReserveRuntime(ctx context.Context, in automationapp.RuntimeReservation) (automationapp.RuntimeReceipt, bool, error) {
	t, e := tx(ctx)
	if e != nil {
		return automationapp.RuntimeReceipt{}, false, e
	}
	var id int64
	e = t.QueryRow(ctx, `INSERT INTO automation_runtime_operation_receipts(operation,actor_scope,key_digest,payload_digest,state,created_at) VALUES($1,$2,$3,$4,'reserved',$5) ON CONFLICT(operation,actor_scope,key_digest) DO NOTHING RETURNING id`, in.Operation, in.ActorScope, in.KeyDigest[:], in.PayloadDigest[:], in.CreatedAt).Scan(&id)
	if e == nil {
		return automationapp.RuntimeReceipt{ID: id, Operation: in.Operation, ActorScope: in.ActorScope, State: "reserved", KeyDigest: in.KeyDigest, PayloadDigest: in.PayloadDigest}, true, nil
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return automationapp.RuntimeReceipt{}, false, e
	}
	var out automationapp.RuntimeReceipt
	var key, payload []byte
	e = t.QueryRow(ctx, `SELECT id,operation,actor_scope,key_digest,payload_digest,state,result_snapshot FROM automation_runtime_operation_receipts WHERE operation=$1 AND actor_scope=$2 AND key_digest=$3`, in.Operation, in.ActorScope, in.KeyDigest[:]).Scan(&out.ID, &out.Operation, &out.ActorScope, &key, &payload, &out.State, &out.Result)
	if e != nil {
		return out, false, e
	}
	copy(out.KeyDigest[:], key)
	copy(out.PayloadDigest[:], payload)
	if out.PayloadDigest != in.PayloadDigest {
		return automationapp.RuntimeReceipt{}, false, automationapp.ErrRuntimeConflict
	}
	return out, false, nil
}
func (r *Repository) CompleteRuntime(ctx context.Context, id int64, result json.RawMessage, now time.Time) error {
	t, e := tx(ctx)
	if e != nil {
		return e
	}
	tag, e := t.Exec(ctx, `UPDATE automation_runtime_operation_receipts SET state='completed',result_snapshot=$2::jsonb,completed_at=$3 WHERE id=$1 AND state='reserved'`, id, result, now)
	if e != nil {
		return e
	}
	if tag.RowsAffected() != 1 {
		return automationapp.ErrRuntimeConflict
	}
	return nil
}
func (r *Repository) AppendRuntimeFact(ctx context.Context, f automationapp.RuntimeFact) error {
	if f.ID < 1 || f.Actor < 1 || f.Kind == "" || f.EventType == "" {
		return ErrInvalid
	}
	t, e := tx(ctx)
	if e != nil {
		return e
	}
	payloadDigest := sha256.Sum256(f.Payload)
	keyDigest := sha256.Sum256([]byte(f.Key))
	if _, e = t.Exec(ctx, `INSERT INTO automation_runtime_audit_events(resource_kind,resource_id,operation,actor_id,occurred_at,payload_digest) VALUES($1,$2,$3,$4,$5,$6)`, f.Kind, f.ID, f.Operation, f.Actor, f.At, payloadDigest[:]); e != nil {
		return e
	}
	_, e = t.Exec(ctx, `INSERT INTO automation_runtime_outbox(event_type,aggregate_kind,aggregate_id,payload,idempotency_digest,occurred_at) VALUES($1,$2,$3,$4::jsonb,$5,$6)`, f.EventType, f.Kind, f.ID, f.Payload, keyDigest[:], f.At)
	return e
}
func (r *Repository) CreatePreview(ctx context.Context, p automationdomain.RunPreview) (automationdomain.RunPreview, error) {
	t, e := tx(ctx)
	if e != nil {
		return p, e
	}
	e = t.QueryRow(ctx, `INSERT INTO automation_run_previews(package_id,package_version,snapshot_id,configuration_version_id,agent_id,agent_published_version,binding_version,sender_set_version,target_count,skipped_count,runtime_config_observed,runtime_config_revision,max_recipients_per_run,preview_digest,created_by,created_at,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17) RETURNING id`, p.PackageID, p.PackageVersion, p.SnapshotID, p.ConfigurationVersionID, p.AgentID, p.AgentPublishedVersion, p.BindingVersion, p.SenderSetVersion, p.TargetCount, p.SkippedCount, p.RuntimeConfigObserved, nullableRuntimeRevision(p.RuntimeConfigObserved, p.RuntimeConfigRevision), nullableRuntimeLimit(p.RuntimeConfigObserved, p.MaxRecipientsPerRun), p.PreviewDigest[:], p.CreatedBy, p.CreatedAt, p.ExpiresAt).Scan(&p.ID)
	if unique(e) {
		return p, automationapp.ErrRuntimeConflict
	}
	return p, e
}
func (r *Repository) PreviewByDigest(ctx context.Context, digest [32]byte) (automationdomain.RunPreview, error) {
	t, e := tx(ctx)
	if e != nil {
		return automationdomain.RunPreview{}, e
	}
	var p automationdomain.RunPreview
	var d []byte
	e = t.QueryRow(ctx, `SELECT id,package_id,package_version,snapshot_id,configuration_version_id,agent_id,agent_published_version,binding_version,sender_set_version,target_count,skipped_count,runtime_config_observed,COALESCE(runtime_config_revision,0),COALESCE(max_recipients_per_run,0),preview_digest,created_by,created_at,expires_at FROM automation_run_previews WHERE preview_digest=$1`, digest[:]).Scan(&p.ID, &p.PackageID, &p.PackageVersion, &p.SnapshotID, &p.ConfigurationVersionID, &p.AgentID, &p.AgentPublishedVersion, &p.BindingVersion, &p.SenderSetVersion, &p.TargetCount, &p.SkippedCount, &p.RuntimeConfigObserved, &p.RuntimeConfigRevision, &p.MaxRecipientsPerRun, &d, &p.CreatedBy, &p.CreatedAt, &p.ExpiresAt)
	if errors.Is(e, pgx.ErrNoRows) {
		return p, automationapp.ErrRuntimeNotFound
	}
	copy(p.PreviewDigest[:], d)
	return p, e
}
func (r *Repository) CreateRun(ctx context.Context, run automationdomain.RuntimeRun, recipients []automationdomain.RuntimeRecipient) (automationdomain.RuntimeRun, []automationdomain.RuntimeRecipient, error) {
	t, e := tx(ctx)
	if e != nil {
		return run, nil, e
	}
	e = t.QueryRow(ctx, `INSERT INTO automation_runs(policy_id,policy_version,package_id,package_version,snapshot_id,agent_id,agent_published_version,ai_plan_id,binding_version,sender_set_version,runtime_config_observed,runtime_config_revision,max_recipients_per_run,preview_digest,state,target_count,skipped_count,created_by,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$19) RETURNING id`, nullablePositive(run.PolicyID), nullablePositive(run.PolicyVersion), run.PackageID, run.PackageVersion, run.SnapshotID, run.AgentID, run.AgentPublishedVersion, nullablePositive(run.AIPlanID), run.BindingVersion, run.SenderSetVersion, run.RuntimeConfigObserved, nullableRuntimeRevision(run.RuntimeConfigObserved, run.RuntimeConfigRevision), nullableRuntimeLimit(run.RuntimeConfigObserved, run.MaxRecipientsPerRun), run.PreviewDigest[:], run.State, run.TargetCount, run.SkippedCount, run.CreatedBy, run.CreatedAt).Scan(&run.ID)
	if e != nil {
		return run, nil, e
	}
	for i := range recipients {
		recipients[i].RunID = run.ID
		recipients[i].CreatedAt = run.CreatedAt
		recipients[i].UpdatedAt = run.CreatedAt
		e = t.QueryRow(ctx, `INSERT INTO automation_run_recipients(run_id,customer_id,sender_staff_id,state,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$5) RETURNING id`, run.ID, recipients[i].CustomerID, recipients[i].SenderStaffID, recipients[i].State, run.CreatedAt).Scan(&recipients[i].ID)
		if e != nil {
			return run, nil, e
		}
	}
	return run, recipients, nil
}

func nullablePositive(value int64) any {
	if value > 0 {
		return value
	}
	return nil
}
func nullableRuntimeRevision(observed bool, value int64) any {
	if observed && value >= 0 {
		return value
	}
	return nil
}
func nullableRuntimeLimit(observed bool, value int) any {
	if observed && value >= 1 {
		return value
	}
	return nil
}
func (r *Repository) BindRecipientEffect(ctx context.Context, recipientID int64, effectID string, now time.Time) error {
	t, e := tx(ctx)
	if e != nil {
		return e
	}
	tag, e := t.Exec(ctx, `UPDATE automation_run_recipients SET effect_id=$2,updated_at=$3 WHERE id=$1 AND state='accepted' AND effect_id IS NULL`, recipientID, effectID, now)
	if e != nil {
		return e
	}
	if tag.RowsAffected() != 1 {
		return automationapp.ErrRuntimeConflict
	}
	return nil
}
func (r *Repository) ProjectMessageCompletion(ctx context.Context, completion outboundport.MessageCompletion) error {
	t, e := tx(ctx)
	if e != nil {
		return e
	}
	state := automationport.RecipientFinalFailed
	switch completion.State {
	case outboundport.CompletionProviderAccepted:
		state = automationport.RecipientProviderAccepted
	case outboundport.CompletionDeliveryProven:
		state = automationport.RecipientDeliveryProven
	case outboundport.CompletionRetryableFailed:
		state = automationport.RecipientRetryableFailed
	case outboundport.CompletionFinalFailed:
		state = automationport.RecipientFinalFailed
	case outboundport.CompletionOutcomeUnknown:
		state = automationport.RecipientOutcomeUnknown
	case outboundport.CompletionReconciled:
		state = automationport.RecipientReconciled
	default:
		return automationapp.ErrRuntimeInvalid
	}
	var runID int64
	e = t.QueryRow(ctx, `UPDATE automation_run_recipients SET state=$2,updated_at=clock_timestamp() WHERE effect_id=$1 AND state NOT IN ('delivery_proven','final_failed','cancelled') RETURNING run_id`, completion.EffectID, state).Scan(&runID)
	if errors.Is(e, pgx.ErrNoRows) {
		failureCode := ""
		if state == automationport.RecipientFinalFailed {
			failureCode = "provider_failed"
		} else if state == automationport.RecipientOutcomeUnknown {
			failureCode = "outcome_unknown"
		}
		tag, directErr := t.Exec(ctx, `UPDATE automation_audience_push_items SET send_state=$2,failure_code=CASE WHEN $3='' THEN failure_code ELSE $3 END,updated_at=clock_timestamp() WHERE effect_id=$1 AND send_state NOT IN ('delivery_proven','final_failed','cancelled')`, completion.EffectID, state, failureCode)
		if directErr != nil {
			return directErr
		}
		if tag.RowsAffected() != 1 {
			return automationapp.ErrRuntimeConflict
		}
		return nil
	}
	if e != nil {
		return e
	}
	_, e = t.Exec(ctx, `UPDATE automation_runs SET state=CASE WHEN EXISTS(SELECT 1 FROM automation_run_recipients WHERE run_id=$1 AND state='outcome_unknown') THEN 'outcome_unknown' WHEN EXISTS(SELECT 1 FROM automation_run_recipients WHERE run_id=$1 AND state IN ('accepted','attempted','retryable_failed','provider_accepted')) THEN 'executing' WHEN EXISTS(SELECT 1 FROM automation_run_recipients WHERE run_id=$1 AND state IN ('final_failed','skipped')) OR EXISTS(SELECT 1 FROM automation_run_reconciliations WHERE run_id=$1 AND resolution='final_failed') THEN 'partial_failed' ELSE 'completed' END,updated_at=clock_timestamp(),completed_at=CASE WHEN NOT EXISTS(SELECT 1 FROM automation_run_recipients WHERE run_id=$1 AND state IN ('accepted','attempted','retryable_failed','provider_accepted','outcome_unknown')) THEN clock_timestamp() ELSE NULL END WHERE id=$1`, runID)
	return e
}

const runColumns = `id,COALESCE(policy_id,0),COALESCE(policy_version,0),package_id,package_version,snapshot_id,agent_id,agent_published_version,COALESCE(ai_plan_id,0),binding_version,sender_set_version,runtime_config_observed,COALESCE(runtime_config_revision,0),COALESCE(max_recipients_per_run,0),preview_digest,state,target_count,skipped_count,(SELECT count(*) FROM automation_run_recipients unknown_recipient WHERE unknown_recipient.run_id=automation_runs.id AND unknown_recipient.state='outcome_unknown'),created_by,created_at,updated_at,completed_at`

func scanRun(row pgx.Row) (automationdomain.RuntimeRun, error) {
	var out automationdomain.RuntimeRun
	var digest []byte
	var state string
	var completed *time.Time
	e := row.Scan(&out.ID, &out.PolicyID, &out.PolicyVersion, &out.PackageID, &out.PackageVersion, &out.SnapshotID, &out.AgentID, &out.AgentPublishedVersion, &out.AIPlanID, &out.BindingVersion, &out.SenderSetVersion, &out.RuntimeConfigObserved, &out.RuntimeConfigRevision, &out.MaxRecipientsPerRun, &digest, &state, &out.TargetCount, &out.SkippedCount, &out.OutcomeUnknownCount, &out.CreatedBy, &out.CreatedAt, &out.UpdatedAt, &completed)
	if errors.Is(e, pgx.ErrNoRows) {
		return out, automationapp.ErrRuntimeNotFound
	}
	if e != nil {
		return out, e
	}
	if len(digest) != 32 {
		return out, automationapp.ErrRuntimeConflict
	}
	copy(out.PreviewDigest[:], digest)
	out.State = automationport.RunState(state)
	return out, nil
}
func (r *Repository) ListRuns(ctx context.Context, cursor int64, limit int) ([]automationdomain.RuntimeRun, string, error) {
	t, e := tx(ctx)
	if e != nil {
		return nil, "", e
	}
	query := `SELECT ` + runColumns + ` FROM automation_runs WHERE ($1=0 OR id<$1) ORDER BY id DESC LIMIT $2`
	rows, e := t.Query(ctx, query, cursor, limit+1)
	if e != nil {
		return nil, "", e
	}
	defer rows.Close()
	out := []automationdomain.RuntimeRun{}
	for rows.Next() {
		item, scanErr := scanRun(rows)
		if scanErr != nil {
			return nil, "", scanErr
		}
		out = append(out, item)
	}
	if e = rows.Err(); e != nil {
		return nil, "", e
	}
	next := ""
	if len(out) > limit {
		next = strconv.FormatInt(out[limit-1].ID, 10)
		out = out[:limit]
	}
	return out, next, nil
}
func (r *Repository) Run(ctx context.Context, id int64) (automationdomain.RuntimeRun, error) {
	t, e := tx(ctx)
	if e != nil {
		return automationdomain.RuntimeRun{}, e
	}
	return scanRun(t.QueryRow(ctx, `SELECT `+runColumns+` FROM automation_runs WHERE id=$1`, id))
}
func (r *Repository) RunRecipients(ctx context.Context, runID, cursor int64, limit int) ([]automationdomain.RuntimeRecipient, string, error) {
	t, e := tx(ctx)
	if e != nil {
		return nil, "", e
	}
	var exists bool
	if e = t.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM automation_runs WHERE id=$1)`, runID).Scan(&exists); e != nil {
		return nil, "", e
	}
	if !exists {
		return nil, "", automationapp.ErrRuntimeNotFound
	}
	rows, e := t.Query(ctx, `SELECT id,run_id,customer_id,sender_staff_id,state,COALESCE(skip_code,''),COALESCE(effect_id,''),created_at,updated_at FROM automation_run_recipients WHERE run_id=$1 AND ($2=0 OR id>$2) ORDER BY id LIMIT $3`, runID, cursor, limit+1)
	if e != nil {
		return nil, "", e
	}
	defer rows.Close()
	out := []automationdomain.RuntimeRecipient{}
	for rows.Next() {
		var x automationdomain.RuntimeRecipient
		var state string
		if e = rows.Scan(&x.ID, &x.RunID, &x.CustomerID, &x.SenderStaffID, &state, &x.SkipCode, &x.EffectID, &x.CreatedAt, &x.UpdatedAt); e != nil {
			return nil, "", e
		}
		x.State = automationport.RecipientState(state)
		out = append(out, x)
	}
	if e = rows.Err(); e != nil {
		return nil, "", e
	}
	next := ""
	if len(out) > limit {
		next = strconv.FormatInt(out[limit-1].ID, 10)
		out = out[:limit]
	}
	return out, next, nil
}

func (r *Repository) RecipientForEffect(ctx context.Context, runID int64, effectID string) (automationdomain.RuntimeRecipient, error) {
	t, e := tx(ctx)
	if e != nil {
		return automationdomain.RuntimeRecipient{}, e
	}
	var out automationdomain.RuntimeRecipient
	var state string
	e = t.QueryRow(ctx, `SELECT id,run_id,customer_id,sender_staff_id,state,COALESCE(skip_code,''),COALESCE(effect_id,''),created_at,updated_at FROM automation_run_recipients WHERE run_id=$1 AND effect_id=$2 FOR UPDATE`, runID, effectID).Scan(&out.ID, &out.RunID, &out.CustomerID, &out.SenderStaffID, &state, &out.SkipCode, &out.EffectID, &out.CreatedAt, &out.UpdatedAt)
	if errors.Is(e, pgx.ErrNoRows) {
		return out, automationapp.ErrRuntimeNotFound
	}
	out.State = automationport.RecipientState(state)
	return out, e
}

func (r *Repository) CreateRunReconciliation(ctx context.Context, record automationdomain.RunReconciliation) (automationdomain.RunReconciliation, error) {
	t, e := tx(ctx)
	if e != nil {
		return record, e
	}
	e = t.QueryRow(ctx, `INSERT INTO automation_run_reconciliations(run_id,recipient_id,effect_id,generation,fence,lease_expires_at,evidence_digest,resolution,actor_id,receipt_key_digest,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING id`, record.RunID, record.RecipientID, record.EffectID, record.Generation, record.Fence, record.LeaseExpiresAt, record.EvidenceDigest[:], record.Resolution, record.ActorID, record.ReceiptDigest[:], record.CreatedAt).Scan(&record.ID)
	if unique(e) {
		return record, automationapp.ErrRuntimeConflict
	}
	return record, e
}
func (r *Repository) CancelRun(ctx context.Context, id int64, now time.Time) (automationdomain.RuntimeRun, error) {
	t, e := tx(ctx)
	if e != nil {
		return automationdomain.RuntimeRun{}, e
	}
	current, e := scanRun(t.QueryRow(ctx, `SELECT `+runColumns+` FROM automation_runs WHERE id=$1 FOR UPDATE`, id))
	if e != nil {
		return current, e
	}
	if current.State == automationport.RunCompleted || current.State == automationport.RunCancelled {
		return current, automationapp.ErrRuntimeConflict
	}
	if _, e = t.Exec(ctx, `UPDATE automation_run_recipients SET state='cancelled',updated_at=$2 WHERE run_id=$1 AND state='accepted'`, id, now); e != nil {
		return current, e
	}
	out, e := scanRun(t.QueryRow(ctx, `UPDATE automation_runs SET state='cancelled',updated_at=$2,completed_at=$2 WHERE id=$1 RETURNING `+runColumns, id, now))
	return out, e
}
