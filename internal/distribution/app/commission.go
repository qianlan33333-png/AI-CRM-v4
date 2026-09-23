package app

import (
	"context"
	"errors"
	"strconv"
	"time"

	distributiondomain "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/domain"
	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
	distributionstore "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/store"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// DueCheckEnqueuer persists a Distribution-owned due check in the active
// PostgreSQL transaction. The concrete implementation is River/jobqueue; it
// is deliberately an internal task rather than a timer in this module.
type DueCheckEnqueuer interface {
	EnqueueCommissionDueWithin(context.Context, int64, time.Time) error
}

type commissionStore interface {
	ReadAttributionQualificationContextByOrderItemWithin(context.Context, int64, int32, bool) (distributionstore.AttributionQualificationContext, error)
	InsertCommissionWithin(context.Context, distributiondomain.Commission) (distributiondomain.Commission, bool, error)
	AppendAuditWithin(context.Context, string, string, int64, string, any, time.Time) error
	AppendOutboxWithin(context.Context, string, string, int64, any, time.Time) error
}

// CommissionService consumes the existing immutable Order paid event. It has
// no Order table access and never creates a second payment event. The unique
// attribution_id key plus River insertion in the same UoW makes callback
// replay and post-crash recovery safe.
type CommissionService struct {
	store         commissionStore
	dueTasks      DueCheckEnqueuer
	qualification *QualificationService
}

func NewCommissionService(store commissionStore, dueTasks DueCheckEnqueuer, qualification *QualificationService) (*CommissionService, error) {
	if store == nil || dueTasks == nil || qualification == nil {
		return nil, distributionport.ErrUnavailable
	}
	return &CommissionService{store: store, dueTasks: dueTasks, qualification: qualification}, nil
}

func (s *CommissionService) ConsumePaidEventWithin(ctx context.Context, event orderport.PaidEvent) error {
	if s == nil || s.store == nil || s.dueTasks == nil || s.qualification == nil || !event.Valid() {
		return distributionport.ErrUnavailable
	}
	if _, err := platformpostgres.RequireTransaction(ctx); err != nil {
		return distributionport.ErrUnavailable
	}
	attributionContext, err := s.store.ReadAttributionQualificationContextByOrderItemWithin(ctx, event.OrderID, 1, true)
	attribution := attributionContext.Attribution
	if errors.Is(err, distributionport.ErrNotFound) {
		// A plain checkout has no Distribution fact; Order's paid event remains
		// authoritative and needs no Distribution side effect.
		return nil
	}
	if err != nil {
		return err
	}
	if attribution.OrderID != event.OrderID || attribution.OrderItemLine != 1 {
		return distributionport.ErrConflict
	}
	var paidMinor int64
	for _, item := range event.Order.Items {
		if item.LineNo == attribution.OrderItemLine {
			paidMinor = item.LineAmountMinor
			break
		}
	}
	if paidMinor < 1 || event.Order.Amount.Currency != "CNY" || paidMinor > event.Order.Amount.AmountMinor {
		return distributionport.ErrUnavailable
	}
	commission, err := distributiondomain.NewCommission(attribution, paidMinor, event.OccurredAt)
	if err != nil {
		return distributionport.ErrConflict
	}
	// A qualifying purchase can be refunded after checkout but before this
	// paid callback. Recheck in the same UoW and retain a visible held/cancelled
	// fact instead of failing the authoritative payment callback.
	qualification, qualificationErr := s.qualification.CheckWithin(ctx, attributionContext.DistributorCustomerID, attributionContext.ProductID, attributionContext.ProductType)
	if qualificationErr != nil {
		qualification = distributiondomain.Qualification{State: distributiondomain.QualificationUnavailable, Reason: "qualification_evidence_unavailable", CheckedAt: event.OccurredAt.UTC()}
	}
	if commission.Status == distributiondomain.CommissionPending {
		switch qualification.State {
		case distributiondomain.QualificationIneligible:
			commission, err = commission.CancelUnsettled(commission.Version, "qualification_revoked", event.OccurredAt)
		case distributiondomain.QualificationEligible:
			// normal pending commission
		default:
			commission, err = commission.Hold(commission.Version, "qualification_evidence_pending", event.OccurredAt)
		}
		if err != nil {
			return distributionport.ErrConflict
		}
	}
	saved, created, err := s.store.InsertCommissionWithin(ctx, commission)
	if err != nil {
		return err
	}
	if !created {
		return nil
	}
	if saved.Status == distributiondomain.CommissionPending || saved.Status == distributiondomain.CommissionHeld || saved.Status == distributiondomain.CommissionCancelled {
		if err = s.dueTasks.EnqueueCommissionDueWithin(ctx, saved.ID, saved.DueAt); err != nil {
			return err
		}
	}
	payload := map[string]any{
		"commission_id": saved.ID,
		"order_id":      event.OrderID,
		"paid_event_id": event.ID,
		"initial_minor": saved.InitialMinor,
		"status":        saved.Status,
		"qualification": qualification.State,
	}
	if err = s.store.AppendAuditWithin(ctx, "distribution.commission_created.v1", "commission", saved.ID, "order-paid-event:"+strconv.FormatInt(event.ID, 10), payload, event.OccurredAt); err != nil {
		return err
	}
	return s.store.AppendOutboxWithin(ctx, "distribution.commission_created.v1", "distribution.commission:paid-event:"+strconv.FormatInt(event.ID, 10), saved.ID, payload, event.OccurredAt)
}

var _ orderport.PaidEventConsumer = (*CommissionService)(nil)
