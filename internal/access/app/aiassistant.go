package app

import (
	"context"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accessport "github.com/qianlan33333-png/AI-CRM-v3/internal/access/port"
)

// AIAssistantAuthorizer keeps the review surface on the existing Access RBAC
// model. Viewers may inspect, while administrators and super-administrators
// may perform the ordinary review, approval and reconciliation operations.
type AIAssistantAuthorizer struct{}

func (AIAssistantAuthorizer) AuthorizeAIAssistant(_ context.Context, principal domain.Principal, action accessport.AIAssistantAction) error {
	if principal.Validate() != nil || (principal.Kind != domain.KindAdmin && principal.Kind != domain.KindStaff) {
		return domain.ErrAuthentication
	}
	has := func(expected domain.Role) bool {
		for _, role := range principal.Roles {
			if role == expected {
				return true
			}
		}
		return false
	}
	if action == accessport.AIAssistantReconcile {
		if has(domain.RoleAdmin) || principal.IsSuperAdmin() {
			return nil
		}
		return domain.ErrPermissionDenied
	}
	if action == accessport.AIAssistantRead && (has(domain.RoleViewer) || has(domain.RoleAdmin) || principal.IsSuperAdmin()) {
		return nil
	}
	if (action == accessport.AIAssistantReview || action == accessport.AIAssistantApprove) && (has(domain.RoleAdmin) || principal.IsSuperAdmin()) {
		return nil
	}
	return domain.ErrPermissionDenied
}

var _ accessport.AIAssistantAuthorizer = AIAssistantAuthorizer{}
