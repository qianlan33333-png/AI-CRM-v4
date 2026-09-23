package outbound

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	channelport "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/port"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	tagport "github.com/qianlan33333-png/AI-CRM-v3/internal/tag/port"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

type ChannelEntrantProvider struct {
	reader        channelport.PublishedEntrantActionReader
	messages      channelport.WelcomeMessageFreezer
	uow           platformport.UnitOfWork
	grants        wecomport.WelcomeGrantRedeemer
	relationships wecomport.CurrentExternalContactReader
	tags          tagport.ProviderTagBindingReader
	writer        wecomport.EntrantActionWriter
	Now           func() time.Time
}

func NewChannelEntrantProvider(reader channelport.PublishedEntrantActionReader, messages channelport.WelcomeMessageFreezer, uow platformport.UnitOfWork, grants wecomport.WelcomeGrantRedeemer, relationships wecomport.CurrentExternalContactReader, tags tagport.ProviderTagBindingReader, writer wecomport.EntrantActionWriter) *ChannelEntrantProvider {
	return &ChannelEntrantProvider{reader: reader, messages: messages, uow: uow, grants: grants, relationships: relationships, tags: tags, writer: writer}
}

func (provider *ChannelEntrantProvider) Execute(ctx context.Context, envelope effectport.Envelope, attempt effectport.Attempt) (effectport.AdapterResult, error) {
	if provider == nil || provider.reader == nil || provider.uow == nil || provider.writer == nil || !envelope.Valid() || (envelope.Kind != effectport.KindChannelWelcome && envelope.Kind != effectport.KindChannelEntryTag) {
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash("channel.entrant.invalid")}, nil
	}
	action, err := provider.reader.ReadPublishedEntrantAction(ctx, string(envelope.SourceRefDigest))
	if err != nil {
		if envelope.Kind == effectport.KindChannelWelcome {
			return welcomeAdapterResult(effectport.StateRetryable, effectport.Hash("channel.entrant.read-unavailable", string(envelope.Fingerprint())), "provider_unavailable", false), nil
		}
		return effectport.AdapterResult{Completion: effectport.StateRetryable, ReceiptDigest: effectport.Hash("channel.entrant.read-unavailable", string(envelope.Fingerprint()))}, nil
	}
	if envelope.Kind == effectport.KindChannelWelcome {
		if action.Kind != "welcome" || provider.grants == nil || provider.messages == nil {
			return welcomeAdapterResult(effectport.StateFinalFailed, effectport.Hash("channel.welcome.not-configured"), "welcome_not_configured", false), nil
		}
		// Pre-0066 entrant records did not carry a first receipt deadline. Their
		// ten-minute encrypted grant retention is not permission to send, so they
		// are a durable no-send rather than a fresh execution window.
		if action.SendDeadlineAt.IsZero() {
			return welcomeAdapterResult(effectport.StateFinalFailed, effectport.Hash("channel.welcome.deadline-missing-not-attempted", action.EffectRef), "deadline_missing", false), nil
		}
		if !provider.now().Before(action.SendDeadlineAt) {
			return welcomeAdapterResult(effectport.StateFinalFailed, effectport.Hash("channel.welcome.expired-not-attempted", action.EffectRef), "deadline_expired", false), nil
		}
		attachments, materialErr := welcomeAttachments(action.WelcomeMaterialSnapshot)
		if materialErr != nil {
			return welcomeAdapterResult(effectport.StateFinalFailed, effectport.Hash("channel.welcome.material-invalid", action.EffectRef), "material_invalid", false), nil
		}
		var message string
		// The Channel adapter owns one short UOW containing its row lock, Customer
		// presentation read and snapshot insert. Do not wrap it again here: nested
		// UOWs could split those facts or accidentally hold one through Provider IO.
		message, err = provider.messages.FreezePublishedWelcomeMessage(ctx, channelport.WelcomeMessageFreezeRequest{EffectRef: action.EffectRef, Envelope: envelope})
		if err != nil {
			switch {
			case errors.Is(err, channelport.ErrWelcomeMessageCustomerNameUnavailable):
				return welcomeAdapterResult(effectport.StateRetryable, effectport.Hash("channel.welcome.customer-name-unavailable", action.EffectRef, strconv.Itoa(int(attempt.Number))), "customer_name_unavailable", false), nil
			case errors.Is(err, channelport.ErrWelcomeMessageTemplateInvalid):
				return welcomeAdapterResult(effectport.StateFinalFailed, effectport.Hash("channel.welcome.template-invalid", action.EffectRef), "welcome_template_invalid", false), nil
			case errors.Is(err, channelport.ErrWelcomeMessageTooLong):
				return welcomeAdapterResult(effectport.StateFinalFailed, effectport.Hash("channel.welcome.message-too-long", action.EffectRef), "welcome_message_too_long", false), nil
			default:
				return welcomeAdapterResult(effectport.StateFinalFailed, effectport.Hash("channel.welcome.message-unavailable", action.EffectRef), "frozen_message_unavailable", false), nil
			}
		}
		var welcomeCode string
		err = provider.uow.Within(ctx, func(tx context.Context) error {
			var redeemErr error
			welcomeCode, redeemErr = provider.grants.Redeem(tx, action.WelcomeGrantRef, action.EffectRef)
			return redeemErr
		})
		if err != nil {
			if errors.Is(err, wecomport.ErrWelcomeGrantExpired) {
				return welcomeAdapterResult(effectport.StateFinalFailed, effectport.Hash("channel.welcome.grant-expired-not-attempted", action.EffectRef), "grant_expired", false), nil
			}
			return welcomeAdapterResult(effectport.StateRetryable, effectport.Hash("channel.welcome.grant-unavailable-not-attempted", action.EffectRef, strconv.Itoa(int(attempt.Number))), "provider_unavailable", false), nil
		}
		if !provider.now().Before(action.SendDeadlineAt) {
			welcomeCode = ""
			return welcomeAdapterResult(effectport.StateFinalFailed, effectport.Hash("channel.welcome.expired-not-attempted", action.EffectRef), "deadline_expired", false), nil
		}
		// Keep the Provider HTTP budget within the frozen business deadline. A
		// writer that honors context therefore cannot start a normal timeout
		// after the welcome window has already elapsed.
		callContext, cancel := ctx, func() {}
		callContext, cancel = context.WithDeadline(ctx, action.SendDeadlineAt)
		if callContext.Err() != nil {
			cancel()
			welcomeCode = ""
			return welcomeAdapterResult(effectport.StateFinalFailed, effectport.Hash("channel.welcome.expired-not-attempted", action.EffectRef), "deadline_expired", false), nil
		}
		err = provider.writer.SendWelcomeMessage(callContext, welcomeCode, message, attachments)
		cancel()
		welcomeCode = ""
	} else {
		if action.Kind != "entry_tag" || provider.relationships == nil || provider.tags == nil {
			return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash("channel.entry-tag.not-configured")}, nil
		}
		contact, readErr := provider.relationships.CurrentExternalContact(ctx, customerdomain.CustomerID(action.CustomerID), action.StaffID)
		if readErr != nil {
			return effectport.AdapterResult{Completion: effectport.StateRetryable, ReceiptDigest: effectport.Hash("channel.entry-tag.relationship-unavailable", action.EffectRef)}, nil
		}
		providerTagID, found, readErr := provider.tags.ProviderTagID(ctx, action.LocalTagID)
		if readErr != nil || !found {
			return effectport.AdapterResult{Completion: effectport.StateRetryable, ReceiptDigest: effectport.Hash("channel.entry-tag.binding-unavailable", action.EffectRef)}, nil
		}
		err = provider.writer.AddContactTag(ctx, contact.EmployeeUserID, contact.ExternalUserID, providerTagID)
	}
	if err != nil {
		attempted := wecomport.ProviderCallAttempted(err)
		state := effectport.StateRetryable
		if attempted {
			state = effectport.StateUnknown
		}
		if envelope.Kind == effectport.KindChannelWelcome {
			// A strictly parsed numeric Provider errcode is a complete rejection,
			// unlike a transport/proxy ambiguity. Return nil here so External
			// Effects records the final result rather than conservatively replacing
			// every non-nil post-call error with outcome_unknown.
			if attempted && wecomport.ProviderWriteClassified(err) && !wecomport.ProviderOutcomeUnknown(err) {
				if code, known := wecomport.ProviderErrorCode(err); known {
					return welcomeProviderRejectedResult(action.EffectRef, attempt.Number, code), nil
				}
			}
			reason := "provider_unavailable"
			if attempted {
				reason = "outcome_unknown"
			}
			return welcomeAdapterResult(state, effectport.Hash("channel.entrant.provider-error", action.EffectRef, strconv.Itoa(int(attempt.Number))), reason, attempted), err
		}
		return effectport.AdapterResult{Completion: state, ReceiptDigest: effectport.Hash("channel.entrant.provider-error", action.EffectRef, strconv.Itoa(int(attempt.Number))), CallAttempted: attempted, RealExternalCallExecuted: attempted}, err
	}
	if envelope.Kind == effectport.KindChannelWelcome {
		return welcomeAdapterResult(effectport.StateExecuted, effectport.Hash("channel.entrant.executed", action.EffectRef, strconv.Itoa(int(attempt.Number))), "sent", true), nil
	}
	return effectport.AdapterResult{Completion: effectport.StateExecuted, ReceiptDigest: effectport.Hash("channel.entrant.executed", action.EffectRef, strconv.Itoa(int(attempt.Number))), CallAttempted: true, RealExternalCallExecuted: true}, nil
}

