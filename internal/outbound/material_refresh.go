package outbound

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	platformjobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

type MaterialRefreshJobArgs struct {
	RoundID int64 `json:"round_id"`
}

func (MaterialRefreshJobArgs) Kind() string { return "outbound.material-refresh.v1" }

type RiverMaterialRefreshEnqueuer struct{ client *river.Client[pgx.Tx] }

func NewRiverMaterialRefreshEnqueuer(client *river.Client[pgx.Tx]) (*RiverMaterialRefreshEnqueuer, error) {
	if client == nil {
		return nil, ErrMaterialPreparation
	}
	return &RiverMaterialRefreshEnqueuer{client}, nil
}
func (e *RiverMaterialRefreshEnqueuer) EnqueueMaterialRefreshWithin(ctx context.Context, id int64) (int64, error) {
	if e == nil || e.client == nil || id < 1 {
		return 0, ErrMaterialPreparation
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return 0, err
	}
	inserted, err := platformjobqueue.InsertTxWithOptions(ctx, e.client, tx, MaterialRefreshJobArgs{RoundID: id}, river.InsertOpts{Queue: platformjobqueue.OutboundMediaQueue, MaxAttempts: 100})
	if err != nil {
		return 0, err
	}
	return inserted.Job.ID, nil
}

func (s *MaterialPreparationService) RefreshAll(ctx context.Context, c outboundport.MaterialRefreshCommand) (out outboundport.MaterialRefreshRound, err error) {
	if s == nil || s.refresh == nil || ctx == nil {
		return out, ErrMaterialPreparation
	}
	now := s.now()
	loc, _ := time.LoadLocation("Asia/Shanghai")
	today := now.In(loc).Format(time.DateOnly)
	kind := "daily"
	opKey := "daily:" + today
	var actor any
	if c.ActorAdminID > 0 {
		kind = "manual"
		if !c.Force || (c.LocalDate != "" && c.LocalDate != today) || len(c.OperationKey) < 8 || len(c.OperationKey) > 128 {
			return out, outboundport.ErrInvalidMaterialRequest
		}
		opKey = "manual:" + strconv.FormatInt(c.ActorAdminID, 10) + ":" + c.OperationKey
		actor = c.ActorAdminID
	}
	opDigest := string(effectport.Hash("outbound.material.refresh.operation.v1", opKey))
	err = s.uow.Within(ctx, func(txctx context.Context) error {
		tx, e := platformpostgres.RequireTransaction(txctx)
		if e != nil {
			return e
		}
		if _, e = tx.Exec(txctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, opDigest); e != nil {
			return e
		}
		e = tx.QueryRow(txctx, `SELECT id,local_date::text,state,cursor,total,queued,succeeded,failed,unknown_count,started_at,completed_at FROM outbound_material_refresh_rounds WHERE operation_key_digest=$1`, opDigest).Scan(&out.ID, &out.LocalDate, &out.State, &out.Cursor, &out.Total, &out.Queued, &out.Succeeded, &out.Failed, &out.Unknown, &out.StartedAt, &out.CompletedAt)
		if e == nil {
			return nil
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
		e = tx.QueryRow(txctx, `INSERT INTO outbound_material_refresh_rounds(local_date,round_kind,operation_key_digest,actor_admin_user_id,state) VALUES($1,$2,$3,$4,'queued') ON CONFLICT(local_date) WHERE round_kind='daily' DO UPDATE SET local_date=EXCLUDED.local_date RETURNING id,local_date::text,state,cursor,total,queued,succeeded,failed,unknown_count,started_at,completed_at`, today, kind, opDigest, actor).Scan(&out.ID, &out.LocalDate, &out.State, &out.Cursor, &out.Total, &out.Queued, &out.Succeeded, &out.Failed, &out.Unknown, &out.StartedAt, &out.CompletedAt)
		if e != nil {
			return e
		}
		job, e := s.refresh.EnqueueMaterialRefreshWithin(txctx, out.ID)
		if e != nil {
			return e
		}
		_, e = tx.Exec(txctx, `UPDATE outbound_material_refresh_rounds SET river_job_id=$2 WHERE id=$1 AND river_job_id IS NULL`, out.ID, job)
		return e
	})
	out.OperationKey = c.OperationKey
	out.ActorAdminID = c.ActorAdminID
	return out, err
}

// EnsureDailyRefresh provides restart catch-up without an in-process ticker.
// Before 02:00 Shanghai it is a no-op; afterwards the date-unique daily round
// is accepted atomically and may resume an existing River job.
func (s *MaterialPreparationService) EnsureDailyRefresh(ctx context.Context) error {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	local := s.now().In(loc)
	due := time.Date(local.Year(), local.Month(), local.Day(), 2, 0, 0, 0, loc)
	if local.Before(due) {
		return nil
	}
	_, err := s.RefreshAll(ctx, outboundport.MaterialRefreshCommand{})
	return err
}

func (s *MaterialPreparationService) GetRefreshRound(ctx context.Context, id int64) (out outboundport.MaterialRefreshRound, err error) {
	if s == nil || id < 1 {
		return out, ErrMaterialPreparation
	}
	err = s.pool.QueryRow(ctx, `SELECT id,local_date::text,state,cursor,total,queued,succeeded,failed,unknown_count,COALESCE(actor_admin_user_id,0),started_at,completed_at FROM outbound_material_refresh_rounds WHERE id=$1`, id).Scan(&out.ID, &out.LocalDate, &out.State, &out.Cursor, &out.Total, &out.Queued, &out.Succeeded, &out.Failed, &out.Unknown, &out.ActorAdminID, &out.StartedAt, &out.CompletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		err = outboundport.ErrMaterialPreparationNotFound
	}
	return out, err
}
func (s *MaterialPreparationService) GetTodayRefreshRound(ctx context.Context) (out outboundport.MaterialRefreshRound, found bool, err error) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	date := s.now().In(loc).Format(time.DateOnly)
	err = s.pool.QueryRow(ctx, `SELECT id,local_date::text,state,cursor,total,queued,succeeded,failed,unknown_count,started_at,completed_at FROM outbound_material_refresh_rounds WHERE local_date=$1 AND round_kind='daily'`, date).Scan(&out.ID, &out.LocalDate, &out.State, &out.Cursor, &out.Total, &out.Queued, &out.Succeeded, &out.Failed, &out.Unknown, &out.StartedAt, &out.CompletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, false, nil
	}
	return out, err == nil, err
}

