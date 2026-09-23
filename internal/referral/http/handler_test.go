package http

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
	referraldomain "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/domain"
	referralport "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/port"
)

type publicStub struct {
	previewCalls int
	joinCalls    int
	issueErr     error
}

func (s *publicStub) ListPublicCampaigns(context.Context) ([]referralport.CampaignSummary, error) {
	return nil, nil
}
func (s *publicStub) ReadPublicCampaign(context.Context, int64) (referralport.CampaignView, error) {
	return referralport.CampaignView{}, nil
}
func (s *publicStub) PreviewInvitation(_ context.Context, _ string) (referralport.InvitationPreview, error) {
	s.previewCalls++
	return referralport.InvitationPreview{Campaign: referralport.CampaignSummary{Campaign: referraldomain.Campaign{ID: 7}}}, nil
}
func (s *publicStub) JoinCampaign(_ context.Context, _ referralport.JoinCampaignCommand) (referralport.MyCampaign, error) {
	s.joinCalls++
	return referralport.MyCampaign{}, nil
}
func (s *publicStub) IssueInvitation(context.Context, referralport.IssueInvitationCommand) (referralport.InvitationLink, error) {
	return referralport.InvitationLink{}, s.issueErr
}
func (s *publicStub) MyCampaign(context.Context, referralport.TrustedSessionActor, int64) (referralport.MyCampaign, error) {
	return referralport.MyCampaign{}, nil
}
func (s *publicStub) ListMyInvites(context.Context, referralport.TrustedSessionActor, int64, string, int32) (referralport.InvitePage, error) {
	return referralport.InvitePage{}, nil
}
func (s *publicStub) Leaderboard(context.Context, referralport.LeaderboardQuery) (referralport.LeaderboardPage, error) {
	return referralport.LeaderboardPage{}, nil
}
func (s *publicStub) IssueProductActivityContext(context.Context, referralport.TrustedSessionActor, int64, string) (string, error) {
	return "rpa_test", nil
}

type adminStub struct{}

func (adminStub) CreateCampaign(context.Context, referralport.CreateCampaignCommand) (referraldomain.Campaign, error) {
	return referraldomain.Campaign{}, nil
}
func (adminStub) UpdateCampaign(context.Context, referralport.UpdateCampaignCommand) (referraldomain.Campaign, error) {
	return referraldomain.Campaign{}, nil
}
func (adminStub) SetCampaignState(context.Context, referralport.SetCampaignStateCommand) (referraldomain.Campaign, error) {
	return referraldomain.Campaign{}, nil
}
func (adminStub) CreateTeam(context.Context, referralport.CreateTeamCommand) (referraldomain.Team, error) {
	return referraldomain.Team{}, nil
}
func (adminStub) ReverseInvitation(context.Context, referralport.ReverseInvitationCommand) error {
	return nil
}
func (adminStub) RecordReward(context.Context, referralport.RecordRewardCommand) (referraldomain.RewardRecord, error) {
	return referraldomain.RewardRecord{}, nil
}
func (adminStub) RevokeInvitation(context.Context, referralport.RevokeInvitationCommand) error {
	return nil
}
func (adminStub) ReadAdminCampaign(context.Context, int64) (referralport.CampaignView, error) {
	return referralport.CampaignView{}, nil
}
func (adminStub) ListAdminCampaigns(context.Context, string, int32) (referralport.AdminCampaignPage, error) {
	return referralport.AdminCampaignPage{}, nil
}
func (adminStub) ListAdminReferrals(context.Context, int64, string, int32) (referralport.AdminReferralPage, error) {
	return referralport.AdminReferralPage{}, nil
}
func (adminStub) ListAdminParticipants(context.Context, referralport.AdminParticipantQuery) (referralport.AdminParticipantPage, error) {
	return referralport.AdminParticipantPage{}, nil
}
func (adminStub) ListAdminInvitations(context.Context, referralport.AdminInvitationQuery) (referralport.AdminInvitationPage, error) {
	return referralport.AdminInvitationPage{}, nil
}
func (adminStub) ListAdminParticipantInvitations(context.Context, referralport.AdminParticipantInvitationQuery) (referralport.AdminInvitationPage, error) {
	return referralport.AdminInvitationPage{}, nil
}
func (adminStub) ListAdminParticipantInvitationsForExport(context.Context, referralport.AdminParticipantInvitationQuery) ([]referralport.AdminInvitationRecord, error) {
	return nil, nil
}
func (adminStub) ListAdminParticipantsForExport(context.Context, referralport.AdminParticipantQuery) ([]referralport.AdminParticipantRecord, error) {
	return nil, nil
}
func (adminStub) ListAdminInvitationsForExport(context.Context, referralport.AdminInvitationQuery) ([]referralport.AdminInvitationRecord, error) {
	return nil, nil
}
func (adminStub) ListRelationshipHistory(context.Context, int64, string, int32) (referralport.RelationshipHistoryPage, error) {
	return referralport.RelationshipHistoryPage{}, nil
}
func (adminStub) ListRewards(context.Context, int64, string, int32) (referralport.RewardPage, error) {
	return referralport.RewardPage{}, nil
}