func (provider *ChannelEntrantProvider) now() time.Time {
	if provider != nil && provider.Now != nil {
		return provider.Now().UTC()
	}
	return time.Now().UTC()
}

const channelWelcomeOutcomeArtifactKind = "channel.welcome.outcome.v1"

// welcomeAdapterResult carries only a closed safe reason into Channel's
// completion receipt. It never includes a decrypted welcome code, raw
// callback body, or Provider response.
func welcomeAdapterResult(state effectport.State, receipt effectport.Digest, reason string, attempted bool) effectport.AdapterResult {
	payload := []byte(`{"reason":"` + reason + `"}`)
	artifact := effectport.ResultArtifact{Kind: channelWelcomeOutcomeArtifactKind, Payload: payload}
	artifact.Digest = effectport.Hash("external-effect.artifact.v1", artifact.Kind, string(payload))
	return effectport.AdapterResult{Completion: state, ReceiptDigest: receipt, CallAttempted: attempted, RealExternalCallExecuted: attempted, Artifact: artifact}
}

func welcomeProviderRejectedResult(effectRef string, attempt int32, code int64) effectport.AdapterResult {
	result := welcomeAdapterResult(effectport.StateFinalFailed, effectport.Hash("channel.welcome.provider-rejected", effectRef, strconv.Itoa(int(attempt)), strconv.FormatInt(code, 10)), "provider_rejected", true)
	result.FailureCode = "wecom_errcode_" + strconv.FormatInt(code, 10)
	return result
}

