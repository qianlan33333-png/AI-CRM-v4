package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/messagearchive/domain"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

var ErrUnsupportedEnvelope = errors.New("unsupported archive message envelope")

// NormalizeArchiveRecord retains a complete protected payload for an unknown
// message type rather than converting it to empty text.  Text and image keep
// the old archive's minimal rendering contract; additional official types can
// be added without changing the storage and cursor transaction boundary.
func NormalizeArchiveRecord(corpScope string, record wecomport.PlainArchiveRecord) (domain.Message, error) {
	if !strings.HasPrefix(corpScope, "wecom-corp:") || record.Seq == 0 || strings.TrimSpace(record.MsgID) != record.MsgID || record.MsgID == "" || !json.Valid(record.Payload) {
		return domain.Message{}, domain.ErrInvalidMessage
	}
	var envelope struct {
		MsgID   string          `json:"msgid"`
		Action  string          `json:"action"`
		From    string          `json:"from"`
		ToList  []string        `json:"tolist"`
		RoomID  string          `json:"roomid"`
		MsgType string          `json:"msgtype"`
		MsgTime json.RawMessage `json:"msgtime"`
		Text    struct {
			Content string `json:"content"`
		} `json:"text"`
		Image struct {
			SDKFileID string          `json:"sdkfileid"`
			MD5       string          `json:"md5sum"`
			FileSize  json.RawMessage `json:"filesize"`
		} `json:"image"`
		Revoke struct {
			PreMsgID string `json:"pre_msgid"`
		} `json:"revoke"`
	}
	if err := json.Unmarshal(record.Payload, &envelope); err != nil {
		return domain.Message{}, domain.ErrInvalidMessage
	}
	if envelope.MsgID != "" && envelope.MsgID != record.MsgID {
		return domain.Message{}, domain.ErrInvalidMessage
	}
	if !validProviderText(envelope.From, 1024) || !validMessageType(envelope.MsgType) || strings.TrimSpace(envelope.RoomID) != envelope.RoomID {
		return domain.Message{}, domain.ErrInvalidMessage
	}
	for _, value := range envelope.ToList {
		if !validProviderText(value, 1024) {
			return domain.Message{}, domain.ErrInvalidMessage
		}
	}
	messageTime, err := parseMessageMilliseconds(envelope.MsgTime)
	if err != nil {
		return domain.Message{}, domain.ErrInvalidMessage
	}
	conversation := "private"
	if envelope.RoomID != "" {
		conversation = "group"
	}
	normalized := map[string]any{"msgtype": envelope.MsgType, "action": envelope.Action}
	message := domain.Message{
		CorpScope: corpScope, Seq: record.Seq, MsgID: record.MsgID, Action: envelope.Action,
		MessageType: envelope.MsgType, Conversation: conversation, RoomID: envelope.RoomID,
		OccurredAt: time.UnixMilli(messageTime).UTC(), Normalized: mustJSON(normalized),
		Participants: participantsFor(envelope.From, envelope.ToList),
	}
	switch envelope.MsgType {
	case "text":
		message.ContentText = strings.TrimSpace(envelope.Text.Content)
		message.Normalized = mustJSON(map[string]any{"msgtype": "text", "content": message.ContentText})
	case "image":
		message.Normalized = mustJSON(map[string]any{"msgtype": "image", "preview": "private_media"})
		if envelope.Image.SDKFileID != "" {
			media := domain.MediaReference{Kind: "image", FileID: envelope.Image.SDKFileID, Digest: domain.DigestProviderValue(envelope.Image.SDKFileID), MD5: envelope.Image.MD5}
			if size, present := parseOptionalInt64(envelope.Image.FileSize); present {
				if size < 0 {
					return domain.Message{}, domain.ErrInvalidMessage
				}
				media.Size, media.HasSize = size, true
			}
			message.Media = []domain.MediaReference{media}
		}
	case "revoke":
		message.RecalledMsgID = envelope.Revoke.PreMsgID
		message.Normalized = mustJSON(map[string]any{"msgtype": "revoke", "target_missing": false})
	default:
		message.ProviderPayload = append(json.RawMessage(nil), record.Payload...)
		message.Normalized = mustJSON(map[string]any{"msgtype": envelope.MsgType, "render_type": "unsupported"})
	}
	if !message.Valid() {
		return domain.Message{}, domain.ErrInvalidMessage
	}
	return message, nil
}

func participantsFor(sender string, recipients []string) []domain.Participant {
	items := make([]domain.Participant, 0, len(recipients)+1)
	items = append(items, newParticipant("sender", sender))
	seen := map[string]struct{}{sender: {}}
	for _, recipient := range recipients {
		if _, exists := seen[recipient]; exists {
			continue
		}
		seen[recipient] = struct{}{}
		items = append(items, newParticipant("recipient", recipient))
	}
	return items
}

func newParticipant(role, value string) domain.Participant {
	actor := domain.ActorStaff
	status, reason := domain.ResolutionNotApplicable, ""
	// The official archive payload's robot identifiers are wb-prefixed.  The
	// external-contact form is wm-prefixed; values outside those documented
	// forms are treated as staff first and never guessed as customers.
	if strings.HasPrefix(value, "wb") {
		actor = domain.ActorRobot
	} else if strings.HasPrefix(value, "wm") || strings.HasPrefix(value, "wo") {
		// Frozen v1's SDK boundary recognizes both wm and wo forms as archive
		// external IDs. They still resolve only when this exact decrypted record
		// carries a WeCom-provider fact; the prefix is never verification.
		actor, status, reason = domain.ActorExternal, domain.ResolutionNotFound, "provider_actor_unknown"
	}
	return domain.Participant{Role: role, ActorType: actor, ProviderValue: value, ProviderDigest: domain.DigestProviderValue(value), ResolutionStatus: status, ResolutionReason: reason}
}

func validProviderText(value string, maximum int) bool {
	return value != "" && len(value) <= maximum && strings.TrimSpace(value) == value
}
func validMessageType(value string) bool {
	if !validProviderText(value, 120) {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_') {
			return false
		}
	}
	return true
}
func parseMessageMilliseconds(raw json.RawMessage) (int64, error) {
	if len(raw) == 0 {
		return 0, errors.New("missing msgtime")
	}
	var number json.Number
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if err := decoder.Decode(&number); err != nil {
		return 0, err
	}
	value, err := strconv.ParseInt(string(number), 10, 64)
	if err != nil || value < 1 {
		return 0, fmt.Errorf("invalid msgtime")
	}
	if value < 100000000000 {
		value *= 1000
	}
	return value, nil
}
func parseOptionalInt64(raw json.RawMessage) (int64, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, false
	}
	var number json.Number
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if decoder.Decode(&number) != nil {
		return 0, false
	}
	value, err := strconv.ParseInt(string(number), 10, 64)
	if err != nil {
		return 0, false
	}
	return value, true
}
func mustJSON(value any) json.RawMessage { raw, _ := json.Marshal(value); return raw }
