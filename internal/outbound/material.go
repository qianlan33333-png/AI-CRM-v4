package outbound

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

var ErrMaterialPreparation = errors.New("material preparation unavailable")

type MaterialPreparationService struct {
	uow     platformport.UnitOfWork
	effects effectport.TransactionalAccepter
	pool    *pgxpool.Pool
	sources outboundport.MaterialSourceReader
	now     func() time.Time
	refresh outboundport.MaterialRefreshEnqueuer
}

func (s *MaterialPreparationService) BindRefreshEnqueuer(enqueuer outboundport.MaterialRefreshEnqueuer) error {
	if s == nil || enqueuer == nil || s.refresh != nil {
		return ErrMaterialPreparation
	}
	s.refresh = enqueuer
	return nil
}

func NewMaterialPreparationService(uow platformport.UnitOfWork, effects effectport.TransactionalAccepter, pool *pgxpool.Pool, sources outboundport.MaterialSourceReader) (*MaterialPreparationService, error) {
	if uow == nil || effects == nil || pool == nil || sources == nil {
		return nil, ErrMaterialPreparation
	}
	return &MaterialPreparationService{uow: uow, effects: effects, pool: pool, sources: sources, now: time.Now}, nil
}

func validMaterialRequest(v outboundport.MaterialRequest) bool {
	return v.SourceRef != "" && len(v.SourceRef) <= 512 && !strings.ContainsAny(v.SourceRef, "\x00\r\n") &&
		(v.SourceType == "image" || v.SourceType == "file" || v.SourceType == "video" || v.SourceType == "voice") &&
		v.ContentDigest != [32]byte{} && v.FileName != "" && len(v.FileName) <= 255 && !strings.ContainsAny(v.FileName, "\x00\r\n") &&
		v.MediaType != "" && len(v.MediaType) <= 255 && v.SizeBytes > 0 && v.SnapshotVersion > 0 && effectport.ValidDigest(effectport.Digest(v.CorpScopeDigest)) &&
		(v.RoundDate == "" || validLocalDate(v.RoundDate)) && (!v.ForceRefresh || v.RoundDate != "" || (v.ActorAdminID > 0 && strings.TrimSpace(v.OperationKey) != "")) &&
		(v.ActorAdminID == 0 || (v.ForceRefresh && strings.TrimSpace(v.OperationKey) != ""))
}
func validLocalDate(v string) bool {
	parsed, e := time.Parse(time.DateOnly, v)
	return e == nil && parsed.Format(time.DateOnly) == v
}

func materialEnvelope(id int64, v outboundport.MaterialRequest) effectport.Envelope {
	return effectport.Envelope{Owner: effectport.OwnerOutbound, Kind: effectport.KindOutboundMedia,
		SourceRefDigest:   effectport.Hash("outbound.material.preparation.v1", strconv.FormatInt(id, 10)),
		TargetRefDigest:   effectport.Digest(v.CorpScopeDigest),
		PayloadDigest:     effectport.Hash("outbound.material.content.v1", v.SourceType, hex.EncodeToString(v.ContentDigest[:]), v.FileName, v.MediaType, strconv.FormatInt(v.SnapshotVersion, 10)),
		PolicyVersionHash: effectport.Hash("outbound.material.upload.policy.v1", "72h")}
}
func materialCacheKey(scope, kind string, digest [32]byte, fileName string) string {
	if kind == "image" {
		fileName = ""
	}
	return string(effectport.Hash("outbound.material.cache.v1", scope, kind, hex.EncodeToString(digest[:]), fileName))
}

