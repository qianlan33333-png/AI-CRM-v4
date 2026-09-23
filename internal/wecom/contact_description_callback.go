package wecom

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	platformjobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/webhook"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
	"github.com/riverqueue/river"
)

const contactDescriptionCallbackJobTimeout = 2 * time.Minute

// ContactDescriptionCallbackJobArgs contains only an internal Inbox ID. Raw
// external_userid stays in the encrypted/durable callback payload until this
// worker reads it in memory after the callback is already authenticated.
type ContactDescriptionCallbackJobArgs struct {
	InboxID int64 `json:"inbox_id"`
}

func (ContactDescriptionCallbackJobArgs) Kind() string {
	return "wecom.contact-description-observation.v1"
}

type ContactDescriptionCallbackJobEnqueuer interface {
	EnqueueContactDescriptionObservation(context.Context, int64) error
}

type RiverContactDescriptionCallbackEnqueuer struct{ client *river.Client[pgx.Tx] }

func NewRiverContactDescriptionCallbackEnqueuer(client *river.Client[pgx.Tx]) (*RiverContactDescriptionCallbackEnqueuer, error) {
	if client == nil {
		return nil, ErrSyncNotReady
	}
	return &RiverContactDescriptionCallbackEnqueuer{client: client}, nil
}

func (e *RiverContactDescriptionCallbackEnqueuer) EnqueueContactDescriptionObservation(ctx context.Context, inboxID int64) error {
	if e == nil || e.client == nil || inboxID < 1 {
		return ErrSyncNotReady
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	_, err = platformjobqueue.InsertTxWithOptions(ctx, e.client, tx, ContactDescriptionCallbackJobArgs{InboxID: inboxID}, river.InsertOpts{
		Queue: CustomerSyncQueue, MaxAttempts: 12, UniqueOpts: river.UniqueOpts{ByArgs: true},
	})
	return err
}

// ContactDescriptionCallbackService makes the callback's lightweight durable
// follow-up read explicit. It never writes WeCom: after the real detail read it
// atomically accepts an Outbound intent, which is the sole owner of the write.
type ContactDescriptionCallbackService struct {
	Enabled       bool
	CorpID        string
	Inbox         *webhook.Service
	Provider      wecomport.ExternalContactDescriptionTargetReader
	Identity      identityport.Resolver
	Relationships FollowRelationshipStore
	Intents       outboundport.ContactDescriptionIntentWriter
	UOW           platformport.UnitOfWork
}

func (s ContactDescriptionCallbackService) Ready() bool {
	return s.Enabled && s.CorpID != "" && s.Inbox != nil && s.Provider != nil && s.Identity != nil && s.Relationships != nil && s.Intents != nil && s.UOW != nil
}

type callbackDescriptionTarget struct {
	event      CallbackEvent
	customerID customerdomain.CustomerID
}

func (s ContactDescriptionCallbackService) Process(ctx context.Context, inboxID int64) error {
	if !s.Ready() || inboxID < 1 {
		return ErrSyncNotReady
	}
	target, skip, err := s.loadTarget(ctx, inboxID)
	if err != nil || skip {
		return err
	}
	descriptionTarget, err := s.Provider.ReadExternalContactDescriptionTarget(ctx, target.event.ExternalUserID, target.event.UserID)
	if err != nil {
		return err
	}
	if !descriptionTarget.Projected {
		// A missing relationship or unprojected field is not evidence that the
		// description is empty. The job ends safely and never writes.
		return nil
	}
	description := descriptionTarget.Description
	externalDigest := effectport.Hash(target.event.ExternalUserID)
	observed := outboundport.ContactDescriptionObservedDigest(description)
	command := outboundport.ContactDescriptionIntentCommand{
		CustomerID:                target.customerID,
		EmployeeUserID:            target.event.UserID,
		SourceDigest:              effectport.Hash("wecom.contact.description.source.v1", "callback", strconv.FormatInt(inboxID, 10), target.event.UserID, string(externalDigest), string(observed)),
		TargetDigest:              outboundport.ContactDescriptionTargetDigest(target.event.UserID, target.event.ExternalUserID),
		ObservedDescriptionDigest: observed,
		PayloadDigest:             outboundport.ContactDescriptionPayloadDigest(observed),
		ReceiptKey:                outboundport.ContactDescriptionRelationshipKey("wecom-corp:"+s.CorpID, target.event.UserID, externalDigest),
		Operation:                 outboundport.ContactDescriptionOperationWrite,
	}
	err = s.UOW.Within(ctx, func(txContext context.Context) error {
		current, stale, loadErr := s.loadTargetWithin(txContext, inboxID)
		if loadErr != nil || stale {
			return loadErr
		}
		if current.customerID != target.customerID || current.event.UserID != target.event.UserID || current.event.ExternalUserID != target.event.ExternalUserID {
			return nil
		}
		_, writeErr := s.Intents.WriteContactDescriptionIntentWithin(txContext, command)
		if errors.Is(writeErr, outboundport.ErrContactDescriptionReplanRequired) || errors.Is(writeErr, outboundport.ErrContactDescriptionInFlight) || errors.Is(writeErr, outboundport.ErrContactDescriptionOutcomeUnknown) {
			// A callback cannot silently replace a prior immutable snapshot and
			// must never retry an unknown provider write under a new key.
			return nil
		}
		return writeErr
	})
	return err
}

func (s ContactDescriptionCallbackService) loadTarget(ctx context.Context, inboxID int64) (callbackDescriptionTarget, bool, error) {
	var target callbackDescriptionTarget
	var skip bool
	err := s.UOW.Within(ctx, func(txContext context.Context) error {
		var err error
		target, skip, err = s.loadTargetWithin(txContext, inboxID)
		return err
	})
	return target, skip, err
}

func (s ContactDescriptionCallbackService) loadTargetWithin(ctx context.Context, inboxID int64) (callbackDescriptionTarget, bool, error) {
	delivery, err := s.Inbox.Get(ctx, inboxID)
	if err != nil {
		return callbackDescriptionTarget{}, false, err
	}
	if delivery.Provider != callbackProvider || delivery.Status != webhook.StatusProcessed {
		return callbackDescriptionTarget{}, true, nil
	}
	event, err := decodeDescriptionCallbackEvent(delivery.Payload)
	if err != nil {
		return callbackDescriptionTarget{}, false, err
	}
	if event.CorpID == "" {
		event.CorpID = s.CorpID
	}
	// The PRD limits automatic maintenance to fully-added contacts. Half/add,
	// edit and delete callbacks retain their existing lifecycle behavior only.
	if event.CorpID != s.CorpID || event.Event != "change_external_contact" || event.ChangeType != ChangeAddExternalContact || event.UserID == "" || event.ExternalUserID == "" {
		return callbackDescriptionTarget{}, true, nil
	}
	resolved, err := s.Identity.Resolve(ctx, identitydomain.Reference{Kind: identitydomain.KindWeComExternalUserID, Scope: "wecom-corp:" + s.CorpID, Value: event.ExternalUserID, Assurance: identitydomain.AssuranceVerified, Source: "wecom.callback"})
	if err != nil {
		return callbackDescriptionTarget{}, false, err
	}
	if resolved.Status != identityport.ResolveFound || resolved.CustomerID < 1 {
		return callbackDescriptionTarget{}, true, nil
	}
	active, err := s.Relationships.IsActive(ctx, s.CorpID, event.UserID, resolved.CustomerID)
	if err != nil {
		return callbackDescriptionTarget{}, false, err
	}
	if !active {
		return callbackDescriptionTarget{}, true, nil
	}
	return callbackDescriptionTarget{event: event, customerID: resolved.CustomerID}, false, nil
}

func decodeDescriptionCallbackEvent(payload json.RawMessage) (CallbackEvent, error) {
	var event CallbackEvent
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&event); err != nil {
		return CallbackEvent{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return CallbackEvent{}, errors.New("invalid callback payload")
	}
	return event, nil
}

type ContactDescriptionCallbackWorker struct {
	river.WorkerDefaults[ContactDescriptionCallbackJobArgs]
	service *ContactDescriptionCallbackService
}

func NewContactDescriptionCallbackWorker() *ContactDescriptionCallbackWorker {
	return &ContactDescriptionCallbackWorker{}
}

func (*ContactDescriptionCallbackWorker) Timeout(*river.Job[ContactDescriptionCallbackJobArgs]) time.Duration {
	return contactDescriptionCallbackJobTimeout
}

func (w *ContactDescriptionCallbackWorker) BindService(service ContactDescriptionCallbackService) error {
	if w == nil || w.service != nil || !service.Ready() {
		return ErrSyncNotReady
	}
	w.service = &service
	return nil
}

func (w *ContactDescriptionCallbackWorker) Work(ctx context.Context, job *river.Job[ContactDescriptionCallbackJobArgs]) error {
	if w == nil || w.service == nil || job == nil || job.JobRow == nil || job.Args.InboxID < 1 {
		return ErrSyncNotReady
	}
	return w.service.Process(ctx, job.Args.InboxID)
}
