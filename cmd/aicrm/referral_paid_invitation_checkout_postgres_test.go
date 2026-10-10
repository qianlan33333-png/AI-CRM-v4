package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	distributionapp "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/app"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	identityquery "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/query"
	identitystore "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/store"
	orderdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformoutbox "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/outbox"
	referralapp "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/app"
	referraldomain "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/domain"
	referralport "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/port"
	referralstore "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/store"
)

// Actual Order checkout + paid fanout, using synthetic trusted payment facts.
// No Provider, callback verifier or effects worker is run.
func TestPostgreSQLReferralPaidInvitationRealOrderWithoutDistributor(t *testing.T) {
	f := newPromotionCheckoutFixture(t)
	defer f.close()
	ctx := context.Background()
	repository, err := referralstore.NewPostgreSQL(f.pool, f.uow)
	if err != nil {
		t.Fatal(err)
	}
	audit, err := platformaudit.NewService(platformaudit.NewPostgreSQLStore())
	if err != nil {
		t.Fatal(err)
	}
	queries := identityquery.NewPostgreSQL()
	service, err := referralapp.NewService(f.uow, repository, "https://crm.example.test", base64.RawStdEncoding.EncodeToString(make([]byte, 32)), sol61ReferralCloseSink{}, audit, platformoutbox.NewPostgreSQL())
	if err != nil {
		t.Fatal(err)
	}
	if err = service.BindIdentitySubjects(queries); err != nil {
		t.Fatal(err)
	}
	if err = service.SetSaleEvidenceReaders(distributionapp.NewFrozenAttributionReader(f.distributions), platformaudit.NewPostgreSQLStore()); err != nil {
		t.Fatal(err)
	}
	admin, err := referralapp.NewAdminService(f.uow, repository, referralCanonicalCustomerVerifier{resolver: canonicalCustomerAdapter{reader: queries}, identities: queries}, sol61ReferralCloseSink{}, audit, platformoutbox.NewPostgreSQL())
	if err != nil {
		t.Fatal(err)
	}
	dataKey := base64.RawStdEncoding.EncodeToString(make([]byte, 32))
	application, err := compose(ctx, platformconfig.Runtime{Role: platformconfig.RoleAPI, DatabaseURL: f.pool.Config().ConnConfig.ConnString(), PublicOrigin: "https://crm.example.test", ReleaseSHA: "paid-invitation-test", WorkerOwner: "paid-invitation-test", WorkerLimit: 1, Referral: platformconfig.Referral{TokenDataKey: dataKey}, GroupOps: platformconfig.GroupOps{WebhookSecret: "synthetic-paid-invitation"}, Survey: platformconfig.Survey{DataKey: dataKey, IdentityPhoneDataKey: dataKey}})
	if err != nil {
		t.Fatal(err)
	}
	defer application.Close()
	f.orders = application.nativeOrders
	var identityID int64
	if err = f.pool.QueryRow(ctx, `INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,verified_at) VALUES($1,'mp_openid','wechat-app:promotion-checkout','paid-invitation-owner','verified','synthetic-test',1,$2) RETURNING id`, f.buyer, time.Now()).Scan(&identityID); err != nil {
		t.Fatal(err)
	}
	actor := referralport.TrustedSessionActor{CustomerID: f.buyer, IdentityID: identityID, AppID: "wx-promotion-checkout", AppScope: "wechat-app:promotion-checkout", Channel: "mini_program", OccurredAt: time.Now()}
	c, err := admin.CreateCampaign(ctx, referralport.CreateCampaignCommand{ActorAdminID: 1, Name: "paid referral", StartsAt: time.Now().Add(-time.Hour), EndsAt: time.Now().Add(time.Hour), TeamMode: referraldomain.TeamModeIndividual, QualificationMode: referraldomain.QualificationProductPurchase, ProductID: f.productID, ProductType: "standard_product", LeaderboardMetric: referraldomain.LeaderboardInvites, IdempotencyKey: "real-paid-campaign"})
	if err != nil {
		t.Fatal(err)
	}
	c, err = admin.SetCampaignState(ctx, referralport.SetCampaignStateCommand{ActorAdminID: 1, CampaignID: c.ID, ExpectedVersion: c.Version, Target: referraldomain.CampaignActive, IdempotencyKey: "real-paid-activate"})
	if err != nil {
		t.Fatal(err)
	}
	entry, err := service.IssueProductActivityContext(ctx, actor, c.ID, "real-organic-entry")
	if err != nil {
		t.Fatal(err)
	}
	create := func(customer int64, activity, key string) orderdomain.Snapshot {
		t.Helper()
		var result orderdomain.Snapshot
		if e := f.uow.Within(ctx, func(tx context.Context) error {
			var e error
			result, e = f.orders.CreatePaymentOrderWithin(tx, orderport.PaymentOrderCommand{Provider: orderdomain.ProviderWeChatPay, MerchantOrderNo: "M-" + key, PayerCustomerID: customer, BeneficiaryCustomerID: customer, ProductID: f.productID, ProductCode: "promotion-checkout-product", ProductName: "分销归因商品", ProductVersion: 1, ProductType: "standard_product", UnitAmountMinor: 1000, Currency: "CNY", ReferralActivityContext: activity, ActorScope: "checkout:" + key, IdempotencyKey: key})
			return e
		}); e != nil {
			t.Fatal(e)
		}
		return result
	}
	settle := func(order orderdomain.Snapshot, key string) {
		t.Helper()
		if e := f.uow.Within(ctx, func(tx context.Context) error {
			_, e := f.orders.SettlePaymentWithin(tx, orderport.PaymentSettlementCommand{OrderID: order.ID, ProviderTransactionNo: "synthetic-" + key, OccurredAt: order.CreatedAt.Add(time.Millisecond), ReceiptKey: "paid-invitation-" + key})
			return e
		}); e != nil {
			t.Fatal(e)
		}
	}
	own := create(f.buyer, entry.Token, "real-own-purchase")
	settle(own, "real-own-paid")
	mine, err := service.MyCampaign(ctx, actor, c.ID)
	if err != nil || mine.Participation == nil || !mine.InvitationAvailable {
		t.Fatalf("purchase did not enable sharing: %+v %v", mine, err)
	}
	var registrations, receivers int
	if err = f.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM distribution_distributors WHERE customer_id=$1),(SELECT count(*) FROM payment_profit_sharing_receivers WHERE customer_id=$1)`, f.buyer).Scan(&registrations, &receivers); err != nil || registrations != 0 || receivers != 0 {
		t.Fatalf("inviter has financial preparation: %d/%d %v", registrations, receivers, err)
	}
	link, err := service.IssueInvitation(ctx, referralport.IssueInvitationCommand{Actor: actor, CampaignID: c.ID, IdempotencyKey: "real-share-poster"})
	if err != nil {
		t.Fatal(err)
	}
	token := strings.TrimPrefix(link.URL, "https://crm.example.test/referral/invite/")
	referred := create(f.promoter.CustomerID, token, "real-referred-purchase")
	f.assertOrderAttribution(t, referred.ID, 0, false)
	settle(referred, "real-referred-paid")
	f.assertCommission(t, referred.ID, 0)
	board, err := service.Leaderboard(ctx, referralport.LeaderboardQuery{CampaignID: c.ID, Kind: referralport.LeaderboardPersonal, Period: referralport.LeaderboardTotal, ViewerCustomerID: f.buyer, Limit: 20})
	if err != nil || len(board.Items) != 1 || board.Items[0].CustomerID != f.buyer || board.Items[0].Score != 1 {
		t.Fatalf("native paid board=%+v err=%v", board, err)
	}
	var buyerParticipants, sales int
	if err = f.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM referral_participations WHERE campaign_id=$1 AND customer_id=$2 AND state='active'),(SELECT count(*) FROM referral_product_sale_events WHERE campaign_id=$1 AND order_id=$3 AND kind='credit')`, c.ID, f.promoter.CustomerID, referred.ID).Scan(&buyerParticipants, &sales); err != nil || buyerParticipants != 1 || sales != 1 {
		t.Fatalf("atomic referred facts participants=%d sales=%d err=%v", buyerParticipants, sales, err)
	}
	// An invitation remains a self-invitation after real OneID merge, whether
	// the merge happens before checkout or between checkout and payment.
	knownCustomers := []int64{f.buyer, f.promoter.CustomerID, f.otherPromoter.CustomerID}
	oneID := identityapp.OneIDService{Store: identitystore.NewPostgresStore()}
	for _, when := range []string{"before-checkout", "after-checkout"} {
		targetFact := evidenceMergeFact(t, identitydomain.KindMPOpenID, "wechat-app:paid-invitation-alias-"+when, "paid-invitation-self-alias-"+when)
		var alias identityport.ProvisionResult
		if err = f.uow.Within(ctx, func(tx context.Context) error {
			var e error
			alias, e = oneID.ProvisionCustomerFromVerifiedIdentity(tx, targetFact)
			return e
		}); err != nil {
			t.Fatal(err)
		}
		knownCustomers = append(knownCustomers, int64(alias.CustomerID))
		merge := func() int64 {
			t.Helper()
			var mergeID int64
			if e := f.uow.Within(ctx, func(tx context.Context) error {
				candidate, e := oneID.LinkVerifiedIdentity(tx, identityapp.LinkCommand{SourceCustomerID: customerdomain.CustomerID(f.buyer), Target: targetFact, Evidence: identitydomain.LinkEvidence{Type: "test", Strength: identitydomain.EvidenceStrong, Source: "paid.referral.synthetic", EventID: "paid-referral-" + when, Digest: "paid-referral-" + when, PolicyVersion: "test-v1"}})
				if e != nil {
					return e
				}
				if candidate.Candidate == nil {
					return fmt.Errorf("missing self-alias merge candidate when=%s status=%s", when, candidate.Status)
				}
				merged, e := oneID.ConfirmMerge(tx, identityapp.ConfirmMergeCommand{CandidateID: candidate.Candidate.ID, SurvivorCustomerID: alias.CustomerID, Operator: "paid-referral-test"})
				if e != nil {
					return e
				}
				if merged.Merge == nil {
					return fmt.Errorf("missing self-alias merge when=%s status=%s", when, merged.Status)
				}
				mergeID = merged.Merge.ID
				return nil
			}); e != nil {
				t.Fatal(e)
			}
			return mergeID
		}
		var mergeID int64
		if when == "before-checkout" {
			mergeID = merge()
		}
		self := create(int64(alias.CustomerID), token, "self-"+when+"-checkout")
		if when == "after-checkout" {
			mergeID = merge()
		}
		settle(self, "self-"+when+"-paid")
		var participants, credits int
		if err = f.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM referral_participations WHERE campaign_id=$1),(SELECT count(*) FROM referral_product_sale_events WHERE campaign_id=$1 AND kind='credit')`, c.ID).Scan(&participants, &credits); err != nil || participants != 2 || credits != 1 {
			t.Fatalf("merged self purchase changed facts when=%s participants=%d credits=%d err=%v", when, participants, credits, err)
		}
		if err = f.uow.Within(ctx, func(tx context.Context) error { _, e := oneID.RevertConfirmedMerge(tx, mergeID); return e }); err != nil {
			t.Fatal(err)
		}
	}

	preparePaidReferralInstalledDirectory(t, f, knownCustomers)
	verifyInstalledPaidReferral(t, f, actor, c.ID)
}
