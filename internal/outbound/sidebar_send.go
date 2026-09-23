package outbound

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

var ErrSidebarSendConflict = errors.New("sidebar send conflict")

type SidebarSendService struct {
	uow     platformport.UnitOfWork
	effects interface {
		effectport.TransactionalAccepter
		effectport.ClientCompleter
	}
	now    func() time.Time
	random func([]byte) error
}

func NewSidebarSendService(uow platformport.UnitOfWork, effects interface {
	effectport.TransactionalAccepter
	effectport.ClientCompleter
}) (*SidebarSendService, error) {
	if uow == nil || effects == nil {
		return nil, errors.New("sidebar send dependencies are required")
	}
	return &SidebarSendService{uow: uow, effects: effects, now: time.Now, random: func(b []byte) error { _, e := rand.Read(b); return e }}, nil
}

func (s *SidebarSendService) AcceptSidebarSend(ctx context.Context, in outboundport.SidebarSendCommand) (out outboundport.SidebarSendAcceptance, err error) {
	if in.CustomerID < 1 || in.EmployeeID == "" || len(in.EmployeeID) > 1024 || !validSidebarResource(in.ResourceKind) || in.ResourceID == "" || len(in.ResourceID) > 100 || in.ContentDigest == ([32]byte{}) || len(in.Payload) == 0 || !json.Valid(in.Payload) || len(in.IdempotencyKey) < 8 || len(in.IdempotencyKey) > 200 || strings.TrimSpace(in.IdempotencyKey) != in.IdempotencyKey {
		return out, ErrSidebarSendConflict
	}
	keyDigest := sha256.Sum256([]byte(in.IdempotencyKey))
	intentRaw, _ := json.Marshal([]any{in.CustomerID, in.EmployeeID, in.ResourceKind, in.ResourceID, in.ContentDigest, in.Payload})
	intentDigest := sha256.Sum256(intentRaw)
	employeeDigest := sha256.Sum256([]byte(in.EmployeeID))
	resourceBinding, bindingErr := sidebarSendResourceBinding(in.ResourceKind, in.ResourceID, in.Payload)
	if bindingErr != nil {
		return out, ErrSidebarSendConflict
	}
	now := s.now().UTC()
	err = s.uow.Within(ctx, func(txctx context.Context) error {
		tx, e := platformpostgres.RequireTransaction(txctx)
		if e != nil {
			return e
		}
		// A receipt key only protects one browser request. Serialize a complete
		// target/resource binding too, so a reload or another WebView cannot mint
		// a replacement effect while the earlier result is still unknown.
		lockKey := "outbound.sidebar_send.unresolved.v1:" + strconv.FormatInt(in.CustomerID, 10) + ":" + hex.EncodeToString(employeeDigest[:]) + ":" + resourceBinding
		if _, e = tx.Exec(txctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, lockKey); e != nil {
			return e
		}
		var id int64
		var priorDigest []byte
		var effectID, queueID *string
		var state string
		var payload []byte
		// Exact receipt replay remains the first decision: a reused browser key
		// can only return its immutable original intent, never a newer one.
		e = tx.QueryRow(txctx, `SELECT id,intent_digest,effect_id,queue_receipt_id,state,payload FROM outbound_sidebar_send_intents WHERE receipt_key_digest=$1 FOR UPDATE`, keyDigest[:]).Scan(&id, &priorDigest, &effectID, &queueID, &state, &payload)
		if e == nil {
			if subtle.ConstantTimeCompare(priorDigest, intentDigest[:]) != 1 {
				return ErrSidebarSendConflict
			}
			out = sidebarSendReplay(id, effectID, state, payload)
			return nil
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return e
		}

		// Existing versions stored no explicit product type. Their immutable
		// payload still carries the trusted /p/ or /s/ binding. An older malformed
		// product payload is deliberately treated as a lock for both product
		// kinds, because the server cannot prove which card may have been sent.
		rows, e := tx.Query(txctx, `SELECT id,effect_id,state,payload FROM outbound_sidebar_send_intents WHERE customer_id=$1 AND employee_digest=$2 AND resource_kind=$3 AND resource_id=$4 AND state IN ('accepted','queued','outcome_unknown') ORDER BY CASE state WHEN 'outcome_unknown' THEN 0 WHEN 'queued' THEN 1 ELSE 2 END,id FOR UPDATE`, in.CustomerID, employeeDigest[:], in.ResourceKind, in.ResourceID)
		if e != nil {
			return e
		}
		for rows.Next() {
			var existingID int64
			var existingEffectID *string
			var existingState string
			var existingPayload []byte
			if e = rows.Scan(&existingID, &existingEffectID, &existingState, &existingPayload); e != nil {
				rows.Close()
				return e
			}
			existingBinding, existingBindingErr := sidebarSendResourceBinding(in.ResourceKind, in.ResourceID, existingPayload)
			if existingBindingErr != nil || existingBinding == resourceBinding {
				rows.Close()
				out = sidebarSendReplay(existingID, existingEffectID, existingState, existingPayload)
				return nil
			}
		}
		if e = rows.Err(); e != nil {
			rows.Close()
			return e
		}
		rows.Close()

		e = tx.QueryRow(txctx, `INSERT INTO outbound_sidebar_send_intents(customer_id,employee_digest,resource_kind,resource_id,content_digest,payload,receipt_key_digest,intent_digest,state,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6::jsonb,$7,$8,'accepted',$9,$9) ON CONFLICT(receipt_key_digest) DO NOTHING RETURNING id,intent_digest,effect_id,queue_receipt_id,state,payload`, in.CustomerID, employeeDigest[:], in.ResourceKind, in.ResourceID, in.ContentDigest[:], in.Payload, keyDigest[:], intentDigest[:], now).Scan(&id, &priorDigest, &effectID, &queueID, &state, &payload)
		if errors.Is(e, pgx.ErrNoRows) {
			e = tx.QueryRow(txctx, `SELECT id,intent_digest,effect_id,queue_receipt_id,state,payload FROM outbound_sidebar_send_intents WHERE receipt_key_digest=$1 FOR UPDATE`, keyDigest[:]).Scan(&id, &priorDigest, &effectID, &queueID, &state, &payload)
			if e != nil {
				return e
			}
			if subtle.ConstantTimeCompare(priorDigest, intentDigest[:]) != 1 {
				return ErrSidebarSendConflict
			}
			out = sidebarSendReplay(id, effectID, state, payload)
			return nil
		}
		if e != nil {
			return e
		}
		// A new intent after an explicit terminal result is a new browser-owned
		// effect. Bind EER to the durable intent ID so its immutable fingerprint
		// cannot collide with the prior, already completed effect. Exact receipt
		// replays return above and never create a second intent or effect.
		envelope := effectport.Envelope{Owner: effectport.OwnerOutbound, Kind: effectport.KindSidebarJSSDKSend, SourceRefDigest: effectport.Hash("sidebar.send.intent.v1", strconv.FormatInt(id, 10), in.ResourceKind, in.ResourceID), TargetRefDigest: effectport.Hash("sidebar.send.target", hex.EncodeToString(employeeDigest[:]), strconv.FormatInt(in.CustomerID, 10)), PayloadDigest: effectport.Digest("sha256:" + hex.EncodeToString(in.ContentDigest[:])), PolicyVersionHash: effectport.Hash("sidebar.send.policy.v1")}
		projection, receipt, e := s.effects.AcceptAndQueueWithin(txctx, effectport.AcceptCommand{ReceiptKey: effectport.Hash("sidebar.send.accept", in.IdempotencyKey), Envelope: envelope, ScheduledAt: now.Add(6 * time.Minute)})
		if e != nil {
			return e
		}
		grantBytes := make([]byte, 32)
		if e = s.random(grantBytes); e != nil {
			return e
		}
		grant := base64.RawURLEncoding.EncodeToString(grantBytes)
		grantDigest := sha256.Sum256([]byte(grant))
		expires := now.Add(5 * time.Minute)
		if _, e = tx.Exec(txctx, `UPDATE outbound_sidebar_send_intents SET effect_id=$2,queue_receipt_id=$3,state='queued',updated_at=$4 WHERE id=$1`, id, projection.ID, receipt.QueueReceiptID, now); e != nil {
			return e
		}
		if _, e = tx.Exec(txctx, `INSERT INTO outbound_sidebar_send_grants(intent_id,token_digest,expires_at) VALUES($1,$2,$3)`, id, grantDigest[:], expires); e != nil {
			return e
		}
		auditDigest := sha256.Sum256(intentRaw)
		if _, e = tx.Exec(txctx, `INSERT INTO outbound_sidebar_send_audit_events(intent_id,operation,payload_digest,occurred_at) VALUES($1,'accept',$2,$3)`, id, auditDigest[:], now); e != nil {
			return e
		}
		if _, e = tx.Exec(txctx, `INSERT INTO outbound_sidebar_send_outbox(event_type,intent_id,payload,idempotency_digest,occurred_at) VALUES('outbound.sidebar_send.queued.v1',$1,jsonb_build_object('intent_id',$1::bigint,'effect_id',$2::text,'state','queued'),$3::bytea,$4::timestamptz)`, id, projection.ID, keyDigest[:], now); e != nil {
			return e
		}
		out = outboundport.SidebarSendAcceptance{IntentID: id, EffectID: projection.ID, State: "queued", Grant: grant, GrantExpiresAt: expires, Payload: append([]byte(nil), in.Payload...)}
		return nil
	})
	return out, err
}

