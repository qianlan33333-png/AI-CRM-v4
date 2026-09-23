package wecom

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode"

	channeldomain "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/domain"
	channelport "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/port"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	platformoutbox "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/outbox"
)

var (
	ErrInvalidExternalContactLifecycle = errors.New("invalid external contact lifecycle")
	ErrInvalidExternalContactFact      = errors.New("invalid external contact lifecycle fact")
)

const (
	ChangeAddExternalContact     = "add_external_contact"
	ChangeAddHalfExternalContact = "add_half_external_contact"
	ChangeEditExternalContact    = "edit_external_contact"
	ChangeDelFollowUser          = "del_follow_user"
	ChangeDelExternalContact     = "del_external_contact"
)

// CallbackOutcome is append-only: one verified callback can create or resolve
// a customer, change a relationship, and produce a separate channel result.
type CallbackOutcome string

const (
	OutcomeCustomerCreated         CallbackOutcome = "customer_created"
	OutcomeCustomerResolved        CallbackOutcome = "customer_resolved"
	OutcomeRelationshipActivated   CallbackOutcome = "relationship_activated"
	OutcomeRelationshipDeactivated CallbackOutcome = "relationship_deactivated"
	OutcomeChannelAttributed       CallbackOutcome = "channel_attributed"
	OutcomeChannelUnmatched        CallbackOutcome = "channel_unmatched"
	OutcomeChannelAmbiguous        CallbackOutcome = "channel_ambiguous"
	OutcomeIdentityConflict        CallbackOutcome = "identity_conflict"
	OutcomeIgnored                 CallbackOutcome = "ignored"
)

// ExternalContactLifecycleFact is constructed only after provider verification
// and durable inboxing. VerifiedIdentity remains opaque to HTTP DTOs, while
// CallbackID identifies the durable callback without exposing external_userid
// in downstream receipt keys.
type ExternalContactLifecycleFact struct {
	CallbackID       string
	InboxID          int64
	CorpID           string
	ChangeType       string
	ExternalUserID   string
	EmployeeUserID   string
	HasState         bool
	StateDigest      [32]byte
	WelcomeGrantRef  string
	OccurredAt       time.Time
	VerifiedIdentity identitydomain.VerifiedFact
}

func (fact ExternalContactLifecycleFact) Valid() bool {
	if !validLifecycleText(fact.CallbackID, 512) || fact.InboxID < 1 || !validLifecycleText(fact.CorpID, 256) ||
		!validLifecycleText(fact.ExternalUserID, 1024) || fact.OccurredAt.IsZero() ||
		(fact.EmployeeUserID != "" && !validLifecycleText(fact.EmployeeUserID, 1024)) ||
		(fact.supported() && fact.EmployeeUserID == "") ||
		(!fact.HasState && fact.StateDigest != ([32]byte{})) ||
		(fact.HasState && fact.StateDigest == ([32]byte{})) || !fact.VerifiedIdentity.Valid() {
		return false
	}
	if fact.WelcomeGrantRef != "" && !strings.HasPrefix(fact.WelcomeGrantRef, "wgrant_") {
		return false
	}
	reference := fact.VerifiedIdentity.Reference()
	return reference.Kind == identitydomain.KindWeComExternalUserID &&
		reference.Scope == "wecom-corp:"+fact.CorpID && reference.NormalizedValue == fact.ExternalUserID &&
		reference.Assurance == identitydomain.AssuranceVerified && reference.Source == "wecom.callback"
}

func (fact ExternalContactLifecycleFact) entrant() bool {
	return fact.ChangeType == ChangeAddExternalContact || fact.ChangeType == ChangeAddHalfExternalContact
}

func (fact ExternalContactLifecycleFact) createsWhenMissing() bool {
	return fact.entrant() || fact.ChangeType == ChangeEditExternalContact
}

func (fact ExternalContactLifecycleFact) deletesRelationship() bool {
	return fact.ChangeType == ChangeDelFollowUser || fact.ChangeType == ChangeDelExternalContact
}

func (fact ExternalContactLifecycleFact) supported() bool {
	return fact.entrant() || fact.ChangeType == ChangeEditExternalContact || fact.deletesRelationship()
}

// ExternalContactIdentity is the narrow cross-domain OneID port. Resolver
// results must carry the canonical root; callers never traverse merge history
// or select a customer themselves.
type ExternalContactIdentity interface {
	identityport.Resolver
	identityport.VerifiedProvisioner
}

// ExternalContactLifecycle applies local callback effects in the transaction
// context supplied by its caller. It has no database/store implementation and
// never performs a provider network call. Its adapters must make relationship
// and entrant writes idempotent by their natural keys / CallbackID,
// respectively. The inbox worker owns the surrounding transaction so Inbox
// completion, callback receipt, audit, OneID and relationship writes commit or
// roll back together.
type ExternalContactLifecycle struct {
	Identity      ExternalContactIdentity
	Relationships CallbackFollowRelationshipStore
	States        channelport.StateResolver
	Entrants      channelport.EntrantReceiptRecorder
	Actions       channelport.EntrantActionAccepter
	Directory     customerport.CallbackProjectionWriter
	Outbox        platformoutbox.Appender
}

