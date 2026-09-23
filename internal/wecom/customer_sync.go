package wecom

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/idempotency"
	platformoutbox "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/outbox"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
	wecomprovider "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/provider"
)

var (
	ErrSyncNotReady        = errors.New("wecom customer sync is not ready")
	ErrSyncConflict        = errors.New("wecom customer sync already active")
	ErrSyncNotFound        = errors.New("wecom customer sync run not found")
	ErrSyncCAS             = errors.New("wecom customer sync changed concurrently")
	errSyncStaleRead       = errors.New("wecom customer sync stale read failed")
	errSyncProjection      = errors.New("wecom customer sync projection failed")
	errSyncOutboxReconcile = errors.New("wecom customer sync outbox reconciliation failed")
	errSyncCompletion      = errors.New("wecom customer sync completion failed")
	errSyncAudit           = errors.New("wecom customer sync audit failed")
)

// maximumAudiencePrimaryOwnerBatch bounds the read-only audience bridge. It
// matches Segment's evaluated-audience ceiling without making WeCom depend on
// Segment implementation packages.
const maximumAudiencePrimaryOwnerBatch = 100000

type CustomerSyncStatus string

// syncRetryError retains only a safe business failure code for River's final
// attempt. The original Provider error may contain transport detail and must
// not be propagated into job logs.
type syncRetryError struct {
	code        string
	maxAttempts int
}

func (err *syncRetryError) Error() string { return "customer sync step scheduled for retry" }

const (
	SyncQueued           CustomerSyncStatus = "queued"
	SyncListingStaff     CustomerSyncStatus = "listing_staff"
	SyncFetchingProfiles CustomerSyncStatus = "fetching_profiles"
	SyncIngesting        CustomerSyncStatus = "ingesting"
	SyncReconciling      CustomerSyncStatus = "reconciling"
	SyncSucceeded        CustomerSyncStatus = "succeeded"
	SyncFailedRetryable  CustomerSyncStatus = "failed_retryable"
	SyncFailedTerminal   CustomerSyncStatus = "failed_terminal"
)

type CustomerSyncRun struct {
	ID             int64              `json:"run_id"`
	RunKey         string             `json:"-"`
	Trigger        string             `json:"trigger"`
	Status         CustomerSyncStatus `json:"status"`
	ResumeStatus   CustomerSyncStatus `json:"-"`
	CorpScope      string             `json:"-"`
	StaffIDs       []string           `json:"-"`
	StaffIndex     int                `json:"staff_index"`
	ProviderCursor string             `json:"-"`
	Discovered     int64              `json:"discovered"`
	Activated      int64              `json:"activated"`
	AlreadyLinked  int64              `json:"already_linked"`
	Conflict       int64              `json:"conflict"`
	TerminalFailed int64              `json:"terminal_failed"`
	Projected      int64              `json:"projected"`
	Stale          int64              `json:"stale"`
	Version        int64              `json:"-"`
	LastErrorCode  string             `json:"last_error_code,omitempty"`
	RequestedBy    int64              `json:"requested_by,omitempty"`
	StartedAt      *time.Time         `json:"started_at,omitempty"`
	CompletedAt    *time.Time         `json:"completed_at,omitempty"`
	CreatedAt      time.Time          `json:"created_at"`
	UpdatedAt      time.Time          `json:"updated_at"`
}

type CreateCustomerSyncRun struct {
	RunKey      string
	Trigger     string
	CorpScope   string
	RequestedBy int64
}

type SyncItem struct {
	ExternalUserID       string
	ExternalUserIDDigest [32]byte
	StaffIDDigest        [32]byte
	PayloadDigest        [32]byte
	Outcome              string
	CustomerID           customerdomain.CustomerID
	IdentityID           int64
	ErrorCode            string
}

// ContactDescriptionSourceCoverage is a digest-safe count of the distinct
// customer-follow pairs observed by one directory run. It deliberately records
// a missing description field separately from an explicit empty description.
// Neither omission nor an unsubmitted projected pair can satisfy backfill
// completion.
type ContactDescriptionSourceCoverage struct {
	Observed  int64 `json:"observed"`
	Projected int64 `json:"projected"`
	Omitted   int64 `json:"omitted"`
}

