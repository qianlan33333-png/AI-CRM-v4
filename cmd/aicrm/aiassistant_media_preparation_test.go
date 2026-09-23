package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	aiassistantport "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/port"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	outbound "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound"
	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
)

type legacyExcelReaderStub struct{ content []byte }

func (s legacyExcelReaderStub) LoadExcelCard(_ context.Context, card aiassistantport.ExcelCard) (outbound.PrivateMessageAttachment, error) {
	return outbound.PrivateMessageAttachment{Kind: "mini_program", Content: s.content, FileName: "cover.png", MediaType: "image/png", AppID: card.AppID, PagePath: card.Path, Title: card.Title}, nil
}

type legacyPreparedStub struct {
	calls []outboundport.MaterialRequest
}

func (s *legacyPreparedStub) Prepare(context.Context, outboundport.MaterialRequest) (outboundport.MaterialResult, error) {
	return outboundport.MaterialResult{}, errors.New("not used")
}
func (s *legacyPreparedStub) ReadyForSend(_ context.Context, request outboundport.MaterialRequest) (outboundport.MaterialResult, error) {
	s.calls = append(s.calls, request)
	return outboundport.MaterialResult{State: "ready", EffectID: "eer_shared", MediaID: "shared-provider-id", ExpiresAt: time.Now().Add(time.Hour)}, nil
}

type preparedAIContentStub struct {
	content aiassistantport.ContentVersion
}

func (s preparedAIContentStub) LoadOutboundContent(context.Context, string, effectport.Digest) (aiassistantport.ContentVersion, error) {
	return s.content, nil
}

type preparedAISourceStub struct {
	source outboundport.MaterialSourceSnapshot
}

func (s preparedAISourceStub) GetSourceSnapshot(_ context.Context, ref string) (outboundport.MaterialSourceSnapshot, error) {
	if ref != s.source.SourceRef {
		return outboundport.MaterialSourceSnapshot{}, errors.New("wrong source")
	}
	return s.source, nil
}
func (preparedAISourceStub) ListEnabledSourceSnapshots(context.Context, outboundport.MaterialSnapshotPageRequest) (outboundport.MaterialSnapshotPage, error) {
	return outboundport.MaterialSnapshotPage{}, errors.New("not used")
}
func (preparedAISourceStub) ReadSourceBytes(context.Context, outboundport.MaterialSourceSnapshot) (outboundport.MaterialSourceContent, error) {
	return outboundport.MaterialSourceContent{}, errors.New("must not read bytes")
}

type preparedAIPreparerStub struct {
	calls  int
	result outboundport.MaterialResult
}

func (s *preparedAIPreparerStub) ReadyForSend(_ context.Context, req outboundport.MaterialRequest) (outboundport.MaterialResult, error) {
	s.calls++
	if req.SourceRef != "image:41" || req.CorpScopeDigest == "" {
		return outboundport.MaterialResult{}, errors.New("bad material request")
	}
	return s.result, nil
}
func (s *preparedAIPreparerStub) Prepare(context.Context, outboundport.MaterialRequest) (outboundport.MaterialResult, error) {
	return outboundport.MaterialResult{}, errors.New("not used")
}

type preparedAICapturerStub struct{ digest string }

func (s preparedAICapturerStub) CaptureGroupOpsMaterialSources(_ context.Context, plan mediaport.GroupOpsMaterialPlan) (mediaport.GroupOpsMaterialSourceSnapshot, error) {
	if len(plan.References) != 1 || plan.References[0].Kind != "image" || plan.References[0].ID != 41 {
		return mediaport.GroupOpsMaterialSourceSnapshot{}, errors.New("bad capture")
	}
	return mediaport.GroupOpsMaterialSourceSnapshot{SchemaVersion: 1, References: []mediaport.GroupOpsMaterialSourceReference{{Reference: plan.References[0], SourceDigest: s.digest}}}, nil
}

