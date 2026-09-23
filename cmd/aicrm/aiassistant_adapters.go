package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accessport "github.com/qianlan33333-png/AI-CRM-v3/internal/access/port"
	aiassistantapp "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/app"
	aiassistantport "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/port"
	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
	customerapp "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/app"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	mediastore "github.com/qianlan33333-png/AI-CRM-v3/internal/media/store"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/outbound"
	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

// Concrete adapters below intentionally live in the composition root: AI
// Assistant depends on stable values and never imports Customer, Access, or
// Media stores.
type aiCustomerSnapshotAdapter struct {
	read func(context.Context, customerdomain.CustomerID) (customerdomain.CustomerID, customerdomain.Status, string, string, error)
}

type legacyExcelCardReader interface {
	LoadExcelCard(context.Context, aiassistantport.ExcelCard) (outbound.PrivateMessageAttachment, error)
}

// excelMaterialSources gives historical CoverImageID=0 batches an immutable,
// digest-addressed read path into the same Outbound preparation cache. Daily
// enumeration remains Media-owned; this compatibility source is read only
// when an accepted historical payload references its frozen digest.
type excelMaterialSources struct {
	media outboundport.MaterialSourceReader
	excel legacyExcelCardReader
}

func (s excelMaterialSources) ListEnabledSourceSnapshots(ctx context.Context, request outboundport.MaterialSnapshotPageRequest) (outboundport.MaterialSnapshotPage, error) {
	return s.media.ListEnabledSourceSnapshots(ctx, request)
}
func (s excelMaterialSources) GetSourceSnapshot(ctx context.Context, sourceRef string) (outboundport.MaterialSourceSnapshot, error) {
	if !strings.HasPrefix(sourceRef, "excel-cover:") {
		return s.media.GetSourceSnapshot(ctx, sourceRef)
	}
	snapshot, _, err := s.legacyExcelSource(ctx, sourceRef)
	return snapshot, err
}
func (s excelMaterialSources) ReadSourceBytes(ctx context.Context, expected outboundport.MaterialSourceSnapshot) (outboundport.MaterialSourceContent, error) {
	if !strings.HasPrefix(expected.SourceRef, "excel-cover:") {
		return s.media.ReadSourceBytes(ctx, expected)
	}
	snapshot, content, err := s.legacyExcelSource(ctx, expected.SourceRef)
	if err != nil {
		return outboundport.MaterialSourceContent{}, err
	}
	if snapshot != expected {
		return outboundport.MaterialSourceContent{}, outboundport.ErrMaterialSourceChanged
	}
	return content, nil
}
func (s excelMaterialSources) legacyExcelSource(ctx context.Context, sourceRef string) (outboundport.MaterialSourceSnapshot, outboundport.MaterialSourceContent, error) {
	if s.excel == nil {
		return outboundport.MaterialSourceSnapshot{}, outboundport.MaterialSourceContent{}, outboundport.ErrMaterialSourceChanged
	}
	digestText := strings.TrimPrefix(sourceRef, "excel-cover:")
	digest := effectport.Digest(digestText)
	if !effectport.ValidDigest(digest) {
		return outboundport.MaterialSourceSnapshot{}, outboundport.MaterialSourceContent{}, outboundport.ErrMaterialSourceChanged
	}
	card := aiassistantport.ExcelCard{AppID: "legacy-excel-cover", Path: "pages/legacy-cover", Title: "legacy excel cover", CoverDigest: digest}
	attachment, err := s.excel.LoadExcelCard(ctx, card)
	if err != nil || len(attachment.Content) == 0 {
		return outboundport.MaterialSourceSnapshot{}, outboundport.MaterialSourceContent{}, outboundport.ErrMaterialSourceChanged
	}
	decoded, err := hex.DecodeString(strings.TrimPrefix(digestText, "sha256:"))
	if err != nil || len(decoded) != sha256.Size {
		return outboundport.MaterialSourceSnapshot{}, outboundport.MaterialSourceContent{}, outboundport.ErrMaterialSourceChanged
	}
	var contentDigest [sha256.Size]byte
	copy(contentDigest[:], decoded)
	snapshot := outboundport.MaterialSourceSnapshot{SourceRef: sourceRef, SourceType: "image", ContentDigest: contentDigest, FileName: attachment.FileName, MediaType: attachment.MediaType, SizeBytes: int64(len(attachment.Content)), SnapshotVersion: 1}
	return snapshot, outboundport.MaterialSourceContent{Bytes: attachment.Content, FileName: attachment.FileName, MediaType: attachment.MediaType}, nil
}