type ContactDescriptionSourceCoverageReader interface {
	ContactDescriptionSourceCoverage(context.Context, int64) (ContactDescriptionSourceCoverage, error)
}

type CustomerSyncStore interface {
	Create(context.Context, CreateCustomerSyncRun) (CustomerSyncRun, bool, error)
	Active(context.Context) (CustomerSyncRun, bool, error)
	Get(context.Context, int64) (CustomerSyncRun, error)
	List(context.Context, int) ([]CustomerSyncRun, error)
	Transition(context.Context, int64, int64, CustomerSyncStatus, CustomerSyncStatus) error
	SaveStaff(context.Context, int64, int64, []string) error
	InsertItem(context.Context, int64, string, SyncItem) (bool, error)
	UpsertProfile(context.Context, int64, string, identityport.ProvisionResult, wecomport.ExternalContact, [32]byte, time.Time) error
	UpsertProfileObservations(context.Context, int64, string, customerdomain.CustomerID, []wecomport.ExternalContactFollowInfo, time.Time) error
	AddCountsAndAdvance(context.Context, int64, int64, int64, int64, int64, int64, int64, int, string, CustomerSyncStatus) error
	StaleCustomers(context.Context, int64) ([]customerdomain.CustomerID, error)
	ReconcileProfileObservations(context.Context, int64, time.Time) error
	RefreshProfilePrimaryOwners(context.Context, int64, time.Time) error
	Complete(context.Context, int64, int64, int64) error
	Fail(context.Context, int64, int64, CustomerSyncStatus, string) error
	Terminate(context.Context, int64, string) error
}

type CustomerSyncService struct {
	Enabled    bool
	CorpID     string
	Provider   wecomport.DirectoryProvider
	Identity   identityport.VerifiedProvisioner
	Projection customerport.ProjectionWriter
	Timeline   customerport.TimelineWriter
	Store      CustomerSyncStore
	Outbox     platformoutbox.Service
	Enqueuer   CustomerSyncJobEnqueuer
	// DescriptionIntents is optional until the composition root supplies the
	// Outbound-owned immutable dispatch store. Its nil default preserves the
	// current read-only directory sync behavior.
	DescriptionIntents outboundport.ContactDescriptionIntentWriter
	// DescriptionSourceCoverage remains owned by WeCom. It supplies the
	// directory-response field-presence denominator to the admin read model;
	// Outbound retains ownership of queued and completed effect outcomes.
	DescriptionSourceCoverage ContactDescriptionSourceCoverageReader
	Audit                     interface {
		Append(context.Context, platformaudit.Event) (platformaudit.Event, error)
	}
	UOW platformport.UnitOfWork
	Now func() time.Time
}

func (service CustomerSyncService) ContactDescriptionSourceStats(ctx context.Context, runID int64) (ContactDescriptionSourceCoverage, error) {
	if runID < 1 || service.DescriptionSourceCoverage == nil || service.UOW == nil {
		return ContactDescriptionSourceCoverage{}, ErrSyncNotReady
	}
	var coverage ContactDescriptionSourceCoverage
	err := service.UOW.Within(ctx, func(txContext context.Context) error {
		var readErr error
		coverage, readErr = service.DescriptionSourceCoverage.ContactDescriptionSourceCoverage(txContext, runID)
		return readErr
	})
	if err != nil {
		return ContactDescriptionSourceCoverage{}, err
	}
	if coverage.Observed < 0 || coverage.Projected < 0 || coverage.Omitted < 0 || coverage.Observed != coverage.Projected+coverage.Omitted {
		return ContactDescriptionSourceCoverage{}, ErrSyncCAS
	}
	return coverage, nil
}

func (service CustomerSyncService) Ready() bool {
	return service.Enabled && service.CorpID != "" && service.Provider != nil && service.Provider.DirectoryReady() && service.Identity != nil &&
		service.Projection != nil && service.Timeline != nil && service.Store != nil && service.Outbox != nil && service.Enqueuer != nil && service.Audit != nil && service.UOW != nil
}

