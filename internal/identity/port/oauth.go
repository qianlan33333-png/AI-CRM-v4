package port

import (
	"context"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
)

// OAuthSubjectCommand contains two identities verified in one Provider
// userinfo exchange, never claims submitted by the browser.
type OAuthSubjectCommand struct {
	OpenID  domain.VerifiedFact
	UnionID domain.VerifiedFact
	EventID string
}
type OAuthSubjectResult struct {
	ProvisionResult
	Conflict bool
}
type VerifiedOAuthSubjectProvisioner interface {
	ProvisionVerifiedOAuthSubject(context.Context, OAuthSubjectCommand) (OAuthSubjectResult, error)
}