func welcomeAttachments(raw json.RawMessage) ([]wecomport.WelcomeAttachment, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var snapshot mediaport.GroupOpsMaterialSnapshot
	if json.Unmarshal(raw, &snapshot) != nil || mediaport.ValidateGroupOpsMaterialSnapshot(snapshot) != nil {
		return nil, errors.New("invalid channel welcome material snapshot")
	}
	result := make([]wecomport.WelcomeAttachment, len(snapshot.Attachments))
	for index, item := range snapshot.Attachments {
		result[index] = wecomport.WelcomeAttachment{MsgType: item.MsgType, MediaID: item.MediaID, AppID: item.AppID, PagePath: item.PagePath, Title: item.Title, URL: item.URL, Description: item.Description, PicURL: item.PicURL}
	}
	return result, nil
}

type ChannelEntrantCompletionSink struct {
	writer channelport.EntrantActionCompletionWriter
}

func NewChannelEntrantCompletionSink(writer channelport.EntrantActionCompletionWriter) (*ChannelEntrantCompletionSink, error) {
	if writer == nil {
		return nil, errors.New("channel entrant completion writer is required")
	}
	return &ChannelEntrantCompletionSink{writer: writer}, nil
}

func (sink *ChannelEntrantCompletionSink) CompleteEffect(ctx context.Context, effectRef string, envelope effectport.Envelope, attempt effectport.Attempt, result effectport.AdapterResult) error {
	if sink == nil || sink.writer == nil || (envelope.Kind != effectport.KindChannelWelcome && envelope.Kind != effectport.KindChannelEntryTag) || !effectport.ValidDigest(result.ReceiptDigest) {
		return errors.New("invalid channel entrant completion")
	}
	completion := channelport.EntrantActionCompletion{EffectRef: effectRef, State: string(result.Completion), ResultDigest: string(result.ReceiptDigest), Attempt: attempt.Number, CompletedAt: time.Now().UTC()}
	if envelope.Kind == effectport.KindChannelWelcome {
		completion.ResultReason = channelWelcomeCompletionReason(result)
		code, err := channelWelcomeProviderErrorCode(result, completion.ResultReason)
		if err != nil {
			return err
		}
		completion.ProviderErrorCode = code
	}
	return sink.writer.CompleteEntrantAction(ctx, completion)
}