func (service CustomerSyncService) Create(ctx context.Context, command CreateCustomerSyncRun) (CustomerSyncRun, bool, error) {
	if !service.Ready() || command.CorpScope != "wecom-corp:"+service.CorpID || command.RequestedBy < 1 || command.Trigger != "manual" {
		return CustomerSyncRun{}, false, ErrSyncNotReady
	}
	var run CustomerSyncRun
	var replay bool
	err := service.UOW.Within(ctx, func(txContext context.Context) error {
		var createErr error
		run, replay, createErr = service.Store.Create(txContext, command)
		if createErr != nil {
			return createErr
		}
		if replay {
			return nil
		}
		if createErr = service.Enqueuer.EnqueueCustomerSync(txContext, run.ID); createErr != nil {
			return createErr
		}
		_, createErr = service.Audit.Append(txContext, platformaudit.Event{IdempotencyKey: idempotency.Key("wecom-sync-created:" + command.RunKey),
			Action: "wecom.customer_sync_created", ActorType: "admin", ActorID: strconv.FormatInt(command.RequestedBy, 10),
			ResourceType: "wecom_customer_sync", ResourceID: strconv.FormatInt(run.ID, 10), Payload: json.RawMessage(`{"trigger":"manual"}`)})
		return createErr
	})
	return run, replay, err
}

func (service CustomerSyncService) CreateScheduled(ctx context.Context, trigger, runKey string) (CustomerSyncRun, bool, error) {
	if !service.Ready() || (trigger != "daily" && trigger != "initial") || runKey == "" {
		return CustomerSyncRun{}, false, ErrSyncNotReady
	}
	command := CreateCustomerSyncRun{RunKey: runKey, Trigger: trigger, CorpScope: "wecom-corp:" + service.CorpID}
	var run CustomerSyncRun
	var replay bool
	err := service.UOW.Within(ctx, func(txContext context.Context) error {
		var createErr error
		run, replay, createErr = service.Store.Create(txContext, command)
		if createErr != nil {
			return createErr
		}
		if replay {
			return nil
		}
		if createErr = service.Enqueuer.EnqueueCustomerSync(txContext, run.ID); createErr != nil {
			return createErr
		}
		_, createErr = service.Audit.Append(txContext, platformaudit.Event{IdempotencyKey: idempotency.Key("wecom-sync-created:" + runKey), Action: "wecom.customer_sync_created", ActorType: "system", ResourceType: "wecom_customer_sync", ResourceID: strconv.FormatInt(run.ID, 10), Payload: json.RawMessage(`{"scheduled":true}`)})
		return createErr
	})
	return run, replay, err
}

func (service CustomerSyncService) Get(ctx context.Context, id int64) (CustomerSyncRun, error) {
	var run CustomerSyncRun
	err := service.UOW.Within(ctx, func(txContext context.Context) error {
		var queryErr error
		run, queryErr = service.Store.Get(txContext, id)
		return queryErr
	})
	return run, err
}

func (service CustomerSyncService) List(ctx context.Context, limit int) ([]CustomerSyncRun, error) {
	var runs []CustomerSyncRun
	err := service.UOW.Within(ctx, func(txContext context.Context) error {
		var queryErr error
		runs, queryErr = service.Store.List(txContext, limit)
		return queryErr
	})
	return runs, err
}

func (service CustomerSyncService) processRunOnce(ctx context.Context, run CustomerSyncRun) error {
	var err error
	switch run.Status {
	case SyncQueued:
		err = service.UOW.Within(ctx, func(txContext context.Context) error {
			return service.Store.Transition(txContext, run.ID, run.Version, run.Status, SyncListingStaff)
		})
		return err
	case SyncFailedRetryable:
		if run.ResumeStatus != SyncListingStaff && run.ResumeStatus != SyncFetchingProfiles && run.ResumeStatus != SyncIngesting && run.ResumeStatus != SyncReconciling {
			return ErrSyncCAS
		}
		err = service.UOW.Within(ctx, func(txContext context.Context) error {
			return service.Store.Transition(txContext, run.ID, run.Version, run.Status, run.ResumeStatus)
		})
		return err
	case SyncListingStaff:
		staff, providerErr := service.Provider.ListContactStaff(ctx)
		if providerErr != nil {
			return service.recordFailure(ctx, run, providerErr)
		}
		err = service.UOW.Within(ctx, func(txContext context.Context) error {
			return service.Store.SaveStaff(txContext, run.ID, run.Version, staff)
		})
		return err
	case SyncFetchingProfiles, SyncIngesting:
		if run.StaffIndex >= len(run.StaffIDs) {
			err = service.UOW.Within(ctx, func(txContext context.Context) error {
				return service.Store.Transition(txContext, run.ID, run.Version, run.Status, SyncReconciling)
			})
			return err
		}
		staffID := run.StaffIDs[run.StaffIndex]
		// This is an observation clock, so capture it before the Provider read.
		// A delayed older response must never win merely because it persisted
		// after a newer complete contact read.
		observedAt := service.now()
		page, providerErr := service.Provider.BatchExternalContacts(ctx, staffID, run.ProviderCursor, 100)
		if providerErr != nil {
			return service.recordFailure(ctx, run, providerErr)
		}
		err = service.ingestPage(ctx, run, staffID, page, observedAt)
		return err
	case SyncReconciling:
		err = service.reconcile(ctx, run)
		return err
	default:
		return nil
	}
}

