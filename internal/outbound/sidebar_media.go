package outbound

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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

var ErrSidebarImagePreparation = errors.New("sidebar image preparation unavailable")

type SidebarMediaPreparationService struct {
	uow     platformport.UnitOfWork
	effects effectport.TransactionalAccepter
	pool    *pgxpool.Pool
	now     func() time.Time
}

func NewSidebarMediaPreparationService(uow platformport.UnitOfWork, effects effectport.TransactionalAccepter, pool *pgxpool.Pool) (*SidebarMediaPreparationService, error) {
	if uow == nil || effects == nil || pool == nil {
		return nil, ErrSidebarImagePreparation
	}
	return &SidebarMediaPreparationService{uow: uow, effects: effects, pool: pool, now: time.Now}, nil
}

func validSidebarImageSource(source outboundport.SidebarImagePreparationSource) bool {
	return source.ImageID > 0 && len(source.Content) > 5 && len(source.Content) <= 2<<20 && source.SourceDigest == sha256.Sum256(source.Content) && source.Scope != "" && len(source.Scope) <= 1024 && source.FileName != "" && len(source.FileName) <= 255 && !strings.ContainsAny(source.FileName, "\r\n\x00") && (source.MediaType == "image/png" || source.MediaType == "image/jpeg")
}

func sidebarImageEnvelope(id, imageID int64, scope string, digest []byte, fileName, mediaType string) effectport.Envelope {
	return effectport.Envelope{Owner: effectport.OwnerOutbound, Kind: effectport.KindOutboundMedia, SourceRefDigest: effectport.Hash("sidebar.image.preparation.v1", strconv.FormatInt(id, 10)), TargetRefDigest: effectport.Hash("sidebar.image.scope.v1", scope), PayloadDigest: effectport.Hash("sidebar.image.content.v1", strconv.FormatInt(imageID, 10), hex.EncodeToString(digest), fileName, mediaType), PolicyVersionHash: effectport.Hash("sidebar.image.upload.policy.v1")}
}

func (s *SidebarMediaPreparationService) PrepareSidebarImage(ctx context.Context, source outboundport.SidebarImagePreparationSource, requiredThrough time.Time) (out outboundport.SidebarImagePreparation, err error) {
	if s == nil || ctx == nil || !validSidebarImageSource(source) || requiredThrough.IsZero() {
		return out, ErrSidebarImagePreparation
	}
	now := s.now().UTC()
	if !requiredThrough.After(now) || requiredThrough.After(now.Add(time.Hour)) {
		return out, ErrSidebarImagePreparation
	}
	scope := string(effectport.Hash("sidebar.image.scope.config.v1", source.Scope))
	err = s.uow.Within(ctx, func(txctx context.Context) error {
		tx, e := platformpostgres.RequireTransaction(txctx)
		if e != nil {
			return e
		}
		if _, e = tx.Exec(txctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, scope+":"+strconv.FormatInt(source.ImageID, 10)); e != nil {
			return e
		}
		var id int64
		var oldDigest []byte
		var effectID, state string
		var mediaID *string
		var ready *time.Time
		e = tx.QueryRow(txctx, `SELECT id,source_digest,COALESCE(effect_id,''),state,media_id,ready_until FROM outbound_sidebar_image_preparations WHERE scope_digest=$1 AND image_id=$2 ORDER BY id DESC LIMIT 1 FOR UPDATE`, scope, source.ImageID).Scan(&id, &oldDigest, &effectID, &state, &mediaID, &ready)
		if e == nil {
			// A changed source cannot replace an unresolved upload. Confirmed
			// success or explicitly audited abandonment permits a new generation.
			if state != "executed" && state != "reconciled" {
				out = outboundport.SidebarImagePreparation{State: state, EffectID: effectID}
				return nil
			}
			if state == "executed" && hex.EncodeToString(oldDigest) == hex.EncodeToString(source.SourceDigest[:]) && ready != nil && ready.After(requiredThrough) && mediaID != nil {
				out = outboundport.SidebarImagePreparation{State: "ready", EffectID: effectID, MediaID: *mediaID, ReadyUntil: *ready}
				return nil
			}
		} else if !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
		e = tx.QueryRow(txctx, `INSERT INTO outbound_sidebar_image_preparations(image_id,scope_digest,source_digest,content,file_name,media_type,state,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,'accepted',$7,$7) RETURNING id`, source.ImageID, scope, source.SourceDigest[:], source.Content, source.FileName, source.MediaType, now).Scan(&id)
		if e != nil {
			return e
		}
		envelope := sidebarImageEnvelope(id, source.ImageID, scope, source.SourceDigest[:], source.FileName, source.MediaType)
		projection, _, e := s.effects.AcceptAndQueueWithin(txctx, effectport.AcceptCommand{ReceiptKey: effectport.Hash("sidebar.image.accept.v1", strconv.FormatInt(id, 10)), Envelope: envelope})
		if e != nil {
			return e
		}
		if projection.ID == "" || projection.State != effectport.StateQueued {
			return ErrSidebarImagePreparation
		}
		if _, e = tx.Exec(txctx, `UPDATE outbound_sidebar_image_preparations SET state='queued',effect_id=$2 WHERE id=$1`, id, projection.ID); e != nil {
			return e
		}
		if e = sidebarImageEvent(txctx, tx, id, "queued", string(envelope.Fingerprint()), now); e != nil {
			return e
		}
		out = outboundport.SidebarImagePreparation{State: "queued", EffectID: projection.ID}
		return nil
	})
	return out, err
}

