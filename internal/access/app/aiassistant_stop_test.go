package app

import (
	"context"
	"errors"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	port "github.com/qianlan33333-png/AI-CRM-v3/internal/access/port"
	"testing"
)

func TestAIAssistantStopKeepsAdministratorBoundary(t *testing.T) {
	authorizer := AIAssistantAuthorizer{}
	for _, role := range []domain.Role{domain.RoleAdmin, domain.RoleSuperAdmin, domain.RoleViewer} {
		p := domain.Principal{Kind: domain.KindStaff, InternalID: 10, Roles: []domain.Role{role}}
		if role == domain.RoleSuperAdmin {
			p.Kind = domain.KindAdmin
		}
		err := authorizer.AuthorizeAIAssistant(context.Background(), p, port.AIAssistantStop)
		if role == domain.RoleViewer {
			if !errors.Is(err, domain.ErrPermissionDenied) {
				t.Fatal("viewer received stop authority")
			}
		} else if err != nil {
			t.Fatal(err)
		}
	}
}
