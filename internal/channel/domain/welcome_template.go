package domain

import (
	"errors"
	"strings"
	"unicode/utf8"
)

// WelcomeCustomerNameVariable is the only supported Channel welcome template
// variable.  It is deliberately exact: aliases and whitespace variants would
// make a saved message ambiguous after it has become an outbound intent.
const WelcomeCustomerNameVariable = "{{客户名}}"

const WelcomeCustomerNameFallback = "朋友"

// WelcomeMessageMaxRunes is the text bound enforced by the configured WeCom
// welcome-message writer. Catalog permits a longer draft, but the frozen
// Provider body must obey this limit before a one-time welcome grant is read.
const WelcomeMessageMaxRunes = 4000

var ErrInvalidWelcomeTemplate = errors.New("invalid channel welcome template")
var ErrWelcomeMessageTooLong = errors.New("channel welcome message exceeds provider limit")

// ValidateWelcomeMessageTemplate accepts plain text and the exact customer
// name marker. It rejects all other double-brace constructs at save time so a
// legacy unknown variable is never silently removed or sent literally.
func ValidateWelcomeMessageTemplate(message string) error {
	remaining := strings.ReplaceAll(message, WelcomeCustomerNameVariable, "")
	if strings.Contains(remaining, "{{") || strings.Contains(remaining, "}}") {
		return ErrInvalidWelcomeTemplate
	}
	return nil
}

// RenderWelcomeMessage performs one non-recursive replacement. A customer
// display name is presentation data, never another template source.
func RenderWelcomeMessage(template, displayName string) (string, error) {
	if err := ValidateWelcomeMessageTemplate(template); err != nil {
		return "", err
	}
	if strings.TrimSpace(displayName) == "" {
		displayName = WelcomeCustomerNameFallback
	}
	return strings.ReplaceAll(template, WelcomeCustomerNameVariable, displayName), nil
}

// ValidateRenderedWelcomeMessage verifies the exact text that would reach the
// Provider. It intentionally does not truncate either configuration or a
// customer name, because truncation would make a successful receipt attest to
// content different from the accepted intent.
func ValidateRenderedWelcomeMessage(message string) error {
	if !utf8.ValidString(message) || utf8.RuneCountInString(message) > WelcomeMessageMaxRunes {
		return ErrWelcomeMessageTooLong
	}
	return nil
}
