package app

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	referraldomain "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/domain"
	referralport "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/port"
)

// A checkout freezes an activity while accepting, but participation still
// requires that activity to have been accepting at the actual payment instant.
// Delayed delivery of a payment made before disable must remain admissible.
func TestPostgreSQLReferralSol61CheckoutParticipationUsesPaymentLifecycle(t *testing.T) {
	for _, scenario := range []struct {
		name         string
		paidAtOffset time.Duration
		disable      bool
		want         int64
	}{
		{name: "disabled_before_payment", paidAtOffset: 2 * time.Minute, disable: true, want: 0},
		{name: "delayed_payment_before_disable", paidAtOffset: time.Minute / 2, disable: true, want: 1},
		{name: "payment_at_exclusive_end", paidAtOffset: 2 * time.Hour, want: 0},
		{name: "payment_before_end", paidAtOffset: 2*time.Hour - time.Second, want: 1},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			h := newReferralPostgreSQLHarness(t)
			defer h.cleanup()
			ctx := context.Background()
			checkoutAt := h.clock
			campaign, err := h.admin.CreateCampaign(ctx, referralport.CreateCampaignCommand{ActorAdminID: 9001, Name: "sol61 payment lifecycle", StartsAt: checkoutAt.Add(-time.Hour), EndsAt: checkoutAt.Add(2 * time.Hour), TeamMode: referraldomain.TeamModeIndividual, QualificationMode: referraldomain.QualificationProductPurchase, ProductID: 51, ProductType: referraldomain.ProductTypeStandard, LeaderboardMetric: referraldomain.LeaderboardSalesAmount, IdempotencyKey: "sol61-lifecycle-create"})
			if err != nil {
				t.Fatal(err)
			}
			campaign, err = h.admin.SetCampaignState(ctx, referralport.SetCampaignStateCommand{ActorAdminID: 9001, CampaignID: campaign.ID, ExpectedVersion: campaign.Version, Target: referraldomain.CampaignActive, IdempotencyKey: "sol61-lifecycle-active"})
			if err != nil {
				t.Fatal(err)
			}
			credential, err := h.service.IssueProductActivityContext(ctx, referralActor(521, checkoutAt), campaign.ID, "sol61-lifecycle-context")
			if err != nil {
				t.Fatal(err)
			}
			if err = h.uow.Within(ctx, func(tx context.Context) error {
				return h.service.RecordProductSaleCheckoutAttributionWithin(tx, orderport.ProductSaleCheckoutContextCommand{OrderID: 9521, OrderVersion: 1, ProductID: 51, ProductType: "standard_product", ProductCode: "p-51", ProductName: "Product 51", ProductVersion: 1, BuyerCustomerID: 521, BeneficiaryCustomerID: 521, ReferralActivityContext: credential.Token, OccurredAt: checkoutAt})
			}); err != nil {
				t.Fatal(err)
			}
			if err = h.service.SetSaleEvidenceReaders(saleAttributionStub{}, platformaudit.NewPostgreSQLStore()); err != nil {
				t.Fatal(err)
			}
			if scenario.disable {
				h.clock = checkoutAt.Add(time.Minute)
				if _, err = h.admin.SetCampaignState(ctx, referralport.SetCampaignStateCommand{ActorAdminID: 9001, CampaignID: campaign.ID, ExpectedVersion: campaign.Version, Target: referraldomain.CampaignDisabled, IdempotencyKey: "sol61-lifecycle-disable"}); err != nil {
					t.Fatal(err)
				}
			}
			event := salePaidEvent(9521, 51, 521, checkoutAt.Add(scenario.paidAtOffset))
			event.ReferralActivityContextDigest = sha256.Sum256([]byte(credential.Token))
			if h.clock.Before(event.OccurredAt.Add(time.Minute)) {
				h.clock = event.OccurredAt.Add(time.Minute)
			}
			for replay := 0; replay < 2; replay++ {
				if err = h.uow.Within(ctx, func(tx context.Context) error { return h.service.ConsumePaidEventWithin(tx, event) }); err != nil {
					t.Fatal(err)
				}
			}
			assertCount(t, h.pool, `SELECT count(*) FROM referral_participations WHERE campaign_id=$1 AND customer_id=521`, scenario.want, campaign.ID)
			assertCount(t, h.pool, `SELECT count(*) FROM referral_product_sale_events WHERE order_id=9521`, 0)
		})
	}
}

type sol61RewardParticipationBarrier struct {
	adminStore
	customerID int64
	read       chan struct{}
	resume     chan struct{}
}

func (s sol61RewardParticipationBarrier) ReadParticipationWithin(ctx context.Context, campaignID, customerID int64, lock bool) (referraldomain.Participation, error) {
	value, err := s.adminStore.ReadParticipationWithin(ctx, campaignID, customerID, lock)
	if err == nil && customerID == s.customerID {
		close(s.read)
		select {
		case <-s.resume:
		case <-ctx.Done():
			return referraldomain.Participation{}, ctx.Err()
		}
	}
	return value, err
}

