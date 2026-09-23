package port

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

var ErrInvalidGroupOpsMaterialSnapshot = errors.New("invalid group ops material snapshot")

var (
	ErrInvalidGroupOpsMaterialPreparation  = errors.New("invalid group ops material preparation")
	ErrGroupOpsMaterialPreparationConflict = errors.New("group ops material preparation conflict")
	ErrSidebarMaterialNotReady             = errors.New("sidebar material is not provider ready")
	ErrSidebarMaterialPreparing            = errors.New("sidebar material preparation pending")
	ErrSidebarMaterialOutcomeUnknown       = errors.New("sidebar material upload outcome unknown")
	ErrSidebarMaterialPreparationFailed    = errors.New("sidebar material preparation failed")
)

// SidebarImageSendMaterial is the Provider-ready, lease-bound projection used
// by WeCom's sendChatMessage image contract. Its preparation reader must use
// a Provider receipt and never manufacture a media ID from a local ID.
type SidebarImageSendMaterial struct {
	ImageID    int64
	MediaID    string
	ReadyUntil time.Time
}

type SidebarImageSendReader interface {
	ReadSidebarImageForSend(context.Context, int64, time.Time) (SidebarImageSendMaterial, error)
}

// GroupOpsMaterialSnapshot is the immutable message shape of one accepted
// Group Ops execution. Link/card fields are frozen at acceptance. Uploadable
// attachments may omit their temporary Provider media ID; the Outbound EER
// preflight resolves that credential from the separately frozen source facts.
type GroupOpsMaterialSnapshot struct {
	SchemaVersion int                               `json:"schema_version"`
	NodeKind      string                            `json:"node_kind"`
	Attachments   []GroupOpsProviderReadyAttachment `json:"attachments,omitempty"`
}

// GroupOpsMaterialIntentSnapshot is the accepted, provider-neutral message
// shape paired with GroupOpsMaterialSourceSnapshot. Temporary media IDs are
// deliberately absent and can only be filled by Outbound preflight after the
// source digest has been revalidated.
type GroupOpsMaterialIntentSnapshot struct {
	SchemaVersion int                               `json:"schema_version"`
	NodeKind      string                            `json:"node_kind"`
	Attachments   []GroupOpsProviderReadyAttachment `json:"attachments,omitempty"`
}

// GroupOpsMaterialPlan is persisted by Group Ops as an ordered list of stable
// local Media IDs. It is intentionally not a mutable content-package pointer.
// The Media freezer locks and resolves it into GroupOpsMaterialSnapshot before
// an execution is accepted.
type GroupOpsMaterialPlan struct {
	References []GroupOpsMaterialReference `json:"references"`
}

type GroupOpsMaterialReference struct {
	Kind string `json:"kind"`
	ID   int64  `json:"id"`
}

// GroupOpsMaterialSourceSnapshot is captured under the Media transaction that
// accepts a Group Ops execution. It binds the ordered local references to the
// exact source digests that a later preparation reader must prove ready.
type GroupOpsMaterialSourceSnapshot struct {
	SchemaVersion int                               `json:"schema_version"`
	References    []GroupOpsMaterialSourceReference `json:"references"`
}

type GroupOpsMaterialSourceReference struct {
	Reference             GroupOpsMaterialReference       `json:"reference"`
	SourceDigest          string                          `json:"source_digest"`
	ThumbnailImageID      int64                           `json:"thumbnail_image_id,omitempty"`
	ThumbnailSourceDigest string                          `json:"thumbnail_source_digest,omitempty"`
	ProviderFields        GroupOpsProviderReadyAttachment `json:"provider_fields,omitempty"`
}

// GroupOpsProviderReadyAttachment is intentionally the small intersection of
// the WeCom group-message template contract and the legacy P4 content package.
// It contains no local media record IDs, credentials, or mutable URLs that a
// worker would need to resolve later.
type GroupOpsProviderReadyAttachment struct {
	MsgType     string `json:"msgtype"`
	MediaID     string `json:"media_id,omitempty"`
	AppID       string `json:"appid,omitempty"`
	PagePath    string `json:"pagepath,omitempty"`
	Title       string `json:"title,omitempty"`
	URL         string `json:"url,omitempty"`
	Description string `json:"description,omitempty"`
	PicURL      string `json:"picurl,omitempty"`
}

// GroupOpsMaterialSnapshotFreezer belongs to Media and remains the strict
// provider-ready reader for legacy consumers. New Group Ops EER intents freeze
// source facts at acceptance and resolve temporary credentials in preflight.
type GroupOpsMaterialSnapshotFreezer interface {
	FreezeGroupOpsMaterial(context.Context, GroupOpsMaterialSourceSnapshot, time.Time) (GroupOpsMaterialSnapshot, error)
}