type sessionStub struct{}

func (sessionStub) Resolve(context.Context, string) (referralport.TrustedSessionActor, error) {
	return referralport.TrustedSessionActor{CustomerID: 9, IdentityID: 8, Channel: "h5_official_account", AppID: "oa", AppScope: "wechat-app:oa", OccurredAt: time.Now()}, nil
}

type bridgeStub struct{ err error }

func (s bridgeStub) BridgePaymentSession(context.Context, string) (string, time.Time, error) {
	return "dist_" + strings.Repeat("a", 43), time.Now().Add(time.Hour), s.err
}

type namesStub struct{}

func (namesStub) DisplayNames(context.Context, []customerdomain.CustomerID) (map[customerdomain.CustomerID]string, error) {
	return map[customerdomain.CustomerID]string{}, nil
}

type productTargetStub struct {
	item productport.ProductOption
	err  error
}

func (s productTargetStub) ReadProductTarget(context.Context, productport.ProductOptionType, productport.ID) (productport.ProductOption, error) {
	return s.item, s.err
}

type securityStub struct{ err error }

func (s securityStub) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	if s.err != nil {
		return accessdomain.Principal{}, s.err
	}
	return accessdomain.Principal{InternalID: 1, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}, nil
}
func (s securityStub) AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error) {
	return accessdomain.Principal{}, s.err
}

func referralTestHandler(t *testing.T, public *publicStub) *Handler {
	t.Helper()
	handler, err := NewHandler(Config{Public: public, Admin: adminStub{}, Sessions: sessionStub{}, Bridge: bridgeStub{}, Names: namesStub{}, Security: securityStub{}, CookieSecure: true, AllowedOrigins: []string{"https://crm.example.test"}, SessionCookieName: "dist", CSRFCookieName: "csrf", CSRFHeader: "X-CSRF"})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func TestInvitationHandoffIsReadOnlyAndCanonical(t *testing.T) {
	public := &publicStub{}
	handler := referralTestHandler(t, public)
	token := "rfi_" + strings.Repeat("A", 43)
	response := httptest.NewRecorder()
	handler.ServePublicHTTP(response, httptest.NewRequest(http.MethodGet, "/referral/invite/"+token, nil))
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/referral?campaign=7&invite="+token || public.previewCalls != 1 || public.joinCalls != 0 {
		t.Fatalf("status=%d location=%q preview=%d join=%d", response.Code, response.Header().Get("Location"), public.previewCalls, public.joinCalls)
	}
}

func TestAddPublicCampaignLinksUsesDedicatedActivityAndProductURLs(t *testing.T) {
	handler := referralTestHandler(t, &publicStub{})
	handler.productTargets = productTargetStub{item: productport.ProductOption{ID: 42, Code: "ai-trial", ProductType: productport.ProductOptionServicePeriod}}
	campaign := referraldomain.Campaign{ID: 7, QualificationMode: referraldomain.QualificationProductPurchase, ProductID: 42, ProductType: referraldomain.ProductTypeServicePeriod}
	response := map[string]any{}
	handler.addPublicCampaignLinks(context.Background(), response, campaign)
	if response["activity_url"] != "/referral?campaign=7" || response["product_url"] != "/s/ai-trial" {
		t.Fatalf("unexpected campaign links: %#v", response)
	}
}

