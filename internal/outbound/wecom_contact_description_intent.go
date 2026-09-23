package outbound

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// ContactDescriptionIntentStore owns immutable acceptance records. It stores
// no external_userid and no description text; those values exist only at
// observation and provider execution boundaries.
type ContactDescriptionIntentStore struct {
	pool    *pgxpool.Pool
	effects effectport.TransactionalAccepter
}

func NewContactDescriptionIntentStore(pool *pgxpool.Pool, effects effectport.TransactionalAccepter) (*ContactDescriptionIntentStore, error) {
	if pool == nil || effects == nil {
		return nil, errors.New("contact description intent store dependencies are required")
	}
	return &ContactDescriptionIntentStore{pool: pool, effects: effects}, nil
}

func (s *ContactDescriptionIntentStore) WriteContactDescriptionIntentWithin(ctx context.Context, command outboundport.ContactDescriptionIntentCommand) (outboundport.ContactDescriptionIntentResult, error) {
	if s == nil || s.effects == nil || !command.Valid() {
		return outboundport.ContactDescriptionIntentResult{}, errors.New("invalid contact description intent")
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return outboundport.ContactDescriptionIntentResult{}, err
	}
	// Serialise plan selection by relationship, then create a fresh immutable
	// plan only after an explicit replan. The stable relationship key never
	// contains the observed text or whether this came from a full sync/callback.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, command.ReceiptKey); err != nil {
		return outboundport.ContactDescriptionIntentResult{}, err
	}
	var prior outboundport.ContactDescriptionIntentResult
	var customerID int64
	var employeeID, effectID, state, operation string
	var target, observed, payload []byte
	var revision int64
	err = tx.QueryRow(ctx, `SELECT id,customer_id,employee_userid,effect_ref,state,operation,target_digest,observed_description_digest,payload_digest,plan_revision
		FROM outbound_wecom_contact_description_intents WHERE relationship_digest=$1 ORDER BY plan_revision DESC LIMIT 1 FOR UPDATE`, digestBytes(command.ReceiptKey)).Scan(&prior.IntentID, &customerID, &employeeID, &effectID, &state, &operation, &target, &observed, &payload, &revision)
	if err == nil {
		if customerID == int64(command.CustomerID) && employeeID == command.EmployeeUserID && operation == command.Operation && bytes.Equal(target, digestBytes(command.TargetDigest)) && bytes.Equal(observed, digestBytes(command.ObservedDescriptionDigest)) && bytes.Equal(payload, digestBytes(command.PayloadDigest)) {
			prior.EffectID, prior.Replayed = effectID, true
			if command.SourceRunID > 0 {
				if _, err = tx.Exec(ctx, `INSERT INTO outbound_wecom_contact_description_run_items(source_run_id,intent_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, command.SourceRunID, prior.IntentID); err != nil {
					return outboundport.ContactDescriptionIntentResult{}, err
				}
			}
			return prior, nil
		}
		if state == string(effectport.StateUnknown) {
			return outboundport.ContactDescriptionIntentResult{}, outboundport.ErrContactDescriptionOutcomeUnknown
		}
		if state == string(effectport.StateQueued) || state == string(effectport.StateAttempted) || state == string(effectport.StateRetryable) {
			return outboundport.ContactDescriptionIntentResult{}, outboundport.ErrContactDescriptionInFlight
		}
		if !command.Replan {
			return outboundport.ContactDescriptionIntentResult{}, outboundport.ErrContactDescriptionReplanRequired
		}
		revision++
	} else if errors.Is(err, pgx.ErrNoRows) {
		revision = 1
	} else {
		return outboundport.ContactDescriptionIntentResult{}, err
	}
	planReceipt := outboundport.ContactDescriptionEffectReceiptKey(command.ReceiptKey, revision)
	// A command source identifies the observed event/run, while the persisted
	// source additionally identifies its immutable plan revision.  This permits
	// a later explicit replan to return to an older description snapshot without
	// reusing the older dispatch row or effect envelope.
	planSource := effectport.Hash("wecom.contact.description.plan-source.v1", string(command.SourceDigest), strconv.FormatInt(revision, 10))
	envelope := effectport.Envelope{Owner: effectport.OwnerOutbound, Kind: effectport.KindWeComContactDescription, SourceRefDigest: planSource, TargetRefDigest: command.TargetDigest, PayloadDigest: command.PayloadDigest, PolicyVersionHash: effectport.Hash("wecom.contact.description.policy.v1")}
	projection, receipt, err := s.effects.AcceptAndQueueWithin(ctx, effectport.AcceptCommand{ReceiptKey: planReceipt, Envelope: envelope})
	if err != nil {
		return outboundport.ContactDescriptionIntentResult{}, err
	}
	if projection.ID == "" || projection.QueueJobID < 1 || receipt.QueueReceiptID == "" {
		return outboundport.ContactDescriptionIntentResult{}, errors.New("incomplete contact description effect acceptance")
	}
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO outbound_wecom_contact_description_intents(customer_id,employee_userid,operation,relationship_digest,plan_revision,source_ref_digest,target_digest,observed_description_digest,payload_digest,receipt_key,effect_ref,state)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,'queued') RETURNING id`, command.CustomerID, command.EmployeeUserID, command.Operation, digestBytes(command.ReceiptKey), revision, digestBytes(planSource), digestBytes(command.TargetDigest), digestBytes(command.ObservedDescriptionDigest), digestBytes(command.PayloadDigest), digestBytes(planReceipt), projection.ID).Scan(&id)
	if err != nil {
		return outboundport.ContactDescriptionIntentResult{}, err
	}
	if command.SourceRunID > 0 {
		if _, err = tx.Exec(ctx, `INSERT INTO outbound_wecom_contact_description_run_items(source_run_id,intent_id) VALUES($1,$2)`, command.SourceRunID, id); err != nil {
			return outboundport.ContactDescriptionIntentResult{}, err
		}
	}
	return outboundport.ContactDescriptionIntentResult{IntentID: id, EffectID: projection.ID}, nil
}

// ContactDescriptionRunStats reports effect outcomes separately from the
// source CustomerSync terminal status. A successful directory enumeration is
// never presented as a successful Provider backfill.
func (s *ContactDescriptionIntentStore) ContactDescriptionRunStats(ctx context.Context, runID int64) (outboundport.ContactDescriptionRunStats, error) {
	if s == nil || s.pool == nil || runID < 1 {
		return outboundport.ContactDescriptionRunStats{}, errors.New("invalid contact description run")
	}
	var stats outboundport.ContactDescriptionRunStats
	err := s.pool.QueryRow(ctx, `WITH latest AS (
		SELECT DISTINCT ON (i.relationship_digest) i.*
		FROM outbound_wecom_contact_description_run_items item
		JOIN outbound_wecom_contact_description_intents i ON i.id=item.intent_id
		WHERE item.source_run_id=$1
		ORDER BY i.relationship_digest,i.plan_revision DESC
	)
	SELECT COUNT(*),
		COUNT(*) FILTER (WHERE state='queued'),
		COUNT(*) FILTER (WHERE result_status='written'),
		COUNT(*) FILTER (WHERE result_status='already_present'),
		COUNT(*) FILTER (WHERE readback_state='confirmed'),
		COUNT(*) FILTER (WHERE readback_state='failed'),
		COUNT(*) FILTER (WHERE result_status='description_changed'),
		COUNT(*) FILTER (WHERE result_status='too_long'),
		COUNT(*) FILTER (WHERE result_status='not_authorized'),
		COUNT(*) FILTER (WHERE state='retryable_failed'),
		COUNT(*) FILTER (WHERE state='final_failed'),
		COUNT(*) FILTER (WHERE state='outcome_unknown'),
		COUNT(*) FILTER (WHERE readback_state='confirmed' OR result_status='already_present')
	FROM latest`, runID).Scan(&stats.Discovered, &stats.Queued, &stats.Written, &stats.AlreadyPresent, &stats.ReadbackConfirmed, &stats.ReadbackFailed, &stats.DescriptionChanged, &stats.TooLong, &stats.NotAuthorized, &stats.RetryableFailed, &stats.TerminalFailed, &stats.OutcomeUnknown, &stats.BackfillCompleted)
	return stats, err
}

// ScheduleContactDescriptionReadbacksWithin creates only readback plans. It
// selects rows whose original write already returned success but whose
// confirmation read failed; unknown and failed writes are deliberately absent.
func (s *ContactDescriptionIntentStore) ScheduleContactDescriptionReadbacksWithin(ctx context.Context, runID int64) (int64, error) {
	if s == nil || s.effects == nil || runID < 1 {
		return 0, errors.New("invalid contact description readback request")
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return 0, err
	}
	rows, err := tx.Query(ctx, `SELECT i.customer_id,i.employee_userid,i.relationship_digest,i.target_digest,i.observed_description_digest
		FROM outbound_wecom_contact_description_run_items item
		JOIN outbound_wecom_contact_description_intents i ON i.id=item.intent_id
		WHERE item.source_run_id=$1 AND i.operation='write' AND i.state='executed' AND i.result_status='written' AND i.readback_state='failed'
		ORDER BY i.id FOR UPDATE OF i`, runID)
	if err != nil {
		return 0, err
	}
	type candidate struct {
		customerID   int64
		employeeID   string
		relationship []byte
		target       []byte
		observed     []byte
	}
	var candidates []candidate
	for rows.Next() {
		var value candidate
		if err = rows.Scan(&value.customerID, &value.employeeID, &value.relationship, &value.target, &value.observed); err != nil {
			rows.Close()
			return 0, err
		}
		candidates = append(candidates, value)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	var scheduled int64
	for _, candidate := range candidates {
		relationDigest := digestFromBytes(candidate.relationship)
		observedDigest := digestFromBytes(candidate.observed)
		readbackPayload := effectport.Hash("wecom.contact.description.readback.payload.v1", string(relationDigest), string(observedDigest))
		result, writeErr := s.WriteContactDescriptionIntentWithin(ctx, outboundport.ContactDescriptionIntentCommand{
			CustomerID: customerdomain.CustomerID(candidate.customerID), EmployeeUserID: candidate.employeeID,
			SourceDigest: effectport.Hash("wecom.contact.description.readback.source.v1", string(relationDigest), string(observedDigest)),
			TargetDigest: digestFromBytes(candidate.target), ObservedDescriptionDigest: observedDigest, PayloadDigest: readbackPayload,
			ReceiptKey: relationDigest, SourceRunID: runID, Replan: true, Operation: outboundport.ContactDescriptionOperationReadback,
		})
		if writeErr != nil {
			if errors.Is(writeErr, outboundport.ErrContactDescriptionInFlight) {
				continue
			}
			return 0, writeErr
		}
		if !result.Replayed {
			scheduled++
		}
	}
	return scheduled, nil
}

func (s *ContactDescriptionIntentStore) ReadContactDescriptionDispatch(ctx context.Context, sourceDigest string) (ContactDescriptionDispatch, error) {
	if s == nil || s.pool == nil || !effectport.ValidDigest(effectport.Digest(sourceDigest)) {
		return ContactDescriptionDispatch{}, errors.New("contact description dispatch unavailable")
	}
	var value ContactDescriptionDispatch
	var target, payload, observed []byte
	err := s.pool.QueryRow(ctx, `SELECT effect_ref,customer_id,employee_userid,operation,target_digest,payload_digest,observed_description_digest
		FROM outbound_wecom_contact_description_intents WHERE source_ref_digest=$1`, digestBytes(effectport.Digest(sourceDigest))).Scan(&value.EffectRef, &value.CustomerID, &value.EmployeeUserID, &value.Operation, &target, &payload, &observed)
	if err != nil {
		return ContactDescriptionDispatch{}, err
	}
	value.TargetDigest, value.PayloadDigest, value.ObservedDescriptionDigest = digestFromBytes(target), digestFromBytes(payload), digestFromBytes(observed)
	return value, nil
}

func digestFromBytes(value []byte) effectport.Digest {
	if len(value) != 32 {
		return ""
	}
	return effectport.Digest("sha256:" + hex.EncodeToString(value))
}

type ContactDescriptionCompletionSink struct {
	store *ContactDescriptionIntentStore
}

func NewContactDescriptionCompletionSink(store *ContactDescriptionIntentStore) (*ContactDescriptionCompletionSink, error) {
	if store == nil {
		return nil, errors.New("contact description intent store is required")
	}
	return &ContactDescriptionCompletionSink{store: store}, nil
}

func (s *ContactDescriptionCompletionSink) CompleteEffect(ctx context.Context, effectRef string, envelope effectport.Envelope, _ effectport.Attempt, result effectport.AdapterResult) error {
	if s == nil || s.store == nil || envelope.Kind != effectport.KindWeComContactDescription || effectRef == "" || !effectport.ValidDigest(result.ReceiptDigest) {
		return errors.New("invalid contact description completion")
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	state, status, readback := string(result.Completion), "", ""
	var providerErrorCode *int64
	if result.Artifact.Kind == "wecom.contact.description.result.v1" {
		var value struct {
			Status   string `json:"status"`
			Readback string `json:"readback"`
		}
		if json.Unmarshal(result.Artifact.Payload, &value) != nil {
			return errors.New("invalid contact description result")
		}
		status, readback = value.Status, value.Readback
	} else if result.Artifact.Kind == "wecom.contact.description.reason.v1" {
		var value struct {
			Reason            string `json:"reason"`
			ProviderErrorCode *int64 `json:"provider_error_code,omitempty"`
		}
		if json.Unmarshal(result.Artifact.Payload, &value) != nil {
			return errors.New("invalid contact description final reason")
		}
		status, providerErrorCode = value.Reason, value.ProviderErrorCode
		switch status {
		case "relationship_unavailable", "target_changed", "dispatch_changed", "provider_rejected", "provider_disabled":
		default:
			return errors.New("invalid contact description final reason")
		}
		if (status != "provider_rejected" && providerErrorCode != nil) || (providerErrorCode != nil && (*providerErrorCode < -2147483648 || *providerErrorCode > 2147483647)) {
			return errors.New("invalid contact description provider error code")
		}
	}
	_, err = tx.Exec(ctx, `UPDATE outbound_wecom_contact_description_intents SET state=$2,result_status=NULLIF($3,''),readback_state=NULLIF($4,''),provider_error_code=$5,updated_at=clock_timestamp() WHERE effect_ref=$1`, effectRef, state, status, readback, providerErrorCode)
	return err
}

var _ outboundport.ContactDescriptionIntentWriter = (*ContactDescriptionIntentStore)(nil)
var _ ContactDescriptionDispatchReader = (*ContactDescriptionIntentStore)(nil)
var _ effectport.CompletionSink = (*ContactDescriptionCompletionSink)(nil)
var _ outboundport.ContactDescriptionRunStatusReader = (*ContactDescriptionIntentStore)(nil)
var _ outboundport.ContactDescriptionReadbackScheduler = (*ContactDescriptionIntentStore)(nil)
