package app

import (
	"context"
	"testing"
	"time"

	distributiondomain "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/domain"
	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
	orderdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	referraldomain "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/domain"
	referralport "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/port"
)

type saleAttributionStub struct {
	values map[int64]distributionport.FrozenOrderAttribution
}

func (s saleAttributionStub) ReadFrozenOrderAttributionWithin(_ context.Context, orderID int64, _ int32) (distributionport.FrozenOrderAttribution, error) {
	value, ok := s.values[orderID]
	if !ok {
		return distributionport.FrozenOrderAttribution{}, distributionport.ErrNotFound
	}
	return value, nil
}

func salePaidEvent(id, productID, buyerID int64, at time.Time) orderport.PaidEvent {
	productVersion, promoter := int64(1), buyerID
	source := orderport.NewPaidEventSourceDigest(id, 2)
	return orderport.PaidEvent{ID: id + 1000, OrderID: id, OrderVersion: 2, DomainEventOutboxID: id + 2000,
		CheckoutProductID: productID, CheckoutProductType: "standard_product", CheckoutGrossAmountMinor: 990,
		OccurredAt: at, SourceDigest: source,
		Order: orderdomain.Snapshot{ID: id, Provider: orderdomain.ProviderWeChatPay, Amount: orderdomain.Money{AmountMinor: 990, Currency: "CNY"},
			PayerCustomerID: &buyerID, BeneficiaryCustomerID: &promoter, Status: orderdomain.StatusPaid,
			RecordOrigin: orderdomain.RecordOriginNative, EffectEligible: true, Version: 2, UpdatedAt: at,
			Items: []orderdomain.ItemSnapshot{{LineNo: 1, ProductID: &productID, ProductVersion: &productVersion, ProductCode: "p-51", ProductName: "Product 51", UnitAmountMinor: 990, Quantity: 1, LineAmountMinor: 990}}},
	}
}

