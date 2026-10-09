// Package port is the stable invoice Provider and refund boundary.
package port

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	ErrDisabled     = errors.New("invoice not enabled")
	ErrInvalid      = errors.New("invalid invoice request")
	ErrNotFound     = errors.New("invoice not found")
	ErrIneligible   = errors.New("order not invoice eligible")
	ErrConflict     = errors.New("invoice conflict")
	ErrPending      = errors.New("invoice provider result pending")
	ErrUnavailable  = errors.New("invoice provider unavailable")
	ErrNotSubmitted = errors.New("invoice application not submitted")
	ErrTitleAbsent  = errors.New("invoice official title absent")
)

// ValidApplyID follows the official fapiao_apply_id string(32) contract.
func ValidApplyID(id string) bool {
	if len(id) == 0 || len(id) > 32 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("_-|*", c)) {
			return false
		}
	}
	return true
}

type Tax struct {
	Code                   string `json:"code"`
	Rate                   int64  `json:"rate"`
	GoodsName              string `json:"goods_name"`
	PreferentialPolicyCode *int64 `json:"preferential_policy_code,omitempty"`
}

// Tax facts are supplied by finance. Reject unsupported official rates rather
// than dispatching an invalid request and leaving the customer awaiting a ticket.
func (t Tax) Valid() bool {
	if len(t.Code) != 19 || strings.Trim(t.Code, "0123456789") != "" || !ValidGoodsName(t.GoodsName) {
		return false
	}
	switch t.Rate {
	case 0, 100, 150, 300, 500, 600, 900, 1000, 1100, 1300, 1600, 1700:
	default:
		return false
	}
	return t.PreferentialPolicyCode == nil || *t.PreferentialPolicyCode >= 1 && *t.PreferentialPolicyCode <= 18
}

// ValidGoodsName is the shared invoice item-name contract used before publishing
// runtime configuration and when freezing invoice tax facts.
func ValidGoodsName(name string) bool {
	return utf8.ValidString(name) && strings.TrimSpace(name) != "" && utf8.RuneCountInString(name) <= 128
}

type Config struct {
	Enabled                                                               bool
	MerchantID, AppID, AppScope, SellerName, SellerTaxID, BillingPersonID string
	Taxes                                                                 map[string]Tax
}
type Line struct {
	TaxCode                string `json:"tax_code"`
	GoodsName              string `json:"goods_name"`
	Quantity               int64  `json:"quantity"`
	TotalAmount            int64  `json:"total_amount"`
	TaxRate                int64  `json:"tax_rate"`
	Discount               bool   `json:"discount"`
	PreferentialPolicyCode *int64 `json:"preferential_policy_code,omitempty"`
}
type Snapshot struct {
	MerchantID      string `json:"merchant_id"`
	AppID           string `json:"appid"`
	AppScope        string `json:"app_scope"`
	SellerName      string `json:"seller_name"`
	SellerTaxID     string `json:"seller_tax_id"`
	BillingPersonID string `json:"billing_person_id"`
	OrderNo         string `json:"order_no"`
	TransactionID   string `json:"transaction_id"`
	Amount          int64  `json:"amount_minor"`
	Lines           []Line `json:"lines"`
}
type Application struct {
	ID                                                 string
	OrderID, CustomerID, IdentityID                    int64
	InvoiceID, State, CardState, BlueNumber, RedNumber string
	Snapshot                                           Snapshot
	Buyer                                              Buyer
	RefundConfirmed                                    bool
	IssueEffect, ReverseEffect                         string
	ReverseAttempt, IssueAttempt, DispatchAttempt      int
	UpdatedAt                                          time.Time
}
type Buyer struct {
	Type             string `json:"type"`
	Name             string `json:"name"`
	TaxpayerID       string `json:"taxpayer_id,omitempty"`
	Address          string `json:"address,omitempty"`
	Telephone        string `json:"telephone,omitempty"`
	BankName         string `json:"bank_name,omitempty"`
	BankAccount      string `json:"bank_account,omitempty"`
	Phone            string `json:"phone,omitempty" encryption:"EM_APIV3"`
	Email            string `json:"email,omitempty" encryption:"EM_APIV3"`
	UserApplyMessage string `json:"user_apply_message,omitempty"`
	Amount           int64  `json:"amount,omitempty"`
	OrderNo          string `json:"out_trade_no,omitempty"`
	BillType         string `json:"fapiao_bill_type,omitempty"`
}

