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
}

func publicPosters(posters []referralport.CampaignPoster, admin bool) []any {
	result := make([]any, 0, len(posters))
	for _, poster := range posters {
		item := map[string]any{"slot": poster.Slot, "description": poster.Description, "image_url": poster.ImageURL}
		if admin {
			item["image_id"] = poster.SourceImageID
		}
		result = append(result, item)
	}
	return result
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