func sidebarImageEvent(ctx context.Context, tx pgx.Tx, id int64, operation, digest string, now time.Time) error {
	if _, err := tx.Exec(ctx, `INSERT INTO outbound_sidebar_image_events(preparation_id,operation,evidence_digest,occurred_at) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, id, operation, digest, now); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO outbound_sidebar_image_outbox(preparation_id,event_type,payload,evidence_digest,occurred_at) VALUES($1,$2,jsonb_build_object('preparation_id',$1::bigint,'state',$3::text),$4,$5) ON CONFLICT DO NOTHING`, id, "outbound.sidebar_image."+operation+".v1", operation, digest, now)
	return err
}

type SidebarMediaPreparationProvider struct {
	service  *SidebarMediaPreparationService
	uploader outboundport.SidebarImageUploader
}

func NewSidebarMediaPreparationProvider(service *SidebarMediaPreparationService, uploader outboundport.SidebarImageUploader) (*SidebarMediaPreparationProvider, error) {
	if service == nil || uploader == nil {
		return nil, ErrSidebarImagePreparation
	}
	return &SidebarMediaPreparationProvider{service, uploader}, nil
}

func (p *SidebarMediaPreparationProvider) Execute(ctx context.Context, envelope effectport.Envelope, attempt effectport.Attempt) (effectport.AdapterResult, error) {
	fail := func(state effectport.State, attempted bool, reason string) effectport.AdapterResult {
		return effectport.AdapterResult{Completion: state, CallAttempted: attempted, RealExternalCallExecuted: attempted, ReceiptDigest: effectport.Hash("sidebar.image.upload.result.v1", attempt.EffectID, reason)}
	}
	if p == nil || p.service == nil || p.uploader == nil || attempt.EffectID == "" || envelope.Kind != effectport.KindOutboundMedia {
		return fail(effectport.StateFinalFailed, false, "invalid"), nil
	}
	var id int64
	var scope, state string
	var digest []byte
	var source outboundport.SidebarImagePreparationSource
	err := p.service.pool.QueryRow(ctx, `SELECT id,image_id,scope_digest,source_digest,content,file_name,media_type,state FROM outbound_sidebar_image_preparations WHERE effect_id=$1`, attempt.EffectID).Scan(&id, &source.ImageID, &scope, &digest, &source.Content, &source.FileName, &source.MediaType, &state)
	if err != nil {
		return fail(effectport.StateRetryable, false, "read_unavailable"), err
	}
	copy(source.SourceDigest[:], digest)
	source.Scope = scope
	if (state != "queued" && state != "retryable_failed") || len(digest) != 32 || !validSidebarImageSource(source) || sidebarImageEnvelope(id, source.ImageID, scope, digest, source.FileName, source.MediaType) != envelope {
		return fail(effectport.StateFinalFailed, false, "snapshot_changed"), nil
	}
	receipt, attempted, err := p.uploader.UploadSidebarImage(ctx, source)
	if err != nil {
		if attempted {
			out := fail(effectport.StateUnknown, true, "upload_unknown")
			diagnostic := sidebarImageFailureDiagnostic{FailureCode: "upload_unknown"}
			var typed outboundport.SidebarImageUploadError
			if errors.As(err, &typed) {
				diagnostic = safeSidebarImageFailure(typed)
				if !typed.OutcomeUnknown() && diagnostic.ProviderErrorCode != 0 {
					out.Completion = effectport.StateFinalFailed
					out.RealExternalCallExecuted = false
				}
			}
			out.ReceiptDigest = effectport.Hash("sidebar.image.upload.result.v1", attempt.EffectID, diagnostic.FailureCode, strconv.Itoa(diagnostic.HTTPStatusCode))
			raw, _ := json.Marshal(diagnostic)
			out.Artifact = effectport.ResultArtifact{Kind: "outbound.sidebar_image.failure.v1", Payload: raw}
			out.Artifact.Digest = effectport.Hash("external-effect.artifact.v1", out.Artifact.Kind, string(raw))
			return out, nil
		}
		return fail(effectport.StateRetryable, false, "upload_unavailable"), err
	}
	if !attempted || strings.TrimSpace(receipt.MediaID) == "" || len(receipt.MediaID) > 1024 || !receipt.ReadyUntil.After(p.service.now().Add(6*time.Minute)) {
		return fail(effectport.StateUnknown, attempted, "invalid_receipt"), nil
	}
	raw, _ := json.Marshal(receipt)
	artifact := effectport.ResultArtifact{Kind: "outbound.sidebar_image.upload.v1", Payload: raw}
	artifact.Digest = effectport.Hash("external-effect.artifact.v1", artifact.Kind, string(raw))
	out := fail(effectport.StateExecuted, true, "uploaded")
	out.Artifact = artifact
	return out, nil
}