func TestParticipantCSVIncludesSafeRole(t *testing.T) {
	at := time.Date(2026, time.September, 18, 9, 30, 0, 0, time.UTC)
	recorder := httptest.NewRecorder()
	writeParticipantCSV(recorder, []referralport.AdminParticipantRecord{{
		Participation: referraldomain.Participation{ID: 12, CampaignID: 7, CustomerID: 9, TeamID: 4, State: referraldomain.ParticipationActive, JoinedAt: at},
		Team:          referraldomain.Team{ID: 4, CampaignID: 7, Name: "追光战队", CaptainCustomerID: 9, Version: 1, CreatedAt: at, UpdatedAt: at},
	}}, map[customerdomain.CustomerID]string{9: "队长阿青"})
	body := recorder.Body.String()
	if !strings.Contains(body, "角色") || !strings.Contains(body, "队长") {
		t.Fatalf("participant CSV must include the safe captain role: %q", body)
	}
	if strings.Contains(body, "CustomerID") || strings.Contains(body, ",9,") {
		t.Fatalf("participant CSV must not export an internal customer identifier: %q", body)
	}
}

func TestJoinRequiresExplicitSameOriginCSRFAndTrustedSession(t *testing.T) {
	public := &publicStub{}
	handler := referralTestHandler(t, public)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/referral/campaigns/7/participations", strings.NewReader(`{"team_id":3}`))
	response := httptest.NewRecorder()
	handler.ServePublicHTTP(response, request)
	if response.Code != http.StatusForbidden || public.joinCalls != 0 {
		t.Fatalf("status=%d join=%d", response.Code, public.joinCalls)
	}

	request = httptest.NewRequest(http.MethodPost, "/api/v1/referral/campaigns/7/participations", strings.NewReader(`{"team_id":3}`))
	request.Header.Set("Origin", "https://crm.example.test")
	request.Header.Set("X-CSRF", "proof")
	request.Header.Set("Idempotency-Key", strings.Repeat("k", 16))
	request.AddCookie(&http.Cookie{Name: "csrf", Value: "proof"})
	request.AddCookie(&http.Cookie{Name: "dist", Value: "trusted"})
	response = httptest.NewRecorder()
	handler.ServePublicHTTP(response, request)
	if response.Code != http.StatusOK || public.joinCalls != 1 {
		t.Fatalf("status=%d join=%d", response.Code, public.joinCalls)
	}
}

