package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

type WebhookReceipt struct {
	ID                  int64
	PackageID           int64
	EventIDDigest       [32]byte
	PayloadDigest       [32]byte
	IdentityKind        string
	IdentityScopeDigest [32]byte
	IdentityValueDigest [32]byte
	Disposition         string
	CustomerID          int64
	IdentityID          int64
	OccurredAt          time.Time
	AcceptedAt          time.Time
	RefreshRunID        int64
}

func (r *Repository) PackageIDByCode(ctx context.Context, code string) (int64, error) {
	t, err := tx(ctx)
	if err != nil {
		return 0, err
	}
	var id int64
	err = t.QueryRow(ctx, `SELECT id FROM segment_audience_packages WHERE code=$1 AND archived_at IS NULL`, code).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	}
	return id, err
}

func (r *Repository) RecordWebhook(ctx context.Context, in WebhookReceipt) (WebhookReceipt, bool, error) {
	if in.PackageID < 1 || in.IdentityKind == "" || in.OccurredAt.IsZero() || in.AcceptedAt.IsZero() {
		return WebhookReceipt{}, false, ErrInvalid
	}
	t, err := tx(ctx)
	if err != nil {
		return WebhookReceipt{}, false, err
	}
	var id int64
	err = t.QueryRow(ctx, `INSERT INTO segment_audience_webhook_receipts(package_id,event_id_digest,payload_digest,identity_kind,identity_scope_digest,identity_value_digest,disposition,customer_id,identity_id,occurred_at,accepted_at,refresh_run_id)
		VALUES($1,$2,$3,$4,$5,$6,$7,NULLIF($8,0),NULLIF($9,0),$10,$11,NULLIF($12,0)) ON CONFLICT(package_id,event_id_digest) DO NOTHING RETURNING id`, in.PackageID, in.EventIDDigest[:], in.PayloadDigest[:], in.IdentityKind, in.IdentityScopeDigest[:], in.IdentityValueDigest[:], in.Disposition, in.CustomerID, in.IdentityID, in.OccurredAt, in.AcceptedAt, in.RefreshRunID).Scan(&id)
	if err == nil {
		in.ID = id
		return in, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return WebhookReceipt{}, false, err
	}
	var event, payload, scope, value []byte
	var existing WebhookReceipt
	err = t.QueryRow(ctx, `SELECT id,package_id,event_id_digest,payload_digest,identity_kind,identity_scope_digest,identity_value_digest,disposition,COALESCE(customer_id,0),COALESCE(identity_id,0),occurred_at,accepted_at,COALESCE(refresh_run_id,0) FROM segment_audience_webhook_receipts WHERE package_id=$1 AND event_id_digest=$2`, in.PackageID, in.EventIDDigest[:]).Scan(&existing.ID, &existing.PackageID, &event, &payload, &existing.IdentityKind, &scope, &value, &existing.Disposition, &existing.CustomerID, &existing.IdentityID, &existing.OccurredAt, &existing.AcceptedAt, &existing.RefreshRunID)
	if err != nil {
		return WebhookReceipt{}, false, err
	}
	if len(event) != sha256.Size || len(payload) != sha256.Size || len(scope) != sha256.Size || len(value) != sha256.Size {
		return WebhookReceipt{}, false, ErrConflict
	}
	copy(existing.EventIDDigest[:], event)
	copy(existing.PayloadDigest[:], payload)
	copy(existing.IdentityScopeDigest[:], scope)
	copy(existing.IdentityValueDigest[:], value)
	if existing.PayloadDigest != in.PayloadDigest {
		return WebhookReceipt{}, false, ErrConflict
	}
	return existing, false, nil
}
