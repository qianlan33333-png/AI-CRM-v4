package http

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	referralport "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/port"
)

var promotionPath = regexp.MustCompile(`^/d/(dpc_[A-Za-z0-9_-]{16,})$`)

type activityContextIssuer interface {
	IssuePublicProductActivityContext(context.Context, int64) (string, error)
}

func (h *Handler) activitySignature(campaignID int64, token string) string {
	mac := hmac.New(sha256.New, h.activityHandoffKey)
	_, _ = mac.Write([]byte("v1:" + strconv.FormatInt(campaignID, 10) + ":" + token))
	return hex.EncodeToString(mac.Sum(nil))
}

func (h *Handler) signedActivityURL(campaignID int64, raw string) (string, error) {
	if h == nil || len(h.activityHandoffKey) < 32 || campaignID < 1 {
		return "", referralport.ErrUnavailable
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Scheme != "https" {
		return "", referralport.ErrUnavailable
	}
	if _, ok := h.allowedOrigins[parsed.Scheme+"://"+parsed.Host]; !ok {
		return "", referralport.ErrUnavailable
	}
	match := promotionPath.FindStringSubmatch(parsed.Path)
	if len(match) != 2 {
		return "", referralport.ErrUnavailable
	}
	result := url.URL{Scheme: parsed.Scheme, Host: parsed.Host, Path: "/referral/activity/" + strconv.FormatInt(campaignID, 10) + "/" + match[1]}
	params := url.Values{}
	params.Set("sig", h.activitySignature(campaignID, match[1]))
	result.RawQuery = params.Encode()
	return result.String(), nil
}

func (h *Handler) activityHandoff(w http.ResponseWriter, r *http.Request, tail string) {
	if r.Method != http.MethodGet || len(h.activityHandoffKey) < 32 {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	parts := strings.Split(tail, "/")
	if len(parts) != 2 || !promotionPath.MatchString("/d/"+parts[1]) || len(r.URL.Query()) != 1 {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	campaignID, ok := id(parts[0])
	if !ok {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	provided, err := hex.DecodeString(r.URL.Query().Get("sig"))
	expected, _ := hex.DecodeString(h.activitySignature(campaignID, parts[1]))
	if err != nil || !hmac.Equal(provided, expected) {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	issuer, ok := h.public.(activityContextIssuer)
	if !ok {
		resultError(w, referralport.ErrUnavailable)
		return
	}
	token, err := issuer.IssuePublicProductActivityContext(r.Context(), campaignID)
	if err != nil {
		resultError(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "aicrm_referral_activity_context", Value: token, Path: "/", HttpOnly: true, Secure: h.cookieSecure, SameSite: http.SameSiteLaxMode, Expires: time.Now().UTC().Add(24 * time.Hour)})
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, "/d/"+parts[1], http.StatusSeeOther)
}
