package excel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	ai "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/port"
	effect "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	outbound "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	"testing"
)

func TestMissingTitleOrCoverNeverLoadsMedia(t *testing.T) {
	// An unusable URL proves validation occurs before any network request.
	client := &Client{Base: "invalid"}
	for _, test := range []struct{ title, cover, code string }{
		{"", "", "title_missing"}, {"  ", "", "title_missing"}, {"Excel title", "", "cover_missing"},
	} {
		_, err := client.LoadExcelCard(context.Background(), ai.ExcelCard{AppID: "app", Path: "pages/a/a", Title: test.title})
		coded, ok := err.(outbound.PayloadPreparationError)
		if !ok || coded.FailureCode() != test.code {
			t.Fatalf("missing content did not fail safely: %v", err)
		}
	}
}

type excelCoverReaderStub struct{ content mediaport.SourceContent }

func (s excelCoverReaderStub) ReadExcelCover(_ context.Context, _ int64, _ [32]byte) (mediaport.SourceContent, error) {
	return s.content, nil
}

func TestMediaBackedExcelCoverReadsFrozenMediaBytes(t *testing.T) {
	// A minimal PNG signature is sufficient for the payload shape check; the
	// source reader itself owns the full frozen-byte/digest CAS validation.
	raw := []byte{137, 80, 78, 71, 13, 10, 26, 10, 0, 0, 0, 0}
	digest := sha256.Sum256(raw)
	client := &Client{MediaCovers: excelCoverReaderStub{content: mediaport.SourceContent{Bytes: raw, FileName: "historic.png", MediaType: "image/png"}}}
	attachment, err := client.LoadExcelCard(context.Background(), ai.ExcelCard{AppID: "app", Path: "pages/a/a", Title: "Excel title", CoverImageID: 42, CoverDigest: effect.Digest("sha256:" + hex.EncodeToString(digest[:]))})
	if err != nil || attachment.Kind != "mini_program" || string(attachment.Content) != string(raw) {
		t.Fatalf("attachment=%+v err=%v", attachment, err)
	}
}
