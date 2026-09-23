package migration

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	orderdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	paymentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

type Runner struct {
	UOW                platformport.UnitOfWork
	Identities         identityport.HistoricalSubjectProvisioner
	Facts              identityport.HistoricalFactFactory
	IdentityRuns       IdentityRunStore
	Orders             orderport.HistoricalImporter
	Payments           paymentport.HistoricalImporter
	Runs               RunStore
	DeltaOrders        orderport.HistoricalDeltaImporter
	DeltaPreconditions map[string]orderport.HistoricalDeltaPrecondition
}

type IdentityRunStore interface {
	RecordSubject(context.Context, string, string, [32]byte, int64, int) error
	RecordQuarantine(context.Context, string, string, [32]byte, string, string) error
}

type RunStore interface {
	Begin(context.Context, string, [32]byte, int64) error
	Complete(context.Context, string, int64) error
}

type Result struct{ Subjects, Identities, IdentityQuarantines, Orders, Payments, Refunds int }

func (runner Runner) Apply(ctx context.Context, manifest Manifest) (Result, error) {
	if runner.UOW == nil || runner.Identities == nil || runner.Facts == nil || runner.IdentityRuns == nil || runner.Orders == nil || runner.Payments == nil || runner.Runs == nil {
		return Result{}, errors.New("migration runner is not configured")
	}
	if err := manifest.Validate(true); err != nil {
		return Result{}, err
	}
	inputCount := len(manifest.Subjects) + len(manifest.IdentityQuarantines) + len(manifest.Orders) + len(manifest.Refunds)
	if err := runner.Runs.Begin(ctx, manifest.RunKey, manifest.Digest, int64(inputCount)); err != nil {
		return Result{}, err
	}
	facts := make(map[string]identitydomain.VerifiedFact, len(manifest.Identities))
	result := Result{}
	for _, row := range manifest.Identities {
		fact, err := runner.Facts.VerifiedHistoricalFact(identityport.HistoricalVerifiedInput{Kind: row.Kind, Scope: row.Scope, Value: row.Value, Source: row.Source})
		if err != nil {
			return result, fmt.Errorf("identity %s: %w", row.SourceKey, err)
		}
		facts[row.SourceKey] = fact
	}
	type subjectResult struct {
		customerID int64
		identities map[string]int64
	}
	subjects := make(map[string]subjectResult, len(manifest.Subjects))
	for _, row := range manifest.Subjects {
		subjectFacts := make([]identitydomain.VerifiedFact, 0, len(row.IdentityKeys))
		for _, key := range row.IdentityKeys {
			subjectFacts = append(subjectFacts, facts[key])
		}
		raw, _ := json.Marshal(struct {
			Subject SubjectRow    `json:"subject"`
			Rows    []IdentityRow `json:"identities"`
		}{Subject: row, Rows: identityRows(manifest.Identities, row.IdentityKeys)})
		digest := sha256.Sum256(raw)
		var provision identityport.HistoricalSubjectResult
		err := runner.UOW.Within(ctx, func(tx context.Context) error {
			var inner error
			provision, inner = runner.Identities.ProvisionHistoricalSubject(tx, identityport.HistoricalSubjectCommand{SubjectKey: row.SourceKey, Facts: subjectFacts, SourceDigest: digest})
			if inner != nil {
				return inner
			}
			return runner.IdentityRuns.RecordSubject(tx, manifest.RunKey, row.SourceKey, digest, int64(provision.CustomerID), len(provision.IdentityIDs))
		})
		if err != nil {
			return result, err
		}
		byKey := make(map[string]int64, len(row.IdentityKeys))
		for index, key := range row.IdentityKeys {
			byKey[key] = provision.IdentityIDs[index]
		}
		subjects[row.SourceKey] = subjectResult{customerID: int64(provision.CustomerID), identities: byKey}
		result.Subjects++
		result.Identities += len(provision.IdentityIDs)
	}
	for _, row := range manifest.IdentityQuarantines {
		raw, _ := json.Marshal(row)
		digest := sha256.Sum256(raw)
		if err := runner.UOW.Within(ctx, func(tx context.Context) error {
			return runner.IdentityRuns.RecordQuarantine(tx, manifest.RunKey, row.SourceKey, digest, row.ReasonCode, row.EvidenceDigest)
		}); err != nil {
			return result, err
		}
		result.IdentityQuarantines++
	}
	applyBusiness := func(ctx context.Context) error {
		refundedByOrder := make(map[string]int64, len(manifest.Refunds))
		for _, refund := range manifest.Refunds {
			if refund.Completed() {
				refundedByOrder[string(refund.Provider)+"\x00"+refund.MerchantOrderNo] += refund.AmountMinor
			}
		}
		orders := make(map[string]struct{ orderID, paymentID int64 }, len(manifest.Orders))
		for _, row := range manifest.Orders {
			payer := subjects[row.PayerSubjectKey]
			beneficiary := subjects[row.BeneficiarySubjectKey]
			var payerID, beneficiaryID *int64
			payerIdentityID := int64(0)
			if row.PayerIdentityKey != "" {
				payerID = &payer.customerID
				if row.BeneficiarySubjectKey != "" {
					beneficiaryID = &beneficiary.customerID
				}
				payerIdentityID = payer.identities[row.PayerIdentityKey]
			}
			status, err := orderStatus(row.Status)
			if err != nil {
				return err
			}
			items := make([]orderdomain.ItemSnapshot, 0, len(row.Items))
			for _, item := range row.Items {
				items = append(items, orderdomain.ItemSnapshot{LineNo: item.LineNo, ProductCode: item.ProductCode, ProductName: item.ProductName, UnitAmountMinor: item.UnitAmountMinor, Quantity: item.Quantity, LineAmountMinor: item.LineAmountMinor})
			}
			snapshot := orderdomain.Snapshot{Provider: row.Provider, SourceSystem: "commerce-history", SourceKey: row.SourceKey, MerchantOrderNo: row.MerchantOrderNo, ProviderTransactionNo: row.ProviderTransactionNo, PayerCustomerID: payerID, BeneficiaryCustomerID: beneficiaryID, Amount: orderdomain.Money{AmountMinor: row.AmountMinor, Currency: row.Currency}, RefundedMinor: refundedByOrder[string(row.Provider)+"\x00"+row.MerchantOrderNo], Status: status, Items: items, RecordOrigin: orderdomain.RecordOriginHistory, EffectEligible: false, Version: 1, CreatedAt: row.CreatedAt.UTC(), UpdatedAt: row.UpdatedAt.UTC()}
			raw, _ := json.Marshal(row)
			digest := sha256.Sum256(raw)
			var imported orderdomain.Snapshot
			command := orderport.HistoricalImportCommand{RunID: manifest.RunKey, SourceDigest: digest, Order: snapshot}
			if precondition, ok := runner.DeltaPreconditions[row.SourceKey]; ok {
				if runner.DeltaOrders == nil {
					return errors.New("history delta importer missing")
				}
				imported, err = runner.DeltaOrders.ApplyHistoricalDeltaWithin(ctx, orderport.HistoricalDeltaCommand{HistoricalImportCommand: command, Before: precondition})
			} else {
				imported, err = runner.Orders.ImportHistorical(ctx, command)
			}
			if err != nil {
				return err
			}
			entry := struct{ orderID, paymentID int64 }{orderID: imported.ID}
			result.Orders++
			paymentStatus, hasPayment := historicalPaymentStatus(status)
			if hasPayment && row.Provider != orderdomain.ProviderAlipay {
				var paidConfirmedAt *time.Time
				if row.PaidConfirmedAt != nil {
					value := row.PaidConfirmedAt.UTC()
					paidConfirmedAt = &value
				}
				transactionDigest := ""
				if row.ProviderTransactionNo != "" {
					transactionDigest = string(effectport.Hash("history.transaction", row.ProviderTransactionNo))
				}
				payment := paymentdomain.Payment{Historical: true, SourceStatus: row.SourceStatus, HistoryReason: row.HistoryReason, OrderID: imported.ID, Provider: paymentdomain.Provider(row.Provider), MerchantOrderNo: row.MerchantOrderNo, PayerIdentityID: payerIdentityID, PayerCustomerID: payer.customerID, BeneficiaryCustomerID: beneficiary.customerID, AmountMinor: row.AmountMinor, Currency: row.Currency, Status: paymentStatus, ProviderTransactionDigest: transactionDigest, Version: 1, PaidConfirmedAt: paidConfirmedAt, CreatedAt: row.CreatedAt.UTC(), UpdatedAt: row.UpdatedAt.UTC()}
				persisted, err := runner.Payments.ImportTerminalPayment(ctx, payment, digest, manifest.RunKey)
				if err != nil {
					return err
				}
				entry.paymentID = persisted.ID
				result.Payments++
			}
			orders[string(row.Provider)+"\x00"+row.MerchantOrderNo] = entry
		}
		for _, row := range manifest.Refunds {
			entry, ok := orders[string(row.Provider)+"\x00"+row.MerchantOrderNo]
			if !ok || entry.paymentID < 1 {
				return ErrInvalidManifest
			}
			raw, _ := json.Marshal(row)
			digest := sha256.Sum256(raw)
			refundDigest := ""
			if row.ProviderRefundNo != "" {
				refundDigest = string(effectport.Hash("history.refund", row.ProviderRefundNo))
			}
			refund := paymentdomain.Refund{PaymentID: entry.paymentID, Provider: paymentdomain.Provider(row.Provider), RefundNo: row.RefundNo, Reason: row.Reason, AmountMinor: row.AmountMinor, Status: paymentdomain.RefundStatus(row.HistoricalStatus()), ProviderRefundDigest: refundDigest, Version: 1, CreatedAt: row.OccurredAt.UTC(), UpdatedAt: row.OccurredAt.UTC()}
			if _, err := runner.Payments.ImportTerminalRefund(ctx, refund, digest, manifest.RunKey); err != nil {
				return err
			}
			result.Refunds++
		}

		return nil
	}
	var businessErr error
	if runner.DeltaOrders != nil {
		businessErr = runner.UOW.Within(ctx, applyBusiness)
	} else {
		businessErr = applyBusiness(ctx)
	}
	if businessErr != nil {
		return result, businessErr
	}
	if err := runner.Runs.Complete(ctx, manifest.RunKey, int64(result.Subjects+result.IdentityQuarantines+result.Orders+result.Refunds)); err != nil {
		return result, err
	}
	return result, nil
}

