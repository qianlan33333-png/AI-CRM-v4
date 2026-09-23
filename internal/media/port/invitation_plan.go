package port

import (
	"context"
	"errors"
	"time"
)

var ErrInvitationVersion = errors.New("invitation plan version changed")

type InvitationBinding struct {
	ChatID    string `json:"chat_id"`
	Retired   bool   `json:"retired"`
	QRCode    string `json:"qr_code,omitempty"`
	CodeState string `json:"code_state"`
}
type InvitationPlan struct {
	ID               int64               `json:"id"`
	Name             string              `json:"name"`
	Title            string              `json:"title"`
	Description      string              `json:"description"`
	CoverImageID     int64               `json:"cover_image_id"`
	Mode             string              `json:"mode"`
	Threshold        *int                `json:"threshold"`
	Enabled          bool                `json:"enabled"`
	Version          int64               `json:"version"`
	Token            string              `json:"token"`
	JoinURL          string              `json:"join_url"`
	State            string              `json:"state"`
	CurrentChatID    string              `json:"current_chat_id"`
	Bindings         []InvitationBinding `json:"bindings"`
	ProviderConfigID string              `json:"provider_config_id,omitempty"`
	ProviderQRCode   string              `json:"provider_qr_code,omitempty"`
	ProviderState    string              `json:"provider_state,omitempty"`
}
type InvitationInput struct {
	ID           int64    `json:"id"`
	Version      int64    `json:"version"`
	Name         string   `json:"name"`
	Title        string   `json:"title"`
	Description  string   `json:"description"`
	CoverImageID int64    `json:"cover_image_id"`
	Mode         string   `json:"mode"`
	Threshold    *int     `json:"threshold"`
	Enabled      bool     `json:"enabled"`
	ChatIDs      []string `json:"chat_ids"`
}
type InvitationSwitch struct {
	From string    `json:"from"`
	To   string    `json:"to"`
	At   time.Time `json:"at"`
}
type InvitationCodeIntent struct {
	ChatID       string
	SourceDigest string
	EffectID     string
}
type InvitationPlanCodeIntent struct {
	InviteID     int64
	ChatIDs      []string
	SourceDigest string
	EffectID     string
	ConfigID     string
}
type InvitationCodeCompletion struct {
	EffectID string
	State    string
	ConfigID string
	QRCode   string
}
type InvitationCodeStore interface {
	ReadInvitationCodeIntent(context.Context, string) (InvitationCodeIntent, error)
	CompleteInvitationCode(context.Context, InvitationCodeCompletion) error
}
type InvitationPlanCodeStore interface {
	ReadInvitationPlanCodeIntent(context.Context, string) (InvitationPlanCodeIntent, error)
	CompleteInvitationPlanCode(context.Context, InvitationCodeCompletion) error
}
