package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	identityadapter "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/adapter"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identitymigration "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/migration"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	identitystore "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/store"
	orderapp "github.com/qianlan33333-png/AI-CRM-v3/internal/order/app"
	ordermigration "github.com/qianlan33333-png/AI-CRM-v3/internal/order/migration"
	orderstore "github.com/qianlan33333-png/AI-CRM-v3/internal/order/store"
	paymentapp "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/app"
	paymentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	paymentmigration "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/migration"
	paymentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/store"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

type options struct {
	mode, snapshot, digest, deltaPreconditions string
	confirm, orderOnly, existingOnly           bool
}

type historyIdentityResolution struct {
	CustomerID int64
	IdentityID int64
}

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "commerce migration failed:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("migrate-commerce-history", flag.ContinueOnError)
	var cfg options
	flags.StringVar(&cfg.mode, "mode", "inspect", "inspect|dry-run|apply|reconcile")
	flags.StringVar(&cfg.snapshot, "snapshot", "", "path to normalized snapshot")
	flags.StringVar(&cfg.digest, "manifest-sha256", "", "required snapshot sha256")
	flags.StringVar(&cfg.deltaPreconditions, "history-delta-preconditions", "", "protected target CAS evidence bound to exact manifest; full import only")
	flags.BoolVar(&cfg.existingOnly, "existing-identities-only", false, "resolve already existing identities without provisioning or assurance upgrades")
	flags.BoolVar(&cfg.confirm, "confirm-apply", false, "confirm the exact apply manifest")
	flags.BoolVar(&cfg.orderOnly, "order-only", false, "accept only the audited floating WeChat Pay order snapshot")
	if err := flags.Parse(args); err != nil || cfg.snapshot == "" {
		return errors.New("snapshot is required")
	}
	manifest, err := ordermigration.Load(cfg.snapshot)
	if err != nil {
		return err
	}
	if cfg.deltaPreconditions != "" {
		if _, err := loadDeltaPreconditions(cfg.deltaPreconditions, manifest); err != nil {
			return err
		}
	}
	summary := manifest.Summary()
	if cfg.orderOnly {
		if cfg.deltaPreconditions != "" {
			return errors.New("history delta cannot be order-only")
		}
		if err = ordermigration.ValidateOrderOnly(manifest); err != nil {
			return err
		}
	}
	if cfg.mode == "inspect" {
		return printJSON(map[string]any{"mode": cfg.mode, "order_only": cfg.orderOnly, "manifest_sha256": hex.EncodeToString(manifest.Digest[:]), "summary": summary})
	}
	if !cfg.orderOnly {
		err = manifest.Validate(true)
	}
	if err != nil {
		return err
	}
	if cfg.mode == "dry-run" {
		return printJSON(map[string]any{"mode": cfg.mode, "order_only": cfg.orderOnly, "eligible": cfg.deltaPreconditions == "", "target_delta_cas_pending": cfg.deltaPreconditions != "", "manifest_sha256": hex.EncodeToString(manifest.Digest[:]), "summary": summary})
	}
	provided, err := hex.DecodeString(cfg.digest)
	if err != nil || len(provided) != 32 || string(provided) != string(manifest.Digest[:]) {
		return errors.New("manifest digest confirmation mismatch")
	}
	databaseURL, err := platformconfig.DatabaseURL()
	if err != nil {
		return err
	}
	pool, err := platformpostgres.Open(ctx, platformpostgres.Config{URL: databaseURL, MaxConnections: 10, MinConnections: 1})
	if err != nil {
		return err
	}
	defer pool.Close()
	runs := ordermigration.PostgreSQLRuns{Pool: pool.Native()}
	if cfg.mode == "reconcile" && cfg.orderOnly {
		result, reconcileErr := runs.ReconcileOrders(ctx, manifest)
		if reconcileErr != nil {
			return reconcileErr
		}
		return printJSON(map[string]any{"mode": "reconcile", "order_only": true, "run_key": manifest.RunKey, "result": result})
	}
	if cfg.mode == "reconcile" {
		return reconcile(ctx, pool, manifest)
	}
	if cfg.mode != "apply" || !cfg.confirm {
		return errors.New("apply requires --confirm-apply")
	}
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		return err
	}
	orderRepository, err := orderstore.NewPostgreSQL(pool.Native(), uow)
	if err != nil {
		return err
	}
	orderService := orderapp.NewService(uow, orderRepository)
	if cfg.orderOnly {
		result, applyErr := (ordermigration.OrderOnlyRunner{Orders: orderService, Runs: runs}).Apply(ctx, manifest)
		if applyErr != nil {
			return applyErr
		}
		return printJSON(map[string]any{"mode": "apply", "order_only": true, "result": result, "run_key": manifest.RunKey})
	}
	identity := identityapp.OneIDService{Store: identitystore.NewPostgresStore()}
	paymentRepository := paymentstore.NewPostgreSQL()
	runner := ordermigration.Runner{UOW: uow, Identities: identity, Facts: identityadapter.ProviderHistory{}, IdentityRuns: identitymigration.PostgreSQLReceipts{}, Orders: orderService, Payments: paymentapp.NewService(uow, paymentRepository, nil, nil, nil), Runs: runs}
	if cfg.existingOnly {
		runner.Identities = existingIdentitiesOnly{Resolver: identity}
	}
	if cfg.deltaPreconditions != "" {
		runner.DeltaPreconditions, err = loadDeltaPreconditions(cfg.deltaPreconditions, manifest)
		if err != nil {
			return err
		}
		runner.DeltaOrders = orderRepository
		runner.Orders = withinOrderImporter{repository: orderRepository}
		runner.Payments = paymentRepository
	}
	result, err := runner.Apply(ctx, manifest)
	if err != nil {
		return err
	}
	return printJSON(map[string]any{"mode": "apply", "result": result, "run_key": manifest.RunKey})
}

