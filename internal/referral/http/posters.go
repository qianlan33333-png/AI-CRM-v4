package http

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	referralport "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/port"
)

type CampaignPosters interface {
	ListCampaignPosters(context.Context, int64, bool) ([]referralport.CampaignPoster, error)
	ReadCampaignPoster(context.Context, int64, int) (referralport.CampaignPosterImage, error)
	ReadCampaignPosterMetadata(context.Context, int64, int) (referralport.CampaignPosterMetadata, error)
	ReadCampaignPosterSnapshot(context.Context, int64, int, [32]byte) (referralport.CampaignPosterImage, error)
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
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet {
		method(w, http.MethodGet)
		return
	}
	slot, err := strconv.Atoi(rawSlot)
	if err != nil || slot < 1 || slot > 3 || h.posters == nil {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	version := query.Get("v")
	if err != nil || len(query) > 1 || (len(query) == 1 && (len(query["v"]) != 1 || !validPosterRevision(version))) {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	metadata, err := h.posters.ReadCampaignPosterMetadata(r.Context(), campaignID, slot)
	if err != nil {
		resultError(w, err)
		return
	}
	revision := referralport.PosterImageRevision(metadata.Digest)
	if version != "" && version != revision {
		w.Header().Set("Cache-Control", "no-store")
		writeError(w, http.StatusConflict, "poster_updated")
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "public, max-age=60")
	// Fallback may return the original encoding of the same pixels.
	w.Header().Set("ETag", "W/\""+revision+"\"")
	if posterETagMatches(r.Header.Get("If-None-Match"), w.Header().Get("ETag")) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	h.posterImagesOnce.Do(func() { h.posterImages = newPosterImageCache() })
	poster, err := h.posterImages.read(r.Context(), posterImageKey{campaign: campaignID, slot: slot, revision: revision}, func(ctx context.Context) (referralport.CampaignPosterImage, error) {
		return h.posters.ReadCampaignPosterSnapshot(ctx, campaignID, slot, metadata.Digest)
	})
	if err != nil {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Del("ETag")
		switch {
		case errors.Is(err, errPosterImageBusy):
			w.Header().Set("Retry-After", "1")
			writeError(w, http.StatusServiceUnavailable, "poster_busy")
		case errors.Is(err, referralport.ErrConflict):
			writeError(w, http.StatusConflict, "poster_updated")
		default:
			resultError(w, err)
		}
		return
	}
	w.Header().Set("Content-Type", poster.MediaType)
	w.Header().Set("Content-Length", strconv.Itoa(len(poster.Content)))
	_, _ = w.Write(poster.Content)
}

func validPosterRevision(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, ch := range value {
		if !(ch >= '0' && ch <= '9') && !(ch >= 'a' && ch <= 'f') {
			return false
		}
	}
	return true
}

func posterETagMatches(header, etag string) bool {
	for _, value := range strings.Split(header, ",") {
		value = strings.TrimSpace(value)
		if value == "*" || strings.TrimPrefix(value, "W/") == strings.TrimPrefix(etag, "W/") {
			return true
		}
	}
	return false
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
