// Package port is the only cross-domain contract for AI Assistant review plans.
package port

import (
	"context"
	"errors"
	"strings"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
)

const (
	MaxRecipients        = 5000
	MaxMessagesPerTarget = 20
)

var ErrInvalidCommand = errors.New("invalid AI Assistant command")

type PlanID int64
type RecipientID int64
type ContentVersionID int64

type ActorKind string

const (
	ActorAdmin   ActorKind = "admin"
	ActorService ActorKind = "service"
)

type Actor struct {
	Kind ActorKind
	ID   int64
}

func (a Actor) Valid() bool {
	return a.ID > 0 && (a.Kind == ActorAdmin || a.Kind == ActorService)
}

type ContentKind string

const (
	ContentText        ContentKind = "text"
	ContentImage       ContentKind = "image"
	ContentMiniProgram ContentKind = "mini_program"
	ContentLink        ContentKind = "link"
	ContentAttachment  ContentKind = "attachment"
)

type ExcelCard struct {
	AppID       string            `json:"appid"`
	Path        string            `json:"path"`
	Title       string            `json:"title"`
	CoverDigest effectport.Digest `json:"cover_digest"`
	// CoverImageID is an opaque, stable Media image reference. Zero preserves
	// pre-0126 Python-backed covers; new Media-backed covers require it.
	CoverImageID int64 `json:"cover_image_id,omitempty"`
}

func (c ExcelCard) Valid() bool {
	return validLegacyReferencePart(c.AppID, 128) && validLegacyReferencePart(c.Path, 1024) && strings.HasPrefix(c.Path, "pages/") && !strings.Contains(c.Path, "://") && !strings.HasPrefix(c.Path, "//") && len(c.Title) <= 512 && c.CoverImageID >= 0 && (c.CoverDigest == "" || effectport.ValidDigest(c.CoverDigest)) && (c.CoverImageID == 0 || effectport.ValidDigest(c.CoverDigest))
}

type DeferredTarget struct {
	UnionID      string `json:"unionid"`
	Scope        string `json:"scope"`
	SenderUserID string `json:"sender_userid"`
}

func (t DeferredTarget) Valid() bool {
	return validLegacyReferencePart(t.UnionID, 256) && strings.HasPrefix(t.Scope, "wechat-open-platform:") && len(t.Scope) > len("wechat-open-platform:") && validLegacyReferencePart(t.Scope, 256) && validLegacyReferencePart(t.SenderUserID, 256)
}

type ContentBlock struct {
	ExcelCard      *ExcelCard        `json:"excel_card,omitempty"`
	Kind           ContentKind       `json:"kind"`
	Text           string            `json:"text,omitempty"`
	MaterialKind   string            `json:"material_kind,omitempty"`
	MaterialID     int64             `json:"material_id,omitempty"`
	MaterialDigest effectport.Digest `json:"material_digest,omitempty"`
	// LegacySourceSystem and LegacyMaterialID are accepted only at the
	// authenticated compatibility edge. Media resolves them under the plan UoW
	// and they must be empty before a content version is frozen.
	LegacySourceSystem string `json:"legacy_source_system,omitempty"`
	LegacyMaterialID   string `json:"legacy_material_id,omitempty"`
}

func (b ContentBlock) Valid() bool {
	if b.ExcelCard != nil {
		return b.Kind == ContentMiniProgram && b.ExcelCard.Valid() && b.MaterialID == 0 && b.MaterialKind == "" && b.MaterialDigest == "" && b.Text == "" && b.LegacyMaterialID == "" && b.LegacySourceSystem == ""
	}
	switch b.Kind {
	case ContentText:
		return strings.TrimSpace(b.Text) != "" && len(b.Text) <= 8000 && b.MaterialKind == "" && b.MaterialID == 0 && b.MaterialDigest == "" && b.LegacySourceSystem == "" && b.LegacyMaterialID == ""
	case ContentImage, ContentMiniProgram, ContentAttachment, ContentLink:
		return b.MaterialID > 0 && b.MaterialKind == materialKindForContent(b.Kind) && effectport.ValidDigest(b.MaterialDigest) && len(b.Text) <= 2000 && b.LegacySourceSystem == "" && b.LegacyMaterialID == ""
	default:
		return false
	}
}