type MaterialRefreshWorker struct {
	river.WorkerDefaults[MaterialRefreshJobArgs]
	service *MaterialPreparationService
	scope   string
}

type materialRefreshItemResult struct {
	key, effect, state, failure string
	count                       int64
}

func NewMaterialRefreshWorker(service *MaterialPreparationService, scope string) *MaterialRefreshWorker {
	return &MaterialRefreshWorker{service: service, scope: scope}
}
func (w *MaterialRefreshWorker) Bind(service *MaterialPreparationService) error {
	if w == nil || service == nil || w.service != nil {
		return ErrMaterialPreparation
	}
	w.service = service
	return nil
}
func (w *MaterialRefreshWorker) Work(ctx context.Context, job *river.Job[MaterialRefreshJobArgs]) error {
	if w == nil || w.service == nil || job == nil {
		return ErrMaterialPreparation
	}
	if job.Args.RoundID == 0 {
		_, err := w.service.RefreshAll(ctx, outboundport.MaterialRefreshCommand{})
		return err
	}
	round, err := w.service.GetRefreshRound(ctx, job.Args.RoundID)
	if err != nil {
		return err
	}
	if round.State == "completed" || round.State == "completed_with_failures" {
		return nil
	}
	// The final page has already advanced the source cursor. Waiting only means
	// that accepted material effects have not all reached terminal states yet;
	// it must not rescan the source list from an empty final cursor.
	if round.State == "waiting" {
		return w.service.finalizeRefreshRound(ctx, round.ID)
	}
	page, err := w.service.sources.ListEnabledSourceSnapshots(ctx, outboundport.MaterialSnapshotPageRequest{CorpScopeDigest: w.scope, Cursor: round.Cursor, Limit: 100})
	if err != nil {
		return err
	}
	// A nonterminal page must move the durable cursor. Accepting a stalled
	// cursor would let later retries claim and count the same sources forever.
	if !page.Done && (page.NextCursor == "" || page.NextCursor == round.Cursor) {
		return ErrMaterialPreparation
	}
	items := map[string]materialRefreshItemResult{}
	for _, failure := range page.Failures {
		key := string(effectport.Hash("outbound.material.source-failure.v1", failure.SourceRef))
		code := failure.FailureCode
		if code == "" {
			code = "source_invalid"
		}
		items[key] = materialRefreshItemResult{key: key, state: "final_failed", failure: code, count: 1}
	}
	for _, source := range page.Items {
		cache := materialCacheKey(w.scope, source.SourceType, source.ContentDigest, source.FileName)
		if old, ok := items[cache]; ok {
			old.count++
			items[cache] = old
			continue
		}
		key := "round:" + strconv.FormatInt(round.ID, 10) + ":" + cache
		// Prepare atomically binds the effect to the round before its Provider
		// completion can arrive. Its source_count is a zero placeholder; only
		// advanceMaterialRefreshPage records a claimed page's source references.
		result, e := w.service.Prepare(ctx, outboundport.MaterialRequest{MaterialSourceSnapshot: source, CorpScopeDigest: w.scope, ForceRefresh: true, RoundDate: round.LocalDate, RefreshRoundID: round.ID, OperationKey: key})
		if e != nil {
			items[cache] = materialRefreshItemResult{key: cache, state: "final_failed", failure: "prepare_unavailable", count: 1}
			continue
		}
		items[cache] = materialRefreshItemResult{key: cache, effect: result.EffectID, state: result.State, failure: result.FailureCode, count: 1}
	}
	claimed, err := w.advanceMaterialRefreshPage(ctx, round, page, items)
	if err != nil {
		return err
	}
	if !claimed {
		return nil
	}
	if !page.Done {
		return river.JobSnooze(time.Second)
	}
	return w.service.finalizeRefreshRound(ctx, round.ID)
}

