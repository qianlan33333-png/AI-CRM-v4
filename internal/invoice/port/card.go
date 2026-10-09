package port

import (
	"context"
	"net/url"
	"strings"
	"unicode/utf8"
)

// Configuration materials have no customer identity. Queue/result state belongs
// exclusively to External Effects, not a second setup state machine.
type CardConfig struct {
	MerchantID string `json:"merchant_id"`
	AppID      string `json:"app_id"`
	LogoURL    string `json:"logo_url"`
	PayeeName  string `json:"payee_name"`
	JumpURL    string `json:"jump_url"`
	Confirmed  bool   `json:"confirmed"`
}

func (c CardConfig) Valid() bool {
	logo, e := url.Parse(c.LogoURL)
	jump, j := url.Parse(c.JumpURL)
	return c.Confirmed && c.MerchantID != "" && c.AppID != "" && utf8.RuneCountInString(c.PayeeName) <= 32 && e == nil && (logo.Scheme == "https" || logo.Scheme == "http") && logo.Host == "mmbiz.qpic.cn" && logo.User == nil && logo.Fragment == "" && strings.HasPrefix(logo.Path, "/mmbiz") && len(c.LogoURL) <= 1024 && j == nil && jump.Scheme == "https" && jump.Host != "" && jump.User == nil && jump.Path == "/my/orders"
}

type CardEffectMaterial interface {
	PrepareCardEffect(context.Context, string) (CardConfig, error)
}
type CardSetup struct {
	ConfigurationApplied bool   `json:"configuration_applied"`
	EffectID             string `json:"effect_id,omitempty"`
	State                string `json:"state"`
	CardID               string `json:"card_id,omitempty"`
	FailureCode          string `json:"failure_code,omitempty"`
	LogoURL              string `json:"logo_url,omitempty"`
}
type CardSetupAdmin interface {
	ReadCardSetup(context.Context) (CardSetup, error)
	AcceptCardSetup(context.Context, int64) (CardSetup, error)
}
