package port

import (
	"context"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
)

const AIReviewPlanOperationPrefix = "ai_review_plan:"

// OperationCustomerScopeAuthorizer must use the caller's bound read context.
// It only verifies scope; it never provisions or merges a customer.
type OperationCustomerScopeAuthorizer interface {
	AuthorizeOperationCustomer(context.Context, accessdomain.MachinePrincipal, customerdomain.CustomerID) error
}