func (a aiCustomerSnapshotAdapter) CustomerSnapshot(ctx context.Context, id customerdomain.CustomerID) (aiassistantapp.CustomerSnapshot, error) {
	canonical, status, name, label, err := a.read(ctx, id)
	if errors.Is(err, customerapp.ErrNotFound) {
		// A missing directory projection is a target eligibility fact, not a
		// transient database failure. The service records it safely and creates
		// no recipient for this target.
		return aiassistantapp.CustomerSnapshot{}, nil
	}
	return aiassistantapp.CustomerSnapshot{CanonicalID: canonical, Status: status, DisplayName: name, OneIDLabel: label}, err
}

type aiStaffSnapshotAdapter struct{ repository accessport.Repository }

func (a aiStaffSnapshotAdapter) StaffSnapshot(ctx context.Context, id int64) (aiassistantapp.StaffSnapshot, error) {
	user, err := a.repository.UserByID(ctx, id, false)
	if errors.Is(err, accessdomain.ErrNotFound) {
		return aiassistantapp.StaffSnapshot{}, nil
	}
	if err != nil {
		return aiassistantapp.StaffSnapshot{}, err
	}
	return aiassistantapp.StaffSnapshot{ID: user.ID, DisplayName: user.DisplayName, Active: user.Active}, nil
}

func (a aiStaffSnapshotAdapter) StaffByWeComUserID(ctx context.Context, value string) (aiassistantapp.StaffSnapshot, error) {
	user, err := a.repository.UserByWeComUserID(ctx, value, false)
	if errors.Is(err, accessdomain.ErrNotFound) {
		return aiassistantapp.StaffSnapshot{}, nil
	}
	if err != nil {
		return aiassistantapp.StaffSnapshot{}, err
	}
	return aiassistantapp.StaffSnapshot{ID: user.ID, DisplayName: user.DisplayName, Active: user.Active}, nil
}

type aiMaterialAdapter struct {
	capturer   mediaport.GroupOpsMaterialSourceCapturer
	references mediaport.MaterialReferenceRegistrar
	legacy     mediaport.LegacyMaterialMappingResolver
}

func (a aiMaterialAdapter) ResolveMaterial(ctx context.Context, block aiassistantport.ContentBlock) (aiassistantport.ContentBlock, error) {
	kind := block.MaterialKind
	switch block.Kind {
	case aiassistantport.ContentImage:
		kind = "image"
	case aiassistantport.ContentMiniProgram:
		kind = "miniprogram"
	case aiassistantport.ContentLink:
		kind = "group_invite"
	case aiassistantport.ContentAttachment:
		kind = "attachment"
	}
	if block.LegacySourceSystem != "" || block.LegacyMaterialID != "" {
		if a.legacy == nil || block.LegacySourceSystem == "" || block.LegacyMaterialID == "" {
			return aiassistantport.ContentBlock{}, aiassistantapp.ErrLegacyMaterialUnmapped
		}
		mapping, found, err := a.legacy.ResolveLegacyMaterialMapping(ctx, mediaport.LegacyMaterialReference{SourceSystem: block.LegacySourceSystem, MaterialKind: kind, LegacyID: block.LegacyMaterialID})
		if err != nil {
			return aiassistantport.ContentBlock{}, err
		}
		if !found || mapping.MaterialKind != kind || mapping.MaterialID < 1 || mapping.SourceDigest == "" {
			return aiassistantport.ContentBlock{}, aiassistantapp.ErrLegacyMaterialUnmapped
		}
		block.MaterialID = mapping.MaterialID
		block.LegacySourceSystem, block.LegacyMaterialID = "", ""
		// A mapping is usable only while the Media source still equals the
		// digest verified by the frozen-source import.
		snapshot, captureErr := a.capture(ctx, kind, block.MaterialID)
		if errors.Is(captureErr, mediastore.ErrNotFound) || errors.Is(captureErr, mediastore.ErrConflict) || len(snapshot.References) != 1 || snapshot.References[0].SourceDigest != mapping.SourceDigest {
			return aiassistantport.ContentBlock{}, aiassistantapp.ErrLegacyMaterialUnmapped
		}
		if captureErr != nil {
			return aiassistantport.ContentBlock{}, captureErr
		}
		block.MaterialKind = kind
		block.MaterialDigest = effectport.Digest(mapping.SourceDigest)
		return block, nil
	}
	snapshot, err := a.capture(ctx, kind, block.MaterialID)
	if err != nil || len(snapshot.References) != 1 {
		return aiassistantport.ContentBlock{}, errors.New("material unavailable")
	}
	block.MaterialKind = kind
	block.MaterialDigest = effectport.Digest(snapshot.References[0].SourceDigest)
	return block, nil
}

func (a aiMaterialAdapter) capture(ctx context.Context, kind string, id int64) (mediaport.GroupOpsMaterialSourceSnapshot, error) {
	if a.capturer == nil || id < 1 || kind == "" {
		return mediaport.GroupOpsMaterialSourceSnapshot{}, errors.New("material unavailable")
	}
	return a.capturer.CaptureGroupOpsMaterialSources(ctx, mediaport.GroupOpsMaterialPlan{References: []mediaport.GroupOpsMaterialReference{{Kind: kind, ID: id}}})
}