type ExternalContactLifecycleResult struct {
	CustomerID customerdomain.CustomerID
	Outcomes   []CallbackOutcome
}

// ProcessWithin deliberately does not start a UnitOfWork. Callers must pass a
// transaction-bound context; this prevents nested PostgreSQL transactions and
// lets the delivery processor atomically complete its Inbox with these effects.
func (service ExternalContactLifecycle) ProcessWithin(ctx context.Context, fact ExternalContactLifecycleFact) (ExternalContactLifecycleResult, error) {
	if ctx == nil || service.Identity == nil || service.Relationships == nil || !fact.Valid() {
		return ExternalContactLifecycleResult{}, ErrInvalidExternalContactLifecycle
	}
	if fact.entrant() && (service.States == nil || service.Entrants == nil) {
		return ExternalContactLifecycleResult{}, ErrInvalidExternalContactLifecycle
	}
	var result ExternalContactLifecycleResult
	if !fact.supported() {
		result.Outcomes = []CallbackOutcome{OutcomeIgnored}
		return result, nil
	}
	admission, err := service.Relationships.AdmitExternalContactEvent(ctx, CallbackExternalContactEvent{
		CallbackID: fact.CallbackID, CorpID: fact.CorpID, EmployeeID: fact.EmployeeUserID,
		ExternalIdentityDigest: externalContactIdentityDigest(fact),
		Active:                 !fact.deletesRelationship(), OccurredAt: fact.OccurredAt,
	})
	if err != nil {
		return ExternalContactLifecycleResult{}, err
	}
	if !admission.Admitted {
		result.Outcomes = []CallbackOutcome{OutcomeIgnored}
		return result, nil
	}
	customerID, resolved, conflict, err := service.resolveCustomer(ctx, fact)
	if err != nil {
		return ExternalContactLifecycleResult{}, err
	}
	if conflict {
		// A conflict has no reliable customer root. Do not update a follow
		// relationship and, critically, do not resolve or write a channel
		// attribution. Entrant conflicts still receive an isolated receipt so
		// the safe operations API can surface them for explicit reconciliation.
		result.Outcomes = []CallbackOutcome{OutcomeIdentityConflict}
		if fact.entrant() {
			if err := service.Entrants.RecordEntrantReceipt(ctx, channelport.EntrantReceipt{
				CallbackID: fact.CallbackID, InboxID: fact.InboxID, CorpID: fact.CorpID,
				ChangeType: fact.ChangeType, Status: channelport.EntrantReceiptIdentityConflict,
				OccurredAt: fact.OccurredAt,
			}); err != nil {
				return ExternalContactLifecycleResult{}, err
			}
		}
		return result, nil
	}
	if customerID < 1 {
		result.Outcomes = []CallbackOutcome{OutcomeIgnored}
		return result, nil
	}
	result.CustomerID = customerID
	if resolved {
		result.Outcomes = append(result.Outcomes, OutcomeCustomerResolved)
	} else {
		result.Outcomes = append(result.Outcomes, OutcomeCustomerCreated)
	}
	if fact.EmployeeUserID != "" {
		active := !fact.deletesRelationship()
		application, err := service.Relationships.ApplyCallbackEvent(ctx, CallbackFollowRelationship{
			CallbackID: fact.CallbackID, CorpID: fact.CorpID, EmployeeID: fact.EmployeeUserID,
			CustomerID: customerID, Active: active, OccurredAt: fact.OccurredAt,
		})
		if err != nil {
			return ExternalContactLifecycleResult{}, err
		}
		if application.Applied && application.Active {
			result.Outcomes = append(result.Outcomes, OutcomeRelationshipActivated)
		} else if application.Applied {
			result.Outcomes = append(result.Outcomes, OutcomeRelationshipDeactivated)
		}
	}
	if !fact.deletesRelationship() && (service.Directory != nil || service.Outbox != nil) {
		if service.Directory == nil || service.Outbox == nil {
			return ExternalContactLifecycleResult{}, ErrInvalidExternalContactLifecycle
		}
		if err := service.Directory.ActivateDirectoryCustomer(ctx, customerID, "wecom_callback", fact.OccurredAt); err != nil {
			return ExternalContactLifecycleResult{}, err
		}
		payload, err := json.Marshal(map[string]any{"customer_id": customerID, "source": "wecom_callback"})
		if err != nil {
			return ExternalContactLifecycleResult{}, err
		}
		if _, err = service.Outbox.Append(ctx, platformoutbox.Event{
			AggregateType: "customer", AggregateID: strconv.FormatInt(int64(customerID), 10),
			Type: "customer.directory_callback_activated", Version: 1,
			IdempotencyKey: callbackProjectionKey(fact.CallbackID),
			Payload:        payload, OccurredAt: fact.OccurredAt, Processed: true,
		}); err != nil {
			return ExternalContactLifecycleResult{}, err
		}
	}
	if fact.entrant() {
		if err := service.correlateEntrant(ctx, fact, customerID, &result); err != nil {
			return ExternalContactLifecycleResult{}, err
		}
	}
	return result, nil
}