// GroupOpsMaterialSourceCapturer runs in the Group Ops acceptance UoW. It
// locks enabled Media facts and produces the immutable source snapshot before
// any provider preparation is queued.
type GroupOpsMaterialSourceCapturer interface {
	CaptureGroupOpsMaterialSources(context.Context, GroupOpsMaterialPlan) (GroupOpsMaterialSourceSnapshot, error)
}

// GroupOpsMaterialPreparation is the Media-owned proof that a mutable local
// media record has been prepared for a Provider.  The receipt digest is an
// opaque digest of the Provider receipt; the payload contains only the
// provider-shaped attachment that the outbound worker may submit later.
// Group Ops never writes or interprets this record.
type GroupOpsMaterialPreparation struct {
	Reference     GroupOpsMaterialReference       `json:"reference"`
	SourceDigest  string                          `json:"source_digest"`
	ReceiptDigest string                          `json:"receipt_digest"`
	ReadyUntil    time.Time                       `json:"ready_until"`
	Attachment    GroupOpsProviderReadyAttachment `json:"attachment"`
}

// GroupOpsMaterialPreparationCommand is written only after an approved
// Provider preparation adapter has obtained a receipt outside the database
// transaction.  The writer records the receipt and its lease atomically in
// Media-owned tables.  Group invite links deliberately carry no preparation
// receipt: their real title/url/description are already captured by Media.
type GroupOpsMaterialPreparationCommand struct {
	SourceSnapshot  GroupOpsMaterialSourceSnapshot `json:"source_snapshot"`
	Items           []GroupOpsMaterialPreparation  `json:"items"`
	RequiredThrough time.Time                      `json:"required_through"`
	Actor           int64                          `json:"actor"`
	IdempotencyKey  string                         `json:"idempotency_key"`
}

// GroupOpsMaterialPreparationReceipt identifies an idempotent Media write.
// It intentionally exposes digests and counts, never raw Provider response,
// tokens, URLs, or credentials.
type GroupOpsMaterialPreparationReceipt struct {
	ID            int64     `json:"id"`
	Actor         int64     `json:"actor"`
	KeyDigest     string    `json:"key_digest"`
	CommandDigest string    `json:"command_digest"`
	ItemCount     int       `json:"item_count"`
	CreatedAt     time.Time `json:"created_at"`
}

// GroupOpsMaterialPreparationReader is a transaction-bound Media port.  The
// caller must already have captured/locked the source facts in the same UoW;
// the reader returns one ordered item per source or fails closed when a
// non-invite receipt/lease is absent, expired, or tied to a different digest.
type GroupOpsMaterialPreparationReader interface {
	ReadPreparedGroupOpsMaterials(context.Context, GroupOpsMaterialSourceSnapshot, time.Time) ([]GroupOpsMaterialPreparation, error)
}

// GroupOpsMaterialPreparationWriter is a transaction-neutral Media port for
// approved Provider adapters.  Implementations open their own UoW and must
// persist the preparation receipt, audit event, and outbox row atomically.
// Provider-disabled adapters must never call this method.
type GroupOpsMaterialPreparationWriter interface {
	RecordPreparedGroupOpsMaterials(context.Context, GroupOpsMaterialPreparationCommand) (GroupOpsMaterialPreparationReceipt, error)
}

func ValidateGroupOpsMaterialPreparationCommand(value GroupOpsMaterialPreparationCommand) error {
	if value.RequiredThrough.IsZero() || value.Actor < 1 || !validPreparationIdempotencyKey(value.IdempotencyKey) {
		return ErrInvalidGroupOpsMaterialPreparation
	}
	if err := ValidateGroupOpsMaterialPreparations(value.SourceSnapshot, value.Items, value.RequiredThrough); err != nil {
		return err
	}
	for _, source := range value.SourceSnapshot.References {
		if source.Reference.Kind != "group_invite" {
			return nil
		}
	}
	return ErrInvalidGroupOpsMaterialPreparation
}

