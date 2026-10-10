package http

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	referralport "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/port"
)

func TestReferralLeaderboardHTTPPeriodsReachExistingRankingAcrossScopes(t *testing.T) {
	for _, kind := range []string{"personal", "team", "in_team"} {
		for _, period := range []string{"total", "all", "day", "week"} {
			t.Run(kind+"/"+period, func(t *testing.T) {
				public := &review20261009Public{publicStub: &publicStub{}}
				handler := referralTestHandler(t, &publicStub{})
				handler.public = public
				query := "?kind=" + kind + "&period=" + period
				if kind == "in_team" {
					query += "&team_id=8"
				}
				if period == "day" || period == "week" {
					query += "&date=2026-10-10"
				}
				response := httptest.NewRecorder()
				handler.ServePublicHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/referral/campaigns/7/leaderboard"+query, nil))
				wantPeriod := referralport.LeaderboardPeriod(period)
				if period == "total" {
					wantPeriod = referralport.LeaderboardAll
				}
				if response.Code != http.StatusOK || public.query.CampaignID != 7 || public.query.Kind != referralport.LeaderboardKind(kind) || public.query.Period != wantPeriod {
					t.Fatalf("ranking not reached status=%d query=%+v", response.Code, public.query)
				}
				if kind == "in_team" && public.query.TeamID != 8 {
					t.Fatal("team scope lost")
				}
				if period == "day" || period == "week" {
					want := time.Date(2026, 10, 9, 16, 0, 0, 0, time.UTC)
					if !public.query.Anchor.Equal(want) {
						t.Fatalf("Shanghai date anchor=%v want=%v", public.query.Anchor, want)
					}
				} else if !public.query.Anchor.IsZero() {
					t.Fatal("all-time ranking received date anchor")
				}
			})
		}
	}
	for _, invalid := range []string{
		"kind=personal&period=week",
		"kind=personal&period=week&date=2026-02-30",
		"kind=personal&period=week&date=2026-10-10&period=day",
		"kind=personal&period=week&date=2026-10-10&team_id=8",
		"kind=in_team&period=week&date=2026-10-10",
		"kind=personal&period=month&date=2026-10-10",
	} {
		t.Run("reject/"+invalid, func(t *testing.T) {
			public := &review20261009Public{publicStub: &publicStub{}}
			handler := referralTestHandler(t, &publicStub{})
			handler.public = public
			response := httptest.NewRecorder()
			handler.ServePublicHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/referral/campaigns/7/leaderboard?"+invalid, nil))
			if response.Code != http.StatusBadRequest || public.query.CampaignID != 0 {
				t.Fatalf("malformed ranking reached business application status=%d query=%+v", response.Code, public.query)
			}
		})
	}
}