func (a aiMaterialAdapter) RegisterMaterialReference(ctx context.Context, block aiassistantport.ContentBlock, reference effectport.Digest) error {
	if a.references == nil || !effectport.ValidDigest(reference) {
		return errors.New("material reference unavailable")
	}
	return a.references.RegisterMediaReference(ctx, mediaport.MaterialReference{MaterialKind: block.MaterialKind, MaterialID: block.MaterialID, Owner: "aiassistant.content-version", ReferenceDigest: string(reference)})
}

type aiFollowReader interface {
	IsActive(context.Context, string, string, customerdomain.CustomerID) (bool, error)
}
type aiPrivateTargetResolver struct {
	deferred interface {
		LoadDeferredTarget(context.Context, string) (aiassistantport.DeferredTarget, error)
	}
	resolver      identityport.Resolver
	trusted       identityport.ExternalIdentityValueReader
	uow           platformport.UnitOfWork
	identities    identityport.OutboundWeComIdentityReader
	access        accessport.Repository
	relationships aiFollowReader
	corpID        string
}

func (a aiPrivateTargetResolver) ResolvePrivateMessageTarget(ctx context.Context, customerID customerdomain.CustomerID, staffID int64) (outbound.PrivateMessageTarget, error) {
	var target outbound.PrivateMessageTarget
	err := a.uow.Within(ctx, func(tx context.Context) error {
		user, err := a.access.UserByID(tx, staffID, false)
		if err != nil || !user.Active || user.WeComUserID == "" {
			return errors.New("staff channel identity unavailable")
		}
		external, found, err := a.identities.VerifiedWeComIdentityForCustomer(tx, customerID, a.corpID)
		if err != nil || !found {
			return errors.New("customer channel identity unavailable")
		}
		active, err := a.relationships.IsActive(tx, a.corpID, user.WeComUserID, customerID)
		if err != nil || !active {
			return errors.New("customer relationship unavailable")
		}
		target = outbound.PrivateMessageTarget{ExternalUserID: external, StaffUserID: user.WeComUserID}
		return nil
	})
	return target, err
}

type aiImageReader interface {
	GetImageVariant(context.Context, int64, string) (mediaport.ImageVariant, error)
}
type aiMaterialDetails interface {
	MiniProgram(context.Context, int64) (map[string]any, error)
	GroupInvite(context.Context, int64) (map[string]any, error)
}
type aiAttachmentReader interface {
	Attachment(context.Context, int64) (map[string]any, []byte, error)
}
type aiPrivatePayloadReader struct {
	excel interface {
		LoadExcelCard(context.Context, aiassistantport.ExcelCard) (outbound.PrivateMessageAttachment, error)
	}
	content     aiassistantport.OutboundPayloadReader
	images      aiImageReader
	materials   aiMaterialDetails
	attachments aiAttachmentReader
	uow         platformport.UnitOfWork
	capturer    mediaport.GroupOpsMaterialSourceCapturer
	// sources and preparer are stable Outbound ports supplied by composition.
	// When present, every Provider-uploadable Media block is reduced to a
	// pre-prepared MediaID before Execute; bytes remain with Media.
	sources     outboundport.MaterialSourceReader
	preparer    outboundport.MaterialPreparer
	scopeDigest string
}

// frozenAutomationContent is the versioned object stored by Outbound for an
// accepted automatic message. It holds only fixed text and Media source
// digests/local references; it never stores binary content or Provider IDs.
type frozenAutomationContent struct {
	SchemaVersion   int                              `json:"schema_version"`
	ContentText     string                           `json:"content_text,omitempty"`
	Sources         []frozenAutomationMaterialSource `json:"sources,omitempty"`
	ObservationPath string                           `json:"observation_path,omitempty"`
}

// frozenAutomationMaterialSource excludes Provider-shaped metadata and binary
// content. The captured digest proves the complete Media-owned source when it
// is re-read immediately before the one Provider request.
type frozenAutomationMaterialSource struct {
	Kind                  string `json:"kind"`
	ID                    int64  `json:"id"`
	SourceDigest          string `json:"source_digest"`
	Name                  string `json:"name,omitempty"`
	Version               int64  `json:"version,omitempty"`
	AppID                 string `json:"appid,omitempty"`
	PagePath              string `json:"pagepath,omitempty"`
	Title                 string `json:"title,omitempty"`
	ThumbnailImageID      int64  `json:"thumbnail_image_id,omitempty"`
	ThumbnailSourceDigest string `json:"thumbnail_source_digest,omitempty"`
}

type automationOutboundContentFreezer struct {
	capturer  mediaport.GroupOpsMaterialSourceCapturer
	materials aiMaterialDetails
}

