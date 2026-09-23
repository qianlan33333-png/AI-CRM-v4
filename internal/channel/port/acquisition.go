// Package port freezes Channel-owned correlation and receipt boundaries.
package port

import (
	"context"
	"time"

	channeldomain "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/domain"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
)

// StateResolver performs an exact local lookup. The callback boundary computes
// StateDigest before inboxing; raw State never crosses this port.
type StateResolver interface {
	ResolveStateDigest(context.Context, string, [32]byte, time.Time) (channeldomain.StateResolution, error)
}

type EntrantReceiptStatus string

const (
	EntrantReceiptAttributed       EntrantReceiptStatus = "channel_attributed"
	EntrantReceiptUnmatched        EntrantReceiptStatus = "channel_unmatched"
	EntrantReceiptAmbiguous        EntrantReceiptStatus = "channel_ambiguous"
	EntrantReceiptIdentityConflict EntrantReceiptStatus = "identity_conflict"
)

// EntrantReceipt is the non-sensitive, idempotent record of a completed
// correlation decision. CallbackID is an inbox/receipt key, never an external
// user id or the raw State value.
type EntrantReceipt struct {
	CallbackID string
	InboxID    int64
	CorpID     string
	ChangeType string
	Status     EntrantReceiptStatus
	CustomerID customerdomain.CustomerID
	OccurredAt time.Time
	Resolution channeldomain.StateResolution
}

func (receipt EntrantReceipt) Valid() bool {
	if receipt.CallbackID == "" || receipt.InboxID < 1 || receipt.CorpID == "" ||
		(receipt.ChangeType != "add_external_contact" && receipt.ChangeType != "add_half_external_contact") || receipt.OccurredAt.IsZero() {
		return false
	}
	switch receipt.Status {
	case EntrantReceiptAttributed:
		return receipt.CustomerID > 0 && receipt.Resolution.Status == channeldomain.StateAttributed && receipt.Resolution.Valid()
	case EntrantReceiptUnmatched:
		return receipt.CustomerID > 0 && receipt.Resolution.Status == channeldomain.StateUnmatched && receipt.Resolution.Valid()
	case EntrantReceiptAmbiguous:
		return receipt.CustomerID > 0 && receipt.Resolution.Status == channeldomain.StateAmbiguous && receipt.Resolution.Valid()
	case EntrantReceiptIdentityConflict:
		return receipt.CustomerID == 0 && receipt.Resolution == (channeldomain.StateResolution{})
	default:
		return false
	}
}

// EntrantReceiptRecorder is Channel-owned. A production implementation must
// make this record idempotent on CallbackID in the same transaction as the
// callback lifecycle result.
type EntrantReceiptRecorder interface {
	RecordEntrantReceipt(context.Context, EntrantReceipt) error
}
