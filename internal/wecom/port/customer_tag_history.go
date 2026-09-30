package port

import (
	"context"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	"time"
)

type DirectoryPublication struct {
	Revision            int64     `json:"revision"`
	RunID               int64     `json:"run_id"`
	ObservedAt          time.Time `json:"observed_at"`
	FullObservedAt      time.Time `json:"full_observed_at"`
	Complete            bool      `json:"complete"`
	BaselineInitialized bool      `json:"baseline_initialized"`
	BaselineDate        string    `json:"baseline_date"`
}
type DirectoryPublicationReader interface {
	DirectoryPublication(context.Context, string) (DirectoryPublication, error)
}
type DirectoryPublicationNotifier interface {
	DirectoryPublishedWithin(context.Context, DirectoryPublication) error
}
type DirectoryTag struct {
	ID              string     `json:"tag_id"`
	Name            string     `json:"name"`
	GroupName       string     `json:"group_name"`
	Type            int16      `json:"type"`
	Status          string     `json:"status"`
	ObservedAt      time.Time  `json:"observed_at"`
	PeriodStartedAt *time.Time `json:"period_started_at,omitempty"`
	PeriodStartKind *string    `json:"period_start_kind,omitempty"`
	BaselineDate    *string    `json:"baseline_date,omitempty"`
}
type DirectoryFollow struct {
	EmployeeID   string                    `json:"employee_id"`
	EmployeeName string                    `json:"employee_name"`
	Status       string                    `json:"status"`
	ObservedAt   time.Time                 `json:"observed_at"`
	Details      ExternalContactFollowInfo `json:"details"`
	Tags         []DirectoryTag            `json:"tags"`
}
type DirectoryCustomerProfile struct {
	CustomerID   customerdomain.CustomerID `json:"customer_id"`
	Availability string                    `json:"availability"`
	CapturedAt   time.Time                 `json:"captured_at"`
	DisplayName  string                    `json:"display_name"`
	CorpName     string                    `json:"corp_name"`
	Gender       int16                     `json:"gender"`
	ContactType  int16                     `json:"contact_type"`
	AvatarURL    string                    `json:"avatar_url"`
	Publication  DirectoryPublication      `json:"publication"`
	FollowUsers  []DirectoryFollow         `json:"follow_users"`
}
type TagHistoryQuery struct {
	CustomerID    customerdomain.CustomerID
	EmployeeID    string
	ProviderTagID string
	FromDate      string
	ToDate        string
	BeforeID      int64
	Limit         int
}
type TagHistoryEvent struct {
	ID                 int64                     `json:"id"`
	CustomerID         customerdomain.CustomerID `json:"customer_id"`
	EmployeeID         string                    `json:"employee_id"`
	EmployeeName       string                    `json:"employee_name"`
	ProviderTagID      string                    `json:"tag_id"`
	ProviderTagType    int16                     `json:"tag_type"`
	Name               string                    `json:"name"`
	GroupName          string                    `json:"group_name"`
	EventType          string                    `json:"event_type"`
	Reason             string                    `json:"reason"`
	DiscoveredAt       time.Time                 `json:"discovered_at"`
	DiscoveredDate     string                    `json:"discovered_date"`
	RegistrationDate   string                    `json:"registration_date"`
	PreviousObservedAt *time.Time                `json:"previous_observed_at,omitempty"`
	ProviderOperatedAt *time.Time                `json:"provider_operated_at,omitempty"`
	FromRevision       int64                     `json:"from_revision"`
	ToRevision         int64                     `json:"to_revision"`
	RunID              int64                     `json:"run_id"`
	CustomerTransition bool                      `json:"customer_transition"`
}
type TagHistoryPage struct {
	Items        []TagHistoryEvent `json:"items"`
	NextBeforeID int64             `json:"next_before_id,omitempty"`
}
type TagHistoryStatistic struct {
	Date          string `json:"date"`
	EventType     string `json:"event_type"`
	EventCount    int64  `json:"event_count"`
	CustomerCount int64  `json:"customer_count"`
}
type CustomerTagHistoryReader interface {
	CustomerTagHistory(context.Context, TagHistoryQuery) (TagHistoryPage, error)
	CustomerTagHistoryStatistics(context.Context, TagHistoryQuery) ([]TagHistoryStatistic, error)
}
type DirectoryCustomerProfileReader interface {
	DirectoryCustomerProfile(context.Context, customerdomain.CustomerID) (DirectoryCustomerProfile, error)
}