func (a automationOutboundContentFreezer) FreezeOutboundContent(ctx context.Context, content automationport.OutboundPublishedContent) (json.RawMessage, [32]byte, error) {
	if a.capturer == nil || content.AgentID < 1 || content.PublishedVersion < 1 || len(content.Content.DynamicMiniprogramCard) != 0 {
		return nil, [32]byte{}, errors.New("automation content freezer unavailable")
	}
	references := make([]mediaport.GroupOpsMaterialReference, 0, len(content.Content.ImageLibraryIDs)+len(content.Content.MiniprogramLibraryIDs)+len(content.Content.AttachmentLibraryIDs)+len(content.Content.GroupInviteLibraryIDs))
	for _, id := range content.Content.ImageLibraryIDs {
		references = append(references, mediaport.GroupOpsMaterialReference{Kind: "image", ID: id})
	}
	for _, id := range content.Content.MiniprogramLibraryIDs {
		references = append(references, mediaport.GroupOpsMaterialReference{Kind: "miniprogram", ID: id})
	}
	for _, id := range content.Content.AttachmentLibraryIDs {
		references = append(references, mediaport.GroupOpsMaterialReference{Kind: "attachment", ID: id})
	}
	for _, id := range content.Content.GroupInviteLibraryIDs {
		references = append(references, mediaport.GroupOpsMaterialReference{Kind: "group_invite", ID: id})
	}
	snapshot := frozenAutomationContent{SchemaVersion: 1, ContentText: strings.TrimSpace(content.Content.ContentText)}
	if len(references) > 0 {
		var err error
		captured, err := a.capturer.CaptureGroupOpsMaterialSources(ctx, mediaport.GroupOpsMaterialPlan{References: references})
		if err != nil || mediaport.ValidateGroupOpsMaterialSourceSnapshot(captured) != nil {
			return nil, [32]byte{}, errors.New("automation content material unavailable")
		}
		snapshot.Sources = make([]frozenAutomationMaterialSource, len(captured.References))
		for index, source := range captured.References {
			snapshot.Sources[index] = frozenAutomationMaterialSource{Kind: source.Reference.Kind, ID: source.Reference.ID, SourceDigest: source.SourceDigest}
		}
	}
	if snapshot.ContentText == "" && len(snapshot.Sources) == 0 {
		return nil, [32]byte{}, errors.New("automation content is empty")
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return nil, [32]byte{}, err
	}
	return raw, sha256.Sum256(raw), nil
}

type automationFrozenPayloadReader struct{ preparer aiPrivatePayloadReader }

func (a automationFrozenPayloadReader) PrepareFrozenAutomationMessageMedia(ctx context.Context, raw json.RawMessage, digest [32]byte) error {
	_, err := a.LoadFrozenAutomationMessagePayload(ctx, raw, digest)
	return err
}

func (a automationFrozenPayloadReader) LoadFrozenAutomationMessagePayload(ctx context.Context, raw json.RawMessage, digest [32]byte) (outbound.PrivateMessagePayload, error) {
	var snapshot frozenAutomationContent
	if len(raw) == 0 || json.Unmarshal(raw, &snapshot) != nil || snapshot.SchemaVersion != 1 || sha256.Sum256(mustMarshalFrozenAutomationContent(snapshot)) != digest {
		return outbound.PrivateMessagePayload{}, errors.New("automation content snapshot is invalid")
	}
	references := make([]mediaport.GroupOpsMaterialReference, len(snapshot.Sources))
	for index, source := range snapshot.Sources {
		if !effectport.ValidDigest(effectport.Digest(source.SourceDigest)) {
			return outbound.PrivateMessagePayload{}, errors.New("automation content source digest is invalid")
		}
		references[index] = mediaport.GroupOpsMaterialReference{Kind: source.Kind, ID: source.ID}
	}
	if len(references) > 0 && mediaport.ValidateGroupOpsMaterialPlan(mediaport.GroupOpsMaterialPlan{References: references}) != nil {
		return outbound.PrivateMessagePayload{}, errors.New("automation content sources are invalid")
	}
	blocks := make([]aiassistantport.ContentBlock, 0, 1+len(snapshot.Sources))
	frozenAttachments := make([]outbound.PrivateMessageAttachment, 0, len(snapshot.Sources))
	if text := strings.TrimSpace(snapshot.ContentText); text != "" {
		blocks = append(blocks, aiassistantport.ContentBlock{Kind: aiassistantport.ContentText, Text: text})
	}
	for _, source := range snapshot.Sources {
		if source.Kind == "miniprogram" && source.AppID != "" {
			attachment, err := a.preparer.prepareFrozenAutomationMiniProgram(ctx, source)
			if err != nil {
				return outbound.PrivateMessagePayload{}, err
			}
			frozenAttachments = append(frozenAttachments, attachment)
			continue
		}
		block := aiassistantport.ContentBlock{MaterialKind: source.Kind, MaterialID: source.ID, MaterialDigest: effectport.Digest(source.SourceDigest)}
		switch source.Kind {
		case "image":
			block.Kind = aiassistantport.ContentImage
		case "miniprogram":
			block.Kind = aiassistantport.ContentMiniProgram
		case "attachment":
			block.Kind = aiassistantport.ContentAttachment
		case "group_invite":
			block.Kind = aiassistantport.ContentLink
		default:
			return outbound.PrivateMessagePayload{}, errors.New("automation content source kind is invalid")
		}
		blocks = append(blocks, block)
	}
	payload, err := a.preparer.prepareBlocks(ctx, blocks)
	if err != nil {
		return outbound.PrivateMessagePayload{}, err
	}
	payload.Attachments = append(payload.Attachments, frozenAttachments...)
	return payload, nil
}