func channelWelcomeCompletionReason(result effectport.AdapterResult) string {
	if result.Artifact.Valid() && result.Artifact.Kind == channelWelcomeOutcomeArtifactKind {
		var artifact struct {
			Reason string `json:"reason"`
		}
		if json.Unmarshal(result.Artifact.Payload, &artifact) == nil && validChannelWelcomeResultReason(artifact.Reason) {
			return artifact.Reason
		}
	}
	switch result.Completion {
	case effectport.StateExecuted:
		return "sent"
	case effectport.StateUnknown:
		return "outcome_unknown"
	case effectport.StateRetryable:
		return "provider_unavailable"
	default:
		return "final_failed"
	}
}

func channelWelcomeProviderErrorCode(result effectport.AdapterResult, reason string) (int64, error) {
	if reason != "provider_rejected" {
		if result.FailureCode != "" {
			return 0, errors.New("unexpected channel welcome failure code")
		}
		return 0, nil
	}
	const prefix = "wecom_errcode_"
	if !strings.HasPrefix(result.FailureCode, prefix) {
		return 0, errors.New("missing channel welcome provider error code")
	}
	code, err := strconv.ParseInt(strings.TrimPrefix(result.FailureCode, prefix), 10, 64)
	if err != nil || code == 0 {
		return 0, errors.New("invalid channel welcome provider error code")
	}
	return code, nil
}

func validChannelWelcomeResultReason(reason string) bool {
	switch reason {
	case "welcome_not_configured", "deadline_missing", "deadline_expired", "grant_expired", "material_invalid", "provider_unavailable", "outcome_unknown", "sent", "final_failed", "provider_rejected", "customer_name_unavailable", "welcome_template_invalid", "welcome_message_too_long", "frozen_message_unavailable":
		return true
	default:
		return false
	}
}

var _ effectport.ProviderAdapter = (*ChannelEntrantProvider)(nil)
var _ effectport.CompletionSink = (*ChannelEntrantCompletionSink)(nil)
