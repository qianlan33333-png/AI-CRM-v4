package wecom

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	channeldomain "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/domain"
	channelport "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/idempotency"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/webhook"
)

const (
	maxCallbackBody           = 1 << 20
	callbackProtocolVersion   = "v1"
	callbackProvider          = "wecom.external_contact"
	callbackIdempotencyPrefix = "wecom:external-contact:"
)

// CallbackProtocolVersion is included in callback idempotency material so a
// future parser/normalization change cannot silently reuse old receipts.
const CallbackProtocolVersion = callbackProtocolVersion

// CallbackEvent is the privacy-safe durable representation of an
// authenticated external-contact callback. It deliberately contains no raw
// State, WelcomeCode, Source, or FailReason values.
type CallbackEvent struct {
	CorpID         string `json:"corp_id"`
	ToUserName     string `xml:"ToUserName" json:"to_user_name"`
	MsgType        string `xml:"MsgType" json:"msg_type"`
	Event          string `xml:"Event" json:"event"`
	ChangeType     string `xml:"ChangeType" json:"change_type"`
	ExternalUserID string `xml:"ExternalUserID" json:"external_userid"`
	UserID         string `xml:"UserID" json:"userid"`

	StatePresent bool   `json:"state_present"`
	StateDigest  string `json:"state_digest,omitempty"`

	CreateTime   int64  `xml:"CreateTime" json:"create_time"`
	MsgID        string `xml:"MsgId" json:"msg_id,omitempty"`
	MsgIDPresent bool   `json:"msg_id_present"`

	WelcomeCodePresent bool   `json:"welcome_code_present"`
	WelcomeCodeDigest  string `json:"welcome_code_digest,omitempty"`
	WelcomeGrantRef    string `json:"welcome_grant_ref,omitempty"`
	SourcePresent      bool   `json:"source_present"`
	SourceDigest       string `json:"source_digest,omitempty"`
	FailReasonPresent  bool   `json:"fail_reason_present"`
	FailReasonDigest   string `json:"fail_reason_digest,omitempty"`
}

func (event CallbackEvent) supported() bool {
	if event.Event != "change_external_contact" {
		return false
	}
	switch event.ChangeType {
	case ChangeAddExternalContact, ChangeAddHalfExternalContact, ChangeEditExternalContact, ChangeDelFollowUser, ChangeDelExternalContact:
		return true
	default:
		return false
	}
}

// IsEntrant reports whether the callback can begin the new-customer flow.
func (event CallbackEvent) IsEntrant() bool {
	return event.ChangeType == "add_external_contact" || event.ChangeType == "add_half_external_contact"
}

// EventType is stable for local routing and does not contain a provider secret.
func (event CallbackEvent) EventType() string {
	return event.Event + ":" + event.ChangeType
}

type parsedXMLField struct {
	value   string
	present bool
}