func (a aiPrivatePayloadReader) prepareFrozenAutomationMiniProgram(ctx context.Context, source frozenAutomationMaterialSource) (outbound.PrivateMessageAttachment, error) {
	if a.sources == nil || a.preparer == nil || strings.TrimSpace(source.AppID) == "" || strings.TrimSpace(source.PagePath) == "" || strings.TrimSpace(source.Title) == "" ||
		source.ThumbnailImageID < 1 || !effectport.ValidDigest(effectport.Digest(source.ThumbnailSourceDigest)) {
		return outbound.PrivateMessageAttachment{}, errors.New("frozen mini program is invalid")
	}
	thumbnail, err := a.sources.GetSourceSnapshot(ctx, "image:"+strconv.FormatInt(source.ThumbnailImageID, 10))
	if err != nil || sourceSnapshotDigest(thumbnail) != source.ThumbnailSourceDigest {
		return outbound.PrivateMessageAttachment{}, errors.New("frozen mini program thumbnail unavailable")
	}
	mediaID, err := a.readyMedia(ctx, thumbnail)
	if err != nil {
		return outbound.PrivateMessageAttachment{}, err
	}
	return outbound.PrivateMessageAttachment{Kind: "mini_program", MediaID: mediaID, AppID: source.AppID, PagePath: source.PagePath, Title: source.Title}, nil
}

func mustMarshalFrozenAutomationContent(value frozenAutomationContent) []byte {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	return raw
}

func (a aiPrivatePayloadReader) LoadPrivateMessagePayload(ctx context.Context, reference string, digest effectport.Digest) (outbound.PrivateMessagePayload, error) {
	content, err := a.content.LoadOutboundContent(ctx, reference, digest)
	if err != nil {
		return outbound.PrivateMessagePayload{}, err
	}
	return a.prepareBlocks(ctx, content.Blocks)
}

// verifyFrozenMaterial is deliberately called after the bytes/fields are read.
// Capture then checks the Media Owner's enabled/archive and dependent-cover
// gates in a transaction and compares that exact current source to the frozen
// digest. This prevents management-only reads from bypassing a disabled item,
// while still rejecting a source changed between capture and preparation.
func (a aiPrivatePayloadReader) verifyFrozenMaterial(ctx context.Context, block aiassistantport.ContentBlock) error {
	if block.Kind == aiassistantport.ContentText {
		return nil
	}
	if a.uow == nil || a.capturer == nil || !effectport.ValidDigest(block.MaterialDigest) {
		return errors.New("frozen material availability unavailable")
	}
	kind := block.MaterialKind
	switch block.Kind {
	case aiassistantport.ContentImage:
		kind = "image"
	case aiassistantport.ContentMiniProgram:
		kind = "miniprogram"
	case aiassistantport.ContentLink:
		kind = "group_invite"
	case aiassistantport.ContentAttachment:
		kind = "attachment"
	}
	return a.uow.Within(ctx, func(tx context.Context) error {
		snapshot, err := a.capturer.CaptureGroupOpsMaterialSources(tx, mediaport.GroupOpsMaterialPlan{References: []mediaport.GroupOpsMaterialReference{{Kind: kind, ID: block.MaterialID}}})
		if err != nil || len(snapshot.References) != 1 || effectport.Digest(snapshot.References[0].SourceDigest) != block.MaterialDigest {
			return errors.New("frozen material unavailable or drifted")
		}
		return nil
	})
}