func callbackProjectionKey(callbackID string) string {
	digest := sha256.Sum256([]byte(callbackID))
	return "wecom-callback-projection:" + base64.RawURLEncoding.EncodeToString(digest[:])
}

func externalContactIdentityDigest(fact ExternalContactLifecycleFact) [32]byte {
	reference := fact.VerifiedIdentity.Reference()
	material := strings.Join([]string{
		"wecom-external-contact-cursor-v1", string(reference.Kind), reference.Scope,
		reference.NormalizedValue,
	}, "\x00")
	return sha256.Sum256([]byte(material))
}

func (service ExternalContactLifecycle) resolveCustomer(ctx context.Context, fact ExternalContactLifecycleFact) (customerdomain.CustomerID, bool, bool, error) {
	reference := fact.VerifiedIdentity.Reference()
	resolved, err := service.Identity.Resolve(ctx, identitydomain.Reference{
		Kind: reference.Kind, Scope: reference.Scope, Value: reference.NormalizedValue,
		Assurance: reference.Assurance, Source: reference.Source,
	})
	if err != nil {
		return 0, false, false, err
	}
	switch resolved.Status {
	case identityport.ResolveFound:
		if resolved.CustomerID < 1 {
			return 0, false, false, ErrInvalidExternalContactFact
		}
		return resolved.CustomerID, true, false, nil
	case identityport.ResolveConflict:
		if resolved.CustomerID != 0 {
			return 0, false, false, ErrInvalidExternalContactFact
		}
		return 0, false, true, nil
	case identityport.ResolveNotFound:
		if !fact.createsWhenMissing() {
			return 0, false, false, nil
		}
		provisioned, err := service.Identity.ProvisionVerifiedIdentity(ctx, identityport.ProvisionCommand{
			Fact: fact.VerifiedIdentity, IdempotencyKey: fact.CallbackID,
		})
		if err != nil {
			return 0, false, false, err
		}
		if provisioned.CustomerID < 1 || provisioned.IdentityID < 1 {
			return 0, false, false, ErrInvalidExternalContactFact
		}
		return provisioned.CustomerID, !provisioned.Created, false, nil
	default:
		return 0, false, false, ErrInvalidExternalContactFact
	}
}

func (service ExternalContactLifecycle) correlateEntrant(ctx context.Context, fact ExternalContactLifecycleFact, customerID customerdomain.CustomerID, result *ExternalContactLifecycleResult) error {
	resolution := channeldomain.StateResolution{Status: channeldomain.StateUnmatched}
	if fact.HasState {
		resolved, err := service.States.ResolveStateDigest(ctx, fact.CorpID, fact.StateDigest, fact.OccurredAt)
		if err != nil {
			return err
		}
		if !resolved.Valid() {
			return ErrInvalidExternalContactFact
		}
		resolution = resolved
	}
	if err := service.Entrants.RecordEntrantReceipt(ctx, channelport.EntrantReceipt{
		CallbackID: fact.CallbackID, InboxID: fact.InboxID, CorpID: fact.CorpID, ChangeType: fact.ChangeType,
		Status: entrantReceiptStatus(resolution.Status), CustomerID: customerID,
		OccurredAt: fact.OccurredAt, Resolution: resolution,
	}); err != nil {
		return err
	}
	switch resolution.Status {
	case channeldomain.StateAttributed:
		skipped := false
		if service.Actions != nil {
			if err := service.Actions.AcceptEntrantActions(ctx, channelport.EntrantActionCommand{CallbackID: fact.CallbackID, CustomerID: customerID, Resolution: resolution, WelcomeGrantRef: fact.WelcomeGrantRef, OccurredAt: fact.OccurredAt}); err != nil {
				if !errors.Is(err, channelport.ErrEntrantActionsSkippedInactiveChannel) {
					return err
				}
				skipped = true
			}
		}
		result.Outcomes = append(result.Outcomes, OutcomeChannelAttributed)
		if skipped {
			result.Outcomes = append(result.Outcomes, OutcomeIgnored)
		}
	case channeldomain.StateUnmatched:
		result.Outcomes = append(result.Outcomes, OutcomeChannelUnmatched)
	case channeldomain.StateAmbiguous:
		result.Outcomes = append(result.Outcomes, OutcomeChannelAmbiguous)
	default:
		return ErrInvalidExternalContactFact
	}
	return nil
}

func entrantReceiptStatus(status channeldomain.StateResolutionStatus) channelport.EntrantReceiptStatus {
	switch status {
	case channeldomain.StateAttributed:
		return channelport.EntrantReceiptAttributed
	case channeldomain.StateUnmatched:
		return channelport.EntrantReceiptUnmatched
	case channeldomain.StateAmbiguous:
		return channelport.EntrantReceiptAmbiguous
	default:
		return ""
	}
}

func validLifecycleText(value string, maximum int) bool {
	return value != "" && len(value) <= maximum && strings.TrimSpace(value) == value && strings.IndexFunc(value, unicode.IsControl) < 0
}
