package app

import (
	"context"
	"crypto/sha256"
	"fmt"
	"testing"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	referraldomain "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/domain"
	referralport "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/port"
)

// This synthetic Identity Port reads a real PG version marker in the caller's
// transaction. A second connection commits a merge after batch one. Actual
// trusted identity and FOR SHARE behavior remain covered by Composition's
// genuine OneID journey; this fixture isolates the multi-batch snapshot.
type inviteSnapshotRoots struct {
	h     *referralPostgreSQLHarness
	calls int
}

func (r *inviteSnapshotRoots) CanonicalCustomerRoots(ctx context.Context, ids []customerdomain.CustomerID) (map[customerdomain.CustomerID]customerdomain.CustomerID, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	var merged bool
	if err = tx.QueryRow(ctx, `SELECT merged FROM audit_test_identity_reporting`).Scan(&merged); err != nil {
		return nil, err
	}
	roots := map[customerdomain.CustomerID]customerdomain.CustomerID{}
	for _, id := range ids {
		root := id
		if id >= 1000 {
			root = 101
			if merged {
				root = 11
			}
		}
		roots[id] = root
	}
	r.calls++
	if r.calls == 1 {
		_, err = r.h.pool.Exec(context.Background(), `UPDATE audit_test_identity_reporting SET merged=true`)
	}
	return roots, err
}

func TestPostgreSQLMyInvitesCanonicalBatchesShareIdentitySnapshot(t *testing.T) {
	h := newReferralPostgreSQLHarness(t)
	defer h.cleanup()
	ctx := context.Background()
	c, err := h.admin.CreateCampaign(ctx, referralport.CreateCampaignCommand{ActorAdminID: 9001, Name: "invite snapshot", StartsAt: h.clock.Add(-time.Hour), EndsAt: h.clock.Add(time.Hour), TeamMode: referraldomain.TeamModeIndividual, QualificationMode: referraldomain.QualificationProductPurchase, ProductID: 51, ProductType: referraldomain.ProductTypeStandard, LeaderboardMetric: referraldomain.LeaderboardInvites, IdempotencyKey: "invite-snapshot-create"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.pool.Exec(ctx, `CREATE TABLE audit_test_identity_reporting(merged boolean NOT NULL); INSERT INTO audit_test_identity_reporting VALUES(false)`); err != nil {
		t.Fatal(err)
	}
	err = h.uow.Within(ctx, func(tx context.Context) error {
		if _, e := h.repository.InsertParticipationWithin(tx, referraldomain.Participation{CampaignID: c.ID, CustomerID: 11, State: referraldomain.ParticipationActive, JoinedAt: h.clock}); e != nil {
			return e
		}
		for i := 0; i < 605; i++ {
			_, e := h.repository.InsertProductSaleEventWithin(tx, referraldomain.ProductSaleEvent{CampaignID: c.ID, OrderID: 10000 + int64(i), OrderVersion: 2, ProductID: 51, ProductType: "standard_product", ProductCode: "p-51", ProductName: "frozen product", ProductVersion: 1, PromoterCustomerID: 11, PromotionCredentialRef: "synthetic-frozen", PolicyVersion: 1, BuyerCustomerID: 1000 + int64(i), BeneficiaryCustomerID: 1000 + int64(i), Kind: referraldomain.ProductSaleCredit, AmountDeltaMinor: 680, OrderCountDelta: 1, Currency: "CNY", SourcePaidEventID: 20000 + int64(i), SourceDigest: sha256.Sum256([]byte(fmt.Sprint(i))), OccurredAt: h.clock})
			if e != nil {
				return e
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	roots := &inviteSnapshotRoots{h: h}
	if err = h.repository.BindCanonicalCustomerRoots(roots); err != nil {
		t.Fatal(err)
	}
	wrapped, err := platformpostgres.Wrap(h.pool, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := platformpostgres.NewRepeatableReadUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	if err = h.service.BindIdentityReportingUnitOfWork(snapshot); err != nil {
		t.Fatal(err)
	}
	page, err := h.service.ListMyInvites(ctx, referralActor(11, h.clock), c.ID, "", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Participation.CustomerID != 101 || page.Items[0].ScoreDelta != 605*680 {
		t.Fatalf("mixed snapshot lost invitation facts: %+v err=%v", page, err)
	}
	if roots.calls < 2 {
		t.Fatal("Identity multi-batch boundary was not exercised")
	}
	next, err := h.service.ListMyInvites(ctx, referralActor(11, h.clock), c.ID, "", 20)
	if err != nil || len(next.Items) != 0 {
		t.Fatalf("next snapshot missed committed canonical self-merge: %+v err=%v", next, err)
	}
	assertCount(t, h.pool, `SELECT count(*) FROM referral_product_sale_events WHERE campaign_id=$1`, 605, c.ID)
}
