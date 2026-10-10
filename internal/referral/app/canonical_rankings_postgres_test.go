package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"testing"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	referraldomain "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/domain"
	referralport "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/port"
)

type reportingRootsFixture struct {
	calls   []int
	missing bool
}

func TestPostgreSQLCanonicalFreeCloseSnapshotKeepsOneWritableSnapshot(t *testing.T) {
	h := newReferralPostgreSQLHarness(t)
	defer h.cleanup()
	ctx := context.Background()
	c, team, _ := h.createCampaignWithTeams(t, "canonical-free-close", 11, 13)
	h.joinDirect(t, c.ID, team.ID, 11, "canonical-free-owner")
	h.joinDirect(t, c.ID, team.ID, 12, "canonical-free-owner-alias")
	token := h.issue(t, c.ID, 11, "canonical-free-invitation")
	aliasToken := h.issue(t, c.ID, 12, "canonical-free-alias-invitation")
	h.joinInvite(t, c.ID, 1000, token, "canonical-free-buyer-one")
	h.joinInvite(t, c.ID, 1001, aliasToken, "canonical-free-buyer-alias")
	h.joinInvite(t, c.ID, 99999, token, "canonical-free-later-self")
	if err := h.repository.BindCanonicalCustomerRoots(&reportingRootsFixture{}); err != nil {
		t.Fatal(err)
	}
	wrapped, err := platformpostgres.Wrap(h.pool, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	closeUow, err := platformpostgres.NewRepeatableReadUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	if err = h.admin.BindCampaignCloseUnitOfWork(closeUow); err != nil {
		t.Fatal(err)
	}
	var encoded []byte
	var watermark, sourceWatermark int64
	err = closeUow.Within(ctx, func(tx context.Context) error {
		native, e := platformpostgres.RequireTransaction(tx)
		if e != nil {
			return e
		}
		if e = native.QueryRow(tx, `SELECT max(id) FROM referral_score_events WHERE campaign_id=$1`, c.ID).Scan(&sourceWatermark); e != nil {
			return e
		}
		// A second connection commits another legitimate invitation after the
		// reporting snapshot is established. It must not enter the frozen award.
		h.joinInvite(t, c.ID, 1002, token, "canonical-free-concurrent-buyer")
		if _, e = h.repository.InsertCampaignCloseSnapshotWithin(tx, c.ID, c.EndsAt.Add(time.Minute)); e != nil {
			return e
		}
		return native.QueryRow(tx, `SELECT rankings,score_watermark FROM referral_campaign_snapshots WHERE campaign_id=$1`, c.ID).Scan(&encoded, &watermark)
	})
	if err != nil {
		t.Fatal(err)
	}
	var snapshot struct {
		Personal []struct {
			CustomerID int64 `json:"customer_id"`
			Score      int64 `json:"score"`
		} `json:"personal"`
		Team []struct {
			Score int64 `json:"score"`
		} `json:"team"`
	}
	if err = json.Unmarshal(encoded, &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Personal) != 1 || snapshot.Personal[0].CustomerID != 11 || snapshot.Personal[0].Score != 1 || len(snapshot.Team) != 1 || snapshot.Team[0].Score != 1 || watermark != sourceWatermark {
		t.Fatalf("free snapshot disagrees with canonical subjects/source watermark: %s watermark=%d source=%d", encoded, watermark, sourceWatermark)
	}
	live, err := h.service.Leaderboard(ctx, referralport.LeaderboardQuery{CampaignID: c.ID, Kind: referralport.LeaderboardPersonal, Period: referralport.LeaderboardTotal, Limit: 20})
	if err != nil || len(live.Items) != 1 || live.Items[0].Score != 2 {
		t.Fatalf("concurrent committed invitation lost: %+v err=%v", live, err)
	}
	h.clock = c.EndsAt.Add(time.Minute)
	if err = h.admin.RunCampaignClose(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if err = h.admin.RunCampaignClose(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	var after []byte
	if err = h.pool.QueryRow(ctx, `SELECT rankings FROM referral_campaign_snapshots WHERE campaign_id=$1`, c.ID).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if string(after) != string(encoded) {
		t.Fatal("close replay rewrote the frozen snapshot")
	}
	assertCount(t, h.pool, `SELECT count(*) FROM referral_operation_receipts WHERE operation='campaign_close'`, 1)
}

func (r *reportingRootsFixture) CanonicalCustomerRoots(_ context.Context, ids []customerdomain.CustomerID) (map[customerdomain.CustomerID]customerdomain.CustomerID, error) {
	r.calls = append(r.calls, len(ids))
	values := map[customerdomain.CustomerID]customerdomain.CustomerID{}
	for _, id := range ids {
		root := id
		if id == 12 || id == 99999 {
			root = 11
		}
		if id >= 1000 && id < 2000 {
			root = 100000 + (id-1000)/2
		}
		if !r.missing {
			values[id] = root
		}
	}
	return values, nil
}

// A separate in-memory totals model covers more than one Identity batch,
// aliases on both sides, both teams, partial/full refunds, duplicate buyers,
// canonical self-exclusion, pagination and a missing-root failure. Real OneID
// merge/reversal is covered by the Composition journey, not this fixture map.
func TestPostgreSQLCanonicalPaidRankingsMatchModelAcrossBatches(t *testing.T) {
	for _, metric := range []referraldomain.LeaderboardMetric{referraldomain.LeaderboardInvites, referraldomain.LeaderboardSalesAmount, referraldomain.LeaderboardSalesOrders, "sales"} {
		t.Run(string(metric), func(t *testing.T) {
			h := newReferralPostgreSQLHarness(t)
			defer h.cleanup()
			ctx := context.Background()
			c, err := h.admin.CreateCampaign(ctx, referralport.CreateCampaignCommand{ActorAdminID: 9001, Name: "canonical model", StartsAt: h.clock.Add(-time.Hour), EndsAt: h.clock.Add(time.Hour), TeamMode: referraldomain.TeamModeTeam, QualificationMode: referraldomain.QualificationProductPurchase, ProductID: 51, ProductType: referraldomain.ProductTypeStandard, LeaderboardMetric: metric, IdempotencyKey: "canonical-model-create"})
			if err != nil {
				t.Fatal(err)
			}
			team, err := h.admin.CreateTeam(ctx, referralport.CreateTeamCommand{ActorAdminID: 9001, CampaignID: c.ID, CaptainCustomerID: 11, Name: "one", IdempotencyKey: "canonical-model-team-one"})
			if err != nil {
				t.Fatal(err)
			}
			other, err := h.admin.CreateTeam(ctx, referralport.CreateTeamCommand{ActorAdminID: 9001, CampaignID: c.ID, CaptainCustomerID: 13, Name: "two", IdempotencyKey: "canonical-model-team-two"})
			if err != nil {
				t.Fatal(err)
			}
			roots := &reportingRootsFixture{}
			if err = h.repository.BindCanonicalCustomerRoots(roots); err != nil {
				t.Fatal(err)
			}
			type fact struct{ owner, buyer, team, net int64 }
			var facts []fact
			err = h.uow.Within(ctx, func(tx context.Context) error {
				for i := 0; i < 605; i++ {
					owner, teamID := int64(11), team.ID
					if i%2 == 1 {
						owner = 12
					}
					if i%5 == 0 {
						owner, teamID = 13, other.ID
					}
					buyer := int64(1000 + i)
					if i == 604 {
						buyer, owner, teamID = 99999, 11, team.ID
					}
					digest := sha256.Sum256([]byte("model-" + itoa(int64(i))))
					credit, e := h.repository.InsertProductSaleEventWithin(tx, referraldomain.ProductSaleEvent{CampaignID: c.ID, OrderID: 10000 + int64(i), OrderVersion: 2, ProductID: 51, ProductType: "standard_product", ProductCode: "p-51", ProductName: "frozen product", ProductVersion: 1, TeamID: teamID, PromoterCustomerID: owner, PromotionCredentialRef: "synthetic-frozen", PolicyVersion: 1, BuyerCustomerID: buyer, BeneficiaryCustomerID: buyer, Kind: referraldomain.ProductSaleCredit, AmountDeltaMinor: 680, OrderCountDelta: 1, Currency: "CNY", SourcePaidEventID: 20000 + int64(i), SourceDigest: digest, OccurredAt: h.clock})
					if e != nil {
						return e
					}
					net := int64(680)
					if i%11 == 0 || i%7 == 0 {
						reversal := credit
						reversal.ID = 0
						reversal.Kind = referraldomain.ProductSaleReversal
						reversal.ReversesSaleEventID = credit.ID
						reversal.RefundReceiptKey = "model-refund-" + itoa(int64(i))
						reversal.OrderVersion = 3
						reversal.AmountDeltaMinor = -200
						reversal.OrderCountDelta = 0
						if i%11 == 0 {
							reversal.AmountDeltaMinor = -680
							reversal.OrderCountDelta = -1
						}
						reversal.OccurredAt = h.clock.Add(time.Second)
						if _, e = h.repository.InsertProductSaleEventWithin(tx, reversal); e != nil {
							return e
						}
						net += reversal.AmountDeltaMinor
					}
					if owner == 12 {
						owner = 11
					}
					if buyer == 99999 {
						buyer = 11
					} else {
						buyer = 100000 + (buyer-1000)/2
					}
					facts = append(facts, fact{owner, buyer, teamID, net})
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			type total struct {
				buyers         map[int64]bool
				amount, orders int64
			}
			for _, kind := range []referralport.LeaderboardKind{referralport.LeaderboardPersonal, referralport.LeaderboardTeam, referralport.LeaderboardInTeam} {
				expected := map[int64]*total{}
				for _, f := range facts {
					if f.net <= 0 || f.owner == f.buyer || (kind == referralport.LeaderboardInTeam && f.team != team.ID) {
						continue
					}
					key := f.owner
					if kind == referralport.LeaderboardTeam {
						key = f.team
					}
					if expected[key] == nil {
						expected[key] = &total{buyers: map[int64]bool{}}
					}
					v := expected[key]
					v.buyers[f.buyer] = true
					v.amount += f.net
					v.orders++
				}
				for _, period := range []referralport.LeaderboardPeriod{referralport.LeaderboardDay, referralport.LeaderboardWeek, referralport.LeaderboardTotal, "all"} {
					q := referralport.LeaderboardQuery{CampaignID: c.ID, Kind: kind, Period: period, Anchor: h.clock, TeamID: team.ID, ViewerCustomerID: 12, ViewerTeamID: team.ID, Limit: 1}
					seen := map[int64]bool{}
					for {
						page, e := h.service.Leaderboard(ctx, q)
						if e != nil {
							t.Fatal(e)
						}
						if page.MyEntry == nil || (kind != referralport.LeaderboardTeam && page.MyEntry.CustomerID != 11) {
							t.Fatalf("alias own entry missing: %+v", page)
						}
						for _, item := range page.Items {
							key := item.CustomerID
							if kind == referralport.LeaderboardTeam {
								key = item.TeamID
							}
							want := expected[key]
							if want == nil || seen[key] {
								t.Fatalf("unknown or duplicate row %+v", item)
							}
							seen[key] = true
							score := int64(len(want.buyers))
							if metric == referraldomain.LeaderboardSalesAmount || metric == "sales" {
								score = want.amount
							}
							if metric == referraldomain.LeaderboardSalesOrders {
								score = want.orders
							}
							if item.Score != score || item.SalesAmountMinor != want.amount || item.SalesOrderCount != want.orders {
								t.Fatalf("%s/%s row=%+v want score=%d amount=%d orders=%d", kind, period, item, score, want.amount, want.orders)
							}
						}
						if page.NextCursor == "" {
							break
						}
						q.Cursor = page.NextCursor
					}
					if len(seen) != len(expected) {
						t.Fatalf("%s/%s lost rows", kind, period)
					}
				}
			}
			for _, n := range roots.calls {
				if n > 500 {
					t.Fatalf("Identity batch contract exceeded: %d", n)
				}
			}
			if len(roots.calls) < 2 {
				t.Fatal("multi-batch projection not exercised")
			}
			err = h.uow.Within(ctx, func(tx context.Context) error {
				count, e := h.repository.CountPaidInviteesWithin(tx, c.ID, 12)
				if e != nil {
					return e
				}
				unique := map[int64]bool{}
				for _, f := range facts {
					if f.owner == 11 && f.net > 0 && f.owner != f.buyer {
						unique[f.buyer] = true
					}
				}
				if count != int64(len(unique)) {
					t.Fatalf("personal effective count=%d want=%d", count, len(unique))
				}
				items, e := h.repository.ListPaidInviteItemsWithin(tx, c.ID, 12, 0, 101)
				if e != nil {
					return e
				}
				for _, item := range items {
					if item.Participation.CustomerID < 100000 {
						t.Fatalf("detail leaked alias/self %+v", item)
					}
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			// Close-time awards must use the same current subjects and totals,
			// while retaining the original immutable event watermark.
			live, err := h.service.Leaderboard(ctx, referralport.LeaderboardQuery{CampaignID: c.ID, Kind: referralport.LeaderboardPersonal, Period: referralport.LeaderboardTotal, Limit: 100})
			if err != nil {
				t.Fatal(err)
			}
			liveTeams, err := h.service.Leaderboard(ctx, referralport.LeaderboardQuery{CampaignID: c.ID, Kind: referralport.LeaderboardTeam, Period: referralport.LeaderboardTotal, Limit: 100})
			if err != nil {
				t.Fatal(err)
			}
			err = h.uow.Within(ctx, func(tx context.Context) error {
				_, e := h.repository.InsertCampaignCloseSnapshotWithin(tx, c.ID, h.clock.Add(time.Minute))
				return e
			})
			if err != nil {
				t.Fatal(err)
			}
			var encoded []byte
			if err = h.pool.QueryRow(ctx, `SELECT rankings FROM referral_campaign_snapshots WHERE campaign_id=$1`, c.ID).Scan(&encoded); err != nil {
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
			if err = json.Unmarshal(encoded, &snapshot); err != nil {
				t.Fatal(err)
			}
			if len(snapshot.Personal) != len(live.Items) {
				t.Fatalf("canonical close has %d rows, live has %d", len(snapshot.Personal), len(live.Items))
			}
			for i, row := range snapshot.Personal {
				if row.CustomerID != live.Items[i].CustomerID || row.Score != live.Items[i].Score {
					t.Fatalf("close/live disagree: snapshot=%+v live=%+v", snapshot.Personal, live.Items)
				}
			}
			if len(snapshot.Team) != len(liveTeams.Items) {
				t.Fatal("team close lost canonical rows")
			}
			for i, row := range snapshot.Team {
				if row.TeamID != liveTeams.Items[i].TeamID || row.Score != liveTeams.Items[i].Score {
					t.Fatal("team close disagrees with canonical live board")
				}
			}
			roots.missing = true
			if _, e := h.service.Leaderboard(ctx, referralport.LeaderboardQuery{CampaignID: c.ID, Kind: referralport.LeaderboardPersonal, Period: referralport.LeaderboardTotal, Limit: 20}); e == nil {
				t.Fatal("missing identity facts silently produced a board")
			}
		})
	}
}
