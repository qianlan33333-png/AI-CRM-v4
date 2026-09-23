// Package domain defines the private, durable facts owned by messagearchive.
package domain

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
)

var ErrInvalidMessage = errors.New("invalid archive message")

type ActorType string

const (
	ActorStaff    ActorType = "staff"
	ActorExternal ActorType = "external_customer"
	ActorRobot    ActorType = "robot"
	ActorUnknown  ActorType = "unknown"
)

type ResolutionStatus string

const (
	ResolutionFound         ResolutionStatus = "found"
	ResolutionNotFound      ResolutionStatus = "not_found"
	ResolutionConflict      ResolutionStatus = "conflict"
	ResolutionNotApplicable ResolutionStatus = "not_applicable"
)

type Participant struct {
	Role             string
	ActorType        ActorType
	ProviderValue    string
	ProviderDigest   [sha256.Size]byte
	StaffUserID      int64
	CustomerID       customerdomain.CustomerID
	IdentityID       int64
	ResolutionStatus ResolutionStatus
	ResolutionReason string
	ResolvedAt       *time.Time
}

type MediaReference struct {
	Kind    string
	FileID  string
	Digest  [sha256.Size]byte
	MD5     string
	Size    int64
	HasSize bool
}

type Message struct {
	CorpScope       string
	Seq             uint64
	MsgID           string
	Action          string
	MessageType     string
	Conversation    string
	RoomID          string
	OccurredAt      time.Time
	ContentText     string
	Normalized      json.RawMessage
	ProviderPayload json.RawMessage
	RecalledMsgID   string
	Participants    []Participant
	Media           []MediaReference
}

func (message Message) Valid() bool {
	if !strings.HasPrefix(message.CorpScope, "wecom-corp:") || len(message.CorpScope) <= len("wecom-corp:") ||
		strings.TrimSpace(message.MsgID) != message.MsgID || message.MsgID == "" ||
		strings.TrimSpace(message.MessageType) != message.MessageType || message.MessageType == "" ||
		(message.Conversation != "private" && message.Conversation != "group") || message.OccurredAt.IsZero() || !json.Valid(message.Normalized) {
		return false
	}
	for _, participant := range message.Participants {
		if !validParticipant(participant) {
			return false
		}
	}
	return true
}

func validParticipant(participant Participant) bool {
	if (participant.Role != "sender" && participant.Role != "recipient" && participant.Role != "subject") ||
		(participant.ActorType != ActorStaff && participant.ActorType != ActorExternal && participant.ActorType != ActorRobot && participant.ActorType != ActorUnknown) ||
		participant.ProviderValue == "" || strings.TrimSpace(participant.ProviderValue) != participant.ProviderValue ||
		(participant.ResolutionStatus != ResolutionFound && participant.ResolutionStatus != ResolutionNotFound && participant.ResolutionStatus != ResolutionConflict && participant.ResolutionStatus != ResolutionNotApplicable) {
		return false
	}
	if participant.ResolutionStatus == ResolutionFound && participant.CustomerID < 1 {
		return false
	}
	if participant.ResolutionStatus != ResolutionFound && participant.CustomerID != 0 {
		return false
	}
	return true
}

func DigestProviderValue(value string) [sha256.Size]byte { return sha256.Sum256([]byte(value)) }

func NewIssuePayload(raw []byte) ([]byte, [sha256.Size]byte) {
	digest := sha256.Sum256(raw)
	return append([]byte(nil), raw...), digest
}
