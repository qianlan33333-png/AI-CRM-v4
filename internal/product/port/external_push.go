package port

import (
	"context"
	"time"
)

// ExternalPushProductKind distinguishes the two CRM-local product projections
// that can own the same closed commerce external-push configuration. It does
// not identify a provider, destination, URL, credential, or payload.
type ExternalPushProductKind string

const (
	ExternalPushWeChatPay     ExternalPushProductKind = "wechat_pay"
	ExternalPushServicePeriod ExternalPushProductKind = "service_period"
)

// ExternalPushConfiguration combines Product business settings with an
// admin-only URL projection read through the Outbound endpoint Port. Product
// persists only the opaque reference; destination storage, secrets, identity
// disclosure policy and dispatch remain Outbound-owned.
type ExternalPushConfiguration struct {
	// URL is a transient admin projection, excluded from Product receipt storage.
	URL                    string                  `json:"url,omitempty"`
	ProductID              ID                      `json:"product_id"`
	ProductKind            ExternalPushProductKind `json:"product_kind"`
	Enabled                bool                    `json:"enabled"`
	ConfigurationReference string                  `json:"configuration_reference,omitempty"`
	PushType               string                  `json:"type"`
	Day                    *int64                  `json:"day"`
	Frequency              *int64                  `json:"frequency"`
	// ExpiresAtTS is the frozen V2 config expiry in Unix seconds. Nil means the
	// legacy configuration had no expiry; it is a Product business fact, not a
	// protected deployment-target policy.
	ExpiresAtTS  *int64         `json:"expires_at_ts"`
	Remark       string         `json:"remark"`
	CustomParams map[string]any `json:"custom_params"`
	FieldMapping *FieldMapping  `json:"field_mapping,omitempty"`
	Revision     int64          `json:"revision"`
	ProductName  string         `json:"-"`
	UpdatedAt    time.Time      `json:"updated_at"`
}

type SaveExternalPushConfigurationCommand struct {
	URL                    *string
	ProductID              ID
	ProductKind            ExternalPushProductKind
	Enabled                bool
	ConfigurationReference string
	// When false, old frozen hosts update only the opaque binding and retain
	// Product-owned business parameters. The V3 host submits a complete set.
	FieldMapping          *FieldMapping
	FieldMappingSet       bool
	BusinessParametersSet bool
	PushType              string
	Day                   *int64
	Frequency             *int64
	ExpiresAtTS           *int64
	Remark                string
	CustomParams          map[string]any
	ExpectedRevision      int64
	Actor                 int64
	IdempotencyKey        string
}

type QueueExternalPushTestCommand struct {
	ProductID      ID
	ProductKind    ExternalPushProductKind
	Actor          int64
	IdempotencyKey string
}

// ExternalPushTest is a local EER acceptance projection. State=accepted or
// queued is never evidence of Provider acceptance or delivery.
type ExternalPushTest struct {
	ProductID   ID                      `json:"product_id"`
	ProductKind ExternalPushProductKind `json:"product_kind"`
	EffectID    string                  `json:"effect_id"`
	// DeliveryID is the immutable legacy protocol delivery identifier assigned
	// when this explicit test is accepted. It is an Outbound-produced value,
	// not proof that a Provider received or delivered the request. Historical
	// Product rows and receipt snapshots may omit it.
	DeliveryID               string    `json:"delivery_id,omitempty"`
	State                    string    `json:"state"`
	AttemptCount             int32     `json:"attempt_count"`
	ProviderAccepted         bool      `json:"provider_accepted"`
	DeliveryProven           bool      `json:"delivery_proven"`
	RealExternalCallExecuted bool      `json:"real_external_call_executed"`
	AutoRetryAllowed         bool      `json:"auto_retry_allowed"`
	CreatedAt                time.Time `json:"created_at"`
	UpdatedAt                time.Time `json:"updated_at"`
}

// ExternalPushTestStatus is an Outbound-owned, digest-safe delivery
// projection for a Product-owned test binding. It carries no endpoint, signed
// body, identity value, Provider response, or retry control. An executed call
// is never presented as delivery proof.
type ExternalPushTestStatus struct {
	EffectID                 string    `json:"effect_id"`
	State                    string    `json:"state"`
	AttemptCount             int32     `json:"attempt_count"`
	ProviderCallAttempted    bool      `json:"provider_call_attempted"`
	RealExternalCallExecuted bool      `json:"real_external_call_executed"`
	ProviderResultReceived   *bool     `json:"provider_result_received"`
	UpdatedAt                time.Time `json:"updated_at"`
}

type CommerceExternalPushApplication interface {
	GetExternalPushConfiguration(context.Context, ID, ExternalPushProductKind) (ExternalPushConfiguration, error)
	SaveExternalPushConfiguration(context.Context, SaveExternalPushConfigurationCommand) (ExternalPushConfiguration, error)
	QueueExternalPushTest(context.Context, QueueExternalPushTestCommand) (ExternalPushTest, error)
	ListExternalPushTests(context.Context, ID, ExternalPushProductKind) ([]ExternalPushTest, error)
}

// ExternalPushConfigurationReader is the Product-owned read boundary used by
// Outbound for a frozen Order item. The read locks the Product row used by
// configuration writes for the caller's Unit of Work, so the first paid event freezes one
// revision before a concurrent administrator update can take effect. Product
// URLs, credentials, and mutable product rows do not cross the boundary.
type ExternalPushConfigurationReader interface {
	ReadExternalPushConfigurationForOrder(context.Context, ID) (ExternalPushConfiguration, error)
}

// ExternalPushTestIntent is Product's opaque synthetic-operation handoff to
// Outbound. The Product receipt key digest prevents a different administrator
// operation from being mistaken for a replay; Provider details remain Outbound
// owned.
type ExternalPushTestIntent struct {
	ProductID              ID
	ProductKind            ExternalPushProductKind
	ConfigurationReference string
	ConfigurationRevision  int64
	ReceiptKeyDigest       [32]byte
}

// ExternalPushTestAccepter joins the current Product Unit of Work. A returned
// accepted/queued state is only an EER local fact; it is never a Provider
// receipt or delivery claim.
type ExternalPushTestAccepter interface {
	AcceptExternalPushTestWithin(context.Context, ExternalPushTestIntent) (ExternalPushTest, error)
}

// ExternalPushTestStatusReader is implemented by Outbound. Product reads this
// only after loading its own immutable test binding; it never reads Outbound
// tables or controls retry or reconciliation.
type ExternalPushTestStatusReader interface {
	ReadExternalPushTestStatus(context.Context, ID, string) (ExternalPushTestStatus, error)
}
