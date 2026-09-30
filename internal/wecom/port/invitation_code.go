package port

import (
	"context"
	"unicode/utf8"
)

// InvitationJoinWayOptions describes the native QR join-way contract (scene=2).
// Group selection and automatic creation remain owned by WeCom.
type InvitationJoinWayOptions struct {
	AutoCreateRoom bool   `json:"auto_create_room"`
	RoomBaseName   string `json:"room_base_name"`
	RoomBaseID     int    `json:"room_base_id"`
	Remark         string `json:"remark"`
	State          string `json:"state"`
}

func (o InvitationJoinWayOptions) Valid() bool {
	return utf8.ValidString(o.RoomBaseName) && utf8.RuneCountInString(o.RoomBaseName) <= 40 &&
		utf8.ValidString(o.Remark) && utf8.RuneCountInString(o.Remark) <= 30 &&
		utf8.ValidString(o.State) && utf8.RuneCountInString(o.State) <= 30
}

type NativeInvitationCodeProvider interface {
	CreateNativeInvitationCode(context.Context, []string, InvitationJoinWayOptions) (InvitationCode, error)
	UpdateNativeInvitationCode(context.Context, string, []string, InvitationJoinWayOptions) (InvitationCode, error)
}

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
