package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
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
	referralhttp "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/http"
	referralport "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/port"
	referralstore "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/store"
)

func TestReview20261009ReferralHTTPLeaderboardTracksRealOneIDMergeAndSplit(t *testing.T) {
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
	queries := identityquery.NewPostgreSQL()
	if err = sessions.BindIdentitySubjects(queries); err != nil {
		t.Fatal(err)
	}
	audit, err := platformaudit.NewService(platformaudit.NewPostgreSQLStore())
	if err != nil {
		t.Fatal(err)
	}
	service, err := referralapp.NewService(uow, repository, "https://crm.example.test", base64.RawStdEncoding.EncodeToString(make([]byte, 32)), sol61ReferralCloseSink{}, audit, platformoutbox.NewPostgreSQL())
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
	scope := "wechat-app:review-20261009-referral"
	oldFact := evidenceMergeFact(t, identitydomain.KindMPOpenID, scope, "synthetic-review-referral-old")
	survivorFact := evidenceMergeFact(t, identitydomain.KindWeComExternalUserID, "wecom-corp:review-20261009-referral", "synthetic-review-referral-survivor")
	inviteeFact := evidenceMergeFact(t, identitydomain.KindMPOpenID, scope, "synthetic-review-referral-invitee")
	observerFact := evidenceMergeFact(t, identitydomain.KindMPOpenID, scope, "synthetic-review-referral-observer")
	var old, survivor, invitee, observer identityport.ProvisionResult
	if err = uow.Within(ctx, func(tx context.Context) error {
		var e error
		if old, e = oneID.ProvisionCustomerFromVerifiedIdentity(tx, oldFact); e != nil {
			return e
		}
		if survivor, e = oneID.ProvisionCustomerFromVerifiedIdentity(tx, survivorFact); e != nil {
			return e
		}
		if invitee, e = oneID.ProvisionCustomerFromVerifiedIdentity(tx, inviteeFact); e != nil {
			return e
		}
		observer, e = oneID.ProvisionCustomerFromVerifiedIdentity(tx, observerFact)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	actor := func(subject identityport.ProvisionResult) distributionport.TrustedSessionActor {
		return distributionport.TrustedSessionActor{CustomerID: int64(subject.CustomerID), IdentityID: subject.IdentityID, Channel: "mini_program", AppID: "review-20261009-referral", AppScope: scope, OccurredAt: time.Now().UTC()}
	}
	issueSession := func(subject identityport.ProvisionResult) string {
		t.Helper()
		token, _, e := sessions.Issue(ctx, actor(subject))
		if e != nil {
			t.Fatal(e)
		}
		return token
	}
	oldToken, survivorToken, observerToken := issueSession(old), issueSession(survivor), issueSession(observer)
	at := time.Now().UTC()
	campaign, err := admin.CreateCampaign(ctx, referralport.CreateCampaignCommand{ActorAdminID: 9001, Name: "synthetic referral board ownership", StartsAt: at.Add(-time.Hour), EndsAt: at.Add(time.Hour), TeamMode: referraldomain.TeamModeIndividual, QualificationMode: referraldomain.QualificationFreeSignup, LeaderboardMetric: referraldomain.LeaderboardInvites, IdempotencyKey: "review-20261009-board-create"})
	if err != nil {
		t.Fatal(err)
	}
	campaign, err = admin.SetCampaignState(ctx, referralport.SetCampaignStateCommand{ActorAdminID: 9001, CampaignID: campaign.ID, ExpectedVersion: campaign.Version, Target: referraldomain.CampaignActive, IdempotencyKey: "review-20261009-board-activate"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.JoinCampaign(ctx, referralport.JoinCampaignCommand{Actor: actor(old), CampaignID: campaign.ID, IdempotencyKey: "review-20261009-board-promoter-join"}); err != nil {
		t.Fatal(err)
	}
	link, err := service.IssueInvitation(ctx, referralport.IssueInvitationCommand{Actor: actor(old), CampaignID: campaign.ID, IdempotencyKey: "review-20261009-board-invitation"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.JoinCampaign(ctx, referralport.JoinCampaignCommand{Actor: actor(invitee), CampaignID: campaign.ID, InvitationToken: link.URL[strings.LastIndex(link.URL, "/")+1:], IdempotencyKey: "review-20261009-board-invitee-join"}); err != nil {
		t.Fatal(err)
	}
	handler, err := referralhttp.NewHandler(referralhttp.Config{Public: service, Admin: admin, Sessions: sessions, Bridge: review20261009ReferralUnusedBridge{}, Names: review20261009ReferralNames{}, Security: review20261009ReferralUnusedSecurity{}, AllowedOrigins: []string{"https://crm.example.test"}, SessionCookieName: "dist", CSRFCookieName: "csrf", CSRFHeader: "X-CSRF"})
	if err != nil {
		t.Fatal(err)
	}
	assertBoard := func(token string, owns bool) {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, "/api/v1/referral/campaigns/"+strconv.FormatInt(campaign.ID, 10)+"/leaderboard?kind=personal&period=total", nil).WithContext(ctx)
		request.AddCookie(&http.Cookie{Name: "dist", Value: token})
		response := httptest.NewRecorder()
		handler.ServePublicHTTP(response, request)
		var body struct {
			Items []struct {
				Mine  bool  `json:"mine"`
				Score int64 `json:"score"`
			} `json:"items"`
			MyEntry *struct {
				Mine  bool  `json:"mine"`
				Score int64 `json:"score"`
			} `json:"my_entry"`
		}
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &body) != nil || len(body.Items) != 1 {
			t.Fatalf("board response status=%d body=%s", response.Code, response.Body.String())
		}
		if owns {
			if body.MyEntry == nil || !body.MyEntry.Mine || body.MyEntry.Score != 1 || !body.Items[0].Mine {
				t.Fatalf("trusted lineage lost its original score: %s", response.Body.String())
			}
		} else if body.MyEntry != nil || body.Items[0].Mine {
			t.Fatalf("another current subject received owner-only board markers: %s", response.Body.String())
		}
	}
	assertBoard(oldToken, true)
	assertBoard(survivorToken, false)
	assertBoard(observerToken, false)
	var candidate identityapp.LinkResult
	if err = uow.Within(ctx, func(tx context.Context) error {
		var e error
		candidate, e = oneID.LinkVerifiedIdentity(tx, identityapp.LinkCommand{SourceCustomerID: old.CustomerID, Target: survivorFact, Evidence: identitydomain.LinkEvidence{Type: "test", Strength: identitydomain.EvidenceStrong, Source: "review.referral.synthetic", EventID: "review-20261009-referral-merge", Digest: "synthetic-review-referral-merge", PolicyVersion: "test-v1"}})
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if candidate.Candidate == nil {
		t.Fatalf("synthetic merge candidate missing: status=%s", candidate.Status)
	}
	var mergeID int64
	if err = uow.Within(ctx, func(tx context.Context) error {
		merged, e := oneID.ConfirmMerge(tx, identityapp.ConfirmMergeCommand{CandidateID: candidate.Candidate.ID, SurvivorCustomerID: survivor.CustomerID, Operator: "synthetic-review-referral"})
		if e != nil {
			return e
		}
		if merged.Status != identityapp.LinkMerged || merged.Merge == nil {
			t.Fatalf("synthetic merge not confirmed: %+v", merged)
		}
		mergeID = merged.Merge.ID
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	mergedToken := issueSession(old)
	for _, token := range []string{oldToken, survivorToken, mergedToken} {
		assertBoard(token, true)
	}
	assertBoard(observerToken, false)
	if err = uow.Within(ctx, func(tx context.Context) error { _, e := oneID.RevertConfirmedMerge(tx, mergeID); return e }); err != nil {
		t.Fatal(err)
	}
	assertBoard(oldToken, true)
	assertBoard(mergedToken, true)
	assertBoard(survivorToken, false)
	assertBoard(observerToken, false)
	var participants, credits int
	if err = native.QueryRow(ctx, `SELECT (SELECT count(*) FROM referral_participations WHERE campaign_id=$1),(SELECT count(*) FROM referral_score_events WHERE campaign_id=$1)`, campaign.ID).Scan(&participants, &credits); err != nil || participants != 2 || credits != 1 {
		t.Fatalf("read-only merge/split board changed facts: participants=%d credits=%d err=%v", participants, credits, err)
	}
}

type review20261009ReferralNames struct{}

func (review20261009ReferralNames) DisplayNames(context.Context, []customerdomain.CustomerID) (map[customerdomain.CustomerID]string, error) {
	return map[customerdomain.CustomerID]string{}, nil
}

type review20261009ReferralUnusedBridge struct{}

func (review20261009ReferralUnusedBridge) BridgePaymentSession(context.Context, string) (string, time.Time, error) {
	return "", time.Time{}, referralport.ErrUnavailable
}

type review20261009ReferralUnusedSecurity struct{}

func (review20261009ReferralUnusedSecurity) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	return accessdomain.Principal{}, referralport.ErrUnauthorized
}

func (review20261009ReferralUnusedSecurity) AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error) {
	return accessdomain.Principal{}, referralport.ErrUnauthorized
}
