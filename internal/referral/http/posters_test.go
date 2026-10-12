package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	referralport "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/port"
)

type posterHTTPStub struct {
	calls       []referralport.SetCampaignPostersCommand
	layoutCalls []referralport.SetCampaignPosterLayoutsCommand
}

func (s *posterHTTPStub) SetCampaignPosterLayouts(_ context.Context, c referralport.SetCampaignPosterLayoutsCommand) (referralport.CampaignPosterLayoutConfiguration, error) {
	s.layoutCalls = append(s.layoutCalls, c)
	return referralport.CampaignPosterLayoutConfiguration{CampaignID: c.CampaignID, Version: c.ExpectedVersion + 1, Posters: []referralport.CampaignPoster{{Slot: 1, Width: 1250, Height: 2426, QRLayout: c.Posters[0].PosterQRLayout}}}, nil
}

func (s *posterHTTPStub) ListCampaignPosters(context.Context, int64, bool) ([]referralport.CampaignPoster, error) {
	return nil, nil
}
func (s *posterHTTPStub) ReadCampaignPoster(context.Context, int64, int) (referralport.CampaignPosterImage, error) {
	return referralport.CampaignPosterImage{}, nil
}
func (s *posterHTTPStub) ReadCampaignPosterMetadata(context.Context, int64, int) (referralport.CampaignPosterMetadata, error) {
	return referralport.CampaignPosterMetadata{}, nil
}
func (s *posterHTTPStub) ReadCampaignPosterSnapshot(context.Context, int64, int, [32]byte) (referralport.CampaignPosterImage, error) {
	return referralport.CampaignPosterImage{}, nil
}
func (s *posterHTTPStub) SetCampaignPosters(_ context.Context, c referralport.SetCampaignPostersCommand) ([]referralport.CampaignPoster, error) {
	s.calls = append(s.calls, c)
	return []referralport.CampaignPoster{{Slot: 1, SourceImageID: c.Posters[0].ImageID, Description: c.Posters[0].Description, ShareText: c.Posters[0].ShareText, ImageURL: "/poster/1"}}, nil
}

func TestCampaignPostersShareTextHTTPContract(t *testing.T) {
	for _, text := range []string{"", "  第一行 🎉\n<b>保留原文</b>  "} {
		s := &posterHTTPStub{}
		h := &Handler{posters: s}
		item := map[string]any{"image_id": 1, "description": "说明"}
		if text != "" {
			item["share_text"] = text
		}
		body, _ := json.Marshal(map[string]any{"expected_version": 2, "posters": []any{item}})
		request := httptest.NewRequest(http.MethodPut, "/api/admin/referral/campaigns/7/posters", strings.NewReader(string(body)))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", "poster-share-text-http-key")
		response := httptest.NewRecorder()
		h.setCampaignPosters(response, request, "7", 9001)
		if response.Code != http.StatusOK || len(s.calls) != 1 || s.calls[0].Posters[0].ShareText != text {
			t.Fatalf("write code=%d calls=%+v body=%s", response.Code, s.calls, response.Body.String())
		}
		var decoded struct {
			Posters []struct {
				ShareText string `json:"share_text"`
			} `json:"posters"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil || len(decoded.Posters) != 1 || decoded.Posters[0].ShareText != text {
			t.Fatalf("readback=%+v err=%v", decoded, err)
		}
	}
	s := &posterHTTPStub{}
	h := &Handler{posters: s}
	request := httptest.NewRequest(http.MethodPut, "/api/admin/referral/campaigns/7/posters", strings.NewReader(`{"expected_version":2,"posters":[{"image_id":1,"share_text":"`+strings.Repeat("a", maxBody)+`"}]}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "poster-share-text-large-key")
	response := httptest.NewRecorder()
	h.setCampaignPosters(response, request, "7", 9001)
	if response.Code != http.StatusBadRequest || len(s.calls) != 0 {
		t.Fatalf("body boundary=%d calls=%d", response.Code, len(s.calls))
	}
	public, _ := json.Marshal(publicPosters([]referralport.CampaignPoster{{Slot: 1, SourceImageID: 42, ShareText: "公开话术"}}, false))
	if strings.Contains(string(public), "image_id") || !strings.Contains(string(public), "share_text") {
		t.Fatalf("public projection=%s", public)
	}
}