func reconcile(ctx context.Context, pool *platformpostgres.Pool, manifest ordermigration.Manifest) error {
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		return err
	}
	oneID := identityapp.OneIDService{Store: identitystore.NewPostgresStore()}
	identityByKey := make(map[string]ordermigration.IdentityRow, len(manifest.Identities))
	for _, row := range manifest.Identities {
		identityByKey[row.SourceKey] = row
	}
	resolvedIdentities := 0
	subjectCustomers := make(map[string]int64, len(manifest.Subjects))
	identityResolutions := make(map[string]historyIdentityResolution, len(manifest.Identities))
	for _, subject := range manifest.Subjects {
		var subjectCustomer int64
		for _, identityKey := range subject.IdentityKeys {
			row := identityByKey[identityKey]
			var resolved identityport.ResolveResult
			err = uow.Within(ctx, func(tx context.Context) error {
				var resolveErr error
				resolved, resolveErr = oneID.Resolve(tx, identitydomain.Reference{Kind: identitydomain.Kind(row.Kind), Scope: row.Scope, Value: row.Value, Assurance: identitydomain.AssuranceVerified, Source: row.Source})
				return resolveErr
			})
			if err != nil || resolved.Status != identityport.ResolveFound || resolved.CustomerID < 1 || resolved.IdentityID < 1 || (subjectCustomer > 0 && int64(resolved.CustomerID) != subjectCustomer) {
				return errors.New("commerce identity reconciliation mismatch")
			}
			subjectCustomer = int64(resolved.CustomerID)
			identityResolutions[identityKey] = historyIdentityResolution{CustomerID: int64(resolved.CustomerID), IdentityID: int64(resolved.IdentityID)}
			resolvedIdentities++
		}
		subjectCustomers[subject.SourceKey] = subjectCustomer
	}
	identitySubjects, identityQuarantines, err := identityReceiptExpectations(manifest, subjectCustomers)
	if err != nil {
		return err
	}
	identityChecks, err := (identitymigration.PostgreSQLReceiptVerifier{Pool: pool.Native()}).Verify(ctx, manifest.RunKey, identitySubjects, identityQuarantines)
	if err != nil {
		return err
	}
	runs := ordermigration.PostgreSQLRuns{Pool: pool.Native()}
	full, err := runs.VerifyFull(ctx, manifest, ordermigration.FullReconciliationInput{SubjectCustomerIDs: subjectCustomers})
	if err != nil {
		return err
	}
	orderIDs, paymentFacts, refundFacts, err := paymentHistoryFacts(manifest, full.OrderIDs, subjectCustomers, identityResolutions)
	if err != nil {
		return err
	}
	paymentChecks, err := (paymentmigration.PostgreSQLVerifier{Pool: pool.Native()}).VerifyHistorical(ctx, manifest.RunKey, orderIDs, paymentFacts, refundFacts)
	if err != nil {
		return err
	}
	summary := manifest.Summary()
	if resolvedIdentities != summary.IdentityRows || identityChecks.Canonical != int64(summary.SubjectRows) || identityChecks.Quarantined != int64(summary.IdentityQuarantineRows) || full.Orders != int64(summary.OrderRows) || paymentChecks.Payments != int64(summary.PaymentRows) || paymentChecks.Refunds != int64(summary.RefundRows) || full.AmountMinor != summary.AmountMinor || paymentChecks.RefundMinor != summary.RefundMinor {
		return ordermigration.ErrReconciliationMismatch
	}
	if err = runs.MarkReconciled(ctx, manifest); err != nil {
		return err
	}
	return printJSON(map[string]any{"mode": "reconcile", "matched": true, "subjects": identityChecks.Canonical, "identities": resolvedIdentities, "identity_quarantines": identityChecks.Quarantined, "orders": full.Orders, "payments": paymentChecks.Payments, "refunds": paymentChecks.Refunds, "amount_minor": full.AmountMinor, "refund_minor": paymentChecks.RefundMinor, "checked_at": time.Now().UTC()})
}

