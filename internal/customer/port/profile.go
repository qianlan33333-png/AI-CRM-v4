package port

import (
	"context"
	"errors"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
)

var (
	ErrCapabilityNotReady = errors.New("customer profile capability not ready")
	ErrSectionUnavailable = errors.New("customer profile section unavailable")
)

type SectionState string

const (
	SectionReady    SectionState = "ready"
	SectionDegraded SectionState = "degraded"
	SectionNotReady SectionState = "not_ready"
)

type SectionStatus struct {
	State     SectionState `json:"status"`
	AsOf      *time.Time   `json:"as_of,omitempty"`
	ErrorCode string       `json:"error_code,omitempty"`
}

type CanonicalCustomer struct {
	RequestedCustomerID customerdomain.CustomerID
	CustomerID          customerdomain.CustomerID
	Merged              bool
}

type CanonicalCustomerResolver interface {
	ResolveCanonicalCustomer(context.Context, customerdomain.CustomerID) (CanonicalCustomer, error)
}

type PageQuery struct {
	Limit     int
	Watermark time.Time
	AfterAt   time.Time
	AfterID   int64
	Filter    string
}

type OwnerItem struct {
	DisplayName string    `json:"display_name"`
	Status      string    `json:"status"`
	Source      string    `json:"source,omitempty"`
	ObservedAt  time.Time `json:"observed_at"`
}

type OwnerPage struct {
	Items          []OwnerItem
	UnmatchedCount int
	Status         SectionStatus
}

type CustomerOwnerReader interface {
	CapabilityStatus() SectionStatus
	CustomerOwners(context.Context, customerdomain.CustomerID) (OwnerPage, error)
}

type TagItem struct {
	Name       string    `json:"name"`
	GroupName  string    `json:"group_name,omitempty"`
	Status     string    `json:"status"`
	ObservedAt time.Time `json:"observed_at"`
}

type TagPage struct {
	Items  []TagItem
	Status SectionStatus
}

type CustomerTagReader interface {
	CapabilityStatus() SectionStatus
	CustomerTags(context.Context, customerdomain.CustomerID) (TagPage, error)
}

type SurveyAnswer struct {
	Question string   `json:"question"`
	Answers  []string `json:"answers"`
}

type SurveyItem struct {
	ID              int64          `json:"id"`
	Title           string         `json:"title"`
	SubmittedAt     time.Time      `json:"submitted_at"`
	Score           float64        `json:"score"`
	AssessmentLabel string         `json:"assessment_label,omitempty"`
	Answers         []SurveyAnswer `json:"answers"`
}

type SurveyPage struct {
	Items []SurveyItem
	// Total is supplied by the Survey Owner. Items is only a bounded window,
	// and therefore cannot truthfully stand in for the customer's full count.
	Total  int64
	Status SectionStatus
}

type CustomerSurveyReader interface {
	CapabilityStatus() SectionStatus
	CustomerSurveys(context.Context, customerdomain.CustomerID, PageQuery) (SurveyPage, error)
}

type TimelineItem struct {
	ID           int64     `json:"id"`
	EventType    string    `json:"event_type"`
	Title        string    `json:"title"`
	SourceDomain string    `json:"source_domain"`
	OccurredAt   time.Time `json:"occurred_at"`
}

type TimelinePage struct {
	Items  []TimelineItem
	Status SectionStatus
}

type CustomerTimelineReader interface {
	CapabilityStatus() SectionStatus
	CustomerTimeline(context.Context, customerdomain.CustomerID, PageQuery) (TimelinePage, error)
}

type TimelineEvent struct {
	CustomerID    customerdomain.CustomerID
	SourceDomain  string
	SourceEventID string
	EventType     string
	Title         string
	OccurredAt    time.Time
}

type TimelineWriter interface {
	AppendTimeline(context.Context, TimelineEvent) error
}

type ChatActivityItem struct {
	ID          int64     `json:"id"`
	ChatType    string    `json:"chat_type"`
	MessageType string    `json:"message_type"`
	OccurredAt  time.Time `json:"occurred_at"`
}

type ChatActivityPage struct {
	Items  []ChatActivityItem
	Status SectionStatus
}

type CustomerChatActivityReader interface {
	CapabilityStatus() SectionStatus
	CustomerChatActivity(context.Context, customerdomain.CustomerID, PageQuery) (ChatActivityPage, error)
}