func TestPostgreSQLDistributionSaleScoresWithoutSignupAndRefundsOriginalDay(t *testing.T) {
	h := newReferralPostgreSQLHarness(t)
	defer h.cleanup()
	ctx := context.Background()
	c, err := h.admin.CreateCampaign(ctx, referralport.CreateCampaignCommand{ActorAdminID: 9001, Name: "测试裂变", StartsAt: h.clock.Add(-time.Hour), EndsAt: h.clock.Add(24 * time.Hour), TeamMode: referraldomain.TeamModeIndividual, QualificationMode: referraldomain.QualificationProductPurchase, ProductID: 51, ProductType: referraldomain.ProductTypeStandard, LeaderboardMetric: referraldomain.LeaderboardSalesAmount, IdempotencyKey: "distribution-sale-create"})
	if err != nil {
		t.Fatal(err)
	}
	c, err = h.admin.SetCampaignState(ctx, referralport.SetCampaignStateCommand{ActorAdminID: 9001, CampaignID: c.ID, ExpectedVersion: c.Version, Target: referraldomain.CampaignActive, IdempotencyKey: "distribution-sale-activate"})
	if err != nil {
		t.Fatal(err)
	}
	paidAt := h.clock.Add(time.Minute)
	stub := saleAttributionStub{values: map[int64]distributionport.FrozenOrderAttribution{
		943: {OrderID: 943, OrderItemLine: 1, ProductID: 51, ProductType: distributiondomain.ProductTypeStandard, PromoterCustomerID: 3541, CredentialID: 17, PolicyVersion: 1, CommissionRateBasisPoints: 1000, AttributedAt: h.clock.Add(-time.Minute)},
		944: {OrderID: 944, OrderItemLine: 1, ProductID: 51, ProductType: distributiondomain.ProductTypeStandard, PromoterCustomerID: 3541, CredentialID: 17, PolicyVersion: 1, CommissionRateBasisPoints: 1000, AttributedAt: h.clock.Add(-time.Minute)},
		945: {OrderID: 945, OrderItemLine: 1, ProductID: 52, ProductType: distributiondomain.ProductTypeStandard, PromoterCustomerID: 3541, CredentialID: 17, PolicyVersion: 1, CommissionRateBasisPoints: 1000, AttributedAt: h.clock.Add(-time.Minute)},
		946: {OrderID: 946, OrderItemLine: 1, ProductID: 51, ProductType: distributiondomain.ProductTypeStandard, PromoterCustomerID: 3541, CredentialID: 17, PolicyVersion: 1, CommissionRateBasisPoints: 1000, AttributedAt: h.clock.Add(-time.Minute)},
		947: {OrderID: 947, OrderItemLine: 1, ProductID: 51, ProductType: distributiondomain.ProductTypeStandard, PromoterCustomerID: 3541, CredentialID: 17, PolicyVersion: 1, CommissionRateBasisPoints: 1000, AttributedAt: h.clock.Add(-3 * time.Hour)},
		948: {OrderID: 948, OrderItemLine: 1, ProductID: 51, ProductType: distributiondomain.ProductTypeStandard, PromoterCustomerID: 3541, CredentialID: 17, PolicyVersion: 1, CommissionRateBasisPoints: 1000, AttributedAt: h.clock.Add(-time.Minute)},
	}}
	if err := h.service.SetSaleEvidenceReaders(stub, platformaudit.NewPostgreSQLStore()); err != nil {
		t.Fatal(err)
	}
	event := salePaidEvent(943, 51, 12484, paidAt)
	for i := 0; i < 2; i++ {
		if err := h.uow.Within(ctx, func(tx context.Context) error { return h.service.ConsumePaidEventWithin(tx, event) }); err != nil {
			t.Fatal(err)
		}
	}
	assertCount(t, h.pool, `SELECT count(*) FROM referral_product_sale_events WHERE order_id=943 AND kind='credit'`, 1)
	paidInviteCount := func() int64 {
		var count int64
		if err := h.uow.Within(ctx, func(tx context.Context) error {
			var readErr error
			count, readErr = h.repository.CountPaidInviteesWithin(tx, c.ID, 3541)
			return readErr
		}); err != nil {
			t.Fatal(err)
		}
		return count
	}
	if got := paidInviteCount(); got != 1 {
		t.Fatalf("paid invite count=%d want=1", got)
	}
	assertCount(t, h.pool, `SELECT count(*) FROM referral_participations WHERE campaign_id=$1`, 0, c.ID)
	board, err := h.service.Leaderboard(ctx, referralport.LeaderboardQuery{CampaignID: c.ID, Kind: referralport.LeaderboardPersonal, Period: referralport.LeaderboardTotal, ViewerCustomerID: 3541, Limit: 20})
	if err != nil || len(board.Items) != 1 || board.Items[0].CustomerID != 3541 || board.Items[0].Score != 990 || board.Items[0].SalesAmountMinor != 990 || board.Items[0].SalesOrderCount != 1 {
		t.Fatalf("first sale board=%+v err=%v", board, err)
	}
	partial := orderport.RefundSettlementEvent{Order: event.Order, CheckoutProductID: 51, CheckoutProductType: "standard_product", RefundedDelta: 300, OccurredAt: paidAt.Add(25 * time.Hour), ReceiptKey: "refund-943-1"}
	partial.Order.Status, partial.Order.RefundedMinor, partial.Order.Version = orderdomain.StatusPartiallyRefunded, 300, 3
	if err := h.uow.Within(ctx, func(tx context.Context) error { return h.service.ConsumeRefundSettlementWithin(tx, partial) }); err != nil {
		t.Fatal(err)
	}
	day, err := h.service.Leaderboard(ctx, referralport.LeaderboardQuery{CampaignID: c.ID, Kind: referralport.LeaderboardPersonal, Period: referralport.LeaderboardDay, Anchor: paidAt, Limit: 20})
	if err != nil || len(day.Items) != 1 || day.Items[0].Score != 690 || day.Items[0].SalesOrderCount != 1 {
		t.Fatalf("partial refund original-day board=%+v err=%v", day, err)
	}
	if got := paidInviteCount(); got != 1 {
		t.Fatalf("partial refund invite count=%d want=1", got)
	}
	full := partial
	full.RefundedDelta, full.ReceiptKey, full.OccurredAt = 690, "refund-943-2", paidAt.Add(26*time.Hour)
	full.Order.Status, full.Order.RefundedMinor, full.Order.Version = orderdomain.StatusRefunded, 990, 4
	for i := 0; i < 2; i++ {
		if err := h.uow.Within(ctx, func(tx context.Context) error { return h.service.ConsumeRefundSettlementWithin(tx, full) }); err != nil {
			t.Fatal(err)
		}
	}
	board, err = h.service.Leaderboard(ctx, referralport.LeaderboardQuery{CampaignID: c.ID, Kind: referralport.LeaderboardPersonal, Period: referralport.LeaderboardTotal, Limit: 20})
	if err != nil || len(board.Items) != 0 {
		t.Fatalf("fully refunded board=%+v err=%v", board, err)
	}
	if got := paidInviteCount(); got != 0 {
		t.Fatalf("full refund invite count=%d want=0", got)
	}
	if err := h.uow.Within(ctx, func(tx context.Context) error {
		items, readErr := h.repository.ListPaidInviteItemsWithin(tx, c.ID, 3541, 0, 10)
		if readErr != nil {
			return readErr
		}
		if len(items) != 1 || items[0].Participation.CustomerID != 12484 || items[0].ScoreState != "reversed" || items[0].ScoreDelta != 0 {
			t.Fatalf("refunded invitation details=%+v", items)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	assertCount(t, h.pool, `SELECT count(*) FROM referral_product_sale_events WHERE order_id=943 AND kind='reversal'`, 2)
	for _, item := range []struct{ orderID, buyerID, expected int64 }{{949, 12484, 1}, {950, 12485, 2}} {
		stub.values[item.orderID] = distributionport.FrozenOrderAttribution{OrderID: item.orderID, OrderItemLine: 1, ProductID: 51, ProductType: distributiondomain.ProductTypeStandard, PromoterCustomerID: 3541, CredentialID: 17, PolicyVersion: 1, CommissionRateBasisPoints: 1000, AttributedAt: h.clock.Add(-time.Minute)}
		if err := h.uow.Within(ctx, func(tx context.Context) error {
			return h.service.ConsumePaidEventWithin(tx, salePaidEvent(item.orderID, 51, item.buyerID, paidAt.Add(2*time.Minute)))
		}); err != nil {
			t.Fatal(err)
		}
		if got := paidInviteCount(); got != item.expected {
			t.Fatalf("order=%d unique paid buyers=%d want=%d", item.orderID, got, item.expected)
		}
	}
	// A disabled campaign still admits a replay of a payment made in its
	// audited active interval, while a later payment earns no sale.
	h.clock = paidAt.Add(time.Hour)
	if _, err := h.admin.SetCampaignState(ctx, referralport.SetCampaignStateCommand{ActorAdminID: 9001, CampaignID: c.ID, ExpectedVersion: c.Version, Target: referraldomain.CampaignDisabled, IdempotencyKey: "distribution-sale-disable"}); err != nil {
		t.Fatal(err)
	}
	if err := h.uow.Within(ctx, func(tx context.Context) error { return h.service.BackfillAttributedSaleWithin(tx, event) }); err != nil {
		t.Fatal(err)
	}
	late := salePaidEvent(944, 51, 12485, h.clock.Add(time.Minute))
	if err := h.uow.Within(ctx, func(tx context.Context) error { return h.service.BackfillAttributedSaleWithin(tx, late) }); err != nil {
		t.Fatal(err)
	}
	assertCount(t, h.pool, `SELECT count(*) FROM referral_product_sale_events WHERE order_id=944`, 0)
	for _, excluded := range []orderport.PaidEvent{
		salePaidEvent(945, 52, 12486, paidAt),
		salePaidEvent(946, 51, 3541, paidAt),
		salePaidEvent(947, 51, 12487, c.StartsAt.Add(-time.Second)),
		salePaidEvent(948, 51, 12488, c.EndsAt),
	} {
		if err := h.uow.Within(ctx, func(tx context.Context) error { return h.service.BackfillAttributedSaleWithin(tx, excluded) }); err != nil {
			t.Fatalf("excluded order=%d err=%v", excluded.OrderID, err)
		}
		assertCount(t, h.pool, `SELECT count(*) FROM referral_product_sale_events WHERE order_id=$1`, 0, excluded.OrderID)
	}
}

func TestPostgreSQLDistributionSaleUsesTeamMembershipAtPayment(t *testing.T) {
	h := newReferralPostgreSQLHarness(t)
	defer h.cleanup()
	ctx := context.Background()
	c, err := h.admin.CreateCampaign(ctx, referralport.CreateCampaignCommand{ActorAdminID: 9001, Name: "战队销售", StartsAt: h.clock.Add(-time.Hour), EndsAt: h.clock.Add(24 * time.Hour), TeamMode: referraldomain.TeamModeTeam, QualificationMode: referraldomain.QualificationProductPurchase, ProductID: 51, ProductType: referraldomain.ProductTypeStandard, LeaderboardMetric: referraldomain.LeaderboardSalesAmount, IdempotencyKey: "team-sale-create"})
	if err != nil {
		t.Fatal(err)
	}
	team, err := h.admin.CreateTeam(ctx, referralport.CreateTeamCommand{ActorAdminID: 9001, CampaignID: c.ID, CaptainCustomerID: 5001, Name: "甲队", IdempotencyKey: "team-sale-team"})
	if err != nil {
		t.Fatal(err)
	}
	c, err = h.admin.SetCampaignState(ctx, referralport.SetCampaignStateCommand{ActorAdminID: 9001, CampaignID: c.ID, ExpectedVersion: c.Version, Target: referraldomain.CampaignActive, IdempotencyKey: "team-sale-active"})
	if err != nil {
		t.Fatal(err)
	}
	paidAt := h.clock.Add(time.Minute)
	for _, member := range []struct {
		customerID int64
		joinedAt   time.Time
	}{{3541, paidAt.Add(-time.Minute)}, {3542, paidAt.Add(time.Minute)}} {
		if _, err := h.pool.Exec(ctx, `INSERT INTO referral_participations(campaign_id,customer_id,team_id,state,joined_at)
VALUES($1,$2,$3,'active',$4)`, c.ID, member.customerID, team.ID, member.joinedAt); err != nil {
			t.Fatal(err)
		}
	}
	stub := saleAttributionStub{values: map[int64]distributionport.FrozenOrderAttribution{
		950: {OrderID: 950, OrderItemLine: 1, ProductID: 51, ProductType: distributiondomain.ProductTypeStandard, PromoterCustomerID: 3541, CredentialID: 17, PolicyVersion: 1, CommissionRateBasisPoints: 1000, AttributedAt: h.clock.Add(-time.Minute)},
		951: {OrderID: 951, OrderItemLine: 1, ProductID: 51, ProductType: distributiondomain.ProductTypeStandard, PromoterCustomerID: 3542, CredentialID: 18, PolicyVersion: 1, CommissionRateBasisPoints: 1000, AttributedAt: h.clock.Add(-time.Minute)},
	}}
	if err := h.service.SetSaleEvidenceReaders(stub, platformaudit.NewPostgreSQLStore()); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{950, 951} {
		event := salePaidEvent(id, 51, 12484+id, paidAt)
		if err := h.uow.Within(ctx, func(tx context.Context) error { return h.service.BackfillAttributedSaleWithin(tx, event) }); err != nil {
			t.Fatal(err)
		}
	}
	var firstTeam, secondTeam int64
	if err := h.pool.QueryRow(ctx, `SELECT COALESCE(team_id,0) FROM referral_product_sale_events WHERE order_id=950`).Scan(&firstTeam); err != nil {
		t.Fatal(err)
	}
	if err := h.pool.QueryRow(ctx, `SELECT COALESCE(team_id,0) FROM referral_product_sale_events WHERE order_id=951`).Scan(&secondTeam); err != nil {
		t.Fatal(err)
	}
	if firstTeam != team.ID || secondTeam != 0 {
		t.Fatalf("team at payment first=%d second=%d want=%d,0", firstTeam, secondTeam, team.ID)
	}
	board, err := h.service.Leaderboard(ctx, referralport.LeaderboardQuery{CampaignID: c.ID, Kind: referralport.LeaderboardTeam, Period: referralport.LeaderboardTotal, Limit: 20})
	if err != nil || len(board.Items) != 1 || board.Items[0].TeamID != team.ID || board.Items[0].Score != 990 {
		t.Fatalf("team sales board=%+v err=%v", board, err)
	}
}
