// Package wecom implements protocol adapters and WeCom-owned state. It is not
// a composition root: callers inject platform transactions and trusted ports.
package wecom

import "errors"

var (
	ErrProviderDisabled = errors.New("wecom provider is disabled")
	ErrSignature        = errors.New("wecom signature invalid")
	ErrCallbackExpired  = errors.New("wecom callback expired")
	ErrCorpMismatch     = errors.New("wecom corp mismatch")
	ErrMalformedXML     = errors.New("wecom callback malformed xml")
	ErrInvalidOAuth     = errors.New("invalid oauth state")
	ErrOpenRedirect     = errors.New("oauth redirect is not allowed")
	ErrInvalidContext   = errors.New("invalid sidebar context token")
	ErrRelationship     = errors.New("active follow relationship is required")
)