// ValidInput accepts a missing material digest at the HTTP edge. The Media
// owner resolves and freezes the authoritative digest inside the plan UoW.
func (b ContentBlock) ValidInput() bool {
	if b.ExcelCard != nil {
		return b.Valid()
	}
	if b.Kind == ContentText {
		return b.Valid()
	}
	if b.LegacySourceSystem != "" || b.LegacyMaterialID != "" {
		return b.MaterialID == 0 && b.MaterialDigest == "" && b.MaterialKind == materialKindForContent(b.Kind) && validLegacyReferencePart(b.LegacySourceSystem, 80) && validLegacyReferencePart(b.LegacyMaterialID, 128) && len(b.Text) <= 2000
	}
	provided := b.MaterialDigest
	b.MaterialDigest = effectport.Hash("aiassistant.input-placeholder")
	return (provided == "" || effectport.ValidDigest(provided)) && b.Valid()
}

func materialKindForContent(kind ContentKind) string {
	switch kind {
	case ContentImage:
		return "image"
	case ContentMiniProgram:
		return "miniprogram"
	case ContentAttachment:
		return "attachment"
	case ContentLink:
		return "group_invite"
	default:
		return ""
	}
}

func validLegacyReferencePart(value string, maximum int) bool {
	return value != "" && len(value) <= maximum && strings.TrimSpace(value) == value && !strings.ContainsAny(value, "\r\n\t\x00")
}

type RecipientCandidate struct {
	DeferredTarget *DeferredTarget           `json:"deferred_target,omitempty"`
	CustomerID     customerdomain.CustomerID `json:"customer_id"`
	StaffID        int64                     `json:"staff_id"`
	Content        []ContentBlock            `json:"content"`
}

func (r RecipientCandidate) Valid() bool {
	targetValid := r.CustomerID > 0 && r.StaffID > 0 && r.DeferredTarget == nil
	if r.DeferredTarget != nil {
		targetValid = r.CustomerID == 0 && r.StaffID == 0 && r.DeferredTarget.Valid()
	}
	if !targetValid || len(r.Content) == 0 || len(r.Content) > MaxMessagesPerTarget {
		return false
	}
	for _, block := range r.Content {
		if !block.ValidInput() {
			return false
		}
	}
	return true
}

type CreatePlanCommand struct {
	Actor          Actor
	IdempotencyKey string
	Name           string
	SourceKind     string
	SourceDigest   effectport.Digest
	Recipients     []RecipientCandidate
	OccurredAt     time.Time
}

func (c CreatePlanCommand) Valid() bool {
	if !c.Actor.Valid() || len(c.IdempotencyKey) < 8 || len(c.IdempotencyKey) > 200 || strings.TrimSpace(c.Name) == "" || len(c.Name) > 200 || strings.TrimSpace(c.SourceKind) == "" || !effectport.ValidDigest(c.SourceDigest) || len(c.Recipients) == 0 || len(c.Recipients) > MaxRecipients {
		return false
	}
	for _, recipient := range c.Recipients {
		if !recipient.Valid() {
			return false
		}
	}
	return !c.OccurredAt.IsZero()
}

type CreatePlanResult struct {
	Plan     Plan
	Replayed bool
}

type Intake interface {
	CreatePlan(context.Context, CreatePlanCommand) (CreatePlanResult, error)
}

// TransactionalIntake creates a review plan in the caller's already-bound
// PostgreSQL transaction. It exists for the small set of Owners that must
// commit their own durable fact and an AI review plan atomically.
//
// Calling it without a transaction is an explicit failure; it never opens a
// nested Unit of Work.
type TransactionalIntake interface {
	CreatePlanWithin(context.Context, CreatePlanCommand) (CreatePlanResult, error)
}