func (a aiPrivatePayloadReader) prepareBlocks(ctx context.Context, blocks []aiassistantport.ContentBlock) (outbound.PrivateMessagePayload, error) {
	result := outbound.PrivateMessagePayload{}
	for _, block := range blocks {
		if block.ExcelCard != nil {
			card, err := a.prepareExcelCard(ctx, *block.ExcelCard)
			if err != nil {
				return result, err
			}
			result.Attachments = append(result.Attachments, card)
			continue
		}
		switch block.Kind {
		case aiassistantport.ContentText:
			if result.Text != "" {
				result.Text += "\n"
			}
			result.Text += block.Text
		case aiassistantport.ContentImage:
			if a.mediaPreparationEnabled() {
				mediaID, e := a.readyFrozenMedia(ctx, block, "image:"+strconv.FormatInt(block.MaterialID, 10), block.MaterialDigest)
				if e != nil {
					return outbound.PrivateMessagePayload{}, e
				}
				result.Attachments = append(result.Attachments, outbound.PrivateMessageAttachment{Kind: "image", MediaID: mediaID})
				continue
			}
			variant, e := a.images.GetImageVariant(ctx, block.MaterialID, "original")
			if e != nil {
				return outbound.PrivateMessagePayload{}, e
			}
			if !frozenSourceMatches(block.MaterialDigest, blobSourceDigest(variant.Content)) {
				return outbound.PrivateMessagePayload{}, errors.New("frozen image material drift")
			}
			if err := a.verifyFrozenMaterial(ctx, block); err != nil {
				return outbound.PrivateMessagePayload{}, err
			}
			result.Attachments = append(result.Attachments, outbound.PrivateMessageAttachment{Kind: "image", Content: variant.Content, FileName: imageFileName(variant.MediaType), MediaType: variant.MediaType})
		case aiassistantport.ContentMiniProgram:
			item, e := a.materials.MiniProgram(ctx, block.MaterialID)
			if e != nil {
				return outbound.PrivateMessagePayload{}, e
			}
			thumb, ok := number(item["thumb_image_id"])
			if !ok {
				return outbound.PrivateMessagePayload{}, errors.New("mini program thumbnail unavailable")
			}
			if a.mediaPreparationEnabled() {
				source, sourceErr := a.sources.GetSourceSnapshot(ctx, "image:"+strconv.FormatInt(thumb, 10))
				if sourceErr != nil {
					return outbound.PrivateMessagePayload{}, sourceErr
				}
				miniDigest := canonicalFrozenSourceDigest(struct {
					AppID, PagePath, Title, ThumbDigest string
					ThumbID                             int64
				}{text(item["appid"]), text(item["pagepath"]), text(item["title"]), sourceSnapshotDigest(source), thumb})
				if !frozenSourceMatches(block.MaterialDigest, miniDigest) {
					return outbound.PrivateMessagePayload{}, errors.New("frozen mini program material drift")
				}
				if err := a.verifyFrozenMaterial(ctx, block); err != nil {
					return outbound.PrivateMessagePayload{}, err
				}
				mediaID, prepareErr := a.readyMedia(ctx, source)
				if prepareErr != nil {
					return outbound.PrivateMessagePayload{}, prepareErr
				}
				result.Attachments = append(result.Attachments, outbound.PrivateMessageAttachment{Kind: "mini_program", MediaID: mediaID, AppID: text(item["appid"]), PagePath: text(item["pagepath"]), Title: text(item["title"])})
				continue
			}
			variant, e := a.images.GetImageVariant(ctx, thumb, "original")
			if e != nil {
				return outbound.PrivateMessagePayload{}, e
			}
			miniDigest := canonicalFrozenSourceDigest(struct {
				AppID, PagePath, Title, ThumbDigest string
				ThumbID                             int64
			}{text(item["appid"]), text(item["pagepath"]), text(item["title"]), blobSourceDigest(variant.Content), thumb})
			if !frozenSourceMatches(block.MaterialDigest, miniDigest) {
				return outbound.PrivateMessagePayload{}, errors.New("frozen mini program material drift")
			}
			if err := a.verifyFrozenMaterial(ctx, block); err != nil {
				return outbound.PrivateMessagePayload{}, err
			}
			result.Attachments = append(result.Attachments, outbound.PrivateMessageAttachment{Kind: "mini_program", Content: variant.Content, FileName: imageFileName(variant.MediaType), MediaType: variant.MediaType, AppID: text(item["appid"]), PagePath: text(item["pagepath"]), Title: text(item["title"])})
		case aiassistantport.ContentLink:
			item, e := a.materials.GroupInvite(ctx, block.MaterialID)
			if e != nil {
				return outbound.PrivateMessagePayload{}, e
			}
			var coverID int64
			var coverDigest string
			if coverID, _ = number(item["cover_image_id"]); coverID > 0 {
				cover, coverErr := a.images.GetImageVariant(ctx, coverID, "original")
				if coverErr != nil {
					return outbound.PrivateMessagePayload{}, coverErr
				}
				coverDigest = blobSourceDigest(cover.Content)
			}
			inviteDigest := canonicalFrozenSourceDigest(struct {
				Title, Description, URL string
				CoverID                 int64
				CoverDigest             string
			}{text(item["title"]), text(item["description"]), text(item["join_url"]), coverID, coverDigest})
			if !frozenSourceMatches(block.MaterialDigest, inviteDigest) {
				return outbound.PrivateMessagePayload{}, errors.New("frozen group invite material drift")
			}
			if err := a.verifyFrozenMaterial(ctx, block); err != nil {
				return outbound.PrivateMessagePayload{}, err
			}
			result.Attachments = append(result.Attachments, outbound.PrivateMessageAttachment{Kind: "link", Title: text(item["title"]), Description: text(item["description"]), URL: text(item["join_url"])})
		case aiassistantport.ContentAttachment:
			if a.mediaPreparationEnabled() {
				mediaID, e := a.readyFrozenMedia(ctx, block, "attachment:"+strconv.FormatInt(block.MaterialID, 10), block.MaterialDigest)
				if e != nil {
					return outbound.PrivateMessagePayload{}, e
				}
				result.Attachments = append(result.Attachments, outbound.PrivateMessageAttachment{Kind: "file", MediaID: mediaID})
				continue
			}
			if a.attachments == nil {
				return outbound.PrivateMessagePayload{}, errors.New("attachment reader unavailable")
			}
			metadata, content, e := a.attachments.Attachment(ctx, block.MaterialID)
			if e != nil || !enabled(metadata) || text(metadata["mime_type"]) != "application/pdf" || len(content) == 0 {
				return outbound.PrivateMessagePayload{}, errors.New("PDF attachment unavailable")
			}
			if !frozenSourceMatches(block.MaterialDigest, blobSourceDigest(content)) {
				return outbound.PrivateMessagePayload{}, errors.New("frozen PDF material drift")
			}
			if err := a.verifyFrozenMaterial(ctx, block); err != nil {
				return outbound.PrivateMessagePayload{}, err
			}
			result.Attachments = append(result.Attachments, outbound.PrivateMessageAttachment{Kind: "file", Content: content, FileName: text(metadata["file_name"]), MediaType: text(metadata["mime_type"])})
		default:
			return outbound.PrivateMessagePayload{}, errors.New("unsupported content kind")
		}
	}
	result.Text = strings.TrimSpace(result.Text)
	return result, nil
}

