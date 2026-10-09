package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
	referraldomain "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/domain"
	referralport "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/port"
)

type review20261009Public struct {
	*publicStub
	campaignReads int
	contextCalls  int
	participation *referraldomain.Participation
	query         referralport.LeaderboardQuery
}

func (s *review20261009Public) ReadPublicCampaign(context.Context, int64) (referralport.CampaignView, error) {
	s.campaignReads++
	now := time.Now().UTC()
	return referralport.CampaignView{CampaignSummary: referralport.CampaignSummary{Campaign: referraldomain.Campaign{
		ID: 7, Name: "synthetic product campaign", State: referraldomain.CampaignActive,
		StartsAt: now.Add(-time.Hour), EndsAt: now.Add(time.Hour), Version: 1,
		CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour),
		TeamMode: referraldomain.TeamModeIndividual, QualificationMode: referraldomain.QualificationProductPurchase,
		ProductID: 23, ProductType: referraldomain.ProductTypeStandard, LeaderboardMetric: referraldomain.LeaderboardSalesAmount,
	}}}, nil
}

func (s *review20261009Public) MyCampaign(context.Context, referralport.TrustedSessionActor, int64) (referralport.MyCampaign, error) {
	return referralport.MyCampaign{Participation: s.participation}, nil
}

func (s *review20261009Public) IssueProductActivityContext(context.Context, referralport.TrustedSessionActor, int64, string) (referralport.ProductActivityContextCredential, error) {
	s.contextCalls++
	return referralport.ProductActivityContextCredential{Token: "rpa_synthetic", ExpiresAt: time.Now().UTC().Add(time.Hour)}, nil
}

func (s *review20261009Public) Leaderboard(_ context.Context, query referralport.LeaderboardQuery) (referralport.LeaderboardPage, error) {
	s.query = query
	return referralport.LeaderboardPage{}, nil
}

type review20261009Promotion struct{ calls int }

func (s *review20261009Promotion) IssuePromotionLink(context.Context, distributionport.IssuePromotionCommand) (distributionport.PromotionLink, error) {
	s.calls++
	return distributionport.PromotionLink{URL: "https://crm.example.test/d/dpc_ABCDEFGHIJKLMNOPQRSTUV", ExpiresAt: time.Now().UTC().Add(time.Hour)}, nil
}

func TestReview20261009ReferralProductMutationsRequireExistingCSRFBoundary(t *testing.T) {
	for _, endpoint := range []string{"product-context", "promotion-link"} {
		t.Run(endpoint, func(t *testing.T) {
			for _, branch := range []string{"missing_origin", "missing_csrf", "mismatched_csrf", "foreign_origin", "valid"} {
				t.Run(branch, func(t *testing.T) {
					public := &review20261009Public{publicStub: &publicStub{}, participation: &referraldomain.Participation{ID: 71, CampaignID: 7, CustomerID: 9, State: referraldomain.ParticipationActive}}
					promotion := &review20261009Promotion{}
					handler := referralTestHandler(t, &publicStub{})
					handler.public, handler.promotion = public, promotion
					handler.activityHandoffKey = []byte(strings.Repeat("k", 32))
					request := httptest.NewRequest(http.MethodPost, "/api/v1/referral/campaigns/7/"+endpoint, nil)
					request.AddCookie(&http.Cookie{Name: "dist", Value: "synthetic-trusted"})
					request.Header.Set("Idempotency-Key", "review-20261009-synthetic-key")
					request.Header.Set("Origin", "https://crm.example.test")
					request.AddCookie(&http.Cookie{Name: "csrf", Value: "synthetic-proof"})
					request.Header.Set("X-CSRF", "synthetic-proof")
					switch branch {
					case "missing_origin":
						request.Header.Del("Origin")
					case "missing_csrf":
						request.Header.Del("X-CSRF")
					case "mismatched_csrf":
						request.Header.Set("X-CSRF", "different-proof")
					case "foreign_origin":
						request.Header.Set("Origin", "https://foreign.example.test")
					}
					response := httptest.NewRecorder()
					handler.ServePublicHTTP(response, request)
					if branch == "valid" {
						if response.Code != http.StatusCreated || public.contextCalls+promotion.calls != 1 {
							t.Fatalf("valid mutation status=%d context_calls=%d promotion_calls=%d body=%s", response.Code, public.contextCalls, promotion.calls, response.Body.String())
						}
						return
					}
					if response.Code != http.StatusForbidden || public.contextCalls != 0 || promotion.calls != 0 || public.campaignReads != 0 {
						t.Errorf("unprotected product mutation status=%d context_calls=%d promotion_calls=%d campaign_reads=%d", response.Code, public.contextCalls, promotion.calls, public.campaignReads)
					}
				})
			}
		})
	}
}

func TestReview20261009ReferralLeaderboardUsesResolvedParticipationOwner(t *testing.T) {
	public := &review20261009Public{publicStub: &publicStub{}, participation: &referraldomain.Participation{ID: 71, CampaignID: 7, CustomerID: 42, State: referraldomain.ParticipationActive}}
	handler := referralTestHandler(t, &publicStub{})
	handler.public = public
	request := httptest.NewRequest(http.MethodGet, "/api/v1/referral/campaigns/7/leaderboard?kind=personal&period=total", nil)
	request.AddCookie(&http.Cookie{Name: "dist", Value: "synthetic-merged-session"})
	response := httptest.NewRecorder()
	handler.ServePublicHTTP(response, request)
	if response.Code != http.StatusOK || public.query.ViewerCustomerID != 42 {
		t.Fatalf("merged member board uses stale session owner: status=%d viewer=%d want participation owner=42", response.Code, public.query.ViewerCustomerID)
	}
}
