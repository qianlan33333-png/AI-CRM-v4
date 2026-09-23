package app

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformoutbox "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/outbox"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	referraldomain "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/domain"
	referralport "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/port"
	referralstore "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/store"
)

func TestPostgreSQLReferralAcceptsAndFreezesActivityFacts(t *testing.T) {
	h := newReferralPostgreSQLHarness(t)
	defer h.cleanup()

	campaign, teamOne, _ := h.createCampaignWithTeams(t, "增长活动一", 101, 201)
	h.joinDirect(t, campaign.ID, teamOne.ID, 101, "join-captain-101")
	inviteA := h.issue(t, campaign.ID, 101, "issue-a-101")

	joinedB := h.joinInvite(t, campaign.ID, 102, inviteA, "join-b-from-a")
	if joinedB.Participation == nil || joinedB.Participation.InviterCustomerID != 101 || joinedB.Participation.TeamID != teamOne.ID {
		t.Fatalf("B participation did not freeze inviter and team: %+v", joinedB.Participation)
	}
	firstRelationship, found, err := h.service.RelationshipAt(context.Background(), 102, h.now)
	if err != nil || !found || firstRelationship.ReferrerCustomerID != 101 || firstRelationship.SourceCampaignID != campaign.ID {
		t.Fatalf("initial relationship=%+v found=%t err=%v", firstRelationship, found, err)
	}

	inviteB := h.issue(t, campaign.ID, 102, "issue-b-102")
	h.joinInvite(t, campaign.ID, 103, inviteB, "join-c-from-b")
	mineA, err := h.service.MyCampaign(context.Background(), referralActor(101, h.clock), campaign.ID)
	if err != nil || mineA.DirectInvitationCount != 1 {
		t.Fatalf("my direct invitation count=%d err=%v", mineA.DirectInvitationCount, err)
	}

	personal, err := h.service.Leaderboard(context.Background(), referralport.LeaderboardQuery{CampaignID: campaign.ID, Kind: referralport.LeaderboardPersonal, Period: referralport.LeaderboardTotal, ViewerCustomerID: 101, Limit: 20})
	if err != nil || len(personal.Items) != 2 || personal.Items[0].CustomerID != 101 || personal.Items[0].Score != 1 || personal.Items[1].CustomerID != 102 || personal.Items[1].Score != 1 {
		t.Fatalf("personal board=%+v err=%v", personal, err)
	}
	for _, period := range []referralport.LeaderboardPeriod{referralport.LeaderboardDay, referralport.LeaderboardWeek} {
		periodBoard, periodErr := h.service.Leaderboard(context.Background(), referralport.LeaderboardQuery{CampaignID: campaign.ID, Kind: referralport.LeaderboardPersonal, Period: period, Anchor: h.clock, ViewerCustomerID: 101, Limit: 20})
		if periodErr != nil || len(periodBoard.Items) != 2 || periodBoard.Items[0].CustomerID != 101 || periodBoard.Items[1].CustomerID != 102 {
			t.Fatalf("%s board=%+v err=%v", period, periodBoard, periodErr)
		}
	}
	teamBoard, err := h.service.Leaderboard(context.Background(), referralport.LeaderboardQuery{CampaignID: campaign.ID, Kind: referralport.LeaderboardTeam, Period: referralport.LeaderboardTotal, ViewerTeamID: teamOne.ID, Limit: 20})
	if err != nil || len(teamBoard.Items) != 1 || teamBoard.Items[0].TeamID != teamOne.ID || teamBoard.Items[0].Score != 2 || teamBoard.MyEntry == nil {
		t.Fatalf("team board=%+v err=%v", teamBoard, err)
	}
	inTeam, err := h.service.Leaderboard(context.Background(), referralport.LeaderboardQuery{CampaignID: campaign.ID, Kind: referralport.LeaderboardInTeam, Period: referralport.LeaderboardTotal, TeamID: teamOne.ID, ViewerCustomerID: 102, Limit: 20})
	if err != nil || len(inTeam.Items) != 2 || inTeam.MyEntry == nil || inTeam.MyEntry.CustomerID != 102 {
		t.Fatalf("in-team board=%+v err=%v", inTeam, err)
	}
	adminReferrals, err := h.admin.ListAdminReferrals(context.Background(), campaign.ID, "", 20)
	if err != nil || len(adminReferrals.Items) != 3 {
		t.Fatalf("admin referrals=%+v err=%v", adminReferrals, err)
	}
	for _, item := range adminReferrals.Items {
		if item.Participation.CustomerID == 101 && (item.Participation.InviterCustomerID != 0 || item.ScoreState != "none") {
			t.Fatalf("direct captain referral exposed a fake inviter or reversed state: %+v", item)
		}
	}

	var creditA int64
	if err = h.pool.QueryRow(context.Background(), `SELECT id FROM referral_score_events WHERE campaign_id=$1 AND inviter_customer_id=101 AND kind='credit'`, campaign.ID).Scan(&creditA); err != nil {
		t.Fatal(err)
	}
	h.clock = h.clock.Add(time.Minute)
	reward, err := h.admin.RecordReward(context.Background(), referralport.RecordRewardCommand{CampaignID: campaign.ID, CustomerID: 101, ScoreEventID: creditA, Period: "total", Reward: "首名礼品", EvidenceReference: "manual:receipt-1", ActorAdminID: 9001, IdempotencyKey: "reward-a-101"})
	if err != nil || reward.ID < 1 {
		t.Fatalf("record reward=%+v err=%v", reward, err)
	}
	if _, err = h.admin.RecordReward(context.Background(), referralport.RecordRewardCommand{CampaignID: campaign.ID, CustomerID: 101, ScoreEventID: creditA, Period: "total", Reward: "首名礼品", EvidenceReference: "manual:receipt-1", ActorAdminID: 9001, IdempotencyKey: "reward-a-101-fresh-key"}); !errors.Is(err, referralport.ErrConflict) {
		t.Fatalf("fresh-key duplicate reward err=%v", err)
	}

	h.clock = h.clock.Add(time.Minute)
	if err = h.admin.ReverseInvitation(context.Background(), referralport.ReverseInvitationCommand{ParticipationID: joinedB.Participation.ID, Reason: "作弊核实", ActorAdminID: 9001, IdempotencyKey: "reverse-b-102"}); err != nil {
		t.Fatal(err)
	}
	personal, err = h.service.Leaderboard(context.Background(), referralport.LeaderboardQuery{CampaignID: campaign.ID, Kind: referralport.LeaderboardPersonal, Period: referralport.LeaderboardTotal, ViewerCustomerID: 101, Limit: 20})
	if err != nil || len(personal.Items) != 1 || personal.Items[0].CustomerID != 102 || personal.MyEntry != nil {
		t.Fatalf("reversal did not correct original credit board=%+v err=%v", personal, err)
	}
	dayAfterReversal, err := h.service.Leaderboard(context.Background(), referralport.LeaderboardQuery{CampaignID: campaign.ID, Kind: referralport.LeaderboardPersonal, Period: referralport.LeaderboardDay, Anchor: h.clock, ViewerCustomerID: 101, Limit: 20})
	if err != nil || len(dayAfterReversal.Items) != 1 || dayAfterReversal.Items[0].CustomerID != 102 {
		t.Fatalf("reversal did not correct original day board=%+v err=%v", dayAfterReversal, err)
	}
	adminView, err := h.admin.ReadAdminCampaign(context.Background(), campaign.ID)
	if err != nil || adminView.InvitationCount != 1 || len(adminView.DailyMetrics) != 1 || adminView.DailyMetrics[0].InviteCount != 1 {
		t.Fatalf("reversal did not correct campaign counts view=%+v err=%v", adminView, err)
	}
	rewards, err := h.admin.ListRewards(context.Background(), campaign.ID, "", 20)
	if err != nil || len(rewards.Items) != 1 || rewards.Items[0].State != referraldomain.RewardNeedsReview {
		t.Fatalf("reversal did not flag recorded reward rewards=%+v err=%v", rewards, err)
	}
	if _, err = h.admin.RecordReward(context.Background(), referralport.RecordRewardCommand{CampaignID: campaign.ID, CustomerID: 101, ScoreEventID: creditA, Period: "total", Reward: "撤销后奖励", EvidenceReference: "manual:receipt-2", ActorAdminID: 9001, IdempotencyKey: "reward-after-reversal"}); !errors.Is(err, referralport.ErrConflict) {
		t.Fatalf("reversed score event accepted a new reward err=%v", err)
	}

	// A second campaign changes only the current relationship. Campaign one's
	// invitation and score remain immutable even after B accepts the new invite.
	h.clock = h.clock.Add(time.Minute)
	campaignTwo, _, teamTwo := h.createCampaignWithTeams(t, "增长活动二", 301, 201)
	h.joinDirect(t, campaignTwo.ID, teamTwo.ID, 201, "join-captain-201")
	inviteC := h.issue(t, campaignTwo.ID, 201, "issue-c-201")
	h.clock = h.clock.Add(time.Minute)
	h.joinInvite(t, campaignTwo.ID, 102, inviteC, "join-b-from-c")
	current, found, err := h.service.CurrentRelationship(context.Background(), 102)
	if err != nil || !found || current.ReferrerCustomerID != 201 || current.SourceCampaignID != campaignTwo.ID || current.Version != 2 {
		t.Fatalf("current relation was not last accepted fact: %+v found=%t err=%v", current, found, err)
	}
	prior, found, err := h.service.RelationshipAt(context.Background(), 102, h.now.Add(-time.Second))
	if err != nil || !found || prior.ReferrerCustomerID != 101 || prior.SourceCampaignID != campaign.ID {
		t.Fatalf("historical relation changed retroactively: %+v found=%t err=%v", prior, found, err)
	}
	var originalInviter int64
	if err = h.pool.QueryRow(context.Background(), `SELECT inviter_customer_id FROM referral_participations WHERE campaign_id=$1 AND customer_id=102`, campaign.ID).Scan(&originalInviter); err != nil || originalInviter != 101 {
		t.Fatalf("original participation was rewritten inviter=%d err=%v", originalInviter, err)
	}
	if _, err = h.service.JoinCampaign(context.Background(), referralport.JoinCampaignCommand{Actor: referralActor(102, h.clock), CampaignID: campaign.ID, TeamID: teamOne.ID, IdempotencyKey: "join-b-from-a"}); !errors.Is(err, referralport.ErrConflict) {
		t.Fatalf("same key with a different join payload err=%v", err)
	}
	campaignThree, teamThree, _ := h.createCampaignWithTeams(t, "增长活动三", 401, 402)
	h.joinDirect(t, campaignThree.ID, teamThree.ID, 102, "join-b-without-invite")
	current, found, err = h.service.CurrentRelationship(context.Background(), 102)
	if err != nil || !found || current.ReferrerCustomerID != 201 || current.SourceCampaignID != campaignTwo.ID {
		t.Fatalf("direct participation changed current relation=%+v found=%t err=%v", current, found, err)
	}

	// Once an activity is over, an already-participating member can retry with
	// a fresh idempotency key and receives the immutable original result.
	h.clock = campaign.EndsAt.Add(time.Minute)
	replay, err := h.service.JoinCampaign(context.Background(), referralport.JoinCampaignCommand{Actor: referralActor(102, h.now), CampaignID: campaign.ID, TeamID: teamOne.ID, IdempotencyKey: "replay-after-close-102"})
	if err != nil || replay.Participation == nil || replay.Participation.ID != joinedB.Participation.ID {
		t.Fatalf("post-close repeat did not replay participation=%+v err=%v", replay.Participation, err)
	}

	if err = h.admin.RunCampaignClose(context.Background(), campaign.ID); err != nil {
		t.Fatal(err)
	}
	var state string
	var snapshot map[string]any
	if err = h.pool.QueryRow(context.Background(), `SELECT c.state,s.rankings FROM referral_campaigns c JOIN referral_campaign_snapshots s ON s.campaign_id=c.id WHERE c.id=$1`, campaign.ID).Scan(&state, &snapshot); err != nil || state != string(referraldomain.CampaignEnded) {
		t.Fatalf("close snapshot state=%q snapshot=%v err=%v", state, snapshot, err)
	}
	if _, ok := snapshot["personal"]; !ok {
		t.Fatalf("close snapshot missing ranked personal payload: %v", snapshot)
	}
	if err = h.admin.RunCampaignClose(context.Background(), campaign.ID); err != nil {
		t.Fatalf("replayed close=%v", err)
	}
	var snapshots int
	if err = h.pool.QueryRow(context.Background(), `SELECT count(*) FROM referral_campaign_snapshots WHERE campaign_id=$1`, campaign.ID).Scan(&snapshots); err != nil || snapshots != 1 {
		t.Fatalf("close made duplicate snapshot count=%d err=%v", snapshots, err)
	}
}

