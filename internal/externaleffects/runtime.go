// Package externaleffects is the closed, digest-only external-effect kernel.
package externaleffects

import (
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
)

var (
	ErrInvalid           = errors.New("invalid external effect command")
	ErrNotFound          = errors.New("external effect not found")
	ErrPayloadMismatch   = port.ErrPayloadMismatch
	ErrTransition        = errors.New("external effect transition forbidden")
	ErrReconcileRequired = errors.New("external effect reconciliation required")
)

type Digest = port.Digest
type Owner = port.Owner
type Kind = port.Kind
type State = port.State
type Envelope = port.Envelope
type AcceptCommand = port.AcceptCommand
type Projection = port.Projection
type Receipt = port.Receipt
type ResultArtifact = port.ResultArtifact

const (
	OwnerOutbound               = port.OwnerOutbound
	OwnerPayment                = port.OwnerPayment
	OwnerAutomation             = port.OwnerAutomation
	OwnerAdminOps               = port.OwnerAdminOps
	KindFeishuOpsNotification   = port.KindFeishuOpsNotification
	OwnerSegment                = port.OwnerSegment
	KindAIRecommend             = port.KindAIRecommend
	KindInvitationCode          = port.KindInvitationCode
	KindOutboundMessage         = port.KindOutboundMessage
	KindAutomationMessage       = port.KindAutomationMessage
	KindOutboundMedia           = port.KindOutboundMedia
	KindWeComTagCatalog         = port.KindWeComTagCatalog
	KindWeComTagCatalogMutation = port.KindWeComTagCatalogMutation
	KindWeComContactDescription = port.KindWeComContactDescription
	KindGroupMessage            = port.KindGroupMessage
	KindChannelAsset            = port.KindChannelAsset
	KindChannelWelcome          = port.KindChannelWelcome
	KindChannelEntryTag         = port.KindChannelEntryTag
	KindCustomerTagCommand      = port.KindCustomerTagCommand
	KindCustomerOwnerHandoff    = port.KindCustomerOwnerHandoff
	KindCommerceProductPush     = port.KindCommerceProductPush
	KindChannelLink             = port.KindChannelLink
	KindSurveyCompletion        = port.KindSurveyCompletion
	KindAIAgentGenerate         = port.KindAIAgentGenerate
	KindWeChatPayPrepay         = port.KindWeChatPayPrepay
	KindWeChatPayRefund         = port.KindWeChatPayRefund
	KindWeChatShopRefund        = port.KindWeChatShopRefund
	StateAccepted               = port.StateAccepted
	StateQueued                 = port.StateQueued
	StateAttempted              = port.StateAttempted
	StateExecuted               = port.StateExecuted
	StateUnknown                = port.StateUnknown
	StateReconciled             = port.StateReconciled
	StateRetryable              = port.StateRetryable
	StateFinalFailed            = port.StateFinalFailed
	StateCancelled              = port.StateCancelled
)

func Hash(parts ...string) Digest   { return port.Hash(parts...) }
func ValidDigest(value Digest) bool { return port.ValidDigest(value) }

type ControlCommand struct {
	EffectID         string
	ReceiptKey       Digest
	EvidenceDigest   Digest
	ActorAdminUserID int64
	// Generation/Fence/LeaseExpiresAt are optional for the legacy EER HTTP
	// control surface. The Group Ops owner supplies them on its transactional
	// reconcile seam so a stale worker cannot close a newer attempt.
	Generation     int64
	Fence          int64
	LeaseExpiresAt time.Time
	// ReconciliationOutcome is required only for outbound media whose Provider
	// upload outcome was unknown. no_effect releases the blocked snapshot;
	// confirmed_effect requires a validated upload receipt artifact.
	ReconciliationOutcome  string
	ReconciliationArtifact ResultArtifact
}

func (c ControlCommand) Valid() bool {
	return strings.HasPrefix(c.EffectID, "eer_") && ValidDigest(c.ReceiptKey) && c.ActorAdminUserID > 0
}
func (c ControlCommand) Digest(op string) Digest {
	if !c.Valid() || (op == "reconcile" && !ValidDigest(c.EvidenceDigest)) {
		return ""
	}
	return Hash(op, c.EffectID, string(c.ReceiptKey), string(c.EvidenceDigest), strconv.FormatInt(c.ActorAdminUserID, 10), c.ReconciliationOutcome, string(c.ReconciliationArtifact.Digest))
}
func CanTransition(from, to State) bool {
	switch from {
	case StateAccepted:
		return to == StateQueued || to == StateCancelled
	case StateQueued:
		return to == StateAttempted || to == StateCancelled
	case StateAttempted:
		return to == StateExecuted || to == StateUnknown || to == StateRetryable || to == StateFinalFailed
	case StateRetryable:
		return to == StateQueued
	case StateUnknown:
		return to == StateReconciled
	}
	return false
}
