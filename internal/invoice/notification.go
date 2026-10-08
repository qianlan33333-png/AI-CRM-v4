package invoice

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	effects "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	p "github.com/qianlan33333-png/AI-CRM-v3/internal/invoice/port"
	pg "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"time"
)

func (s *Service) Notification(ctx context.Context, n p.Notice) error {
	if n.ID == "" || n.ApplicationID == "" || !effects.ValidDigest(effects.Digest(n.BodyDigest)) {
		return p.ErrInvalid
	}
	return s.UoW.Within(ctx, func(tx context.Context) error {
		a, e := s.Store.get(tx, n.ApplicationID, nil, true)
		if errors.Is(e, p.ErrNotFound) {
			return nil
		}
		if e != nil {
			return e
		}
		if a.Snapshot.MerchantID != n.SubMerchantID {
			return p.ErrConflict
		}
		t, e := pg.RequireTransaction(tx)
		if e != nil {
			return e
		}
		id := effects.Hash("invoice.notify.id", n.ID)
		var prior string
		e = t.QueryRow(tx, `INSERT INTO invoice_callback_receipts(event_digest,body_digest,application_id)VALUES($1,$2,$3)ON CONFLICT DO NOTHING RETURNING body_digest`, id, n.BodyDigest, a.ID).Scan(&prior)
		if errors.Is(e, pgx.ErrNoRows) {
			e = t.QueryRow(tx, `SELECT body_digest FROM invoice_callback_receipts WHERE event_digest=$1`, id).Scan(&prior)
			if e != nil {
				return e
			}
			if prior != n.BodyDigest {
				return p.ErrConflict
			}
		} else if e != nil {
			return e
		}
		// Callbacks wake a fresh authenticated query. Their order cannot downgrade
		// a reversed invoice or confuse card acceptance with card delivery.
		return s.Jobs.EnqueueWithin(tx, a.ID, time.Time{})
	})
}
