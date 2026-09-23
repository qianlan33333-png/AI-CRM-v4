package store

import (
	"context"

	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
)

// ReadOverview reads only Distribution-owned commission and exception facts.
// The reporting period is based on the immutable paid_confirmed_at stored when
// Order's first-native-paid event was consumed. Current settlement figures are
// deliberately not constrained to that period.
func (r *Repository) ReadOverview(ctx context.Context, window distributionport.OverviewWindow) (distributionport.Overview, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return distributionport.Overview{}, err
	}
	if !window.Valid() {
		return distributionport.Overview{}, ErrInvalid
	}
	result := distributionport.Overview{Currency: "CNY"}
	err = tx.QueryRow(ctx, `SELECT
		COALESCE(SUM(c.original_item_paid_minor) FILTER (WHERE c.paid_confirmed_at >= $1 AND c.paid_confirmed_at < $2),0),
		COALESCE(SUM(c.initial_minor) FILTER (WHERE c.paid_confirmed_at >= $1 AND c.paid_confirmed_at < $2),0),
		COALESCE(COUNT(*) FILTER (WHERE c.paid_confirmed_at >= $1 AND c.paid_confirmed_at < $2),0),
		COALESCE(SUM(CASE WHEN c.status NOT IN ('paid','cancelled','zero_commission') THEN GREATEST(c.current_payable_minor-c.paid_minor,0) ELSE 0 END),0),
		COALESCE(SUM(c.paid_minor),0),
		(SELECT COALESCE(COUNT(DISTINCT c.order_id),0)
			FROM distribution_exceptions e
			JOIN distribution_commissions c ON c.id=e.commission_id
			WHERE e.status IN ('open','querying')),
		(SELECT COALESCE(COUNT(*),0) FROM distribution_exceptions e WHERE e.status IN ('open','querying'))
		FROM distribution_commissions c`, window.Start.UTC(), window.End.UTC()).Scan(
		&result.PeriodPaidSalesMinor,
		&result.PeriodInitialCommission,
		&result.PeriodCommissionCount,
		&result.CurrentUnsettledMinor,
		&result.CurrentSettledMinor,
		&result.CurrentExceptionOrderCount,
		&result.OpenExceptionCount,
	)
	if err != nil {
		return distributionport.Overview{}, mapError(err)
	}
	return result, nil
}

var _ distributionport.OverviewReader = (*Repository)(nil)