func identityReceiptExpectations(manifest ordermigration.Manifest, subjectCustomers map[string]int64) ([]identitymigration.SubjectReceiptExpectation, []identitymigration.QuarantineReceiptExpectation, error) {
	identityByKey := make(map[string]ordermigration.IdentityRow, len(manifest.Identities))
	for _, row := range manifest.Identities {
		identityByKey[row.SourceKey] = row
	}
	subjects := make([]identitymigration.SubjectReceiptExpectation, 0, len(manifest.Subjects))
	for _, row := range manifest.Subjects {
		customerID := subjectCustomers[row.SourceKey]
		if customerID < 1 {
			return nil, nil, ordermigration.ErrReconciliationMismatch
		}
		identityRows := make([]ordermigration.IdentityRow, 0, len(row.IdentityKeys))
		for _, key := range row.IdentityKeys {
			identity, ok := identityByKey[key]
			if !ok {
				return nil, nil, ordermigration.ErrReconciliationMismatch
			}
			identityRows = append(identityRows, identity)
		}
		raw, marshalErr := json.Marshal(struct {
			Subject ordermigration.SubjectRow    `json:"subject"`
			Rows    []ordermigration.IdentityRow `json:"identities"`
		}{Subject: row, Rows: identityRows})
		if marshalErr != nil {
			return nil, nil, marshalErr
		}
		subjects = append(subjects, identitymigration.SubjectReceiptExpectation{SourceKey: row.SourceKey, SourceDigest: sha256.Sum256(raw), CustomerID: customerID, IdentityCount: len(row.IdentityKeys)})
	}
	quarantines := make([]identitymigration.QuarantineReceiptExpectation, 0, len(manifest.IdentityQuarantines))
	for _, row := range manifest.IdentityQuarantines {
		raw, marshalErr := json.Marshal(row)
		if marshalErr != nil {
			return nil, nil, marshalErr
		}
		quarantines = append(quarantines, identitymigration.QuarantineReceiptExpectation{SourceKey: row.SourceKey, SourceDigest: sha256.Sum256(raw), ReasonCode: row.ReasonCode, EvidenceDigest: row.EvidenceDigest})
	}
	return subjects, quarantines, nil
}

