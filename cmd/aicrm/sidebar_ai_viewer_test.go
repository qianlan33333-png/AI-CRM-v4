package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/sidebar"
)

type sidebarAIUsers struct {
	user accessdomain.User
	id   int64
}

func (s *sidebarAIUsers) UserByID(_ context.Context, id int64, _ bool) (accessdomain.User, error) {
	s.id = id
	return s.user, nil
}

func TestSidebarAIViewerUsesCurrentCookieAndActiveWeComEmployee(t *testing.T) {
	auth := &fakeAccessAuthentication{principal: accessdomain.Principal{InternalID: 7}}
	users := &sidebarAIUsers{user: accessdomain.User{Active: true, WeComUserID: "sales-A"}}
	resolver := sidebarPrincipalResolver{authentication: auth, users: users, uow: directUnitOfWork{}, corpID: "corp"}
	for _, cookie := range []string{"missing", ""} {
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("Authorization", "Bearer ignored-browser-employee")
		if cookie != "missing" {
			r.AddCookie(&http.Cookie{Name: "aicrm_sidebar_session", Value: cookie})
		}
		if _, err := resolver.VerifyAIViewer(r.Context(), r); !errors.Is(err, sidebar.ErrInvalidContext) {
			t.Fatal("empty session accepted")
		}
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{Name: "aicrm_sidebar_session", Value: "synthetic-session"})
	p, err := resolver.VerifyAIViewer(r.Context(), r)
	if err != nil || p.EmployeeID != "sales-A" || p.CorpID != "corp" || users.id != 7 || auth.session != "synthetic-session" {
		t.Fatal("viewer not from authenticated local user")
	}
	for _, user := range []accessdomain.User{{Active: false, WeComUserID: "sales-A"}, {Active: true}} {
		users.user = user
		if _, err := resolver.VerifyAIViewer(r.Context(), r); err == nil {
			t.Fatal("inactive/unlinked employee accepted")
		}
	}
	auth.err = accessdomain.ErrAuthentication
	if _, err := resolver.VerifyAIViewer(r.Context(), r); err == nil {
		t.Fatal("revoked session accepted")
	}
}