func TestAIPrivatePayloadUsesPreparedMediaIDWithoutReadingBytes(t *testing.T) {
	digest := [32]byte{1}
	digestText := "sha256:" + hex.EncodeToString(digest[:])
	source := outboundport.MaterialSourceSnapshot{SourceRef: "image:41", SourceType: "image", ContentDigest: digest, FileName: "cover.png", MediaType: "image/png", SizeBytes: 10, SnapshotVersion: 3}
	preparer := &preparedAIPreparerStub{result: outboundport.MaterialResult{State: "ready", MediaID: "prepared-image-id"}}
	reader := aiPrivatePayloadReader{content: preparedAIContentStub{content: aiassistantport.ContentVersion{Blocks: []aiassistantport.ContentBlock{{Kind: aiassistantport.ContentImage, MaterialKind: "image", MaterialID: 41, MaterialDigest: effectport.Digest(digestText)}}}}, sources: preparedAISourceStub{source: source}, preparer: preparer, scopeDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", uow: directUnitOfWork{}, capturer: preparedAICapturerStub{digest: digestText}}
	if err := reader.PreparePrivateMessageMedia(context.Background(), "recipient:1", effectport.Hash("payload")); err != nil {
		t.Fatal(err)
	}
	payload, err := reader.LoadPrivateMessagePayload(context.Background(), "recipient:1", effectport.Hash("payload"))
	if err != nil {
		t.Fatal(err)
	}
	if len(payload.Attachments) != 1 || payload.Attachments[0].MediaID != "prepared-image-id" || len(payload.Attachments[0].Content) != 0 || preparer.calls != 2 {
		t.Fatalf("payload=%+v calls=%d", payload, preparer.calls)
	}
}

func TestAIPrivatePayloadReturnsTypedPendingUntilPrepared(t *testing.T) {
	digest := [32]byte{1}
	digestText := "sha256:" + hex.EncodeToString(digest[:])
	source := outboundport.MaterialSourceSnapshot{SourceRef: "image:41", SourceType: "image", ContentDigest: digest, FileName: "cover.png", MediaType: "image/png", SizeBytes: 10, SnapshotVersion: 3}
	reader := aiPrivatePayloadReader{content: preparedAIContentStub{content: aiassistantport.ContentVersion{Blocks: []aiassistantport.ContentBlock{{Kind: aiassistantport.ContentImage, MaterialKind: "image", MaterialID: 41, MaterialDigest: effectport.Digest(digestText)}}}}, sources: preparedAISourceStub{source: source}, preparer: &preparedAIPreparerStub{result: outboundport.MaterialResult{State: "queued"}}, scopeDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", uow: directUnitOfWork{}, capturer: preparedAICapturerStub{digest: digestText}}
	err := reader.PreparePrivateMessageMedia(context.Background(), "recipient:1", effectport.Hash("payload"))
	var pending outboundport.MediaPreparationPendingError
	if !errors.As(err, &pending) || pending.RetryAfter() != time.Second {
		t.Fatalf("err=%v pending=%+v", err, pending)
	}
}

func TestAudienceDirectPushUsesFrozenMiniProgramFields(t *testing.T) {
	thumbnailDigest := [32]byte{2}
	thumbnailDigestText := "sha256:" + hex.EncodeToString(thumbnailDigest[:])
	source := outboundport.MaterialSourceSnapshot{SourceRef: "image:41", SourceType: "image", ContentDigest: thumbnailDigest, FileName: "cover.png", MediaType: "image/png", SizeBytes: 10, SnapshotVersion: 3}
	preparer := &preparedAIPreparerStub{result: outboundport.MaterialResult{State: "ready", MediaID: "frozen-cover-id"}}
	snapshot := frozenAutomationContent{SchemaVersion: 1, ContentText: "冻结话术", Sources: []frozenAutomationMaterialSource{{Kind: "miniprogram", ID: 128, SourceDigest: "sha256:" + strings.Repeat("a", 64), Name: "课程卡", Version: 7, AppID: "wx-frozen", PagePath: "pages/frozen", Title: "冻结标题", ThumbnailImageID: 41, ThumbnailSourceDigest: thumbnailDigestText}}, ObservationPath: "pages/frozen"}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	reader := automationFrozenPayloadReader{preparer: aiPrivatePayloadReader{sources: preparedAISourceStub{source: source}, preparer: preparer, scopeDigest: "sha256:" + strings.Repeat("b", 64)}}
	payload, err := reader.LoadFrozenAutomationMessagePayload(context.Background(), raw, sha256.Sum256(raw))
	if err != nil {
		t.Fatal(err)
	}
	if payload.Text != "冻结话术" || len(payload.Attachments) != 1 || payload.Attachments[0].AppID != "wx-frozen" || payload.Attachments[0].PagePath != "pages/frozen" || payload.Attachments[0].Title != "冻结标题" || payload.Attachments[0].MediaID != "frozen-cover-id" {
		t.Fatalf("payload=%+v", payload)
	}
}

func TestLegacyExcelRecipientsUseOneDigestAddressedPreparation(t *testing.T) {
	content := []byte("legacy frozen cover")
	digest := sha256.Sum256(content)
	digestText := effectport.Digest("sha256:" + hex.EncodeToString(digest[:]))
	excel := legacyExcelReaderStub{content: content}
	sources := excelMaterialSources{excel: excel}
	preparer := &legacyPreparedStub{}
	reader := aiPrivatePayloadReader{excel: excel, sources: sources, preparer: preparer, scopeDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	card := aiassistantport.ExcelCard{AppID: "wx-app", Path: "pages/card", Title: "Title", CoverDigest: digestText}
	first, err := reader.prepareExcelCard(context.Background(), card)
	if err != nil {
		t.Fatal(err)
	}
	second, err := reader.prepareExcelCard(context.Background(), card)
	if err != nil {
		t.Fatal(err)
	}
	if first.MediaID != "shared-provider-id" || second.MediaID != first.MediaID || len(first.Content) != 0 || len(second.Content) != 0 || len(preparer.calls) != 2 {
		t.Fatalf("first=%+v second=%+v calls=%d", first, second, len(preparer.calls))
	}
	wantRef := "excel-cover:" + string(digestText)
	if preparer.calls[0].SourceRef != wantRef || preparer.calls[1].SourceRef != wantRef || preparer.calls[0].ContentDigest != digest || preparer.calls[1].ContentDigest != digest {
		t.Fatalf("legacy source drift: %+v", preparer.calls)
	}
}

var _ outboundport.PrivateMessageMediaPreflighter = aiPrivatePayloadReader{}
var _ outbound.PrivateMessagePayloadReader = aiPrivatePayloadReader{}
