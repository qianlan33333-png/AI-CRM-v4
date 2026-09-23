package externaleffects

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	platformjobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"github.com/riverqueue/river"
)

// Repository owns only the opaque external-effects tables. It never writes a
// customer, recipient, provider credential, or provider response.
type Repository struct {
	pool  *pgxpool.Pool
	river *river.Client[pgx.Tx]
	now   func() time.Time
	sink  port.CompletionSink
}

func (r *Repository) SetCompletionSink(sink port.CompletionSink) error {
	if r == nil || sink == nil || r.sink != nil {
		return ErrInvalid
	}
	r.sink = sink
	return nil
}

var _ port.Accepter = (*Repository)(nil)
var _ port.TransactionalAccepter = (*Repository)(nil)
var _ port.TransactionalCanceller = (*Repository)(nil)
var _ port.TransactionalReconciler = (*Repository)(nil)
var _ port.UnknownReconciler = (*Repository)(nil)
var _ port.ClientCompleter = (*Repository)(nil)

func (r *Repository) CancelQueuedEffectWithin(ctx context.Context, command port.CancelCommand) (port.Projection, error) {
	if r == nil || command.EffectID == "" || !port.ValidDigest(command.ReceiptKey) || command.ReasonCode == "" || len(command.ReasonCode) > 80 || !command.Actor.Valid() {
		return port.Projection{}, ErrInvalid
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return port.Projection{}, err
	}
	id, err := parseEffectID(command.EffectID)
	if err != nil {
		return port.Projection{}, err
	}
	var owner, kind, state string
	var attempts int32
	var generation int64
	var updated time.Time
	err = tx.QueryRow(ctx, `SELECT owner,kind,state,attempt_count,generation,updated_at FROM external_effects WHERE id=$1 FOR UPDATE`, id).Scan(&owner, &kind, &state, &attempts, &generation, &updated)
	if errors.Is(err, pgx.ErrNoRows) {
		return port.Projection{}, ErrNotFound
	}
	if err != nil {
		return port.Projection{}, err
	}
	commandDigest := port.Hash("cancel", command.EffectID, string(command.ReceiptKey), command.ReasonCode, strconv.FormatInt(command.Actor.AdminUserID, 10), command.Actor.SystemRef)
	var prior string
	priorErr := tx.QueryRow(ctx, `SELECT command_digest FROM external_effect_operation_receipts WHERE operation='cancel' AND effect_id=$1 AND receipt_key_digest=$2`, id, command.ReceiptKey).Scan(&prior)
	if priorErr == nil {
		if port.Digest(prior) != commandDigest || port.State(state) != port.StateCancelled {
			return port.Projection{}, ErrPayloadMismatch
		}
		return projection(id, port.Owner(owner), port.Kind(kind), port.State(state), attempts, generation, updated), nil
	}
	if !errors.Is(priorErr, pgx.ErrNoRows) {
		return port.Projection{}, priorErr
	}
	if port.State(state) != port.StateQueued {
		return port.Projection{}, ErrTransition
	}
	// The named system principal is reserved for the Payment due worker's
	// unsubmitted split cancellation. It cannot be used to control another
	// owner or kind merely because it satisfies the receipt schema.
	if command.Actor.System() && (port.Owner(owner) != port.OwnerPayment || port.Kind(kind) != port.KindWeChatPayProfitSharing) {
		return port.Projection{}, ErrInvalid
	}
	if err = tx.QueryRow(ctx, `UPDATE external_effects SET state='cancelled',updated_at=clock_timestamp() WHERE id=$1 AND state='queued' RETURNING updated_at`, id).Scan(&updated); err != nil {
		return port.Projection{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO external_effect_operation_receipts(operation,effect_id,receipt_key_digest,command_digest,actor_admin_user_id,actor_kind,actor_ref,state) VALUES('cancel',$1,$2,$3,NULLIF($4,0),NULLIF($5,''),NULLIF($6,''),'cancelled')`, id, command.ReceiptKey, commandDigest, command.Actor.AdminUserID, systemActorKind(command.Actor), command.Actor.SystemRef); err != nil {
		return port.Projection{}, err
	}
	return projection(id, port.Owner(owner), port.Kind(kind), port.StateCancelled, attempts, generation, updated), nil
}

func systemActorKind(actor port.ControlActor) string {
	if actor.System() {
		return "system"
	}
	return ""
}

func (r *Repository) CompleteClientEffectWithin(ctx context.Context, command port.ClientCompletionCommand) (port.Projection, error) {
	if r == nil || command.EffectID == "" || !port.ValidDigest(command.ReceiptKey) || !port.ValidDigest(command.EvidenceDigest) || (command.State != port.StateExecuted && command.State != port.StateUnknown && command.State != port.StateFinalFailed) {
		return port.Projection{}, ErrInvalid
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return port.Projection{}, err
	}
	id, err := parseEffectID(command.EffectID)
	if err != nil {
		return port.Projection{}, err
	}
	var owner, kind, state string
	var attempts int32
	var generation int64
	var updated time.Time
	err = tx.QueryRow(ctx, `SELECT owner,kind,state,attempt_count,generation,updated_at FROM external_effects WHERE id=$1 FOR UPDATE`, id).Scan(&owner, &kind, &state, &attempts, &generation, &updated)
	if errors.Is(err, pgx.ErrNoRows) {
		return port.Projection{}, ErrNotFound
	}
	if err != nil {
		return port.Projection{}, err
	}
	if port.Kind(kind) != port.KindSidebarJSSDKSend {
		return port.Projection{}, ErrInvalid
	}
	commandDigest := port.Hash("client-complete", command.EffectID, string(command.ReceiptKey), string(command.EvidenceDigest), string(command.State))
	var priorDigest, priorState string
	priorErr := tx.QueryRow(ctx, `SELECT command_digest,state FROM external_effect_operation_receipts WHERE operation='complete' AND effect_id=$1 AND receipt_key_digest=$2`, id, command.ReceiptKey).Scan(&priorDigest, &priorState)
	if priorErr == nil {
		if port.Digest(priorDigest) != commandDigest {
			return port.Projection{}, ErrPayloadMismatch
		}
		return projection(id, Owner(owner), Kind(kind), State(state), attempts, generation, updated), nil
	}
	if !errors.Is(priorErr, pgx.ErrNoRows) {
		return port.Projection{}, priorErr
	}
	if port.State(state) != port.StateQueued {
		return port.Projection{}, ErrTransition
	}
	attempts++
	callExecuted := command.State == port.StateExecuted || command.State == port.StateUnknown
	if _, err = tx.Exec(ctx, `INSERT INTO external_effect_attempts(effect_id,number,generation,fence,state,receipt_digest,evidence_digest,call_attempted,real_external_call_executed,completed_at) VALUES($1,$2,$3,0,$4,$5,$6,true,$7,clock_timestamp())`, id, attempts, generation, command.State, command.EvidenceDigest, command.EvidenceDigest, callExecuted); err != nil {
		return port.Projection{}, err
	}
	if err = tx.QueryRow(ctx, `UPDATE external_effects SET state=$2,attempt_count=$3,updated_at=clock_timestamp() WHERE id=$1 RETURNING updated_at`, id, command.State, attempts).Scan(&updated); err != nil {
		return port.Projection{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO external_effect_operation_receipts(operation,effect_id,receipt_key_digest,command_digest,state) VALUES('complete',$1,$2,$3,$4)`, id, command.ReceiptKey, commandDigest, command.State); err != nil {
		return port.Projection{}, err
	}
	return projection(id, Owner(owner), Kind(kind), command.State, attempts, generation, updated), nil
}

func (r *Repository) ReconcileUnknownWithin(ctx context.Context, command port.ReconcileCommand) error {
	if command.EffectID == "" || !ValidDigest(command.ReceiptKey) || !ValidDigest(command.EvidenceDigest) || command.ActorAdminUserID < 1 || command.Generation < 1 || command.Fence < 1 {
		return ErrInvalid
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	id, err := parseEffectID(command.EffectID)
	if err != nil {
		return err
	}
	var lease *time.Time
	if err = tx.QueryRow(ctx, `SELECT lease_expires_at FROM external_effects WHERE id=$1 AND state='outcome_unknown' AND generation=$2 AND lease_fence=$3`, id, command.Generation, command.Fence).Scan(&lease); err != nil || lease == nil {
		return ErrReconcileRequired
	}
	return r.ReconcileWithin(ctx, ControlCommand{EffectID: command.EffectID, ReceiptKey: command.ReceiptKey, EvidenceDigest: command.EvidenceDigest, ActorAdminUserID: command.ActorAdminUserID, Generation: command.Generation, Fence: command.Fence, LeaseExpiresAt: *lease})
}

func NewRepository(pool *pgxpool.Pool, client *river.Client[pgx.Tx]) (*Repository, error) {
	if pool == nil || client == nil {
		return nil, platformjobqueue.ErrUnavailable
	}
	return &Repository{pool: pool, river: client, now: time.Now}, nil
}

func effectID(id int64) string { return "eer_" + strconv.FormatInt(id, 10) }
func parseEffectID(value string) (int64, error) {
	if !strings.HasPrefix(value, "eer_") {
		return 0, ErrInvalid
	}
	digits := strings.TrimPrefix(value, "eer_")
	if digits == "" || digits[0] == '0' {
		return 0, ErrInvalid
	}
	for _, character := range digits {
		if character < '0' || character > '9' {
			return 0, ErrInvalid
		}
	}
	id, err := strconv.ParseInt(digits, 10, 64)
	if err != nil || id < 1 {
		return 0, ErrInvalid
	}
	return id, nil
}
func projection(id int64, owner Owner, kind Kind, state State, attempts int32, generation int64, updated time.Time) Projection {
	return Projection{ID: effectID(id), Owner: owner, Kind: kind, State: state, AttemptCount: attempts, Generation: generation, UpdatedAt: updated.UTC()}
}

func (r *Repository) AcceptAndQueue(ctx context.Context, command AcceptCommand) (Projection, Receipt, error) {
	if r == nil || r.pool == nil || !command.Valid() {
		return Projection{}, Receipt{}, ErrInvalid
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Projection{}, Receipt{}, err
	}
	defer tx.Rollback(ctx)
	return r.acceptAndQueueTx(ctx, tx, command, true)
}

// AcceptAndQueueWithin joins the caller's existing Unit of Work. It is the
// only supported way for a domain mutation to atomically persist its own
// receipt/audit/outbox facts and EER acceptance/River enqueue facts.
func (r *Repository) AcceptAndQueueWithin(ctx context.Context, command AcceptCommand) (Projection, Receipt, error) {
	if r == nil || !command.Valid() {
		return Projection{}, Receipt{}, ErrInvalid
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return Projection{}, Receipt{}, err
	}
	return r.acceptAndQueueTx(ctx, tx, command, false)
}

func (r *Repository) acceptAndQueueTx(ctx context.Context, tx pgx.Tx, command AcceptCommand, commit bool) (Projection, Receipt, error) {
	if tx == nil || !command.Valid() {
		return Projection{}, Receipt{}, ErrInvalid
	}
	var err error
	// Serialize only identical opaque acceptance keys. This makes the unique
	// acceptance receipt a deterministic replay/drift decision under races.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, command.ReceiptKey); err != nil {
		return Projection{}, Receipt{}, err
	}
	var id int64
	var owner, kind, state string
	var attempts int32
	var generation int64
	var updated time.Time
	var priorReceiptID int64
	var priorDigest, priorState string
	var priorCompleted time.Time
	priorErr := tx.QueryRow(ctx, `SELECT receipt.id,receipt.command_digest,receipt.state,receipt.completed_at,effect.id,effect.owner,effect.kind,effect.state,effect.attempt_count,effect.generation,effect.updated_at FROM external_effect_operation_receipts receipt JOIN external_effects effect ON effect.id=receipt.effect_id WHERE receipt.operation='accept' AND receipt.receipt_key_digest=$1 FOR UPDATE OF receipt,effect`, command.ReceiptKey).Scan(&priorReceiptID, &priorDigest, &priorState, &priorCompleted, &id, &owner, &kind, &state, &attempts, &generation, &updated)
	if priorErr == nil {
		if Digest(priorDigest) != command.Digest() {
			return Projection{}, Receipt{}, ErrPayloadMismatch
		}
		p := projection(id, Owner(owner), Kind(kind), State(state), attempts, generation, updated)
		p.QueueJobID, err = r.queueJobID(ctx, tx, id, generation)
		if err != nil {
			return Projection{}, Receipt{}, err
		}
		queueReceiptID, queueErr := r.queueReceiptID(ctx, tx, id)
		if queueErr != nil {
			return Projection{}, Receipt{}, queueErr
		}
		return finishAcceptance(tx, p, Receipt{ID: "eerop_" + strconv.FormatInt(priorReceiptID, 10), EffectID: effectID(id), CommandDigest: Digest(priorDigest), State: State(priorState), CompletedAt: priorCompleted, QueueReceiptID: queueReceiptID}, commit)
	}
	if !errors.Is(priorErr, pgx.ErrNoRows) {
		return Projection{}, Receipt{}, priorErr
	}
	err = tx.QueryRow(ctx, `SELECT id,owner,kind,state,attempt_count,generation,updated_at FROM external_effects WHERE envelope_fingerprint=$1 FOR UPDATE`, command.Envelope.Fingerprint()).Scan(&id, &owner, &kind, &state, &attempts, &generation, &updated)
	if err == nil {
		var rid int64
		var digest, receiptState string
		var completed time.Time
		receiptErr := tx.QueryRow(ctx, `SELECT id,command_digest,state,completed_at FROM external_effect_operation_receipts WHERE operation='accept' AND effect_id=$1 AND receipt_key_digest=$2`, id, command.ReceiptKey).Scan(&rid, &digest, &receiptState, &completed)
		if receiptErr != nil || Digest(digest) != command.Digest() {
			return Projection{}, Receipt{}, ErrPayloadMismatch
		}
		p := projection(id, Owner(owner), Kind(kind), State(state), attempts, generation, updated)
		p.QueueJobID, err = r.queueJobID(ctx, tx, id, generation)
		if err != nil {
			return Projection{}, Receipt{}, err
		}
		queueReceiptID, queueErr := r.queueReceiptID(ctx, tx, id)
		if queueErr != nil {
			return Projection{}, Receipt{}, queueErr
		}
		return finishAcceptance(tx, p, Receipt{ID: "eerop_" + strconv.FormatInt(rid, 10), EffectID: effectID(id), CommandDigest: Digest(digest), State: State(receiptState), CompletedAt: completed, QueueReceiptID: queueReceiptID}, commit)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Projection{}, Receipt{}, err
	}
	err = tx.QueryRow(ctx, `INSERT INTO external_effects (owner,kind,source_ref_digest,target_ref_digest,payload_digest,policy_version_hash,envelope_fingerprint,delivery_lane,state) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'accepted') RETURNING id,created_at`, command.Envelope.Owner, command.Envelope.Kind, command.Envelope.SourceRefDigest, command.Envelope.TargetRefDigest, command.Envelope.PayloadDigest, command.Envelope.PolicyVersionHash, command.Envelope.Fingerprint(), command.Lane).Scan(&id, &updated)
	if err != nil {
		return Projection{}, Receipt{}, err
	}
	var acceptReceiptID int64
	var acceptCompleted time.Time
	if err = tx.QueryRow(ctx, `INSERT INTO external_effect_operation_receipts(operation,effect_id,receipt_key_digest,command_digest,state) VALUES ('accept',$1,$2,$3,'accepted') RETURNING id,completed_at`, id, command.ReceiptKey, command.Digest()).Scan(&acceptReceiptID, &acceptCompleted); err != nil {
		return Projection{}, Receipt{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO external_effect_generations(effect_id,generation) VALUES ($1,1)`, id); err != nil {
		return Projection{}, Receipt{}, err
	}
	queue := effectQueue(command.Envelope.Kind, command.Lane)
	inserted, err := platformjobqueue.InsertTxWithOptions(ctx, r.river, tx, EffectJobArgs{EffectID: id, Generation: 1}, river.InsertOpts{Queue: queue, ScheduledAt: command.ScheduledAt.UTC()})
	if err != nil {
		return Projection{}, Receipt{}, err
	}
	queueKey := Hash("queue", effectID(id))
	queueDigest := Hash("queue", effectID(id), strconv.FormatInt(inserted.Job.ID, 10))
	if _, err = tx.Exec(ctx, `UPDATE external_effects SET state='queued',updated_at=clock_timestamp() WHERE id=$1`, id); err != nil {
		return Projection{}, Receipt{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO external_effect_jobs(effect_id,generation,river_job_id,queue,args_digest,scheduled_at) VALUES ($1,1,$2,$3,$4,$5)`, id, inserted.Job.ID, queue, Hash("river-args", effectID(id), "1"), inserted.Job.ScheduledAt); err != nil {
		return Projection{}, Receipt{}, err
	}
	// The queue receipt is a distinct audit fact. The caller always receives
	// the accept receipt so first delivery and an Idempotency-Key replay are
	// byte-for-byte the same response-level acknowledgement.
	var queueReceiptID int64
	if err = tx.QueryRow(ctx, `INSERT INTO external_effect_operation_receipts(operation,effect_id,receipt_key_digest,command_digest,state) VALUES ('queue',$1,$2,$3,'queued') RETURNING id`, id, queueKey, queueDigest).Scan(&queueReceiptID); err != nil {
		return Projection{}, Receipt{}, err
	}
	var final time.Time
	if err = tx.QueryRow(ctx, `SELECT updated_at FROM external_effects WHERE id=$1`, id).Scan(&final); err != nil {
		return Projection{}, Receipt{}, err
	}
	p := projection(id, command.Envelope.Owner, command.Envelope.Kind, StateQueued, 0, 1, final)
	p.QueueJobID = inserted.Job.ID
	return finishAcceptance(tx, p, Receipt{ID: "eerop_" + strconv.FormatInt(acceptReceiptID, 10), EffectID: effectID(id), CommandDigest: command.Digest(), State: StateAccepted, CompletedAt: acceptCompleted, QueueReceiptID: "eerop_" + strconv.FormatInt(queueReceiptID, 10)}, commit)
}

func (r *Repository) queueJobID(ctx context.Context, tx pgx.Tx, effectID, generation int64) (int64, error) {
	var id int64
	err := tx.QueryRow(ctx, `SELECT river_job_id FROM external_effect_jobs WHERE effect_id=$1 AND generation=$2`, effectID, generation).Scan(&id)
	return id, err
}

func (r *Repository) queueReceiptID(ctx context.Context, tx pgx.Tx, effectID int64) (string, error) {
	var id int64
	err := tx.QueryRow(ctx, `SELECT id FROM external_effect_operation_receipts WHERE operation='queue' AND effect_id=$1`, effectID).Scan(&id)
	if err != nil {
		return "", err
	}
	return "eerop_" + strconv.FormatInt(id, 10), nil
}

func finishAcceptance(tx pgx.Tx, p Projection, receipt Receipt, commit bool) (Projection, Receipt, error) {
	if !commit {
		return p, receipt, nil
	}
	return commitProjection(tx, p, receipt)
}

func commitProjection(tx pgx.Tx, p Projection, receipt Receipt) (Projection, Receipt, error) {
	if err := tx.Commit(context.Background()); err != nil {
		return Projection{}, Receipt{}, err
	}
	return p, receipt, nil
}

func (r *Repository) List(ctx context.Context, limit int) ([]Projection, error) {
	if limit < 1 || limit > 100 {
		return nil, ErrInvalid
	}
	rows, err := r.pool.Query(ctx, `SELECT id,owner,kind,state,attempt_count,generation,updated_at FROM external_effects ORDER BY updated_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Projection{}
	for rows.Next() {
		var id int64
		var owner, kind, state string
		var attempts int32
		var generation int64
		var updated time.Time
		if err = rows.Scan(&id, &owner, &kind, &state, &attempts, &generation, &updated); err != nil {
			return nil, err
		}
		out = append(out, projection(id, Owner(owner), Kind(kind), State(state), attempts, generation, updated))
	}
	return out, rows.Err()
}
func (r *Repository) Get(ctx context.Context, id string) (Projection, error) {
	numeric, err := parseEffectID(id)
	if err != nil {
		return Projection{}, err
	}
	var owner, kind, state string
	var attempts int32
	var generation int64
	var updated time.Time
	err = r.pool.QueryRow(ctx, `SELECT owner,kind,state,attempt_count,generation,updated_at FROM external_effects WHERE id=$1`, numeric).Scan(&owner, &kind, &state, &attempts, &generation, &updated)
	if errors.Is(err, pgx.ErrNoRows) {
		return Projection{}, ErrNotFound
	}
	return projection(numeric, Owner(owner), Kind(kind), State(state), attempts, generation, updated), err
}

func (r *Repository) ReconciliationCandidate(ctx context.Context, id string) (port.ReconciliationCandidate, error) {
	numeric, err := parseEffectID(id)
	if err != nil {
		return port.ReconciliationCandidate{}, err
	}
	var owner, kind, state string
	var attempts int32
	var generation, fence int64
	var leaseExpires *time.Time
	var updated time.Time
	err = r.pool.QueryRow(ctx, `SELECT owner,kind,state,attempt_count,generation,lease_fence,lease_expires_at,updated_at FROM external_effects WHERE id=$1`, numeric).Scan(&owner, &kind, &state, &attempts, &generation, &fence, &leaseExpires, &updated)
	if errors.Is(err, pgx.ErrNoRows) {
		return port.ReconciliationCandidate{}, port.ErrReconciliationNotFound
	}
	if err != nil {
		return port.ReconciliationCandidate{}, err
	}
	if State(state) != StateUnknown || generation < 1 || fence < 1 || leaseExpires == nil {
		return port.ReconciliationCandidate{}, port.ErrReconciliationConflict
	}
	return port.ReconciliationCandidate{Projection: projection(numeric, Owner(owner), Kind(kind), State(state), attempts, generation, updated), Fence: fence, LeaseExpiresAt: leaseExpires.UTC()}, nil
}

func (r *Repository) ReconcileEffectWithin(ctx context.Context, command port.ReconcileCommand) (port.Projection, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return port.Projection{}, err
	}
	projection, _, err := r.controlWithin(ctx, tx, ControlCommand{
		EffectID: command.EffectID, ReceiptKey: command.ReceiptKey, EvidenceDigest: command.EvidenceDigest,
		ActorAdminUserID: command.ActorAdminUserID, Generation: command.Generation, Fence: command.Fence, LeaseExpiresAt: command.LeaseExpiresAt,
	}, "reconcile")
	if errors.Is(err, ErrNotFound) {
		return port.Projection{}, port.ErrReconciliationNotFound
	}
	if errors.Is(err, ErrReconcileRequired) || errors.Is(err, ErrTransition) || errors.Is(err, ErrPayloadMismatch) {
		return port.Projection{}, port.ErrReconciliationConflict
	}
	return projection, err
}
func (r *Repository) Diagnostics(ctx context.Context) (map[string]int64, error) {
	stats, err := r.pushStats(ctx)
	return map[string]int64{"accepted": stats.Accepted, "queued": stats.Queued, "attempted": stats.Attempted, "executed": stats.Sent, "outcome_unknown": stats.Unknown, "reconciled": stats.Reconciled, "retryable_failed": stats.Retryable, "final_failed": stats.FinalFailed, "cancelled": stats.Cancelled, "total": stats.Total}, err
}

type PushStats struct {
	Total, Accepted, Queued, Attempted, Sent, Unknown, Reconciled, Retryable, FinalFailed, Cancelled int64
}

func (r *Repository) pushStats(ctx context.Context) (PushStats, error) {
	var stats PushStats
	err := r.pool.QueryRow(ctx, `SELECT
        count(*),
        count(*) FILTER (WHERE state='accepted'),
        count(*) FILTER (WHERE state='queued'),
        count(*) FILTER (WHERE state='attempted'),
        count(*) FILTER (WHERE state='executed'),
        count(*) FILTER (WHERE state='outcome_unknown'),
        count(*) FILTER (WHERE state='reconciled'),
        count(*) FILTER (WHERE state='retryable_failed'),
        count(*) FILTER (WHERE state='final_failed'),
        count(*) FILTER (WHERE state='cancelled')
      FROM external_effects`).Scan(&stats.Total, &stats.Accepted, &stats.Queued, &stats.Attempted, &stats.Sent, &stats.Unknown, &stats.Reconciled, &stats.Retryable, &stats.FinalFailed, &stats.Cancelled)
	return stats, err
}

func (r *Repository) control(ctx context.Context, command ControlCommand, operation string) (Projection, Receipt, error) {
	if r == nil || r.pool == nil {
		return Projection{}, Receipt{}, ErrInvalid
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Projection{}, Receipt{}, err
	}
	defer tx.Rollback(ctx)
	projection, receipt, err := r.controlWithin(ctx, tx, command, operation)
	if err != nil {
		return Projection{}, Receipt{}, err
	}
	return commitProjection(tx, projection, receipt)
}

// ReconcileWithin joins the Group Ops Unit of Work. It is deliberately the
// only EER control operation exposed through this composition seam: Group Ops
// must commit its execution projection and the EER reconciled receipt in one
// PostgreSQL transaction, without importing the EER store package.
func (r *Repository) ReconcileWithin(ctx context.Context, command ControlCommand) error {
	if r == nil {
		return ErrInvalid
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	_, _, err = r.controlWithin(ctx, tx, command, "reconcile")
	return err
}

func (r *Repository) controlWithin(ctx context.Context, tx pgx.Tx, command ControlCommand, operation string) (Projection, Receipt, error) {
	if r == nil || tx == nil || !command.Valid() || (operation == "reconcile" && !ValidDigest(command.EvidenceDigest)) {
		return Projection{}, Receipt{}, ErrInvalid
	}
	id, err := parseEffectID(command.EffectID)
	if err != nil {
		return Projection{}, Receipt{}, err
	}
	var owner, kind, state, source, target, payload, policy string
	var attempts int32
	var generation, fence int64
	var leaseExpires *time.Time
	var updated time.Time
	var lane port.Lane
	err = tx.QueryRow(ctx, `SELECT owner,kind,state,source_ref_digest,target_ref_digest,payload_digest,policy_version_hash,attempt_count,generation,lease_fence,lease_expires_at,updated_at,delivery_lane FROM external_effects WHERE id=$1 FOR UPDATE`, id).Scan(&owner, &kind, &state, &source, &target, &payload, &policy, &attempts, &generation, &fence, &leaseExpires, &updated, &lane)
	if errors.Is(err, pgx.ErrNoRows) {
		return Projection{}, Receipt{}, ErrNotFound
	}
	if err != nil {
		return Projection{}, Receipt{}, err
	}
	if operation == "reconcile" && Kind(kind) == KindOutboundMedia && lane == port.LaneOutboundMedia {
		switch command.ReconciliationOutcome {
		case "no_effect":
			if command.ReconciliationArtifact.Kind != "" || len(command.ReconciliationArtifact.Payload) != 0 || command.ReconciliationArtifact.Digest != "" {
				return Projection{}, Receipt{}, ErrReconcileRequired
			}
		case "confirmed_effect":
			if !command.ReconciliationArtifact.Valid() || command.ReconciliationArtifact.Kind != "outbound.material.upload.v1" {
				return Projection{}, Receipt{}, ErrReconcileRequired
			}
		default:
			return Projection{}, Receipt{}, ErrReconcileRequired
		}
	}
	if operation == "reconcile" && (command.Generation != 0 || command.Fence != 0 || !command.LeaseExpiresAt.IsZero()) {
		if command.Generation < 1 || command.Fence < 1 || command.LeaseExpiresAt.IsZero() || generation != command.Generation || fence != command.Fence || leaseExpires == nil || !leaseExpires.Equal(command.LeaseExpiresAt.UTC()) || leaseExpires.After(time.Now().UTC()) {
			return Projection{}, Receipt{}, ErrReconcileRequired
		}
	}
	digest := command.Digest(operation)
	var rid int64
	var oldDigest, oldState string
	var oldActor *int64
	var completed time.Time
	receiptErr := tx.QueryRow(ctx, `SELECT id,command_digest,actor_admin_user_id,state,completed_at FROM external_effect_operation_receipts WHERE operation=$1 AND effect_id=$2 AND receipt_key_digest=$3`, operation, id, command.ReceiptKey).Scan(&rid, &oldDigest, &oldActor, &oldState, &completed)
	if receiptErr == nil {
		if Digest(oldDigest) != digest {
			return Projection{}, Receipt{}, ErrPayloadMismatch
		}
		return projection(id, Owner(owner), Kind(kind), State(state), attempts, generation, updated), Receipt{ID: "eerop_" + strconv.FormatInt(rid, 10), EffectID: command.EffectID, CommandDigest: Digest(oldDigest), ActorAdminUserID: oldActor, State: State(oldState), CompletedAt: completed}, nil
	}
	if !errors.Is(receiptErr, pgx.ErrNoRows) {
		return Projection{}, Receipt{}, receiptErr
	}
	var next State
	switch operation {
	case "cancel":
		if State(state) != StateAccepted && State(state) != StateQueued {
			return Projection{}, Receipt{}, ErrTransition
		}
		next = StateCancelled
	case "retry":
		if State(state) != StateRetryable {
			return Projection{}, Receipt{}, ErrTransition
		}
		next = StateQueued
		generation++
	case "reconcile":
		if State(state) != StateUnknown {
			return Projection{}, Receipt{}, ErrReconcileRequired
		}
		next = StateReconciled
	default:
		return Projection{}, Receipt{}, ErrInvalid
	}
	if operation == "retry" {
		if _, err = tx.Exec(ctx, `INSERT INTO external_effect_generations(effect_id,generation) VALUES($1,$2)`, id, generation); err != nil {
			return Projection{}, Receipt{}, err
		}
		queue := effectQueue(Kind(kind), lane)
		inserted, insertErr := platformjobqueue.InsertTxWithOptions(ctx, r.river, tx, EffectJobArgs{EffectID: id, Generation: generation}, river.InsertOpts{Queue: queue})
		if insertErr != nil {
			return Projection{}, Receipt{}, insertErr
		}
		if _, err = tx.Exec(ctx, `INSERT INTO external_effect_jobs(effect_id,generation,river_job_id,queue,args_digest,scheduled_at) VALUES ($1,$2,$3,$4,$5,clock_timestamp())`, id, generation, inserted.Job.ID, queue, Hash("river-args", command.EffectID, strconv.FormatInt(generation, 10))); err != nil {
			return Projection{}, Receipt{}, err
		}
	}
	if operation == "reconcile" {
		// The lease-expiry guard belongs only to the fenced Group Ops
		// reconciliation contract. Existing admin reconciliation commands omit
		// generation/fence/lease and must retain their original semantics.
		var requiredExpiredLease any
		if command.Generation != 0 || command.Fence != 0 || !command.LeaseExpiresAt.IsZero() {
			requiredExpiredLease = leaseExpires
		}
		result, updateErr := tx.Exec(ctx, `UPDATE external_effect_attempts SET state='reconciled',evidence_digest=$2,completed_at=clock_timestamp() WHERE effect_id=$1 AND generation=$3 AND fence=$4 AND state='outcome_unknown' AND ($5::timestamptz IS NULL OR $5 <= clock_timestamp())`, id, command.EvidenceDigest, generation, fence, requiredExpiredLease)
		if updateErr != nil {
			return Projection{}, Receipt{}, updateErr
		}
		if result.RowsAffected() != 1 {
			return Projection{}, Receipt{}, ErrReconcileRequired
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE external_effects SET state=$2,generation=$3,updated_at=clock_timestamp() WHERE id=$1`, id, next, generation); err != nil {
		return Projection{}, Receipt{}, err
	}
	err = tx.QueryRow(ctx, `INSERT INTO external_effect_operation_receipts(operation,effect_id,receipt_key_digest,command_digest,actor_admin_user_id,state) VALUES ($1,$2,$3,$4,$5,$6) RETURNING id,completed_at`, operation, id, command.ReceiptKey, digest, command.ActorAdminUserID, next).Scan(&rid, &completed)
	if err != nil {
		return Projection{}, Receipt{}, err
	}
	if err = tx.QueryRow(ctx, `SELECT updated_at FROM external_effects WHERE id=$1`, id).Scan(&updated); err != nil {
		return Projection{}, Receipt{}, err
	}
	// Group Ops owns its manual-reconciliation projection in RuntimeService;
	// projecting it here as well would update the same execution twice. AI
	// Assistant and Automation Operations delegate their owner projection to
	// the sink.
	if (Kind(kind) == KindWeComTagCatalog || Kind(kind) == KindWeComTagCatalogMutation || Kind(kind) == KindOutboundMessage || (Kind(kind) == KindOutboundMedia && lane == port.LaneOutboundMedia) || Kind(kind) == KindAutomationMessage || Kind(kind) == KindAIAgentGenerate || Kind(kind) == KindAIRecommend || Kind(kind) == KindFeishuOpsNotification || Kind(kind) == KindInvitationCode) && r.sink != nil {
		envelope := Envelope{Owner: Owner(owner), Kind: Kind(kind), SourceRefDigest: Digest(source), TargetRefDigest: Digest(target), PayloadDigest: Digest(payload), PolicyVersionHash: Digest(policy)}
		completion := AdapterResult{Completion: next, ReceiptDigest: digest}
		if Kind(kind) == KindOutboundMedia && lane == port.LaneOutboundMedia {
			if command.ReconciliationOutcome == "no_effect" {
				completion.FailureCode = "reconciled_no_effect"
			} else {
				completion.Completion = StateExecuted
				completion.Artifact = command.ReconciliationArtifact
			}
		}
		if err = r.sink.CompleteEffect(platformpostgres.BindTransaction(ctx, tx), effectID(id), envelope, Attempt{EffectID: effectID(id), Number: attempts, Generation: generation, Fence: fence}, completion); err != nil {
			return Projection{}, Receipt{}, err
		}
	}
	actor := command.ActorAdminUserID
	return projection(id, Owner(owner), Kind(kind), next, attempts, generation, updated), Receipt{ID: "eerop_" + strconv.FormatInt(rid, 10), EffectID: command.EffectID, CommandDigest: digest, ActorAdminUserID: &actor, State: next, CompletedAt: completed}, nil
}

// effectQueue keeps dispatch ownership in the shared External Effects kernel.
// Welcome intents get a registered queue so ordinary outbound backlog cannot
// consume their short provider-call window; worker, claim, receipt, retry and
// reconciliation semantics remain exactly the same.
func effectQueue(kind Kind, lane port.Lane) string {
	if kind == KindFeishuOpsNotification && lane == port.LaneOpsNotification {
		return platformjobqueue.OpsNotificationQueue
	}
	if lane == port.LaneOutboundExcel {
		return platformjobqueue.OutboundExcelQueue
	}
	if lane == port.LaneOutboundMedia {
		return platformjobqueue.OutboundMediaQueue
	}
	if kind == KindChannelWelcome {
		return platformjobqueue.OutboundWelcomeQueue
	}
	return platformjobqueue.OutboundQueue
}
func (r *Repository) Cancel(ctx context.Context, c ControlCommand) (Projection, Receipt, error) {
	return r.control(ctx, c, "cancel")
}
func (r *Repository) Retry(ctx context.Context, c ControlCommand) (Projection, Receipt, error) {
	return r.control(ctx, c, "retry")
}
func (r *Repository) Reconcile(ctx context.Context, c ControlCommand) (Projection, Receipt, error) {
	return r.control(ctx, c, "reconcile")
}

// RunLocalAttempt records attempted before the provider boundary. With the
// default-disabled provider it deterministically ends final_failed and makes no
// external call. A future outbound adapter may replace only that post-commit boundary.
// RunAttempt first atomically proves this exact River job owns the queued
// generation, records attempted, commits, and only then calls the adapter.
// A nil adapter is the default-disabled provider: it final-fails locally
// without a provider call. A provider error becomes outcome_unknown only when
// the adapter confirms a call was attempted; a pre-call failure is retryable.
func (r *Repository) RunAttempt(ctx context.Context, id, generation, riverJobID int64, adapter ProviderAdapter) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var state, owner, kind, source, target, payload, policy string
	var attempts int32
	var fence int64
	var leaseExpires *time.Time
	var deliveryQueue string
	err = tx.QueryRow(ctx, `SELECT effect.state,effect.owner,effect.kind,effect.source_ref_digest,effect.target_ref_digest,effect.payload_digest,effect.policy_version_hash,effect.attempt_count,effect.lease_fence,effect.lease_expires_at,job.queue FROM external_effects effect JOIN external_effect_jobs job ON job.effect_id=effect.id AND job.generation=effect.generation WHERE effect.id=$1 AND effect.generation=$2 AND job.river_job_id=$3 FOR UPDATE OF effect`, id, generation, riverJobID).Scan(&state, &owner, &kind, &source, &target, &payload, &policy, &attempts, &fence, &leaseExpires, &deliveryQueue)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if State(state) == StateAttempted {
		// A process may have died after committing attempted and before it could
		// record a provider outcome. Never turn that replay into another call:
		// wait for the original lease, then fail closed to unknown.
		if leaseExpires != nil && leaseExpires.After(r.now()) {
			delay := time.Until(*leaseExpires)
			if delay < time.Second {
				delay = time.Second
			}
			return river.JobSnooze(delay)
		}
		recovery := Hash("attempt-lease-expired", effectID(id), strconv.FormatInt(generation, 10), strconv.Itoa(int(attempts)), strconv.FormatInt(fence, 10))
		if result, updateErr := tx.Exec(ctx, `UPDATE external_effect_attempts SET state='outcome_unknown',receipt_digest=$5,completed_at=clock_timestamp() WHERE effect_id=$1 AND number=$2 AND generation=$3 AND fence=$4 AND state='attempted'`, id, attempts, generation, fence, recovery); updateErr != nil || result.RowsAffected() != 1 {
			if updateErr != nil {
				return updateErr
			}
			return ErrTransition
		}
		if result, updateErr := tx.Exec(ctx, `UPDATE external_effects SET state='outcome_unknown',updated_at=clock_timestamp() WHERE id=$1 AND generation=$2 AND lease_fence=$3 AND state='attempted' AND (lease_expires_at IS NULL OR lease_expires_at <= clock_timestamp())`, id, generation, fence); updateErr != nil || result.RowsAffected() != 1 {
			if updateErr != nil {
				return updateErr
			}
			return ErrTransition
		}
		if projectsStaleAttempt(Kind(kind)) && r.sink != nil {
			envelope := Envelope{Owner: Owner(owner), Kind: Kind(kind), SourceRefDigest: Digest(source), TargetRefDigest: Digest(target), PayloadDigest: Digest(payload), PolicyVersionHash: Digest(policy)}
			completion := AdapterResult{Completion: StateUnknown, ReceiptDigest: recovery, CallAttempted: true}
			// Sidebar JSSDK effects are browser-owned: an expired worker lease is
			// not a server-side Provider attempt, but the intent must still become
			// unknown so a fresh browser key cannot receive a new SDK grant.
			if envelope.Kind == port.KindSidebarJSSDKSend {
				completion.CallAttempted = false
			}
			if err = r.sink.CompleteEffect(platformpostgres.BindTransaction(ctx, tx), effectID(id), envelope, Attempt{EffectID: effectID(id), Number: attempts, Generation: generation, Fence: fence}, completion); err != nil {
				return err
			}
		}
		return tx.Commit(ctx)
	}
	if State(state) != StateQueued {
		return nil
	}
	attempts++
	fence++
	if result, updateErr := tx.Exec(ctx, `UPDATE external_effects SET state='attempted',attempt_count=$2,lease_fence=$3,lease_expires_at=clock_timestamp()+interval '5 minutes',updated_at=clock_timestamp() WHERE id=$1 AND generation=$4 AND state='queued'`, id, attempts, fence, generation); updateErr != nil || result.RowsAffected() != 1 {
		if updateErr != nil {
			return updateErr
		}
		return ErrTransition
	}
	if _, err = tx.Exec(ctx, `INSERT INTO external_effect_attempts(effect_id,number,generation,fence,state) VALUES($1,$2,$3,$4,'attempted')`, id, attempts, generation, fence); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	var next State
	var receipt Digest
	var callAttempted, realExternalCallExecuted bool
	var adapterResult AdapterResult
	var callErr error
	owner, kind, source, target, payload, policy = "", "", "", "", "", ""
	if err := r.pool.QueryRow(ctx, `SELECT owner,kind,source_ref_digest,target_ref_digest,payload_digest,policy_version_hash FROM external_effects WHERE id=$1 AND generation=$2`, id, generation).Scan(&owner, &kind, &source, &target, &payload, &policy); err != nil {
		return err
	}
	envelope := Envelope{Owner: Owner(owner), Kind: Kind(kind), SourceRefDigest: Digest(source), TargetRefDigest: Digest(target), PayloadDigest: Digest(payload), PolicyVersionHash: Digest(policy)}
	if adapter == nil {
		next = StateFinalFailed // Provider disabled: no call was attempted.
		receipt = Hash("provider-disabled", strconv.FormatInt(id, 10), strconv.Itoa(int(attempts)))
	} else {
		adapterResult, callErr = adapter.Execute(ctx, envelope, Attempt{EffectID: effectID(id), Number: attempts, Generation: generation, Fence: fence})
		result := adapterResult
		callAttempted, realExternalCallExecuted = result.CallAttempted, result.RealExternalCallExecuted
		if callErr != nil {
			if result.CallAttempted {
				next = StateUnknown
			} else {
				next = StateRetryable
			}
			receipt = Hash("provider-error", strconv.FormatInt(id, 10), strconv.Itoa(int(attempts)))
		} else if !ValidDigest(result.ReceiptDigest) || (result.RealExternalCallExecuted && !result.CallAttempted) {
			if result.CallAttempted {
				next = StateUnknown
			} else {
				next = StateFinalFailed
			}
			receipt = Hash("provider-invalid", strconv.FormatInt(id, 10), strconv.Itoa(int(attempts)))
		} else if result.Completion == StateExecuted && result.CallAttempted && result.RealExternalCallExecuted {
			next = StateExecuted
			receipt = result.ReceiptDigest
		} else if result.Completion == StateExecuted && envelope.Kind == KindWeComContactDescription &&
			result.CallAttempted && !result.RealExternalCallExecuted && result.Artifact.Valid() {
			// Description effects have a compare-before-write contract. A live
			// read can prove already_present, too_long, or a changed snapshot;
			// each is a terminal business result without fabricating a write.
			next = StateExecuted
			receipt = result.ReceiptDigest
			// A browser-owned sidebar send can lose its one-time client receipt after
			// the SDK may have been invoked. Preserve that distinct unknown result
			// without recording a server-side Provider call that did not occur.
		} else if result.Completion == StateUnknown && (result.CallAttempted || envelope.Kind == port.KindSidebarJSSDKSend) {
			next = StateUnknown
			receipt = result.ReceiptDigest
		} else if result.Completion == StateRetryable && (!result.CallAttempted || (((envelope.Kind == KindOutboundMedia && deliveryQueue == platformjobqueue.OutboundMediaQueue) || ((envelope.Kind == KindOutboundMessage || envelope.Kind == KindAutomationMessage) && deliveryQueue == platformjobqueue.OutboundExcelQueue)) && result.CallAttempted && !result.RealExternalCallExecuted && result.SafeToRetryRejected)) {
			next = StateRetryable
			receipt = result.ReceiptDigest
		} else if result.Completion == StateFinalFailed {
			next = StateFinalFailed
			receipt = result.ReceiptDigest
		} else {
			if result.CallAttempted {
				next = StateUnknown
			} else {
				next = StateFinalFailed
			}
			receipt = Hash("provider-invalid", strconv.FormatInt(id, 10), strconv.Itoa(int(attempts)))
		}
	}
	if next == StateExecuted && (envelope.Kind == KindOutboundMedia || envelope.Kind == KindWeComTagCatalog || envelope.Kind == KindWeComTagCatalogMutation || envelope.Kind == KindChannelAsset || envelope.Kind == KindChannelLink || envelope.Kind == KindCustomerOwnerHandoff || (envelope.Kind == KindAIAgentGenerate || envelope.Kind == KindAIRecommend)) && (r.sink == nil || !adapterResult.Artifact.Valid()) {
		next, receipt = StateUnknown, Hash("provider-artifact-invalid", strconv.FormatInt(id, 10), strconv.Itoa(int(attempts)))
	}
	autoRetryLane := (envelope.Kind == KindOutboundMedia && deliveryQueue == platformjobqueue.OutboundMediaQueue) ||
		((envelope.Kind == KindOutboundMessage || envelope.Kind == KindAutomationMessage) && deliveryQueue == platformjobqueue.OutboundExcelQueue) ||
		(envelope.Kind == KindFeishuOpsNotification && deliveryQueue == platformjobqueue.OpsNotificationQueue && !callAttempted)
	autoRetry := autoRetryLane && next == StateRetryable && attempts < 5
	persistedState := next
	if autoRetry {
		persistedState = StateQueued
	}
	tx, err = r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if result, updateErr := tx.Exec(ctx, `UPDATE external_effect_attempts SET state=$2,receipt_digest=$3,call_attempted=$4,real_external_call_executed=$5,completed_at=clock_timestamp() WHERE effect_id=$1 AND number=$6 AND generation=$7 AND fence=$8 AND state='attempted'`, id, next, receipt, callAttempted, realExternalCallExecuted, attempts, generation, fence); updateErr != nil || result.RowsAffected() != 1 {
		if updateErr != nil {
			return updateErr
		}
		return ErrTransition
	}
	if result, updateErr := tx.Exec(ctx, `UPDATE external_effects SET state=$2,updated_at=clock_timestamp() WHERE id=$1 AND generation=$3 AND lease_fence=$4 AND state='attempted'`, id, persistedState, generation, fence); updateErr != nil || result.RowsAffected() != 1 {
		if updateErr != nil {
			return updateErr
		}
		return ErrTransition
	}
	terminal := next == StateExecuted || next == StateUnknown || next == StateRetryable || next == StateFinalFailed
	shouldComplete := r.sink != nil && terminal && (envelope.Kind == KindOutboundMedia || envelope.Kind == KindGroupMessage || envelope.Kind == KindWeComTagCatalog || envelope.Kind == KindWeComTagCatalogMutation || envelope.Kind == KindWeComContactDescription || envelope.Kind == KindChannelAsset || envelope.Kind == KindChannelWelcome || envelope.Kind == KindChannelEntryTag || envelope.Kind == KindCustomerTagCommand || envelope.Kind == KindCustomerOwnerHandoff || envelope.Kind == KindCommerceProductPush || envelope.Kind == KindInvitationCode || envelope.Kind == KindChannelLink || envelope.Kind == KindOutboundMessage || envelope.Kind == KindAutomationMessage || envelope.Kind == port.KindSidebarJSSDKSend || envelope.Kind == KindSurveyCompletion || envelope.Kind == KindAIAgentGenerate || envelope.Kind == KindAIRecommend || envelope.Owner == OwnerPayment || envelope.Kind == KindFeishuOpsNotification)
	if shouldComplete {
		completionResult := adapterResult
		completionResult.Completion = persistedState
		if !ValidDigest(completionResult.ReceiptDigest) {
			completionResult.ReceiptDigest = receipt
		}
		if err = r.sink.CompleteEffect(platformpostgres.BindTransaction(ctx, tx), effectID(id), envelope, Attempt{EffectID: effectID(id), Number: attempts, Generation: generation, Fence: fence}, completionResult); err != nil {
			return err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	if autoRetry {
		return river.JobSnooze(providerAutomaticRetryDelay(attempts))
	}
	return nil
}

func providerAutomaticRetryDelay(attempt int32) time.Duration {
	switch attempt {
	case 1:
		return time.Second
	case 2:
		return 5 * time.Second
	case 3:
		return 30 * time.Second
	default:
		return 2 * time.Minute
	}
}

// QueuedEnvelope is the read-only preflight view. RunAttempt still performs
// the authoritative queued/generation/job CAS after preflight completes.
func (r *Repository) QueuedEnvelope(ctx context.Context, id, generation, riverJobID int64) (Envelope, bool, error) {
	if r == nil || r.pool == nil || id < 1 || generation < 1 || riverJobID < 1 {
		return Envelope{}, false, ErrInvalid
	}
	var state, owner, kind, source, target, payload, policy string
	err := r.pool.QueryRow(ctx, `SELECT effect.state,effect.owner,effect.kind,effect.source_ref_digest,effect.target_ref_digest,effect.payload_digest,effect.policy_version_hash FROM external_effects effect JOIN external_effect_jobs job ON job.effect_id=effect.id AND job.generation=effect.generation WHERE effect.id=$1 AND effect.generation=$2 AND job.river_job_id=$3`, id, generation, riverJobID).Scan(&state, &owner, &kind, &source, &target, &payload, &policy)
	if errors.Is(err, pgx.ErrNoRows) {
		return Envelope{}, false, nil
	}
	if err != nil {
		return Envelope{}, false, err
	}
	if State(state) != StateQueued {
		return Envelope{}, false, nil
	}
	envelope := Envelope{Owner: Owner(owner), Kind: Kind(kind), SourceRefDigest: Digest(source), TargetRefDigest: Digest(target), PayloadDigest: Digest(payload), PolicyVersionHash: Digest(policy)}
	if !envelope.Valid() {
		return Envelope{}, false, ErrInvalid
	}
	return envelope, true, nil
}

func projectsStaleAttempt(kind Kind) bool {
	switch kind {
	case KindFeishuOpsNotification, KindInvitationCode, KindOutboundMedia, KindWeComTagCatalog, KindWeComTagCatalogMutation, KindGroupMessage, KindChannelAsset, KindOutboundMessage, KindAutomationMessage, KindSurveyCompletion, KindCustomerTagCommand, KindCustomerOwnerHandoff, KindCommerceProductPush, KindAIAgentGenerate, KindAIRecommend, port.KindSidebarJSSDKSend:
		return true
	default:
		return false
	}
}