// parseSimpleXML accepts one root element with strict known-field handling.
// Unknown, well-formed provider extensions are skipped without retention so
// a legal provider rollout does not cause endless retries. Duplicate known
// fields and trailing XML remain rejected at the signed callback boundary.
func parseSimpleXML(value []byte, root string, allowed map[string]string) (map[string]parsedXMLField, error) {
	if len(value) == 0 || len(value) > maxCallbackBody || !utf8.Valid(value) {
		return nil, ErrMalformedXML
	}
	decoder := xml.NewDecoder(bytes.NewReader(value))
	decoder.Strict = true

	var rootStart xml.StartElement
	for {
		token, err := decoder.Token()
		if err != nil {
			return nil, ErrMalformedXML
		}
		switch current := token.(type) {
		case xml.StartElement:
			rootStart = current
			if rootStart.Name.Space != "" || rootStart.Name.Local != root || len(rootStart.Attr) != 0 {
				return nil, ErrMalformedXML
			}
			goto rootFound
		case xml.ProcInst:
			// An XML declaration is allowed before the document element.
		case xml.Comment:
			// XML comments before the document element are harmless.
		case xml.Directive:
			return nil, ErrMalformedXML
		case xml.CharData:
			if strings.TrimSpace(string(current)) != "" {
				return nil, ErrMalformedXML
			}
		default:
			return nil, ErrMalformedXML
		}
	}

rootFound:
	fields := make(map[string]parsedXMLField, len(allowed))
	for {
		token, err := decoder.Token()
		if err != nil {
			return nil, ErrMalformedXML
		}
		switch current := token.(type) {
		case xml.CharData:
			if strings.TrimSpace(string(current)) != "" {
				return nil, ErrMalformedXML
			}
		case xml.StartElement:
			canonical, ok := allowed[current.Name.Local]
			if !ok {
				// WeCom has added scalar fields to callback payloads over time.
				// Ignore an unknown, well-formed extension so a provider rollout
				// does not cause endless retries. Known fields still use the strict
				// duplicate/canonicalization checks below, and Skip validates the
				// complete unknown subtree without retaining its contents.
				if err := decoder.Skip(); err != nil {
					return nil, ErrMalformedXML
				}
				continue
			}
			if current.Name.Space != "" || len(current.Attr) != 0 {
				return nil, ErrMalformedXML
			}
			if canonical == "" || fields[canonical].present {
				return nil, ErrMalformedXML
			}
			field, err := decodeScalarElement(decoder, current)
			if err != nil {
				return nil, ErrMalformedXML
			}
			fields[canonical] = parsedXMLField{value: field, present: true}
		case xml.EndElement:
			if current.Name.Space != rootStart.Name.Space || current.Name.Local != rootStart.Name.Local {
				return nil, ErrMalformedXML
			}
			goto rootClosed
		case xml.Comment, xml.ProcInst:
			// Comments and processing instructions are valid inside the root.
		case xml.Directive:
			return nil, ErrMalformedXML
		default:
			return nil, ErrMalformedXML
		}
	}

rootClosed:
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return fields, nil
		}
		if err != nil {
			return nil, ErrMalformedXML
		}
		// Whitespace after the root is the only permitted trailing content.
		if text, ok := token.(xml.CharData); ok && strings.TrimSpace(string(text)) == "" {
			continue
		}
		return nil, ErrMalformedXML
	}
}

func decodeScalarElement(decoder *xml.Decoder, start xml.StartElement) (string, error) {
	var value strings.Builder
	for {
		token, err := decoder.Token()
		if err != nil {
			return "", err
		}
		switch current := token.(type) {
		case xml.CharData:
			value.Write([]byte(current))
		case xml.EndElement:
			if current.Name.Space != start.Name.Space || current.Name.Local != start.Name.Local {
				return "", ErrMalformedXML
			}
			return value.String(), nil
		case xml.StartElement, xml.Comment, xml.Directive, xml.ProcInst:
			return "", ErrMalformedXML
		default:
			return "", ErrMalformedXML
		}
	}
}

var encryptedEnvelopeFields = map[string]string{
	"ToUserName": "ToUserName",
	"AgentID":    "AgentID",
	"Encrypt":    "Encrypt",
}

// parseEncryptedEnvelope parses the outer POST XML. The optional corpID
// argument preserves the old one-argument helper for package-local callers;
// the HTTP handler always supplies it so the outer receiveid is authenticated.
func parseEncryptedEnvelope(value []byte, corpIDs ...string) (string, error) {
	fields, err := parseSimpleXML(value, "xml", encryptedEnvelopeFields)
	if err != nil {
		return "", err
	}
	toUserName := fields["ToUserName"]
	encrypted := fields["Encrypt"]
	if !toUserName.present || toUserName.value == "" || !encrypted.present || encrypted.value == "" || strings.TrimSpace(encrypted.value) != encrypted.value {
		return "", ErrMalformedXML
	}
	if len(corpIDs) > 0 && toUserName.value != corpIDs[0] {
		return "", ErrCorpMismatch
	}
	return encrypted.value, nil
}

