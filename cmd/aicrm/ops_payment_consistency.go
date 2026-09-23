package main

import (
	"context"
	"errors"
	"time"

	opsport "github.com/qianlan33333-png/AI-CRM-v3/internal/adminops/port"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

// OneID: reads existing canonical party references without resolving or
// changing identity. Persistence: one bounded readonly repeatable-read UoW;
// comparison remains in composition and each Owner supplies only its facts.
func opsPaymentOrderConsistencyCollector(snapshot platformport.UnitOfWork, payments paymentport.OpsPaidOrderReader, orders orderport.OpsPaymentOrderReader) opsport.InspectionCollector {
	return opsport.CollectorFunc{ID: "payment.order_consistency", Read: func(ctx context.Context, at time.Time) (opsport.CheckObservation, error) {
		out := opsport.CheckObservation{Status: "unknown", Code: "settlement_evidence_unavailable", ObservedAt: at, Metrics: map[string]int64{}}
		if snapshot == nil || payments == nil || orders == nil {
			return out, errors.New("settlement diagnostic dependencies unavailable")
		}
		ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		keys := []string{"observed_paid_payments", "missing_order", "wrong_order_origin", "paid_state_mismatch", "amount_currency_mismatch", "party_mismatch", "refund_total_mismatch", "missing_paid_time", "checkout_unknown", "checkout_amount_mismatch", "missing_paid_event", "missing_service_grant", "service_grant_mismatch", "missing_service_refund", "external_delivery_outside_scope", "scan_truncated"}
		for _, key := range keys {
			out.Metrics[key] = 0
		}
		e := snapshot.Within(ctx, func(txctx context.Context) error {
			var after int64
			for pageNo := 0; pageNo < 20; pageNo++ {
				page, e := payments.ReadOpsPaidOrderPageWithin(txctx, after, 500)
				if e != nil {
					return e
				}
				if len(page.Items) == 0 {
					if page.More {
						return errors.New("invalid settlement diagnostic page")
					}
					return nil
				}
				ids := make([]int64, 0, len(page.Items))
				for _, p := range page.Items {
					if p.OrderID <= after {
						return errors.New("unordered settlement diagnostic page")
					}
					after = p.OrderID
					ids = append(ids, p.OrderID)
				}
				facts, e := orders.ReadOpsPaymentOrderFactsWithin(txctx, ids)
				if e != nil {
					return e
				}
				byID := map[int64]orderport.OpsPaymentOrderFact{}
				for _, o := range facts {
					if _, exists := byID[o.OrderID]; exists {
						return errors.New("duplicate order diagnostic fact")
					}
					byID[o.OrderID] = o
				}
				for _, p := range page.Items {
					out.Metrics["observed_paid_payments"]++
					o, found := byID[p.OrderID]
					if !found {
						out.Metrics["missing_order"]++
						continue
					}
					compareOpsSettlement(out.Metrics, p, o)
				}
				if !page.More {
					return nil
				}
			}
			out.Metrics["scan_truncated"] = 1
			return nil
		})
		if e != nil {
			return out, e
		}
		out.Status = "ok"
		out.Code = "native_paid_settlement_consistent"
		for _, key := range []string{"missing_paid_time", "checkout_unknown", "missing_paid_event", "scan_truncated"} {
			if out.Metrics[key] > 0 {
				out.Status = "unknown"
				out.Code = "settlement_evidence_incomplete"
			}
		}
		for _, key := range []string{"missing_order", "wrong_order_origin", "paid_state_mismatch", "amount_currency_mismatch", "party_mismatch", "refund_total_mismatch", "checkout_amount_mismatch", "missing_service_grant", "service_grant_mismatch", "missing_service_refund"} {
			if out.Metrics[key] > 0 {
				out.Status = "critical"
				out.Code = "settlement_invariant_violation"
			}
		}
		return out, nil
	}}
}
func compareOpsSettlement(count map[string]int64, p paymentport.OpsPaidOrderFact, o orderport.OpsPaymentOrderFact) {
	if o.RecordOrigin != "native" {
		count["wrong_order_origin"]++
	}
	if o.Status != "paid" && o.Status != "partially_refunded" && o.Status != "refunded" && o.Status != "closed" {
		count["paid_state_mismatch"]++
	}
	if p.AmountMinor != o.AmountMinor || p.Currency != o.Currency || p.Provider != o.Provider {
		count["amount_currency_mismatch"]++
	}
	if p.PayerCustomerID != o.PayerCustomerID || p.BeneficiaryCustomerID != o.BeneficiaryCustomerID {
		count["party_mismatch"]++
	}
	if p.CompletedRefundMinor != o.RefundedMinor {
		count["refund_total_mismatch"]++
	}
	if p.PaidConfirmedAt == nil {
		count["missing_paid_time"]++
	}
	if !o.PaidEventPresent {
		count["missing_paid_event"]++
	}
	if !o.CheckoutPresent {
		count["checkout_unknown"]++
		return
	}
	if o.CheckoutPayableMinor != p.AmountMinor || o.CheckoutCurrency != p.Currency {
		count["checkout_amount_mismatch"]++
	}
	if o.CheckoutProductType == "service_period" {
		if !o.ServiceGrantPresent {
			count["missing_service_grant"]++
		} else if !o.ServiceGrantMatches {
			count["service_grant_mismatch"]++
		}
		if p.CompletedRefundMinor > 0 && !o.ServiceRefundPresent {
			count["missing_service_refund"]++
		}
	} else {
		count["external_delivery_outside_scope"]++
	}
}
