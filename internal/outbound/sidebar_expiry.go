package outbound

import (
	"context"
	"crypto/sha256"
	"errors"
	"time"

	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// SidebarJSSDKExpiry is the durable timeout boundary for a browser-owned
// JSSDK effect. It never invokes a Provider. If a one-time grant expires
// without a client receipt, the server cannot prove that the client did not
// invoke the SDK; it therefore preserves outcome_unknown for reconciliation.
type SidebarJSSDKExpiry struct{}

func (SidebarJSSDKExpiry) Execute(_ context.Context, envelope effectport.Envelope, _ effectport.Attempt) (effectport.AdapterResult, error) {
	if envelope.Kind != effectport.KindSidebarJSSDKSend {
		return effectport.AdapterResult{}, errors.New("unsupported sidebar effect")
	}
	return effectport.AdapterResult{
		Completion:    effectport.StateUnknown,
		ReceiptDigest: effectport.Hash("sidebar.jssdk.grant.expired", string(envelope.PayloadDigest)),
	}, nil
}

func (SidebarJSSDKExpiry) CompleteEffect(ctx context.Context, effectRef string, envelope effectport.Envelope, _ effectport.Attempt, result effectport.AdapterResult) error {
	if envelope.Kind != effectport.KindSidebarJSSDKSend || result.Completion != effectport.StateUnknown || result.CallAttempted || result.RealExternalCallExecuted {
		return errors.New("invalid sidebar expiry completion")
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	receiptDigest := sha256.Sum256([]byte(result.ReceiptDigest))
	var intentID int64
	var transitioned bool
	err = tx.QueryRow(ctx, `WITH transitioned AS (
		UPDATE outbound_sidebar_send_intents SET state='outcome_unknown',updated_at=$2 WHERE effect_id=$1 AND state='queued' RETURNING id
	)
	SELECT id,true FROM transitioned
	UNION ALL
	SELECT id,false FROM outbound_sidebar_send_intents WHERE effect_id=$1 AND state='outcome_unknown'
	LIMIT 1`, effectRef, now).Scan(&intentID, &transitioned)
	if err != nil {
		return err
	}
	if !transitioned {
		return nil
	}
	if _, err = tx.Exec(ctx, `INSERT INTO outbound_sidebar_send_audit_events(intent_id,operation,payload_digest,occurred_at) VALUES($1,'expire',$2,$3)`, intentID, receiptDigest[:], now); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO outbound_sidebar_send_outbox(event_type,intent_id,payload,idempotency_digest,occurred_at) VALUES('outbound.sidebar_send.expired.v1',$1,jsonb_build_object('intent_id',$1::bigint,'effect_id',$2::text,'state','outcome_unknown'),$3::bytea,$4::timestamptz)`, intentID, effectRef, receiptDigest[:], now)
	return err
}

var _ effectport.ProviderAdapter = SidebarJSSDKExpiry{}
var _ effectport.CompletionSink = SidebarJSSDKExpiry{}