func TestPostgreSQLReferralUnjoinedCaptainHasNoInvitationDetailsUntilExplicitJoin(t *testing.T) {
	h := newReferralPostgreSQLHarness(t)
	defer h.cleanup()

	campaign, captainTeam, _ := h.createCampaignWithTeams(t, "队长确认活动", 611, 612)
	actor := referralActor(611, h.clock)
	testKey := "test-key"
	mine, err := h.service.MyCampaign(context.Background(), actor, campaign.ID)
	if err != nil || !mine.IsCaptain || mine.CaptainTeam == nil || mine.CaptainTeam.ID != captainTeam.ID || mine.Participation != nil || mine.InvitationAvailable {
		t.Fatalf("unjoined captain view=%+v err=%v", mine, err)
	}
	page, err := h.service.ListMyInvites(context.Background(), actor, campaign.ID, "", 50)
	if err != nil || len(page.Items) != 0 || page.NextCursor != "" {
		t.Fatalf("unjoined captain invitation details=%+v err=%v", page, err)
	}
	assertCount(t, h.pool, `SELECT count(*) FROM referral_participations WHERE campaign_id=$1 AND customer_id=$2`, 0, campaign.ID, 611)
	assertCount(t, h.pool, `SELECT count(*) FROM referral_relationship_history WHERE customer_id=$1`, 0, 611)
	if _, err = h.service.IssueInvitation(context.Background(), referralport.IssueInvitationCommand{Actor: actor, CampaignID: campaign.ID, IdempotencyKey: testKey}); !errors.Is(err, referralport.ErrParticipationRequired) {
		t.Fatalf("unjoined captain issue invitation err=%v", err)
	}

	joined, err := h.service.JoinCampaign(context.Background(), referralport.JoinCampaignCommand{Actor: actor, CampaignID: campaign.ID, TeamID: captainTeam.ID, IdempotencyKey: testKey})
	if err != nil || joined.Participation == nil || joined.Participation.TeamID != captainTeam.ID || !joined.InvitationAvailable {
		t.Fatalf("captain explicit join=%+v err=%v", joined, err)
	}
	page, err = h.service.ListMyInvites(context.Background(), actor, campaign.ID, "", 50)
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("joined captain invitation details=%+v err=%v", page, err)
	}
	if _, err = h.service.IssueInvitation(context.Background(), referralport.IssueInvitationCommand{Actor: actor, CampaignID: campaign.ID, IdempotencyKey: "captain-issue-611"}); err != nil {
		t.Fatalf("joined captain should issue invitation: %v", err)
	}
}

func TestPostgreSQLReferralConcurrentAcceptsPreserveOneParticipationAndLastRelation(t *testing.T) {
	h := newReferralPostgreSQLHarness(t)
	defer h.cleanup()

	campaignOne, teamOne, _ := h.createCampaignWithTeams(t, "并发活动一", 401, 402)
	h.joinDirect(t, campaignOne.ID, teamOne.ID, 401, "join-captain-401")
	inviteOne := h.issue(t, campaignOne.ID, 401, "issue-401")

	// Two accepts for the same activity use different receipt keys. The stable
	// participation lock must still leave one participation and one score.
	commands := []referralport.JoinCampaignCommand{
		{Actor: referralActor(499, h.now), CampaignID: campaignOne.ID, InvitationToken: inviteOne, IdempotencyKey: "concurrent-join-499-a"},
		{Actor: referralActor(499, h.now), CampaignID: campaignOne.ID, InvitationToken: inviteOne, IdempotencyKey: "concurrent-join-499-b"},
	}
	results := runConcurrentJoins(t, h.service, commands)
	if results[0].participationID < 1 || results[0].participationID != results[1].participationID || results[0].err != nil || results[1].err != nil {
		t.Fatalf("same campaign concurrent results=%+v", results)
	}
	assertCount(t, h.pool, `SELECT count(*) FROM referral_participations WHERE campaign_id=$1 AND customer_id=499`, 1, campaignOne.ID)
	assertCount(t, h.pool, `SELECT count(*) FROM referral_score_events WHERE campaign_id=$1 AND participation_id=$2 AND kind='credit'`, 1, campaignOne.ID, results[0].participationID)

	// Separate campaigns can proceed until they serialize on B's stable current
	// relationship lock. No update is lost: history has two versions and the
	// current row agrees with the final committed history version.
	campaignTwo, teamTwo, _ := h.createCampaignWithTeams(t, "并发活动二", 501, 502)
	h.joinDirect(t, campaignTwo.ID, teamTwo.ID, 501, "join-captain-501")
	inviteTwo := h.issue(t, campaignTwo.ID, 501, "issue-501")
	cross := runConcurrentJoins(t, h.service, []referralport.JoinCampaignCommand{
		{Actor: referralActor(599, h.now), CampaignID: campaignOne.ID, InvitationToken: inviteOne, IdempotencyKey: "cross-join-599-one"},
		{Actor: referralActor(599, h.now), CampaignID: campaignTwo.ID, InvitationToken: inviteTwo, IdempotencyKey: "cross-join-599-two"},
	})
	if cross[0].err != nil || cross[1].err != nil {
		t.Fatalf("cross campaign joins=%+v", cross)
	}
	current, found, err := h.service.CurrentRelationship(context.Background(), 599)
	if err != nil || !found || current.Version != 2 {
		t.Fatalf("concurrent current relation=%+v found=%t err=%v", current, found, err)
	}
	var historyVersion int64
	var historySource int64
	if err = h.pool.QueryRow(context.Background(), `SELECT version,source_campaign_id FROM referral_relationship_history WHERE customer_id=599 ORDER BY version DESC LIMIT 1`).Scan(&historyVersion, &historySource); err != nil || historyVersion != 2 || historySource != current.SourceCampaignID {
		t.Fatalf("history/current mismatch version=%d source=%d current=%+v err=%v", historyVersion, historySource, current, err)
	}
	assertCount(t, h.pool, `SELECT count(*) FROM referral_relationship_history WHERE customer_id=599`, 2)
}