func (s *MaterialPreparationService) GetMaterialStatus(ctx context.Context, source outboundport.MaterialSourceSnapshot, scope string) (outboundport.MaterialResult, error) {
	if s == nil || ctx == nil || !effectport.ValidDigest(effectport.Digest(scope)) {
		return outboundport.MaterialResult{}, outboundport.ErrInvalidMaterialRequest
	}
	var out outboundport.MaterialResult
	cacheKey := materialCacheKey(scope, source.SourceType, source.ContentDigest, source.FileName)
	var digest []byte
	var created, expires *time.Time
	err := s.pool.QueryRow(ctx, `SELECT latest.state,COALESCE(latest.effect_id,''),COALESCE(current.media_id,''),latest.content_digest,current.provider_created_at,current.expires_at,latest.failure_code FROM outbound_material_preparations latest LEFT JOIN LATERAL (SELECT media_id,provider_created_at,expires_at FROM outbound_material_preparations c WHERE c.scope_digest=latest.scope_digest AND c.cache_key_digest=latest.cache_key_digest AND c.is_current ORDER BY c.id DESC LIMIT 1) current ON TRUE WHERE latest.scope_digest=$1 AND latest.cache_key_digest=$2 ORDER BY latest.id DESC LIMIT 1`, scope, cacheKey).Scan(&out.State, &out.EffectID, &out.MediaID, &digest, &created, &expires, &out.FailureCode)
	if errors.Is(err, pgx.ErrNoRows) {
		out.State = "missing"
		out.CredentialState = "missing"
		return out, nil
	}
	if err != nil {
		return out, err
	}
	copy(out.SourceDigest[:], digest)
	if created != nil {
		out.ProviderCreatedAt = *created
		out.LastSucceededAt = *created
	}
	if expires != nil {
		out.ExpiresAt = *expires
	}
	setCredentialAvailability(&out, s.now().UTC())
	out.NextRefreshAt = nextShanghaiRefresh(s.now())
	return out, nil
}

func setCredentialAvailability(result *outboundport.MaterialResult, now time.Time) {
	if result.MediaID == "" || result.ExpiresAt.IsZero() {
		result.CredentialState = "missing"
		result.CredentialUsable = false
		return
	}
	if !result.ExpiresAt.After(now) {
		result.CredentialState = "expired"
		result.CredentialUsable = false
		return
	}
	result.CredentialState = "ready"
	result.CredentialUsable = true
}

func (s *MaterialPreparationService) ReadyForSend(ctx context.Context, req outboundport.MaterialRequest) (outboundport.MaterialResult, error) {
	req.ForceRefresh = false
	req.RoundDate = ""
	result, err := s.Prepare(ctx, req)
	if err != nil {
		return result, err
	}
	if result.State == "final_failed" || result.State == "outcome_unknown" {
		code := result.FailureCode
		if code == "" {
			code = "media_" + result.State
		}
		return result, outboundport.MediaPreparationTerminalError{Code: code, State: result.State}
	}
	return result, nil
}

func (s *MaterialPreparationService) Prepare(ctx context.Context, req outboundport.MaterialRequest) (out outboundport.MaterialResult, err error) {
	return s.prepare(ctx, req, false)
}

func (s *MaterialPreparationService) AcceptMaterialPreparationWithin(ctx context.Context, req outboundport.MaterialRequest) (outboundport.MaterialResult, error) {
	return s.prepare(ctx, req, true)
}

