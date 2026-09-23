package adminops

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	opsport "github.com/qianlan33333-png/AI-CRM-v3/internal/adminops/port"
	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"time"
)

const retentionVersion = "process-720h-v1"

type RetentionPolicy struct {
	ID        string `json:"id"`
	Resource  string `json:"resource"`
	Retention string `json:"retention"`
	Status    string `json:"status"`
}
type RetentionPreview struct {
	Policy                string    `json:"policy"`
	Cutoff                time.Time `json:"cutoff"`
	Candidates            int64     `json:"candidates"`
	EstimatedPayloadBytes int64     `json:"estimated_payload_bytes"`
	HasMore               bool      `json:"has_more"`
	ProtectedReason       string    `json:"protected_reason"`
}
type RetentionRun struct {
	Policy       string     `json:"policy"`
	Cutoff       time.Time  `json:"cutoff"`
	State        string     `json:"state"`
	Deleted      int64      `json:"deleted_rows"`
	PayloadBytes int64      `json:"payload_bytes"`
	CompletedAt  *time.Time `json:"completed_at"`
}
type RetentionService struct {
	pool      *pgxpool.Pool
	media     mediaport.TransactionalUploadPartRetention
	enabled   bool
	process   map[string]opsport.ProcessRetention
	snapshots opsport.SnapshotRetention
	host      platformport.HostMaintenanceReader
	release   platformport.ReleaseMaintenanceReader
	now       func() time.Time
}

