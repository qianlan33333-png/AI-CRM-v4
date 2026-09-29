package main

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identitystore "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/store"
	orderapp "github.com/qianlan33333-png/AI-CRM-v3/internal/order/app"
	orderdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	orderstore "github.com/qianlan33333-png/AI-CRM-v3/internal/order/store"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// The loser-origin order is created while the loser is still active; the
// survivor-origin order is created after merge. Reversal restores the
// snapshotted loser identity without rewriting either order's owner. Native
// pending orders do not invoke a payment provider or produce an external
// effect.
func TestPostgreSQLIdentityMergeReversePreservesOrderOriginsAndRestoredIdentity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()

	native, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer native.Close()
	wrapped, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	identities := identityapp.OneIDService{Store: identitystore.NewPostgresStore()}
	orderRepository, err := orderstore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	orders := orderapp.NewService(uow, orderRepository)
	createPendingOrder := func(customerID int64, origin string) orderdomain.Snapshot {
		t.Helper()
		command := orderport.PaymentOrderCommand{
			Provider: orderdomain.ProviderWeChatPay, MerchantOrderNo: "M-ONEID-MERGE-" + origin,
			PayerCustomerID: customerID, BeneficiaryCustomerID: customerID,
			ProductID: 991, ProductCode: "oneid-merge-order", ProductName: "Synthetic order history", ProductVersion: 1,
			ProductType: "standard_product", UnitAmountMinor: 1000, Currency: "CNY",
			ActorScope: "oneid-merge-order-history-" + origin, IdempotencyKey: "oneid-merge-order-history-" + origin + "-001",
		}
		var order orderdomain.Snapshot
		if createErr := uow.Within(ctx, func(tx context.Context) error {
			var innerErr error
			order, innerErr = orders.CreatePaymentOrderWithin(tx, command)
			return innerErr
		}); createErr != nil {
			t.Fatalf("create %s pending order: %v", origin, createErr)
		}
		return order
	}

	survivorFact := oneIDMergeOrderFact(t, identitydomain.KindWeComExternalUserID, "wecom-corp:merge-order", "survivor")
	loserFact := oneIDMergeOrderFact(t, identitydomain.KindAlipayOAuthUserID, "alipay-app:merge-order", "loser")
	var survivor, loser struct {
		CustomerID int64
		IdentityID int64
	}
	if err = uow.Within(ctx, func(tx context.Context) error {
		created, provisionErr := identities.ProvisionCustomerFromVerifiedIdentity(tx, survivorFact)
		if provisionErr != nil {
			return provisionErr
		}
		survivor.CustomerID, survivor.IdentityID = int64(created.CustomerID), created.IdentityID
		created, provisionErr = identities.ProvisionCustomerFromVerifiedIdentity(tx, loserFact)
		if provisionErr != nil {
			return provisionErr
		}
		loser.CustomerID, loser.IdentityID = int64(created.CustomerID), created.IdentityID
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	loserOriginOrder := createPendingOrder(loser.CustomerID, "loser-origin")
	assertOneIDMergeOrderOwner(t, ctx, native, loserOriginOrder.ID, loser.CustomerID, "before merge")

	evidence := identitydomain.LinkEvidence{
		Type: "test", Strength: identitydomain.EvidenceStrong, Source: "oneid.merge.order.test",
		EventID: "order-history", Digest: "synthetic-order-history", PolicyVersion: "test-v1",
	}
	var candidate identityapp.LinkResult
	if err = uow.Within(ctx, func(tx context.Context) error {
		var linkErr error
		candidate, linkErr = identities.LinkVerifiedIdentity(tx, identityapp.LinkCommand{
			SourceCustomerID: customerdomain.CustomerID(loser.CustomerID), Target: survivorFact, Evidence: evidence,
		})
		return linkErr
	}); err != nil {
		t.Fatal(err)
	}
	if candidate.Candidate == nil {
		t.Fatalf("link did not create a merge candidate: %+v", candidate)
	}
	var merged identityapp.LinkResult
	if err = uow.Within(ctx, func(tx context.Context) error {
		var confirmErr error
		merged, confirmErr = identities.ConfirmMerge(tx, identityapp.ConfirmMergeCommand{
			CandidateID: candidate.Candidate.ID, SurvivorCustomerID: customerdomain.CustomerID(survivor.CustomerID), Operator: "oneid-order-history-test",
		})
		return confirmErr
	}); err != nil {
		t.Fatal(err)
	}
	if merged.Merge == nil || int64(merged.Merge.FromCustomerID) != loser.CustomerID || int64(merged.Merge.ToCustomerID) != survivor.CustomerID {
		t.Fatalf("unexpected merge direction: %+v", merged.Merge)
	}
	assertOneIDMergeOrderOwner(t, ctx, native, loserOriginOrder.ID, loser.CustomerID, "after merge")

	survivorOriginOrder := createPendingOrder(survivor.CustomerID, "survivor-origin")
	assertOneIDMergeOrderOwner(t, ctx, native, survivorOriginOrder.ID, survivor.CustomerID, "after merge")

	reverseErr := uow.Within(ctx, func(tx context.Context) error {
		_, revertErr := identities.RevertConfirmedMerge(tx, merged.Merge.ID)
		return revertErr
	})
	var reversibleStatus, loserStatus, survivorStatus string
	var reversedAtSet bool
	var loserMergedTo, identityOwner int64
	var loserVersion, loserLineage, survivorVersion, survivorLineage int64
	var orderCount int
	if err = native.QueryRow(ctx, `SELECT reversible_status,reversed_at IS NOT NULL FROM customer_merges WHERE id=$1`, merged.Merge.ID).Scan(&reversibleStatus, &reversedAtSet); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT status,COALESCE(merged_into_customer_id,0),version,lineage_version FROM customers WHERE id=$1`, loser.CustomerID).Scan(&loserStatus, &loserMergedTo, &loserVersion, &loserLineage); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT status,version,lineage_version FROM customers WHERE id=$1`, survivor.CustomerID).Scan(&survivorStatus, &survivorVersion, &survivorLineage); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT customer_id FROM customer_identities WHERE id=$1`, loser.IdentityID).Scan(&identityOwner); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM orders WHERE id=ANY($1::bigint[])`, []int64{loserOriginOrder.ID, survivorOriginOrder.ID}).Scan(&orderCount); err != nil {
		t.Fatal(err)
	}
	assertOneIDMergeOrderOwner(t, ctx, native, loserOriginOrder.ID, loser.CustomerID, "after reversal")
	assertOneIDMergeOrderOwner(t, ctx, native, survivorOriginOrder.ID, survivor.CustomerID, "after reversal")
	var restoredMembers int
	var restoredMemberMatches bool
	if err = native.QueryRow(ctx, `SELECT count(*),bool_and(m.restored_at IS NOT NULL AND m.identity_version_after_restore=m.identity_version_after+1 AND i.customer_id=m.from_customer_id AND i.version=m.identity_version_after_restore AND i.status='active') FROM customer_merge_identity_members m JOIN customer_identities i ON i.id=m.identity_id WHERE m.merge_id=$1 AND m.identity_id=$2`, merged.Merge.ID, loser.IdentityID).Scan(&restoredMembers, &restoredMemberMatches); err != nil {
		t.Fatal(err)
	}
	var mergeCount int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM customer_merges WHERE candidate_id=$1`, candidate.Candidate.ID).Scan(&mergeCount); err != nil {
		t.Fatal(err)
	}
	t.Logf("order-origin reverse readback: customers=%d/%d merge=%s reversed_at=%t loser=%s->%d v=%d/%d survivor=%s v=%d/%d restored_identity_owner=%d restored_members=%d match=%t loser_order_id=%d loser_owner=%d survivor_order_id=%d survivor_owner=%d order_rows=%d", survivor.CustomerID, loser.CustomerID, reversibleStatus, reversedAtSet, loserStatus, loserMergedTo, loserVersion, loserLineage, survivorStatus, survivorVersion, survivorLineage, identityOwner, restoredMembers, restoredMemberMatches, loserOriginOrder.ID, loser.CustomerID, survivorOriginOrder.ID, survivor.CustomerID, orderCount)
	if reverseErr != nil {
		t.Fatalf("reverse after both order origins failed: %v", reverseErr)
	}
	if reversibleStatus != "reversed" || !reversedAtSet || loserStatus != "active" || loserMergedTo != 0 || survivorStatus != "active" || identityOwner != loser.CustomerID || orderCount != 2 || restoredMembers != 1 || !restoredMemberMatches || mergeCount != 1 {
		t.Fatalf("reverse failed to preserve independently owned references: merge=%s reversed_at=%t loser=%s->%d survivor=%s identity_owner=%d restored_members=%d restored_match=%t merge_count=%d order_rows=%d", reversibleStatus, reversedAtSet, loserStatus, loserMergedTo, survivorStatus, identityOwner, restoredMembers, restoredMemberMatches, mergeCount, orderCount)
	}
	if loserVersion != merged.Merge.FromVersionAfter+1 || loserLineage != merged.Merge.FromLineageAfter+1 || survivorVersion != merged.Merge.ToVersionAfter+1 || survivorLineage != merged.Merge.ToLineageAfter+1 {
		t.Fatalf("reversal lineage/version steps differ from ledger: loser=%d/%d merge_after=%d/%d survivor=%d/%d merge_after=%d/%d", loserVersion, loserLineage, merged.Merge.FromVersionAfter, merged.Merge.FromLineageAfter, survivorVersion, survivorLineage, merged.Merge.ToVersionAfter, merged.Merge.ToLineageAfter)
	}
}

func assertOneIDMergeOrderOwner(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orderID, customerID int64, stage string) {
	t.Helper()
	var payer, beneficiary int64
	var status, recordOrigin string
	var effectEligible bool
	err := pool.QueryRow(ctx, `SELECT payer_customer_id,beneficiary_customer_id,status,record_origin,effect_eligible FROM orders WHERE id=$1`, orderID).Scan(&payer, &beneficiary, &status, &recordOrigin, &effectEligible)
	if err != nil {
		t.Fatalf("read %s order %d: %v", stage, orderID, err)
	}
	t.Logf("%s order readback: id=%d payer=%d beneficiary=%d status=%s origin=%s effect_eligible=%t", stage, orderID, payer, beneficiary, status, recordOrigin, effectEligible)
	if payer != customerID || beneficiary != customerID || status != "pending_payment" || recordOrigin != "native" || !effectEligible {
		t.Fatalf("%s order %d owner/status changed: payer=%d beneficiary=%d status=%s origin=%s effect_eligible=%t want_owner=%d", stage, orderID, payer, beneficiary, status, recordOrigin, effectEligible, customerID)
	}
}

func oneIDMergeOrderFact(t *testing.T, kind identitydomain.Kind, scope, value string) identitydomain.VerifiedFact {
	t.Helper()
	fact, err := identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{
		Kind: kind, Scope: scope, Value: value, Source: "oneid.merge.order.test",
	})
	if err != nil {
		t.Fatal(err)
	}
	return fact
}