// ValidateGroupOpsMaterialPreparations validates the ordered rows returned
// by, or written to, the Media preparation boundary without requiring an
// actor/idempotency key. It is shared by the transaction-bound reader and the
// transaction-neutral writer so they cannot disagree about a provider-ready
// payload.
func ValidateGroupOpsMaterialPreparations(sourceSnapshot GroupOpsMaterialSourceSnapshot, items []GroupOpsMaterialPreparation, requiredThrough time.Time) error {
	if ValidateGroupOpsMaterialSourceSnapshot(sourceSnapshot) != nil || requiredThrough.IsZero() || len(items) != len(sourceSnapshot.References) {
		return ErrInvalidGroupOpsMaterialPreparation
	}
	for index, source := range sourceSnapshot.References {
		item := items[index]
		if item.Reference != source.Reference || item.SourceDigest != source.SourceDigest || item.Attachment.MsgType == "" {
			return ErrInvalidGroupOpsMaterialPreparation
		}
		if source.Reference.Kind == "group_invite" {
			if item.ReceiptDigest != "" || !item.ReadyUntil.IsZero() || item.Attachment != source.ProviderFields {
				return ErrInvalidGroupOpsMaterialPreparation
			}
			continue
		}
		if !validDigest(item.ReceiptDigest) || !item.ReadyUntil.After(requiredThrough) {
			return ErrInvalidGroupOpsMaterialPreparation
		}
		want := source.Reference.Kind
		if want == "attachment" {
			want = "file"
		}
		if item.Attachment.MsgType != want || ValidateGroupOpsProviderReadyAttachments([]GroupOpsProviderReadyAttachment{item.Attachment}) != nil {
			return ErrInvalidGroupOpsMaterialPreparation
		}
		if source.Reference.Kind == "miniprogram" && (item.Attachment.AppID != source.ProviderFields.AppID || item.Attachment.PagePath != source.ProviderFields.PagePath || item.Attachment.Title != source.ProviderFields.Title) {
			return ErrInvalidGroupOpsMaterialPreparation
		}
	}
	return nil
}

func validPreparationIdempotencyKey(value string) bool {
	return len(value) >= 16 && len(value) <= 128 && strings.TrimSpace(value) == value
}

func ValidateGroupOpsMaterialSnapshot(value GroupOpsMaterialSnapshot) error {
	if value.SchemaVersion != 2 || value.NodeKind != "message" || len(value.Attachments) > 9 {
		return ErrInvalidGroupOpsMaterialSnapshot
	}
	return ValidateGroupOpsProviderReadyAttachments(value.Attachments)
}

func ValidateGroupOpsMaterialIntentSnapshot(value GroupOpsMaterialIntentSnapshot) error {
	if value.SchemaVersion != 2 || value.NodeKind != "message" || len(value.Attachments) > 9 {
		return ErrInvalidGroupOpsMaterialSnapshot
	}
	for _, attachment := range value.Attachments {
		candidate := attachment
		switch candidate.MsgType {
		case "image", "file", "miniprogram":
			if candidate.MediaID != "" {
				return ErrInvalidGroupOpsMaterialSnapshot
			}
			candidate.MediaID = "pending-provider-media"
		case "link":
		default:
			return ErrInvalidGroupOpsMaterialSnapshot
		}
		if ValidateGroupOpsProviderReadyAttachments([]GroupOpsProviderReadyAttachment{candidate}) != nil {
			return ErrInvalidGroupOpsMaterialSnapshot
		}
	}
	return nil
}

func ValidateGroupOpsMaterialPlan(value GroupOpsMaterialPlan) error {
	if len(value.References) == 0 || len(value.References) > 9 {
		return ErrInvalidGroupOpsMaterialSnapshot
	}
	images, minis, attachments, invites := 0, 0, 0, 0
	seen := make(map[string]struct{}, len(value.References))
	for _, reference := range value.References {
		if reference.ID < 1 {
			return ErrInvalidGroupOpsMaterialSnapshot
		}
		key := reference.Kind + ":" + strconv.FormatInt(reference.ID, 10)
		if _, exists := seen[key]; exists {
			return ErrInvalidGroupOpsMaterialSnapshot
		}
		seen[key] = struct{}{}
		switch reference.Kind {
		case "image":
			images++
		case "miniprogram":
			minis++
		case "attachment":
			attachments++
		case "group_invite":
			invites++
		default:
			return ErrInvalidGroupOpsMaterialSnapshot
		}
	}
	if images > 3 || minis > 1 || attachments > 9 || invites > 1 {
		return ErrInvalidGroupOpsMaterialSnapshot
	}
	return nil
}

