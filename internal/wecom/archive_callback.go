package wecom

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/idempotency"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/webhook"
)

const archiveCallbackProvider = "wecom.message_archive"

var archiveCallbackFields = map[string]string{
	"ToUserName": "ToUserName", "FromUserName": "FromUserName", "CreateTime": "CreateTime",
	"MsgType": "MsgType", "AgentID": "AgentID", "Event": "Event",
}

// ArchiveNotifyEvent is deliberately independent from CallbackEvent.  The
// official notification has neither ExternalUserID nor UserID, so accepting
// it must never weaken the external-contact parser's required fields.
type ArchiveNotifyEvent struct {
	CorpID       string `json:"corp_id"`
	FromUserName string `json:"from_user_name"`
	CreateTime   int64  `json:"create_time"`
	AgentID      int64  `json:"agent_id"`
	Event        string `json:"event"`
}

func parseArchiveNotifyEvent(plain []byte, corpID string, expectedAgentID int64) (ArchiveNotifyEvent, error) {
	fields, err := parseSimpleXML(plain, "xml", archiveCallbackFields)
	if err != nil {
		return ArchiveNotifyEvent{}, err
	}
	if !fields["ToUserName"].present || fields["ToUserName"].value != corpID ||
		!fields["FromUserName"].present || fields["FromUserName"].value != "sys" ||
		!fields["CreateTime"].present || !fields["AgentID"].present ||
		fields["MsgType"].value != "event" || fields["Event"].value != "msgaudit_notify" {
		return ArchiveNotifyEvent{}, ErrMalformedXML
	}
	created, err := strconv.ParseInt(fields["CreateTime"].value, 10, 64)
	if err != nil || created < 1 {
		return ArchiveNotifyEvent{}, ErrMalformedXML
	}
	agent, err := strconv.ParseInt(fields["AgentID"].value, 10, 64)
	if err != nil || agent < 1 || (expectedAgentID > 0 && agent != expectedAgentID) {
		return ArchiveNotifyEvent{}, ErrMalformedXML
	}
	return ArchiveNotifyEvent{CorpID: corpID, FromUserName: "sys", CreateTime: created, AgentID: agent, Event: "msgaudit_notify"}, nil
}

// ArchiveNotifyIdempotencyKey uses only canonical notification facts. A
// notification is a wake-up signal: collisions do not lose a message because
// the actual source of truth is the committed SDK seq cursor.
func ArchiveNotifyIdempotencyKey(event ArchiveNotifyEvent) (idempotency.Key, error) {
	material := strings.Join([]string{"v1", event.CorpID, strconv.FormatInt(event.AgentID, 10), strconv.FormatInt(event.CreateTime, 10), event.Event}, "\x00")
	sum := sha256.Sum256([]byte(material))
	return idempotency.Parse("wecom:message-archive:" + hex.EncodeToString(sum[:]))
}

// ArchiveCallbackDispatcher does the trusted callback half only: strict
// protocol parse plus durable Inbox acceptance in one UoW. It never starts an
// SDK process or opens a provider connection in the HTTP handler.
type ArchiveCallbackDispatcher struct {
	Inbox           *webhook.Service
	UOW             platformport.UnitOfWork
	ExpectedAgentID int64
}

func (dispatcher ArchiveCallbackDispatcher) DispatchDecryptedEvent(ctx context.Context, input DecryptedCallbackEvent) error {
	if dispatcher.Inbox == nil || dispatcher.UOW == nil || input.CorpID == "" || input.ReceivedAt.IsZero() {
		return ErrMalformedXML
	}
	event, err := parseArchiveNotifyEvent(input.Plaintext, input.CorpID, dispatcher.ExpectedAgentID)
	if err != nil {
		return err
	}
	key, err := ArchiveNotifyIdempotencyKey(event)
	if err != nil {
		return err
	}
	// The Inbox receipt owns first-received time. Persist only canonical
	// provider facts so a legitimate callback replay at T+3s hashes to the
	// first receipt instead of weakening webhook payload-drift protection.
	payload, err := json.Marshal(struct {
		CorpID string `json:"corp_id"`
		Event  string `json:"event"`
	}{CorpID: event.CorpID, Event: event.Event})
	if err != nil {
		return err
	}
	return dispatcher.UOW.Within(ctx, func(tx context.Context) error {
		_, err := dispatcher.Inbox.Ingest(tx, webhook.Ingest{Provider: archiveCallbackProvider, IdempotencyKey: key, Payload: payload})
		return err
	})
}