func syncRetryCode(err error) string {
	var retry *syncRetryError
	if errors.As(err, &retry) && retry.code != "" {
		return retry.code
	}
	if errors.Is(err, ErrSyncCAS) {
		return "sync_cas"
	}
	if errors.Is(err, platformoutbox.ErrInvalidEvent) {
		return "outbox_invalid"
	}
	if errors.Is(err, errSyncStaleRead) {
		return "stale_read_failed"
	}
	if errors.Is(err, errSyncProjection) {
		return "projection_failed"
	}
	if errors.Is(err, errSyncOutboxReconcile) {
		return "outbox_reconcile_failed"
	}
	if errors.Is(err, errSyncCompletion) {
		return "completion_failed"
	}
	if errors.Is(err, errSyncAudit) {
		return "audit_failed"
	}
	return "sync_step_failed"
}

func (service CustomerSyncService) ingestPage(ctx context.Context, run CustomerSyncRun, staffID string, page wecomport.ExternalContactPage, observedAt time.Time) error {
	now := service.now()
	return service.UOW.Within(ctx, func(txContext context.Context) error {
		var activated, linked, conflicts, terminal, projected int64
		for _, contact := range page.Contacts {
			payload, _ := json.Marshal(contact)
			item := SyncItem{ExternalUserID: contact.ExternalUserID, ExternalUserIDDigest: sha256.Sum256([]byte(contact.ExternalUserID)),
				StaffIDDigest: sha256.Sum256([]byte(staffID)), PayloadDigest: sha256.Sum256(payload)}
			fact, factErr := wecomprovider.VerifiedExternalContact(service.CorpID, contact.ExternalUserID, "wecom.directory_sync")
			if factErr != nil {
				item.Outcome, item.ErrorCode = "terminal_failed", "invalid_external_contact"
				inserted, insertErr := service.Store.InsertItem(txContext, run.ID, run.CorpScope, item)
				if insertErr != nil {
					return insertErr
				}
				if inserted {
					terminal++
				}
				continue
			}
			provision, provisionErr := service.Identity.ProvisionVerifiedIdentity(txContext, identityport.ProvisionCommand{Fact: fact,
				IdempotencyKey: "wecom-sync:" + strconv.FormatInt(run.ID, 10) + ":" + itemDigestKey(item.ExternalUserIDDigest)})
			if provisionErr != nil {
				return provisionErr
			}
			item.CustomerID, item.IdentityID = provision.CustomerID, provision.IdentityID
			if provision.Created {
				item.Outcome = "activated"
			} else {
				item.Outcome = "already_linked"
			}
			inserted, insertErr := service.Store.InsertItem(txContext, run.ID, run.CorpScope, item)
			if insertErr != nil {
				return insertErr
			}
			if err := service.Store.UpsertProfileObservations(txContext, run.ID, run.CorpScope, provision.CustomerID, contact.FollowInfo, observedAt); err != nil {
				return err
			}
			if service.DescriptionIntents != nil {
				for _, follow := range contact.FollowInfo {
					if !follow.DescriptionProjected || follow.Description == nil || follow.EmployeeID == "" {
						continue
					}
					externalDigest := effectDigest(contact.ExternalUserID)
					observedDigest := outboundport.ContactDescriptionObservedDigest(*follow.Description)
					target := outboundport.ContactDescriptionTargetDigest(follow.EmployeeID, contact.ExternalUserID)
					payload := outboundport.ContactDescriptionPayloadDigest(observedDigest)
					_, enqueueErr := service.DescriptionIntents.WriteContactDescriptionIntentWithin(txContext, outboundport.ContactDescriptionIntentCommand{
						CustomerID: provision.CustomerID, EmployeeUserID: follow.EmployeeID,
						SourceDigest: effectDigest("wecom.contact.description.source.v1", "run", strconv.FormatInt(run.ID, 10), run.CorpScope, follow.EmployeeID, string(externalDigest), string(observedDigest)),
						TargetDigest: target, ObservedDescriptionDigest: observedDigest, PayloadDigest: payload,
						ReceiptKey: outboundport.ContactDescriptionRelationshipKey(run.CorpScope, follow.EmployeeID, externalDigest), SourceRunID: run.ID,
						// A manual run is an explicit administrator backfill/replan. It
						// may make a new immutable plan only after the former plan is
						// terminal and non-unknown; routine/callback maintenance cannot.
						Replan: run.Trigger == "manual", Operation: outboundport.ContactDescriptionOperationWrite,
					})
					if enqueueErr != nil {
						return enqueueErr
					}
				}
			}
			if !inserted {
				continue
			}
			profileDigest := sha256.Sum256(payload)
			if err := service.Store.UpsertProfile(txContext, run.ID, run.CorpScope, provision, contact, profileDigest, now); err != nil {
				return err
			}
			projection := customerport.DirectoryProjection{CustomerID: provision.CustomerID, CustomerStatus: customerdomain.StatusActive,
				DisplayName: contact.Name, AvatarURL: contact.AvatarURL, Gender: contact.Gender, ContactType: contact.Type, CorpName: contact.CorpName,
				OneIDLabel: "CID-" + strconv.FormatInt(int64(provision.CustomerID), 10), ActivationState: "active", Source: "wecom_directory_sync",
				SourceVersion: run.ID, LastSyncedAt: now, UpdatedAt: now}
			if err := service.Projection.UpsertDirectoryProjection(txContext, projection); err != nil {
				return err
			}
			if err := service.Timeline.AppendTimeline(txContext, customerport.TimelineEvent{CustomerID: provision.CustomerID,
				SourceDomain: "wecom", SourceEventID: "directory-sync:" + strconv.FormatInt(run.ID, 10) + ":" + itemDigestKey(item.ExternalUserIDDigest),
				EventType: "customer.profile_synced", Title: "企微客户资料已同步", OccurredAt: now}); err != nil {
				return err
			}
			outboxPayload, _ := json.Marshal(map[string]any{"customer_id": provision.CustomerID, "sync_run_id": run.ID})
			if _, err := service.Outbox.Append(txContext, platformoutbox.Event{AggregateType: "customer", AggregateID: strconv.FormatInt(int64(provision.CustomerID), 10),
				Type: "customer.directory_profile_projected", Version: 1, IdempotencyKey: "wecom-profile:" + strconv.FormatInt(run.ID, 10) + ":" + itemDigestKey(item.ExternalUserIDDigest), Payload: outboxPayload, OccurredAt: now, Processed: true}); err != nil {
				return err
			}
			projected++
			if provision.Created {
				activated++
			} else {
				linked++
			}
		}
		nextIndex, nextCursor, nextStatus := run.StaffIndex, page.NextCursor, SyncIngesting
		if nextCursor == "" {
			nextIndex++
			if nextIndex >= len(run.StaffIDs) {
				nextStatus = SyncReconciling
			}
		}
		if err := service.Store.AddCountsAndAdvance(txContext, run.ID, run.Version, activated, linked, conflicts, terminal, projected, nextIndex, nextCursor, nextStatus); err != nil {
			return err
		}
		_, err := service.Audit.Append(txContext, platformaudit.Event{IdempotencyKey: idempotency.Key("wecom-sync-page:" + strconv.FormatInt(run.ID, 10) + ":" + strconv.Itoa(run.StaffIndex) + ":" + cursorKey(run.ProviderCursor)),
			Action: "wecom.customer_sync_page_committed", ActorType: "system", ResourceType: "wecom_customer_sync", ResourceID: strconv.FormatInt(run.ID, 10),
			Payload: json.RawMessage(`{"pii":false}`), OccurredAt: now})
		return err
	})
}

