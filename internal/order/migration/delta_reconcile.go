package migration

import (
	"context"
	"encoding/json"
	orderdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	"strconv"
	"time"
)

func (s PostgreSQLRuns) verifyHistoricalDelta(ctx context.Context, id, runID, version int64, runKey string, expected OrderRow, refunded int64) error {
	digest := HistoricalOrderDigest(expected)
	var beforeVersion, afterVersion, beforeRefund, afterRefund int64
	var status string
	var when time.Time
	var proof bool
	err := s.Pool.QueryRow(ctx, `SELECT d.before_version,d.after_version,d.before_refunded_minor,d.after_refunded_minor,d.after_status,d.occurred_at,
 EXISTS(SELECT 1 FROM order_import_receipts r WHERE r.order_id=d.order_id AND r.source_row_digest=d.before_source_digest AND r.run_id<>d.run_id)
 FROM order_history_source_deltas d WHERE d.order_id=$1 AND d.run_id=$2 AND d.after_source_digest=$3`, id, runID, digest[:]).Scan(&beforeVersion, &afterVersion, &beforeRefund, &afterRefund, &status, &when, &proof)
	if err != nil {
		return reconciliationError(err)
	}
	if !proof || beforeVersion < 1 || afterVersion != beforeVersion+1 || version != afterVersion || afterRefund < beforeRefund || afterRefund != refunded || status != expected.Status || !sameHistoricalTime(when, expected.UpdatedAt) {
		return ErrReconciliationMismatch
	}
	payload, _ := json.Marshal(map[string]any{"order_id": id, "status": expected.Status, "version": version, "record_origin": orderdomain.RecordOriginHistory})
	var histories, audits, outbox, genesis int64
	err = s.Pool.QueryRow(ctx, `SELECT
 (SELECT count(*) FROM order_status_history h JOIN order_history_source_deltas d ON d.order_id=h.order_id AND d.after_version=h.order_version WHERE d.order_id=$1 AND d.run_id=$2 AND h.from_status=d.before_status AND h.to_status=d.after_status AND h.refunded_minor=d.after_refunded_minor AND h.actor_scope=$3 AND h.occurred_at=d.occurred_at),
 (SELECT count(*) FROM order_audit_events WHERE event_type='order.history_delta_applied' AND order_id=$1 AND actor_scope=$3 AND payload=$4::jsonb AND occurred_at=$5),
 (SELECT count(*) FROM order_outbox WHERE event_type='order.history_delta_applied' AND aggregate_id=$1 AND idempotency_key=$6 AND payload=$4::jsonb AND occurred_at=$5),
 (SELECT count(*) FROM order_status_history WHERE order_id=$1 AND order_version=1 AND from_status IS NULL)`, id, runID, "migration:"+runKey, string(payload), when, "order.history_delta_applied:"+strconv.FormatInt(id, 10)+":"+strconv.FormatInt(version, 10)).Scan(&histories, &audits, &outbox, &genesis)
	if err != nil {
		return reconciliationError(err)
	}
	if histories != 1 || audits != 1 || outbox != 1 || genesis != 1 {
		return ErrReconciliationMismatch
	}
	return nil
}
