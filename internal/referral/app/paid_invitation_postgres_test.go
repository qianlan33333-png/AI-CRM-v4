package app

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	orderdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	referraldomain "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/domain"
	referralport "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/port"
)

func TestPostgreSQLPaidInvitationWithoutDistributorFreezesAndScoresAtomically(t *testing.T) {
	h := newReferralPostgreSQLHarness(t)
	defer h.cleanup()
	ctx := context.Background()
	c, err := h.admin.CreateCampaign(ctx, referralport.CreateCampaignCommand{ActorAdminID: 9001, Name: "paid invitation", StartsAt: h.clock.Add(-time.Hour), EndsAt: h.clock.Add(24 * time.Hour), TeamMode: referraldomain.TeamModeTeam, QualificationMode: referraldomain.QualificationProductPurchase, ProductID: 51, ProductType: referraldomain.ProductTypeStandard, LeaderboardMetric: referraldomain.LeaderboardInvites, IdempotencyKey: "paid-invite-create"})
	if err != nil {
		t.Fatal(err)
	}
	c, err = h.admin.SetCampaignState(ctx, referralport.SetCampaignStateCommand{ActorAdminID: 9001, CampaignID: c.ID, ExpectedVersion: c.Version, Target: referraldomain.CampaignActive, IdempotencyKey: "paid-invite-active"})
	if err != nil {
		t.Fatal(err)
	}
	team, err := h.admin.CreateTeam(ctx, referralport.CreateTeamCommand{ActorAdminID: 9001, CampaignID: c.ID, CaptainCustomerID: 11, Name: "paid team", IdempotencyKey: "paid-invite-team"})
	if err != nil {
		t.Fatal(err)
	}
	if err = h.uow.Within(ctx, func(tx context.Context) error {
		_, e := h.repository.InsertParticipationWithin(tx, referraldomain.Participation{CampaignID: c.ID, CustomerID: 11, TeamID: team.ID, State: referraldomain.ParticipationActive, JoinedAt: h.clock.Add(-time.Minute)})
		return e
	}); err != nil {
		t.Fatal(err)
	}
	// No Distribution store, distributor registration, receiver or Provider exists
	// in this harness. Active paid participation is the invitation authority.
	token := h.issue(t, c.ID, 11, "paid-invite-token")
	if err = h.service.SetSaleEvidenceReaders(saleAttributionStub{}, platformaudit.NewPostgreSQLStore()); err != nil {
		t.Fatal(err)
	}
	command := orderport.ProductSaleCheckoutContextCommand{OrderID: 8101, OrderVersion: 1, ProductID: 51, ProductType: "standard_product", ProductCode: "p-51", ProductName: "Product 51", ProductVersion: 1, BuyerCustomerID: 22, BeneficiaryCustomerID: 22, ReferralActivityContext: token, OccurredAt: h.clock}
	rollback := errors.New("owner rollback")
	freeze := func(tx context.Context) error {
		return h.service.RecordProductSaleCheckoutAttributionWithin(tx, command)
	}
	if err = h.uow.Within(ctx, func(tx context.Context) error {
		if e := freeze(tx); e != nil {
			return e
		}
		return rollback
	}); !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	assertCount(t, h.pool, `SELECT count(*) FROM referral_product_sale_checkouts`, 0)
	for range 2 {
		if err = h.uow.Within(ctx, freeze); err != nil {
			t.Fatal(err)
		}
	}
	assertCount(t, h.pool, `SELECT count(*) FROM referral_product_sale_checkouts WHERE order_id=8101 AND promoter_customer_id=11 AND policy_version IS NULL AND commission_rate_basis_points=0`, 1)
	event := salePaidEvent(8101, 51, 22, h.clock.Add(time.Minute))
	event.ReferralActivityContextDigest = sha256.Sum256([]byte(token))
	pay := func(tx context.Context) error { return h.service.ConsumePaidEventWithin(tx, event) }
	if err = h.uow.Within(ctx, func(tx context.Context) error {
		if e := pay(tx); e != nil {
			return e
		}
		return rollback
	}); !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	assertCount(t, h.pool, `SELECT count(*) FROM referral_product_sale_events`, 0)
	assertCount(t, h.pool, `SELECT count(*) FROM referral_participations WHERE customer_id=22`, 0)
	for range 2 {
		if err = h.uow.Within(ctx, pay); err != nil {
			t.Fatal(err)
		}
	}
	assertCount(t, h.pool, `SELECT count(*) FROM referral_product_sale_events WHERE order_id=8101 AND promoter_customer_id=11 AND team_id=$1`, 1, team.ID)
	assertCount(t, h.pool, `SELECT count(*) FROM referral_participations WHERE customer_id=22 AND state='active'`, 1)
	// The referred buyer can immediately issue their own invitation, still
	// without any financial registration or receiver preparation.
	h.issue(t, c.ID, 22, "paid-invite-buyer-share")
	tampered := event
	tampered.Order.PayerCustomerID = new(int64)
	*tampered.Order.PayerCustomerID = 23
	if err = h.uow.Within(ctx, func(tx context.Context) error { return h.service.ConsumePaidEventWithin(tx, tampered) }); !errors.Is(err, referralport.ErrConflict) {
		t.Fatalf("payer substitution: %v", err)
	}
	tampered = event
	tampered.Order.Items = append([]orderdomain.ItemSnapshot(nil), event.Order.Items...)
	tampered.Order.Items[0].ProductName = "substituted"
	if err = h.uow.Within(ctx, func(tx context.Context) error { return h.service.ConsumePaidEventWithin(tx, tampered) }); !errors.Is(err, referralport.ErrConflict) {
		t.Fatalf("item substitution: %v", err)
	}
	score := func(want int64) {
		t.Helper()
		board, e := h.service.Leaderboard(ctx, referralport.LeaderboardQuery{CampaignID: c.ID, Kind: referralport.LeaderboardPersonal, Period: referralport.LeaderboardTotal, Limit: 20})
		if e != nil {
			t.Fatal(e)
		}
		if want == 0 {
			if len(board.Items) != 0 {
				t.Fatalf("expected empty: %+v", board.Items)
			}
			return
		}
		if len(board.Items) != 1 || board.Items[0].CustomerID != 11 || board.Items[0].Score != want {
			t.Fatalf("board=%+v want=%d", board.Items, want)
		}
	}
	score(1)
	command.OrderID = 8102
	if err = h.uow.Within(ctx, freeze); err != nil {
		t.Fatal(err)
	}
	second := salePaidEvent(8102, 51, 22, event.OccurredAt.Add(time.Minute))
	second.ReferralActivityContextDigest = event.ReferralActivityContextDigest
	if err = h.uow.Within(ctx, func(tx context.Context) error { return h.service.ConsumePaidEventWithin(tx, second) }); err != nil {
		t.Fatal(err)
	}
	score(1)
	for _, paid := range []orderport.PaidEvent{event, second} {
		refund := orderport.RefundSettlementEvent{Order: paid.Order, CheckoutProductID: 51, CheckoutProductType: "standard_product", RefundedDelta: 990, OccurredAt: paid.OccurredAt.Add(time.Hour), ReceiptKey: "paid-invite-refund-" + itoa(paid.OrderID)}
		refund.Order.Status, refund.Order.RefundedMinor, refund.Order.Version = orderdomain.StatusRefunded, 990, 3
		for range 2 {
			if err = h.uow.Within(ctx, func(tx context.Context) error { return h.service.ConsumeRefundSettlementWithin(tx, refund) }); err != nil {
				t.Fatal(err)
			}
		}
		if paid.OrderID == 8101 {
			score(1)
		} else {
			score(0)
		}
	}
	// Invalid or mismatched invitation context remains an ordinary checkout.
	for _, invalid := range []struct {
		token   string
		product int64
		at      time.Time
	}{{"rfi_invalid", 51, h.clock}, {token, 52, h.clock}, {token, 51, c.EndsAt}} {
		command.OrderID++
		command.ReferralActivityContext, command.ProductID, command.OccurredAt = invalid.token, invalid.product, invalid.at
		if err = h.uow.Within(ctx, freeze); err != nil {
			t.Fatal(err)
		}
		assertCount(t, h.pool, `SELECT count(*) FROM referral_product_sale_checkouts WHERE order_id=$1`, 0, command.OrderID)
	}
	command.OrderID = 8110
	command.ReferralActivityContext, command.ProductID, command.OccurredAt = token, 51, h.clock
	command.BuyerCustomerID, command.BeneficiaryCustomerID = 11, 11
	if err = h.uow.Within(ctx, freeze); err != nil {
		t.Fatal(err)
	}
	self := salePaidEvent(8110, 51, 11, h.clock.Add(time.Minute))
	self.ReferralActivityContextDigest = event.ReferralActivityContextDigest
	if err = h.uow.Within(ctx, func(tx context.Context) error { return h.service.ConsumePaidEventWithin(tx, self) }); err != nil {
		t.Fatal(err)
	}
	assertCount(t, h.pool, `SELECT count(*) FROM referral_product_sale_events WHERE order_id=8110`, 0)
	command.OrderID = 8111
	command.BuyerCustomerID, command.BeneficiaryCustomerID = 33, 33
	if err = h.uow.Within(ctx, freeze); err != nil {
		t.Fatal(err)
	}
	h.clock = h.clock.Add(10 * time.Minute)
	if _, err = h.admin.SetCampaignState(ctx, referralport.SetCampaignStateCommand{ActorAdminID: 9001, CampaignID: c.ID, ExpectedVersion: c.Version, Target: referraldomain.CampaignDisabled, IdempotencyKey: "paid-invite-disable"}); err != nil {
		t.Fatal(err)
	}
	late := salePaidEvent(8111, 51, 33, h.clock.Add(time.Minute))
	late.ReferralActivityContextDigest = event.ReferralActivityContextDigest
	if err = h.uow.Within(ctx, func(tx context.Context) error { return h.service.ConsumePaidEventWithin(tx, late) }); err != nil {
		t.Fatal(err)
	}
	assertCount(t, h.pool, `SELECT count(*) FROM referral_product_sale_events WHERE order_id=8111`, 0)
	assertCount(t, h.pool, `SELECT count(*) FROM referral_participations WHERE customer_id=33`, 0)
}
