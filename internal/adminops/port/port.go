// Package port defines Admin Ops' local control-plane contract. It carries no
// credential material: secret-bearing operations use only a safe reference.
package port

import (
	"context"
	"errors"
	"time"
)

var ErrNotFound = errors.New("admin ops value not found")

type CredentialKind string

const (
	CredentialDirectAPIKey CredentialKind = "direct_api_key"
	CredentialAPIClient    CredentialKind = "api_client"
)

type Credential struct {
	ID          int64
	Kind        CredentialKind
	ClientID    string
	DisplayName string
	State       string
	SecretRef   string
	SecretMask  string
	Metadata    []byte
	Version     int64
	CreatedBy   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type Category struct {
	Key       string
	Enabled   bool
	Settings  []byte
	Version   int64
	UpdatedBy string
	UpdatedAt time.Time
}

type Release struct {
	ID                  int64
	State               string
	Changes             []byte
	Checksum            string
	BasedOnReleaseID    *int64
	RollbackOfReleaseID *int64
	CreatedBy           string
	PublishedBy         string
	CreatedAt           time.Time
	ValidatedAt         *time.Time
	PublishedAt         *time.Time
}

type Job struct {
	ID          int64
	Key         string
	Kind        string
	State       string
	TargetRef   string
	Request     []byte
	Result      []byte
	Version     int64
	RequestedBy string
	FailureCode string
	CreatedAt   time.Time
	StartedAt   *time.Time
	CompletedAt *time.Time
	UpdatedAt   time.Time
}

// NotificationSetting is a provider-neutral local configuration row. The
// secret is represented only by a vault reference and mask; no token or
// webhook material crosses this port.
type NotificationSetting struct {
	Enabled         bool
	Channel         string
	SecretRef       string
	SecretMask      string
	ValidationState string
	UpdatedAt       time.Time
}

// Receipt carries the transaction-local idempotency state needed by Admin
// Ops mutations. Store implementations belong to Terra and must provide the
// concrete PostgreSQL owner later.
type Receipt struct {
	ID                               int64
	Action, Actor, State             string
	KeyDigest, PayloadDigest, Result []byte
}

// Repository is the narrow persistence seam for the Admin Ops local control
// plane. It has no generated SQL or cross-domain store dependency.
type Repository interface {
	CreateCredential(context.Context, Credential) (Credential, error)
	GetCredential(context.Context, CredentialKind, string) (Credential, error)
	ListCredentials(context.Context) ([]Credential, error)
	UpdateCredential(context.Context, Credential) (Credential, error)
	UpsertCategory(context.Context, Category) (Category, error)
	GetCategory(context.Context, string) (Category, error)
	ListCategories(context.Context) ([]Category, error)
	CreateRelease(context.Context, Release) (Release, error)
	GetRelease(context.Context, int64) (Release, error)
	ListReleases(context.Context, int32) ([]Release, error)
	ValidateRelease(context.Context, int64, time.Time) (Release, error)
	PublishRelease(context.Context, int64, string, string, time.Time) (Release, error)
	RollbackRelease(context.Context, int64, string, time.Time) (Release, error)
	CreateJob(context.Context, Job) (Job, error)
	GetJob(context.Context, string) (Job, error)
	ListJobs(context.Context, string, string, int32) ([]Job, error)
	TransitionJob(context.Context, Job) (Job, error)
	GetNotification(context.Context) (NotificationSetting, error)
	UpsertNotification(context.Context, bool, string, string, string, string, time.Time) (NotificationSetting, error)
	ReserveReceipt(context.Context, string, string, []byte, []byte, time.Time) (Receipt, bool, error)
	CompleteReceipt(context.Context, int64, []byte, time.Time) (Receipt, error)
}
