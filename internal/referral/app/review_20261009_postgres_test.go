package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"testing"
	"time"

	platformoutbox "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/outbox"
	referraldomain "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/domain"
	referralport "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/port"
	referralstore "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/store"
)

func TestReview20261009ReferralInvitationCannotAssignForeignCampaignTeam(t *testing.T) {
	h := newReferralPostgreSQLHarness(t)
	defer h.cleanup()
	ctx := context.Background()
	campaign, localTeam, _ := h.createCampaignWithTeams(t, "review-20261009-target", 1101, 1102)
	otherCampaign, foreignTeam, _ := h.createCampaignWithTeams(t, "review-20261009-other", 1201, 1202)
	h.joinDirect(t, campaign.ID, 0, 1301, "review-20261009-unteamed-inviter")
	token := h.issue(t, campaign.ID, 1301, "review-20261009-unteamed-invitation")
	before := review20261009Facts(t, h)
	joined, err := h.service.JoinCampaign(ctx, referralport.JoinCampaignCommand{
		Actor: referralActor(1302, h.clock), CampaignID: campaign.ID, InvitationToken: token,
		TeamID: foreignTeam.ID, IdempotencyKey: "review-20261009-foreign-team-attempt",
	})
	if !errors.Is(err, referralport.ErrConflict) {
		t.Errorf("foreign campaign team accepted: participation=%+v err=%v target=%d foreign_campaign=%d foreign_team=%d", joined.Participation, err, campaign.ID, otherCampaign.ID, foreignTeam.ID)
	}
	if after := review20261009Facts(t, h); after != before {
		t.Errorf("foreign campaign rejection must roll back every fact: before=%v after=%v", before, after)
	}
	joined, err = h.service.JoinCampaign(ctx, referralport.JoinCampaignCommand{
		Actor: referralActor(1303, h.clock), CampaignID: campaign.ID, InvitationToken: token,
		TeamID: localTeam.ID, IdempotencyKey: "review-20261009-local-team-valid",
	})
	if err != nil || joined.Participation == nil || joined.Participation.TeamID != localTeam.ID {
		t.Fatalf("existing same-campaign selection failed: participation=%+v err=%v", joined.Participation, err)
	}
}

type review20261009RelationshipBoundary struct {
	*referralstore.Repository
	entered chan struct{}
}

func (r *review20261009RelationshipBoundary) LockRelationshipCustomerWithin(ctx context.Context, customerID int64) error {
	close(r.entered)
	return r.Repository.LockRelationshipCustomerWithin(ctx, customerID)
}