var callbackEventFields = map[string]string{
	"ToUserName":     "ToUserName",
	"FromUserName":   "FromUserName",
	"CreateTime":     "CreateTime",
	"MsgType":        "MsgType",
	"Event":          "Event",
	"ChangeType":     "ChangeType",
	"ExternalUserID": "ExternalUserID",
	"ExternalUserId": "ExternalUserID",
	"UserID":         "UserID",
	"State":          "State",
	"WelcomeCode":    "WelcomeCode",
	"Source":         "Source",
	"FailReason":     "FailReason",
	"MsgId":          "MsgID",
	"MsgID":          "MsgID",
}

// callbackEventDraft is request-local. State is retained only until the
// injected StateDigester has converted it into a fixed-size digest; this type
// must never be marshalled or passed to a durable store.
type callbackEventDraft struct {
	event          CallbackEvent
	state          string
	statePresent   bool
	welcome        string
	welcomePresent bool
}

// parseCallbackEvent parses only authenticated/decrypted XML and never keeps
// raw sensitive values in the returned durable event. A callback containing
// State requires the request handler's injected digester; callers that need to
// parse such an event should use parseCallbackEventWithStateDigester.
func parseCallbackEvent(value []byte, corpIDs ...string) (CallbackEvent, error) {
	draft, err := parseCallbackEventDraft(value, corpIDs...)
	if err != nil {
		return CallbackEvent{}, err
	}
	return materializeCallbackEvent(&draft, nil)
}

// parseCallbackEventWithStateDigester is the package-level typed parser used
// by tests and adapters that already hold a trusted StateDigester.
func parseCallbackEventWithStateDigester(value []byte, digester StateDigester, corpIDs ...string) (CallbackEvent, error) {
	draft, err := parseCallbackEventDraft(value, corpIDs...)
	if err != nil {
		return CallbackEvent{}, err
	}
	return materializeCallbackEvent(&draft, digester)
}

func parseCallbackEventDraft(value []byte, corpIDs ...string) (callbackEventDraft, error) {
	fields, err := parseSimpleXML(value, "xml", callbackEventFields)
	if err != nil {
		return callbackEventDraft{}, err
	}
	get := func(name string) string { return fields[name].value }
	toUserName := fields["ToUserName"]
	if !toUserName.present || !validText(toUserName.value, 256) {
		return callbackEventDraft{}, ErrMalformedXML
	}
	if len(corpIDs) > 0 && toUserName.value != corpIDs[0] {
		return callbackEventDraft{}, ErrCorpMismatch
	}
	if get("MsgType") != "event" || get("Event") != "change_external_contact" || !validCallbackLabel(get("ChangeType")) {
		return callbackEventDraft{}, ErrMalformedXML
	}
	createTime := fields["CreateTime"]
	if !createTime.present || !validText(createTime.value, 32) {
		return callbackEventDraft{}, ErrMalformedXML
	}
	seconds, err := strconv.ParseInt(createTime.value, 10, 64)
	if err != nil || seconds <= 0 {
		return callbackEventDraft{}, ErrMalformedXML
	}
	externalUserID := fields["ExternalUserID"]
	if !externalUserID.present || !validText(externalUserID.value, 1024) {
		return callbackEventDraft{}, ErrMalformedXML
	}
	userID := fields["UserID"]
	if userID.present && userID.value != "" && !validText(userID.value, 1024) {
		return callbackEventDraft{}, ErrMalformedXML
	}
	if callbackChangeRequiresUserID(get("ChangeType")) && (!userID.present || !validText(userID.value, 1024)) {
		return callbackEventDraft{}, ErrMalformedXML
	}

	event := CallbackEvent{
		CorpID:         toUserName.value,
		ToUserName:     toUserName.value,
		MsgType:        get("MsgType"),
		Event:          get("Event"),
		ChangeType:     get("ChangeType"),
		ExternalUserID: externalUserID.value,
		UserID:         userID.value,
		CreateTime:     seconds,
	}
	if msgID := fields["MsgID"]; msgID.present {
		if msgID.value != "" && !validText(msgID.value, 256) {
			return callbackEventDraft{}, ErrMalformedXML
		}
		event.MsgIDPresent = true
		event.MsgID = msgID.value
	}
	draft := callbackEventDraft{event: event}
	if state := fields["State"]; state.present {
		if !validOptionalCallbackValue(state.value) {
			return callbackEventDraft{}, ErrMalformedXML
		}
		draft.statePresent = true
		draft.state = state.value
	}
	if welcome := fields["WelcomeCode"]; welcome.present {
		if !validSecretCallbackValue(welcome.value) {
			return callbackEventDraft{}, ErrMalformedXML
		}
		event.WelcomeCodePresent = true
		event.WelcomeCodeDigest = callbackValueDigest(welcome.value)
		draft.welcomePresent = true
		draft.welcome = welcome.value
	}
	if source := fields["Source"]; source.present {
		if !validSecretCallbackValue(source.value) {
			return callbackEventDraft{}, ErrMalformedXML
		}
		event.SourcePresent = true
		event.SourceDigest = callbackValueDigest(source.value)
	}
	if reason := fields["FailReason"]; reason.present {
		if !validSecretCallbackValue(reason.value) {
			return callbackEventDraft{}, ErrMalformedXML
		}
		event.FailReasonPresent = true
		event.FailReasonDigest = callbackValueDigest(reason.value)
	}
	draft.event = event
	return draft, nil
}

