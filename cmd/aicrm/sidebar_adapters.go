package main

import (
	"context"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/sidebar"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/wecom"
)

type sidebarContextAdapter struct{ tokens wecom.ContextTokenService }

func (adapter sidebarContextAdapter) VerifySidebarContext(ctx context.Context, token string) (sidebar.Principal, customerdomain.CustomerID, error) {
	principal, customerID, err := adapter.tokens.Verify(ctx, token)
	return sidebar.Principal{CorpID: principal.CorpID, EmployeeID: principal.EmployeeID}, customerID, err
}