// PreparePrivateMessageMedia is called by Outbound before its EER attempt.
// It only reads immutable Media facts and queues missing credentials through
// MaterialPreparer; it never performs a provider write or returns bytes.
func (a aiPrivatePayloadReader) PreparePrivateMessageMedia(ctx context.Context, reference string, digest effectport.Digest) error {
	if a.content == nil {
		return errors.New("media preparation unavailable")
	}
	content, err := a.content.LoadOutboundContent(ctx, reference, digest)
	if err != nil {
		return err
	}
	needsPreparation := false
	for _, block := range content.Blocks {
		if block.ExcelCard != nil || block.Kind == aiassistantport.ContentImage || block.Kind == aiassistantport.ContentMiniProgram || block.Kind == aiassistantport.ContentAttachment {
			needsPreparation = true
			break
		}
	}
	if !needsPreparation {
		return nil
	}
	if !a.mediaPreparationEnabled() {
		return errors.New("media preparation unavailable")
	}
	_, err = a.prepareBlocks(ctx, content.Blocks)
	return err
}

func (a aiPrivatePayloadReader) mediaPreparationEnabled() bool {
	return a.sources != nil && a.preparer != nil && effectport.ValidDigest(effectport.Digest(a.scopeDigest))
}

func (a aiPrivatePayloadReader) prepareExcelCard(ctx context.Context, card aiassistantport.ExcelCard) (outbound.PrivateMessageAttachment, error) {
	if strings.TrimSpace(card.Title) == "" {
		return outbound.PrivateMessageAttachment{}, outboundport.PayloadPreparationError("title_missing")
	}
	if card.CoverDigest == "" {
		return outbound.PrivateMessageAttachment{}, outboundport.PayloadPreparationError("cover_missing")
	}
	if !card.Valid() {
		return outbound.PrivateMessageAttachment{}, errors.New("excel card unavailable")
	}
	if card.CoverImageID > 0 && a.mediaPreparationEnabled() {
		source, err := a.sources.GetSourceSnapshot(ctx, "image:"+strconv.FormatInt(card.CoverImageID, 10))
		if err != nil {
			return outbound.PrivateMessageAttachment{}, err
		}
		if sourceSnapshotDigest(source) != string(card.CoverDigest) {
			return outbound.PrivateMessageAttachment{}, errors.New("frozen excel cover drift")
		}
		mediaID, err := a.readyMedia(ctx, source)
		if err != nil {
			return outbound.PrivateMessageAttachment{}, err
		}
		return outbound.PrivateMessageAttachment{Kind: "mini_program", MediaID: mediaID, AppID: card.AppID, PagePath: card.Path, Title: card.Title}, nil
	}
	if a.excel == nil || !a.mediaPreparationEnabled() {
		return outbound.PrivateMessageAttachment{}, errors.New("excel card reader unavailable")
	}
	// Historical Python covers remain immutable by their accepted digest, but
	// they use the same preparation cache so multiple recipients never upload
	// the same frozen cover once per message.
	source, err := a.sources.GetSourceSnapshot(ctx, "excel-cover:"+string(card.CoverDigest))
	if err != nil || sourceSnapshotDigest(source) != string(card.CoverDigest) {
		return outbound.PrivateMessageAttachment{}, errors.New("legacy excel cover unavailable")
	}
	mediaID, err := a.readyMedia(ctx, source)
	if err != nil {
		return outbound.PrivateMessageAttachment{}, err
	}
	return outbound.PrivateMessageAttachment{Kind: "mini_program", MediaID: mediaID, AppID: card.AppID, PagePath: card.Path, Title: card.Title}, nil
}

