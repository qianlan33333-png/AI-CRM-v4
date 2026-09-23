package outbound

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	port "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	"time"
)

func (w *PrivateMessageWriter) RecordPrivateMessageReceipt(ctx context.Context, ref string, target PrivateMessageTarget, msgid, reason string) error {
	if w.pool == nil {
		return errors.New("receipt store unavailable")
	}
	tag, err := w.pool.Exec(ctx, `INSERT INTO outbound_private_message_receipts(payload_reference,message_id,sender_userid,external_userid,reason) VALUES($1,$2,$3,$4,$5)
 ON CONFLICT(payload_reference) DO UPDATE SET message_id=EXCLUDED.message_id,sender_userid=EXCLUDED.sender_userid,external_userid=EXCLUDED.external_userid,reason=EXCLUDED.reason,observed_at=clock_timestamp()
 WHERE outbound_private_message_receipts.message_id='' OR outbound_private_message_receipts.message_id=EXCLUDED.message_id`, ref, msgid, target.StaffUserID, target.ExternalUserID, reason)
	if err == nil && tag.RowsAffected() != 1 {
		return errors.New("receipt conflict")
	}
	return err
}
func (w *PrivateMessageWriter) PrivateMessageReceipt(ctx context.Context, ref string) (port.PrivateMessageDelivery, bool, error) {
	var v port.PrivateMessageDelivery
	err := w.pool.QueryRow(ctx, `SELECT message_id,sender_userid,external_userid,reason,delivery_status,sent_at,observed_at FROM outbound_private_message_receipts WHERE payload_reference=$1`, ref).Scan(&v.MessageID, &v.SenderUserID, &v.ExternalUserID, &v.Reason, &v.Status, &v.SentAt, &v.ObservedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, false, nil
	}
	return v, err == nil, err
}
func (w *PrivateMessageWriter) SavePrivateMessageDelivery(ctx context.Context, ref string, v port.PrivateMessageDelivery) error {
	if v.Status == nil || (*v.Status == 1 && (v.SentAt == nil || v.SentAt.After(time.Now().Add(time.Minute)))) {
		return errors.New("incomplete delivery evidence")
	}
	tag, err := w.pool.Exec(ctx, `UPDATE outbound_private_message_receipts SET delivery_status=$5,sent_at=$6,observed_at=clock_timestamp()
 WHERE payload_reference=$1 AND message_id=$2 AND sender_userid=$3 AND external_userid=$4 AND (delivery_status IS NULL OR delivery_status<>1 OR ($5=1 AND sent_at=$6))`, ref, v.MessageID, v.SenderUserID, v.ExternalUserID, v.Status, v.SentAt)
	if err == nil && tag.RowsAffected() != 1 {
		return errors.New("delivery evidence conflict")
	}
	return err
}
