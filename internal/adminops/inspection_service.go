package adminops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	opsport "github.com/qianlan33333-png/AI-CRM-v3/internal/adminops/port"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

var (
	ErrInspectionInvalid  = errors.New("invalid inspection request")
	ErrInspectionConflict = errors.New("inspection version or idempotency conflict")
	ErrInspectionNotFound = errors.New("inspection record not found")
)

const InspectionQueue = "adminops_inspections"

type InspectionOptions struct {
	ReleaseSHA            string
	NotificationTargetRef string
	NotificationEnabled   bool
	DetailURL             string
	Now                   func() time.Time
	WindowReader          effectport.OpsWindowReader
}
type InspectionService struct {
	pool       *pgxpool.Pool
	uow        platformport.UnitOfWork
	collectors map[string]opsport.InspectionCollector
	effects    effectport.TransactionalAccepter
	options    InspectionOptions
	now        func() time.Time
}

func NewInspectionService(pool *pgxpool.Pool, uow platformport.UnitOfWork, collectors []opsport.InspectionCollector, effects effectport.TransactionalAccepter, options InspectionOptions) (*InspectionService, error) {
	if pool == nil || uow == nil || options.ReleaseSHA == "" || len(options.ReleaseSHA) > 200 {
		return nil, ErrInspectionInvalid
	}
	if options.NotificationEnabled && (effects == nil || options.NotificationTargetRef == "" || strings.Contains(options.NotificationTargetRef, "://")) {
		return nil, ErrInspectionInvalid
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	s := &InspectionService{pool: pool, uow: uow, collectors: map[string]opsport.InspectionCollector{}, effects: effects, options: options, now: now}
	valid := map[string]bool{}
	for _, d := range InspectionCatalog() {
		valid[d.ID] = true
	}
	for _, c := range collectors {
		if c == nil || !valid[c.CheckID()] || s.collectors[c.CheckID()] != nil {
			return nil, ErrInspectionInvalid
		}
		s.collectors[c.CheckID()] = c
	}
	if s.collectors["inspection.self"] == nil {
		s.collectors["inspection.self"] = opsport.CollectorFunc{ID: "inspection.self", Read: s.collectSelfHealth}
	}
	if s.collectors["diagnostics.application_errors"] == nil {
		s.collectors["diagnostics.application_errors"] = opsport.CollectorFunc{ID: "diagnostics.application_errors", Read: s.collectErrors}
	}
	return s, nil
}
func (s *InspectionService) Readiness(ctx context.Context) error {
	var ready bool
	err := s.pool.QueryRow(ctx, `SELECT NOT EXISTS(SELECT 1 FROM unnest(ARRAY['adminops_inspection_commands','adminops_inspection_runs','adminops_inspection_results','adminops_inspection_issues','adminops_inspection_reports','adminops_diagnostic_events']) n WHERE to_regclass(current_schema()||'.'||n) IS NULL)`).Scan(&ready)
	if err != nil {
		return err
	}
	if !ready {
		return ErrInspectionNotFound
	}
	return nil
}
func (s *InspectionService) Scan(ctx context.Context, slot time.Time) (opsport.InspectionRun, error) {
	if slot.IsZero() {
		slot = s.now()
	}
	slot = slot.UTC().Truncate(5 * time.Minute)
	return s.scan(ctx, "scheduled:"+slot.Format(time.RFC3339), slot)
}
func (s *InspectionService) ManualScan(ctx context.Context, key string) (opsport.InspectionRun, error) {
	digest, err := manualInspectionDigest(key)
	if err != nil {
		return opsport.InspectionRun{}, err
	}
	return s.scan(ctx, digest, s.now().UTC())
}
func (s *InspectionService) scan(ctx context.Context, key string, slot time.Time) (opsport.InspectionRun, error) {
	var run opsport.InspectionRun
	now := s.now().UTC()
	// The atomic upsert locks the run before refreshing a resumed execution.
	// Retention locks the same row and rechecks started_at before deleting;
	// abandoned running details still expire rather than becoming permanent.
	err := s.pool.QueryRow(ctx, `INSERT INTO adminops_inspection_runs(request_key,slot,state,release_sha,started_at) VALUES($1,$2,'running',$3,$4)
 ON CONFLICT(request_key) DO UPDATE SET
 started_at=CASE WHEN adminops_inspection_runs.state='running' THEN EXCLUDED.started_at ELSE adminops_inspection_runs.started_at END,
 release_sha=CASE WHEN adminops_inspection_runs.state='running' THEN EXCLUDED.release_sha ELSE adminops_inspection_runs.release_sha END
 RETURNING id,request_key,slot,state,release_sha,started_at,completed_at`, key, slot, s.options.ReleaseSHA, now).Scan(&run.ID, &run.RequestKey, &run.Slot, &run.State, &run.ReleaseSHA, &run.StartedAt, &run.CompletedAt)
	if err != nil {
		return run, err
	}
	if run.State != "running" {
		if err = s.uow.Within(ctx, func(txctx context.Context) error {
			tx, e := platformpostgres.RequireTransaction(txctx)
			if e != nil {
				return e
			}
			return completeManualInspectionWithin(txctx, tx, key, run.ID, s.now().UTC())
		}); err != nil {
			return run, err
		}
		return s.Run(ctx, run.ID)
	}
	results := make([]opsport.CheckResult, 0, len(InspectionCatalog()))
	for _, def := range InspectionCatalog() {
		o := opsport.CheckObservation{Status: "uncovered", Code: "collector_not_configured", ObservedAt: now, Metrics: map[string]int64{}}
		if c := s.collectors[def.ID]; c != nil {
			bounded, cancel := context.WithTimeout(ctx, 3*time.Second)
			read, readErr := c.Collect(bounded, now)
			cancel()
			if readErr != nil {
				o.Status = "unknown"
				o.Code = "source_unavailable"
			} else {
				o = normalizeObservation(read, def, now)
			}
		}
		results = append(results, opsport.CheckResult{CheckDefinition: def, CheckObservation: o, RunID: run.ID})
	}
	if ctx.Err() != nil {
		return run, ctx.Err()
	}
	err = s.uow.Within(ctx, func(txctx context.Context) error {
		tx, e := platformpostgres.RequireTransaction(txctx)
		if e != nil {
			return e
		}
		// Completion may arrive out of order. Serialize only local writes and
		// preserve the lifecycle decision from a newer completed observation.
		if _, e = tx.Exec(txctx, `SELECT pg_advisory_xact_lock(hashtextextended('adminops-inspection-completion',0))`); e != nil {
			return e
		}
		var superseded bool
		if e = tx.QueryRow(txctx, `SELECT EXISTS(SELECT 1 FROM adminops_inspection_runs WHERE state<>'running' AND (started_at,id)>($1,$2))`, run.StartedAt, run.ID).Scan(&superseded); e != nil {
			return e
		}
		var state string
		if e = tx.QueryRow(txctx, `SELECT state FROM adminops_inspection_runs WHERE id=$1 FOR UPDATE`, run.ID).Scan(&state); e != nil {
			return e
		}
		if state != "running" {
			return completeManualInspectionWithin(txctx, tx, key, run.ID, s.now().UTC())
		}
		partial := false
		var critical, recovery []inspectionTransition
		for _, r := range results {
			metrics, _ := json.Marshal(r.Metrics)
			if _, e = tx.Exec(txctx, `INSERT INTO adminops_inspection_results(run_id,check_id,owner,status,code,observed_at,metrics) VALUES($1,$2,$3,$4,$5,$6,$7)`, run.ID, r.ID, r.Owner, r.Status, r.Code, r.ObservedAt, metrics); e != nil {
				return e
			}
			if r.Status == "unknown" || r.Status == "uncovered" || r.Status == "stale" {
				partial = true
			}
			if superseded {
				continue
			}
			opened, recovered, issueErr := recordInspectionIssue(txctx, tx, r, now)
			if issueErr != nil {
				return issueErr
			}
			if opened != nil {
				critical = append(critical, *opened)
			}
			if recovered != nil {
				recovery = append(recovery, *recovered)
			}
		}
		state = "completed"
		if partial {
			state = "partial_failed"
		}
		_, e = tx.Exec(txctx, `UPDATE adminops_inspection_runs SET state=$2,completed_at=$3 WHERE id=$1`, run.ID, state, s.now().UTC())
		if e != nil {
			return e
		}
		for _, notice := range []struct {
			kind    string
			changes []inspectionTransition
		}{{"critical", critical}, {"recovery", recovery}} {
			if len(notice.changes) == 0 {
				continue
			}
			eventKey := notice.kind + ":run:" + strconv.FormatInt(run.ID, 10)
			_, e = s.prepareNotificationWithin(txctx, tx, now.Truncate(time.Hour), notice.kind, eventKey, func() ([]byte, error) {
				return transitionMessage(notice.kind, run.ID, now, notice.changes, s.options.DetailURL)
			})
			if e != nil {
				return e
			}
		}
		return completeManualInspectionWithin(txctx, tx, key, run.ID, s.now().UTC())
	})
	if err != nil {
		return run, err
	}
	return s.Run(ctx, run.ID)
}

type inspectionQueryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func (s *InspectionService) Run(ctx context.Context, id int64) (opsport.InspectionRun, error) {
	return readInspectionRun(ctx, s.pool, id, s.now().UTC())
}
func readInspectionRun(ctx context.Context, q inspectionQueryer, id int64, now time.Time) (opsport.InspectionRun, error) {
	var out opsport.InspectionRun
	e := q.QueryRow(ctx, `SELECT id,request_key,slot,state,release_sha,started_at,completed_at FROM adminops_inspection_runs WHERE id=$1 AND started_at>=$2`, id, now.Add(-720*time.Hour)).Scan(&out.ID, &out.RequestKey, &out.Slot, &out.State, &out.ReleaseSHA, &out.StartedAt, &out.CompletedAt)
	if errors.Is(e, pgx.ErrNoRows) {
		return out, ErrInspectionNotFound
	}
	if e != nil {
		return out, e
	}
	rows, e := q.Query(ctx, `SELECT check_id,owner,status,code,observed_at,metrics FROM adminops_inspection_results WHERE run_id=$1 ORDER BY check_id`, id)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	defs := map[string]opsport.CheckDefinition{}
	for _, d := range InspectionCatalog() {
		defs[d.ID] = d
	}
	out.Results = []opsport.CheckResult{}
	for rows.Next() {
		var r opsport.CheckResult
		var metrics []byte
		if e = rows.Scan(&r.ID, &r.Owner, &r.Status, &r.Code, &r.ObservedAt, &metrics); e != nil {
			return out, e
		}
		r.CheckDefinition = defs[r.ID]
		r.RunID = id
		if e = json.Unmarshal(metrics, &r.Metrics); e != nil {
			return out, e
		}
		out.Results = append(out.Results, r)
	}
	return out, rows.Err()
}
func (s *InspectionService) Overview(ctx context.Context) (opsport.InspectionOverview, error) {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly, IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return opsport.InspectionOverview{}, err
	}
	defer func() {
		rollbackCtx, cancelRollback := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancelRollback()
		_ = tx.Rollback(rollbackCtx)
	}()
	if _, err = tx.Exec(ctx, `SET LOCAL statement_timeout='3s'; SET LOCAL lock_timeout='250ms'`); err != nil {
		return opsport.InspectionOverview{}, err
	}
	// Run selection, checks and issue lifecycle must describe one database
	// snapshot even when a new scan commits between these reads.
	out, err := readInspectionOverview(ctx, tx, s.now().UTC())
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}
func readInspectionOverview(ctx context.Context, q inspectionQueryer, now time.Time) (opsport.InspectionOverview, error) {
	out := opsport.InspectionOverview{ObservedAt: now, Checks: []opsport.CheckResult{}, Issues: []opsport.InspectionIssue{}}
	var id int64
	e := q.QueryRow(ctx, `SELECT id FROM adminops_inspection_runs WHERE state<>'running' AND started_at>=$1 ORDER BY started_at DESC,id DESC LIMIT 1`, now.Add(-720*time.Hour)).Scan(&id)
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		return out, e
	}
	byID := map[string]opsport.CheckResult{}
	if e == nil {
		run, e := readInspectionRun(ctx, q, id, now)
		if e != nil {
			return out, e
		}
		out.Latest = &run
		out.Fresh = run.CompletedAt != nil && now.Sub(*run.CompletedAt) <= 10*time.Minute
		for _, r := range run.Results {
			byID[r.ID] = r
		}
	}
	for _, d := range InspectionCatalog() {
		r, ok := byID[d.ID]
		if !ok {
			r = opsport.CheckResult{CheckDefinition: d, CheckObservation: opsport.CheckObservation{Status: "uncovered", Code: "no_observation", ObservedAt: now, Metrics: map[string]int64{}}}
		} else {
			r.CheckObservation = normalizeObservation(r.CheckObservation, d, now)
		}
		out.Checks = append(out.Checks, r)
	}
	rows, e := q.Query(ctx, `SELECT id,check_id,code,status,severity,version,first_seen,last_seen,resolved_at,occurrences FROM adminops_inspection_issues WHERE status<>'resolved' ORDER BY last_seen DESC,id DESC LIMIT 200`)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		var i opsport.InspectionIssue
		if e = rows.Scan(&i.ID, &i.CheckID, &i.Code, &i.Status, &i.Severity, &i.Version, &i.FirstSeen, &i.LastSeen, &i.ResolvedAt, &i.Occurrences); e != nil {
			return out, e
		}
		out.Issues = append(out.Issues, i)
	}
	return out, rows.Err()
}
func (s *InspectionService) UpdateIssue(ctx context.Context, id, version, actor int64, status, key string) error {
	if id < 1 || version < 1 || actor < 1 || (status != "open" && status != "acknowledged") || len(key) < 8 || len(key) > 160 {
		return ErrInspectionInvalid
	}
	key = string(effectport.Hash("ops-issue-action-v1", strconv.FormatInt(actor, 10), key))
	return s.uow.Within(ctx, func(txctx context.Context) error {
		tx, e := platformpostgres.RequireTransaction(txctx)
		if e != nil {
			return e
		}
		tag, e := tx.Exec(txctx, `INSERT INTO adminops_inspection_issue_actions(issue_id,request_key,actor_id,expected_version,new_status,created_at) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(request_key) DO NOTHING`, id, key, actor, version, status, s.now().UTC())
		if e != nil {
			return e
		}
		if tag.RowsAffected() == 0 {
			var oldID, oldVersion int64
			var oldStatus string
			e = tx.QueryRow(txctx, `SELECT issue_id,expected_version,new_status FROM adminops_inspection_issue_actions WHERE request_key=$1`, key).Scan(&oldID, &oldVersion, &oldStatus)
			if e != nil {
				return e
			}
			if oldID != id || oldVersion != version || oldStatus != status {
				return ErrInspectionConflict
			}
			return nil
		}
		tag, e = tx.Exec(txctx, `UPDATE adminops_inspection_issues SET status=$3,version=version+1 WHERE id=$1 AND version=$2 AND status<>'resolved'`, id, version, status)
		if e != nil {
			return e
		}
		if tag.RowsAffected() != 1 {
			return ErrInspectionConflict
		}
		return nil
	})
}

