package main

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"strings"
	"testing"
)

// Digest entire synthetic rows, including versions, timestamps, receipts,
// audit and outbox. Rejected callbacks must preserve every original fact.
func refundFixtureFacts(t *testing.T, ctx context.Context, pool *pgxpool.Pool) string {
	t.Helper()
	var digests []string
	for _, table := range []string{"orders", "order_items", "order_service_entitlements", "payments", "payment_refunds", "payment_callback_receipts", "payment_operation_receipts", "payment_audit_events", "payment_outbox"} {
		var digest string
		err := pool.QueryRow(ctx, `SELECT md5(COALESCE(jsonb_agg(to_jsonb(x) ORDER BY to_jsonb(x)::text)::text,'[]')) FROM `+pgx.Identifier{table}.Sanitize()+` x`).Scan(&digest)
		if err != nil {
			t.Fatalf("synthetic protected facts table=%s: %v", table, err)
		}
		digests = append(digests, digest)
	}
	return strings.Join(digests, ":")
}
