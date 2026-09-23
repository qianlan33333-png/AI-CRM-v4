package outbound

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
)

func (s *MessageService) RecordAutomationMessageReceipt(ctx context.Context, intentID int64, reference string, target outboundport.PrivateMessageTarget, messageID, reason string) error {
	if s == nil || s.pool == nil || intentID < 1 || reference == "" {
		return ErrInvalidMessageIntent
	}
	tag, err := s.pool.Exec(ctx, `INSERT INTO outbound_message_receipts(content_reference,message_intent_id,message_id,sender_userid,external_userid,reason) VALUES($1,$2,$3,$4,$5,$6)
		ON CONFLICT(content_reference) DO UPDATE SET message_id=EXCLUDED.message_id,sender_userid=EXCLUDED.sender_userid,external_userid=EXCLUDED.external_userid,reason=EXCLUDED.reason,observed_at=clock_timestamp()
		WHERE outbound_message_receipts.message_intent_id=EXCLUDED.message_intent_id AND (outbound_message_receipts.message_id='' OR outbound_message_receipts.message_id=EXCLUDED.message_id)`, reference, intentID, messageID, target.StaffUserID, target.ExternalUserID, reason)
	if err == nil && tag.RowsAffected() != 1 {
		return errors.New("automation message receipt conflict")
	}
	return err
}

func (s *MessageService) AutomationMessageReceipt(ctx context.Context, reference string) (outboundport.PrivateMessageDelivery, bool, error) {
	var v outboundport.PrivateMessageDelivery
	err := s.pool.QueryRow(ctx, `SELECT message_id,sender_userid,external_userid,reason,delivery_status,sent_at,observed_at FROM outbound_message_receipts WHERE content_reference=$1`, reference).Scan(&v.MessageID, &v.SenderUserID, &v.ExternalUserID, &v.Reason, &v.Status, &v.SentAt, &v.ObservedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, false, nil
	}
	return v, err == nil, err
}
func (s *MessageService) SaveAutomationMessageDelivery(ctx context.Context, reference string, v outboundport.PrivateMessageDelivery) error {
	if v.Status == nil || (*v.Status == 1 && (v.SentAt == nil || v.SentAt.After(time.Now().Add(time.Minute)))) {
		return errors.New("incomplete delivery evidence")
	}
	tag, err := s.pool.Exec(ctx, `UPDATE outbound_message_receipts SET delivery_status=$5,sent_at=$6,observed_at=clock_timestamp() WHERE content_reference=$1 AND message_id=$2 AND sender_userid=$3 AND external_userid=$4 AND (delivery_status IS NULL OR delivery_status<>1 OR ($5=1 AND sent_at=$6))`, reference, v.MessageID, v.SenderUserID, v.ExternalUserID, v.Status, v.SentAt)
	if err == nil && tag.RowsAffected() != 1 {
		return errors.New("delivery evidence conflict")
	}
	return err
}

var _ outboundport.MessageReceiptRecorder = (*MessageService)(nil)
var _ outboundport.MessageDeliveryStore = (*MessageService)(nil)