func (a aiPrivatePayloadReader) readyFrozenMedia(ctx context.Context, block aiassistantport.ContentBlock, sourceRef string, expected effectport.Digest) (string, error) {
	source, err := a.sources.GetSourceSnapshot(ctx, sourceRef)
	if err != nil {
		return "", err
	}
	if !frozenSourceMatches(expected, sourceSnapshotDigest(source)) {
		return "", errors.New("frozen material drift")
	}
	if err = a.verifyFrozenMaterial(ctx, block); err != nil {
		return "", err
	}
	return a.readyMedia(ctx, source)
}

func (a aiPrivatePayloadReader) readyMedia(ctx context.Context, source outboundport.MaterialSourceSnapshot) (string, error) {
	result, err := a.preparer.ReadyForSend(ctx, outboundport.MaterialRequest{MaterialSourceSnapshot: source, CorpScopeDigest: a.scopeDigest, ValidThrough: time.Now().UTC().Add(30 * time.Second)})
	if err != nil {
		return "", err
	}
	if result.State == "ready" && strings.TrimSpace(result.MediaID) != "" {
		return result.MediaID, nil
	}
	code := strings.TrimSpace(result.FailureCode)
	if code == "" {
		code = "media_preparation_pending"
	}
	return "", outboundport.MediaPreparationPendingError{Code: code, After: time.Second}
}

func sourceSnapshotDigest(source outboundport.MaterialSourceSnapshot) string {
	return "sha256:" + hex.EncodeToString(source.ContentDigest[:])
}

// A payload is made from the bytes and fields just read from the Media owner.
// Matching their canonical source digest to the immutable intent prevents a
// changed record between an earlier capture and this Provider call from being
// sent as if it were the accepted content.
func frozenSourceMatches(want effectport.Digest, got string) bool {
	return effectport.ValidDigest(want) && string(want) == got
}
func blobSourceDigest(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}
func canonicalFrozenSourceDigest(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return blobSourceDigest(raw)
}
func text(value any) string             { out, _ := value.(string); return out }
func enabled(value map[string]any) bool { out, _ := value["enabled"].(bool); return out }
func imageFileName(mediaType string) string {
	if mediaType == "image/png" {
		return "image.png"
	}
	return "image.jpg"
}
func number(value any) (int64, bool) {
	switch v := value.(type) {
	case int64:
		return v, v > 0
	case int:
		return int64(v), v > 0
	case float64:
		return int64(v), v > 0 && v == float64(int64(v))
	default:
		return 0, false
	}
}

func (a aiPrivateTargetResolver) ResolveDeferredPrivateMessageTarget(ctx context.Context, ref string) (outbound.PrivateMessageTarget, error) {
	if a.deferred == nil || a.resolver == nil || a.trusted == nil {
		return outbound.PrivateMessageTarget{}, errors.New("deferred identity unavailable")
	}
	target, err := a.deferred.LoadDeferredTarget(ctx, ref)
	if err != nil {
		return outbound.PrivateMessageTarget{}, err
	}
	var result outbound.PrivateMessageTarget
	err = a.uow.Within(ctx, func(tx context.Context) error {
		resolved, e := a.resolver.Resolve(tx, identitydomain.Reference{Kind: identitydomain.KindUnionID, Scope: target.Scope, Value: target.UnionID, Assurance: identitydomain.AssuranceDeclared, Source: "aiassistant.excel"})
		if e != nil || resolved.Status != identityport.ResolveFound {
			return outboundport.TargetResolutionError("unionid_not_unique")
		}
		trusted, found, e := a.trusted.VerifiedExternalIdentityValue(tx, resolved.CustomerID, identitydomain.KindUnionID, target.Scope)
		if e != nil || !found || trusted != target.UnionID {
			return outboundport.TargetResolutionError("unionid_unverified")
		}
		external, found, e := a.identities.VerifiedWeComIdentityForCustomer(tx, resolved.CustomerID, a.corpID)
		if e != nil || !found {
			return outboundport.TargetResolutionError("wecom_identity_unavailable")
		}
		// User explicitly selected the sender userid. Provider determines reachability;
		// this import lane does not override it with an owner or precheck follow relations.
		result = outbound.PrivateMessageTarget{ExternalUserID: external, StaffUserID: target.SenderUserID}
		return nil
	})
	return result, err
}
