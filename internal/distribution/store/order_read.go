package store

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	distributiondomain "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/domain"
	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
)

type orderDistributionAdjustmentJSON struct {
	Kind                  string    `json:"kind"`
	DeltaMinor            int64     `json:"delta_minor"`
	ResultingPayableMinor int64     `json:"resulting_payable_minor"`
	Reason                string    `json:"reason"`
	OccurredAt            time.Time `json:"occurred_at"`
}

type orderDistributionSettlementJSON struct {
	Reference             string     `json:"reference"`
	AmountMinor           int64      `json:"amount_minor"`
	Currency              string     `json:"currency"`
	State                 string     `json:"state"`
	ProviderDeadlineAt    *time.Time `json:"provider_deadline_at"`
	SettlementConfirmedAt *time.Time `json:"settlement_confirmed_at"`
	CreatedAt             *time.Time `json:"created_at"`
	UpdatedAt             *time.Time `json:"updated_at"`
}

type orderDistributionExceptionJSON struct {
	Kind              string    `json:"kind"`
	Status            string    `json:"status"`
	AmountMinor       int64     `json:"amount_minor"`
	Reason            string    `json:"reason"`
	EvidenceReference string    `json:"evidence_reference"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// ReadOrderDistribution returns Distribution-owned, immutable order snapshots
// in bounded batches. It intentionally receives Order IDs rather than calling
// back into Order or reusing the attribution-detail reader.
func (r *Repository) ReadOrderDistribution(ctx context.Context, orderIDs []int64) (map[int64][]distributionport.OrderDistributionLine, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, err
	}
	ids := distinctOrderIDs(orderIDs)
	if len(ids) > 200 {
		return nil, distributiondomain.ErrInvalid
	}
	if len(ids) == 0 {
		return map[int64][]distributionport.OrderDistributionLine{}, nil
	}
	values := make(map[int64][]distributionport.OrderDistributionLine)
	// PostgreSQL assigns one MVCC snapshot to this complete statement.  Keep
	// each nested fact in the statement, rather than issuing follow-up reads in
	// the default READ COMMITTED transaction: a settlement that commits between
	// those reads must not produce a mixed commission/settlement DTO.
	rows, err := tx.Query(ctx, `SELECT
		a.order_id,a.id,a.order_item_line,a.product_name,d.customer_id,
		a.commission_rate_basis_points,a.wait_days,a.policy_version,
		COALESCE(c.id,0),COALESCE(c.initial_minor,0),COALESCE(c.current_payable_minor,0),COALESCE(c.paid_minor,0),
		COALESCE(c.status,''),COALESCE(c.hold_reason,''),COALESCE(c.cancel_reason,''),COALESCE(c.exception_reason,''),
		c.due_at,
		(SELECT MAX(ae.occurred_at) FROM distribution_audit_events ae
		 WHERE ae.aggregate_type='commission' AND ae.aggregate_id=c.id
		   AND ae.event_type='distribution.settlement_paid.v1'
		   AND EXISTS(
			SELECT 1 FROM distribution_settlements s
			WHERE s.commission_id=c.id
			  AND s.settlement_reference=ae.payload->>'settlement_reference'
		   )),
		'CNY',
		COALESCE(adjustments.items,'[]'::jsonb),
		COALESCE(settlements.items,'[]'::jsonb),
		COALESCE(exceptions.items,'[]'::jsonb)
		FROM distribution_order_attributions a
		JOIN distribution_distributors d ON d.id=a.distributor_id
		LEFT JOIN distribution_commissions c ON c.attribution_id=a.id
		LEFT JOIN LATERAL (
			SELECT jsonb_agg(jsonb_build_object(
				'kind',ca.kind,
				'delta_minor',ca.delta_minor,
				'resulting_payable_minor',ca.resulting_payable_minor,
				'reason',ca.reason,
				'occurred_at',ca.occurred_at
			) ORDER BY ca.id) AS items
			FROM distribution_commission_adjustments ca
			WHERE ca.commission_id=c.id
		) adjustments ON c.id IS NOT NULL
		LEFT JOIN LATERAL (
			SELECT jsonb_agg(jsonb_build_object(
				'reference',s.settlement_reference,
				'amount_minor',s.amount_minor,
				'currency',s.currency,
				'state',s.state,
				'provider_deadline_at',s.provider_deadline_at,
				'settlement_confirmed_at',(
					SELECT MAX(ae.occurred_at) FROM distribution_audit_events ae
					WHERE ae.aggregate_type='commission' AND ae.aggregate_id=s.commission_id
					  AND ae.event_type='distribution.settlement_paid.v1'
					  AND ae.payload->>'settlement_reference'=s.settlement_reference
				),
				'created_at',s.created_at,
				'updated_at',s.updated_at
			) ORDER BY s.id) AS items
			FROM distribution_settlements s
			WHERE s.commission_id=c.id
		) settlements ON c.id IS NOT NULL
		LEFT JOIN LATERAL (
			SELECT jsonb_agg(jsonb_build_object(
				'kind',e.kind,
				'status',e.status,
				'amount_minor',e.amount_minor,
				'reason',e.reason,
				'evidence_reference',e.evidence_reference,
				'created_at',e.created_at,
				'updated_at',e.updated_at
			) ORDER BY e.id) AS items
			FROM distribution_exceptions e
			WHERE e.commission_id=c.id
		) exceptions ON c.id IS NOT NULL
		WHERE a.order_id=ANY($1::bigint[])
		ORDER BY a.order_id,a.order_item_line,a.id`, ids)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	for rows.Next() {
		var item distributionport.OrderDistributionLine
		var adjustmentsRaw, settlementsRaw, exceptionsRaw []byte
		if err := rows.Scan(&item.OrderID, &item.AttributionID, &item.ItemLine, &item.ProductName, &item.DistributorCustomerID,
			&item.RateBasisPoints, &item.WaitDays, &item.PolicyVersion,
			&item.CommissionID, &item.InitialMinor, &item.CurrentPayableMinor, &item.PaidMinor,
			&item.Status, &item.HoldReason, &item.CancelReason, &item.ExceptionReason,
			&item.DueAt, &item.SettlementConfirmedAt, &item.Currency,
			&adjustmentsRaw, &settlementsRaw, &exceptionsRaw); err != nil {
			return nil, mapError(err)
		}
		if item.Adjustments, err = decodeOrderAdjustments(adjustmentsRaw); err != nil {
			return nil, err
		}
		if item.Settlements, err = decodeOrderSettlements(settlementsRaw); err != nil {
			return nil, err
		}
		if item.Exceptions, err = decodeOrderExceptions(exceptionsRaw); err != nil {
			return nil, err
		}
		item.HasCommission = item.CommissionID > 0
		values[item.OrderID] = append(values[item.OrderID], item)
	}
	if err := rows.Err(); err != nil {
		return nil, mapError(err)
	}
	return values, nil
}

func decodeOrderAdjustments(raw []byte) ([]distributionport.OrderDistributionAdjustment, error) {
	var values []orderDistributionAdjustmentJSON
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, err
	}
	items := make([]distributionport.OrderDistributionAdjustment, 0, len(values))
	for _, value := range values {
		items = append(items, distributionport.OrderDistributionAdjustment{Kind: value.Kind, DeltaMinor: value.DeltaMinor, ResultingPayableMinor: value.ResultingPayableMinor, Reason: value.Reason, OccurredAt: value.OccurredAt})
	}
	return items, nil
}

func decodeOrderSettlements(raw []byte) ([]distributionport.OrderDistributionSettlement, error) {
	var values []orderDistributionSettlementJSON
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, err
	}
	items := make([]distributionport.OrderDistributionSettlement, 0, len(values))
	for _, value := range values {
		items = append(items, distributionport.OrderDistributionSettlement{Reference: value.Reference, AmountMinor: value.AmountMinor, Currency: value.Currency, State: value.State, ProviderDeadlineAt: value.ProviderDeadlineAt, SettlementConfirmedAt: value.SettlementConfirmedAt, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt})
	}
	return items, nil
}

func decodeOrderExceptions(raw []byte) ([]distributionport.OrderDistributionException, error) {
	var values []orderDistributionExceptionJSON
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, err
	}
	items := make([]distributionport.OrderDistributionException, 0, len(values))
	for _, value := range values {
		items = append(items, distributionport.OrderDistributionException{Kind: value.Kind, Status: value.Status, AmountMinor: value.AmountMinor, Reason: value.Reason, EvidenceReference: value.EvidenceReference, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt})
	}
	return items, nil
}

func distinctOrderIDs(orderIDs []int64) []int64 {
	seen := make(map[int64]struct{}, len(orderIDs))
	for _, id := range orderIDs {
		if id > 0 {
			seen[id] = struct{}{}
		}
	}
	return mapKeys(seen)
}

func mapKeys(values map[int64]struct{}) []int64 {
	ids := make([]int64, 0, len(values))
	for id := range values {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}
