package port

import (
	"context"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
)

// SidebarProfile is the Customer-owned safe projection. It deliberately
// contains neither raw external identifiers nor a clear-text phone.
type SidebarProfile struct {
	CustomerNumber        string                    `json:"customer_number,omitempty"`
	CustomerID            customerdomain.CustomerID `json:"customer_id"`
	DisplayName           string                    `json:"display_name"`
	AvatarURL             string                    `json:"avatar_url,omitempty"`
	PhoneMasked           string                    `json:"phone_masked,omitempty"`
	PhoneAssurance        string                    `json:"phone_assurance,omitempty"`
	Status                string                    `json:"status"`
	ActivationState       string                    `json:"activation_status"`
	Gender                int16                     `json:"gender,omitempty"`
	ContactType           int16                     `json:"contact_type,omitempty"`
	CorpName              string                    `json:"corp_name,omitempty"`
	Source                string                    `json:"source,omitempty"`
	ProfileSource         string                    `json:"profile_source,omitempty"`
	ProfileVersion        int64                     `json:"profile_version"`
	Industry              string                    `json:"industry,omitempty"`
	IndustryDescription   string                    `json:"industry_description,omitempty"`
	NeedsBlockersFollowup string                    `json:"needs_blockers_followup,omitempty"`
	Version               int64                     `json:"version"`
	LastSyncedAt          *time.Time                `json:"last_synced_at,omitempty"`
	UpdatedAt             time.Time                 `json:"updated_at"`
}

type SidebarProfileUpdate struct {
	CustomerID               customerdomain.CustomerID
	EmployeeID               string
	DisplayName              string
	Gender                   int16
	CorpName                 string
	ExpectedVersion          int64
	ExpectedProfileVersion   int64
	IdempotencyKey           string
	ProfileSource            string
	Industry                 string
	IndustryDescription      string
	NeedsBlockersFollowup    string
	SourceSet                bool
	IndustrySet              bool
	IndustryDescriptionSet   bool
	NeedsBlockersFollowupSet bool
}

type SidebarPhoneBind struct {
	CustomerID     customerdomain.CustomerID
	EmployeeID     string
	Phone          string
	IdempotencyKey string
}

type SidebarPhoneResult struct {
	Status         string `json:"status"`
	PhoneMasked    string `json:"phone_masked,omitempty"`
	PhoneAssurance string `json:"phone_assurance,omitempty"`
}

type SidebarProfileService interface {
	ReadSidebarProfile(context.Context, customerdomain.CustomerID) (SidebarProfile, error)
	UpdateSidebarProfile(context.Context, SidebarProfileUpdate) (SidebarProfile, error)
	BindSidebarPhone(context.Context, SidebarPhoneBind) (SidebarPhoneResult, error)
}
