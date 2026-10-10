package app

import (
	"context"
	"crypto/sha256"
	"testing"
	"time"

	referraldomain "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/domain"
	referralport "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/port"
)

func TestPostgreSQLPaidRankingThreeMetricsScopesPeriodsAndRefunds(t *testing.T) {
	for _, metric := range []referraldomain.LeaderboardMetric{referraldomain.LeaderboardInvites, referraldomain.LeaderboardSalesAmount, referraldomain.LeaderboardSalesOrders, "sales"} {
		t.Run(string(metric), func(t *testing.T) {
			h := newReferralPostgreSQLHarness(t)
			defer h.cleanup()
			ctx := context.Background()
			c, err := h.admin.CreateCampaign(ctx, referralport.CreateCampaignCommand{ActorAdminID: 9001, Name: "ranking matrix", StartsAt: h.clock.Add(-20 * 24 * time.Hour), EndsAt: h.clock.Add(24 * time.Hour), TeamMode: referraldomain.TeamModeTeam, QualificationMode: referraldomain.QualificationProductPurchase, ProductID: 51, ProductType: referraldomain.ProductTypeStandard, LeaderboardMetric: metric, IdempotencyKey: "matrix-create"})
			if err != nil {
				t.Fatal(err)
			}
			c, err = h.admin.SetCampaignState(ctx, referralport.SetCampaignStateCommand{ActorAdminID: 9001, CampaignID: c.ID, ExpectedVersion: c.Version, Target: referraldomain.CampaignActive, IdempotencyKey: "matrix-active"})
			if err != nil {
				t.Fatal(err)
			}
			team, err := h.admin.CreateTeam(ctx, referralport.CreateTeamCommand{ActorAdminID: 9001, CampaignID: c.ID, CaptainCustomerID: 11, Name: "team one", IdempotencyKey: "matrix-team-one"})
			if err != nil {
				t.Fatal(err)
			}
			other, err := h.admin.CreateTeam(ctx, referralport.CreateTeamCommand{ActorAdminID: 9001, CampaignID: c.ID, CaptainCustomerID: 13, Name: "team two", IdempotencyKey: "matrix-team-two"})
			if err != nil {
				t.Fatal(err)
			}
			var credits []referraldomain.ProductSaleEvent
			for i, f := range []struct{ promoter, buyer, amount, days, team int64 }{{11, 21, 500, -1, team.ID}, {11, 22, 700, 0, team.ID}, {11, 22, 100, 0, team.ID}, {12, 23, 1000, 0, team.ID}, {12, 24, 300, -8, team.ID}, {12, 22, 200, 0, team.ID}, {13, 25, 900, 0, other.ID}} {
				var credit referraldomain.ProductSaleEvent
				if err = h.uow.Within(ctx, func(tx context.Context) error {
					var e error
					credit, e = h.repository.InsertProductSaleEventWithin(tx, referraldomain.ProductSaleEvent{CampaignID: c.ID, OrderID: int64(8300 + i), OrderVersion: 2, ProductID: 51, ProductType: "standard_product", ProductCode: "p-51", ProductName: "Product 51", ProductVersion: 1, TeamID: f.team, PromoterCustomerID: f.promoter, PromotionCredentialRef: "synthetic:paid-ranking", PolicyVersion: 1, BuyerCustomerID: f.buyer, BeneficiaryCustomerID: f.buyer, Kind: referraldomain.ProductSaleCredit, AmountDeltaMinor: f.amount, OrderCountDelta: 1, SourcePaidEventID: int64(9300 + i), Currency: "CNY", SourceDigest: sha256.Sum256([]byte("ranking-" + itoa(int64(i)))), OccurredAt: h.clock.Add(time.Duration(f.days) * 24 * time.Hour)})
					return e
				}); err != nil {
					t.Fatal(err)
				}
				credits = append(credits, credit)
			}
			for _, r := range []struct {
				index          int
				amount, orders int64
			}{{1, -200, 0}, {3, -1000, -1}} {
				correction := credits[r.index]
				correction.ID = 0
				correction.Kind = referraldomain.ProductSaleReversal
				correction.ReversesSaleEventID = credits[r.index].ID
				correction.RefundReceiptKey = "matrix-refund-" + itoa(int64(r.index))
				correction.AmountDeltaMinor, correction.OrderCountDelta = r.amount, r.orders
				correction.OccurredAt = h.clock.Add(25 * time.Hour)
				if err = h.uow.Within(ctx, func(tx context.Context) error {
					_, e := h.repository.InsertProductSaleEventWithin(tx, correction)
					return e
				}); err != nil {
					t.Fatal(err)
				}
			}
			// Corrections made on a later day adjust the original payment window.
			// A single buyer credited to two members still counts once in the team.
			type totals struct{ people, amount, orders int64 }
			for _, period := range []referralport.LeaderboardPeriod{referralport.LeaderboardDay, referralport.LeaderboardWeek, referralport.LeaderboardTotal, "all"} {
				personal := map[int64]totals{11: {2, 1100, 3}, 12: {2, 500, 2}, 13: {1, 900, 1}}
				teamTotal := totals{3, 1600, 5}
				if period == referralport.LeaderboardDay {
					personal[11], personal[12] = totals{1, 600, 2}, totals{1, 200, 1}
					teamTotal = totals{1, 800, 3}
				}
				if period == referralport.LeaderboardWeek {
					personal[12] = totals{1, 200, 1}
					teamTotal = totals{2, 1300, 4}
				}
				for _, kind := range []referralport.LeaderboardKind{referralport.LeaderboardPersonal, referralport.LeaderboardTeam, referralport.LeaderboardInTeam} {
					expected := personal
					teamID := int64(0)
					if kind == referralport.LeaderboardTeam {
						expected = map[int64]totals{team.ID: teamTotal, other.ID: {1, 900, 1}}
					}
					if kind == referralport.LeaderboardInTeam {
						teamID = team.ID
						expected = map[int64]totals{11: personal[11], 12: personal[12]}
					}
					board, e := h.service.Leaderboard(ctx, referralport.LeaderboardQuery{CampaignID: c.ID, Kind: kind, Period: period, TeamID: teamID, Anchor: h.clock, Limit: 100})
					if e != nil {
						t.Fatalf("%s/%s: %v", kind, period, e)
					}
					if len(board.Items) != len(expected) {
						t.Fatalf("%s/%s: %+v", kind, period, board.Items)
					}
					for _, item := range board.Items {
						owner := item.CustomerID
						if kind == referralport.LeaderboardTeam {
							owner = item.TeamID
						}
						want, ok := expected[owner]
						if !ok {
							t.Fatalf("unexpected owner %d", owner)
						}
						score := want.people
						if c.Config().LeaderboardMetric == referraldomain.LeaderboardSalesAmount {
							score = want.amount
						}
						if c.Config().LeaderboardMetric == referraldomain.LeaderboardSalesOrders {
							score = want.orders
						}
						if item.Score != score || item.SalesAmountMinor != want.amount || item.SalesOrderCount != want.orders {
							t.Fatalf("metric=%s %s/%s owner=%d got=%+v want=%+v score=%d", metric, kind, period, owner, item, want, score)
						}
					}
				}
			}
		})
	}
}
