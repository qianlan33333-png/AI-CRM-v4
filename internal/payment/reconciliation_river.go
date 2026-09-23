package payment

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

const (
	ReconciliationQueue = "payment-reconciliation"

	defaultReconciliationMaxAttempts          = 12
	weChatPayPaymentReconciliationMaxAttempts = 16
)

var errReconciliationPending = errors.New("payment reconciliation is pending")

type ReconciliationJobArgs struct {
	Provider  domain.Provider `json:"provider"`
	PaymentID int64           `json:"payment_id,omitempty"`
	RefundID  int64           `json:"refund_id,omitempty"`
}

func (ReconciliationJobArgs) Kind() string { return "payment.provider-reconciliation.v1" }

type RiverReconciliationEnqueuer struct {
	client *river.Client[pgx.Tx]
}

func NewRiverReconciliationEnqueuer(client *river.Client[pgx.Tx]) (*RiverReconciliationEnqueuer, error) {
	if client == nil {
		return nil, paymentport.ErrUnavailable
	}
	return &RiverReconciliationEnqueuer{client: client}, nil
}

func reconciliationMaxAttempts(target paymentport.ReconciliationTarget) int {
	if target.Provider == domain.ProviderWeChatPay && target.PaymentID > 0 && target.RefundID == 0 {
		return weChatPayPaymentReconciliationMaxAttempts
	}
	return defaultReconciliationMaxAttempts
}

func (enqueuer *RiverReconciliationEnqueuer) EnqueueWithin(ctx context.Context, target paymentport.ReconciliationTarget) error {
	validPayment := (target.Provider == domain.ProviderWeChatPay || target.Provider == domain.ProviderAlipay) && target.PaymentID > 0 && target.RefundID == 0
	validRefund := (target.Provider == domain.ProviderWeChatPay || target.Provider == domain.ProviderWeChatShop || target.Provider == domain.ProviderAlipay) && target.RefundID > 0 && target.PaymentID == 0
	if enqueuer == nil || enqueuer.client == nil || (!validPayment && !validRefund) {
		return paymentport.ErrUnavailable
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	_, err = enqueuer.client.InsertTx(ctx, tx, ReconciliationJobArgs{Provider: target.Provider, PaymentID: target.PaymentID, RefundID: target.RefundID}, &river.InsertOpts{Queue: ReconciliationQueue, MaxAttempts: reconciliationMaxAttempts(target)})
	return err
}

type ReconciliationApplication interface {
	ReconcileShopRefund(context.Context, int64) (domain.Refund, error)
	ReconcileWeChatPayPayment(context.Context, int64) (domain.Payment, error)
	ReconcileWeChatPayRefund(context.Context, int64) (domain.Refund, error)
	ReconcileAlipayPayment(context.Context, int64) (domain.Payment, error)
	ReconcileAlipayRefund(context.Context, int64) (domain.Refund, error)
}

type ReconciliationWorker struct {
	river.WorkerDefaults[ReconciliationJobArgs]
	service ReconciliationApplication
}

func NewReconciliationWorker() *ReconciliationWorker { return &ReconciliationWorker{} }

func (*ReconciliationWorker) Timeout(*river.Job[ReconciliationJobArgs]) time.Duration {
	return 30 * time.Second
}

func (worker *ReconciliationWorker) BindService(service ReconciliationApplication) error {
	if worker == nil || worker.service != nil || service == nil {
		return paymentport.ErrUnavailable
	}
	worker.service = service
	return nil
}

func (worker *ReconciliationWorker) Work(ctx context.Context, job *river.Job[ReconciliationJobArgs]) error {
	if worker == nil || worker.service == nil || job == nil || job.JobRow == nil {
		return paymentport.ErrUnavailable
	}
	if job.Args.Provider == domain.ProviderWeChatPay && job.Args.PaymentID > 0 && job.Args.RefundID == 0 {
		payment, err := worker.service.ReconcileWeChatPayPayment(ctx, job.Args.PaymentID)
		if err != nil {
			return err
		}
		if payment.Status != domain.StatusPaid && payment.Status != domain.StatusFailed && payment.Status != domain.StatusCancelled {
			return errReconciliationPending
		}
		return nil
	}
	if job.Args.Provider == domain.ProviderAlipay && job.Args.PaymentID > 0 && job.Args.RefundID == 0 {
		payment, err := worker.service.ReconcileAlipayPayment(ctx, job.Args.PaymentID)
		if err != nil {
			return err
		}
		if payment.Status != domain.StatusPaid && payment.Status != domain.StatusFailed && payment.Status != domain.StatusCancelled {
			return errReconciliationPending
		}
		return nil
	}
	if job.Args.RefundID > 0 && job.Args.PaymentID == 0 {
		var refund domain.Refund
		var err error
		if job.Args.Provider == domain.ProviderWeChatPay {
			refund, err = worker.service.ReconcileWeChatPayRefund(ctx, job.Args.RefundID)
		} else if job.Args.Provider == domain.ProviderWeChatShop {
			refund, err = worker.service.ReconcileShopRefund(ctx, job.Args.RefundID)
		} else if job.Args.Provider == domain.ProviderAlipay {
			refund, err = worker.service.ReconcileAlipayRefund(ctx, job.Args.RefundID)
		} else {
			return paymentport.ErrUnavailable
		}
		if err != nil {
			return err
		}
		if refund.Status != domain.RefundCompleted && refund.Status != domain.RefundFinalFailed {
			return errReconciliationPending
		}
		return nil
	}
	return paymentport.ErrUnavailable
}

var _ paymentport.ReconciliationEnqueuer = (*RiverReconciliationEnqueuer)(nil)