func (s *MaterialPreparationService) prepare(ctx context.Context, req outboundport.MaterialRequest, joinExisting bool) (out outboundport.MaterialResult, err error) {
	if s == nil || ctx == nil || !validMaterialRequest(req) {
		return out, outboundport.ErrInvalidMaterialRequest
	}
	now := s.now().UTC()
	if req.ValidThrough.IsZero() {
		req.ValidThrough = now
	}
	if req.ValidThrough.Before(now) || req.ValidThrough.After(now.Add(72*time.Hour)) {
		return out, outboundport.ErrInvalidMaterialRequest
	}
	refreshDate := req.RoundDate
	if req.ForceRefresh && refreshDate == "" {
		refreshDate = now.Format(time.DateOnly)
	}
	cacheKey := materialCacheKey(req.CorpScopeDigest, req.SourceType, req.ContentDigest, req.FileName)
	lockKey := cacheKey
	var operationDigest any
	var operationCommandDigest any
	if strings.TrimSpace(req.OperationKey) != "" {
		operationDigest = string(effectport.Hash("outbound.material.operation.v1", strconv.FormatInt(req.ActorAdminID, 10), req.OperationKey, req.RoundDate))
		operationCommandDigest = string(effectport.Hash("outbound.material.operation.command.v1", req.SourceType, hex.EncodeToString(req.ContentDigest[:]), req.FileName, req.MediaType, strconv.FormatInt(req.SnapshotVersion, 10), strconv.FormatBool(req.ForceRefresh), strconv.FormatInt(req.RefreshRoundID, 10)))
	}
	work := func(txctx context.Context) error {
		tx, e := platformpostgres.RequireTransaction(txctx)
		if e != nil {
			return e
		}
		lockKeys := []string{lockKey}
		if operationDigest != nil && operationDigest != lockKey {
			lockKeys = append(lockKeys, operationDigest.(string))
		}
		sort.Strings(lockKeys)
		for _, key := range lockKeys {
			if _, e = tx.Exec(txctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, key); e != nil {
				return e
			}
		}
		if operationDigest != nil {
			var digest []byte
			var created, expires *time.Time
			var storedCommand string
			e = tx.QueryRow(txctx, `SELECT state,COALESCE(effect_id,''),COALESCE(media_id,''),content_digest,provider_created_at,expires_at,failure_code,operation_command_digest FROM outbound_material_preparations WHERE scope_digest=$1 AND operation_key_digest=$2`, req.CorpScopeDigest, operationDigest).Scan(&out.State, &out.EffectID, &out.MediaID, &digest, &created, &expires, &out.FailureCode, &storedCommand)
			if e == nil {
				if storedCommand != operationCommandDigest {
					return outboundport.ErrMaterialOperationConflict
				}
				copy(out.SourceDigest[:], digest)
				if created != nil {
					out.ProviderCreatedAt = *created
					out.LastSucceededAt = *created
				}
				if expires != nil {
					out.ExpiresAt = *expires
				}
				out.NextRefreshAt = nextShanghaiRefresh(now)
				return nil
			}
			if !errors.Is(e, pgx.ErrNoRows) {
				return e
			}
		}
		// A valid current credential always remains the fallback. Force only skips
		// returning it; it is replaced atomically after a confirmed new upload.
		var current outboundport.MaterialResult
		var currentDigest []byte
		currentErr := tx.QueryRow(txctx, `SELECT state,COALESCE(effect_id,''),COALESCE(media_id,''),content_digest,provider_created_at,expires_at FROM outbound_material_preparations WHERE scope_digest=$1 AND cache_key_digest=$2 AND is_current ORDER BY id DESC LIMIT 1 FOR UPDATE`, req.CorpScopeDigest, cacheKey).Scan(&current.State, &current.EffectID, &current.MediaID, &currentDigest, &current.ProviderCreatedAt, &current.ExpiresAt)
		if currentErr == nil {
			copy(current.SourceDigest[:], currentDigest)
			if !req.ForceRefresh && current.State == "executed" && current.MediaID != "" && current.ExpiresAt.After(req.ValidThrough) {
				out = current
				out.State = "ready"
				setCredentialAvailability(&out, now)
				return nil
			}
		} else if !errors.Is(currentErr, pgx.ErrNoRows) {
			return currentErr
		}
		var id int64
		var state, effectID string
		var digest []byte
		var created, expires *time.Time
		var mediaID *string
		query := `SELECT id,state,COALESCE(effect_id,''),content_digest,media_id,provider_created_at,expires_at FROM outbound_material_preparations WHERE scope_digest=$1 AND cache_key_digest=$2 AND state IN ('accepted','queued','retryable_failed','outcome_unknown') ORDER BY id DESC LIMIT 1 FOR UPDATE`
		var date any
		if refreshDate != "" {
			date = refreshDate
		}
		e = tx.QueryRow(txctx, query, req.CorpScopeDigest, cacheKey).Scan(&id, &state, &effectID, &digest, &mediaID, &created, &expires)
		if e == nil {
			out.State = state
			out.EffectID = effectID
			copy(out.SourceDigest[:], digest)
			if mediaID != nil {
				out.MediaID = *mediaID
			}
			if created != nil {
				out.ProviderCreatedAt = *created
			}
			if expires != nil {
				out.ExpiresAt = *expires
			}
			if e = linkMaterialRound(txctx, tx, req, out); e != nil {
				return e
			}
			return nil
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
		var actor, roundID any
		if req.ActorAdminID > 0 {
			actor = req.ActorAdminID
		}
		if req.RefreshRoundID > 0 {
			roundID = req.RefreshRoundID
		}
		e = tx.QueryRow(txctx, `INSERT INTO outbound_material_preparations(source_ref,source_type,scope_digest,cache_key_digest,content_digest,file_name,media_type,size_bytes,source_version,refresh_date,refresh_round_id,operation_key_digest,operation_command_digest,actor_admin_user_id,state) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,'accepted') RETURNING id`, req.SourceRef, req.SourceType, req.CorpScopeDigest, cacheKey, req.ContentDigest[:], req.FileName, req.MediaType, req.SizeBytes, req.SnapshotVersion, date, roundID, operationDigest, operationCommandDigest, actor).Scan(&id)
		if e != nil {
			return e
		}
		envelope := materialEnvelope(id, req)
		projection, _, e := s.effects.AcceptAndQueueWithin(txctx, effectport.AcceptCommand{ReceiptKey: effectport.Hash("outbound.material.accept.v1", strconv.FormatInt(id, 10)), Envelope: envelope, Lane: effectport.LaneOutboundMedia})
		if e != nil {
			return e
		}
		if projection.State != effectport.StateQueued {
			return ErrMaterialPreparation
		}
		_, e = tx.Exec(txctx, `UPDATE outbound_material_preparations SET state='queued',effect_id=$2,updated_at=clock_timestamp() WHERE id=$1`, id, projection.ID)
		if e != nil {
			return e
		}
		e = materialEvent(txctx, tx, id, "queued", string(envelope.Fingerprint()), "")
		if e != nil {
			return e
		}
		out = outboundport.MaterialResult{State: "queued", EffectID: projection.ID, SourceDigest: req.ContentDigest}
		if e = linkMaterialRound(txctx, tx, req, out); e != nil {
			return e
		}
		return nil
	}
	if joinExisting {
		if _, txErr := platformpostgres.RequireTransaction(ctx); txErr != nil {
			return out, txErr
		}
		err = work(ctx)
	} else {
		err = s.uow.Within(ctx, work)
	}
	return out, err
}

func linkMaterialRound(ctx context.Context, tx pgx.Tx, req outboundport.MaterialRequest, result outboundport.MaterialResult) error {
	if req.RefreshRoundID < 1 {
		return nil
	}
	cache := materialCacheKey(req.CorpScopeDigest, req.SourceType, req.ContentDigest, req.FileName)
	state := result.State
	if state == "ready" {
		state = "executed"
	}
	if state == "" {
		state = "final_failed"
	}
	_, err := tx.Exec(ctx, `INSERT INTO outbound_material_refresh_items(round_id,cache_key_digest,preparation_effect_id,state,failure_code) VALUES($1,$2,NULLIF($3,''),$4,$5) ON CONFLICT(round_id,cache_key_digest) DO NOTHING`, req.RefreshRoundID, cache, result.EffectID, state, result.FailureCode)
	return err
}

func nextShanghaiRefresh(now time.Time) time.Time {
	loc, e := time.LoadLocation("Asia/Shanghai")
	if e != nil {
		return time.Time{}
	}
	local := now.In(loc)
	next := time.Date(local.Year(), local.Month(), local.Day(), 2, 0, 0, 0, loc)
	if !next.After(local) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}

func materialEvent(ctx context.Context, tx pgx.Tx, id int64, operation, evidence, failure string) error {
	if _, e := tx.Exec(ctx, `INSERT INTO outbound_material_events(preparation_id,operation,evidence_digest,failure_code) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, id, operation, evidence, failure); e != nil {
		return e
	}
	_, e := tx.Exec(ctx, `INSERT INTO outbound_material_outbox(preparation_id,event_type,payload,evidence_digest) VALUES($1,$2,jsonb_build_object('preparation_id',$1::bigint,'state',$3::text,'failure_code',$4::text),$5) ON CONFLICT DO NOTHING`, id, "outbound.material."+operation+".v1", operation, failure, evidence)
	return e
}

type MaterialPreparationProvider struct {
	service  *MaterialPreparationService
	uploader outboundport.MaterialUploader
}

func NewMaterialPreparationProvider(service *MaterialPreparationService, uploader outboundport.MaterialUploader) (*MaterialPreparationProvider, error) {
	if service == nil || uploader == nil {
		return nil, ErrMaterialPreparation
	}
	return &MaterialPreparationProvider{service, uploader}, nil
}
func (p *MaterialPreparationProvider) Handles(ctx context.Context, effectID string) bool {
	var found bool
	if p != nil && p.service != nil {
		_ = p.service.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM outbound_material_preparations WHERE effect_id=$1)`, effectID).Scan(&found)
	}
	return found
}
func (p *MaterialPreparationProvider) Execute(ctx context.Context, envelope effectport.Envelope, attempt effectport.Attempt) (effectport.AdapterResult, error) {
	fail := func(state effectport.State, called bool, code string) effectport.AdapterResult {
		return effectport.AdapterResult{Completion: state, CallAttempted: called, RealExternalCallExecuted: called, FailureCode: code, ReceiptDigest: effectport.Hash("outbound.material.result.v1", attempt.EffectID, code)}
	}
	if p == nil || p.service == nil || p.uploader == nil || envelope.Kind != effectport.KindOutboundMedia {
		return fail(effectport.StateFinalFailed, false, "invalid"), nil
	}
	var id int64
	var req outboundport.MaterialRequest
	var digest []byte
	var state string
	err := p.service.pool.QueryRow(ctx, `SELECT id,source_ref,source_type,scope_digest,content_digest,file_name,media_type,size_bytes,source_version,state FROM outbound_material_preparations WHERE effect_id=$1`, attempt.EffectID).Scan(&id, &req.SourceRef, &req.SourceType, &req.CorpScopeDigest, &digest, &req.FileName, &req.MediaType, &req.SizeBytes, &req.SnapshotVersion, &state)
	if err != nil {
		return fail(effectport.StateRetryable, false, "read_unavailable"), err
	}
	copy(req.ContentDigest[:], digest)
	if state != "queued" && state != "retryable_failed" || materialEnvelope(id, req) != envelope {
		return fail(effectport.StateFinalFailed, false, "snapshot_changed"), nil
	}
	content, err := p.service.sources.ReadSourceBytes(ctx, req.MaterialSourceSnapshot)
	if err != nil {
		if errors.Is(err, outboundport.ErrMaterialSourceChanged) {
			return fail(effectport.StateFinalFailed, false, "source_changed"), nil
		}
		return fail(effectport.StateRetryable, false, "source_unavailable"), err
	}
	receipt, called, err := p.uploader.UploadMaterial(ctx, req.MaterialSourceSnapshot, content, req.CorpScopeDigest)
	if err != nil {
		if called {
			var typed outboundport.MaterialUploadError
			if errors.As(err, &typed) && !typed.OutcomeUnknown() {
				state := effectport.StateFinalFailed
				if typed.Retryable() {
					state = effectport.StateRetryable
				}
				out := fail(state, true, typed.FailureCode())
				out.RealExternalCallExecuted = false
				out.SafeToRetryRejected = typed.Retryable()
				return out, nil
			}
			return fail(effectport.StateUnknown, true, "upload_outcome_unknown"), nil
		}
		return fail(effectport.StateRetryable, false, "upload_unavailable"), err
	}
	if !called || receipt.MediaID == "" || receipt.ProviderCreatedAt.IsZero() {
		return fail(effectport.StateUnknown, called, "invalid_receipt"), nil
	}
	raw, _ := json.Marshal(receipt)
	artifact := effectport.ResultArtifact{Kind: "outbound.material.upload.v1", Payload: raw}
	artifact.Digest = effectport.Hash("external-effect.artifact.v1", artifact.Kind, string(raw))
	out := fail(effectport.StateExecuted, true, "uploaded")
	out.Artifact = artifact
	return out, nil
}

