package port

import (
	"context"
	"time"
)

// ExecutionReceipt is restricted administrator business evidence. Provider
// target identities, content, credentials and raw responses are never exposed.
type ExecutionReceipt struct {
	Found                bool       `json:"receipt_found"`
	MessageID            string     `json:"msgid"`
	SenderUserID         string     `json:"sender_userid"`
	FailureReason        string     `json:"failure_reason"`
	CreationErrorCode    *int64     `json:"creation_errcode"`
	CreationCodeEvidence string     `json:"creation_errcode_evidence"`
	LocalStatus          *int       `json:"local_delivery_status"`
	LocalSentAt          *time.Time `json:"local_sent_at"`
	LocalObservedAt      *time.Time `json:"local_observed_at"`
	QueryState           string     `json:"query_state"`
	QueryHTTPStatus      int        `json:"query_http_status"`
	QueryErrorCode       *int64     `json:"query_errcode"`
	QueryFailure         string     `json:"query_failure"`
	ProviderStatus       *int       `json:"provider_delivery_status"`
	ProviderSentAt       *time.Time `json:"provider_sent_at"`
	ProviderObservedAt   *time.Time `json:"provider_observed_at"`
}

type ExecutionReceiptReader interface {
	ReadExecutionReceipt(context.Context, string, bool) (ExecutionReceipt, error)
}