func TestInvitationRequiresTrustedSessionAndActiveParticipation(t *testing.T) {
	public := &publicStub{issueErr: referralport.ErrParticipationRequired}
	handler := referralTestHandler(t, public)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/referral/campaigns/7/invite", nil)
	request.Header.Set("Origin", "https://crm.example.test")
	request.Header.Set("X-CSRF", "proof")
	request.Header.Set("Idempotency-Key", strings.Repeat("k", 16))
	request.AddCookie(&http.Cookie{Name: "csrf", Value: "proof"})
	response := httptest.NewRecorder()
	handler.ServePublicHTTP(response, request)
	if response.Code != http.StatusUnauthorized || !strings.Contains(response.Body.String(), "referral_session_required") {
		t.Fatalf("missing session status=%d body=%s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/api/v1/referral/campaigns/7/invite", nil)
	request.Header.Set("Origin", "https://crm.example.test")
	request.Header.Set("X-CSRF", "proof")
	request.Header.Set("Idempotency-Key", strings.Repeat("k", 16))
	request.AddCookie(&http.Cookie{Name: "csrf", Value: "proof"})
	request.AddCookie(&http.Cookie{Name: "dist", Value: "trusted"})
	response = httptest.NewRecorder()
	handler.ServePublicHTTP(response, request)
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "referral_participation_required") {
		t.Fatalf("unjoined trusted customer status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestFirstReferralBridgeNeedsTrustedPaymentSessionButNoPriorDistributionCSRF(t *testing.T) {
	public := &publicStub{}
	handler := referralTestHandler(t, public)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/referral/session/bridge", nil)
	request.Header.Set("Origin", "https://crm.example.test")
	request.AddCookie(&http.Cookie{Name: "aicrm_payment_session", Value: "payment-session"})
	response := httptest.NewRecorder()
	handler.ServePublicHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var session, csrf bool
	for _, cookie := range response.Result().Cookies() {
		session = session || cookie.Name == "dist"
		csrf = csrf || cookie.Name == "csrf"
	}
	if !session || !csrf {
		t.Fatalf("bridge did not issue both browser credentials: %v", response.Result().Cookies())
	}
}

func TestReferralBridgeClassifiesInvalidTrustedPaymentSessionAsUnauthorized(t *testing.T) {
	handler := referralTestHandler(t, &publicStub{})
	handler.bridge = bridgeStub{err: distributionport.ErrUnauthorized}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/referral/session/bridge", nil)
	request.Header.Set("Origin", "https://crm.example.test")
	request.AddCookie(&http.Cookie{Name: "aicrm_payment_session", Value: "payment-session"})
	response := httptest.NewRecorder()
	handler.ServePublicHTTP(response, request)
	if response.Code != http.StatusUnauthorized || !strings.Contains(response.Body.String(), "payment_session_required") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestAdminRejectsUnauthenticatedRequest(t *testing.T) {
	public := &publicStub{}
	handler := referralTestHandler(t, public)
	handler.security = securityStub{err: errors.New("no session")}
	response := httptest.NewRecorder()
	handler.ServeAdminHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/referral/campaigns", nil))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d", response.Code)
	}
}

func TestHistoryQueryAcceptsOnlyItsCustomerSelectionAndPaging(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/admin/referral/relationship-history?customer_id=7&cursor=next&limit=10", nil)
	customerID, cursor, limit, ok := historyPageQuery(request)
	if !ok || customerID != 7 || cursor != "next" || limit != 10 {
		t.Fatalf("history query customer=%d cursor=%q limit=%d ok=%v", customerID, cursor, limit, ok)
	}
	request = httptest.NewRequest(http.MethodGet, "/api/admin/referral/relationship-history?customer_id=7&next=/admin", nil)
	if _, _, _, ok = historyPageQuery(request); ok {
		t.Fatal("accepted unknown history query key")
	}
}

func TestLeaderboardProfileIDsExcludeTeamOnlyRows(t *testing.T) {
	ids := entryIDs([]referralport.LeaderboardEntry{{TeamID: 7, TeamName: "Team"}, {CustomerID: 9}})
	if len(ids) != 1 || ids[0] != 9 {
		t.Fatalf("profile ids=%v", ids)
	}
}

func TestAdminReferralRoutesUseTheirCanonicalCampaignPrefix(t *testing.T) {
	handler := referralTestHandler(t, &publicStub{})
	tests := []struct {
		method, path, body string
		want               int
	}{
		{http.MethodGet, "/api/admin/referral/campaigns", "", http.StatusOK},
		{http.MethodGet, "/api/admin/referral/campaigns/7", "", http.StatusOK},
		{http.MethodGet, "/api/admin/referral/referrals?campaign_id=7", "", http.StatusOK},
		{http.MethodGet, "/api/admin/referral/relationship-history?customer_id=7", "", http.StatusOK},
		{http.MethodGet, "/api/admin/referral/rewards?campaign_id=7", "", http.StatusOK},
		{http.MethodPost, "/api/admin/referral/campaigns", `{"name":"活动","starts_at":"2026-09-18T00:00:00Z","ends_at":"2026-09-19T00:00:00Z"}`, http.StatusCreated},
		{http.MethodPut, "/api/admin/referral/campaigns/7", `{"expected_version":1,"name":"活动","starts_at":"2026-09-18T00:00:00Z","ends_at":"2026-09-19T00:00:00Z"}`, http.StatusOK},
		{http.MethodPost, "/api/admin/referral/campaigns/7/state", `{"expected_version":1,"target":"active"}`, http.StatusOK},
		{http.MethodPost, "/api/admin/referral/campaigns/7/teams", `{"captain_customer_id":8,"name":"战队"}`, http.StatusCreated},
		{http.MethodPost, "/api/admin/referral/participations/7/reverse", `{"reason":"作弊"}`, http.StatusNoContent},
		{http.MethodPost, "/api/admin/referral/invitations/7/revoke", `{"reason":"失效"}`, http.StatusNoContent},
		{http.MethodPost, "/api/admin/referral/rewards", `{"campaign_id":7,"customer_id":8,"score_event_id":0,"period":"total","reward":"礼品","evidence_reference":"manual"}`, http.StatusCreated},
	}
	for _, test := range tests {
		request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
		if test.method != http.MethodGet {
			request.Header.Set("Idempotency-Key", strings.Repeat("k", 16))
		}
		response := httptest.NewRecorder()
		handler.ServeAdminHTTP(response, request)
		if response.Code != test.want {
			t.Fatalf("%s %s status=%d want=%d body=%s", test.method, test.path, response.Code, test.want, response.Body.String())
		}
	}
}
