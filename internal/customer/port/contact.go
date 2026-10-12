package port

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	addressport "github.com/qianlan33333-png/AI-CRM-v3/internal/address/port"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
)

// ShippingContact is business contact data, never identity evidence.
type ShippingContact struct {
	MobileE164    string `json:"mobile_e164"`
	RecipientName string `json:"recipient_name"`
	ProvinceCode  string `json:"province_code"`
	ProvinceName  string `json:"province_name"`
	CityCode      string `json:"city_code"`
	CityName      string `json:"city_name"`
	DistrictCode  string `json:"district_code"`
	DistrictName  string `json:"district_name"`
	DetailAddress string `json:"detail_address"`
}

func NormalizeContactPhone(value string) (string, bool) {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "+86")
	if len(value) != 11 || value[0] != '1' || value[1] < '3' || value[1] > '9' {
		return "", false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return "", false
		}
	}
	return "+86" + value, true
}

func ValidShippingContact(v ShippingContact) bool {
	_, phoneOK := NormalizeContactPhone(v.MobileE164)
	return phoneOK && utf8.ValidString(v.RecipientName) && utf8.ValidString(v.DetailAddress) && strings.TrimSpace(v.RecipientName) != "" && strings.TrimSpace(v.DetailAddress) != "" && addressport.Validate(v.ProvinceCode, v.ProvinceName, v.CityCode, v.CityName, v.DistrictCode, v.DistrictName) == nil
}

type ContactProfile struct {
	MobileE164 string           `json:"mobile_e164,omitempty"`
	Shipping   *ShippingContact `json:"shipping_address,omitempty"`
}

type ContactUpdate struct {
	CustomerID customerdomain.CustomerID
	MobileE164 string
	Shipping   *ShippingContact
	Source     string
	// SourceAt is used only by fill-empty historical initialization. New writes
	// are ordered under the row lock using the server clock, not request time.
	SourceAt    time.Time
	OnlyIfEmpty bool
}

// ContactWriter must be called only for a newly accepted source record, inside
// the same UoW, after the source's idempotency check. It does not attach identities.
type ContactWriter interface {
	UpdateContactWithin(context.Context, ContactUpdate) error
}

// Reads current OneID lineage; callers must establish the authorized subject.
type ContactReader interface {
	ReadContactWithin(context.Context, customerdomain.CustomerID, bool) (ContactProfile, error)
}

// ContactHistory is an internal maintenance fact. Never serialize its Update
// into logs or reports; only Cursor and SkipReason are suitable for reporting.
type ContactHistory struct {
	Cursor     int64
	Update     ContactUpdate
	SkipReason string
}