func callbackChangeRequiresUserID(changeType string) bool {
	switch changeType {
	case ChangeAddExternalContact, ChangeAddHalfExternalContact, ChangeEditExternalContact, ChangeDelFollowUser, ChangeDelExternalContact:
		return true
	default:
		return false
	}
}

func materializeCallbackEvent(draft *callbackEventDraft, digester StateDigester) (CallbackEvent, error) {
	if draft == nil {
		return CallbackEvent{}, ErrMalformedXML
	}
	defer func() {
		draft.state = ""
		draft.statePresent = false
	}()
	event := draft.event
	if !draft.statePresent {
		return event, nil
	}
	if digester == nil {
		return CallbackEvent{}, ErrStateDigestUnavailable
	}
	digest, err := digester.DigestState(event.ToUserName, draft.state)
	// The raw State is request-local and should not survive this conversion,
	// including on adapter failure. The decrypted XML itself remains owned by
	// the request handler and is never marshalled into the Inbox payload.
	if err != nil {
		return CallbackEvent{}, err
	}
	if digest == ([32]byte{}) {
		return CallbackEvent{}, ErrInvalidStateDigester
	}
	event.StatePresent = true
	event.StateDigest = formatStateDigest(digest)
	return event, nil
}

func validCorpID(value string) bool {
	if len(value) == 0 || len(value) > 128 || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if !(character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '_' || character == '-') {
			return false
		}
	}
	return true
}

func validToken(value string) bool {
	return len(value) > 0 && len(value) <= 256 && strings.TrimSpace(value) == value
}

func validText(value string, maximum int) bool {
	return value != "" && len(value) <= maximum && strings.TrimSpace(value) == value && utf8.ValidString(value)
}

func validCallbackLabel(value string) bool {
	if !validText(value, 128) {
		return false
	}
	for _, character := range value {
		if !(character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '_') {
			return false
		}
	}
	return true
}

func validOptionalCallbackValue(value string) bool {
	return value == "" || validText(value, 1024)
}

func validSecretCallbackValue(value string) bool {
	return len(value) <= 4096 && utf8.ValidString(value) && strings.TrimSpace(value) == value
}

func callbackValueDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(digest[:])
}

// CallbackIdempotencyDigest binds the complete authenticated plaintext to the
// configured enterprise and parser protocol version. Framing separators make
// the three components unambiguous while retaining the exact plaintext bytes.
func CallbackIdempotencyDigest(corpID string, plaintext []byte) string {
	material := strings.Join([]string{callbackProtocolVersion, corpID, string(plaintext)}, "\x00")
	digest := sha256.Sum256([]byte(material))
	return "sha256:" + hex.EncodeToString(digest[:])
}

func stableCallbackKey(corpID string, plaintext []byte) string {
	return CallbackIdempotencyDigest(corpID, plaintext)[len("sha256:"):]
}