func (s *SidebarSendService) CompleteSidebarSend(ctx context.Context, in outboundport.SidebarSendOutcomeCommand) (out outboundport.SidebarSendAcceptance, err error) {
	if in.IntentID < 1 || in.CustomerID < 1 || in.EmployeeID == "" || in.Grant == "" || in.EvidenceDigest == ([32]byte{}) || (in.Outcome != "client_executed" && in.Outcome != "outcome_unknown" && in.Outcome != "final_failed") {
		return out, ErrSidebarSendConflict
	}
	now := s.now().UTC()
	grantDigest := sha256.Sum256([]byte(in.Grant))
	employeeDigest := sha256.Sum256([]byte(in.EmployeeID))
	err = s.uow.Within(ctx, func(txctx context.Context) error {
		tx, e := platformpostgres.RequireTransaction(txctx)
		if e != nil {
			return e
		}
		var effectID, state string
		var payload []byte
		var storedEmployee, storedGrant []byte
		var expires time.Time
		var consumed *time.Time
		var priorOutcome *string
		e = tx.QueryRow(txctx, `SELECT intent.effect_id,intent.state,intent.payload,intent.employee_digest,send_grant.token_digest,send_grant.expires_at,send_grant.consumed_at,send_grant.outcome FROM outbound_sidebar_send_intents intent JOIN outbound_sidebar_send_grants send_grant ON send_grant.intent_id=intent.id WHERE intent.id=$1 AND intent.customer_id=$2 FOR UPDATE`, in.IntentID, in.CustomerID).Scan(&effectID, &state, &payload, &storedEmployee, &storedGrant, &expires, &consumed, &priorOutcome)
		if e != nil {
			return e
		}
		if subtle.ConstantTimeCompare(storedEmployee, employeeDigest[:]) != 1 || subtle.ConstantTimeCompare(storedGrant, grantDigest[:]) != 1 {
			return ErrSidebarSendConflict
		}
		if consumed != nil {
			if priorOutcome == nil || *priorOutcome != in.Outcome {
				return ErrSidebarSendConflict
			}
			out = outboundport.SidebarSendAcceptance{IntentID: in.IntentID, EffectID: effectID, State: state, Payload: payload, Replayed: true}
			return nil
		}
		if now.After(expires) {
			return ErrSidebarSendConflict
		}
		effectState := effectport.StateExecuted
		if in.Outcome == "outcome_unknown" {
			effectState = effectport.StateUnknown
		} else if in.Outcome == "final_failed" {
			effectState = effectport.StateFinalFailed
		}
		projection, e := s.effects.CompleteClientEffectWithin(txctx, effectport.ClientCompletionCommand{EffectID: effectID, ReceiptKey: effectport.Hash("sidebar.send.client", string(grantDigest[:])), EvidenceDigest: effectport.Digest("sha256:" + hex.EncodeToString(in.EvidenceDigest[:])), State: effectState})
		if e != nil {
			return e
		}
		if _, e = tx.Exec(txctx, `UPDATE outbound_sidebar_send_grants SET consumed_at=$2,outcome=$3,evidence_digest=$4 WHERE intent_id=$1`, in.IntentID, now, in.Outcome, in.EvidenceDigest[:]); e != nil {
			return e
		}
		delivery := "unknown"
		if _, e = tx.Exec(txctx, `UPDATE outbound_sidebar_send_intents SET state=$2,delivery_state=$3,updated_at=$4 WHERE id=$1`, in.IntentID, in.Outcome, delivery, now); e != nil {
			return e
		}
		if _, e = tx.Exec(txctx, `INSERT INTO outbound_sidebar_send_audit_events(intent_id,operation,payload_digest,occurred_at) VALUES($1,'client_complete',$2,$3)`, in.IntentID, in.EvidenceDigest[:], now); e != nil {
			return e
		}
		eventKey := sha256.Sum256([]byte("complete\x00" + in.Grant))
		if _, e = tx.Exec(txctx, `INSERT INTO outbound_sidebar_send_outbox(event_type,intent_id,payload,idempotency_digest,occurred_at) VALUES('outbound.sidebar_send.client_completed.v1',$1,jsonb_build_object('intent_id',$1::bigint,'effect_id',$2::text,'state',$3::text,'delivery_state','unknown'),$4::bytea,$5::timestamptz)`, in.IntentID, effectID, in.Outcome, eventKey[:], now); e != nil {
			return e
		}
		out = outboundport.SidebarSendAcceptance{IntentID: in.IntentID, EffectID: projection.ID, State: in.Outcome, Payload: payload}
		return nil
	})
	return out, err
}

