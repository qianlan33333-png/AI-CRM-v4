package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	distributionapp "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/app"
	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
	distributionstore "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/store"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	identityquery "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/query"
	identitystore "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/store"
	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	platformoutbox "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/outbox"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	referralapp "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/app"
	referraldomain "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/domain"
	referralport "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/port"
	referralstore "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/store"
)

type sol61ReferralCloseSink struct{}

func (sol61ReferralCloseSink) EnqueueCampaignCloseWithin(context.Context, int64, time.Time) error {
	return nil
}

// Actual OneID provision/merge, browser-session owner, Referral write UoW and
// immutable scores; all verified identity facts and sessions are synthetic.
func TestPostgreSQLReferralSol61MergedCustomerCannotJoinTwice(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()
	native, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer native.Close()
	wrapped, err := platformpostgres.Wrap(native, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := referralstore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	distributions, err := distributionstore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := distributionapp.NewBrowserSessionService(uow, distributions)
	if err != nil {
		t.Fatal(err)
	}
	audit, err := platformaudit.NewService(platformaudit.NewPostgreSQLStore())
	if err != nil {
		t.Fatal(err)
	}
	queries := identityquery.NewPostgreSQL()
	dataKey := base64.RawStdEncoding.EncodeToString(make([]byte, 32))
	service, err := referralapp.NewService(uow, repository, "https://sol61-referral.test", dataKey, sol61ReferralCloseSink{}, audit, platformoutbox.NewPostgreSQL())
	if err != nil {
		t.Fatal(err)
	}
	if err = service.BindIdentitySubjects(queries); err != nil {
		t.Fatal(err)
	}
	admin, err := referralapp.NewAdminService(uow, repository, referralCanonicalCustomerVerifier{resolver: canonicalCustomerAdapter{reader: queries}, identities: queries}, sol61ReferralCloseSink{}, audit, platformoutbox.NewPostgreSQL())
	if err != nil {
		t.Fatal(err)
	}
	oneID := identityapp.OneIDService{Store: identitystore.NewPostgresStore()}
	scope := "wechat-app:sol61-referral"
	loserFact := evidenceMergeFact(t, identitydomain.KindMPOpenID, scope, "sol61-loser-openid")
	survivorFact := evidenceMergeFact(t, identitydomain.KindWeComExternalUserID, "wecom-corp:sol61-referral", "sol61-survivor-external")
	inviterFact := evidenceMergeFact(t, identitydomain.KindMPOpenID, scope, "sol61-inviter-openid")
	var loser, survivor, inviter identityport.ProvisionResult
	if err = uow.Within(ctx, func(tx context.Context) error {
		var e error
		if loser, e = oneID.ProvisionCustomerFromVerifiedIdentity(tx, loserFact); e != nil {
			return e
		}
		if survivor, e = oneID.ProvisionCustomerFromVerifiedIdentity(tx, survivorFact); e != nil {
			return e
		}
		inviter, e = oneID.ProvisionCustomerFromVerifiedIdentity(tx, inviterFact)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	actor := func(subject identityport.ProvisionResult) distributionport.TrustedSessionActor {
		token, _, e := sessions.Issue(ctx, distributionport.TrustedSessionActor{CustomerID: int64(subject.CustomerID), IdentityID: subject.IdentityID, AppID: "sol61-referral", AppScope: scope, Channel: "mini_program", OccurredAt: time.Now().UTC()})
		if e != nil {
			t.Fatal(e)
		}
		result, e := sessions.Resolve(ctx, token)
		if e != nil {
			t.Fatal(e)
		}
		return result
	}
	at := time.Now().UTC()
	campaign, err := admin.CreateCampaign(ctx, referralport.CreateCampaignCommand{ActorAdminID: 9001, Name: "sol61 canonical participation", StartsAt: at.Add(-time.Hour), EndsAt: at.Add(time.Hour), TeamMode: referraldomain.TeamModeIndividual, QualificationMode: referraldomain.QualificationFreeSignup, LeaderboardMetric: referraldomain.LeaderboardInvites, IdempotencyKey: "sol61-merge-campaign"})
	if err != nil {
		t.Fatal(err)
	}
	campaign, err = admin.SetCampaignState(ctx, referralport.SetCampaignStateCommand{ActorAdminID: 9001, CampaignID: campaign.ID, ExpectedVersion: campaign.Version, Target: referraldomain.CampaignActive, IdempotencyKey: "sol61-merge-campaign-active"})
	if err != nil {
		t.Fatal(err)
	}
	inviterActor := actor(inviter)
	if _, err = service.JoinCampaign(ctx, referralport.JoinCampaignCommand{Actor: inviterActor, CampaignID: campaign.ID, IdempotencyKey: "sol61-merge-inviter-join"}); err != nil {
		t.Fatal(err)
	}
	invite, err := service.IssueInvitation(ctx, referralport.IssueInvitationCommand{Actor: inviterActor, CampaignID: campaign.ID, IdempotencyKey: "sol61-merge-invitation"})
	if err != nil {
		t.Fatal(err)
	}
	token := invite.URL[strings.LastIndex(invite.URL, "/")+1:]
	oldActor := actor(loser)
	firstCommand := referralport.JoinCampaignCommand{Actor: oldActor, CampaignID: campaign.ID, InvitationToken: token, IdempotencyKey: "sol61-merge-first-join"}
	first, err := service.JoinCampaign(ctx, firstCommand)
	if err != nil {
		t.Fatal(err)
	}
	oldIssueCommand := referralport.IssueInvitationCommand{Actor: oldActor, CampaignID: campaign.ID, IdempotencyKey: "sol61-merge-old-invitation"}
	oldInvitation, err := service.IssueInvitation(ctx, oldIssueCommand)
	if err != nil {
		t.Fatal(err)
	}
	newCampaign := func(key string) referraldomain.Campaign {
		created, e := admin.CreateCampaign(ctx, referralport.CreateCampaignCommand{ActorAdminID: 9001, Name: key, StartsAt: at.Add(-time.Hour), EndsAt: at.Add(time.Hour), TeamMode: referraldomain.TeamModeIndividual, QualificationMode: referraldomain.QualificationFreeSignup, LeaderboardMetric: referraldomain.LeaderboardInvites, IdempotencyKey: key + "-create"})
		if e != nil {
			t.Fatal(e)
		}
		created, e = admin.SetCampaignState(ctx, referralport.SetCampaignStateCommand{ActorAdminID: 9001, CampaignID: created.ID, ExpectedVersion: created.Version, Target: referraldomain.CampaignActive, IdempotencyKey: key + "-activate"})
		if e != nil {
			t.Fatal(e)
		}
		return created
	}
	ambiguousCampaign := newCampaign("sol61-merge-two-histories")
	for index, subject := range []identityport.ProvisionResult{loser, survivor} {
		key := []string{"sol61-merge-history-a", "sol61-merge-history-b"}[index]
		if _, err = service.JoinCampaign(ctx, referralport.JoinCampaignCommand{Actor: actor(subject), CampaignID: ambiguousCampaign.ID, IdempotencyKey: key}); err != nil {
			t.Fatal(err)
		}
	}
	evidence := identitydomain.LinkEvidence{Type: "test", Strength: identitydomain.EvidenceStrong, Source: "referral.merge.test", EventID: "sol61-referral-merge", Digest: "synthetic-referral-merge", PolicyVersion: "test-v1"}
	var candidate identityapp.LinkResult
	if err = uow.Within(ctx, func(tx context.Context) error {
		var e error
		candidate, e = oneID.LinkVerifiedIdentity(tx, identityapp.LinkCommand{SourceCustomerID: loser.CustomerID, Target: survivorFact, Evidence: evidence})
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if candidate.Candidate == nil {
		t.Fatal("actual merge candidate missing")
	}
	var mergeID int64
	if err = uow.Within(ctx, func(tx context.Context) error {
		merged, e := oneID.ConfirmMerge(tx, identityapp.ConfirmMergeCommand{CandidateID: candidate.Candidate.ID, SurvivorCustomerID: survivor.CustomerID, Operator: "sol61-referral-synthetic-merge"})
		if e != nil {
			return e
		}
		mergeID = merged.Merge.ID
		loser, e = oneID.ProvisionCustomerFromVerifiedIdentity(tx, loserFact)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if loser.CustomerID != survivor.CustomerID {
		t.Fatal("original verified handle did not resolve to actual survivor")
	}
	mergedActor := actor(loser)
	second, secondErr := service.JoinCampaign(ctx, referralport.JoinCampaignCommand{Actor: mergedActor, CampaignID: campaign.ID, InvitationToken: token, IdempotencyKey: "sol61-merge-second-join"})
	var participants, credits int
	ids := []int64{first.Participation.CustomerID, int64(survivor.CustomerID)}
	if err = native.QueryRow(ctx, `SELECT (SELECT count(*) FROM referral_participations WHERE campaign_id=$1 AND customer_id=ANY($2::bigint[])),(SELECT count(*) FROM referral_score_events WHERE campaign_id=$1 AND inviter_customer_id=$3 AND kind='credit')`, campaign.ID, ids, int64(inviter.CustomerID)).Scan(&participants, &credits); err != nil {
		t.Fatal(err)
	}
	if secondErr != nil || second.Participation == nil || second.Participation.ID != first.Participation.ID || participants != 1 || credits != 1 {
		t.Fatalf("same verified human after real merge received a second participation/credit: first=%+v second=%+v err=%v rows=%d credits=%d", first.Participation, second.Participation, secondErr, participants, credits)
	}
	facts := func() [6]int64 {
		var counts [6]int64
		if e := native.QueryRow(ctx, `SELECT (SELECT count(*) FROM referral_participations),(SELECT count(*) FROM referral_score_events),(SELECT count(*) FROM referral_operation_receipts),(SELECT count(*) FROM referral_invitations),(SELECT count(*) FROM audit_events),(SELECT count(*) FROM outbox_events)`).Scan(&counts[0], &counts[1], &counts[2], &counts[3], &counts[4], &counts[5]); e != nil {
			t.Fatal(e)
		}
		return counts
	}
	t.Run("historical_receipt_and_token_replay_preserve_original_scope", func(t *testing.T) {
		before := facts()
		firstCommand.Actor = mergedActor
		replay, e := service.JoinCampaign(ctx, firstCommand)
		if e != nil || replay.Participation == nil || replay.Participation.ID != first.Participation.ID {
			t.Fatalf("historical receipt replay=%+v err=%v", replay.Participation, e)
		}
		oldIssueCommand.Actor = mergedActor
		issued, e := service.IssueInvitation(ctx, oldIssueCommand)
		if e != nil || issued.URL != oldInvitation.URL || !issued.ExpiresAt.Equal(oldInvitation.ExpiresAt) {
			t.Fatalf("historical invitation replay changed token: %+v err=%v", issued, e)
		}
		if issued.InviterCanonicalCustomerID != int64(survivor.CustomerID) {
			t.Fatalf("poster presentation used historical alias after merge: %d", issued.InviterCanonicalCustomerID)
		}
		changed := firstCommand
		changed.TeamID = 1
		if _, e = service.JoinCampaign(ctx, changed); !errors.Is(e, referralport.ErrConflict) {
			t.Fatalf("changed receipt payload accepted: %v", e)
		}
		if after := facts(); after != before {
			t.Fatalf("replay or conflict appended facts: before=%v after=%v", before, after)
		}
	})
	t.Run("old_and_new_sessions_read_same_historical_participation", func(t *testing.T) {
		for _, sessionActor := range []distributionport.TrustedSessionActor{oldActor, mergedActor} {
			mine, e := service.MyCampaign(ctx, sessionActor, campaign.ID)
			if e != nil || mine.Participation == nil || mine.Participation.ID != first.Participation.ID || mine.Participation.CustomerID != first.Participation.CustomerID {
				t.Fatalf("own immutable participation=%+v err=%v", mine.Participation, e)
			}
			if _, e = service.ListMyInvites(ctx, sessionActor, campaign.ID, "", 10); e != nil {
				t.Fatal(e)
			}
		}
		before := facts()
		foreignCommand := firstCommand
		foreignCommand.Actor = inviterActor
		if _, e := service.JoinCampaign(ctx, foreignCommand); !errors.Is(e, referralport.ErrInvitationInvalid) {
			t.Fatalf("third subject replayed another lineage's receipt: %v", e)
		}
		if after := facts(); after != before {
			t.Fatalf("third subject appended facts: before=%v after=%v", before, after)
		}
	})
	t.Run("merged_alias_cannot_invite_itself", func(t *testing.T) {
		before := facts()
		ownToken := oldInvitation.URL[strings.LastIndex(oldInvitation.URL, "/")+1:]
		if _, e := service.JoinCampaign(ctx, referralport.JoinCampaignCommand{Actor: mergedActor, CampaignID: campaign.ID, InvitationToken: ownToken, IdempotencyKey: "sol61-merge-own-alias-invite"}); !errors.Is(e, referralport.ErrInvitationInvalid) {
			t.Fatalf("same current lineage accepted its frozen alias invitation: %v", e)
		}
		if after := facts(); after != before {
			t.Fatalf("self invitation appended facts: before=%v after=%v", before, after)
		}
	})
	t.Run("new_invitation_retains_unique_historical_participant", func(t *testing.T) {
		issued, e := service.IssueInvitation(ctx, referralport.IssueInvitationCommand{Actor: mergedActor, CampaignID: campaign.ID, IdempotencyKey: "sol61-merge-new-invitation"})
		if e != nil {
			t.Fatal(e)
		}
		if issued.InviterCanonicalCustomerID != int64(survivor.CustomerID) {
			t.Fatalf("new poster presentation used historical alias: %d", issued.InviterCanonicalCustomerID)
		}
		preview, e := service.PreviewInvitation(ctx, issued.URL[strings.LastIndex(issued.URL, "/")+1:])
		if e != nil || preview.InviterCustomerID != first.Participation.CustomerID {
			t.Fatalf("new invitation abandoned its historical participation: %+v err=%v", preview, e)
		}
	})
	t.Run("two_historical_participations_require_business_resolution", func(t *testing.T) {
		before := facts()
		if _, e := service.JoinCampaign(ctx, referralport.JoinCampaignCommand{Actor: mergedActor, CampaignID: ambiguousCampaign.ID, IdempotencyKey: "sol61-merge-ambiguous-new"}); !errors.Is(e, referralport.ErrConflict) {
			t.Fatalf("ambiguous join=%v", e)
		}
		if _, e := service.MyCampaign(ctx, mergedActor, ambiguousCampaign.ID); !errors.Is(e, referralport.ErrConflict) {
			t.Fatalf("ambiguous read=%v", e)
		}
		if _, e := service.IssueInvitation(ctx, referralport.IssueInvitationCommand{Actor: mergedActor, CampaignID: ambiguousCampaign.ID, IdempotencyKey: "sol61-merge-ambiguous-invite"}); !errors.Is(e, referralport.ErrConflict) {
			t.Fatalf("ambiguous invitation=%v", e)
		}
		if after := facts(); after != before {
			t.Fatalf("ambiguity appended/deleted facts: before=%v after=%v", before, after)
		}
	})
	t.Run("free_rank_counter_and_detail_share_canonical_promoter", func(t *testing.T) {
		var extra identityport.ProvisionResult
		if e := uow.Within(ctx, func(tx context.Context) error {
			var e error
			extra, e = oneID.ProvisionCustomerFromVerifiedIdentity(tx, evidenceMergeFact(t, identitydomain.KindMPOpenID, scope, "sol61-referral-extra"))
			return e
		}); e != nil {
			t.Fatal(e)
		}
		ownToken := oldInvitation.URL[strings.LastIndex(oldInvitation.URL, "/")+1:]
		if _, e := service.JoinCampaign(ctx, referralport.JoinCampaignCommand{Actor: actor(extra), CampaignID: campaign.ID, InvitationToken: ownToken, IdempotencyKey: "canonical-free-extra-join"}); e != nil {
			t.Fatal(e)
		}
		for _, sessionActor := range []distributionport.TrustedSessionActor{oldActor, mergedActor} {
			mine, e := service.MyCampaign(ctx, sessionActor, campaign.ID)
			if e != nil || mine.DirectInvitationCount != 1 || mine.PersonalTotalScore != 1 || mine.PersonalRank < 1 {
				t.Fatalf("canonical free own summary=%+v err=%v", mine, e)
			}
			items, e := service.ListMyInvites(ctx, sessionActor, campaign.ID, "", 10)
			if e != nil || len(items.Items) != 1 || items.Items[0].Participation.InviterCustomerID != int64(survivor.CustomerID) {
				t.Fatalf("canonical free detail=%+v err=%v", items, e)
			}
		}
		board, e := service.Leaderboard(ctx, referralport.LeaderboardQuery{CampaignID: campaign.ID, Kind: referralport.LeaderboardPersonal, Period: referralport.LeaderboardTotal, ViewerCustomerID: first.Participation.CustomerID, Limit: 20})
		if e != nil || board.MyEntry == nil || board.MyEntry.CustomerID != int64(survivor.CustomerID) || board.MyEntry.Score != 1 {
			t.Fatalf("canonical free rank=%+v err=%v", board, e)
		}
	})
	t.Run("paid_rankings_use_current_subjects_without_rewriting_facts", func(t *testing.T) {
		for _, metric := range []referraldomain.LeaderboardMetric{referraldomain.LeaderboardInvites, referraldomain.LeaderboardSalesAmount, referraldomain.LeaderboardSalesOrders} {
			paid, e := admin.CreateCampaign(ctx, referralport.CreateCampaignCommand{ActorAdminID: 9001, Name: "merged paid ranking " + string(metric), StartsAt: at.Add(-time.Hour), EndsAt: at.Add(time.Hour), TeamMode: referraldomain.TeamModeIndividual, QualificationMode: referraldomain.QualificationProductPurchase, ProductID: 51, ProductType: referraldomain.ProductTypeStandard, LeaderboardMetric: metric, IdempotencyKey: "merged-ranking-" + string(metric)})
			if e != nil {
				t.Fatal(e)
			}
			oldID, rootID, otherID := first.Participation.CustomerID, int64(survivor.CustomerID), int64(inviter.CustomerID)
			for i, pair := range [][2]int64{{otherID, oldID}, {otherID, rootID}, {oldID, otherID}, {rootID, otherID}, {oldID, rootID}} {
				digest := sha256.Sum256([]byte("merged-paid-ranking-" + string(metric) + "-" + strconv.Itoa(i)))
				if e = uow.Within(ctx, func(tx context.Context) error {
					_, e := repository.InsertProductSaleEventWithin(tx, referraldomain.ProductSaleEvent{CampaignID: paid.ID, OrderID: paid.ID*100 + int64(i) + 1, OrderVersion: 2, ProductID: 51, ProductType: "standard_product", ProductCode: "p-51", ProductName: "original", ProductVersion: 1, PromoterCustomerID: pair[0], PromotionCredentialRef: "frozen-synthetic", PolicyVersion: 1, BuyerCustomerID: pair[1], BeneficiaryCustomerID: pair[1], Kind: referraldomain.ProductSaleCredit, AmountDeltaMinor: 680, OrderCountDelta: 1, Currency: "CNY", SourcePaidEventID: paid.ID*100 + int64(i) + 1, SourceDigest: digest, OccurredAt: at})
					return e
				}); e != nil {
					t.Fatal(e)
				}
			}
			for _, period := range []referralport.LeaderboardPeriod{referralport.LeaderboardDay, referralport.LeaderboardWeek, referralport.LeaderboardTotal} {
				board, e := service.Leaderboard(ctx, referralport.LeaderboardQuery{CampaignID: paid.ID, Kind: referralport.LeaderboardPersonal, Period: period, Anchor: at, ViewerCustomerID: oldID, Limit: 20})
				want := int64(1)
				if metric == referraldomain.LeaderboardSalesAmount {
					want = 1360
				}
				if metric == referraldomain.LeaderboardSalesOrders {
					want = 2
				}
				if e != nil || len(board.Items) != 2 || board.MyEntry == nil || board.MyEntry.CustomerID != rootID {
					t.Fatalf("metric=%s period=%s canonical board=%+v err=%v", metric, period, board, e)
				}
				for _, item := range board.Items {
					if item.Score != want || item.SalesAmountMinor != 1360 || item.SalesOrderCount != 2 {
						t.Fatalf("metric=%s canonical entry=%+v want=%d", metric, item, want)
					}
				}
			}
			var count int
			if e = native.QueryRow(ctx, "SELECT count(*) FROM referral_product_sale_events WHERE campaign_id=$1", paid.ID).Scan(&count); e != nil || count != 5 {
				t.Fatalf("immutable history count=%d err=%v", count, e)
			}
		}
	})
	t.Run("merge_period_session_follows_actual_reversal", func(t *testing.T) {
		if e := uow.Within(ctx, func(tx context.Context) error { _, e := oneID.RevertConfirmedMerge(tx, mergeID); return e }); e != nil {
			t.Fatal(e)
		}
		mine, e := service.MyCampaign(ctx, mergedActor, campaign.ID)
		if e != nil || mine.Participation == nil || mine.Participation.ID != first.Participation.ID {
			t.Fatalf("merge-period session failed current handle after reversal: %+v err=%v", mine.Participation, e)
		}
		freshCampaign := newCampaign("sol61-revert-current-root")
		joined, e := service.JoinCampaign(ctx, referralport.JoinCampaignCommand{Actor: mergedActor, CampaignID: freshCampaign.ID, IdempotencyKey: "sol61-revert-new-join"})
		if e != nil || joined.Participation == nil || joined.Participation.CustomerID != first.Participation.CustomerID {
			t.Fatalf("new fact used stale session owner after reversal: %+v err=%v", joined.Participation, e)
		}
	})
	t.Run("withdrawn_verified_handle_rejects_all_actor_entries", func(t *testing.T) {
		if e := uow.Within(ctx, func(tx context.Context) error {
			db, e := platformpostgres.RequireTransaction(tx)
			if e != nil {
				return e
			}
			_, e = db.Exec(tx, `UPDATE customer_identities SET status='retired',version=version+1 WHERE id=$1`, mergedActor.IdentityID)
			return e
		}); e != nil {
			t.Fatal(e)
		}
		before := facts()
		_, joinErr := service.JoinCampaign(ctx, referralport.JoinCampaignCommand{Actor: mergedActor, CampaignID: campaign.ID, IdempotencyKey: "sol61-withdrawn-join"})
		_, readErr := service.MyCampaign(ctx, mergedActor, campaign.ID)
		_, listErr := service.ListMyInvites(ctx, mergedActor, campaign.ID, "", 10)
		_, inviteErr := service.IssueInvitation(ctx, referralport.IssueInvitationCommand{Actor: mergedActor, CampaignID: campaign.ID, IdempotencyKey: "sol61-withdrawn-invite"})
		_, contextErr := service.IssueProductActivityContext(ctx, mergedActor, campaign.ID, "sol61-withdrawn-context")
		for _, e := range []error{joinErr, readErr, listErr, inviteErr, contextErr} {
			if !errors.Is(e, referralport.ErrUnauthorized) {
				t.Errorf("withdrawn handle entry=%v", e)
			}
		}
		if after := facts(); after != before {
			t.Fatalf("withdrawn handle appended facts: before=%v after=%v", before, after)
		}
	})
}