func (b Buyer) Valid(s Snapshot) bool {
	return (b.Type == "INDIVIDUAL" || b.Type == "ORGANIZATION" && b.TaxpayerID != "") && b.Name != "" && len(b.Name) <= 300 && len(b.TaxpayerID) <= 64 && (b.Amount == 0 || b.Amount == s.Amount) && (b.OrderNo == "" || b.OrderNo == s.OrderNo) && (b.BillType == "" || b.BillType == "COMM_FAPIAO")
}

type Launch struct {
	AppID     string    `json:"miniprogram_appid"`
	Path      string    `json:"miniprogram_path"`
	UserName  string    `json:"miniprogram_user_name"`
	ExpiresAt time.Time `json:"expires_at"`
}
type Result struct {
	InvoiceID   string `json:"fapiao_id"`
	Status      string `json:"status"`
	TotalAmount int64  `json:"total_amount"`
	Seller      struct {
		Name       string `json:"name"`
		TaxpayerID string `json:"taxpayer_id"`
	} `json:"seller_information"`
	Blue struct {
		Number string `json:"fapiao_number"`
	} `json:"blue_fapiao"`
	Red struct {
		Number string `json:"fapiao_number"`
	} `json:"red_fapiao"`
	Card struct {
		Status string `json:"card_status"`
		OpenID string `json:"card_openid"`
		AppID  string `json:"card_appid"`
		ID     string `json:"card_id"`
		Code   string `json:"card_code"`
	} `json:"card_information"`
}

// CardEntry is returned only after ownership and official recipient validation.
// Do not put the card code or the recipient identity into URLs or logs.
type CardEntry struct {
	ID   string `json:"cardId"`
	Code string `json:"code"`
}

// OfficialResponse is an authenticated business receipt, owned by Invoice.
// Its message can contain invoice data and must never enter application logs.
type OfficialResponse struct {
	HTTPStatus int    `json:"http_status"`
	Code       string `json:"code"`
	Message    string `json:"message"`
}

func (r OfficialResponse) Error() string { return ErrUnavailable.Error() }
func (r OfficialResponse) Unwrap() error { return ErrUnavailable }

const OfficialResponseArtifact = "invoice.official-response.v1"

type Notice struct {
	ID, EventType, ApplicationID, MerchantID, SubMerchantID string
	BodyDigest                                              string
}
type JSConfig struct {
	AppID     string `json:"appId"`
	Timestamp int64  `json:"timestamp"`
	Nonce     string `json:"nonceStr"`
	Signature string `json:"signature"`
}

// NotificationVerifier authenticates inbound messages without granting any
// invoice, card or refund write capability.
type NotificationVerifier interface {
	Verify(context.Context, *http.Request) (Notice, error)
}
type Provider interface {
	NotificationVerifier
	Title(context.Context, Application, string) (Launch, error)
	ReadTitle(context.Context, Application) (Buyer, error)
	Query(context.Context, Application) (Result, error)
	Write(context.Context, Application, bool) error
	File(context.Context, Application) ([]byte, error)
	SignPage(context.Context, string) (JSConfig, error)
}

// Only authenticated affirmative absence permits a self-collected title.
type OptionalTitleReader interface {
	ReadOptionalTitle(context.Context, Application) (Buyer, bool, error)
}

