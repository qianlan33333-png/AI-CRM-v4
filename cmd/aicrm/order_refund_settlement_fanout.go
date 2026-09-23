package main

import (
	"context"

	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
)

// orderRefundSettlementFanout keeps Order as the single transaction owner
// while each domain consumes the same confirmed refund fact in its own tables.
type orderRefundSettlementFanout struct {
	distribution orderport.RefundSettlementConsumer
	referral     orderport.RefundSettlementConsumer
}

func (f orderRefundSettlementFanout) ConsumeRefundSettlementWithin(ctx context.Context, event orderport.RefundSettlementEvent) error {
	if f.distribution != nil {
		if err := f.distribution.ConsumeRefundSettlementWithin(ctx, event); err != nil {
			return err
		}
	}
	if f.referral != nil {
		return f.referral.ConsumeRefundSettlementWithin(ctx, event)
	}
	return nil
}

var _ orderport.RefundSettlementConsumer = orderRefundSettlementFanout{}
