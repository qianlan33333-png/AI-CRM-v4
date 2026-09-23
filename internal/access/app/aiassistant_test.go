package app

import (
	"context"
	"errors"
	"testing"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accessport "github.com/qianlan33333-png/AI-CRM-v3/internal/access/port"
)

func TestAIAssistantAuthorizationMatrix(t *testing.T) {
	a := AIAssistantAuthorizer{}
	viewer := domain.Principal{Kind: domain.KindAdmin, InternalID: 1, Roles: []domain.Role{domain.RoleViewer}}
	admin := domain.Principal{Kind: domain.KindAdmin, InternalID: 2, Roles: []domain.Role{domain.RoleAdmin}}
	super := domain.Principal{Kind: domain.KindAdmin, InternalID: 3, Roles: []domain.Role{domain.RoleSuperAdmin}}
	if err := a.AuthorizeAIAssistant(context.Background(), viewer, accessport.AIAssistantRead); err != nil {
		t.Fatal(err)
	}
	if err := a.AuthorizeAIAssistant(context.Background(), viewer, accessport.AIAssistantReview); !errors.Is(err, domain.ErrPermissionDenied) {
		t.Fatalf("viewer review = %v", err)
	}
	if err := a.AuthorizeAIAssistant(context.Background(), admin, accessport.AIAssistantApprove); err != nil {
		t.Fatal(err)
	}
	if err := a.AuthorizeAIAssistant(context.Background(), admin, accessport.AIAssistantReconcile); err != nil {
		t.Fatalf("admin reconcile = %v", err)
	}
	if err := a.AuthorizeAIAssistant(context.Background(), super, accessport.AIAssistantReconcile); err != nil {
		t.Fatal(err)
	}
}