type CallbackHandler struct {
	Enabled        bool
	Crypto         *CallbackCrypto
	StateDigester  StateDigester
	Inbox          *webhook.Service
	UOW            port.UnitOfWork
	WelcomeGrants  WelcomeGrantStore
	WelcomeActions channelport.CallbackWelcomeAccepter
	States         channelport.StateResolver
	Dispatcher     DecryptedEventDispatcher
	Now            func() time.Time
}

// DecryptedCallbackEvent is restricted to the authenticated WeCom boundary.
// Plaintext is request-scoped, is never logged or persisted directly, and is
// exposed only so independently owned Event handlers can parse their own
// strict protocol after the common verify/decrypt/Corp checks have passed.
type DecryptedCallbackEvent struct {
	CorpID      string
	CallbackKey idempotency.Key
	Plaintext   []byte
	ReceivedAt  time.Time
}

type DecryptedEventDispatcher interface {
	DispatchDecryptedEvent(context.Context, DecryptedCallbackEvent) error
}

// CallbackEventDispatcher is the narrow post-decryption Event router. New
// callback consumers register their strict handler here instead of changing
// the external-contact parser or weakening its required fields.
type CallbackEventDispatcher struct {
	ExternalContact DecryptedEventDispatcher
	Archive         DecryptedEventDispatcher
}

func (dispatcher CallbackEventDispatcher) DispatchDecryptedEvent(ctx context.Context, input DecryptedCallbackEvent) error {
	fields, err := parseSimpleXML(input.Plaintext, "xml", map[string]string{"Event": "Event"})
	if err != nil || !fields["Event"].present || !validCallbackLabel(fields["Event"].value) {
		return ErrMalformedXML
	}
	switch fields["Event"].value {
	case "change_external_contact":
		if dispatcher.ExternalContact == nil {
			return ErrMalformedXML
		}
		return dispatcher.ExternalContact.DispatchDecryptedEvent(ctx, input)
	case "msgaudit_notify":
		if dispatcher.Archive == nil {
			return ErrMalformedXML
		}
		return dispatcher.Archive.DispatchDecryptedEvent(ctx, input)
	default:
		return ErrMalformedXML
	}
}

// ExternalContactCallbackDispatcher keeps the established external-contact
// protocol strict while accepting the time-critical welcome intent in the
// same UoW as its Inbox row and encrypted grant.
type ExternalContactCallbackDispatcher struct {
	StateDigester  StateDigester
	Inbox          *webhook.Service
	UOW            port.UnitOfWork
	WelcomeGrants  WelcomeGrantStore
	WelcomeActions channelport.CallbackWelcomeAccepter
	States         channelport.StateResolver
}