func (s *MaterialPreparationService) Handles(ctx context.Context, effectID string) bool {
	var found bool
	if s != nil {
		_ = s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM outbound_material_preparations WHERE effect_id=$1)`, effectID).Scan(&found)
	}
	return found
}
func (s *MaterialPreparationService) CompleteEffect(ctx context.Context, effectID string, envelope effectport.Envelope, attempt effectport.Attempt, result effectport.AdapterResult) error {
	if s == nil || effectID == "" || effectID != attempt.EffectID || envelope.Kind != effectport.KindOutboundMedia || !effectport.ValidDigest(result.ReceiptDigest) {
		return ErrMaterialPreparation
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	var id int64
	var state string
	if err = tx.QueryRow(ctx, `SELECT id,state FROM outbound_material_preparations WHERE effect_id=$1 FOR UPDATE`, effectID).Scan(&id, &state); err != nil {
		return err
	}
	mediaReconciliation := state == "outcome_unknown" && (result.Completion == effectport.StateReconciled || result.Completion == effectport.StateExecuted)
	if state != "queued" && state != "retryable_failed" && !mediaReconciliation {
		return ErrMaterialPreparation
	}
	next := string(result.Completion)
	var mediaID *string
	var providerCreated, expires *time.Time
	if result.Completion == effectport.StateExecuted {
		var receipt outboundport.MaterialUploadReceipt
		if !result.Artifact.Valid() || result.Artifact.Kind != "outbound.material.upload.v1" || json.Unmarshal(result.Artifact.Payload, &receipt) != nil || receipt.MediaID == "" || receipt.ProviderCreatedAt.IsZero() || receipt.ProviderCreatedAt.After(s.now().UTC().Add(5*time.Minute)) {
			return ErrMaterialPreparation
		}
		expiry := receipt.ProviderCreatedAt.UTC().Add(72 * time.Hour)
		mediaID = &receipt.MediaID
		created := receipt.ProviderCreatedAt.UTC()
		providerCreated = &created
		expires = &expiry
		if _, err = tx.Exec(ctx, `UPDATE outbound_material_preparations SET is_current=FALSE WHERE is_current AND (scope_digest,cache_key_digest)=(SELECT scope_digest,cache_key_digest FROM outbound_material_preparations WHERE id=$1)`, id); err != nil {
			return err
		}
	} else if result.Completion != effectport.StateQueued && result.Completion != effectport.StateCancelled && result.Completion != effectport.StateUnknown && result.Completion != effectport.StateRetryable && result.Completion != effectport.StateFinalFailed && !(mediaReconciliation && result.Completion == effectport.StateReconciled && result.FailureCode == "reconciled_no_effect") {
		return ErrMaterialPreparation
	}
	failureCode := result.FailureCode
	if result.Completion == effectport.StateCancelled && failureCode == "" {
		failureCode = "cancelled"
	}
	_, err = tx.Exec(ctx, `UPDATE outbound_material_preparations SET state=$2,media_id=$3,provider_created_at=$4,expires_at=$5,is_current=($2='executed'),failure_code=$6,updated_at=clock_timestamp() WHERE id=$1`, id, next, mediaID, providerCreated, expires, failureCode)
	if err != nil {
		return err
	}
	if err = materialEvent(ctx, tx, id, next, string(result.ReceiptDigest), failureCode); err != nil {
		return err
	}
	itemState := next
	if result.Completion == effectport.StateReconciled || result.Completion == effectport.StateCancelled {
		itemState = "final_failed"
	}
	if _, err = tx.Exec(ctx, `UPDATE outbound_material_refresh_items SET state=$2,failure_code=$3,updated_at=clock_timestamp() WHERE preparation_effect_id=$1`, effectID, itemState, failureCode); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT DISTINCT round_id FROM outbound_material_refresh_items WHERE preparation_effect_id=$1`, effectID)
	if err != nil {
		return err
	}
	defer rows.Close()
	var rounds []int64
	for rows.Next() {
		var round int64
		if err = rows.Scan(&round); err != nil {
			return err
		}
		rounds = append(rounds, round)
	}
	if err = rows.Err(); err != nil {
		return err
	}
	for _, round := range rounds {
		if err = refreshRoundCounts(ctx, tx, round); err != nil {
			return err
		}
	}
	return nil
}

var _ outboundport.MaterialPreparer = (*MaterialPreparationService)(nil)
var _ outboundport.MaterialPreparationAccepter = (*MaterialPreparationService)(nil)
var _ outboundport.MaterialStatusReader = (*MaterialPreparationService)(nil)
var _ effectport.ProviderAdapter = (*MaterialPreparationProvider)(nil)
var _ effectport.CompletionSink = (*MaterialPreparationService)(nil)
