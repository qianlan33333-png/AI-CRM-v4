package app

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	referraldomain "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/domain"
	referralport "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/port"
	referralstore "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/store"
	"testing"
	"time"
)

func TestPostgreSQLInviteStreamCountsRetainsAndPagesConcurrentJoins(t *testing.T) {
	h := newReferralPostgreSQLHarness(t)
	defer h.cleanup()
	ctx := context.Background()
	c, team, _ := h.createCampaignWithTeams(t, "invite-stream", 11001, 12001)
	h.joinDirect(t, c.ID, team.ID, 11001, "stream-owner")
	actor := referralActor(11001, h.clock)
	empty, err := h.service.ListMyInvites(ctx, actor, c.ID, "", 20)
	if err != nil || empty.TotalCount != 0 || len(empty.Items) != 0 || empty.NextCursor != "" {
		t.Fatalf("empty=%+v err=%v", empty, err)
	}
	token := h.issue(t, c.ID, 11001, "stream-invite")
	joined := make([]referralport.MyCampaign, 0, 100)
	for i := 0; i < 100; i++ {
		joined = append(joined, h.joinInvite(t, c.ID, int64(20000+i), token, fmt.Sprintf("stream-join-%d", i)))
	}
	first, err := h.service.ListMyInvites(ctx, actor, c.ID, "", 20)
	if err != nil || first.TotalCount != 100 || len(first.Items) != 20 || first.NextCursor == "" {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	h.clock = h.clock.Add(time.Minute)
	h.joinInvite(t, c.ID, 30000, token, "stream-new-after-page")
	if err = h.admin.ReverseInvitation(ctx, referralport.ReverseInvitationCommand{ParticipationID: joined[0].Participation.ID, Reason: "synthetic retention", ActorAdminID: 9001, IdempotencyKey: "stream-reverse"}); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	revoked := false
	page := first
	for n := 0; n < 6; n++ {
		if page.TotalCount != 100 {
			t.Fatalf("snapshot count changed: %d", page.TotalCount)
		}
		for _, row := range page.Items {
			if row.EntryKey == "" || seen[row.EntryKey] || row.Participation.CustomerID == 30000 {
				t.Fatal("duplicate, missing key, or new arrival entered continuation")
			}
			seen[row.EntryKey] = true
			if row.Participation.CustomerID == 20000 && row.ScoreState == "reversed" {
				revoked = true
			}
		}
		if page.NextCursor == "" {
			break
		}
		page, err = h.service.ListMyInvites(ctx, actor, c.ID, page.NextCursor, 20)
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(seen) != 100 || !revoked {
		t.Fatalf("complete retained list=%d revoked=%t", len(seen), revoked)
	}
	refresh, err := h.service.ListMyInvites(ctx, actor, c.ID, "", 20)
	if err != nil || refresh.TotalCount != 101 {
		t.Fatalf("refresh count=%d err=%v", refresh.TotalCount, err)
	}
	if _, err = h.service.ListMyInvites(ctx, referralActor(12001, h.clock), c.ID, first.NextCursor, 20); !errors.Is(err, referralport.ErrConflict) {
		t.Fatalf("foreign cursor accepted: %v", err)
	}
}

func TestPostgreSQLInviteStreamPaidCountsReversalsAndCanonicalAliases(t *testing.T) {
	h := newReferralPostgreSQLHarness(t)
	defer h.cleanup()
	ctx := context.Background()
	c, err := h.admin.CreateCampaign(ctx, referralport.CreateCampaignCommand{ActorAdminID: 9001, Name: "paid stream", StartsAt: h.clock.Add(-time.Hour), EndsAt: h.clock.Add(time.Hour), TeamMode: referraldomain.TeamModeIndividual, QualificationMode: referraldomain.QualificationProductPurchase, ProductID: 51, ProductType: referraldomain.ProductTypeStandard, IdempotencyKey: "paid-stream-create"})
	if err != nil {
		t.Fatal(err)
	}
	if err = h.repository.BindCanonicalCustomerRoots(&reportingRootsFixture{}); err != nil {
		t.Fatal(err)
	}
	var first referralstore.InviteStreamPage
	err = h.uow.Within(ctx, func(tx context.Context) error {
		for i, buyer := range []int64{1000, 1001, 1002, 99999} {
			owner := int64(11)
			if i%2 == 1 {
				owner = 12
			}
			event := referraldomain.ProductSaleEvent{CampaignID: c.ID, OrderID: int64(60000 + i), OrderVersion: 2, ProductID: 51, ProductType: "standard_product", ProductCode: "p-51", ProductName: "fixture", ProductVersion: 1, PromoterCustomerID: owner, PromotionCredentialRef: "synthetic", PolicyVersion: 1, BuyerCustomerID: buyer, BeneficiaryCustomerID: buyer, Kind: referraldomain.ProductSaleCredit, AmountDeltaMinor: 680, OrderCountDelta: 1, Currency: "CNY", SourcePaidEventID: int64(70000 + i), SourceDigest: sha256.Sum256([]byte(fmt.Sprint(i))), OccurredAt: h.clock}
			credit, e := h.repository.InsertProductSaleEventWithin(tx, event)
			if e != nil {
				return e
			}
			if buyer == 1002 {
				originalID := credit.ID
				credit.ID = 0
				credit.Kind = referraldomain.ProductSaleReversal
				credit.ReversesSaleEventID = originalID
				credit.OrderVersion = 3
				credit.RefundReceiptKey = "paid-stream-refund"
				credit.AmountDeltaMinor = -680
				credit.OrderCountDelta = -1
				_, e = h.repository.InsertProductSaleEventWithin(tx, credit)
				if e != nil {
					return e
				}
			}
		}
		var e error
		first, e = h.repository.ReadInviteStreamWithin(tx, referralstore.InviteStreamQuery{CampaignID: c.ID, InviterID: 11, Paid: true, Limit: 101})
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.TotalCount != 2 || len(first.Items) != 2 {
		t.Fatalf("canonical distinct list=%+v", first)
	}
	reversed := false
	for _, row := range first.Items {
		if row.Participation.CustomerID == 100001 && row.ScoreState == "reversed" {
			reversed = true
		}
		if row.Participation.CustomerID == 11 {
			t.Fatal("canonical self purchase included")
		}
	}
	if !reversed {
		t.Fatal("refunded buyer removed from total/list")
	}
}

func TestPostgreSQLInviteStreamFreeCountsCanonicalAliases(t *testing.T) {
	h := newReferralPostgreSQLHarness(t)
	defer h.cleanup()
	ctx := context.Background()
	c, team, _ := h.createCampaignWithTeams(t, "free-canonical-stream", 11, 13)
	h.joinDirect(t, c.ID, team.ID, 11, "free-stream-owner")
	h.joinDirect(t, c.ID, team.ID, 12, "free-stream-owner-alias")
	token := h.issue(t, c.ID, 11, "free-stream-invite")
	alias := h.issue(t, c.ID, 12, "free-stream-alias-invite")
	h.joinInvite(t, c.ID, 1000, token, "free-stream-buyer")
	h.joinInvite(t, c.ID, 1001, alias, "free-stream-buyer-alias")
	h.joinInvite(t, c.ID, 99999, token, "free-stream-self-alias")
	if err := h.repository.BindCanonicalCustomerRoots(&reportingRootsFixture{}); err != nil {
		t.Fatal(err)
	}
	page, err := h.service.ListMyInvites(ctx, referralActor(11, h.clock), c.ID, "", 20)
	if err != nil || page.TotalCount != 1 || len(page.Items) != 1 || page.Items[0].Participation.CustomerID != 100000 {
		t.Fatalf("canonical free list=%+v err=%v", page, err)
	}
}