func effectDigest(parts ...string) effectport.Digest { return effectport.Hash(parts...) }

func (service CustomerSyncService) reconcile(ctx context.Context, run CustomerSyncRun) error {
	now := service.now()
	return service.UOW.Within(ctx, func(txContext context.Context) error {
		if run.Discovered != run.Activated+run.AlreadyLinked+run.Conflict+run.TerminalFailed || run.Projected != run.Activated+run.AlreadyLinked {
			return ErrSyncCAS
		}
		staleIDs, err := service.Store.StaleCustomers(txContext, run.ID)
		if err != nil {
			return errSyncStaleRead
		}
		stale, err := service.Projection.MarkDirectoryStale(txContext, staleIDs, now)
		if err != nil {
			return errSyncProjection
		}
		if stale != int64(len(staleIDs)) {
			return ErrSyncCAS
		}
		if err = service.Store.ReconcileProfileObservations(txContext, run.ID, now); err != nil {
			return errSyncProjection
		}
		// A primary is valid only after every staff/page in this scope has been
		// ingested and its absent relationships reconciled in this same UoW.
		if err = service.Store.RefreshProfilePrimaryOwners(txContext, run.ID, now); err != nil {
			return errSyncProjection
		}
		pending, err := service.Outbox.PendingForSyncRun(txContext, run.ID)
		if err != nil {
			return errSyncOutboxReconcile
		}
		if pending != 0 {
			return ErrSyncCAS
		}
		if err = service.Store.Complete(txContext, run.ID, run.Version, stale); err != nil {
			return errSyncCompletion
		}
		_, err = service.Audit.Append(txContext, platformaudit.Event{IdempotencyKey: idempotency.Key("wecom-sync-complete:" + strconv.FormatInt(run.ID, 10)),
			Action: "wecom.customer_sync_succeeded", ActorType: "system", ResourceType: "wecom_customer_sync", ResourceID: strconv.FormatInt(run.ID, 10),
			Payload: json.RawMessage(`{"reconciled":true}`), OccurredAt: now})
		if err != nil {
			return errSyncAudit
		}
		return nil
	})
}

