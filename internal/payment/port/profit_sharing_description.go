package port

import (
	"context"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ProfitSharingDescriptionReader reads presentation facts in the caller's
// Payment UoW. These trusted IDs come from the original Payment; text cannot
// select or change the receiver, buyer, amount, or Order.
type ProfitSharingDescriptionReader interface {
	ReadProfitSharingDescriptionWithin(context.Context, int64, int64) (string, error)
}

const LegacyProfitSharingDescription = "distribution commission"
const ProfitSharingDescriptionMaxBytes = 80

func ValidProfitSharingDescription(value string) bool {
	return value != "" && utf8.ValidString(value) && len(value) <= ProfitSharingDescriptionMaxBytes && strings.TrimSpace(value) == value && strings.IndexFunc(value, unicode.IsControl) == -1
}

// FormatProfitSharingPurchaseDescription keeps both subjects and the requested
// Chinese wording. Long display text is visibly abbreviated at rune boundaries
// to fit the provider's string(80) field (also within 80 UTF-8 bytes).
func FormatProfitSharingPurchaseDescription(nickname, productName string) string {
	clean := func(value, fallback string) string {
		value = strings.TrimSpace(strings.Map(func(r rune) rune {
			if unicode.IsControl(r) || r == utf8.RuneError {
				return -1
			}
			return r
		}, value))
		if value == "" {
			return fallback
		}
		return value
	}
	nickname, productName = clean(nickname, "微信用户"), clean(productName, "商品")
	if value := nickname + "购买" + productName + "分账"; len(value) <= ProfitSharingDescriptionMaxBytes {
		return value
	}
	budget := ProfitSharingDescriptionMaxBytes - len("购买分账")
	nicknameBudget := budget / 2
	if len(productName) < budget-nicknameBudget {
		nicknameBudget = budget - len(productName)
	}
	nickname = abbreviateProfitSharingText(nickname, nicknameBudget)
	productName = abbreviateProfitSharingText(productName, budget-len(nickname))
	return nickname + "购买" + productName + "分账"
}

func abbreviateProfitSharingText(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	end := 0
	for index, r := range value {
		if index+utf8.RuneLen(r) > limit-len("…") {
			break
		}
		end = index + utf8.RuneLen(r)
	}
	return value[:end] + "…"
}