func (w *MaterialRefreshWorker) advanceMaterialRefreshPage(ctx context.Context, round outboundport.MaterialRefreshRound, page outboundport.MaterialSnapshotPage, items map[string]materialRefreshItemResult) (bool, error) {
	state := "running"
	if page.Done {
		state = "waiting"
	}
	tx, err := w.service.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	// Advance first. This compare-and-swap serializes source-count writes with
	// the durable page cursor; a stale worker rolls back without recording the
	// same page a second time.
	var id int64
	err = tx.QueryRow(ctx, `UPDATE outbound_material_refresh_rounds SET state=$2,cursor=$3 WHERE id=$1 AND cursor=$4 AND state IN ('queued','running') RETURNING id`, round.ID, state, page.NextCursor, round.Cursor).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	for _, item := range items {
		// A prepared source already has a zero-count row, inserted atomically
		// with its effect. Do not overwrite that row's state: a Provider receipt
		// may have completed it before this page cursor is claimed.
		if _, err = tx.Exec(ctx, `INSERT INTO outbound_material_refresh_items(round_id,cache_key_digest,preparation_effect_id,state,failure_code,source_count) VALUES($1,$2,NULLIF($3,''),$4,$5,$6) ON CONFLICT(round_id,cache_key_digest) DO UPDATE SET source_count=outbound_material_refresh_items.source_count+EXCLUDED.source_count,updated_at=clock_timestamp()`, round.ID, item.key, item.effect, item.state, item.failure, item.count); err != nil {
			return false, err
		}
	}
	if err = refreshRoundCounts(ctx, tx, id); err != nil {
		return false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}
func (s *MaterialPreparationService) finalizeRefreshRound(ctx context.Context, id int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = refreshRoundCounts(ctx, tx, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func refreshRoundCounts(ctx context.Context, tx pgx.Tx, id int64) error {
	_, err := tx.Exec(ctx, `WITH c AS (SELECT count(*) total,count(*) FILTER(WHERE state IN ('queued','retryable_failed')) queued,count(*) FILTER(WHERE state='executed') succeeded,count(*) FILTER(WHERE state='final_failed') failed,count(*) FILTER(WHERE state='outcome_unknown') unknown_count,count(*) FILTER(WHERE state IN ('queued','retryable_failed')) pending FROM outbound_material_refresh_items WHERE round_id=$1) UPDATE outbound_material_refresh_rounds r SET total=c.total,queued=c.queued,succeeded=c.succeeded,failed=c.failed,unknown_count=c.unknown_count,state=CASE WHEN r.state<>'waiting' OR c.pending>0 THEN r.state WHEN c.failed>0 OR c.unknown_count>0 THEN 'completed_with_failures' ELSE 'completed' END,completed_at=CASE WHEN r.state='waiting' AND c.pending=0 THEN clock_timestamp() ELSE r.completed_at END FROM c WHERE r.id=$1`, id)
	return err
}

type shanghaiDailyAtTwo struct{}

func (shanghaiDailyAtTwo) Next(after time.Time) time.Time {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	local := after.In(loc)
	next := time.Date(local.Year(), local.Month(), local.Day(), 2, 0, 0, 0, loc)
	if !next.After(local) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}

func MaterialRefreshPeriodicJob() *river.PeriodicJob {
	return river.NewPeriodicJob(shanghaiDailyAtTwo{}, func() (river.JobArgs, *river.InsertOpts) {
		return MaterialRefreshJobArgs{}, &river.InsertOpts{Queue: platformjobqueue.OutboundMediaQueue, UniqueOpts: river.UniqueOpts{ByPeriod: 24 * time.Hour}}
	}, nil)
}

var _ outboundport.MaterialRefresher = (*MaterialPreparationService)(nil)
var _ outboundport.MaterialRefreshEnqueuer = (*RiverMaterialRefreshEnqueuer)(nil)
var _ rivertype.JobArgs = MaterialRefreshJobArgs{}