func identityRows(rows []IdentityRow, keys []string) []IdentityRow {
	byKey := make(map[string]IdentityRow, len(rows))
	for _, row := range rows {
		byKey[row.SourceKey] = row
	}
	result := make([]IdentityRow, 0, len(keys))
	for _, key := range keys {
		result = append(result, byKey[key])
	}
	return result
}

func historicalPaymentStatus(status orderdomain.Status) (paymentdomain.Status, bool) {
	switch status {
	case orderdomain.StatusPaid, orderdomain.StatusPartiallyRefunded, orderdomain.StatusRefunded:
		return paymentdomain.StatusPaid, true
	case orderdomain.StatusPaymentFailed:
		return paymentdomain.StatusFailed, true
	case orderdomain.StatusCancelled, orderdomain.StatusClosed:
		return paymentdomain.StatusCancelled, true
	default:
		return "", false
	}
}

func orderStatus(value string) (orderdomain.Status, error) {
	switch orderdomain.Status(value) {
	case orderdomain.StatusPendingPayment, orderdomain.StatusPaid, orderdomain.StatusPartiallyRefunded, orderdomain.StatusRefunded, orderdomain.StatusCancelled, orderdomain.StatusPaymentFailed, orderdomain.StatusClosed:
		return orderdomain.Status(value), nil
	default:
		return "", ErrInvalidManifest
	}
}

var _ = time.Time{}
