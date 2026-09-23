package store

import (
	"context"
	"crypto/sha256"
	"errors"

	"github.com/jackc/pgx/v5"
	customerapp "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/app"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

type TagCommandPostgreSQL struct{}

func (TagCommandPostgreSQL) FindTagCommand(ctx context.Context, source, sourceRef, key string) (customerport.TagCommandResult, [32]byte, bool, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return customerport.TagCommandResult{}, [32]byte{}, false, err
	}
	var r customerport.TagCommandResult
	var digest []byte
	err = tx.QueryRow(ctx, `SELECT id,source,state,occurred_at,updated_at,payload_digest FROM customer_tag_commands WHERE source=$1 AND source_ref=$2 AND idempotency_key=$3 FOR UPDATE`, source, sourceRef, key).Scan(&r.ID, &r.Source, &r.State, &r.OccurredAt, &r.UpdatedAt, &digest)
	if errors.Is(err, pgx.ErrNoRows) {
		return customerport.TagCommandResult{}, [32]byte{}, false, nil
	}
	if err != nil || len(digest) != sha256.Size {
		return customerport.TagCommandResult{}, [32]byte{}, false, err
	}
	var d [32]byte
	copy(d[:], digest)
	rows, err := tx.Query(ctx, `SELECT id,customer_id,COALESCE(staff_id,0),add_tag_ids,remove_tag_ids,COALESCE(binding_digest,''),COALESCE(target_digest,''),COALESCE(effect_ref,''),COALESCE(accept_receipt_ref,''),COALESCE(queue_receipt_ref,''),state,COALESCE(reject_reason,''),COALESCE(result_reason,'') FROM customer_tag_command_lines WHERE command_id=$1 ORDER BY id`, r.ID)
	if err != nil {
		return customerport.TagCommandResult{}, [32]byte{}, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var l customerport.TagCommandLine
		if err = rows.Scan(&l.ID, &l.CustomerID, &l.StaffID, &l.AddTagIDs, &l.RemoveTagIDs, &l.BindingDigest, &l.TargetDigest, &l.EffectRef, &l.AcceptReceiptRef, &l.QueueReceiptRef, &l.State, &l.RejectReason, &l.ResultReason); err != nil {
			return customerport.TagCommandResult{}, [32]byte{}, false, err
		}
		r.Lines = append(r.Lines, l)
	}
	return r, d, true, rows.Err()
}

