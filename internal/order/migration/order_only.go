package migration

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"

	orderdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
)

var ErrNotOrderOnly = errors.New("manifest is not an approved order-only snapshot")

type OrderOnlyRunner struct {
	Orders orderport.HistoricalImporter
	Runs   OrderOnlyRunStore
}

type OrderOnlyRunStore interface {
	Begin(context.Context, string, [32]byte, int64) error
	CompleteOrders(context.Context, string, int64) error
}

type OrderOnlyReconciliation struct {
	Matched        bool  `json:"matched"`
	Orders         int64 `json:"orders"`
	AmountMinor    int64 `json:"amount_minor"`
	Imported       int64 `json:"imported"`
	Replayed       int64 `json:"replayed"`
	Floating       int64 `json:"floating"`
	EffectEligible int64 `json:"effect_eligible"`
}

// ValidateOrderOnly is intentionally narrower than the full commerce
// migration contract. It accepts the audited historical WeChat Pay order
// snapshot and categorically excludes identity, payment, refund and provider
// effects.
func ValidateOrderOnly(manifest Manifest) error {
	if err := manifest.Validate(false); err != nil {
		return err
	}
	coverage := manifest.Coverage
	if !coverage.WeChatPayOrders || coverage.Identities || coverage.WeChatPayRefunds || coverage.WeChatShopOrders || coverage.WeChatShopRefunds || coverage.AlipayOrders || len(manifest.Subjects) != 0 || len(manifest.Identities) != 0 || len(manifest.IdentityQuarantines) != 0 || len(manifest.Refunds) != 0 || len(manifest.Orders) == 0 {
		return ErrNotOrderOnly
	}
	for _, row := range manifest.Orders {
		if row.Provider != orderdomain.ProviderWeChatPay || row.PayerIdentityKey != "" || row.PayerSubjectKey != "" || row.BeneficiarySubjectKey != "" {
			return ErrNotOrderOnly
		}
	}
	return nil
}

func (runner OrderOnlyRunner) Apply(ctx context.Context, manifest Manifest) (Result, error) {
	if runner.Orders == nil || runner.Runs == nil {
		return Result{}, errors.New("order-only migration runner is not configured")
	}
	if err := ValidateOrderOnly(manifest); err != nil {
		return Result{}, err
	}
	if err := runner.Runs.Begin(ctx, manifest.RunKey, manifest.Digest, int64(len(manifest.Orders))); err != nil {
		return Result{}, err
	}
	result := Result{}
	for _, row := range manifest.Orders {
		status, err := orderStatus(row.Status)
		if err != nil {
			return result, err
		}
		items := make([]orderdomain.ItemSnapshot, 0, len(row.Items))
		for _, item := range row.Items {
			items = append(items, orderdomain.ItemSnapshot{LineNo: item.LineNo, ProductCode: item.ProductCode, ProductName: item.ProductName, UnitAmountMinor: item.UnitAmountMinor, Quantity: item.Quantity, LineAmountMinor: item.LineAmountMinor})
		}
		raw, _ := json.Marshal(row)
		digest := sha256.Sum256(raw)
		_, err = runner.Orders.ImportHistorical(ctx, orderport.HistoricalImportCommand{RunID: manifest.RunKey, SourceDigest: digest, Order: orderdomain.Snapshot{
			Provider: row.Provider, SourceSystem: "commerce-history", SourceKey: row.SourceKey,
			MerchantOrderNo: row.MerchantOrderNo, ProviderTransactionNo: row.ProviderTransactionNo,
			Amount: orderdomain.Money{AmountMinor: row.AmountMinor, Currency: row.Currency}, Status: status,
			Items: items, RecordOrigin: orderdomain.RecordOriginHistory, EffectEligible: false, Version: 1,
			CreatedAt: row.CreatedAt.UTC(), UpdatedAt: row.UpdatedAt.UTC(),
		}})
		if err != nil {
			return result, err
		}
		result.Orders++
	}
	if err := runner.Runs.CompleteOrders(ctx, manifest.RunKey, int64(result.Orders)); err != nil {
		return result, err
	}
	return result, nil
}

func DigestMatches(manifest Manifest, provided [32]byte) bool {
	return subtle.ConstantTimeCompare(manifest.Digest[:], provided[:]) == 1
}
