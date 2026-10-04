package store

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
)

func readExternalHistory(ctx context.Context, tx pgx.Tx, s domain.Snapshot) (*domain.ExternalHistoryFacts, error) {
	if s.Provider != domain.ProviderExternalHistory {
		return nil, nil
	}
	var f domain.ExternalHistoryFacts
	err := tx.QueryRow(ctx, `SELECT source_paid_at,imported_at,source_status,source_after_sales FROM order_external_history_facts WHERE order_id=$1`, s.ID).Scan(&f.SourcePaidAt, &f.ImportedAt, &f.SourceStatus, &f.SourceAfterSales)
	return &f, mapError(err)
}

// Lookup is strictly read-only; source content changes are never overwritten.
func (r *Repository) LookupExternalHistoryWithin(ctx context.Context, source, key string, digest [32]byte) (domain.Snapshot, bool, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return domain.Snapshot{}, false, err
	}
	var id int64
	var prior []byte
	err = tx.QueryRow(ctx, `SELECT id,source_row_digest FROM orders WHERE source_system=$1 AND source_key=$2`, source, key).Scan(&id, &prior)
	if err == pgx.ErrNoRows {
		return domain.Snapshot{}, false, nil
	}
	if err != nil {
		return domain.Snapshot{}, false, mapError(err)
	}
	if string(prior) != string(digest[:]) {
		return domain.Snapshot{}, false, orderport.ErrConflict
	}
	o, err := r.Get(ctx, id, false)
	return o.Snapshot(), true, err
}
func (r *Repository) ImportHistoricalWithin(ctx context.Context, c orderport.HistoricalImportCommand) (domain.Snapshot, bool, error) {
	if c.Order.RecordOrigin != domain.RecordOriginHistory || c.Order.EffectEligible {
		return domain.Snapshot{}, false, orderport.ErrConflict
	}
	c.Order.ID = 0
	o, err := domain.Restore(c.Order)
	if err != nil {
		return domain.Snapshot{}, false, err
	}
	o, created, err := r.Import(ctx, c.RunID, c.SourceDigest, o)
	return o.Snapshot(), created, err
}