// LockTagCommandTargets serializes pending tag mutations per canonical
// Customer. It deliberately rejects a different request while any line is not
// terminal: WeCom mark_tag add/remove order is observable and an unknown prior
// outcome may still have reached the Provider.
func (TagCommandPostgreSQL) LockTagCommandTargets(ctx context.Context, targets []customerport.TagCommandTarget) error {
	if len(targets) == 0 {
		return customerport.ErrTagCommandInvalid
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	ids := make([]int64, 0, len(targets))
	for index, target := range targets {
		id := int64(target.CustomerID)
		if id < 1 || (index > 0 && id <= ids[index-1]) {
			return customerport.ErrTagCommandInvalid
		}
		ids = append(ids, id)
	}
	rows, err := tx.Query(ctx, `SELECT id FROM customers WHERE id=ANY($1::bigint[]) ORDER BY id FOR UPDATE`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	locked := 0
	for rows.Next() {
		locked++
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if locked != len(ids) {
		return customerport.ErrTagCommandUnavailable
	}
	return nil
}

// EnsureTagCommandTargetsIdle runs after the Customer locks and same-key
// receipt replay check. It rejects a different add/remove request while a
// prior effect has not reached a terminal state.
func (TagCommandPostgreSQL) EnsureTagCommandTargetsIdle(ctx context.Context, targets []customerport.TagCommandTarget) error {
	if len(targets) == 0 {
		return customerport.ErrTagCommandInvalid
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	ids := make([]int64, 0, len(targets))
	for index, target := range targets {
		id := int64(target.CustomerID)
		if id < 1 || (index > 0 && id <= ids[index-1]) {
			return customerport.ErrTagCommandInvalid
		}
		ids = append(ids, id)
	}
	var pending bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM customer_tag_command_lines line
		WHERE line.customer_id=ANY($1::bigint[])
		AND line.state IN ('accepted','queued','attempted','outcome_unknown','retryable_failed')
	)`, ids).Scan(&pending)
	if err != nil {
		return err
	}
	if pending {
		return customerport.ErrTagCommandConflict
	}
	return nil
}

func (TagCommandPostgreSQL) CreateTagCommand(ctx context.Context, c customerport.TagCommand, d [32]byte) (int64, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return 0, err
	}
	var actor any
	if c.ActorAdminUserID > 0 {
		actor = c.ActorAdminUserID
	}
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO customer_tag_commands(actor_admin_user_id,source,source_ref,idempotency_key,payload_digest,state,occurred_at) VALUES($1,$2,$3,$4,$5,'queued',$6) ON CONFLICT(source,source_ref,idempotency_key) DO NOTHING RETURNING id`, actor, c.Source, c.SourceRef, c.IdempotencyKey, d[:], c.OccurredAt.UTC()).Scan(&id)
	return id, err
}

func (TagCommandPostgreSQL) SetTagCommandState(ctx context.Context, id int64, state string) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE customer_tag_commands SET state=$2,updated_at=clock_timestamp() WHERE id=$1`, id, state)
	return err
}
func (TagCommandPostgreSQL) CreateRejectedTagCommandLine(ctx context.Context, commandID int64, target customerport.TagCommandTarget, reason string) (customerport.TagCommandLine, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return customerport.TagCommandLine{}, err
	}
	if target.AddTagIDs == nil {
		target.AddTagIDs = []int64{}
	}
	if target.RemoveTagIDs == nil {
		target.RemoveTagIDs = []int64{}
	}
	var l customerport.TagCommandLine
	var staff any
	if target.StaffID > 0 {
		staff = target.StaffID
	}
	err = tx.QueryRow(ctx, `INSERT INTO customer_tag_command_lines(command_id,customer_id,staff_id,add_tag_ids,remove_tag_ids,state,reject_reason) VALUES($1,$2,$3,$4,$5,'rejected',$6) RETURNING id`, commandID, target.CustomerID, staff, target.AddTagIDs, target.RemoveTagIDs, reason).Scan(&l.ID)
	l.CustomerID, l.StaffID, l.AddTagIDs, l.RemoveTagIDs, l.State, l.RejectReason = target.CustomerID, target.StaffID, target.AddTagIDs, target.RemoveTagIDs, "rejected", reason
	return l, err
}
func (TagCommandPostgreSQL) CreateTagCommandLine(ctx context.Context, commandID int64, target customerport.FrozenTagCommandTarget, source string, effect effectport.Projection, receipt effectport.Receipt) (customerport.TagCommandLine, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return customerport.TagCommandLine{}, err
	}
	if target.AddTagIDs == nil {
		target.AddTagIDs = []int64{}
	}
	if target.RemoveTagIDs == nil {
		target.RemoveTagIDs = []int64{}
	}
	var l customerport.TagCommandLine
	err = tx.QueryRow(ctx, `INSERT INTO customer_tag_command_lines(command_id,customer_id,staff_id,add_tag_ids,remove_tag_ids,binding_digest,target_digest,source_ref_digest,effect_ref,accept_receipt_ref,queue_receipt_ref,state) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) RETURNING id`, commandID, target.CustomerID, target.StaffID, target.AddTagIDs, target.RemoveTagIDs, target.BindingDigest, target.TargetDigest, source, effect.ID, receipt.ID, receipt.QueueReceiptID, effect.State).Scan(&l.ID)
	l.CustomerID, l.StaffID, l.AddTagIDs, l.RemoveTagIDs, l.BindingDigest, l.TargetDigest, l.EffectRef, l.AcceptReceiptRef, l.QueueReceiptRef, l.State = target.CustomerID, target.StaffID, target.AddTagIDs, target.RemoveTagIDs, target.BindingDigest, target.TargetDigest, effect.ID, receipt.ID, receipt.QueueReceiptID, string(effect.State)
	return l, err
}
func (TagCommandPostgreSQL) ReadTagCommandDispatch(ctx context.Context, source string) (customerport.TagCommandDispatch, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return customerport.TagCommandDispatch{}, err
	}
	var d customerport.TagCommandDispatch
	err = tx.QueryRow(ctx, `SELECT line.effect_ref,command.source,line.customer_id,line.staff_id,line.add_tag_ids,line.remove_tag_ids,line.binding_digest,line.target_digest FROM customer_tag_command_lines line JOIN customer_tag_commands command ON command.id=line.command_id WHERE line.source_ref_digest=$1`, source).Scan(&d.EffectRef, &d.Source, &d.CustomerID, &d.StaffID, &d.AddTagIDs, &d.RemoveTagIDs, &d.BindingDigest, &d.TargetDigest)
	if errors.Is(err, pgx.ErrNoRows) {
		return customerport.TagCommandDispatch{}, customerport.ErrTagCommandUnavailable
	}
	return d, err
}
func (TagCommandPostgreSQL) ListTagCommands(ctx context.Context, customerID customerdomain.CustomerID, limit int) ([]customerport.TagCommandResult, error) {
	if customerID < 1 || limit < 1 || limit > 100 {
		return nil, customerport.ErrTagCommandInvalid
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `WITH selected AS (
		SELECT command.id FROM customer_tag_commands command
		WHERE EXISTS (SELECT 1 FROM customer_tag_command_lines line WHERE line.command_id=command.id AND line.customer_id=$1)
		ORDER BY command.created_at DESC,command.id DESC LIMIT $2
	) SELECT command.id,command.source,command.state,command.occurred_at,command.updated_at,
		line.id,line.customer_id,COALESCE(line.staff_id,0),line.add_tag_ids,line.remove_tag_ids,COALESCE(line.binding_digest,''),COALESCE(line.target_digest,''),COALESCE(line.effect_ref,''),COALESCE(line.accept_receipt_ref,''),COALESCE(line.queue_receipt_ref,''),line.state,COALESCE(line.reject_reason,''),COALESCE(line.result_reason,'')
		FROM selected JOIN customer_tag_commands command ON command.id=selected.id
		JOIN customer_tag_command_lines line ON line.command_id=command.id AND line.customer_id=$1
		ORDER BY command.created_at DESC,command.id DESC,line.id`, customerID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byID := make(map[int64]int)
	var result []customerport.TagCommandResult
	for rows.Next() {
		var command customerport.TagCommandResult
		var line customerport.TagCommandLine
		if err = rows.Scan(&command.ID, &command.Source, &command.State, &command.OccurredAt, &command.UpdatedAt, &line.ID, &line.CustomerID, &line.StaffID, &line.AddTagIDs, &line.RemoveTagIDs, &line.BindingDigest, &line.TargetDigest, &line.EffectRef, &line.AcceptReceiptRef, &line.QueueReceiptRef, &line.State, &line.RejectReason, &line.ResultReason); err != nil {
			return nil, err
		}
		index, found := byID[command.ID]
		if !found {
			byID[command.ID] = len(result)
			command.Lines = []customerport.TagCommandLine{line}
			result = append(result, command)
		} else {
			result[index].Lines = append(result[index].Lines, line)
		}
	}
	return result, rows.Err()
}

func (TagCommandPostgreSQL) CompleteTagCommand(ctx context.Context, c customerport.TagCommandCompletion) error {
	if c.EffectRef == "" || !effectport.ValidDigest(effectport.Digest(c.ResultDigest)) || !validTagCommandState(c.State) || c.Attempt < 1 || c.Generation < 1 || c.Fence < 1 || c.CompletedAt.IsZero() {
		return customerport.ErrTagCommandInvalid
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	// Serialize every line completion of a command through its parent before
	// locking the line. Otherwise two effects can each aggregate against the
	// other's queued snapshot and leave the command stale after both commit.
	var commandID int64
	err = tx.QueryRow(ctx, `SELECT command_id FROM customer_tag_command_lines WHERE effect_ref=$1`, c.EffectRef).Scan(&commandID)
	if errors.Is(err, pgx.ErrNoRows) {
		return customerport.ErrTagCommandUnavailable
	}
	if err != nil {
		return err
	}
	if err = tx.QueryRow(ctx, `SELECT id FROM customer_tag_commands WHERE id=$1 FOR UPDATE`, commandID).Scan(&commandID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return customerport.ErrTagCommandUnavailable
		}
		return err
	}
	var state, digest, resultReason string
	var attempts int32
	var generation, fence int64
	err = tx.QueryRow(ctx, `SELECT state,COALESCE(result_digest,''),COALESCE(result_reason,''),attempt_count,completion_generation,completion_fence FROM customer_tag_command_lines WHERE effect_ref=$1 FOR UPDATE`, c.EffectRef).Scan(&state, &digest, &resultReason, &attempts, &generation, &fence)
	if errors.Is(err, pgx.ErrNoRows) {
		return customerport.ErrTagCommandUnavailable
	}
	if err != nil {
		return err
	}
	if state == c.State && digest == c.ResultDigest && resultReason == c.ResultReason && generation == c.Generation && fence == c.Fence && attempts >= c.Attempt {
		return nil
	}
	if generation > c.Generation || (generation == c.Generation && fence > c.Fence) {
		return customerport.ErrTagCommandConflict
	}
	if state != "queued" && state != "attempted" && state != "outcome_unknown" && state != "retryable_failed" {
		return customerport.ErrTagCommandConflict
	}
	result, err := tx.Exec(ctx, `UPDATE customer_tag_command_lines SET state=$2,result_digest=$3,result_reason=NULLIF($4,''),attempt_count=GREATEST(attempt_count,$5),completion_generation=$6,completion_fence=$7,completed_at=$8,updated_at=clock_timestamp() WHERE effect_ref=$1`, c.EffectRef, c.State, c.ResultDigest, c.ResultReason, c.Attempt, c.Generation, c.Fence, c.CompletedAt.UTC())
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return customerport.ErrTagCommandUnavailable
	}
	_, err = tx.Exec(ctx, `UPDATE customer_tag_commands command SET state=states.state,updated_at=clock_timestamp() FROM (SELECT command_id,CASE WHEN bool_or(state='outcome_unknown') THEN 'outcome_unknown' WHEN bool_or(state='attempted') THEN 'attempted' WHEN bool_or(state='queued') THEN 'queued' WHEN bool_or(state='retryable_failed') THEN 'retryable_failed' WHEN bool_and(state='rejected') THEN 'rejected' WHEN bool_and(state IN ('executed','reconciled')) THEN 'executed' WHEN bool_and(state='final_failed') THEN 'final_failed' WHEN bool_and(state='cancelled') THEN 'cancelled' WHEN bool_or(state IN ('rejected','executed','reconciled','final_failed','cancelled')) THEN 'partial' ELSE 'accepted' END AS state FROM customer_tag_command_lines WHERE command_id=$1 GROUP BY command_id) states WHERE command.id=states.command_id`, commandID)
	return err
}
func validTagCommandState(v string) bool {
	switch v {
	case "accepted", "queued", "attempted", "executed", "outcome_unknown", "retryable_failed", "final_failed", "reconciled", "cancelled", "rejected", "partial":
		return true
	}
	return false
}

var _ customerapp.TagCommandStore = TagCommandPostgreSQL{}
var _ customerport.TagCommandDispatchReader = TagCommandPostgreSQL{}
var _ customerport.TagCommandCompletionWriter = TagCommandPostgreSQL{}
var _ customerport.TagCommandHistoryReader = TagCommandPostgreSQL{}