func (dispatcher ExternalContactCallbackDispatcher) DispatchDecryptedEvent(ctx context.Context, input DecryptedCallbackEvent) error {
	if dispatcher.Inbox == nil || dispatcher.UOW == nil || input.CorpID == "" || input.CallbackKey == "" || input.ReceivedAt.IsZero() {
		return ErrMalformedXML
	}
	draft, err := parseCallbackEventDraft(input.Plaintext, input.CorpID)
	if err != nil {
		return err
	}
	defer func() { draft.welcome = ""; draft.welcomePresent = false }()
	event, err := materializeCallbackEvent(&draft, dispatcher.StateDigester)
	if err != nil {
		return err
	}
	return dispatcher.UOW.Within(ctx, func(txContext context.Context) error {
		if draft.welcomePresent && draft.welcome != "" {
			if dispatcher.WelcomeGrants == nil || dispatcher.WelcomeActions == nil {
				return ErrWelcomeGrantUnavailable
			}
			grant, sealErr := dispatcher.WelcomeGrants.Seal(txContext, string(input.CallbackKey), draft.welcome, input.ReceivedAt.UTC().Add(10*time.Minute))
			if sealErr != nil {
				return sealErr
			}
			event.WelcomeGrantRef = grant
			resolution := channeldomain.StateResolution{Status: channeldomain.StateUnmatched}
			if event.StatePresent {
				if dispatcher.States == nil {
					return ErrWelcomeGrantUnavailable
				}
				digest, digestErr := ParseStateDigest(event.StateDigest)
				if digestErr != nil {
					return digestErr
				}
				resolution, digestErr = dispatcher.States.ResolveStateDigest(txContext, input.CorpID, digest, time.Unix(event.CreateTime, 0).UTC())
				if digestErr != nil || !resolution.Valid() {
					if digestErr != nil {
						return digestErr
					}
					return ErrWelcomeGrantUnavailable
				}
			}
			if acceptErr := dispatcher.WelcomeActions.AcceptCallbackWelcome(txContext, channelport.CallbackWelcomeCommand{
				CallbackID: string(input.CallbackKey), CorpID: input.CorpID, Resolution: resolution, WelcomeGrantRef: grant,
				OccurredAt: time.Unix(event.CreateTime, 0).UTC(), FirstReceivedAt: input.ReceivedAt.UTC(), SendDeadlineAt: input.ReceivedAt.UTC().Add(20 * time.Second),
			}); acceptErr != nil {
				return acceptErr
			}
		}
		payload, marshalErr := json.Marshal(event)
		if marshalErr != nil {
			return marshalErr
		}
		_, ingestErr := dispatcher.Inbox.Ingest(txContext, webhook.Ingest{Provider: callbackProvider, IdempotencyKey: input.CallbackKey, Payload: payload})
		return ingestErr
	})
}

func (handler CallbackHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	// Establish the business window at HTTP arrival, before decrypting or
	// parsing. Only a verified callback can commit it, but signature work must
	// not silently extend the time available to call the welcome endpoint.
	receivedAt := handler.now()
	if !handler.Enabled {
		writeWeComError(writer, http.StatusServiceUnavailable, "provider_unavailable")
		return
	}
	if handler.Crypto == nil || handler.Inbox == nil || handler.UOW == nil || !validCorpID(handler.Crypto.corpID) {
		writeWeComError(writer, http.StatusServiceUnavailable, "provider_unavailable")
		return
	}
	if request == nil {
		writeCallbackFailure(writer, ErrMalformedXML)
		return
	}
	switch request.Method {
	case http.MethodGet:
		signature, timestamp, nonce, encrypted, ok := callbackQuery(request)
		if !ok {
			writeCallbackFailure(writer, ErrMalformedXML)
			return
		}
		plain, err := handler.Crypto.VerifyAndDecrypt(signature, timestamp, nonce, encrypted)
		if err != nil {
			writeCallbackFailure(writer, err)
			return
		}
		writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = writer.Write(plain)
	case http.MethodPost:
		signature, timestamp, nonce, encrypted, ok := callbackQuery(request)
		if !ok {
			writeCallbackFailure(writer, ErrMalformedXML)
			return
		}
		if request.Body == nil {
			writeCallbackFailure(writer, ErrMalformedXML)
			return
		}
		request.Body = http.MaxBytesReader(writer, request.Body, maxCallbackBody)
		body, err := io.ReadAll(request.Body)
		if err != nil {
			writeWeComError(writer, http.StatusRequestEntityTooLarge, "invalid_request")
			return
		}
		encrypted, err = parseEncryptedEnvelope(body, handler.Crypto.corpID)
		if err != nil {
			writeCallbackFailure(writer, err)
			return
		}
		plain, err := handler.Crypto.VerifyAndDecrypt(signature, timestamp, nonce, encrypted)
		if err != nil {
			writeCallbackFailure(writer, err)
			return
		}
		key, err := idempotency.Parse(callbackIdempotencyPrefix + stableCallbackKey(handler.Crypto.corpID, plain))
		if err != nil {
			writeWeComError(writer, http.StatusBadRequest, "invalid_request")
			return
		}
		dispatcher := handler.Dispatcher
		if dispatcher == nil {
			dispatcher = CallbackEventDispatcher{ExternalContact: ExternalContactCallbackDispatcher{
				StateDigester: handler.StateDigester, Inbox: handler.Inbox, UOW: handler.UOW,
				WelcomeGrants: handler.WelcomeGrants, WelcomeActions: handler.WelcomeActions, States: handler.States,
			}}
		}
		err = dispatcher.DispatchDecryptedEvent(request.Context(), DecryptedCallbackEvent{CorpID: handler.Crypto.corpID, CallbackKey: key, Plaintext: plain, ReceivedAt: receivedAt})
		if err != nil {
			if errors.Is(err, ErrMalformedXML) || errors.Is(err, ErrCorpMismatch) {
				writeCallbackFailure(writer, err)
				return
			}
			// Missing local dependencies or a failed UoW must never be
			// acknowledged. The provider can replay the exact callback, which
			// preserves its already-derived idempotency key and deadline once
			// acceptance succeeds.
			writeWeComError(writer, http.StatusServiceUnavailable, "provider_unavailable")
			return
		}
		reply, err := handler.Crypto.EncryptedSuccessReply()
		if err != nil {
			writeWeComError(writer, http.StatusServiceUnavailable, "provider_unavailable")
			return
		}
		writer.Header().Set("Content-Type", "application/xml; charset=utf-8")
		_, _ = writer.Write(reply)
	default:
		writer.Header().Set("Allow", "GET, POST")
		writeWeComError(writer, http.StatusMethodNotAllowed, "invalid_request")
	}
}