func NewRetentionService(pool *pgxpool.Pool, media mediaport.TransactionalUploadPartRetention, snapshots opsport.SnapshotRetention, enabled bool) (*RetentionService, error) {
	if pool == nil || media == nil || snapshots == nil {
		return nil, errors.New("retention dependencies required")
	}
	return &RetentionService{pool: pool, media: media, enabled: enabled, now: time.Now, snapshots: snapshots, process: map[string]opsport.ProcessRetention{}}, nil
}
func (s *RetentionService) Policies() []RetentionPolicy {
	state := "preview_only"
	if s.enabled {
		state = "enabled"
	}
	policies := []RetentionPolicy{
		{"ops_snapshots", "诊断采样快照；发布业务依据保护", "720 小时", state}, {"ops_results", "巡查检查明细", "720 小时", state}, {"ops_runs", "巡查过程记录；陈旧未完成明细同样到期", "720 小时", state}, {"ops_diagnostics", "脱敏排障事件", "720 小时", state}, {"ops_report_payloads", "小时报告内容；接受凭据永久保护", "720 小时", state}, {"media_upload_parts", "终态或失效上传分片；正式素材与收据保护", "720 小时", state}, {"ops_retention_history", "清理过程明细", "720 小时", state},
		{"host_process_files", "宿主过程文件与独立日志namespace", "720 小时", "host_native"}, {"release_artifacts", "当前 + 两个验证回滚版本 + 在用版本", "按兼容性及引用保护", "verified_release_allowlist"},
		{"river_terminal", "River 完成、取消、终止任务", "720 小时", "native_cleaner"},
		{"business_facts", "订单、问卷、商品、客户、幂等与效果依据", "永久", "protected"},
	}
	for _, p := range []RetentionPolicy{{"config_usage", "配置使用过程观测；冻结业务决策保留", "720 小时", state}, {"archive_sync_runs", "会话存档终态同步过程；消息与游标保留", "结束后720小时", state}} {
		if s.process[p.ID] != nil {
			policies = append(policies, p)
		}
	}
	return policies
}
func (s *RetentionService) BindProcessPolicy(id string, owner opsport.ProcessRetention) error {
	if (id != "config_usage" && id != "archive_sync_runs") || owner == nil || s.process[id] != nil {
		return ErrInspectionInvalid
	}
	if s.process == nil {
		s.process = map[string]opsport.ProcessRetention{}
	}
	s.process[id] = owner
	return nil
}
func validRetentionPolicy(id string) bool {
	switch id {
	case "config_usage", "archive_sync_runs", "ops_snapshots", "ops_results", "ops_runs", "ops_diagnostics", "ops_report_payloads", "media_upload_parts", "ops_retention_history":
		return true
	}
	return false
}
func (s *RetentionService) PreviewRetention(ctx context.Context, id string) (RetentionPreview, error) {
	out := RetentionPreview{Policy: id, Cutoff: s.now().UTC().Add(-30 * 24 * time.Hour), ProtectedReason: "业务事实、接受摘要和在途状态不参与清理"}
	if !validRetentionPolicy(id) {
		return out, ErrInspectionInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	if id == "config_usage" || id == "archive_sync_runs" {
		owner := s.process[id]
		if owner == nil {
			return out, ErrInspectionNotFound
		}
		tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
		if err != nil {
			return out, err
		}
		defer tx.Rollback(ctx)
		v, err := owner.CleanupProcessDetailWithin(platformpostgres.BindTransaction(ctx, tx), opsport.ProcessRetentionCommand{Before: out.Cutoff, Limit: 1000})
		out.Candidates, out.EstimatedPayloadBytes, out.HasMore = v.Candidates, v.Bytes, v.Remaining
		return out, err
	}
	if id == "ops_snapshots" {
		tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
		if err != nil {
			return out, err
		}
		defer tx.Rollback(ctx)
		v, err := s.snapshots.CleanupDiagnosticSnapshotsWithin(platformpostgres.BindTransaction(ctx, tx), opsport.SnapshotRetentionCommand{Before: out.Cutoff, Limit: 1000})
		out.Candidates, out.EstimatedPayloadBytes, out.HasMore = v.Candidates, v.Bytes, v.Remaining
		return out, err
	}
	if id == "media_upload_parts" {
		v, e := s.media.CleanupExpiredUploadParts(ctx, mediaport.UploadPartRetentionCommand{Before: out.Cutoff, Limit: 1000, Apply: false})
		out.Candidates = v.Candidates
		out.EstimatedPayloadBytes = v.Bytes
		out.HasMore = v.Remaining
		return out, e
	}
	var query string
	switch id {
	case "ops_results":
		query = `SELECT count(*),coalesce(sum((coalesce(pg_column_size(r.metrics),0))),0) FROM adminops_inspection_results r WHERE observed_at<$1`
	case "ops_runs":
		query = `SELECT count(*),coalesce(sum((coalesce(pg_column_size(r.id),0)+coalesce(pg_column_size(r.request_key),0)+coalesce(pg_column_size(r.slot),0)+coalesce(pg_column_size(r.state),0)+coalesce(pg_column_size(r.release_sha),0)+coalesce(pg_column_size(r.started_at),0)+coalesce(pg_column_size(r.completed_at),0))),0) FROM adminops_inspection_runs r WHERE started_at<$1 AND NOT EXISTS(SELECT 1 FROM adminops_inspection_results x WHERE x.run_id=r.id)`
	case "ops_diagnostics":
		query = `SELECT count(*),coalesce(sum((coalesce(pg_column_size(r.id),0)+coalesce(pg_column_size(r.component),0)+coalesce(pg_column_size(r.release_sha),0)+coalesce(pg_column_size(r.code),0)+coalesce(pg_column_size(r.correlation_digest),0)+coalesce(pg_column_size(r.route_template),0)+coalesce(pg_column_size(r.job_ref),0)+coalesce(pg_column_size(r.effect_ref),0)+coalesce(pg_column_size(r.job_attempt),0)+coalesce(pg_column_size(r.occurred_at),0))),0) FROM adminops_diagnostic_events r WHERE occurred_at<$1`
	case "ops_report_payloads":
		query = `SELECT count(*),coalesce(sum(octet_length(content_bytes)::bigint+pg_column_size(content)),0) FROM adminops_inspection_reports WHERE created_at<$1 AND payload_pruned_at IS NULL`
	case "ops_retention_history":
		query = `SELECT count(*),coalesce(sum((coalesce(pg_column_size(r.id),0)+coalesce(pg_column_size(r.hour_key),0)+coalesce(pg_column_size(r.policy),0)+coalesce(pg_column_size(r.policy_version),0)+coalesce(pg_column_size(r.cutoff),0)+coalesce(pg_column_size(r.state),0)+coalesce(pg_column_size(r.deleted_rows),0)+coalesce(pg_column_size(r.payload_bytes),0)+coalesce(pg_column_size(r.started_at),0)+coalesce(pg_column_size(r.completed_at),0)+coalesce(pg_column_size(r.failure_code),0))),0) FROM adminops_retention_runs r WHERE COALESCE(completed_at,started_at)<$1`
	}
	err := s.pool.QueryRow(ctx, query, out.Cutoff).Scan(&out.Candidates, &out.EstimatedPayloadBytes)
	return out, err
}

// PruneRetentionBatch uses a fixed server-owned policy and server clock. No
// caller-supplied table, path, SQL, or retention period reaches execution.
func (s *RetentionService) PruneRetentionBatch(ctx context.Context, id string) (RetentionRun, error) {
	out := RetentionRun{Policy: id, Cutoff: s.now().UTC().Add(-30 * 24 * time.Hour), State: "running"}
	if !s.enabled || !validRetentionPolicy(id) {
		return out, ErrInspectionInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SET LOCAL lock_timeout='250ms'; SET LOCAL statement_timeout='3s'`); err != nil {
		return out, err
	}
	var locked bool
	if err = tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(824194020)`).Scan(&locked); err != nil {
		return out, err
	}
	if !locked {
		return out, ErrInspectionConflict
	}
	hour := s.now().UTC().Truncate(time.Hour)
	var runID int64
	err = tx.QueryRow(ctx, `INSERT INTO adminops_retention_runs(hour_key,policy,policy_version,cutoff,state) VALUES($1,$2,$3,$4,'running') ON CONFLICT(hour_key,policy) DO UPDATE SET state='running',failure_code='' WHERE adminops_retention_runs.state<>'completed' RETURNING id,cutoff`, hour, id, retentionVersion, out.Cutoff).Scan(&runID, &out.Cutoff)
	if errors.Is(err, pgx.ErrNoRows) {
		out.State = "completed"
		return out, nil
	}
	if err != nil {
		return out, err
	}
	pending := false
	if id == "config_usage" || id == "archive_sync_runs" {
		owner := s.process[id]
		if owner == nil {
			err = ErrInspectionNotFound
		} else {
			v, e := owner.CleanupProcessDetailWithin(platformpostgres.BindTransaction(ctx, tx), opsport.ProcessRetentionCommand{Before: out.Cutoff, Limit: 1000, Apply: true})
			out.Deleted, out.PayloadBytes, pending, err = v.Deleted, v.Bytes, v.Remaining, e
		}
	} else if id == "media_upload_parts" {
		v, e := s.media.CleanupExpiredUploadPartsWithin(platformpostgres.BindTransaction(ctx, tx), mediaport.UploadPartRetentionCommand{Before: out.Cutoff, Limit: 1000, Apply: true})
		out.Deleted = v.Deleted
		out.PayloadBytes = v.Bytes
		pending = v.Remaining
		err = e
	} else if id == "ops_snapshots" {
		v, e := s.snapshots.CleanupDiagnosticSnapshotsWithin(platformpostgres.BindTransaction(ctx, tx), opsport.SnapshotRetentionCommand{Before: out.Cutoff, Limit: 1000, Apply: true})
		out.Deleted, out.PayloadBytes, pending, err = v.Deleted, v.Bytes, v.Remaining, e
	} else {
		out.Deleted, out.PayloadBytes, err = s.pruneOwn(ctx, tx, id, out.Cutoff)
		pending = out.Deleted >= 1000
	}
	if err != nil {
		_ = tx.Rollback(ctx)
		out.State = "failed"
		recordCtx, c := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer c()
		_, _ = s.pool.Exec(recordCtx, `INSERT INTO adminops_retention_runs(hour_key,policy,policy_version,cutoff,state,completed_at,failure_code) VALUES($1,$2,$3,$4,'failed',clock_timestamp(),'batch_failed') ON CONFLICT(hour_key,policy) DO UPDATE SET state='failed',completed_at=clock_timestamp(),failure_code='batch_failed' WHERE adminops_retention_runs.state<>'completed'`, hour, id, retentionVersion, out.Cutoff)
		return out, err
	}
	out.State = "completed"
	if pending {
		out.State = "running"
	}
	if _, err = tx.Exec(ctx, `UPDATE adminops_retention_runs SET state=$2,deleted_rows=deleted_rows+$3,payload_bytes=payload_bytes+$4,completed_at=CASE WHEN $2='running' THEN NULL ELSE clock_timestamp() END,failure_code='' WHERE id=$1`, runID, out.State, out.Deleted, out.PayloadBytes); err != nil {
		return out, err
	}
	if err = tx.Commit(ctx); err != nil {
		return out, err
	}
	return out, nil
}

// PayloadBytes sums removed value sizes column by column (metrics only for
// results, content columns only for permanent reports). Whole-row tuple size
// varies when PostgreSQL rebuilds a tuple, so it is not a stable payload metric.
// This excludes tuple/index overhead and does not claim filesystem reclamation.
func (s *RetentionService) pruneOwn(ctx context.Context, tx pgx.Tx, id string, cutoff time.Time) (int64, int64, error) {
	var query string
	switch id {
	case "ops_results":
		query = `WITH candidates AS(SELECT run_id,check_id,(coalesce(pg_column_size(r.metrics),0))::bigint AS bytes FROM adminops_inspection_results r WHERE observed_at<$1 ORDER BY observed_at LIMIT 1000 FOR UPDATE SKIP LOCKED), changed AS(DELETE FROM adminops_inspection_results r USING candidates c WHERE r.run_id=c.run_id AND r.check_id=c.check_id AND r.observed_at<$1 RETURNING c.bytes) SELECT count(*),coalesce(sum(bytes),0)::bigint FROM changed`
	case "ops_runs":
		query = `WITH candidates AS(SELECT id,(coalesce(pg_column_size(r.id),0)+coalesce(pg_column_size(r.request_key),0)+coalesce(pg_column_size(r.slot),0)+coalesce(pg_column_size(r.state),0)+coalesce(pg_column_size(r.release_sha),0)+coalesce(pg_column_size(r.started_at),0)+coalesce(pg_column_size(r.completed_at),0))::bigint AS bytes FROM adminops_inspection_runs r WHERE started_at<$1 AND NOT EXISTS(SELECT 1 FROM adminops_inspection_results x WHERE x.run_id=r.id) ORDER BY id LIMIT 1000 FOR UPDATE SKIP LOCKED), changed AS(DELETE FROM adminops_inspection_runs r USING candidates c WHERE r.id=c.id AND r.started_at<$1 AND NOT EXISTS(SELECT 1 FROM adminops_inspection_results x WHERE x.run_id=r.id) RETURNING c.bytes) SELECT count(*),coalesce(sum(bytes),0)::bigint FROM changed`
	case "ops_diagnostics":
		query = `WITH candidates AS(SELECT id,(coalesce(pg_column_size(r.id),0)+coalesce(pg_column_size(r.component),0)+coalesce(pg_column_size(r.release_sha),0)+coalesce(pg_column_size(r.code),0)+coalesce(pg_column_size(r.correlation_digest),0)+coalesce(pg_column_size(r.route_template),0)+coalesce(pg_column_size(r.job_ref),0)+coalesce(pg_column_size(r.effect_ref),0)+coalesce(pg_column_size(r.job_attempt),0)+coalesce(pg_column_size(r.occurred_at),0))::bigint AS bytes FROM adminops_diagnostic_events r WHERE occurred_at<$1 ORDER BY id LIMIT 1000 FOR UPDATE SKIP LOCKED), changed AS(DELETE FROM adminops_diagnostic_events r USING candidates c WHERE r.id=c.id AND r.occurred_at<$1 RETURNING c.bytes) SELECT count(*),coalesce(sum(bytes),0)::bigint FROM changed`
	case "ops_report_payloads":
		query = `WITH candidates AS(SELECT id,octet_length(content_bytes)::bigint+pg_column_size(content) AS bytes FROM adminops_inspection_reports WHERE created_at<$1 AND payload_pruned_at IS NULL ORDER BY id LIMIT 1000 FOR UPDATE SKIP LOCKED), changed AS(UPDATE adminops_inspection_reports r SET content=NULL,content_bytes=NULL,payload_pruned_at=clock_timestamp() FROM candidates c WHERE r.id=c.id AND r.created_at<$1 AND r.payload_pruned_at IS NULL RETURNING c.bytes) SELECT count(*),coalesce(sum(bytes),0)::bigint FROM changed`
	case "ops_retention_history":
		query = `WITH candidates AS(SELECT id,(coalesce(pg_column_size(r.id),0)+coalesce(pg_column_size(r.hour_key),0)+coalesce(pg_column_size(r.policy),0)+coalesce(pg_column_size(r.policy_version),0)+coalesce(pg_column_size(r.cutoff),0)+coalesce(pg_column_size(r.state),0)+coalesce(pg_column_size(r.deleted_rows),0)+coalesce(pg_column_size(r.payload_bytes),0)+coalesce(pg_column_size(r.started_at),0)+coalesce(pg_column_size(r.completed_at),0)+coalesce(pg_column_size(r.failure_code),0))::bigint AS bytes FROM adminops_retention_runs r WHERE COALESCE(completed_at,started_at)<$1 ORDER BY id LIMIT 1000 FOR UPDATE SKIP LOCKED), changed AS(DELETE FROM adminops_retention_runs r USING candidates c WHERE r.id=c.id AND COALESCE(r.completed_at,r.started_at)<$1 RETURNING c.bytes) SELECT count(*),coalesce(sum(bytes),0)::bigint FROM changed`
	default:
		return 0, 0, ErrInspectionInvalid
	}
	var rows, bytes int64
	err := tx.QueryRow(ctx, query, cutoff).Scan(&rows, &bytes)
	return rows, bytes, err
}
func (s *RetentionService) Runs(ctx context.Context) ([]RetentionRun, error) {
	rows, e := s.pool.Query(ctx, `SELECT policy,cutoff,state,deleted_rows,payload_bytes,completed_at FROM adminops_retention_runs WHERE started_at>=clock_timestamp()-interval '720 hours' ORDER BY id DESC LIMIT 100`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []RetentionRun{}
	for rows.Next() {
		var r RetentionRun
		if e = rows.Scan(&r.Policy, &r.Cutoff, &r.State, &r.Deleted, &r.PayloadBytes, &r.CompletedAt); e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *RetentionService) Readiness(ctx context.Context) error {
	var table string
	return s.pool.QueryRow(ctx, `SELECT 'adminops_retention_runs'::regclass::text`).Scan(&table)
}

func (s *RetentionService) BindHostReader(host platformport.HostMaintenanceReader) error {
	if host == nil || s.host != nil {
		return ErrInspectionInvalid
	}
	s.host = host
	return nil
}
func (s *RetentionService) HostLatest(ctx context.Context) map[string]any {
	if s.host == nil {
		return map[string]any{"status": "uncovered", "code": "host_retention_not_configured"}
	}
	report, err := s.host.ReadLatest(ctx)
	if err != nil && !errors.Is(err, platformport.ErrHostMaintenancePartial) {
		return map[string]any{"status": "unknown", "code": "host_retention_evidence_unavailable"}
	}
	return map[string]any{"status": report.State, "started_at": report.StartedAt, "finished_at": report.FinishedAt, "runtime": report.Runtime, "journal": report.Journal, "failure_codes": report.FailureCodes, "space_observations": report.SpaceObservations}
}

func (s *RetentionService) BindReleaseReader(reader platformport.ReleaseMaintenanceReader) error {
	if reader == nil || s.release != nil {
		return ErrInspectionInvalid
	}
	s.release = reader
	return nil
}
func (s *RetentionService) ReleaseLatest(ctx context.Context) map[string]any {
	if s.release == nil {
		return map[string]any{"status": "unknown", "code": "release_retention_not_configured"}
	}
	report, err := s.release.ReadReleaseLatest(ctx)
	if err != nil && !errors.Is(err, platformport.ErrHostMaintenancePartial) {
		return map[string]any{"status": "unknown", "code": "release_retention_evidence_unavailable"}
	}
	return map[string]any{"status": report.State, "state": report.State, "reason": report.Reason, "release_sha": report.ReleaseSHA, "observed_at": report.ObservedAt, "deleted_count": report.DeletedCount, "deleted_bytes": report.DeletedBytes, "confirmed_deleted_count": report.ConfirmedDeletedCount, "confirmed_deleted_bytes": report.ConfirmedDeletedBytes, "rollback_count": report.RollbackCount, "space_observations": report.SpaceObservations}
}