func (service CustomerSyncService) recordFailure(ctx context.Context, run CustomerSyncRun, cause error) error {
	status, code := classifySyncFailure(cause)
	err := service.UOW.Within(ctx, func(txContext context.Context) error {
		return service.Store.Fail(txContext, run.ID, run.Version, status, code)
	})
	if err != nil {
		return err
	}
	maxAttempts := 0
	var limited wecomport.DirectoryFailureAttemptLimit
	if errors.As(cause, &limited) && limited.DirectoryFailureMaxAttempts() > 0 {
		maxAttempts = limited.DirectoryFailureMaxAttempts()
	}
	return &syncRetryError{code: code, maxAttempts: maxAttempts}
}

func classifySyncFailure(cause error) (CustomerSyncStatus, string) {
	status, code := SyncFailedRetryable, "provider_unavailable"
	if errors.Is(cause, wecomport.ErrDirectoryDisabled) {
		status, code = SyncFailedTerminal, "provider_disabled"
	} else {
		var failure wecomport.DirectoryFailure
		if errors.As(cause, &failure) {
			code = failure.DirectoryFailureCode()
			if !failure.DirectoryFailureRetryable() {
				status = SyncFailedTerminal
			}
		}
	}
	return status, code
}

func (service CustomerSyncService) now() time.Time {
	if service.Now != nil {
		return service.Now().UTC()
	}
	return time.Now().UTC()
}
func itemDigestKey(value [32]byte) string {
	return strconv.FormatUint(uint64(value[0])<<56|uint64(value[1])<<48|uint64(value[2])<<40|uint64(value[3])<<32|uint64(value[4])<<24|uint64(value[5])<<16|uint64(value[6])<<8|uint64(value[7]), 36)
}
func cursorKey(value string) string {
	digest := sha256.Sum256([]byte(value))
	return itemDigestKey(digest)
}
