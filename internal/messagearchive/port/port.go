// Package port is the stable boundary published by messagearchive.
package port

import (
	"context"
	"errors"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
)

var (
	ErrWorkBudgetExceeded = errors.New("message archive delivery work budget exceeded")
	ErrNotReady           = errors.New("message archive not ready")
	// ErrStaffNotFound is returned only by the archive-owned local staff
	// selector. It never triggers a Provider directory request.
	ErrStaffNotFound = errors.New("message archive staff not found")
)

// InboxDelivery is a safe projection of a platform webhook row.  It is
// passed into the archive domain by WeCom's existing Inbox processor; it
// cannot claim, complete, retry, or otherwise own the Inbox state machine.
type InboxDelivery struct {
	ID             int64
	IdempotencyKey string
	Attempt        int
	MaxAttempts    int
	ReceivedAt     time.Time
	Payload        []byte
}

type InboxDeliveryHandler interface {
	ProcessArchiveDelivery(context.Context, InboxDelivery) error
}

type MessageItem struct {
	ID          int64     `json:"id"`
	ChatType    string    `json:"chat_type"`
	MessageType string    `json:"message_type"`
	OccurredAt  time.Time `json:"occurred_at"`
	ContentText string    `json:"content_text"`
	RenderType  string    `json:"render_type"`
	Direction   string    `json:"direction"`
	StaffNames  []string  `json:"staff_names"`
	StaffIDs    []int64   `json:"-"`
	MediaIDs    []int64   `json:"media_ids"`
}

// MediaContent is returned only after MessageArchive has checked that the
// requested media belongs to the requested customer's current OneID lineage.
// It is intentionally private and is never persisted in a general media store.
type MediaContent struct {
	Kind string
	Data []byte
}

type CustomerQuery struct {
	CustomerID  customerdomain.CustomerID
	CustomerIDs []customerdomain.CustomerID
	ChatType    string
	Search      string
	MessageType string
	Direction   string
	StaffUserID int64
	StartAt     time.Time
	Limit       int
	Watermark   time.Time
	AfterAt     time.Time
	AfterID     int64
}

type CustomerPage struct {
	Items []MessageItem
	AsOf  time.Time
}

// StaffOption is an archive-local projection of an already resolved Access
// staff member. It gives the read Host a display-name selector without
// exposing arbitrary account management data or asking a reader to type an
// internal identifier.
type StaffOption struct {
	ID          int64  `json:"id"`
	DisplayName string `json:"display_name"`
}

type CustomerMessageReader interface {
	CustomerMessages(context.Context, CustomerQuery) (CustomerPage, error)
	CustomerStaff(context.Context, customerdomain.CustomerID) ([]StaffOption, error)
}

// MachineContactTouchReader exposes only observed event times. No message body,
// provider identifier, or attribution claim leaves MessageArchive.
type MachineContactTouchReader interface {
	MachineContactTouchTimes(context.Context, string, []int64) (map[int64]MachineContactTouch, error)
}

type MachineContactTouch struct {
	LastStaffMessageAt    *time.Time
	LastCustomerMessageAt *time.Time
}