func (handler CallbackHandler) now() time.Time {
	if handler.Now != nil {
		return handler.Now().UTC()
	}
	return time.Now().UTC()
}

func callbackQuery(request *http.Request) (signature, timestamp, nonce, encrypted string, ok bool) {
	if request == nil || request.URL == nil || len(request.URL.RawQuery) > maxCallbackBody+1024 {
		return "", "", "", "", false
	}
	query, err := url.ParseQuery(request.URL.RawQuery)
	if err != nil {
		return "", "", "", "", false
	}
	allowed := map[string]bool{"msg_signature": true, "timestamp": true, "nonce": true}
	if request.Method == http.MethodGet {
		allowed["echostr"] = true
	}
	for key, values := range query {
		if !allowed[key] || len(values) != 1 || values[0] == "" {
			return "", "", "", "", false
		}
	}
	signature, signatureOK := oneQuery(query, "msg_signature", 40)
	timestamp, timestampOK := oneQuery(query, "timestamp", 128)
	nonce, nonceOK := oneQuery(query, "nonce", 128)
	if !signatureOK || !timestampOK || !nonceOK {
		return "", "", "", "", false
	}
	if request.Method == http.MethodGet {
		encrypted, ok = oneQuery(query, "echostr", maxCallbackBody)
		return signature, timestamp, nonce, encrypted, ok
	}
	return signature, timestamp, nonce, "", true
}

func oneQuery(query url.Values, key string, maximum int) (string, bool) {
	values, ok := query[key]
	if !ok || len(values) != 1 || values[0] == "" || len(values[0]) > maximum {
		return "", false
	}
	return values[0], true
}

func writeCallbackFailure(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrSignature):
		writeWeComError(writer, http.StatusForbidden, "wecom_signature_invalid")
	case errors.Is(err, ErrCallbackExpired):
		writeWeComError(writer, http.StatusForbidden, "wecom_callback_expired")
	case errors.Is(err, ErrCorpMismatch):
		writeWeComError(writer, http.StatusForbidden, "wecom_corp_mismatch")
	default:
		writeWeComError(writer, http.StatusBadRequest, "invalid_request")
	}
}

func writeWeComError(writer http.ResponseWriter, status int, code string) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(map[string]string{"code": code})
}

// stableEventKey remains for the pre-existing processor's audit key. New
// callback ingress uses stableCallbackKey, which includes the full plaintext.
func stableEventKey(event CallbackEvent) string {
	value := strings.Join([]string{event.ToUserName, event.Event, event.ChangeType, event.ExternalUserID, event.UserID, event.MsgID, time.Unix(event.CreateTime, 0).UTC().Format(time.RFC3339)}, "\x00")
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