func TestReview20261009ReferralCanceledJoinRollsBackAndOriginalKeyRecovers(t *testing.T) {
	h := newReferralPostgreSQLHarness(t)
	defer h.cleanup()
	ctx, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	campaign, team, _ := h.createCampaignWithTeams(t, "review-canceled-join", 2001, 2002)
	if _, err := h.pool.Exec(ctx, `INSERT INTO referral_relationship_locks(customer_id) VALUES(2003)`); err != nil {
		t.Fatal(err)
	}
	locker, err := h.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer locker.Rollback(context.Background())
	if _, err = locker.Exec(ctx, `SELECT 1 FROM referral_relationship_locks WHERE customer_id=2003 FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	h.service.store = &review20261009RelationshipBoundary{Repository: h.repository, entered: entered}
	command := referralport.JoinCampaignCommand{Actor: referralActor(2003, h.clock), CampaignID: campaign.ID, TeamID: team.ID, IdempotencyKey: "review-20261009-canceled-original-key"}
	before := review20261009Facts(t, h)
	request, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { _, e := h.service.JoinCampaign(request, command); done <- e }()
	select {
	case <-entered:
	case e := <-done:
		t.Fatalf("join ended before lock boundary: %v", e)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	cancel()
	select {
	case e := <-done:
		if !errors.Is(e, context.Canceled) {
			t.Fatalf("blocked join cancellation err=%v", e)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if after := review20261009Facts(t, h); after != before {
		t.Fatalf("canceled join committed partial facts: before=%v after=%v", before, after)
	}
	assertCount(t, h.pool, `SELECT count(*) FROM referral_participation_locks WHERE campaign_id=$1 AND customer_id=2003`, 0, campaign.ID)
	if err = locker.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	h.service.store = h.repository
	joined, err := h.service.JoinCampaign(ctx, command)
	if err != nil || joined.Participation == nil || joined.Participation.CustomerID != 2003 {
		t.Fatalf("original-key recovery=%+v err=%v", joined.Participation, err)
	}
	assertCount(t, h.pool, `SELECT count(*) FROM referral_participations WHERE campaign_id=$1 AND customer_id=2003`, 1, campaign.ID)
	assertCount(t, h.pool, `SELECT count(*) FROM referral_operation_receipts WHERE operation='join' AND actor_scope='customer:2003'`, 1)
}

func TestReview20261009ReferralSalesCloseSnapshotMatchesConfiguredRanking(t *testing.T) {
	for _, metric := range []referraldomain.LeaderboardMetric{referraldomain.LeaderboardSalesAmount, referraldomain.LeaderboardSalesOrders} {
		t.Run(string(metric), func(t *testing.T) {
			h := newReferralPostgreSQLHarness(t)
			defer h.cleanup()
			ctx := context.Background()
			campaign, err := h.admin.CreateCampaign(ctx, referralport.CreateCampaignCommand{
				ActorAdminID: 9001, Name: "review sales close " + string(metric), StartsAt: h.clock.Add(-time.Hour), EndsAt: h.clock.Add(time.Hour),
				TeamMode: referraldomain.TeamModeTeam, QualificationMode: referraldomain.QualificationProductPurchase,
				ProductID: 1501, ProductType: referraldomain.ProductTypeStandard, LeaderboardMetric: metric,
				IdempotencyKey: "review-20261009-sales-close-create",
			})
			if err != nil {
				t.Fatal(err)
			}
			campaign, err = h.admin.SetCampaignState(ctx, referralport.SetCampaignStateCommand{
				ActorAdminID: 9001, CampaignID: campaign.ID, ExpectedVersion: campaign.Version, Target: referraldomain.CampaignActive,
				IdempotencyKey: "review-20261009-sales-close-activate",
			})
			if err != nil {
				t.Fatal(err)
			}
			team, err := h.admin.CreateTeam(ctx, referralport.CreateTeamCommand{
				ActorAdminID: 9001, CampaignID: campaign.ID, CaptainCustomerID: 1502, Name: "synthetic sales team", IdempotencyKey: "review-20261009-sales-close-team",
			})
			if err != nil {
				t.Fatal(err)
			}
			// Referral-owned synthetic ledger fixtures only. This test does not
			// invoke Payment, Order settlement, refunds or any Provider.
			var sales [4]referraldomain.ProductSaleEvent
			for i, amount := range []int64{90, 90, 200, 200} {
				promoterID := int64(1502)
				paidAt := h.clock.Add(time.Duration(i) * time.Minute)
				if i >= 2 {
					promoterID = int64(1501 + i)
					// Equal totals, equal order counts and equal reach times must
					// fall back to the same stable owner ordering in the snapshot.
					paidAt = h.clock.Add(2 * time.Minute)
				}
				if err = h.uow.Within(ctx, func(tx context.Context) error {
					var e error
					sales[i], e = h.repository.InsertProductSaleEventWithin(tx, referraldomain.ProductSaleEvent{
						CampaignID: campaign.ID, OrderID: int64(1600 + i), OrderVersion: 2, ProductID: campaign.ProductID, ProductType: campaign.ProductType,
						ProductCode: "synthetic-product", ProductName: "synthetic sale", ProductVersion: 1, TeamID: team.ID, PromoterCustomerID: promoterID,
						PromotionCredentialRef: "synthetic:credential", PolicyVersion: 1, BuyerCustomerID: 1701, BeneficiaryCustomerID: 1701,
						Kind: referraldomain.ProductSaleCredit, AmountDeltaMinor: amount, OrderCountDelta: 1, SourcePaidEventID: int64(1800 + i),
						Currency: "CNY", SourceDigest: sha256.Sum256([]byte("synthetic-sales-close")), OccurredAt: paidAt,
					})
					return e
				}); err != nil {
					t.Fatal(err)
				}
			}
			appendCorrection := func(original referraldomain.ProductSaleEvent, amount, orders int64, key string, at time.Time) {
				t.Helper()
				correction := original
				correction.ID, correction.Kind = 0, referraldomain.ProductSaleReversal
				correction.ReversesSaleEventID, correction.RefundReceiptKey = original.ID, key
				correction.AmountDeltaMinor, correction.OrderCountDelta, correction.OccurredAt = amount, orders, at
				if e := h.uow.Within(ctx, func(tx context.Context) error {
					_, e := h.repository.InsertProductSaleEventWithin(tx, correction)
					return e
				}); e != nil {
					t.Fatal(e)
				}
			}
			// Append Referral-owned correction fixtures directly. No refund
			// consumer, settlement path or external effect is invoked.
			appendCorrection(sales[0], -40, 0, "synthetic-ledger-before-close", h.clock.Add(3*time.Minute))
			board, err := h.service.Leaderboard(ctx, referralport.LeaderboardQuery{CampaignID: campaign.ID, Kind: referralport.LeaderboardPersonal, Period: referralport.LeaderboardTotal, Limit: 100})
			if err != nil || len(board.Items) != 3 {
				t.Fatalf("source board=%+v err=%v", board, err)
			}
			teamBoard, err := h.service.Leaderboard(ctx, referralport.LeaderboardQuery{CampaignID: campaign.ID, Kind: referralport.LeaderboardTeam, Period: referralport.LeaderboardTotal, Limit: 100})
			if err != nil || len(teamBoard.Items) != 1 {
				t.Fatalf("source team board=%+v err=%v", teamBoard, err)
			}
			h.clock = campaign.EndsAt.Add(time.Minute)
			before := review20261009Facts(t, h)
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			if err = h.admin.RunCampaignClose(canceled, campaign.ID); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled close err=%v", err)
			}
			h.admin.outbox = failingReferralOutbox{}
			if err = h.admin.RunCampaignClose(ctx, campaign.ID); !errors.Is(err, errReferralOutbox) {
				t.Fatalf("close outbox failure err=%v", err)
			}
			if after := review20261009Facts(t, h); after != before {
				t.Fatalf("failed close split its transaction: before=%v after=%v", before, after)
			}
			assertCount(t, h.pool, `SELECT count(*) FROM referral_campaign_snapshots WHERE campaign_id=$1`, 0, campaign.ID)
			assertCount(t, h.pool, `SELECT count(*) FROM referral_campaigns WHERE id=$1 AND state='active'`, 1, campaign.ID)
			h.admin.outbox = platformoutbox.NewPostgreSQL()
			if err = h.admin.RunCampaignClose(ctx, campaign.ID); err != nil {
				t.Fatal(err)
			}
			var encoded string
			var watermark int64
			if err = h.pool.QueryRow(ctx, `SELECT rankings::text,score_watermark FROM referral_campaign_snapshots WHERE campaign_id=$1`, campaign.ID).Scan(&encoded, &watermark); err != nil {
				t.Fatal(err)
			}
			var snapshot struct {
				Personal []struct {
					CustomerID int64 `json:"customer_id"`
					Score      int64 `json:"score"`
				} `json:"personal"`
				Team []struct {
					TeamID int64 `json:"team_id"`
					Score  int64 `json:"score"`
				} `json:"team"`
			}
			if err = json.Unmarshal([]byte(encoded), &snapshot); err != nil {
				t.Fatal(err)
			}
			if len(snapshot.Personal) != len(board.Items) || len(snapshot.Team) != len(teamBoard.Items) || watermark != 5 {
				t.Fatalf("sales close snapshot lost configured rankings: metric=%s board=%+v team_board=%+v snapshot=%s watermark=%d", metric, board.Items, teamBoard.Items, encoded, watermark)
			}
			for i, item := range board.Items {
				if snapshot.Personal[i].CustomerID != item.CustomerID || snapshot.Personal[i].Score != item.Score {
					t.Errorf("snapshot personal rank %d=%+v want=%+v", i+1, snapshot.Personal[i], item)
				}
			}
			if snapshot.Team[0].TeamID != team.ID || snapshot.Team[0].Score != teamBoard.Items[0].Score {
				t.Errorf("snapshot team=%+v want=%+v", snapshot.Team, teamBoard.Items)
			}
			appendCorrection(sales[2], -200, -1, "synthetic-ledger-after-close", h.clock)
			corrected, err := h.service.Leaderboard(ctx, referralport.LeaderboardQuery{CampaignID: campaign.ID, Kind: referralport.LeaderboardPersonal, Period: referralport.LeaderboardTotal, Limit: 100})
			if err != nil || len(corrected.Items) != 2 {
				t.Fatalf("late correction did not update live board: %+v err=%v", corrected.Items, err)
			}
			before = review20261009Facts(t, h)
			// Recreate the application to model a process restart: its result
			// must come from the persistent receipt, not an in-memory guard.
			restarted, err := NewAdminService(h.uow, h.repository, referralCustomerVerifier{}, &referralCloseEnqueuer{}, h.admin.audit, platformoutbox.NewPostgreSQL())
			if err != nil {
				t.Fatal(err)
			}
			restarted.now = func() time.Time { return h.clock }
			if err = restarted.RunCampaignClose(ctx, campaign.ID); err != nil {
				t.Fatal(err)
			}
			if after := review20261009Facts(t, h); after != before {
				t.Fatalf("replayed close appended facts: before=%v after=%v", before, after)
			}
			var replayEncoded string
			var replayWatermark int64
			if err = h.pool.QueryRow(ctx, `SELECT rankings::text,score_watermark FROM referral_campaign_snapshots WHERE campaign_id=$1`, campaign.ID).Scan(&replayEncoded, &replayWatermark); err != nil {
				t.Fatal(err)
			}
			if replayEncoded != encoded || replayWatermark != watermark {
				t.Fatalf("late correction rewrote frozen close snapshot: original=%s replay=%s watermark=%d/%d", encoded, replayEncoded, watermark, replayWatermark)
			}
			assertCount(t, h.pool, `SELECT count(*) FROM referral_campaign_snapshots WHERE campaign_id=$1`, 1, campaign.ID)
		})
	}
}

func TestReview20261009ReferralInviteCloseSnapshotPreservesReversalAndTies(t *testing.T) {
	h := newReferralPostgreSQLHarness(t)
	defer h.cleanup()
	ctx := context.Background()
	campaign, firstTeam, secondTeam := h.createCampaignWithTeams(t, "review-invite-close", 1901, 1902)
	h.joinDirect(t, campaign.ID, firstTeam.ID, 1901, "review-20261009-invite-close-first")
	h.joinDirect(t, campaign.ID, secondTeam.ID, 1902, "review-20261009-invite-close-second")
	firstToken := h.issue(t, campaign.ID, 1901, "review-20261009-invite-close-token-first")
	secondToken := h.issue(t, campaign.ID, 1902, "review-20261009-invite-close-token-second")
	joinedAt := h.clock
	h.joinInvite(t, campaign.ID, 1903, firstToken, "review-20261009-invite-close-join-first")
	h.clock = joinedAt
	h.joinInvite(t, campaign.ID, 1904, secondToken, "review-20261009-invite-close-join-second")
	reversed := h.joinInvite(t, campaign.ID, 1905, firstToken, "review-20261009-invite-close-join-reversed")
	if err := h.admin.ReverseInvitation(ctx, referralport.ReverseInvitationCommand{ActorAdminID: 9001, ParticipationID: reversed.Participation.ID, Reason: "synthetic invitation reversal", IdempotencyKey: "review-20261009-invite-close-reverse"}); err != nil {
		t.Fatal(err)
	}
	board, err := h.service.Leaderboard(ctx, referralport.LeaderboardQuery{CampaignID: campaign.ID, Kind: referralport.LeaderboardPersonal, Period: referralport.LeaderboardTotal, Limit: 100})
	if err != nil || len(board.Items) != 2 || board.Items[0].CustomerID != 1901 || board.Items[0].Score != 1 || board.Items[1].CustomerID != 1902 || board.Items[1].Score != 1 {
		t.Fatalf("source invite board=%+v err=%v", board.Items, err)
	}
	h.clock = campaign.EndsAt.Add(time.Minute)
	if err = h.admin.RunCampaignClose(ctx, campaign.ID); err != nil {
		t.Fatal(err)
	}
	var encoded string
	var watermark int64
	if err = h.pool.QueryRow(ctx, `SELECT rankings::text,score_watermark FROM referral_campaign_snapshots WHERE campaign_id=$1`, campaign.ID).Scan(&encoded, &watermark); err != nil {
		t.Fatal(err)
	}
	var snapshot struct {
		Personal []struct {
			CustomerID int64 `json:"customer_id"`
			Score      int64 `json:"score"`
		} `json:"personal"`
	}
	if err = json.Unmarshal([]byte(encoded), &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Personal) != 2 || watermark != 4 {
		t.Fatalf("invite close snapshot=%s watermark=%d", encoded, watermark)
	}
	for i, row := range board.Items {
		if snapshot.Personal[i].CustomerID != row.CustomerID || snapshot.Personal[i].Score != row.Score {
			t.Errorf("invite snapshot rank %d=%+v want=%+v", i+1, snapshot.Personal[i], row)
		}
	}
}

func review20261009Facts(t *testing.T, h *referralPostgreSQLHarness) [7]int64 {
	t.Helper()
	var counts [7]int64
	if err := h.pool.QueryRow(context.Background(), `SELECT
		(SELECT count(*) FROM referral_participations),
		(SELECT count(*) FROM referral_current_relationships),
		(SELECT count(*) FROM referral_relationship_history),
		(SELECT count(*) FROM referral_score_events),
		(SELECT count(*) FROM referral_operation_receipts),
		(SELECT count(*) FROM audit_events),
		(SELECT count(*) FROM outbox_events)`).Scan(&counts[0], &counts[1], &counts[2], &counts[3], &counts[4], &counts[5], &counts[6]); err != nil {
		t.Fatal(err)
	}
	return counts
}
