package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	distributionapp "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/app"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	identityquery "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/query"
	orderdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	orderstore "github.com/qianlan33333-png/AI-CRM-v3/internal/order/store"
	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformoutbox "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/outbox"
	referralapp "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/app"
	referraldomain "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/domain"
	referralport "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/port"
	referralstore "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/store"
)

// Real Composition Root, OneID, qualifying Payment facts and Order paid fanout.
// Only API runs; accepted effects are never dispatched to a real Provider.
func TestPostgreSQLPaidReferralFinancialAttributionAtomicAndInstalled(t *testing.T) {
	for _, channel := range []string{"mini_program", "h5_official_account"} {
		t.Run(channel, func(t *testing.T) { paidReferralFinancialJourney(t, channel) })
	}
}

func paidReferralFinancialJourney(t *testing.T, channel string) {
	f := newPromotionCheckoutFixture(t)
	defer f.close()
	ctx := context.Background()
	for _, value := range os.Environ() {
		key, _, _ := strings.Cut(value, "=")
		if strings.HasPrefix(key, "AICRM_") {
			t.Setenv(key, "")
		}
	}
	for _, value := range paidReferralFinancialInstalledEnvironment(t, f.pool.Config().ConnConfig.ConnString(), strings.Repeat("a", 40), "127.0.0.1:18081") {
		key, value, _ := strings.Cut(value, "=")
		t.Setenv(key, value)
	}
	cfg, err := platformconfig.Load()
	if err != nil {
		t.Fatal(err)
	}
	application, err := compose(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer application.Close()
	f.orders = application.nativeOrders
	queries := identityquery.NewPostgreSQL()
	qualification, err := distributionapp.NewQualificationService(queries, f.orders, application.paymentReconciliation)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := referralstore.NewPostgreSQL(f.pool, f.uow)
	if err != nil {
		t.Fatal(err)
	}
	audit, err := platformaudit.NewService(platformaudit.NewPostgreSQLStore())
	if err != nil {
		t.Fatal(err)
	}
	service, err := referralappNewFinancialService(f, repository, audit, queries, qualification)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := referralappNewFinancialAdmin(f, repository, audit, queries)
	if err != nil {
		t.Fatal(err)
	}
	var identityID int64
	if err = f.pool.QueryRow(ctx, `INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,verified_at) VALUES($1,'mp_openid','wechat-app:promotion-checkout','paid-financial-owner','verified','synthetic-test',1,$2) RETURNING id`, f.buyer, time.Now()).Scan(&identityID); err != nil {
		t.Fatal(err)
	}
	f.seedQualificationPurchase(t, f.buyer, identityID, f.productID, "paid-financial-owner")
	if _, err = f.pool.Exec(ctx, `UPDATE distribution_product_policies SET wait_days=0 WHERE product_id=$1`, f.productID); err != nil {
		t.Fatal(err)
	}
	actor := referralport.TrustedSessionActor{CustomerID: f.buyer, IdentityID: identityID, AppID: "wx-promotion-checkout", AppScope: "wechat-app:promotion-checkout", Channel: "mini_program", OccurredAt: time.Now()}
	if channel == "h5_official_account" {
		if err = f.pool.QueryRow(ctx, `INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,verified_at) VALUES($1,'oa_openid','wechat-app:wx-paid-referral-h5','paid-financial-oa-owner','verified','synthetic-test',1,$2) RETURNING id`, f.buyer, time.Now()).Scan(&actor.IdentityID); err != nil {
			t.Fatal(err)
		}
		actor.AppID, actor.AppScope, actor.Channel = "wx-paid-referral-h5", "wechat-app:wx-paid-referral-h5", channel
	}

	campaign, err := admin.CreateCampaign(ctx, referralport.CreateCampaignCommand{ActorAdminID: 1, Name: "financial referral", StartsAt: time.Now().Add(-time.Hour), EndsAt: time.Now().Add(time.Hour), TeamMode: referraldomain.TeamModeIndividual, QualificationMode: referraldomain.QualificationProductPurchase, ProductID: f.productID, ProductType: "standard_product", LeaderboardMetric: referraldomain.LeaderboardInvites, IdempotencyKey: "paid-financial-campaign"})
	if err != nil {
		t.Fatal(err)
	}
	campaign, err = admin.SetCampaignState(ctx, referralport.SetCampaignStateCommand{ActorAdminID: 1, CampaignID: campaign.ID, ExpectedVersion: campaign.Version, Target: referraldomain.CampaignActive, IdempotencyKey: "paid-financial-activate"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.JoinCampaign(ctx, referralport.JoinCampaignCommand{Actor: actor, CampaignID: campaign.ID, IdempotencyKey: "paid-financial-join"}); err != nil {
		t.Fatal(err)
	}
	link, err := service.IssueInvitation(ctx, referralport.IssueInvitationCommand{Actor: actor, CampaignID: campaign.ID, IdempotencyKey: "paid-financial-invite"})
	if err != nil {
		t.Fatal(err)
	}
	token := strings.TrimPrefix(link.URL, "https://crm.example.test/referral/invite/")
	createWithin := func(tx context.Context, buyer int64, key string) (orderdomain.Snapshot, error) {
		return f.orders.CreatePaymentOrderWithin(tx, orderport.PaymentOrderCommand{Provider: orderdomain.ProviderWeChatPay, MerchantOrderNo: "M-" + key, PayerCustomerID: buyer, BeneficiaryCustomerID: buyer, ProductID: f.productID, ProductCode: "promotion-checkout-product", ProductName: "分销归因商品", ProductVersion: 1, ProductType: "standard_product", UnitAmountMinor: 1000, Currency: "CNY", ReferralActivityContext: token, ActorScope: "checkout:" + key, IdempotencyKey: key})
	}
	aborted := errors.New("isolated rollback assertion")
	if err = f.uow.Within(ctx, func(tx context.Context) error {
		_, e := createWithin(tx, f.promoter.CustomerID, "paid-financial-abort")
		if e != nil {
			return e
		}
		return aborted
	}); !errors.Is(err, aborted) {
		t.Fatal("financial checkout failed before intended rollback", err)
	}
	var enrollments, receivers, attributions, receiverEffects int
	if err = f.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM distribution_distributors WHERE customer_id=$1),(SELECT count(*) FROM payment_profit_sharing_receivers WHERE customer_id=$1),(SELECT count(*) FROM distribution_order_attributions a JOIN distribution_distributors d ON d.id=a.distributor_id WHERE d.customer_id=$1),(SELECT count(*) FROM external_effects WHERE kind=$2)`, f.buyer, string(effectport.KindWeChatPayReceiverAdd)).Scan(&enrollments, &receivers, &attributions, &receiverEffects); err != nil || enrollments != 0 || receivers != 0 || attributions != 0 || receiverEffects != 0 {
		t.Fatal("financial UoW leaked on rollback", enrollments, receivers, attributions, err)
	}
	create := func(buyer int64, key string) orderdomain.Snapshot {
		t.Helper()
		var result orderdomain.Snapshot
		if e := f.uow.Within(ctx, func(tx context.Context) error { var e error; result, e = createWithin(tx, buyer, key); return e }); e != nil {
			t.Fatal(e)
		}
		f.assertOrderAttribution(t, result.ID, 1, true)
		return result
	}
	first := create(f.promoter.CustomerID, "paid-financial-first")
	var receiverEffect int64
	var receiverState, provenance string
	var prepared bool
	if err = f.pool.QueryRow(ctx, `SELECT r.external_effect_id,r.state,d.agreement_version,d.receiver_ready FROM payment_profit_sharing_receivers r JOIN distribution_distributors d ON d.customer_id=r.customer_id WHERE r.customer_id=$1`, f.buyer).Scan(&receiverEffect, &receiverState, &provenance, &prepared); err != nil || receiverEffect < 1 || receiverState != "accepted" || prepared || provenance != "paid_campaign_referral_v1" {
		t.Fatal("automatic receiver preparation not atomically accepted", err)
	}
	orderRepository, err := orderstore.NewPostgreSQL(f.pool, f.uow)
	if err != nil {
		t.Fatal(err)
	}
	replayDue := &refundWorkerDue{}
	commissionConsumer, err := distributionapp.NewCommissionService(f.distributions, replayDue, qualification)
	if err != nil {
		t.Fatal(err)
	}
	settle := func(order orderdomain.Snapshot, key string) {
		t.Helper()
		if e := f.uow.Within(ctx, func(tx context.Context) error {
			_, e := f.orders.SettlePaymentWithin(tx, orderport.PaymentSettlementCommand{OrderID: order.ID, ProviderTransactionNo: "synthetic-" + key, OccurredAt: order.CreatedAt.Add(time.Millisecond), ReceiptKey: "paid-financial-" + key})
			return e
		}); e != nil {
			t.Fatal(e)
		}
		// Replay the same authoritative first-paid fact, as callback handling
		// does after its own receipt gate. SettlePayment itself is a transition
		// command and correctly rejects a second paid transition.
		for replay := 0; replay < 2; replay++ {
			if e := f.uow.Within(ctx, func(tx context.Context) error {
				fact, e := orderRepository.ReadPaidSaleBackfillFactWithin(tx, order.ID)
				if e != nil {
					return e
				}
				if e = commissionConsumer.ConsumePaidEventWithin(tx, fact.Paid); e != nil {
					return e
				}
				return service.ConsumePaidEventWithin(tx, fact.Paid)
			}); e != nil {
				t.Fatal(e)
			}
		}
		if len(replayDue.ids) != 0 {
			t.Fatal("paid event replay enqueued another commission due job")
		}
		f.assertCommission(t, order.ID, 1)
	}
	settle(first, "first")
	var commissions, instructions int
	var paidMinor, payableMinor int64
	var dueAt, paidAt time.Time
	if err = f.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM distribution_commissions WHERE order_id=$1),(SELECT count(*) FROM payment_profit_sharing_instructions),paid_minor,current_payable_minor,due_at,paid_confirmed_at FROM distribution_commissions WHERE order_id=$1`, first.ID).Scan(&commissions, &instructions, &paidMinor, &payableMinor, &dueAt, &paidAt); err != nil || commissions != 1 || instructions != 0 || paidMinor != 0 || payableMinor != 100 || dueAt.Before(paidAt.Add(time.Minute)) {
		t.Fatal("pending receiver or callback replay created invalid money facts", err)
	}
	// Synthetic completion exercises the real Payment-to-Distribution observer.
	if err = f.uow.Within(ctx, func(tx context.Context) error {
		return application.paymentDistribution.CompleteEffect(tx, "eer_"+strconv.FormatInt(receiverEffect, 10), effectport.Envelope{Owner: effectport.OwnerPayment, Kind: effectport.KindWeChatPayReceiverAdd}, effectport.Attempt{EffectID: "eer_" + strconv.FormatInt(receiverEffect, 10), Number: 1, Generation: 1, Fence: 1}, effectport.AdapterResult{Completion: effectport.StateExecuted, ReceiptDigest: effectport.Hash("paid-financial-synthetic-receiver-ready")})
	}); err != nil {
		t.Fatal(err)
	}
	second := create(f.otherPromoter.CustomerID, "paid-financial-second")
	settle(second, "second")
	var credentials int
	if err = f.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM payment_profit_sharing_receivers WHERE customer_id=$1),(SELECT count(*) FROM distribution_promotion_credentials c JOIN distribution_distributors d ON d.id=c.distributor_id WHERE d.customer_id=$1),d.receiver_ready FROM distribution_distributors d WHERE d.customer_id=$1`, f.buyer).Scan(&receivers, &credentials, &prepared); err != nil || receivers != 1 || credentials != 1 || !prepared {
		t.Fatal("receiver or invitation credential duplicated", receivers, credentials, prepared, err)
	}
	board, err := service.Leaderboard(ctx, referralport.LeaderboardQuery{CampaignID: campaign.ID, Kind: referralport.LeaderboardPersonal, Period: referralport.LeaderboardTotal, ViewerCustomerID: f.buyer, Limit: 20})
	if err != nil || len(board.Items) != 1 || board.Items[0].Score != 2 {
		t.Fatal("financial attribution lost referral statistics", err)
	}

	// A stale unrelated Distribution cookie must never replace an invalid RFI.
	stale := promotionCheckoutToken(t, f.issue(t, f.promoter, "paid-financial-unrelated-cookie"))
	var ordinary orderdomain.Snapshot
	if err = f.uow.Within(ctx, func(tx context.Context) error {
		var e error
		ordinary, e = f.orders.CreatePaymentOrderWithin(tx, orderport.PaymentOrderCommand{Provider: orderdomain.ProviderWeChatPay, MerchantOrderNo: "M-paid-financial-invalid", PayerCustomerID: f.promoter.CustomerID, BeneficiaryCustomerID: f.promoter.CustomerID, ProductID: f.productID, ProductCode: "promotion-checkout-product", ProductName: "分销归因商品", ProductVersion: 1, ProductType: "standard_product", UnitAmountMinor: 1000, Currency: "CNY", ReferralActivityContext: "rfi_" + strings.Repeat("A", 43), PromotionContext: stale, ActorScope: "checkout:paid-financial-invalid", IdempotencyKey: "paid-financial-invalid"})
		return e
	}); err != nil {
		t.Fatal(err)
	}
	f.assertOrderAttribution(t, ordinary.ID, 0, false)
	// Existing invitations with no authenticated issuer stay statistics-only.
	legacy, err := service.IssueInvitation(ctx, referralport.IssueInvitationCommand{Actor: actor, CampaignID: campaign.ID, IdempotencyKey: "paid-financial-legacy"})
	if err != nil {
		t.Fatal(err)
	}
	legacyToken := strings.TrimPrefix(legacy.URL, "https://crm.example.test/referral/invite/")
	legacyDigest := sha256.Sum256([]byte(legacyToken))
	if _, err = f.pool.Exec(ctx, `UPDATE referral_invitations SET issuer_identity_id=0,issuer_app_id='',issuer_app_scope='',issuer_channel='' WHERE token_digest=$1`, legacyDigest[:]); err != nil {
		t.Fatal(err)
	}
	originalToken := token
	token = legacyToken
	if err = f.uow.Within(ctx, func(tx context.Context) error {
		var e error
		ordinary, e = createWithin(tx, f.promoter.CustomerID, "paid-financial-legacy-order")
		return e
	}); err != nil {
		t.Fatal(err)
	}
	f.assertOrderAttribution(t, ordinary.ID, 0, false)
	token = originalToken
	if _, err = f.pool.Exec(ctx, `UPDATE distribution_product_policies SET enabled=false WHERE product_id=$1`, f.productID); err != nil {
		t.Fatal(err)
	}
	if err = f.uow.Within(ctx, func(tx context.Context) error {
		var e error
		ordinary, e = createWithin(tx, f.promoter.CustomerID, "paid-financial-disabled-order")
		return e
	}); err != nil {
		t.Fatal(err)
	}
	f.assertOrderAttribution(t, ordinary.ID, 0, false)
	if _, err = f.pool.Exec(ctx, `UPDATE distribution_product_policies SET enabled=true WHERE product_id=$1`, f.productID); err != nil {
		t.Fatal(err)
	}
	preparePaidReferralInstalledDirectory(t, f, []int64{f.buyer, f.promoter.CustomerID, f.otherPromoter.CustomerID})
	verifyInstalledPaidReferralWithOptions(t, f, actor, campaign.ID, true, 2)
}

func referralappNewFinancialService(f *promotionCheckoutFixture, repository *referralstore.Repository, audit *platformaudit.Service, queries identityquery.PostgreSQL, qualification *distributionapp.QualificationService) (*referralapp.Service, error) {
	service, err := referralapp.NewService(f.uow, repository, "https://crm.example.test", base64.RawStdEncoding.EncodeToString(make([]byte, 32)), sol61ReferralCloseSink{}, audit, platformoutbox.NewPostgreSQL())
	if err != nil {
		return nil, err
	}
	if err = service.BindIdentitySubjects(queries); err != nil {
		return nil, err
	}
	if err = service.SetPurchaseQualificationReader(qualification); err != nil {
		return nil, err
	}
	if err = service.SetSaleEvidenceReaders(distributionapp.NewFrozenAttributionReader(f.distributions), platformaudit.NewPostgreSQLStore()); err != nil {
		return nil, err
	}
	return service, nil
}
func referralappNewFinancialAdmin(f *promotionCheckoutFixture, repository *referralstore.Repository, audit *platformaudit.Service, queries identityquery.PostgreSQL) (*referralapp.AdminService, error) {
	return referralapp.NewAdminService(f.uow, repository, referralCanonicalCustomerVerifier{resolver: canonicalCustomerAdapter{reader: queries}, identities: queries}, sol61ReferralCloseSink{}, audit, platformoutbox.NewPostgreSQL())
}
