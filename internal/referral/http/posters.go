package http

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"

	referralport "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/port"
)

type CampaignPosters interface {
	ListCampaignPosters(context.Context, int64, bool) ([]referralport.CampaignPoster, error)
	ReadCampaignPoster(context.Context, int64, int) (referralport.CampaignPosterImage, error)
	SetCampaignPosters(context.Context, referralport.SetCampaignPostersCommand) ([]referralport.CampaignPoster, error)
	SetCampaignPosterLayouts(context.Context, referralport.SetCampaignPosterLayoutsCommand) (referralport.CampaignPosterLayoutConfiguration, error)
}

func publicPosters(posters []referralport.CampaignPoster, admin bool) []any {
	result := make([]any, 0, len(posters))
	for _, poster := range posters {
		item := map[string]any{"slot": poster.Slot, "description": poster.Description, "share_text": poster.ShareText, "image_url": poster.ImageURL, "image_width": poster.Width, "image_height": poster.Height, "qr_layout": poster.QRLayout}
		if admin {
			item["image_id"] = poster.SourceImageID
		}
		result = append(result, item)
	}
	return result
}

func (h *Handler) setCampaignPosterLayouts(w http.ResponseWriter, r *http.Request, raw string, actor int64) {
	if h.posters == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	campaignID, ok := id(raw)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	var input struct {
		ExpectedVersion int64 `json:"expected_version"`
		Posters         []struct {
			Slot int  `json:"slot"`
			X    *int `json:"x"`
			Y    *int `json:"y"`
			Size *int `json:"size"`
		} `json:"posters"`
	}
	if !decode(w, r, &input) {
		return
	}
	if input.ExpectedVersion < 1 || len(input.Posters) < 1 || len(input.Posters) > 3 {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	updates := make([]referralport.PosterLayoutUpdate, 0, len(input.Posters))
	for _, p := range input.Posters {
		if p.X == nil || p.Y == nil || p.Size == nil {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		updates = append(updates, referralport.PosterLayoutUpdate{Slot: p.Slot, PosterQRLayout: referralport.PosterQRLayout{X: *p.X, Y: *p.Y, Size: *p.Size}})
	}
	key, ok := idempotencyKey(w, r)
	if !ok {
		return
	}
	result, err := h.posters.SetCampaignPosterLayouts(r.Context(), referralport.SetCampaignPosterLayoutsCommand{CampaignID: campaignID, ExpectedVersion: input.ExpectedVersion, ActorAdminID: actor, IdempotencyKey: key, Posters: updates})
	if err != nil {
		resultError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"campaign_id": result.CampaignID, "version": result.Version, "posters": publicPosters(result.Posters, true)})
}

func (h *Handler) publicPoster(w http.ResponseWriter, r *http.Request, campaignID int64, rawSlot string) {
	if r.Method != http.MethodGet || r.URL.RawQuery != "" {
		method(w, http.MethodGet)
		return
	}
	slot, err := strconv.Atoi(rawSlot)
	if err != nil || slot < 1 || slot > 3 || h.posters == nil {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	poster, err := h.posters.ReadCampaignPoster(r.Context(), campaignID, slot)
	if err != nil {
		resultError(w, err)
		return
	}
	digest := sha256.Sum256(poster.Content)
	w.Header().Set("Content-Type", poster.MediaType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "public, max-age=60")
	w.Header().Set("ETag", "\""+hex.EncodeToString(digest[:])+"\"")
	if r.Header.Get("If-None-Match") == w.Header().Get("ETag") {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	_, _ = w.Write(poster.Content)
}

func (h *Handler) setCampaignPosters(w http.ResponseWriter, r *http.Request, raw string, actor int64) {
	if h.posters == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	campaignID, ok := id(raw)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	var input struct {
		ExpectedVersion int64                               `json:"expected_version"`
		Posters         []referralport.CampaignPosterSource `json:"posters"`
	}
	if !decode(w, r, &input) {
		return
	}
	if input.ExpectedVersion < 1 || len(input.Posters) > 3 {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	key, ok := idempotencyKey(w, r)
	if !ok {
		return
	}
	posters, err := h.posters.SetCampaignPosters(r.Context(), referralport.SetCampaignPostersCommand{CampaignID: campaignID, ExpectedVersion: input.ExpectedVersion, ActorAdminID: actor, IdempotencyKey: key, Posters: input.Posters})
	if err != nil {
		resultError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"posters": publicPosters(posters, true)})
}