var inspectionRouteTemplate = regexp.MustCompile(`^(/[a-zA-Z_{}][a-zA-Z0-9_{}.-]*)+/?$`)
var inspectionJobRef = regexp.MustCompile(`^river_[1-9][0-9]*$`)
var inspectionEffectRef = regexp.MustCompile(`^eer_[1-9][0-9]*$`)

func (s *InspectionService) RecordDiagnostic(ctx context.Context, component, code, correlation string) error {
	return s.RecordDiagnosticObservation(ctx, opsport.DiagnosticObservation{Component: component, Code: code, Correlation: correlation})
}
func (s *InspectionService) RecordDiagnosticObservation(ctx context.Context, o opsport.DiagnosticObservation) error {
	if !safeInspectionCode.MatchString(o.Component) || !safeInspectionCode.MatchString(o.Code) || o.Correlation == "" || len(o.Correlation) > 200 || len(o.RouteTemplate) > 240 || (o.RouteTemplate != "" && !inspectionRouteTemplate.MatchString(o.RouteTemplate)) || (o.JobRef != "" && (len(o.JobRef) > 25 || !inspectionJobRef.MatchString(o.JobRef))) || (o.EffectRef != "" && (len(o.EffectRef) > 23 || !inspectionEffectRef.MatchString(o.EffectRef))) || o.JobAttempt < 0 || o.JobAttempt > 2147483647 || (o.JobAttempt > 0 && o.JobRef == "") {
		return ErrInspectionInvalid
	}
	_, e := s.pool.Exec(ctx, `INSERT INTO adminops_diagnostic_events(component,code,correlation_digest,route_template,job_ref,effect_ref,job_attempt,occurred_at,release_sha) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT ON CONSTRAINT adminops_diagnostic_event_identity DO NOTHING`, o.Component, o.Code, string(effectport.Hash("ops-correlation-v1", o.Correlation)), o.RouteTemplate, o.JobRef, o.EffectRef, o.JobAttempt, s.now().UTC(), s.options.ReleaseSHA)
	return e
}

