package port

import "context"

type InvitationCode struct {
	ConfigID string `json:"config_id"`
	QRCode   string `json:"qr_code"`
}
type InvitationCodeProvider interface {
	CreateInvitationCode(context.Context, string) (InvitationCode, error)
}

// InvitationPlanCodeProvider owns one stable WeCom join-way configuration.
// Updating its chat list must preserve config_id/qr_code.
type InvitationPlanCodeProvider interface {
	CreateInvitationCodeForGroups(context.Context, []string) (InvitationCode, error)
	UpdateInvitationCodeForGroups(context.Context, string, []string) (InvitationCode, error)
}
