package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

func (r *Repository) ReadPaidPurchaseAction(ctx context.Context, orderID int64) (productport.PaidPurchaseAction, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return productport.PaidPurchaseAction{}, err
	}
	if orderID < 1 {
		return productport.PaidPurchaseAction{}, productport.ErrProductReadNotFound
	}
	return scanPaidPurchaseAction(tx.QueryRow(ctx, paidPurchaseActionSelect+` WHERE order_id=$1`, orderID))
}

func (r *Repository) ReadPaidPurchaseActionForUpdate(ctx context.Context, orderPaidEventID int64) (productport.PaidPurchaseAction, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return productport.PaidPurchaseAction{}, err
	}
	if orderPaidEventID < 1 {
		return productport.PaidPurchaseAction{}, productport.ErrProductReadNotFound
	}
	return scanPaidPurchaseAction(tx.QueryRow(ctx, paidPurchaseActionSelect+` WHERE order_paid_event_id=$1 FOR UPDATE`, orderPaidEventID))
}

func (r *Repository) CreatePaidPurchaseAction(ctx context.Context, value productport.PaidPurchaseAction, tagIDs []int64) (productport.PaidPurchaseAction, bool, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return productport.PaidPurchaseAction{}, false, err
	}
	if value.OrderPaidEventID < 1 || value.OrderID < 1 || value.ProductID < 1 || value.ProductVersion < 1 || value.SourceDigest == ([32]byte{}) || value.Mode == "" || value.TagState == "" || value.CreatedAt.IsZero() {
		return productport.PaidPurchaseAction{}, false, ErrInvalid
	}
	if tagIDs == nil {
		tagIDs = []int64{}
	}
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO product_paid_purchase_actions(
order_paid_event_id,order_id,product_id,product_version,source_digest,enabled,action_mode,lead_channel_id,lead_qr_title,lead_qr_subtitle,redirect_url,completion_target,checkout_snapshot,tag_ids,tag_state,created_at)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12::jsonb,$13,$14,$15,$16)
ON CONFLICT(order_paid_event_id) DO NOTHING RETURNING order_paid_event_id`,
		value.OrderPaidEventID, value.OrderID, value.ProductID, value.ProductVersion, value.SourceDigest[:], value.Enabled, value.Mode, optionalPositive(value.LeadChannelID), value.LeadQRTitle, value.LeadQRSubtitle, value.RedirectURL, optionalCompletionTarget(value.CompletionTarget), value.CheckoutSnapshot, tagIDs, value.TagState, value.CreatedAt.UTC(),
	).Scan(&id)
	if err == nil {
		created, readErr := r.ReadPaidPurchaseActionForUpdate(ctx, value.OrderPaidEventID)
		return created, true, readErr
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return productport.PaidPurchaseAction{}, false, mapDatabaseError(err)
	}
	existing, readErr := r.ReadPaidPurchaseActionForUpdate(ctx, value.OrderPaidEventID)
	return existing, false, readErr
}

func (r *Repository) CompletePaidPurchaseActionTag(ctx context.Context, orderPaidEventID, commandID int64, state string) (productport.PaidPurchaseAction, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return productport.PaidPurchaseAction{}, err
	}
	if orderPaidEventID < 1 || state == "" || commandID < 0 {
		return productport.PaidPurchaseAction{}, ErrInvalid
	}
	var command any
	if commandID > 0 {
		command = commandID
	}
	return scanPaidPurchaseAction(tx.QueryRow(ctx, `UPDATE product_paid_purchase_actions
SET tag_command_id=$2,tag_state=$3 WHERE order_paid_event_id=$1
RETURNING `+paidPurchaseActionColumns, orderPaidEventID, command, state))
}

const paidPurchaseActionColumns = `order_paid_event_id,order_id,product_id,product_version,source_digest,enabled,action_mode,COALESCE(lead_channel_id,0),lead_qr_title,lead_qr_subtitle,redirect_url,completion_target,checkout_snapshot,tag_state,created_at`
const paidPurchaseActionSelect = `SELECT ` + paidPurchaseActionColumns + ` FROM product_paid_purchase_actions`

func scanPaidPurchaseAction(row rowScanner) (productport.PaidPurchaseAction, error) {
	var value productport.PaidPurchaseAction
	var source []byte
	err := row.Scan(&value.OrderPaidEventID, &value.OrderID, &value.ProductID, &value.ProductVersion, &source, &value.Enabled, &value.Mode, &value.LeadChannelID, &value.LeadQRTitle, &value.LeadQRSubtitle, &value.RedirectURL, &value.CompletionTarget, &value.CheckoutSnapshot, &value.TagState, &value.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return productport.PaidPurchaseAction{}, productport.ErrProductReadNotFound
	}
	if err != nil {
		return productport.PaidPurchaseAction{}, mapDatabaseError(err)
	}
	if len(source) != len(value.SourceDigest) {
		return productport.PaidPurchaseAction{}, productport.ErrProductReadUnavailable
	}
	copy(value.SourceDigest[:], source)
	return value, nil
}

func optionalPositive(value int64) any {
	if value < 1 {
		return nil
	}
	return value
}

func optionalCompletionTarget(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return value
}