// CompleteEffect participates in the EER completion UoW. No separate commit,
// Provider call, or message send is permitted here.
func (s *SidebarMediaPreparationService) CompleteEffect(ctx context.Context, effectID string, envelope effectport.Envelope, attempt effectport.Attempt, result effectport.AdapterResult) error {
	if s == nil || effectID == "" || effectID != attempt.EffectID || envelope.Kind != effectport.KindOutboundMedia || !effectport.ValidDigest(result.ReceiptDigest) {
		return ErrSidebarImagePreparation
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	var id, imageID int64
	var scope, fileName, mediaType string
	var digest []byte
	if err = tx.QueryRow(ctx, `SELECT id,image_id,scope_digest,source_digest,file_name,media_type FROM outbound_sidebar_image_preparations WHERE effect_id=$1 FOR UPDATE`, effectID).Scan(&id, &imageID, &scope, &digest, &fileName, &mediaType); err != nil {
		return err
	}
	if sidebarImageEnvelope(id, imageID, scope, digest, fileName, mediaType) != envelope {
		return ErrSidebarImagePreparation
	}
	state := string(result.Completion)
	var mediaID *string
	var ready *time.Time
	switch result.Completion {
	case effectport.StateExecuted:
		var receipt outboundport.SidebarImageUploadReceipt
		if !result.CallAttempted || !result.RealExternalCallExecuted || !result.Artifact.Valid() || result.Artifact.Kind != "outbound.sidebar_image.upload.v1" || json.Unmarshal(result.Artifact.Payload, &receipt) != nil || strings.TrimSpace(receipt.MediaID) == "" || len(receipt.MediaID) > 1024 || receipt.ReadyUntil.IsZero() {
			return ErrSidebarImagePreparation
		}
		mediaID = &receipt.MediaID
		ready = &receipt.ReadyUntil
	case effectport.StateUnknown, effectport.StateRetryable, effectport.StateFinalFailed:
	default:
		return ErrSidebarImagePreparation
	}
	if _, err = tx.Exec(ctx, `UPDATE outbound_sidebar_image_preparations SET state=$2,media_id=$3,ready_until=$4,updated_at=$5 WHERE id=$1`, id, state, mediaID, ready, s.now().UTC()); err != nil {
		return err
	}
	if err = sidebarImageEvent(ctx, tx, id, state, string(result.ReceiptDigest), s.now().UTC()); err != nil {
		return err
	}
	if result.Completion != effectport.StateExecuted && result.Artifact.Kind == "outbound.sidebar_image.failure.v1" {
		var diagnostic sidebarImageFailureDiagnostic
		if !result.Artifact.Valid() || json.Unmarshal(result.Artifact.Payload, &diagnostic) != nil || !diagnostic.valid() {
			return ErrSidebarImagePreparation
		}
		raw, _ := json.Marshal(diagnostic)
		if _, err = tx.Exec(ctx, `UPDATE outbound_sidebar_image_events SET diagnostics=$3 WHERE preparation_id=$1 AND operation=$2 AND evidence_digest=$4`, id, state, raw, string(result.ReceiptDigest)); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE outbound_sidebar_image_outbox SET payload=payload||$3::jsonb WHERE preparation_id=$1 AND event_type=$2 AND evidence_digest=$4`, id, "outbound.sidebar_image."+state+".v1", raw, string(result.ReceiptDigest)); err != nil {
			return err
		}
	}
	return nil
}

var _ outboundport.SidebarImagePreparer = (*SidebarMediaPreparationService)(nil)
var _ effectport.ProviderAdapter = (*SidebarMediaPreparationProvider)(nil)
var _ effectport.CompletionSink = (*SidebarMediaPreparationService)(nil)

type sidebarImageFailureDiagnostic struct {
	FailureCode       string `json:"failure_code"`
	ProviderErrorCode int64  `json:"provider_error_code"`
	HTTPStatusCode    int    `json:"http_status_code"`
}

func (d sidebarImageFailureDiagnostic) valid() bool {
	if d.HTTPStatusCode != 0 && (d.HTTPStatusCode < 100 || d.HTTPStatusCode > 599) {
		return false
	}
	if d.ProviderErrorCode != 0 {
		return d.FailureCode == "wecom_errcode_"+strconv.FormatInt(d.ProviderErrorCode, 10)
	}
	switch d.FailureCode {
	case "upload_unknown", "upload_request_invalid", "upload_transport_unknown", "upload_response_unreadable", "upload_http_unknown", "upload_response_invalid", "upload_response_conflict", "upload_receipt_missing":
		return true
	}
	return false
}
func safeSidebarImageFailure(e outboundport.SidebarImageUploadError) sidebarImageFailureDiagnostic {
	d := sidebarImageFailureDiagnostic{e.FailureCode(), e.ProviderErrorCode(), e.HTTPStatusCode()}
	if !d.valid() {
		return sidebarImageFailureDiagnostic{FailureCode: "upload_unknown"}
	}
	return d
}

// AbandonUnknownSidebarImage explicitly abandons an unconfirmed temporary
// upload. It does not assert Provider failure and never uploads or sends.
// The original outcome_unknown event remains immutable evidence. Reconciliation
// and the owner's audited release of the resource share one PostgreSQL UoW.
func (s *SidebarMediaPreparationService) AbandonUnknownSidebarImage(ctx context.Context, effectID string, actor int64, evidence effectport.Digest) error {
	if s == nil || effectID == "" || actor < 1 || !effectport.ValidDigest(evidence) {
		return ErrSidebarImagePreparation
	}
	reconciler, ok := s.effects.(effectport.TransactionalReconciler)
	if !ok {
		return ErrSidebarImagePreparation
	}
	return s.uow.Within(ctx, func(txctx context.Context) error {
		tx, err := platformpostgres.RequireTransaction(txctx)
		if err != nil {
			return err
		}
		var id int64
		var state string
		if err = tx.QueryRow(txctx, `SELECT id,state FROM outbound_sidebar_image_preparations WHERE effect_id=$1 FOR UPDATE`, effectID).Scan(&id, &state); err != nil {
			return err
		}
		if state == "reconciled" {
			var matches bool
			err = tx.QueryRow(txctx, `SELECT EXISTS(SELECT 1 FROM outbound_sidebar_image_events WHERE preparation_id=$1 AND operation='abandoned_unconfirmed' AND evidence_digest=$2 AND actor_admin_user_id=$3)`, id, string(evidence), actor).Scan(&matches)
			if err != nil {
				return err
			}
			if !matches {
				return ErrSidebarImagePreparation
			}
			return nil
		}
		if state != "outcome_unknown" {
			return ErrSidebarImagePreparation
		}
		candidate, err := reconciler.ReconciliationCandidate(txctx, effectID)
		if err != nil {
			return err
		}
		if candidate.Owner != effectport.OwnerOutbound || candidate.Kind != effectport.KindOutboundMedia || candidate.State != effectport.StateUnknown || candidate.LeaseExpiresAt.After(s.now().UTC()) {
			return ErrSidebarImagePreparation
		}
		projection, err := reconciler.ReconcileEffectWithin(txctx, effectport.ReconcileCommand{EffectID: effectID, ActorAdminUserID: actor, EvidenceDigest: evidence, ReceiptKey: effectport.Hash("sidebar.image.abandon.unconfirmed.v1", effectID, strconv.FormatInt(actor, 10), string(evidence)), Generation: candidate.Generation, Fence: candidate.Fence, LeaseExpiresAt: candidate.LeaseExpiresAt})
		if err != nil {
			return err
		}
		if projection.State != effectport.StateReconciled {
			return ErrSidebarImagePreparation
		}
		if _, err = tx.Exec(txctx, `UPDATE outbound_sidebar_image_preparations SET state='reconciled',updated_at=$2 WHERE id=$1 AND state='outcome_unknown'`, id, s.now().UTC()); err != nil {
			return err
		}
		if err = sidebarImageEvent(txctx, tx, id, "abandoned_unconfirmed", string(evidence), s.now().UTC()); err != nil {
			return err
		}
		if _, err = tx.Exec(txctx, `UPDATE outbound_sidebar_image_events SET actor_admin_user_id=$3 WHERE preparation_id=$1 AND operation='abandoned_unconfirmed' AND evidence_digest=$2`, id, string(evidence), actor); err != nil {
			return err
		}
		_, err = tx.Exec(txctx, `UPDATE outbound_sidebar_image_outbox SET payload=payload||jsonb_build_object('actor_admin_user_id',$3::bigint,'resolution','abandoned_unconfirmed','provider_outcome','unconfirmed') WHERE preparation_id=$1 AND event_type='outbound.sidebar_image.abandoned_unconfirmed.v1' AND evidence_digest=$2`, id, string(evidence), actor)
		return err
	})
}
