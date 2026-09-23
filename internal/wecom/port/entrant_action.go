package port

import (
	"context"
	"errors"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
)

var ErrWelcomeGrantExpired = errors.New("welcome grant expired")

type CurrentExternalContact struct {
	EmployeeUserID, ExternalUserID string
}

type CurrentExternalContactReader interface {
	CurrentExternalContact(context.Context, customerdomain.CustomerID, int64) (CurrentExternalContact, error)
}

type WelcomeAttachment struct {
	MsgType, MediaID, AppID, PagePath, Title, URL, Description, PicURL string
}

type WelcomeGrantRedeemer interface {
	Redeem(context.Context, string, string) (string, error)
}

type EntrantActionWriter interface {
	SendWelcomeMessage(context.Context, string, string, []WelcomeAttachment) error
	AddContactTag(context.Context, string, string, string) error
}

// CustomerTagWriter is the narrow Provider-write boundary for the generic
// Customer command. It carries only trusted values from outbound adapters.
type CustomerTagWriter interface {
	MarkContactTags(context.Context, string, string, []string, []string) error
}