func sidebarSendReplay(id int64, effectID *string, state string, payload []byte) outboundport.SidebarSendAcceptance {
	out := outboundport.SidebarSendAcceptance{IntentID: id, State: state, Payload: payload, Replayed: true}
	if effectID != nil {
		out.EffectID = *effectID
	}
	return out
}

func sidebarSendResourceBinding(kind, resourceID string, payload []byte) (string, error) {
	if kind != "product" {
		return kind + ":" + resourceID, nil
	}
	var message struct {
		MsgType string `json:"msgtype"`
		News    struct {
			Link string `json:"link"`
		} `json:"news"`
	}
	if err := json.Unmarshal(payload, &message); err != nil || message.MsgType != "news" || message.News.Link == "" {
		return "", ErrSidebarSendConflict
	}
	link, err := url.Parse(message.News.Link)
	if err != nil || link.Host == "" {
		return "", ErrSidebarSendConflict
	}
	productType := ""
	switch {
	case strings.HasPrefix(link.Path, "/p/") && strings.TrimPrefix(link.Path, "/p/") != "":
		productType = "standard"
	case strings.HasPrefix(link.Path, "/s/") && strings.TrimPrefix(link.Path, "/s/") != "":
		productType = "service_period"
	default:
		return "", ErrSidebarSendConflict
	}
	return "product:" + productType + ":" + resourceID, nil
}

func validSidebarResource(v string) bool {
	return v == "product" || v == "coupon" || v == "material" || v == "radar_link"
}

var _ outboundport.SidebarSendAccepter = (*SidebarSendService)(nil)
