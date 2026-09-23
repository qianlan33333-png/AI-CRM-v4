package port

import (
	"context"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
)

type AIAssistantAction string

const (
	AIAssistantRead      AIAssistantAction = "aiassistant.read"
	AIAssistantReview    AIAssistantAction = "aiassistant.review"
	AIAssistantApprove   AIAssistantAction = "aiassistant.approve"
	AIAssistantReconcile AIAssistantAction = "aiassistant.reconcile"
)

// AIAssistantAuthorizer is the narrow Access-owned permission seam used by
// the AI Assistant HTTP adapter. It does not expose sessions or Access stores.
type AIAssistantAuthorizer interface {
	AuthorizeAIAssistant(context.Context, accessdomain.Principal, AIAssistantAction) error
}
