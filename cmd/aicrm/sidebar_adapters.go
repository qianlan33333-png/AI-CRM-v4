package main

import (
	"context"
	"net/http"
	"strings"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/sidebar"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/wecom"
)

type sidebarContextAdapter struct{ tokens wecom.ContextTokenService }

func (adapter sidebarContextAdapter) VerifySidebarContext(ctx context.Context, token string) (sidebar.Principal, customerdomain.CustomerID, error) {
	principal, customerID, err := adapter.tokens.Verify(ctx, token)
	return sidebar.Principal{CorpID: principal.CorpID, EmployeeID: principal.EmployeeID}, customerID, err
}

func (adapter sidebarPrincipalResolver) VerifyAIViewer(ctx context.Context, request *http.Request) (sidebar.Principal, error) {
	cookie, err := request.Cookie("aicrm_sidebar_session")
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		return sidebar.Principal{}, sidebar.ErrInvalidContext
	}
	p, err := adapter.SidebarPrincipal(ctx, cookie.Value)
	return sidebar.Principal{CorpID: p.CorpID, EmployeeID: p.EmployeeID}, err
}