func ValidateGroupOpsMaterialSourceSnapshot(value GroupOpsMaterialSourceSnapshot) error {
	if value.SchemaVersion != 1 || len(value.References) == 0 {
		return ErrInvalidGroupOpsMaterialSnapshot
	}
	plan := GroupOpsMaterialPlan{References: make([]GroupOpsMaterialReference, len(value.References))}
	for index, source := range value.References {
		plan.References[index] = source.Reference
		if !validDigest(source.SourceDigest) {
			return ErrInvalidGroupOpsMaterialSnapshot
		}
		switch source.Reference.Kind {
		case "image", "attachment":
			if source.ThumbnailImageID != 0 || !emptyAttachmentFields(source.ProviderFields) {
				return ErrInvalidGroupOpsMaterialSnapshot
			}
		case "miniprogram":
			if source.ThumbnailImageID < 1 || !validDigest(source.ThumbnailSourceDigest) || source.ProviderFields.MsgType != "miniprogram" || source.ProviderFields.MediaID != "" ||
				!validGroupOpsText(source.ProviderFields.AppID, 128) || !validGroupOpsText(source.ProviderFields.PagePath, 1024) ||
				!validGroupOpsText(source.ProviderFields.Title, 64) || !emptyAttachmentFields(source.ProviderFields, "AppID", "PagePath", "Title") {
				return ErrInvalidGroupOpsMaterialSnapshot
			}
		case "group_invite":
			if (source.ThumbnailImageID == 0) != (source.ThumbnailSourceDigest == "") || (source.ThumbnailImageID != 0 && !validDigest(source.ThumbnailSourceDigest)) || source.ProviderFields.MsgType != "link" || ValidateGroupOpsProviderReadyAttachments([]GroupOpsProviderReadyAttachment{source.ProviderFields}) != nil {
				return ErrInvalidGroupOpsMaterialSnapshot
			}
		}
	}
	return ValidateGroupOpsMaterialPlan(plan)
}

func ValidateGroupOpsProviderReadyAttachments(attachments []GroupOpsProviderReadyAttachment) error {
	if len(attachments) > 9 {
		return ErrInvalidGroupOpsMaterialSnapshot
	}
	images, minis, files, invites := 0, 0, 0, 0
	for _, attachment := range attachments {
		switch attachment.MsgType {
		case "image":
			images++
			if !validGroupOpsText(attachment.MediaID, 1024) || !emptyAttachmentFields(attachment, "MediaID") {
				return ErrInvalidGroupOpsMaterialSnapshot
			}
		case "file":
			files++
			if !validGroupOpsText(attachment.MediaID, 1024) || !emptyAttachmentFields(attachment, "MediaID") {
				return ErrInvalidGroupOpsMaterialSnapshot
			}
		case "miniprogram":
			minis++
			if !validGroupOpsText(attachment.AppID, 128) || !validGroupOpsText(attachment.PagePath, 1024) ||
				!validGroupOpsText(attachment.Title, 64) || !validGroupOpsText(attachment.MediaID, 1024) ||
				!emptyAttachmentFields(attachment, "MediaID", "AppID", "PagePath", "Title") {
				return ErrInvalidGroupOpsMaterialSnapshot
			}
		case "link":
			invites++
			if !validGroupOpsText(attachment.Title, 128) || !validGroupInviteURL(attachment.URL) ||
				!validOptionalGroupOpsText(attachment.Description, 512) ||
				(attachment.PicURL != "" && !validHTTPURL(attachment.PicURL)) ||
				!emptyAttachmentFields(attachment, "Title", "URL", "Description", "PicURL") {
				return ErrInvalidGroupOpsMaterialSnapshot
			}
		default:
			return ErrInvalidGroupOpsMaterialSnapshot
		}
	}
	if images > 3 || minis > 1 || files > 9 || invites > 1 {
		return ErrInvalidGroupOpsMaterialSnapshot
	}
	return nil
}

func validDigest(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, char := range value[len("sha256:"):] {
		if !(char >= '0' && char <= '9') && !(char >= 'a' && char <= 'f') {
			return false
		}
	}
	return true
}

func validGroupOpsText(value string, limit int) bool {
	return value != "" && validOptionalGroupOpsText(value, limit)
}

func validOptionalGroupOpsText(value string, limit int) bool {
	return utf8.ValidString(value) && strings.TrimSpace(value) == value && len(value) <= limit
}

func validGroupInviteURL(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || parsed.RawPath != "" {
		return false
	}
	if parsed.Host == "work.weixin.qq.com" {
		return strings.HasPrefix(parsed.Path, "/gm/") && len(parsed.Path) > len("/gm/") && !strings.Contains(parsed.Path[len("/gm/"):], "/")
	}
	return parsed.Host == "www.youcangogogo.com" && strings.HasPrefix(parsed.Path, "/gi/") && len(parsed.Path) > len("/gi/") && !strings.Contains(parsed.Path[len("/gi/"):], "/")
}

func validHTTPURL(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != "" && parsed.User == nil && parsed.Fragment == ""
}

func emptyAttachmentFields(value GroupOpsProviderReadyAttachment, allowed ...string) bool {
	permitted := make(map[string]struct{}, len(allowed))
	for _, field := range allowed {
		permitted[field] = struct{}{}
	}
	for field, current := range map[string]string{
		"MediaID": value.MediaID, "AppID": value.AppID, "PagePath": value.PagePath, "Title": value.Title,
		"URL": value.URL, "Description": value.Description, "PicURL": value.PicURL,
	} {
		if _, ok := permitted[field]; !ok && current != "" {
			return false
		}
	}
	return true
}
