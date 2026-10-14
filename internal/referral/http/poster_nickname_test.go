package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	referralport "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/port"
)

type posterIssuerStub struct {
	*publicStub
	root  int64
	calls int
}

func (s *posterIssuerStub) IssueInvitation(context.Context, referralport.IssueInvitationCommand) (referralport.InvitationLink, error) {
	s.calls++
	return referralport.InvitationLink{URL: "https://crm.example.test/referral/invite/rfi_" + strings.Repeat("A", 43), ExpiresAt: time.Now(), InviterCanonicalCustomerID: s.root}, s.issueErr
}

type posterNicknameStub struct {
	ids  []customerdomain.CustomerID
	err  error
	name string
}

func (s *posterNicknameStub) ReadWeChatNickname(_ context.Context, id customerdomain.CustomerID) (string, error) {
	s.ids = append(s.ids, id)
	return s.name, s.err
}

func TestPosterNicknameUsesIssuedRootAndNeverDisclosesCustomerID(t *testing.T) {
	for _, tc := range []struct {
		name     string
		root     int64
		nickname string
		err      error
	}{
		{"canonical-root", 12, "微信昵称 👩‍💻", nil},
		{"another-user", 19, "另一用户", nil},
		{"empty", 12, "", nil},
		{"blank", 12, " \n", nil},
		{"read-failed", 12, "运营备注不得使用", errors.New("unavailable")},
		{"legacy-issuer", 0, "不得读取", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := referralTestHandler(t, &publicStub{})
			issuer := &posterIssuerStub{publicStub: &publicStub{}, root: tc.root}
			names := &posterNicknameStub{name: tc.nickname, err: tc.err}
			h.public, h.nicknames = issuer, names
			req := httptest.NewRequest(http.MethodPost, "/api/v1/referral/campaigns/7/invite", nil)
			req.Header.Set("Origin", "https://crm.example.test")
			req.Header.Set("Idempotency-Key", "poster-nickname-test")
			req.Header.Set("X-CSRF", "test-csrf")
			req.AddCookie(&http.Cookie{Name: "dist", Value: "session"})
			req.AddCookie(&http.Cookie{Name: "csrf", Value: "test-csrf"})
			res := httptest.NewRecorder()
			h.ServePublicHTTP(res, req)
			var body map[string]any
			if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil || res.Code != 200 || issuer.calls != 1 {
				t.Fatalf("status=%d response=%s err=%v", res.Code, res.Body.String(), err)
			}
			if tc.root > 0 && (len(names.ids) != 1 || names.ids[0] != customerdomain.CustomerID(tc.root)) {
				t.Fatalf("nickname used session alias instead of issued root: %v", names.ids)
			}
			if tc.root == 0 && len(names.ids) != 0 {
				t.Fatal("legacy issuer caused guessed lookup")
			}
			if tc.root > 0 && tc.err == nil && strings.TrimSpace(tc.nickname) != "" {
				if body["poster_nickname"] != tc.nickname {
					t.Fatalf("nickname=%v", body["poster_nickname"])
				}
			} else if _, found := body["poster_nickname"]; found {
				t.Fatal("unavailable nickname must be omitted")
			}
			for key := range body {
				if key != "url" && key != "expires_at" && key != "poster_nickname" {
					t.Fatalf("unexpected public field %q", key)
				}
			}
		})
	}
}

func TestPosterNicknameDoesNotReadBeforeSessionCSRFAndIssuanceAcceptance(t *testing.T) {
	for _, scenario := range []string{"no-session", "no-csrf", "issuance-rejected"} {
		t.Run(scenario, func(t *testing.T) {
			h := referralTestHandler(t, &publicStub{})
			issuer := &posterIssuerStub{publicStub: &publicStub{}, root: 12}
			names := &posterNicknameStub{name: "微信昵称"}
			h.public, h.nicknames = issuer, names
			req := httptest.NewRequest(http.MethodPost, "/api/v1/referral/campaigns/7/invite", nil)
			req.Header.Set("Origin", "https://crm.example.test")
			req.Header.Set("Idempotency-Key", "poster-nickname-denied")
			if scenario != "no-session" {
				req.AddCookie(&http.Cookie{Name: "dist", Value: "session"})
			}
			if scenario != "no-csrf" {
				req.AddCookie(&http.Cookie{Name: "csrf", Value: "test-csrf"})
				req.Header.Set("X-CSRF", "test-csrf")
			}
			if scenario == "issuance-rejected" {
				issuer.issueErr = referralport.ErrParticipationRequired
			}
			res := httptest.NewRecorder()
			h.ServePublicHTTP(res, req)
			if res.Code < 400 || len(names.ids) != 0 {
				t.Fatalf("status=%d reads=%v", res.Code, names.ids)
			}
		})
	}
}