func (s *InspectionService) collectErrors(ctx context.Context, now time.Time) (opsport.CheckObservation, error) {
	var count int64
	e := s.pool.QueryRow(ctx, `SELECT count(*) FROM adminops_diagnostic_events WHERE occurred_at>=$1 AND occurred_at<=$2`, now.Add(-time.Hour), now).Scan(&count)
	status, code := "ok", "no_recorded_errors"
	if count > 0 {
		status, code = "warning", "recorded_errors"
	}
	return opsport.CheckObservation{Status: status, Code: code, ObservedAt: now, Metrics: map[string]int64{"recorded_last_hour": count}}, e
}
func (s *InspectionService) Diagnostics(ctx context.Context) ([]map[string]any, error) {
	return s.DiagnosticsByCorrelation(ctx, "")
}
func (s *InspectionService) DiagnosticsByCorrelation(ctx context.Context, correlation string) ([]map[string]any, error) {
	if len(correlation) > 200 {
		return nil, ErrInspectionInvalid
	}
	digest := ""
	if correlation != "" {
		digest = correlation
		if !effectport.ValidDigest(effectport.Digest(digest)) {
			digest = string(effectport.Hash("ops-correlation-v1", correlation))
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	rows, e := s.pool.Query(ctx, `WITH visible AS (
 SELECT *,count(*) OVER grouped AS occurrences,min(occurred_at) OVER grouped AS first_seen,
 row_number() OVER (PARTITION BY component,code,route_template,release_sha ORDER BY occurred_at DESC,id DESC) AS occurrence_rank
 FROM adminops_diagnostic_events WHERE ($1='' OR correlation_digest=$1) AND occurred_at>=$2
 WINDOW grouped AS (PARTITION BY component,code,route_template,release_sha))
 SELECT id,component,code,correlation_digest,route_template,job_ref,effect_ref,occurred_at,release_sha,occurrences,first_seen
 FROM visible WHERE ($1<>'' OR occurrence_rank=1) ORDER BY occurred_at DESC,id DESC LIMIT 100`, digest, s.now().UTC().Add(-720*time.Hour))
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var c, code, d, route, job, effect, release string
		var at, first time.Time
		var occurrences int64
		if e = rows.Scan(&id, &c, &code, &d, &route, &job, &effect, &at, &release, &occurrences, &first); e != nil {
			return nil, e
		}
		out = append(out, map[string]any{"id": id, "component": c, "code": code, "correlation_digest": d, "route_template": route, "job_ref": job, "effect_ref": effect, "occurred_at": at, "release_sha": release, "occurrences": occurrences, "first_seen": first, "last_seen": at})
	}
	return out, rows.Err()
}

func (s *InspectionService) PrepareReport(ctx context.Context, hour time.Time) (opsport.OpsReport, error) {
	if hour.IsZero() {
		hour = s.now()
	}
	hour = hour.UTC().Truncate(time.Hour)
	var report opsport.OpsReport
	err := s.uow.Within(ctx, func(txctx context.Context) error {
		tx, e := platformpostgres.RequireTransaction(txctx)
		if e != nil {
			return e
		}
		// Use the same ordering as scan completion: completion lock before
		// notification lock. A frozen hourly message cannot combine checks
		// from before a concurrent scan with issue decisions from after it.
		if _, e = tx.Exec(txctx, `SELECT pg_advisory_xact_lock(hashtextextended('adminops-inspection-completion',0))`); e != nil {
			return e
		}
		report, e = s.prepareNotificationWithin(txctx, tx, hour, "hourly", "hourly:"+hour.Format(time.RFC3339), func() ([]byte, error) {
			overview, readErr := readInspectionOverview(txctx, tx, s.now().UTC())
			if readErr != nil {
				return nil, readErr
			}
			var window *effectport.OpsWindowCounts
			// Owner reads are bounded and aggregate only. Failure is explicit in
			// the frozen report while current health remains independently useful.
			if s.options.WindowReader != nil {
				counts, windowErr := s.options.WindowReader.ReadOpsWindowCounts(txctx, hour, hour.Add(time.Hour))
				if windowErr == nil && counts.Started >= 0 && counts.Executed >= 0 {
					window = &counts
				}
			}
			return reportMessage(hour, overview, window, s.options.DetailURL)
		})
		return e
	})
	return report, err
}

// The calling UoW owns the evidence, immutable payload, and effect acceptance.
// It never calls the Provider while a database transaction is held.
func (s *InspectionService) prepareNotificationWithin(ctx context.Context, tx pgx.Tx, hour time.Time, kind, eventKey string, contentFactory func() ([]byte, error)) (opsport.OpsReport, error) {
	var report opsport.OpsReport
	if _, e := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "adminops-report:"+eventKey); e != nil {
		return report, e
	}
	var source, target, payload, policy string
	e := tx.QueryRow(ctx, `SELECT id,hour_key,notification_kind,event_key,content_bytes,COALESCE(effect_id,''),effect_state,created_at,source_digest,target_digest,payload_digest,policy_digest FROM adminops_inspection_reports WHERE event_key=$1 FOR UPDATE`, eventKey).Scan(&report.ID, &report.HourKey, &report.NotificationKind, &report.EventKey, &report.Content, &report.EffectID, &report.EffectState, &report.CreatedAt, &source, &target, &payload, &policy)
	if errors.Is(e, pgx.ErrNoRows) {
		content, contentErr := contentFactory()
		if contentErr != nil {
			return report, contentErr
		}
		source = string(effectport.Hash("ops-event-source-v1", kind, eventKey))
		if kind == "hourly" {
			source = string(effectport.Hash("ops-report-source-v1", hour.Format(time.RFC3339)))
		}
		target = string(effectport.Hash("ops-report-target-v1", s.options.NotificationTargetRef))
		payload = string(effectport.Hash("ops-report-payload-v1", string(content)))
		policy = string(effectport.Hash("ops-report-policy-v1", kind+"-v1"))
		e = tx.QueryRow(ctx, `INSERT INTO adminops_inspection_reports(hour_key,notification_kind,event_key,target_ref,source_digest,target_digest,payload_digest,policy_digest,content,content_bytes,effect_state,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'disabled',$11,$11) RETURNING id,hour_key,notification_kind,event_key,content_bytes,COALESCE(effect_id,''),effect_state,created_at`, hour, kind, eventKey, s.options.NotificationTargetRef, source, target, payload, policy, content, content, s.now().UTC()).Scan(&report.ID, &report.HourKey, &report.NotificationKind, &report.EventKey, &report.Content, &report.EffectID, &report.EffectState, &report.CreatedAt)
	}
	if e != nil {
		return report, e
	}
	if !report.CreatedAt.After(s.now().UTC().Add(-720*time.Hour)) || len(report.Content) == 0 {
		return report, opsport.ErrOpsReportPayloadExpired
	}
	if report.EffectID != "" || !s.options.NotificationEnabled {
		return report, nil
	}
	if target != string(effectport.Hash("ops-report-target-v1", s.options.NotificationTargetRef)) {
		return report, ErrInspectionConflict
	}
	envelope := effectport.Envelope{Owner: effectport.Owner("adminops"), Kind: effectport.Kind("feishu_ops_notification_v1"), SourceRefDigest: effectport.Digest(source), TargetRefDigest: effectport.Digest(target), PayloadDigest: effectport.Digest(payload), PolicyVersionHash: effectport.Digest(policy)}
	projection, _, e := s.effects.AcceptAndQueueWithin(ctx, effectport.AcceptCommand{ReceiptKey: effectport.Hash("ops-report-accept-v1", source), Envelope: envelope, Lane: effectport.Lane("ops_notification")})
	if e != nil {
		return report, e
	}
	if projection.ID == "" {
		return report, ErrInspectionInvalid
	}
	if _, e = tx.Exec(ctx, `UPDATE adminops_inspection_reports SET effect_id=$2,effect_state=$3,updated_at=$4 WHERE id=$1`, report.ID, projection.ID, string(projection.State), s.now().UTC()); e != nil {
		return report, e
	}
	report.EffectID = projection.ID
	report.EffectState = string(projection.State)
	return report, nil
}
func (s *InspectionService) LoadOpsReportPayload(ctx context.Context, envelope effectport.Envelope) (opsport.OpsReportPayload, error) {
	var p opsport.OpsReportPayload
	var source, target, payload, policy string
	if envelope.Owner != "adminops" || envelope.Kind != "feishu_ops_notification_v1" {
		return p, ErrInspectionInvalid
	}
	var expired bool
	e := s.pool.QueryRow(ctx, `SELECT id,hour_key,notification_kind,event_key,target_ref,
 CASE WHEN payload_pruned_at IS NULL AND created_at>$2 THEN content_bytes ELSE NULL END,
 source_digest,target_digest,payload_digest,policy_digest,
 (payload_pruned_at IS NOT NULL OR created_at<=$2)
 FROM adminops_inspection_reports WHERE source_digest=$1 AND effect_id IS NOT NULL`, string(envelope.SourceRefDigest), s.now().UTC().Add(-720*time.Hour)).Scan(&p.ReportID, &p.HourKey, &p.NotificationKind, &p.EventKey, &p.TargetRef, &p.Content, &source, &target, &payload, &policy, &expired)
	if errors.Is(e, pgx.ErrNoRows) {
		return p, ErrInspectionNotFound
	}
	if e != nil {
		return p, e
	}
	if expired {
		return opsport.OpsReportPayload{}, opsport.ErrOpsReportPayloadExpired
	}
	if source != string(envelope.SourceRefDigest) || target != string(envelope.TargetRefDigest) || payload != string(envelope.PayloadDigest) || policy != string(envelope.PolicyVersionHash) || payload != string(effectport.Hash("ops-report-payload-v1", string(p.Content))) {
		return opsport.OpsReportPayload{}, ErrInspectionConflict
	}
	p.PayloadDigest = effectport.Digest(payload)
	p.SourceDigest = effectport.Digest(source)
	p.PolicyDigest = effectport.Digest(policy)
	return p, nil
}
func (s *InspectionService) ObserveOpsReportWithin(ctx context.Context, p effectport.Projection) error {
	if p.Owner != "adminops" || p.Kind != "feishu_ops_notification_v1" || p.ID == "" {
		return ErrInspectionInvalid
	}
	tx, e := platformpostgres.RequireTransaction(ctx)
	if e != nil {
		return e
	}
	switch p.State {
	case effectport.StateAccepted, effectport.StateQueued, effectport.StateAttempted, effectport.StateExecuted, effectport.StateUnknown, effectport.StateReconciled, effectport.StateRetryable, effectport.StateFinalFailed, effectport.StateCancelled:
	default:
		return ErrInspectionInvalid
	}
	tag, e := tx.Exec(ctx, `UPDATE adminops_inspection_reports SET effect_state=$2,updated_at=$3 WHERE effect_id=$1`, p.ID, string(p.State), s.now().UTC())
	if e != nil {
		return e
	}
	if tag.RowsAffected() != 1 {
		return ErrInspectionNotFound
	}
	return nil
}
func (s *InspectionService) Reports(ctx context.Context) ([]opsport.OpsReport, error) {
	rows, e := s.pool.Query(ctx, `SELECT id,hour_key,notification_kind,event_key,content_bytes,COALESCE(effect_id,''),effect_state,created_at FROM adminops_inspection_reports WHERE payload_pruned_at IS NULL AND created_at>$1 ORDER BY created_at DESC,id DESC LIMIT 48`, s.now().UTC().Add(-720*time.Hour))
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []opsport.OpsReport{}
	for rows.Next() {
		var r opsport.OpsReport
		if e = rows.Scan(&r.ID, &r.HourKey, &r.NotificationKind, &r.EventKey, &r.Content, &r.EffectID, &r.EffectState, &r.CreatedAt); e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func (s *InspectionService) String() string {
	return fmt.Sprintf("adminops inspections (%d sources)", len(s.collectors))
}

// reportMessage freezes the exact bytes sent to Feishu. It carries all check
// statuses and bounded issue detail; business payloads are never serialized.
func reportMessage(hour time.Time, o opsport.InspectionOverview, window *effectport.OpsWindowCounts, detailURL string) ([]byte, error) {
	var b strings.Builder
	zone := time.FixedZone("CST", 8*3600)
	fmt.Fprintf(&b, "CRM 每小时巡查 · 窗口 [%s, %s)\n当前观察新鲜：%t\n", hour.In(zone).Format("2006-01-02 15:04"), hour.Add(time.Hour).In(zone).Format("2006-01-02 15:04"), o.Fresh)
	if window == nil {
		b.WriteString("报告窗口外部效果流量：[unknown] 原窗口数据未取得，不以其他窗口替代\n")
	} else {
		fmt.Fprintf(&b, "报告窗口外部效果流量：开始尝试=%d，执行完成=%d（分别按开始、完成时间统计；非群内可见凭证）\n", window.Started, window.Executed)
	}
	fmt.Fprintf(&b, "当前状态采集读回：%s；以下为采集时积压或保留状态总数，不代表本小时新增故障；滚动指标另标窗口。\n", o.ObservedAt.Format(time.RFC3339))
	for _, r := range o.Checks {
		fmt.Fprintf(&b, "[%s] %s · %s", r.Status, r.Title, r.Code)
		keys := make([]string, 0, len(r.Metrics))
		for k := range r.Metrics {
			// The separately queried report window is authoritative; the latest
			// scan's preceding hour may differ after a delayed report attempt.
			if r.ID == "effects.outcomes" && (k == "previous_hour_attempts" || k == "previous_hour_executed") {
				continue
			}
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, " %s%s=%d", k, reportMetricWindow(k), r.Metrics[k])
		}
		b.WriteByte('\n')
	}
	fmt.Fprintf(&b, "未关闭问题：%d（列表上限200）\n", len(o.Issues))
	for i, v := range o.Issues {
		if i >= 20 {
			b.WriteString("其余问题见治理看板。\n")
			break
		}
		fmt.Fprintf(&b, "#%d [%s/%s] %s %s，累计%d次\n", v.ID, v.Severity, v.Status, v.CheckID, v.Code, v.Occurrences)
	}
	if detailURL != "" {
		b.WriteString("详情：" + detailURL + "\n")
	}
	content, e := json.Marshal(map[string]any{"msg_type": "text", "content": map[string]string{"text": b.String()}})
	if e != nil {
		return nil, e
	}
	if len(content) > 24000 {
		return nil, ErrInspectionInvalid
	}
	return content, nil
}

func reportMetricWindow(key string) string {
	switch {
	case strings.HasSuffix(key, "_last_hour"):
		return "（采集前60分钟）"
	case strings.HasSuffix(key, "_last_24h"):
		return "（采集前24小时）"
	case key == "failed" || key == "retryable" || key == "final_failed" || key == "discarded_retained" || strings.HasSuffix(key, "_failed"):
		return "（保留状态总数）"
	default:
		return ""
	}
}