func paymentHistoryFacts(manifest ordermigration.Manifest, orderIDs map[string]int64, subjectCustomers map[string]int64, identities map[string]historyIdentityResolution) ([]int64, []paymentmigration.HistoricalPaymentFact, []paymentmigration.HistoricalRefundFact, error) {
	allOrderIDs := make([]int64, 0, len(manifest.Orders))
	payments := make([]paymentmigration.HistoricalPaymentFact, 0, len(manifest.Orders))
	for _, row := range manifest.Orders {
		key := ordermigration.HistoricalMerchantKey(row.Provider, row.MerchantOrderNo)
		orderID := orderIDs[key]
		if orderID < 1 {
			return nil, nil, nil, ordermigration.ErrReconciliationMismatch
		}
		allOrderIDs = append(allOrderIDs, orderID)
		status, terminal := historicalPaymentStatus(row.Status)
		if !terminal || row.Provider == "alipay" {
			continue
		}
		identity := identities[row.PayerIdentityKey]
		beneficiary := subjectCustomers[row.BeneficiarySubjectKey]
		if (row.PayerIdentityKey != "" && (identity.CustomerID < 1 || identity.IdentityID < 1)) || (row.BeneficiarySubjectKey != "" && beneficiary < 1) {
			return nil, nil, nil, ordermigration.ErrReconciliationMismatch
		}
		payments = append(payments, paymentmigration.HistoricalPaymentFact{SourceStatus: row.SourceStatus, HistoryReason: row.HistoryReason, OrderID: orderID, Provider: paymentdomain.Provider(row.Provider), MerchantOrderNo: row.MerchantOrderNo, PayerIdentityID: identity.IdentityID, PayerCustomerID: identity.CustomerID, BeneficiaryCustomerID: beneficiary, AmountMinor: row.AmountMinor, Currency: row.Currency, Status: status, ProviderTransactionReference: row.ProviderTransactionNo, SourceDigest: ordermigration.HistoricalOrderDigest(row), PaidConfirmedAt: row.PaidConfirmedAt, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt})
	}
	refunds := make([]paymentmigration.HistoricalRefundFact, 0, len(manifest.Refunds))
	for _, row := range manifest.Refunds {
		orderID := orderIDs[ordermigration.HistoricalMerchantKey(row.Provider, row.MerchantOrderNo)]
		if orderID < 1 {
			return nil, nil, nil, ordermigration.ErrReconciliationMismatch
		}
		refunds = append(refunds, paymentmigration.HistoricalRefundFact{Status: paymentdomain.RefundStatus(row.HistoricalStatus()), OrderID: orderID, Provider: paymentdomain.Provider(row.Provider), MerchantOrderNo: row.MerchantOrderNo, RefundNo: row.RefundNo, Reason: row.Reason, AmountMinor: row.AmountMinor, ProviderRefundReference: row.ProviderRefundNo, SourceDigest: ordermigration.HistoricalRefundDigest(row), OccurredAt: row.OccurredAt})
	}
	return allOrderIDs, payments, refunds, nil
}

func historicalPaymentStatus(orderStatus string) (paymentdomain.Status, bool) {
	switch orderStatus {
	case "paid", "partially_refunded", "refunded":
		return paymentdomain.StatusPaid, true
	case "payment_failed":
		return paymentdomain.StatusFailed, true
	case "cancelled", "closed":
		return paymentdomain.StatusCancelled, true
	default:
		return "", false
	}
}

func printJSON(value any) error { return json.NewEncoder(os.Stdout).Encode(value) }