// PaymentVerifier uses the already-authenticated Payment adapter. It validates
// the collecting merchant, exact transaction and amount before issuing.
type PaymentVerifier interface {
	VerifyInvoicePayment(context.Context, Snapshot) error
}
type MaterialReader interface {
	PrepareInvoiceEffect(context.Context, string, bool) (Application, error)
}
type QueryJobEnqueuer interface {
	EnqueueWithin(context.Context, string, time.Time) error
}

// Explicit native title acceptance binds recovery to the failed invoice number.
type RecoveryJobEnqueuer interface {
	EnqueueRecoveryWithin(context.Context, string, string) error
}

// UnsubmittedVerifier is used only by the explicitly selected one-shot repair.
// It must authenticate affirmative Provider evidence, never infer absence from a timeout.
type UnsubmittedVerifier interface {
	VerifyUnsubmitted(context.Context, Application) error
}
type Receipt struct {
	ID              string  `json:"id"`
	OrderID         int64   `json:"order_id"`
	Amount          int64   `json:"amount_minor"`
	State           string  `json:"state"`
	CardState       string  `json:"card_state"`
	BlueNumber      string  `json:"invoice_number,omitempty"`
	RedNumber       string  `json:"red_number,omitempty"`
	RefundConfirmed bool    `json:"refund_confirmed"`
	Launch          *Launch `json:"launch,omitempty"`
	FailureCode     string  `json:"failure_code,omitempty"`
}
type OrdersQuery struct {
	State, Keyword, OrderNo, Cursor string
	From, Until                     *time.Time
	Limit                           int32
	EligibleOnly                    bool
}
type OrderView struct {
	ID              int64      `json:"id"`
	OrderNo         string     `json:"order_no"`
	ProductNames    []string   `json:"product_names"`
	Time            *time.Time `json:"time"`
	TimeKind        string     `json:"time_kind"`
	Amount          int64      `json:"amount_minor"`
	Currency        string     `json:"currency"`
	Provider        string     `json:"provider"`
	State           string     `json:"state"`
	RefundedMinor   int64      `json:"refunded_minor"`
	RefundPending   bool       `json:"refund_pending"`
	InvoiceEligible bool       `json:"invoice_eligible"`
	InvoiceReason   string     `json:"invoice_reason"`
	Invoice         *Receipt   `json:"invoice,omitempty"`
	PaymentPath     string     `json:"payment_path,omitempty"`
}
type OrdersPage struct {
	Items          []OrderView `json:"items"`
	NextCursor     string      `json:"next_cursor"`
	InvoiceEnabled bool        `json:"invoice_enabled"`
}

// MarshalReceipt contains only a safe client projection, never a buyer title.
func MarshalReceipt(a Application) Receipt {
	// A locally stopped dispatch retains its confirmed title and recovers via
	// query jobs. It does not need the customer to fill the title again.
	if a.State == "awaiting_title" && a.IssueEffect != "" && a.Buyer.Valid(a.Snapshot) {
		a.State = "queued"
	}
	return Receipt{ID: a.ID, OrderID: a.OrderID, Amount: a.Snapshot.Amount, State: a.State, CardState: a.CardState, BlueNumber: a.BlueNumber, RedNumber: a.RedNumber, RefundConfirmed: a.RefundConfirmed}
}

// TitleSubmission is the minimal self-collected title. Amount is a confirmation
// precondition, never an authority for invoice calculation.
type TitleInput struct {
	Type       string `json:"type"`
	Name       string `json:"name"`
	TaxpayerID string `json:"taxpayer_id,omitempty"`
}
type TitleSubmission struct {
	Buyer          TitleInput
	ExpectedAmount int64
}
type HistoryQuery struct {
	Cursor string
	Limit  int
}
type HistoryItem struct {
	Receipt
	OrderNo      string    `json:"order_no"`
	ProductNames []string  `json:"product_names"`
	CreatedAt    time.Time `json:"created_at"`
}
type HistoryPage struct {
	Items      []HistoryItem `json:"items"`
	NextCursor string        `json:"next_cursor"`
}