func TestPostgreSQLReferralInviterReversalWinsBeforeBlockedNewAcceptance(t *testing.T) {
	h := newReferralPostgreSQLHarness(t)
	defer h.cleanup()
	campaign, team, _ := h.createCampaignWithTeams(t, "撤销并发活动", 701, 799)
	h.joinDirect(t, campaign.ID, team.ID, 701, "join-captain-701")
	inviteA := h.issue(t, campaign.ID, 701, "issue-701")
	joinedB := h.joinInvite(t, campaign.ID, 702, inviteA, "join-702-from-701")
	inviteB := h.issue(t, campaign.ID, 702, "issue-702")

	locked := make(chan struct{})
	release := make(chan struct{})
	reversal := make(chan error, 1)
	go func() {
		reversal <- h.uow.Within(context.Background(), func(tx context.Context) error {
			participation, err := h.repository.ReadParticipationByIDWithin(tx, joinedB.Participation.ID, true)
			if err != nil {
				return err
			}
			credit, err := h.repository.ReadCreditScoreEventByParticipationWithin(tx, participation.ID, true)
			if err != nil {
				return err
			}
			close(locked)
			<-release
			if _, err = h.repository.InsertScoreEventWithin(tx, referraldomain.ScoreEvent{CampaignID: campaign.ID, ParticipationID: participation.ID, InviterCustomerID: credit.InviterCustomerID, TeamID: credit.TeamID, Kind: referraldomain.ScoreReverse, Delta: -1, ReversesScoreEventID: credit.ID, Reason: "并发撤销", OccurredAt: h.clock}); err != nil {
				return err
			}
			_, err = h.repository.ReverseParticipationWithin(tx, participation)
			return err
		})
	}()
	select {
	case <-locked:
	case err := <-reversal:
		t.Fatalf("reversal lock failed early: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("reversal did not lock inviter participation")
	}

	accepted := make(chan error, 1)
	go func() {
		_, err := h.service.JoinCampaign(context.Background(), referralport.JoinCampaignCommand{Actor: referralActor(703, h.clock), CampaignID: campaign.ID, InvitationToken: inviteB, IdempotencyKey: "join-703-after-reversal"})
		accepted <- err
	}()
	select {
	case err := <-accepted:
		t.Fatalf("new acceptance bypassed locked inviter participation: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if err := <-reversal; err != nil {
		t.Fatal(err)
	}
	if err := <-accepted; !errors.Is(err, referralport.ErrInvitationInvalid) {
		t.Fatalf("reversed inviter accepted a new credit err=%v", err)
	}
	assertCount(t, h.pool, `SELECT count(*) FROM referral_participations WHERE campaign_id=$1 AND customer_id=703`, 0, campaign.ID)
}

func TestPostgreSQLReferralRewardAndReversalSerializeOnTheCredit(t *testing.T) {
	h := newReferralPostgreSQLHarness(t)
	defer h.cleanup()
	campaign, team, _ := h.createCampaignWithTeams(t, "奖励竞态活动", 751, 799)
	h.joinDirect(t, campaign.ID, team.ID, 751, "join-captain-751")
	invite := h.issue(t, campaign.ID, 751, "issue-751")
	joined := h.joinInvite(t, campaign.ID, 752, invite, "join-752-from-751")
	var creditID int64
	if err := h.pool.QueryRow(context.Background(), `SELECT id FROM referral_score_events WHERE participation_id=$1 AND kind='credit'`, joined.Participation.ID).Scan(&creditID); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	rewardResult := make(chan error, 1)
	reversalResult := make(chan error, 1)
	go func() {
		<-start
		_, err := h.admin.RecordReward(context.Background(), referralport.RecordRewardCommand{CampaignID: campaign.ID, CustomerID: 751, ScoreEventID: creditID, Period: "total", Reward: "竞态礼品", EvidenceReference: "manual:race", ActorAdminID: 9001, IdempotencyKey: "reward-race-751"})
		rewardResult <- err
	}()
	go func() {
		<-start
		reversalResult <- h.admin.ReverseInvitation(context.Background(), referralport.ReverseInvitationCommand{ParticipationID: joined.Participation.ID, Reason: "竞态撤销", ActorAdminID: 9001, IdempotencyKey: "reverse-race-752"})
	}()
	close(start)
	rewardErr, reversalErr := <-rewardResult, <-reversalResult
	if reversalErr != nil {
		t.Fatalf("reversal err=%v", reversalErr)
	}
	if rewardErr != nil && !errors.Is(rewardErr, referralport.ErrConflict) {
		t.Fatalf("reward err=%v", rewardErr)
	}
	var count int64
	var state string
	err := h.pool.QueryRow(context.Background(), `SELECT count(*),COALESCE(max(state),'') FROM referral_reward_records WHERE score_event_id=$1`, creditID).Scan(&count, &state)
	if err != nil {
		t.Fatal(err)
	}
	if count > 1 || (count == 1 && state != string(referraldomain.RewardNeedsReview)) {
		t.Fatalf("concurrent award escaped reversal review count=%d state=%q rewardErr=%v", count, state, rewardErr)
	}
}

func TestPostgreSQLReferralReversalOnlyReviewsRelatedOrAmbiguousRewards(t *testing.T) {
	h := newReferralPostgreSQLHarness(t)
	defer h.cleanup()
	campaign, team, _ := h.createCampaignWithTeams(t, "奖励范围活动", 771, 799)
	h.joinDirect(t, campaign.ID, team.ID, 771, "join-captain-771")
	invite := h.issue(t, campaign.ID, 771, "issue-771-one")
	joinedOne := h.joinInvite(t, campaign.ID, 772, invite, "join-772-from-771")
	invite = h.issue(t, campaign.ID, 771, "issue-771-two")
	joinedTwo := h.joinInvite(t, campaign.ID, 773, invite, "join-773-from-771")
	credits := map[int64]int64{}
	rows, err := h.pool.Query(context.Background(), `SELECT participation_id,id FROM referral_score_events WHERE campaign_id=$1 AND kind='credit'`, campaign.ID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var participationID, creditID int64
		if err = rows.Scan(&participationID, &creditID); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		credits[participationID] = creditID
	}
	rows.Close()
	if err = rows.Err(); err != nil || credits[joinedOne.Participation.ID] < 1 || credits[joinedTwo.Participation.ID] < 1 {
		t.Fatalf("credits=%v err=%v", credits, err)
	}
	commands := []referralport.RecordRewardCommand{
		{CampaignID: campaign.ID, CustomerID: 771, ScoreEventID: credits[joinedOne.Participation.ID], Period: "total", Reward: "关联一", EvidenceReference: "manual:one", ActorAdminID: 9001, IdempotencyKey: "scope-linked-one"},
		{CampaignID: campaign.ID, CustomerID: 771, ScoreEventID: credits[joinedTwo.Participation.ID], Period: "total", Reward: "关联二", EvidenceReference: "manual:two", ActorAdminID: 9001, IdempotencyKey: "scope-linked-two"},
		{CampaignID: campaign.ID, CustomerID: 771, Period: "total", Reward: "总榜奖励", EvidenceReference: "manual:total", ActorAdminID: 9001, IdempotencyKey: "scope-total"},
		{CampaignID: campaign.ID, CustomerID: 771, Period: "day:2026-09-18", Reward: "当日奖励", EvidenceReference: "manual:day", ActorAdminID: 9001, IdempotencyKey: "scope-day"},
		{CampaignID: campaign.ID, CustomerID: 771, Period: "day:2026-09-17", Reward: "其他日奖励", EvidenceReference: "manual:other-day", ActorAdminID: 9001, IdempotencyKey: "scope-other-day"},
		{CampaignID: campaign.ID, CustomerID: 771, Period: "运营自定义", Reward: "待核查奖励", EvidenceReference: "manual:unknown", ActorAdminID: 9001, IdempotencyKey: "scope-unknown"},
	}
	for _, command := range commands {
		if _, err = h.admin.RecordReward(context.Background(), command); err != nil {
			t.Fatalf("record reward %q: %v", command.Reward, err)
		}
	}
	if err = h.admin.ReverseInvitation(context.Background(), referralport.ReverseInvitationCommand{ParticipationID: joinedOne.Participation.ID, Reason: "关联一撤销", ActorAdminID: 9001, IdempotencyKey: "scope-reverse-one"}); err != nil {
		t.Fatal(err)
	}
	states := map[string]string{}
	rows, err = h.pool.Query(context.Background(), `SELECT reward,state FROM referral_reward_records WHERE campaign_id=$1`, campaign.ID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var reward, state string
		if err = rows.Scan(&reward, &state); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		states[reward] = state
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	for _, reward := range []string{"关联一", "总榜奖励", "当日奖励", "待核查奖励"} {
		if states[reward] != string(referraldomain.RewardNeedsReview) {
			t.Fatalf("reward %q state=%q; expected review states=%v", reward, states[reward], states)
		}
	}
	for _, reward := range []string{"关联二", "其他日奖励"} {
		if states[reward] != string(referraldomain.RewardRecorded) {
			t.Fatalf("unrelated reward %q state=%q states=%v", reward, states[reward], states)
		}
	}
	var reason string
	if err = h.pool.QueryRow(context.Background(), `SELECT reason FROM referral_reward_reviews rr JOIN referral_reward_records r ON r.id=rr.reward_id WHERE r.reward='待核查奖励'`).Scan(&reason); err != nil || len(reason) < len("unscoped_reward_requires_review:") || reason[:len("unscoped_reward_requires_review:")] != "unscoped_reward_requires_review:" {
		t.Fatalf("unscoped review reason=%q err=%v", reason, err)
	}
}

func TestPostgreSQLReferralCaptainMustJoinOwnTeamAndIsUniquePerCampaign(t *testing.T) {
	t.Skip("team captains are reporting-only; participation no longer grants captain authority")
	h := newReferralPostgreSQLHarness(t)
	defer h.cleanup()
	campaign, captainTeam, otherTeam := h.createCampaignWithTeams(t, "队长归队活动", 761, 762)
	if _, err := h.service.JoinCampaign(context.Background(), referralport.JoinCampaignCommand{Actor: referralActor(761, h.clock), CampaignID: campaign.ID, TeamID: otherTeam.ID, IdempotencyKey: "captain-761-wrong-team"}); !errors.Is(err, referralport.ErrConflict) {
		t.Fatalf("captain joined another team err=%v", err)
	}
	if _, err := h.service.JoinCampaign(context.Background(), referralport.JoinCampaignCommand{Actor: referralActor(761, h.clock), CampaignID: campaign.ID, TeamID: captainTeam.ID, IdempotencyKey: "captain-761-own-team"}); err != nil {
		t.Fatalf("captain could not join own team err=%v", err)
	}
	draft, err := h.admin.CreateCampaign(context.Background(), referralport.CreateCampaignCommand{ActorAdminID: 9001, Name: "队长唯一草稿", StartsAt: h.clock.Add(time.Hour), EndsAt: h.clock.Add(48 * time.Hour), IdempotencyKey: "create-captain-unique-draft"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.admin.CreateTeam(context.Background(), referralport.CreateTeamCommand{ActorAdminID: 9001, CampaignID: draft.ID, CaptainCustomerID: 761, Name: "唯一队长队", IdempotencyKey: "create-captain-unique-team"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.admin.CreateTeam(context.Background(), referralport.CreateTeamCommand{ActorAdminID: 9001, CampaignID: draft.ID, CaptainCustomerID: 761, Name: "重复队长", IdempotencyKey: "duplicate-captain-761"}); !errors.Is(err, referralport.ErrCaptainAlreadyAssigned) {
		t.Fatalf("one captain created multiple teams err=%v", err)
	}
}

func TestPostgreSQLReferralAdminActivityDetailsAndStableExports(t *testing.T) {
	h := newReferralPostgreSQLHarness(t)
	defer h.cleanup()

	campaign, teamOne, teamTwo := h.createCampaignWithTeams(t, "运营活动详情", 861, 862)
	// A designated captain has an explicit team entry before accepting activity
	// rules. The UI uses this to send the captain to their own team first.
	captain, err := h.service.MyCampaign(context.Background(), referralActor(861, h.clock), campaign.ID)
	if err != nil || !captain.IsCaptain || captain.CaptainTeam == nil || captain.CaptainTeam.ID != teamOne.ID || captain.Participation != nil {
		t.Fatalf("pending captain view=%+v err=%v", captain, err)
	}

	// Teams can be created while the effective lifecycle is active. A replay
	// returns the immutable first result; changing the payload for that key is
	// rejected without a second team write.
	teamThree, err := h.admin.CreateTeam(context.Background(), referralport.CreateTeamCommand{ActorAdminID: 9001, CampaignID: campaign.ID, CaptainCustomerID: 863, Name: "运营活动详情丙队", IdempotencyKey: "active-team-three"})
	if err != nil {
		t.Fatalf("create team during active lifecycle: %v", err)
	}
	replayed, err := h.admin.CreateTeam(context.Background(), referralport.CreateTeamCommand{ActorAdminID: 9001, CampaignID: campaign.ID, CaptainCustomerID: 863, Name: "运营活动详情丙队", IdempotencyKey: "active-team-three"})
	if err != nil || replayed.ID != teamThree.ID {
		t.Fatalf("same create-team key did not replay original team=%+v err=%v", replayed, err)
	}
	if _, err = h.admin.CreateTeam(context.Background(), referralport.CreateTeamCommand{ActorAdminID: 9001, CampaignID: campaign.ID, CaptainCustomerID: 864, Name: "changed", IdempotencyKey: "active-team-three"}); !errors.Is(err, referralport.ErrIdempotencyConflict) {
		t.Fatalf("changed create-team payload err=%v", err)
	}
	if _, err = h.admin.CreateTeam(context.Background(), referralport.CreateTeamCommand{ActorAdminID: 9001, CampaignID: campaign.ID, CaptainCustomerID: 864, Name: teamThree.Name, IdempotencyKey: "duplicate-team-name"}); !errors.Is(err, referralport.ErrTeamNameExists) {
		t.Fatalf("duplicate team name err=%v", err)
	}
	if _, err = h.admin.CreateTeam(context.Background(), referralport.CreateTeamCommand{ActorAdminID: 9001, CampaignID: campaign.ID, CaptainCustomerID: 863, Name: "重复队长队", IdempotencyKey: "duplicate-team-captain"}); !errors.Is(err, referralport.ErrCaptainAlreadyAssigned) {
		t.Fatalf("duplicate team captain err=%v", err)
	}

	var auditBefore, outboxBefore int64
	if err = h.pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_events WHERE action='referral.team.created'`).Scan(&auditBefore); err != nil {
		t.Fatal(err)
	}
	if err = h.pool.QueryRow(context.Background(), `SELECT count(*) FROM outbox_events WHERE event_type='referral.team.created'`).Scan(&outboxBefore); err != nil {
		t.Fatal(err)
	}
	h.admin.outbox = failingReferralOutbox{}
	if _, err = h.admin.CreateTeam(context.Background(), referralport.CreateTeamCommand{ActorAdminID: 9001, CampaignID: campaign.ID, CaptainCustomerID: 865, Name: "应回滚的队", IdempotencyKey: "team-outbox-failure"}); !errors.Is(err, errReferralOutbox) {
		t.Fatalf("team outbox failure err=%v", err)
	}
	assertCount(t, h.pool, `SELECT count(*) FROM referral_teams WHERE campaign_id=$1 AND captain_customer_id=865`, 0, campaign.ID)
	assertCount(t, h.pool, `SELECT count(*) FROM audit_events WHERE action='referral.team.created'`, auditBefore)
	assertCount(t, h.pool, `SELECT count(*) FROM outbox_events WHERE event_type='referral.team.created'`, outboxBefore)
	h.admin.outbox = platformoutbox.NewPostgreSQL()

	h.joinDirect(t, campaign.ID, teamOne.ID, 861, "operations-captain-861")
	inviteA := h.issue(t, campaign.ID, 861, "operations-invite-861")
	joinedB := h.joinInvite(t, campaign.ID, 871, inviteA, "operations-join-871")
	inviteB := h.issue(t, campaign.ID, 871, "operations-invite-871")
	h.joinInvite(t, campaign.ID, 872, inviteB, "operations-join-872")
	h.joinDirect(t, campaign.ID, teamOne.ID, 873, "operations-direct-873")

	view, err := h.admin.ReadAdminCampaign(context.Background(), campaign.ID)
	if err != nil || len(view.TeamSummaries) != 3 {
		t.Fatalf("campaign team dashboard=%+v err=%v", view, err)
	}
	var one, two, three referralport.AdminTeamSummary
	for _, summary := range view.TeamSummaries {
		switch summary.Team.ID {
		case teamOne.ID:
			one = summary
		case teamTwo.ID:
			two = summary
		case teamThree.ID:
			three = summary
		}
	}
	if one.ParticipantCount != 4 || one.DirectInvitationCount != 2 || !one.CaptainParticipated || two.ParticipantCount != 0 || two.CaptainParticipated || three.ParticipantCount != 0 || three.CaptainParticipated {
		t.Fatalf("team aggregates one=%+v two=%+v three=%+v", one, two, three)
	}

	participants, err := h.admin.ListAdminParticipants(context.Background(), referralport.AdminParticipantQuery{CampaignID: campaign.ID, TeamID: teamOne.ID, Limit: 2})
	if err != nil || len(participants.Items) != 2 || participants.NextCursor == "" {
		t.Fatalf("first participant page=%+v err=%v", participants, err)
	}
	participants, err = h.admin.ListAdminParticipants(context.Background(), referralport.AdminParticipantQuery{CampaignID: campaign.ID, TeamID: teamOne.ID, Cursor: participants.NextCursor, Limit: 2})
	if err != nil || len(participants.Items) != 2 || participants.NextCursor != "" {
		t.Fatalf("second participant page=%+v err=%v", participants, err)
	}
	exportedParticipants, err := h.admin.ListAdminParticipantsForExport(context.Background(), referralport.AdminParticipantQuery{CampaignID: campaign.ID, TeamID: teamOne.ID, Limit: 1})
	if err != nil || len(exportedParticipants) != 4 {
		t.Fatalf("participant export was truncated values=%+v err=%v", exportedParticipants, err)
	}
	var noSource bool
	for _, item := range exportedParticipants {
		if item.Participation.CustomerID == 873 {
			noSource = item.InviterCustomerID == 0 && item.DirectInvitationCount == 0
		}
	}
	if !noSource {
		t.Fatalf("no-source participant omitted or misrepresented export=%+v", exportedParticipants)
	}

	invitations, err := h.admin.ListAdminInvitations(context.Background(), referralport.AdminInvitationQuery{CampaignID: campaign.ID, InviterCustomerID: 871, State: referraldomain.ParticipationActive, Limit: 20})
	if err != nil || len(invitations.Items) != 1 || invitations.Items[0].Participation.CustomerID != 872 || invitations.Items[0].ScoreState != "valid" || invitations.Items[0].InviterTeamID != teamOne.ID || invitations.Items[0].ParticipantTeam.ID != teamOne.ID {
		t.Fatalf("filtered active invitations=%+v err=%v", invitations, err)
	}
	drilldown, err := h.admin.ListAdminParticipantInvitations(context.Background(), referralport.AdminParticipantInvitationQuery{CampaignID: campaign.ID, ParticipationID: joinedB.Participation.ID, Limit: 20})
	if err != nil || len(drilldown.Items) != 1 || drilldown.Items[0].Participation.CustomerID != 872 {
		t.Fatalf("participant invitation drilldown=%+v err=%v", drilldown, err)
	}
	drilldownExport, err := h.admin.ListAdminParticipantInvitationsForExport(context.Background(), referralport.AdminParticipantInvitationQuery{CampaignID: campaign.ID, ParticipationID: joinedB.Participation.ID, Limit: 1})
	if err != nil || len(drilldownExport) != 1 || drilldownExport[0].Participation.CustomerID != 872 {
		t.Fatalf("participant invitation export=%+v err=%v", drilldownExport, err)
	}
	if _, err = h.admin.ListAdminParticipantInvitationsForExport(context.Background(), referralport.AdminParticipantInvitationQuery{CampaignID: campaign.ID, ParticipationID: joinedB.Participation.ID, Cursor: "1"}); !errors.Is(err, referralport.ErrConflict) {
		t.Fatalf("participant invitation paged export err=%v", err)
	}
	if _, err = h.admin.ListAdminParticipantInvitations(context.Background(), referralport.AdminParticipantInvitationQuery{CampaignID: campaign.ID + 1, ParticipationID: joinedB.Participation.ID, Limit: 20}); !errors.Is(err, referralport.ErrNotFound) {
		t.Fatalf("cross-campaign participant drilldown err=%v", err)
	}

	if err = h.admin.ReverseInvitation(context.Background(), referralport.ReverseInvitationCommand{ParticipationID: joinedB.Participation.ID, ActorAdminID: 9001, Reason: "运营撤销", IdempotencyKey: "operations-reverse-871"}); err != nil {
		t.Fatal(err)
	}
	reversedParticipants, err := h.admin.ListAdminParticipants(context.Background(), referralport.AdminParticipantQuery{CampaignID: campaign.ID, State: referraldomain.ParticipationReversed, Limit: 20})
	if err != nil || len(reversedParticipants.Items) != 1 || reversedParticipants.Items[0].Participation.ID != joinedB.Participation.ID {
		t.Fatalf("reversed participant filter=%+v err=%v", reversedParticipants, err)
	}
	validInvites, err := h.admin.ListAdminInvitations(context.Background(), referralport.AdminInvitationQuery{CampaignID: campaign.ID, State: referraldomain.ParticipationActive, Limit: 20})
	if err != nil || len(validInvites.Items) != 1 || validInvites.Items[0].Participation.CustomerID != 872 {
		t.Fatalf("valid score-state filter=%+v err=%v", validInvites, err)
	}
	reversedInvites, err := h.admin.ListAdminInvitations(context.Background(), referralport.AdminInvitationQuery{CampaignID: campaign.ID, State: referraldomain.ParticipationReversed, Limit: 20})
	if err != nil || len(reversedInvites.Items) != 1 || reversedInvites.Items[0].Participation.ID != joinedB.Participation.ID || reversedInvites.Items[0].ScoreState != "reversed" {
		t.Fatalf("reversed score-state filter=%+v err=%v", reversedInvites, err)
	}
	exportedInvites, err := h.admin.ListAdminInvitationsForExport(context.Background(), referralport.AdminInvitationQuery{CampaignID: campaign.ID, Limit: 1})
	if err != nil || len(exportedInvites) != 2 {
		t.Fatalf("invitation export was truncated values=%+v err=%v", exportedInvites, err)
	}

	// Adding a team after the activity already has facts cannot rewrite any
	// existing participation. A member of another immutable team cannot then
	// become a newly-designated captain.
	if _, err = h.admin.CreateTeam(context.Background(), referralport.CreateTeamCommand{ActorAdminID: 9001, CampaignID: campaign.ID, CaptainCustomerID: 874, Name: "活动中新增队", IdempotencyKey: "team-after-participations"}); err != nil {
		t.Fatalf("create team after activity facts: %v", err)
	}
	assertCount(t, h.pool, `SELECT count(*) FROM referral_participations WHERE campaign_id=$1 AND team_id=$2`, 4, campaign.ID, teamOne.ID)
	h.joinDirect(t, campaign.ID, teamOne.ID, 875, "operations-direct-875")
	if _, err = h.admin.CreateTeam(context.Background(), referralport.CreateTeamCommand{ActorAdminID: 9001, CampaignID: campaign.ID, CaptainCustomerID: 875, Name: "既有队员可作汇总队长", IdempotencyKey: "team-member-captain"}); err != nil {
		t.Fatalf("reporting-only captain assignment failed err=%v", err)
	}

	h.clock = campaign.EndsAt
	if _, err = h.admin.CreateTeam(context.Background(), referralport.CreateTeamCommand{ActorAdminID: 9001, CampaignID: campaign.ID, CaptainCustomerID: 866, Name: "结束后队", IdempotencyKey: "team-after-end"}); !errors.Is(err, referralport.ErrCampaignTeamLocked) {
		t.Fatalf("ended campaign create team err=%v", err)
	}
	h.admin.verifier = untrustedReferralCustomerVerifier{}
	if _, err = h.admin.CreateTeam(context.Background(), referralport.CreateTeamCommand{ActorAdminID: 9001, CampaignID: campaign.ID, CaptainCustomerID: 867, Name: "不可信队长队", IdempotencyKey: "team-ineligible-captain"}); !errors.Is(err, referralport.ErrCaptainIneligible) {
		t.Fatalf("ineligible captain err=%v", err)
	}
}

func TestPostgreSQLReferralCreateTeamSerializesWithCaptainParticipation(t *testing.T) {
	t.Skip("team assignment is a reporting dimension and does not lock participation")
	h := newReferralPostgreSQLHarness(t)
	defer h.cleanup()

	campaign, existingTeam, _ := h.createCampaignWithTeams(t, "队长参与并发", 881, 882)
	start := make(chan struct{})
	var wait sync.WaitGroup
	var createErr, joinErr error
	wait.Add(2)
	go func() {
		defer wait.Done()
		<-start
		_, createErr = h.admin.CreateTeam(context.Background(), referralport.CreateTeamCommand{ActorAdminID: 9001, CampaignID: campaign.ID, CaptainCustomerID: 883, Name: "并发队长队", IdempotencyKey: "concurrent-captain-team"})
	}()
	go func() {
		defer wait.Done()
		<-start
		_, joinErr = h.service.JoinCampaign(context.Background(), referralport.JoinCampaignCommand{Actor: referralActor(883, h.clock), CampaignID: campaign.ID, TeamID: existingTeam.ID, IdempotencyKey: "concurrent-captain-join"})
	}()
	close(start)
	wait.Wait()

	var teamCount, participationCount int64
	if err := h.pool.QueryRow(context.Background(), `SELECT count(*) FROM referral_teams WHERE campaign_id=$1 AND captain_customer_id=883`, campaign.ID).Scan(&teamCount); err != nil {
		t.Fatal(err)
	}
	if err := h.pool.QueryRow(context.Background(), `SELECT count(*) FROM referral_participations WHERE campaign_id=$1 AND customer_id=883`, campaign.ID).Scan(&participationCount); err != nil {
		t.Fatal(err)
	}
	switch {
	case createErr == nil:
		if joinErr == nil || teamCount != 1 || participationCount != 0 {
			t.Fatalf("team assignment should block conflicting join createErr=%v joinErr=%v teams=%d participations=%d", createErr, joinErr, teamCount, participationCount)
		}
	case joinErr == nil:
		if !errors.Is(createErr, referralport.ErrCaptainIneligible) || teamCount != 0 || participationCount != 1 {
			t.Fatalf("first participation should block captain assignment createErr=%v joinErr=%v teams=%d participations=%d", createErr, joinErr, teamCount, participationCount)
		}
	default:
		t.Fatalf("one concurrent command must succeed createErr=%v joinErr=%v", createErr, joinErr)
	}
}

func TestPostgreSQLReferralRejectsUnsafeInvitationsAndRollsBackPlatformFacts(t *testing.T) {
	h := newReferralPostgreSQLHarness(t)
	defer h.cleanup()
	campaign, team, _ := h.createCampaignWithTeams(t, "拒绝活动", 801, 802)
	h.joinDirect(t, campaign.ID, team.ID, 801, "join-captain-801")
	invite := h.issue(t, campaign.ID, 801, "issue-801")

	if _, err := h.service.JoinCampaign(context.Background(), referralport.JoinCampaignCommand{Actor: referralActor(801, h.clock), CampaignID: campaign.ID, InvitationToken: invite, IdempotencyKey: "self-invite-801"}); !errors.Is(err, referralport.ErrInvitationInvalid) {
		t.Fatalf("self invitation err=%v", err)
	}
	if _, err := h.service.JoinCampaign(context.Background(), referralport.JoinCampaignCommand{Actor: referralActor(803, h.clock), CampaignID: campaign.ID, InvitationToken: "rfi_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", IdempotencyKey: "forged-invite-803"}); !errors.Is(err, referralport.ErrInvitationInvalid) {
		t.Fatalf("forged invitation err=%v", err)
	}
	h.clock = h.clock.Add(30 * time.Minute)
	if _, err := h.pool.Exec(context.Background(), `UPDATE referral_invitations SET expires_at=$2 WHERE campaign_id=$1`, campaign.ID, h.clock.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := h.service.JoinCampaign(context.Background(), referralport.JoinCampaignCommand{Actor: referralActor(804, h.clock), CampaignID: campaign.ID, InvitationToken: invite, IdempotencyKey: "expired-invite-804"}); !errors.Is(err, referralport.ErrInvitationInvalid) {
		t.Fatalf("expired invitation err=%v", err)
	}

	if _, err := h.admin.SetCampaignState(context.Background(), referralport.SetCampaignStateCommand{ActorAdminID: 9001, CampaignID: campaign.ID, ExpectedVersion: campaign.Version, Target: referraldomain.CampaignDisabled, IdempotencyKey: "disable-campaign-801"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.service.JoinCampaign(context.Background(), referralport.JoinCampaignCommand{Actor: referralActor(805, h.clock), CampaignID: campaign.ID, TeamID: team.ID, IdempotencyKey: "disabled-direct-805"}); !errors.Is(err, referralport.ErrCampaignUnavailable) {
		t.Fatalf("disabled campaign join err=%v", err)
	}

	// A platform outbox failure happens after Referral's participation, receipt,
	// and audit writes. The common UoW must roll all of them back together.
	rollbackCampaign, rollbackTeam, _ := h.createCampaignWithTeams(t, "回滚活动", 901, 902)
	var auditBefore, outboxBefore int64
	if err := h.pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_events WHERE action='referral.participation.joined'`).Scan(&auditBefore); err != nil {
		t.Fatal(err)
	}
	if err := h.pool.QueryRow(context.Background(), `SELECT count(*) FROM outbox_events WHERE event_type='referral.participation.joined'`).Scan(&outboxBefore); err != nil {
		t.Fatal(err)
	}
	h.service.outbox = failingReferralOutbox{}
	if _, err := h.service.JoinCampaign(context.Background(), referralport.JoinCampaignCommand{Actor: referralActor(903, h.clock), CampaignID: rollbackCampaign.ID, TeamID: rollbackTeam.ID, IdempotencyKey: "outbox-failure-903"}); !errors.Is(err, errReferralOutbox) {
		t.Fatalf("outbox failure join err=%v", err)
	}
	assertCount(t, h.pool, `SELECT count(*) FROM referral_participations WHERE campaign_id=$1 AND customer_id=903`, 0, rollbackCampaign.ID)
	assertCount(t, h.pool, `SELECT count(*) FROM referral_operation_receipts WHERE operation='join' AND actor_scope='customer:903'`, 0)
	assertCount(t, h.pool, `SELECT count(*) FROM audit_events WHERE action='referral.participation.joined'`, auditBefore)
	assertCount(t, h.pool, `SELECT count(*) FROM outbox_events WHERE event_type='referral.participation.joined'`, outboxBefore)
}

func TestPostgreSQLReferralRanksAcrossAsiaShanghaiDayAndWeekBoundaries(t *testing.T) {
	h := newReferralPostgreSQLHarness(t)
	defer h.cleanup()
	h.clock = time.Date(2026, 9, 20, 15, 50, 0, 0, time.UTC) // Sunday 23:50 in Shanghai.
	campaign, team, _ := h.createCampaignWithTeams(t, "跨日周活动", 1001, 1999)
	h.joinDirect(t, campaign.ID, team.ID, 1001, "join-captain-1001")
	inviteA := h.issue(t, campaign.ID, 1001, "issue-1001")
	sunday := h.clock
	h.joinInvite(t, campaign.ID, 1002, inviteA, "join-1002-sunday")
	inviteB := h.issue(t, campaign.ID, 1002, "issue-1002")
	h.clock = time.Date(2026, 9, 20, 16, 30, 0, 0, time.UTC) // Monday 00:30 in Shanghai.
	monday := h.clock
	h.joinInvite(t, campaign.ID, 1003, inviteB, "join-1003-monday")

	for _, scenario := range []struct {
		period referralport.LeaderboardPeriod
		anchor time.Time
		winner int64
	}{
		{referralport.LeaderboardDay, sunday, 1001},
		{referralport.LeaderboardDay, monday, 1002},
		{referralport.LeaderboardWeek, sunday, 1001},
		{referralport.LeaderboardWeek, monday, 1002},
	} {
		board, err := h.service.Leaderboard(context.Background(), referralport.LeaderboardQuery{CampaignID: campaign.ID, Kind: referralport.LeaderboardPersonal, Period: scenario.period, Anchor: scenario.anchor, Limit: 20})
		if err != nil || len(board.Items) != 1 || board.Items[0].CustomerID != scenario.winner || board.Items[0].Score != 1 {
			t.Fatalf("period=%s anchor=%s board=%+v err=%v", scenario.period, scenario.anchor, board, err)
		}
	}
}

func TestPostgreSQLReferralAdminKeysetPaginationIgnoresNewJoinsAndShowsReversal(t *testing.T) {
	h := newReferralPostgreSQLHarness(t)
	defer h.cleanup()

	campaign, team, _ := h.createCampaignWithTeams(t, "管理分页稳定活动", 941, 942)
	h.joinDirect(t, campaign.ID, team.ID, 941, "keyset-join-captain")
	invitation := h.issue(t, campaign.ID, 941, "keyset-issue-captain")
	joinedA := h.joinInvite(t, campaign.ID, 943, invitation, "keyset-join-a")
	h.joinInvite(t, campaign.ID, 944, invitation, "keyset-join-b")
	h.joinInvite(t, campaign.ID, 945, invitation, "keyset-join-c")

	captain, err := h.service.MyCampaign(context.Background(), referralActor(941, h.clock), campaign.ID)
	if err != nil || captain.Participation == nil {
		t.Fatalf("captain participation=%+v err=%v", captain.Participation, err)
	}

	participantsFirst, err := h.admin.ListAdminParticipants(context.Background(), referralport.AdminParticipantQuery{CampaignID: campaign.ID, Limit: 2})
	if err != nil || len(participantsFirst.Items) != 2 || participantsFirst.NextCursor == "" {
		t.Fatalf("first participant page=%+v err=%v", participantsFirst, err)
	}
	invitationsFirst, err := h.admin.ListAdminInvitations(context.Background(), referralport.AdminInvitationQuery{CampaignID: campaign.ID, InviterCustomerID: 941, Limit: 2})
	if err != nil || len(invitationsFirst.Items) != 2 || invitationsFirst.NextCursor == "" {
		t.Fatalf("first invitation page=%+v err=%v", invitationsFirst, err)
	}
	scopedFirst, err := h.admin.ListAdminParticipantInvitations(context.Background(), referralport.AdminParticipantInvitationQuery{CampaignID: campaign.ID, ParticipationID: captain.Participation.ID, Limit: 2})
	if err != nil || len(scopedFirst.Items) != 2 || scopedFirst.NextCursor == "" {
		t.Fatalf("first scoped invitation page=%+v err=%v", scopedFirst, err)
	}

	// D joins after all cursors have been issued. Every continuation is after
	// the last delivered joined_at/id pair, so D cannot shift, duplicate, or
	// hide the older records on page two.
	joinedD := h.joinInvite(t, campaign.ID, 946, invitation, "keyset-join-d-after-page-one")
	if err = h.admin.ReverseInvitation(context.Background(), referralport.ReverseInvitationCommand{ParticipationID: joinedA.Participation.ID, Reason: "page-boundary-reversal", ActorAdminID: 9001, IdempotencyKey: "keyset-reverse-a"}); err != nil {
		t.Fatal(err)
	}

	participantsSecond, err := h.admin.ListAdminParticipants(context.Background(), referralport.AdminParticipantQuery{CampaignID: campaign.ID, Cursor: participantsFirst.NextCursor, Limit: 2})
	if err != nil || len(participantsSecond.Items) != 2 || participantsSecond.NextCursor != "" {
		t.Fatalf("second participant page=%+v err=%v", participantsSecond, err)
	}
	participantIDs := map[int64]bool{}
	for _, item := range append(participantsFirst.Items, participantsSecond.Items...) {
		if participantIDs[item.Participation.ID] {
			t.Fatalf("participant duplicated across keyset pages id=%d", item.Participation.ID)
		}
		participantIDs[item.Participation.ID] = true
	}
	if len(participantIDs) != 4 || participantIDs[joinedD.Participation.ID] || !participantIDs[joinedA.Participation.ID] {
		t.Fatalf("participant traversal drift ids=%v new=%d original=%d", participantIDs, joinedD.Participation.ID, joinedA.Participation.ID)
	}
	if participantsSecond.Items[0].Participation.ID != joinedA.Participation.ID || participantsSecond.Items[0].Participation.State != referraldomain.ParticipationReversed {
		t.Fatalf("reversed older participant missing from continuation=%+v", participantsSecond.Items)
	}

	invitationsSecond, err := h.admin.ListAdminInvitations(context.Background(), referralport.AdminInvitationQuery{CampaignID: campaign.ID, InviterCustomerID: 941, Cursor: invitationsFirst.NextCursor, Limit: 2})
	if err != nil || len(invitationsSecond.Items) != 1 || invitationsSecond.NextCursor != "" || invitationsSecond.Items[0].Participation.ID != joinedA.Participation.ID || invitationsSecond.Items[0].ScoreState != "reversed" {
		t.Fatalf("second invitation page=%+v err=%v", invitationsSecond, err)
	}
	if invitationsSecond.Items[0].Participation.ID == joinedD.Participation.ID {
		t.Fatalf("new invitation leaked into continuation=%+v", invitationsSecond)
	}

	scopedSecond, err := h.admin.ListAdminParticipantInvitations(context.Background(), referralport.AdminParticipantInvitationQuery{CampaignID: campaign.ID, ParticipationID: captain.Participation.ID, Cursor: scopedFirst.NextCursor, Limit: 2})
	if err != nil || len(scopedSecond.Items) != 1 || scopedSecond.NextCursor != "" || scopedSecond.Items[0].Participation.ID != joinedA.Participation.ID || scopedSecond.Items[0].ScoreState != "reversed" {
		t.Fatalf("second scoped invitation page=%+v err=%v", scopedSecond, err)
	}

	// v1 used numeric offsets. Refusing every such continuation prevents mixed
	// traversal semantics while a campaign is live.
	if _, err = h.admin.ListAdminParticipants(context.Background(), referralport.AdminParticipantQuery{CampaignID: campaign.ID, Cursor: "2", Limit: 2}); !errors.Is(err, referralport.ErrConflict) {
		t.Fatalf("legacy participant offset err=%v", err)
	}
	if _, err = h.admin.ListAdminInvitations(context.Background(), referralport.AdminInvitationQuery{CampaignID: campaign.ID, InviterCustomerID: 941, Cursor: "2", Limit: 2}); !errors.Is(err, referralport.ErrConflict) {
		t.Fatalf("legacy invitation offset err=%v", err)
	}
	if _, err = h.admin.ListAdminParticipantInvitations(context.Background(), referralport.AdminParticipantInvitationQuery{CampaignID: campaign.ID, ParticipationID: captain.Participation.ID, Cursor: "2", Limit: 2}); !errors.Is(err, referralport.ErrConflict) {
		t.Fatalf("legacy scoped invitation offset err=%v", err)
	}
}

type referralPostgreSQLHarness struct {
	pool       *pgxpool.Pool
	cleanup    func()
	uow        platformport.UnitOfWork
	repository *referralstore.Repository
	service    *Service
	admin      *AdminService
	now        time.Time
	clock      time.Time
}

func newReferralPostgreSQLHarness(t *testing.T) *referralPostgreSQLHarness {
	t.Helper()
	pool, cleanup := referralPostgreSQLPool(t)
	wrapped, err := platformpostgres.Wrap(pool, time.Second)
	if err != nil {
		cleanup()
		t.Fatal(err)
	}
	t.Cleanup(wrapped.Close)
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		cleanup()
		t.Fatal(err)
	}
	repository, err := referralstore.NewPostgreSQL(pool, uow)
	if err != nil {
		cleanup()
		t.Fatal(err)
	}
	audit, err := platformaudit.NewService(platformaudit.NewPostgreSQLStore())
	if err != nil {
		cleanup()
		t.Fatal(err)
	}
	closeJobs := &referralCloseEnqueuer{}
	key := base64.RawStdEncoding.EncodeToString(make([]byte, 32))
	service, err := NewService(uow, repository, "https://referral.test", key, closeJobs, audit, platformoutbox.NewPostgreSQL())
	if err != nil {
		cleanup()
		t.Fatal(err)
	}
	admin, err := NewAdminService(uow, repository, referralCustomerVerifier{}, closeJobs, audit, platformoutbox.NewPostgreSQL())
	if err != nil {
		cleanup()
		t.Fatal(err)
	}
	clock := time.Date(2026, 9, 18, 1, 30, 0, 0, time.UTC)
	service.now = func() time.Time { return clock }
	admin.now = func() time.Time { return clock }
	h := &referralPostgreSQLHarness{pool: pool, cleanup: cleanup, uow: uow, repository: repository, service: service, admin: admin, clock: clock}
	// Capture current clock through a deliberately shared closure so each test
	// can advance the server time without allowing any browser-supplied time.
	service.now = func() time.Time { return h.clock }
	admin.now = func() time.Time { return h.clock }
	h.now = h.clock
	return h
}

func (h *referralPostgreSQLHarness) createCampaignWithTeams(t *testing.T, name string, captainOne, captainTwo int64) (referraldomain.Campaign, referraldomain.Team, referraldomain.Team) {
	t.Helper()
	starts := h.clock.Add(-time.Hour)
	ends := h.clock.Add(2 * time.Hour)
	campaign, err := h.admin.CreateCampaign(context.Background(), referralport.CreateCampaignCommand{ActorAdminID: 9001, Name: name, StartsAt: starts, EndsAt: ends, IdempotencyKey: "create-" + name})
	if err != nil {
		t.Fatalf("create campaign %q: %v", name, err)
	}
	teamOne, err := h.admin.CreateTeam(context.Background(), referralport.CreateTeamCommand{ActorAdminID: 9001, CampaignID: campaign.ID, CaptainCustomerID: captainOne, Name: name + "甲队", IdempotencyKey: "team-one-" + name})
	if err != nil {
		t.Fatalf("create first team: %v", err)
	}
	teamTwo, err := h.admin.CreateTeam(context.Background(), referralport.CreateTeamCommand{ActorAdminID: 9001, CampaignID: campaign.ID, CaptainCustomerID: captainTwo, Name: name + "乙队", IdempotencyKey: "team-two-" + name})
	if err != nil {
		t.Fatalf("create second team: %v", err)
	}
	campaign, err = h.admin.SetCampaignState(context.Background(), referralport.SetCampaignStateCommand{ActorAdminID: 9001, CampaignID: campaign.ID, ExpectedVersion: campaign.Version, Target: referraldomain.CampaignActive, IdempotencyKey: "activate-" + name})
	if err != nil {
		t.Fatalf("activate campaign: %v", err)
	}
	return campaign, teamOne, teamTwo
}

func (h *referralPostgreSQLHarness) joinDirect(t *testing.T, campaignID, teamID, customerID int64, key string) {
	t.Helper()
	if _, err := h.service.JoinCampaign(context.Background(), referralport.JoinCampaignCommand{Actor: referralActor(customerID, h.clock), CampaignID: campaignID, TeamID: teamID, IdempotencyKey: key}); err != nil {
		t.Fatalf("direct join customer=%d: %v", customerID, err)
	}
	h.clock = h.clock.Add(time.Minute)
}

func (h *referralPostgreSQLHarness) issue(t *testing.T, campaignID, customerID int64, key string) string {
	t.Helper()
	link, err := h.service.IssueInvitation(context.Background(), referralport.IssueInvitationCommand{Actor: referralActor(customerID, h.clock), CampaignID: campaignID, IdempotencyKey: key})
	if err != nil {
		t.Fatalf("issue invitation customer=%d: %v", customerID, err)
	}
	h.clock = h.clock.Add(time.Minute)
	return link.URL[len("https://referral.test/referral/invite/"):]
}

func (h *referralPostgreSQLHarness) joinInvite(t *testing.T, campaignID, customerID int64, token, key string) referralport.MyCampaign {
	t.Helper()
	joined, err := h.service.JoinCampaign(context.Background(), referralport.JoinCampaignCommand{Actor: referralActor(customerID, h.clock), CampaignID: campaignID, InvitationToken: token, IdempotencyKey: key})
	if err != nil {
		t.Fatalf("invite join customer=%d: %v", customerID, err)
	}
	h.now = h.clock
	h.clock = h.clock.Add(time.Minute)
	return joined
}

type referralCloseEnqueuer struct{}

func (*referralCloseEnqueuer) EnqueueCampaignCloseWithin(context.Context, int64, time.Time) error {
	return nil
}

type referralCustomerVerifier struct{}

func (referralCustomerVerifier) VerifyCanonicalCustomer(context.Context, int64) (bool, error) {
	return true, nil
}

type untrustedReferralCustomerVerifier struct{}

func (untrustedReferralCustomerVerifier) VerifyCanonicalCustomer(context.Context, int64) (bool, error) {
	return false, nil
}

func referralActor(customerID int64, at time.Time) distributionport.TrustedSessionActor {
	return distributionport.TrustedSessionActor{CustomerID: customerID, IdentityID: customerID + 10_000, AppID: "wx-referral-test", AppScope: "wechat-app:wx-referral-test", Channel: "h5_official_account", OccurredAt: at}
}

var errReferralOutbox = errors.New("referral test outbox failure")

type failingReferralOutbox struct{}

func (failingReferralOutbox) Append(context.Context, platformoutbox.Event) (platformoutbox.Event, error) {
	return platformoutbox.Event{}, errReferralOutbox
}

type concurrentJoinResult struct {
	participationID int64
	err             error
}

func runConcurrentJoins(t *testing.T, service *Service, commands []referralport.JoinCampaignCommand) []concurrentJoinResult {
	t.Helper()
	start := make(chan struct{})
	results := make([]concurrentJoinResult, len(commands))
	var wait sync.WaitGroup
	for index := range commands {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			value, err := service.JoinCampaign(context.Background(), commands[index])
			if value.Participation != nil {
				results[index].participationID = value.Participation.ID
			}
			results[index].err = err
		}(index)
	}
	close(start)
	wait.Wait()
	return results
}

func assertCount(t *testing.T, pool *pgxpool.Pool, query string, expected int64, args ...any) {
	t.Helper()
	var actual int64
	if err := pool.QueryRow(context.Background(), query, args...).Scan(&actual); err != nil || actual != expected {
		t.Fatalf("count expected=%d actual=%d err=%v query=%s", expected, actual, err, query)
	}
}

func referralPostgreSQLPool(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	databaseURL, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured; skipping Referral PostgreSQL integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	adminConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.NewWithConfig(ctx, adminConfig)
	if err != nil {
		t.Fatal(err)
	}
	var randomBytes [8]byte
	if _, err = rand.Read(randomBytes[:]); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	schema := "aicrm_referral_" + hex.EncodeToString(randomBytes[:])
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	config := adminConfig.Copy()
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close()
		t.Fatal(err)
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		pool.Close()
		admin.Close()
		t.Fatal("locate referral migrations")
	}
	root := filepath.Join(filepath.Dir(file), "..", "..", "..")
	for _, name := range []string{"0001_platform.sql", "0185_referral_core.sql", "0198_referral_activity_config.sql", "0199_referral_individual_invitation.sql"} {
		body, readErr := os.ReadFile(filepath.Join(root, "migrations", name))
		if readErr != nil {
			pool.Close()
			admin.Close()
			t.Fatal(readErr)
		}
		if _, execErr := pool.Exec(ctx, string(body)); execErr != nil {
			pool.Close()
			admin.Close()
			t.Fatalf("apply %s: %v", name, execErr)
		}
	}
	if _, err = pool.Exec(ctx, `CREATE TABLE outbox_events (
        id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
        aggregate_type TEXT NOT NULL,
        aggregate_id TEXT NOT NULL,
        event_type TEXT NOT NULL,
        event_version SMALLINT NOT NULL,
        idempotency_key TEXT NOT NULL UNIQUE,
        payload_json JSONB NOT NULL,
        occurred_at TIMESTAMPTZ NOT NULL,
        processed_at TIMESTAMPTZ NULL
    )`); err != nil {
		pool.Close()
		admin.Close()
		t.Fatal(err)
	}
	return pool, func() {
		pool.Close()
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_, _ = admin.Exec(cleanup, "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close()
	}
}

func TestPostgreSQLIndividualConfigurationAndInvitationReadback(t *testing.T) {
	h := newReferralPostgreSQLHarness(t)
	defer h.cleanup()
	ctx := context.Background()
	c, err := h.admin.CreateCampaign(ctx, referralport.CreateCampaignCommand{ActorAdminID: 9001, Name: "个人活动", StartsAt: h.clock.Add(-time.Hour), EndsAt: h.clock.Add(24 * time.Hour), TeamMode: referraldomain.TeamModeIndividual, QualificationMode: referraldomain.QualificationFreeSignup, LeaderboardMetric: referraldomain.LeaderboardInvites, IdempotencyKey: "individual-create"})
	if err != nil || c.Config().TeamMode != referraldomain.TeamModeIndividual {
		t.Fatalf("config readback=%+v err=%v", c, err)
	}
	c, err = h.admin.SetCampaignState(ctx, referralport.SetCampaignStateCommand{ActorAdminID: 9001, CampaignID: c.ID, ExpectedVersion: c.Version, Target: referraldomain.CampaignActive, IdempotencyKey: "individual-activate"})
	if err != nil {
		t.Fatal(err)
	}
	h.joinDirect(t, c.ID, 0, 101, "individual-direct")
	token := h.issue(t, c.ID, 101, "individual-invite")
	joined := h.joinInvite(t, c.ID, 102, token, "individual-accept")
	if joined.Participation == nil || joined.Participation.TeamID != 0 || joined.Participation.InviterCustomerID != 101 {
		t.Fatalf("individual invited participation=%+v", joined.Participation)
	}
	board, err := h.service.Leaderboard(ctx, referralport.LeaderboardQuery{CampaignID: c.ID, Kind: referralport.LeaderboardPersonal, Period: referralport.LeaderboardTotal, ViewerCustomerID: 101, Limit: 20})
	if err != nil || len(board.Items) != 1 || board.Items[0].Score != 1 {
		t.Fatalf("personal board=%+v err=%v", board, err)
	}
	assertCount(t, h.pool, "SELECT count(*) FROM referral_participations WHERE campaign_id=$1 AND team_id IS NULL", 2, c.ID)
}

func TestPostgreSQLProductCampaignConfigRoundtrip(t *testing.T) {
	h := newReferralPostgreSQLHarness(t)
	defer h.cleanup()
	ctx := context.Background()
	c, err := h.admin.CreateCampaign(ctx, referralport.CreateCampaignCommand{ActorAdminID: 9001, Name: "商品活动", StartsAt: h.clock.Add(time.Hour), EndsAt: h.clock.Add(24 * time.Hour), TeamMode: referraldomain.TeamModeIndividual, QualificationMode: referraldomain.QualificationProductPurchase, ProductID: 51, ProductType: referraldomain.ProductTypeStandard, LeaderboardMetric: referraldomain.LeaderboardSalesAmount, IdempotencyKey: "product-config-create"})
	if err != nil || c.Config().ProductID != 51 || c.Config().QualificationMode != referraldomain.QualificationProductPurchase || c.Config().TeamMode != referraldomain.TeamModeIndividual {
		t.Fatalf("create readback=%+v err=%v", c, err)
	}
	next, err := h.admin.UpdateCampaign(ctx, referralport.UpdateCampaignCommand{CampaignID: c.ID, ExpectedVersion: c.Version, ActorAdminID: 9001, Name: c.Name, StartsAt: c.StartsAt, EndsAt: c.EndsAt, TeamMode: referraldomain.TeamModeTeam, QualificationMode: referraldomain.QualificationProductPurchase, ProductID: 52, ProductType: referraldomain.ProductTypeStandard, LeaderboardMetric: referraldomain.LeaderboardSalesOrders, IdempotencyKey: "product-config-update"})
	if err != nil || next.Config().ProductID != 52 || next.Config().LeaderboardMetric != referraldomain.LeaderboardSalesOrders || next.Config().TeamMode != referraldomain.TeamModeTeam {
		t.Fatalf("update readback=%+v err=%v", next, err)
	}
}