// Pause the real reward command after its active-participation check. If the
// reversal can commit first, awarding from that stale check must fail. Holding
// the participant lock instead lets the award commit first and then reverses.
func TestPostgreSQLReferralSol61RewardActiveParticipationCannotGoStale(t *testing.T) {
	h := newReferralPostgreSQLHarness(t)
	defer h.cleanup()
	campaign, team, _ := h.createCampaignWithTeams(t, "sol61-reward-participation", 651, 699)
	h.joinDirect(t, campaign.ID, team.ID, 651, "sol61-reward-inviter")
	invite := h.issue(t, campaign.ID, 651, "sol61-reward-invite")
	joined := h.joinInvite(t, campaign.ID, 652, invite, "sol61-reward-target")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	read, resume := make(chan struct{}), make(chan struct{})
	awarding := *h.admin
	awarding.store = sol61RewardParticipationBarrier{adminStore: h.repository, customerID: 652, read: read, resume: resume}
	awardDone, reverseDone := make(chan error, 1), make(chan error, 1)
	go func() {
		_, err := awarding.RecordReward(ctx, referralport.RecordRewardCommand{CampaignID: campaign.ID, CustomerID: 652, Period: "total", Reward: "sol61 reward", ActorAdminID: 9001, IdempotencyKey: "sol61-reward-write"})
		awardDone <- err
	}()
	select {
	case <-read:
	case <-ctx.Done():
		t.Fatal("reward did not reach participation check")
	}
	go func() {
		reverseDone <- h.admin.ReverseInvitation(ctx, referralport.ReverseInvitationCommand{ParticipationID: joined.Participation.ID, Reason: "sol61 concurrent reversal", ActorAdminID: 9001, IdempotencyKey: "sol61-reward-reverse"})
	}()
	reversedFirst := false
	select {
	case err := <-reverseDone:
		if err != nil {
			close(resume)
			t.Fatal(err)
		}
		reversedFirst = true
	case <-time.After(200 * time.Millisecond):
	}
	close(resume)
	awardErr := <-awardDone
	if !reversedFirst {
		if err := <-reverseDone; err != nil {
			t.Fatal(err)
		}
	}
	if reversedFirst && !errors.Is(awardErr, referralport.ErrConflict) {
		t.Fatalf("reversal committed before reward acceptance, stale active participation still awarded: %v", awardErr)
	}
	if !reversedFirst && awardErr != nil {
		t.Fatalf("serialized valid award failed: %v", awardErr)
	}
	var count int64
	if err := h.pool.QueryRow(ctx, `SELECT count(*) FROM referral_reward_records WHERE campaign_id=$1 AND customer_id=652`, campaign.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if reversedFirst && count != 0 {
		t.Fatalf("reversed participation retained %d newly accepted rewards", count)
	}
	if !reversedFirst && count != 1 {
		t.Fatalf("award before reversal count=%d", count)
	}
}

func TestPostgreSQLReferralSol61AdminAuditMatchesActorScopedReceipt(t *testing.T) {
	h := newReferralPostgreSQLHarness(t)
	defer h.cleanup()
	campaign, _, _ := h.createCampaignWithTeams(t, "sol61-audit-scope", 751, 799)
	var original referralport.UpdateCampaignCommand
	for _, actor := range []int64{9001, 9002} {
		h.clock = h.clock.Add(time.Minute)
		command := referralport.UpdateCampaignCommand{CampaignID: campaign.ID, ExpectedVersion: campaign.Version, ActorAdminID: actor, Name: campaign.Name, Description: "actor " + itoa(actor), StartsAt: campaign.StartsAt, EndsAt: campaign.EndsAt, IdempotencyKey: "sol61-shared-admin-key"}
		updated, err := h.admin.UpdateCampaign(context.Background(), command)
		if err != nil {
			t.Fatalf("independent actor %d cannot commit actor-scoped key: %v", actor, err)
		}
		campaign = updated
		if actor == 9001 {
			original = command
		}
	}
	if _, err := h.admin.UpdateCampaign(context.Background(), original); err != nil {
		t.Fatalf("original actor receipt replay failed: %v", err)
	}
	changed := original
	changed.Description = "changed payload on the original receipt"
	if _, err := h.admin.UpdateCampaign(context.Background(), changed); !errors.Is(err, referralport.ErrConflict) {
		t.Fatalf("actor receipt payload conflict disappeared: %v", err)
	}
	assertCount(t, h.pool, `SELECT count(*) FROM referral_operation_receipts WHERE operation='campaign_update'`, 2)
	assertCount(t, h.pool, `SELECT count(*) FROM audit_events WHERE action='referral.campaign.updated' AND resource_id=$1`, 2, itoa(campaign.ID))
	assertCount(t, h.pool, `SELECT count(*) FROM outbox_events WHERE event_type='referral.campaign.updated' AND aggregate_id=$1`, 2, itoa(campaign.ID))
}

func TestPostgreSQLReferralSol61RewardAfterCommittedReversalRejects(t *testing.T) {
	h := newReferralPostgreSQLHarness(t)
	defer h.cleanup()
	campaign, team, _ := h.createCampaignWithTeams(t, "sol61-reward-reversed-first", 851, 899)
	h.joinDirect(t, campaign.ID, team.ID, 851, "sol61-reversed-first-inviter")
	invite := h.issue(t, campaign.ID, 851, "sol61-reversed-first-invite")
	joined := h.joinInvite(t, campaign.ID, 852, invite, "sol61-reversed-first-target")
	if err := h.admin.ReverseInvitation(context.Background(), referralport.ReverseInvitationCommand{ParticipationID: joined.Participation.ID, Reason: "committed before reward", ActorAdminID: 9001, IdempotencyKey: "sol61-reversed-first-reverse"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.admin.RecordReward(context.Background(), referralport.RecordRewardCommand{CampaignID: campaign.ID, CustomerID: 852, Period: "total", Reward: "sol61 rejected reward", ActorAdminID: 9001, IdempotencyKey: "sol61-reversed-first-award"}); !errors.Is(err, referralport.ErrConflict) {
		t.Fatalf("reward after committed reversal: %v", err)
	}
	assertCount(t, h.pool, `SELECT count(*) FROM referral_reward_records WHERE campaign_id=$1 AND customer_id=852`, 0, campaign.ID)
	assertCount(t, h.pool, `SELECT count(*) FROM referral_operation_receipts WHERE operation='reward_record'`, 0)
}
