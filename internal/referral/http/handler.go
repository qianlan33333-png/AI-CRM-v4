// Package http exposes Referral's public, trusted-session boundary. It never
// accepts a customer identity from HTTP: the actor comes exclusively from the
// existing verified WeChat browser session.
package http

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"errors"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	distributiondomain "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/domain"
	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
	referraldomain "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/domain"
	referralport "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/port"
)

const (
	publicPrefix = "/api/v1/referral"
	maxBody      = 32 << 10
)

type SessionResolver interface {
	Resolve(context.Context, string) (distributionport.TrustedSessionActor, error)
}

type PromotionIssuer interface {
	// The issuer must derive the distributor and product qualification from
	// the trusted actor and server-side campaign target.
	IssuePromotionLink(context.Context, distributionport.IssuePromotionCommand) (distributionport.PromotionLink, error)
}

// PaymentSessionBridge is composition-owned and reads the one-time trusted
// Payment session only on the server before minting the shared browser session.
type PaymentSessionBridge interface {
	BridgePaymentSession(context.Context, string) (string, time.Time, error)
}

type RequestSecurity interface {
	Authenticate(context.Context, *http.Request) (accessdomain.Principal, error)
	AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error)
}

type Config struct {
	ActivityHandoffKey string
	ProductOptions     productport.ProductOptionReader
	ProductTargets     productport.ProductTargetReader
	Public             referralport.PublicApplication
	Promotion          PromotionIssuer
	Admin              referralport.AdminApplication
	Posters            CampaignPosters
	Sessions           SessionResolver
	Bridge             PaymentSessionBridge
	Names              customerport.DirectoryDisplayNameReader
	Profiles           customerport.DirectoryPublicProfileReader
	Security           RequestSecurity
	CookieSecure       bool
	AllowedOrigins     []string
	SessionCookieName  string
	CSRFCookieName     string
	CSRFHeader         string
}

type Handler struct {
	activityHandoffKey                            []byte
	productOptions                                productport.ProductOptionReader
	productTargets                                productport.ProductTargetReader
	public                                        referralport.PublicApplication
	promotion                                     PromotionIssuer
	admin                                         referralport.AdminApplication
	posters                                       CampaignPosters
	sessions                                      SessionResolver
	bridge                                        PaymentSessionBridge
	names                                         customerport.DirectoryDisplayNameReader
	profiles                                      customerport.DirectoryPublicProfileReader
	security                                      RequestSecurity
	cookieSecure                                  bool
	allowedOrigins                                map[string]struct{}
	sessionCookieName, csrfCookieName, csrfHeader string
}

func NewHandler(config Config) (*Handler, error) {
	if config.Public == nil || config.Admin == nil || config.Sessions == nil || config.Bridge == nil || config.Names == nil || config.Security == nil || config.SessionCookieName == "" || config.CSRFCookieName == "" || config.CSRFHeader == "" {
		return nil, referralport.ErrUnavailable
	}
	allowed := make(map[string]struct{}, len(config.AllowedOrigins))
	for _, raw := range config.AllowedOrigins {
		origin, ok := canonicalOrigin(raw)
		if !ok {
			return nil, referralport.ErrUnavailable
		}
		allowed[origin] = struct{}{}
	}
	if len(allowed) == 0 {
		return nil, referralport.ErrUnavailable
	}
	return &Handler{activityHandoffKey: []byte(config.ActivityHandoffKey), productOptions: config.ProductOptions, productTargets: config.ProductTargets, public: config.Public, promotion: config.Promotion, admin: config.Admin, posters: config.Posters, sessions: config.Sessions, bridge: config.Bridge, names: config.Names, profiles: config.Profiles, security: config.Security, cookieSecure: config.CookieSecure, allowedOrigins: allowed, sessionCookieName: config.SessionCookieName, csrfCookieName: config.CSRFCookieName, csrfHeader: config.CSRFHeader}, nil
}

// SetPromotionApplication wires Distribution after both domains have been
// composed while keeping the dependency on its stable Public port and
// preventing Referral from minting or inspecting Distribution credentials.
func (h *Handler) SetPromotionApplication(application PromotionIssuer) error {
	if h == nil || application == nil {
		return referralport.ErrUnavailable
	}
	h.promotion = application
	return nil
}

func (h *Handler) ServePublicHTTP(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.public == nil || h.sessions == nil || h.bridge == nil || h.names == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	path := strings.TrimSuffix(r.URL.Path, "/")
	switch {
	case strings.HasPrefix(path, "/referral/invite/"):
		h.invitationHandoff(w, r, strings.TrimPrefix(path, "/referral/invite/"))
	case strings.HasPrefix(path, "/referral/activity/"):
		h.activityHandoff(w, r, strings.TrimPrefix(path, "/referral/activity/"))
	case path == publicPrefix+"/session/bridge":
		h.bridgeSession(w, r)
	case path == publicPrefix+"/campaigns":
		h.campaigns(w, r)
	case strings.HasPrefix(path, publicPrefix+"/invitations/"):
		h.invitationPreview(w, r, strings.TrimPrefix(path, publicPrefix+"/invitations/"))
	case strings.HasPrefix(path, publicPrefix+"/campaigns/"):
		h.campaignTail(w, r, strings.TrimPrefix(path, publicPrefix+"/campaigns/"))
	default:
		writeError(w, http.StatusNotFound, "not_found")
	}
}

func (h *Handler) ServeAdminHTTP(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.admin == nil || h.security == nil || h.names == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	tail := strings.Trim(strings.TrimPrefix(strings.TrimSuffix(r.URL.Path, "/"), "/api/admin/referral"), "/")
	if r.URL.Path != "/api/admin/referral" && !strings.HasPrefix(r.URL.Path, "/api/admin/referral/") {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	write := r.Method != http.MethodGet && r.Method != http.MethodHead
	principal, ok := h.authorizeAdmin(w, r, write)
	if !ok {
		return
	}
	actor := principal.InternalID
	parts := split(tail)
	switch {
	case r.Method == http.MethodGet && tail == "product-options":
		h.listProductOptions(w, r)
	case r.Method == http.MethodGet && tail == "campaigns":
		h.listAdminCampaigns(w, r)
	case r.Method == http.MethodPost && tail == "campaigns":
		h.createCampaign(w, r, actor)
	case r.Method == http.MethodGet && tail == "referrals":
		h.listAdminReferrals(w, r)
	case r.Method == http.MethodGet && tail == "relationship-history":
		h.listHistory(w, r)
	case r.Method == http.MethodGet && tail == "rewards":
		h.listRewards(w, r)
	case len(parts) == 2 && parts[0] == "campaigns" && r.Method == http.MethodGet:
		h.readAdminCampaign(w, r, parts[1])
	case len(parts) == 2 && parts[0] == "campaigns" && r.Method == http.MethodPut:
		h.updateCampaign(w, r, parts[1], actor)
	case len(parts) == 3 && parts[0] == "campaigns" && parts[2] == "posters" && r.Method == http.MethodPut:
		h.setCampaignPosters(w, r, parts[1], actor)
	case len(parts) == 3 && parts[0] == "campaigns" && parts[2] == "state" && r.Method == http.MethodPost:
		h.setCampaignState(w, r, parts[1], actor)
	case len(parts) == 3 && parts[0] == "campaigns" && parts[2] == "teams" && r.Method == http.MethodPost:
		h.createTeam(w, r, parts[1], actor)
	case len(parts) == 3 && parts[0] == "campaigns" && parts[2] == "participants" && r.Method == http.MethodGet:
		h.listAdminParticipants(w, r, parts[1])
	case len(parts) == 3 && parts[0] == "campaigns" && parts[2] == "invitations" && r.Method == http.MethodGet:
		h.listAdminInvitations(w, r, parts[1])
	case len(parts) == 3 && parts[0] == "campaigns" && parts[2] == "export" && r.Method == http.MethodGet:
		h.exportAdminCampaign(w, r, parts[1])
	case len(parts) == 5 && parts[0] == "campaigns" && parts[2] == "participants" && parts[4] == "invitations" && r.Method == http.MethodGet:
		h.listAdminParticipantInvitations(w, r, parts[1], parts[3])
	case len(parts) == 3 && parts[0] == "participations" && parts[2] == "reverse" && r.Method == http.MethodPost:
		h.reverseInvitation(w, r, parts[1], actor)
	case len(parts) == 3 && parts[0] == "invitations" && parts[2] == "revoke" && r.Method == http.MethodPost:
		h.revokeInvitation(w, r, parts[1], actor)
	case r.Method == http.MethodPost && tail == "rewards":
		h.recordReward(w, r, actor)
	default:
		writeError(w, http.StatusNotFound, "not_found")
	}
}

func (h *Handler) campaigns(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || r.URL.RawQuery != "" {
		method(w, http.MethodGet)
		return
	}
	items, err := h.public.ListPublicCampaigns(r.Context())
	if err != nil {
		resultError(w, err)
		return
	}
	result := make([]any, 0, len(items))
	for _, item := range items {
		response := campaignSummary(item)
		h.addPublicCampaignLinks(r.Context(), response, item.Campaign)
		result = append(result, response)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": result})
}

func (h *Handler) campaignTail(w http.ResponseWriter, r *http.Request, tail string) {
	parts := split(tail)
	if len(parts) == 0 {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	campaignID, ok := id(parts[0])
	if !ok {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if len(parts) == 1 {
		if r.Method != http.MethodGet || r.URL.RawQuery != "" {
			method(w, http.MethodGet)
			return
		}
		view, err := h.public.ReadPublicCampaign(r.Context(), campaignID)
		if err != nil {
			resultError(w, err)
			return
		}
		response := campaignView(view)
		if h.posters != nil {
			posters, err := h.posters.ListCampaignPosters(r.Context(), campaignID, false)
			if err != nil {
				resultError(w, err)
				return
			}
			response["posters"] = publicPosters(posters, false)
		}
		h.addPublicCampaignLinks(r.Context(), response, view.Campaign)
		writeJSON(w, http.StatusOK, response)
		return
	}
	if len(parts) == 3 && parts[1] == "posters" {
		h.publicPoster(w, r, campaignID, parts[2])
		return
	}
	if len(parts) != 2 {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	switch parts[1] {
	case "me":
		h.myCampaign(w, r, campaignID)
	case "invitations":
		h.myInvites(w, r, campaignID)
	case "leaderboard":
		h.leaderboard(w, r, campaignID)
	case "invite":
		h.issueInvitation(w, r, campaignID)
	case "promotion-link":
		h.issuePromotionLink(w, r, campaignID)
	case "product-context":
		h.productContext(w, r, campaignID)
	case "participations":
		h.joinCampaign(w, r, campaignID)
	default:
		writeError(w, http.StatusNotFound, "not_found")
	}
}

// issuePromotionLink bridges a paid referral campaign to Distribution's
// server-issued promotion credential. Referral owns the campaign and
// participation checks; Distribution owns the credential, qualification and
// attribution semantics. The browser never supplies a product or distributor
// identity.
func (h *Handler) issuePromotionLink(w http.ResponseWriter, r *http.Request, campaignID int64) {
	if r.Method != http.MethodPost || r.URL.RawQuery != "" || r.ContentLength > 0 {
		method(w, http.MethodPost)
		return
	}
	if h.promotion == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	actor, ok := h.sessionActor(w, r)
	if !ok {
		return
	}
	key, ok := idempotencyKey(w, r)
	if !ok {
		return
	}
	view, err := h.public.ReadPublicCampaign(r.Context(), campaignID)
	if err != nil {
		resultError(w, err)
		return
	}
	campaign := view.Campaign
	config := campaign.Config()
	if config.QualificationMode != referraldomain.QualificationProductPurchase || !campaign.AcceptingAt(time.Now().UTC()) {
		resultError(w, referralport.ErrCampaignUnavailable)
		return
	}
	mine, err := h.public.MyCampaign(r.Context(), actor, campaignID)
	if err != nil {
		resultError(w, err)
		return
	}
	if mine.Participation == nil || mine.Participation.State != referraldomain.ParticipationActive {
		resultError(w, referralport.ErrParticipationRequired)
		return
	}
	link, err := h.promotion.IssuePromotionLink(r.Context(), distributionport.IssuePromotionCommand{
		Actor: actor, ProductID: config.ProductID, ProductType: distributiondomain.ProductType(config.ProductType), IdempotencyKey: key,
	})
	if err != nil {
		resultError(w, err)
		return
	}
	wrapped, err := h.signedActivityURL(campaignID, link.URL)
	if err != nil {
		resultError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"url": wrapped, "expires_at": link.ExpiresAt.UTC()})
}

func (h *Handler) productContext(w http.ResponseWriter, r *http.Request, campaignID int64) {
	if r.Method != http.MethodPost || r.URL.RawQuery != "" || r.ContentLength > 0 {
		method(w, http.MethodPost)
		return
	}
	actor, ok := h.sessionActor(w, r)
	if !ok {
		return
	}
	key, ok := idempotencyKey(w, r)
	if !ok {
		return
	}
	token, err := h.public.IssueProductActivityContext(r.Context(), actor, campaignID, key)
	if err != nil {
		resultError(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "aicrm_referral_activity_context", Value: token, Path: "/", HttpOnly: true, Secure: h.cookieSecure, SameSite: http.SameSiteLaxMode, Expires: time.Now().UTC().Add(24 * time.Hour)})
	writeJSON(w, http.StatusCreated, map[string]any{"expires_at": time.Now().UTC().Add(24 * time.Hour)})
}

func (h *Handler) invitationHandoff(w http.ResponseWriter, r *http.Request, token string) {
	if r.Method != http.MethodGet || r.URL.RawQuery != "" || !validInvitationToken(token) {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	preview, err := h.public.PreviewInvitation(r.Context(), token)
	if err != nil {
		resultError(w, err)
		return
	}
	http.Redirect(w, r, "/referral?campaign="+strconv.FormatInt(preview.Campaign.Campaign.ID, 10)+"&invite="+token, http.StatusSeeOther)
}

func (h *Handler) invitationPreview(w http.ResponseWriter, r *http.Request, token string) {
	if r.Method != http.MethodGet || r.URL.RawQuery != "" || !validInvitationToken(token) {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	preview, err := h.public.PreviewInvitation(r.Context(), token)
	if err != nil {
		resultError(w, err)
		return
	}
	profile, err := h.publicProfile(r.Context(), customerdomain.CustomerID(preview.InviterCustomerID))
	if err != nil {
		resultError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"campaign": campaignSummary(preview.Campaign), "inviter_display_name": profile.DisplayName, "inviter_avatar_url": profile.AvatarURL, "inviter_team": publicTeam(preview.InviterTeam), "expires_at": preview.ExpiresAt.UTC()})
}

func (h *Handler) bridgeSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		method(w, http.MethodPost)
		return
	}
	if r.URL.RawQuery != "" || r.ContentLength > 0 {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	// This is the bootstrap step for a first-time trusted WeChat visitor: the
	// HttpOnly Payment session is the server-issued proof and no Referral (or
	// Distribution) CSRF cookie exists yet. Keep the origin check strict, then
	// mint the CSRF cookie used by every later Referral mutation.
	if !h.sameOrigin(w, r) {
		return
	}
	paymentCookie, err := r.Cookie("aicrm_payment_session")
	if err != nil || paymentCookie.Value == "" {
		writeError(w, http.StatusUnauthorized, "payment_session_required")
		return
	}
	token, expiresAt, err := h.bridge.BridgePaymentSession(r.Context(), paymentCookie.Value)
	if err != nil {
		if errors.Is(err, distributionport.ErrUnauthorized) {
			writeError(w, http.StatusUnauthorized, "payment_session_required")
			return
		}
		resultError(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: h.sessionCookieName, Value: token, Path: "/", HttpOnly: true, Secure: h.cookieSecure, SameSite: http.SameSiteLaxMode, Expires: expiresAt.UTC(), MaxAge: int(time.Until(expiresAt).Seconds())})
	csrf, err := randomToken()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: h.csrfCookieName, Value: csrf, Path: "/", Secure: h.cookieSecure, SameSite: http.SameSiteStrictMode, Expires: expiresAt.UTC(), MaxAge: int(time.Until(expiresAt).Seconds())})
	writeJSON(w, http.StatusCreated, map[string]any{"expires_at": expiresAt.UTC()})
}

func (h *Handler) myCampaign(w http.ResponseWriter, r *http.Request, campaignID int64) {
	if r.Method != http.MethodGet || r.URL.RawQuery != "" {
		method(w, http.MethodGet)
		return
	}
	actor, ok := h.sessionActor(w, r)
	if !ok {
		return
	}
	value, err := h.public.MyCampaign(r.Context(), actor, campaignID)
	if err != nil {
		resultError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, myCampaign(value))
}
func (h *Handler) myInvites(w http.ResponseWriter, r *http.Request, campaignID int64) {
	if r.Method != http.MethodGet {
		method(w, http.MethodGet)
		return
	}
	cursor, limit, ok := pageQuery(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	actor, ok := h.sessionActor(w, r)
	if !ok {
		return
	}
	value, err := h.public.ListMyInvites(r.Context(), actor, campaignID, cursor, limit)
	if err != nil {
		resultError(w, err)
		return
	}
	profiles, err := h.publicProfiles(r.Context(), participantIDs(value.Items))
	if err != nil {
		resultError(w, err)
		return
	}
	items := make([]any, 0, len(value.Items))
	for _, item := range value.Items {
		profile := profiles[customerdomain.CustomerID(item.Participation.CustomerID)]
		items = append(items, map[string]any{"display_name": profile.DisplayName, "avatar_url": profile.AvatarURL, "joined_at": item.Participation.JoinedAt.UTC(), "status": item.ScoreState, "score_delta": item.ScoreDelta})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": value.NextCursor})
}
func (h *Handler) leaderboard(w http.ResponseWriter, r *http.Request, campaignID int64) {
	if r.Method != http.MethodGet {
		method(w, http.MethodGet)
		return
	}
	query, ok := leaderboardQuery(r, campaignID)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if actor, found := h.optionalSessionActor(r); found {
		query.ViewerCustomerID = actor.CustomerID
		// A browser never supplies its own team. Resolve it from the immutable
		// participation so the pinned own-team result is truthful even when the
		// caller asks for a different team board.
		mine, err := h.public.MyCampaign(r.Context(), actor, campaignID)
		if err != nil {
			resultError(w, err)
			return
		}
		if mine.Team != nil {
			query.ViewerTeamID = mine.Team.ID
		}
	}
	value, err := h.public.Leaderboard(r.Context(), query)
	if err != nil {
		resultError(w, err)
		return
	}
	profileIDs := entryIDs(value.Items)
	if value.MyEntry != nil && value.MyEntry.CustomerID > 0 {
		profileIDs = append(profileIDs, customerdomain.CustomerID(value.MyEntry.CustomerID))
	}
	profiles, err := h.publicProfiles(r.Context(), profileIDs)
	if err != nil {
		resultError(w, err)
		return
	}
	items := make([]any, 0, len(value.Items))
	for _, item := range value.Items {
		items = append(items, leaderboardEntry(item, profiles[customerdomain.CustomerID(item.CustomerID)]))
	}
	response := map[string]any{"kind": value.Kind, "period": value.Period, "window_start": value.WindowStart.UTC(), "window_end": value.WindowEnd.UTC(), "items": items, "next_cursor": value.NextCursor}
	if value.MyEntry != nil {
		response["my_entry"] = leaderboardEntry(*value.MyEntry, profiles[customerdomain.CustomerID(value.MyEntry.CustomerID)])
	}
	writeJSON(w, http.StatusOK, response)
}
func (h *Handler) issueInvitation(w http.ResponseWriter, r *http.Request, campaignID int64) {
	if r.Method != http.MethodPost {
		method(w, http.MethodPost)
		return
	}
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if !h.authorizeMutation(w, r) {
		return
	}
	actor, ok := h.sessionActor(w, r)
	if !ok {
		return
	}
	key, ok := idempotencyKey(w, r)
	if !ok {
		return
	}
	value, err := h.public.IssueInvitation(r.Context(), referralport.IssueInvitationCommand{Actor: actor, CampaignID: campaignID, IdempotencyKey: key})
	if err != nil {
		resultError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"url": value.URL, "expires_at": value.ExpiresAt.UTC()})
}
func (h *Handler) joinCampaign(w http.ResponseWriter, r *http.Request, campaignID int64) {
	if r.Method != http.MethodPost {
		method(w, http.MethodPost)
		return
	}
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if !h.authorizeMutation(w, r) {
		return
	}
	actor, ok := h.sessionActor(w, r)
	if !ok {
		return
	}
	key, ok := idempotencyKey(w, r)
	if !ok {
		return
	}
	var body struct {
		InvitationToken string `json:"invitation_token"`
		TeamID          int64  `json:"team_id"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.InvitationToken != "" && !validInvitationToken(body.InvitationToken) {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	value, err := h.public.JoinCampaign(r.Context(), referralport.JoinCampaignCommand{Actor: actor, CampaignID: campaignID, InvitationToken: body.InvitationToken, TeamID: body.TeamID, IdempotencyKey: key})
	if err != nil {
		resultError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, myCampaign(value))
}

func (h *Handler) authorizeAdmin(w http.ResponseWriter, r *http.Request, write bool) (accessdomain.Principal, bool) {
	principal, err := h.security.Authenticate(r.Context(), r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return accessdomain.Principal{}, false
	}
	if !adminRole(principal, write) {
		writeError(w, http.StatusForbidden, "permission_denied")
		return accessdomain.Principal{}, false
	}
	if write {
		if _, err = h.security.AuthorizeCSRF(r.Context(), r); err != nil {
			writeError(w, http.StatusForbidden, "csrf_required")
			return accessdomain.Principal{}, false
		}
	}
	return principal, true
}
func (h *Handler) listAdminCampaigns(w http.ResponseWriter, r *http.Request) {
	cursor, limit, ok := pageQuery(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	page, err := h.admin.ListAdminCampaigns(r.Context(), cursor, limit)
	if err != nil {
		resultError(w, err)
		return
	}
	items := make([]any, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, campaignSummary(item))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": page.NextCursor})
}
func (h *Handler) readAdminCampaign(w http.ResponseWriter, r *http.Request, raw string) {
	campaignID, ok := id(raw)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	view, err := h.admin.ReadAdminCampaign(r.Context(), campaignID)
	if err != nil {
		resultError(w, err)
		return
	}
	response := campaignView(view)
	if h.posters != nil {
		posters, err := h.posters.ListCampaignPosters(r.Context(), campaignID, true)
		if err != nil {
			resultError(w, err)
			return
		}
		response["posters"] = publicPosters(posters, true)
	}
	captainIDs := make([]customerdomain.CustomerID, 0, len(view.TeamSummaries))
	for _, summary := range view.TeamSummaries {
		captainIDs = append(captainIDs, customerdomain.CustomerID(summary.Team.CaptainCustomerID))
	}
	names, err := h.displayNames(r.Context(), captainIDs)
	if err != nil {
		resultError(w, err)
		return
	}
	teams := make([]any, 0, len(view.TeamSummaries))
	for _, summary := range view.TeamSummaries {
		team := adminTeam(summary.Team, names[customerdomain.CustomerID(summary.Team.CaptainCustomerID)])
		team["participant_count"] = summary.ParticipantCount
		team["direct_invitation_count"] = summary.DirectInvitationCount
		team["captain_participated"] = summary.CaptainParticipated
		teams = append(teams, team)
	}
	response["team_summaries"] = teams
	writeJSON(w, http.StatusOK, response)
}
func (h *Handler) createCampaign(w http.ResponseWriter, r *http.Request, actor int64) {
	var body campaignInput
	if !decode(w, r, &body) {
		return
	}
	if !body.valid() {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	key, ok := idempotencyKey(w, r)
	if !ok {
		return
	}
	value, err := h.admin.CreateCampaign(r.Context(), referralport.CreateCampaignCommand{ActorAdminID: actor, Name: body.Name, CoverURL: body.CoverURL, Description: body.Description, RewardRules: body.RewardRules, StartsAt: body.StartsAt, EndsAt: body.EndsAt, TeamMode: body.TeamMode, QualificationMode: body.QualificationMode, ProductID: body.ProductID, ProductType: body.ProductType, LeaderboardMetric: body.LeaderboardMetric, IdempotencyKey: key})
	if err != nil {
		resultError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, campaign(value))
}
func (h *Handler) updateCampaign(w http.ResponseWriter, r *http.Request, raw string, actor int64) {
	campaignID, ok := id(raw)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	var body campaignInput
	if !decode(w, r, &body) {
		return
	}
	if !body.valid() || body.ExpectedVersion < 1 {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	key, ok := idempotencyKey(w, r)
	if !ok {
		return
	}
	value, err := h.admin.UpdateCampaign(r.Context(), referralport.UpdateCampaignCommand{CampaignID: campaignID, ExpectedVersion: body.ExpectedVersion, ActorAdminID: actor, Name: body.Name, CoverURL: body.CoverURL, Description: body.Description, RewardRules: body.RewardRules, StartsAt: body.StartsAt, EndsAt: body.EndsAt, TeamMode: body.TeamMode, QualificationMode: body.QualificationMode, ProductID: body.ProductID, ProductType: body.ProductType, LeaderboardMetric: body.LeaderboardMetric, IdempotencyKey: key})
	if err != nil {
		resultError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, campaign(value))
}
func (h *Handler) setCampaignState(w http.ResponseWriter, r *http.Request, raw string, actor int64) {
	campaignID, ok := id(raw)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	var body struct {
		ExpectedVersion int64                        `json:"expected_version"`
		Target          referraldomain.CampaignState `json:"target"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.ExpectedVersion < 1 || !body.Target.Valid() {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	key, ok := idempotencyKey(w, r)
	if !ok {
		return
	}
	value, err := h.admin.SetCampaignState(r.Context(), referralport.SetCampaignStateCommand{CampaignID: campaignID, ExpectedVersion: body.ExpectedVersion, ActorAdminID: actor, Target: body.Target, IdempotencyKey: key})
	if err != nil {
		resultError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, campaign(value))
}
func (h *Handler) createTeam(w http.ResponseWriter, r *http.Request, raw string, actor int64) {
	campaignID, ok := id(raw)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	var body struct {
		CaptainCustomerID int64  `json:"captain_customer_id"`
		Name              string `json:"name"`
		LogoURL           string `json:"logo_url"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.CaptainCustomerID < 1 || strings.TrimSpace(body.Name) == "" || body.Name != strings.TrimSpace(body.Name) || len(body.Name) > 100 || body.LogoURL != strings.TrimSpace(body.LogoURL) || len(body.LogoURL) > 2000 {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	key, ok := idempotencyKey(w, r)
	if !ok {
		return
	}
	value, err := h.admin.CreateTeam(r.Context(), referralport.CreateTeamCommand{CampaignID: campaignID, CaptainCustomerID: body.CaptainCustomerID, ActorAdminID: actor, Name: body.Name, LogoURL: body.LogoURL, IdempotencyKey: key})
	if err != nil {
		resultError(w, err)
		return
	}
	names, err := h.displayNames(r.Context(), []customerdomain.CustomerID{customerdomain.CustomerID(value.CaptainCustomerID)})
	// The command is already committed and idempotent. A presentation-only
	// directory outage must not turn its successful response into a retryable
	// apparent failure, which would confuse operators about team creation.
	if err != nil {
		names = map[customerdomain.CustomerID]string{}
	}
	writeJSON(w, http.StatusCreated, adminTeam(value, names[customerdomain.CustomerID(value.CaptainCustomerID)]))
}
func (h *Handler) listAdminReferrals(w http.ResponseWriter, r *http.Request) {
	campaignID, cursor, limit, ok := adminPageQuery(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	page, err := h.admin.ListAdminReferrals(r.Context(), campaignID, cursor, limit)
	if err != nil {
		resultError(w, err)
		return
	}
	names, err := h.displayNames(r.Context(), adminReferralIDs(page.Items))
	if err != nil {
		resultError(w, err)
		return
	}
	items := make([]any, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, map[string]any{"participation_id": item.Participation.ID, "campaign_name": item.CampaignName, "participant_name": names[customerdomain.CustomerID(item.Participation.CustomerID)], "inviter_name": names[customerdomain.CustomerID(item.Participation.InviterCustomerID)], "joined_at": item.Participation.JoinedAt.UTC(), "score_event_id": item.ScoreEventID, "score_state": item.ScoreState})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": page.NextCursor})
}

func (h *Handler) listAdminParticipants(w http.ResponseWriter, r *http.Request, rawCampaignID string) {
	campaignID, ok := id(rawCampaignID)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	query, ok := adminParticipantQuery(r, campaignID)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	page, err := h.admin.ListAdminParticipants(r.Context(), query)
	if err != nil {
		resultError(w, err)
		return
	}
	names, err := h.displayNames(r.Context(), participantRecordIDs(page.Items))
	if err != nil {
		resultError(w, err)
		return
	}
	items := make([]any, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, adminParticipant(item, names))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": page.NextCursor})
}

func (h *Handler) listAdminInvitations(w http.ResponseWriter, r *http.Request, rawCampaignID string) {
	campaignID, ok := id(rawCampaignID)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	query, ok := adminInvitationQuery(r, campaignID)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	page, err := h.admin.ListAdminInvitations(r.Context(), query)
	if err != nil {
		resultError(w, err)
		return
	}
	names, err := h.displayNames(r.Context(), invitationRecordIDs(page.Items))
	if err != nil {
		resultError(w, err)
		return
	}
	items := make([]any, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, adminInvitation(item, names))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": page.NextCursor})
}

func (h *Handler) listAdminParticipantInvitations(w http.ResponseWriter, r *http.Request, rawCampaignID, rawParticipationID string) {
	campaignID, ok := id(rawCampaignID)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	participationID, ok := id(rawParticipationID)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	values := r.URL.Query()
	for key, entries := range values {
		if (key != "state" && key != "cursor" && key != "limit") || len(entries) != 1 {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
	}
	state, ok := optionalParticipationState(values.Get("state"))
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	cursor, limit, ok := cursorLimit(values)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	page, err := h.admin.ListAdminParticipantInvitations(r.Context(), referralport.AdminParticipantInvitationQuery{CampaignID: campaignID, ParticipationID: participationID, State: state, Cursor: cursor, Limit: limit})
	if err != nil {
		resultError(w, err)
		return
	}
	names, err := h.displayNames(r.Context(), invitationRecordIDs(page.Items))
	if err != nil {
		resultError(w, err)
		return
	}
	items := make([]any, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, adminInvitation(item, names))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": page.NextCursor})
}

func (h *Handler) exportAdminCampaign(w http.ResponseWriter, r *http.Request, rawCampaignID string) {
	campaignID, ok := id(rawCampaignID)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	values := r.URL.Query()
	if len(values["view"]) != 1 || (values.Get("view") != "participants" && values.Get("view") != "invitations" && values.Get("view") != "teams") || values.Get("cursor") != "" || values.Get("limit") != "" {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	view := values.Get("view")
	if view == "teams" {
		if len(values) != 1 {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		campaign, err := h.admin.ReadAdminCampaign(r.Context(), campaignID)
		if err != nil {
			resultError(w, err)
			return
		}
		captainIDs := make([]customerdomain.CustomerID, 0, len(campaign.TeamSummaries))
		for _, summary := range campaign.TeamSummaries {
			captainIDs = append(captainIDs, customerdomain.CustomerID(summary.Team.CaptainCustomerID))
		}
		names, err := h.displayNames(r.Context(), captainIDs)
		if err != nil {
			resultError(w, err)
			return
		}
		writeTeamCSV(w, campaign.TeamSummaries, names)
		return
	}
	if view == "participants" {
		query, valid := adminParticipantQuery(r, campaignID)
		if !valid {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		items, err := h.admin.ListAdminParticipantsForExport(r.Context(), query)
		if err != nil {
			resultError(w, err)
			return
		}
		names, err := h.displayNames(r.Context(), participantRecordIDs(items))
		if err != nil {
			resultError(w, err)
			return
		}
		writeParticipantCSV(w, items, names)
		return
	}
	if rawParticipationID := values.Get("participation_id"); rawParticipationID != "" {
		if view != "invitations" || values.Get("team_id") != "" || values.Get("inviter_customer_id") != "" {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		participationID, valid := id(rawParticipationID)
		if !valid {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		state, valid := optionalParticipationState(values.Get("state"))
		if !valid {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		items, err := h.admin.ListAdminParticipantInvitationsForExport(r.Context(), referralport.AdminParticipantInvitationQuery{CampaignID: campaignID, ParticipationID: participationID, State: state})
		if err != nil {
			resultError(w, err)
			return
		}
		names, err := h.displayNames(r.Context(), invitationRecordIDs(items))
		if err != nil {
			resultError(w, err)
			return
		}
		writeInvitationCSV(w, items, names)
		return
	}
	query, valid := adminInvitationQuery(r, campaignID)
	if !valid {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	items, err := h.admin.ListAdminInvitationsForExport(r.Context(), query)
	if err != nil {
		resultError(w, err)
		return
	}
	names, err := h.displayNames(r.Context(), invitationRecordIDs(items))
	if err != nil {
		resultError(w, err)
		return
	}
	writeInvitationCSV(w, items, names)
}
func (h *Handler) listHistory(w http.ResponseWriter, r *http.Request) {
	customerID, cursor, limit, ok := historyPageQuery(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	page, err := h.admin.ListRelationshipHistory(r.Context(), customerID, cursor, limit)
	if err != nil {
		resultError(w, err)
		return
	}
	ids := make([]customerdomain.CustomerID, 0, len(page.Items)*2)
	for _, item := range page.Items {
		ids = append(ids, customerdomain.CustomerID(item.CustomerID), customerdomain.CustomerID(item.ReferrerCustomerID))
	}
	names, err := h.displayNames(r.Context(), ids)
	if err != nil {
		resultError(w, err)
		return
	}
	items := make([]any, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, map[string]any{"relationship_id": item.RelationshipID, "version": item.Version, "customer_name": names[customerdomain.CustomerID(item.CustomerID)], "previous_referrer_name": names[customerdomain.CustomerID(item.PreviousReferrerCustomerID)], "referrer_name": names[customerdomain.CustomerID(item.ReferrerCustomerID)], "source_campaign_id": item.SourceCampaignID, "invitation_id": item.InvitationID, "accepted_at": item.AcceptedAt.UTC()})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": page.NextCursor})
}
func (h *Handler) listRewards(w http.ResponseWriter, r *http.Request) {
	campaignID, cursor, limit, ok := adminPageQuery(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	page, err := h.admin.ListRewards(r.Context(), campaignID, cursor, limit)
	if err != nil {
		resultError(w, err)
		return
	}
	ids := make([]customerdomain.CustomerID, 0, len(page.Items))
	for _, item := range page.Items {
		ids = append(ids, customerdomain.CustomerID(item.CustomerID))
	}
	names, err := h.displayNames(r.Context(), ids)
	if err != nil {
		resultError(w, err)
		return
	}
	items := make([]any, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, map[string]any{"reward_id": item.ID, "campaign_id": item.CampaignID, "customer_name": names[customerdomain.CustomerID(item.CustomerID)], "score_event_id": item.ScoreEventID, "period": item.Period, "reward": item.Reward, "evidence_reference": item.EvidenceReference, "state": item.State, "recorded_at": item.RecordedAt.UTC()})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": page.NextCursor})
}
func (h *Handler) reverseInvitation(w http.ResponseWriter, r *http.Request, raw string, actor int64) {
	participationID, ok := id(raw)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if !decode(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Reason) == "" || body.Reason != strings.TrimSpace(body.Reason) || len(body.Reason) > 500 {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	key, ok := idempotencyKey(w, r)
	if !ok {
		return
	}
	if err := h.admin.ReverseInvitation(r.Context(), referralport.ReverseInvitationCommand{ParticipationID: participationID, ActorAdminID: actor, Reason: body.Reason, IdempotencyKey: key}); err != nil {
		resultError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (h *Handler) recordReward(w http.ResponseWriter, r *http.Request, actor int64) {
	var body struct {
		CampaignID        int64  `json:"campaign_id"`
		CustomerID        int64  `json:"customer_id"`
		ScoreEventID      int64  `json:"score_event_id"`
		Period            string `json:"period"`
		Reward            string `json:"reward"`
		EvidenceReference string `json:"evidence_reference"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.CampaignID < 1 || body.CustomerID < 1 || body.ScoreEventID < 0 || strings.TrimSpace(body.Period) == "" || body.Period != strings.TrimSpace(body.Period) || len(body.Period) > 32 || strings.TrimSpace(body.Reward) == "" || body.Reward != strings.TrimSpace(body.Reward) || len(body.Reward) > 500 || body.EvidenceReference != strings.TrimSpace(body.EvidenceReference) || len(body.EvidenceReference) > 500 {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	key, ok := idempotencyKey(w, r)
	if !ok {
		return
	}
	value, err := h.admin.RecordReward(r.Context(), referralport.RecordRewardCommand{CampaignID: body.CampaignID, CustomerID: body.CustomerID, ScoreEventID: body.ScoreEventID, ActorAdminID: actor, Period: body.Period, Reward: body.Reward, EvidenceReference: body.EvidenceReference, IdempotencyKey: key})
	if err != nil {
		resultError(w, err)
		return
	}
	names, err := h.displayNames(r.Context(), []customerdomain.CustomerID{customerdomain.CustomerID(value.CustomerID)})
	if err != nil {
		names = map[customerdomain.CustomerID]string{}
	}
	writeJSON(w, http.StatusCreated, map[string]any{"reward_id": value.ID, "customer_name": names[customerdomain.CustomerID(value.CustomerID)], "state": value.State, "recorded_at": value.RecordedAt.UTC()})
}

func (h *Handler) revokeInvitation(w http.ResponseWriter, r *http.Request, raw string, actor int64) {
	invitationID, ok := id(raw)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if !decode(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Reason) == "" || body.Reason != strings.TrimSpace(body.Reason) || len(body.Reason) > 500 {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	key, ok := idempotencyKey(w, r)
	if !ok {
		return
	}
	if err := h.admin.RevokeInvitation(r.Context(), referralport.RevokeInvitationCommand{InvitationID: invitationID, ActorAdminID: actor, Reason: body.Reason, IdempotencyKey: key}); err != nil {
		resultError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type campaignInput struct {
	ExpectedVersion   int64                            `json:"expected_version"`
	Name              string                           `json:"name"`
	CoverURL          string                           `json:"cover_url"`
	Description       string                           `json:"description"`
	RewardRules       string                           `json:"reward_rules"`
	StartsAt          time.Time                        `json:"starts_at"`
	EndsAt            time.Time                        `json:"ends_at"`
	TeamMode          referraldomain.TeamMode          `json:"team_mode"`
	QualificationMode referraldomain.QualificationMode `json:"qualification_mode"`
	ProductID         int64                            `json:"product_id"`
	ProductType       string                           `json:"product_type"`
	LeaderboardMetric referraldomain.LeaderboardMetric `json:"leaderboard_metric"`
}

func (v campaignInput) valid() bool {
	return strings.TrimSpace(v.Name) != "" && v.Name == strings.TrimSpace(v.Name) && len(v.Name) <= 200 &&
		v.CoverURL == strings.TrimSpace(v.CoverURL) && len(v.CoverURL) <= 2000 &&
		v.Description == strings.TrimSpace(v.Description) && len(v.Description) <= 5000 &&
		v.RewardRules == strings.TrimSpace(v.RewardRules) && len(v.RewardRules) <= 5000 &&
		!v.StartsAt.IsZero() && !v.EndsAt.IsZero() && v.EndsAt.After(v.StartsAt) &&
		referraldomain.CampaignConfig{TeamMode: v.TeamMode, QualificationMode: v.QualificationMode, ProductID: v.ProductID, ProductType: v.ProductType, LeaderboardMetric: v.LeaderboardMetric}.Valid()
}

func campaign(value referraldomain.Campaign) map[string]any {
	config := value.Config()
	return map[string]any{"id": value.ID, "name": value.Name, "cover_url": value.CoverURL, "description": value.Description, "reward_rules": value.RewardRules, "state": value.State, "starts_at": value.StartsAt.UTC(), "ends_at": value.EndsAt.UTC(), "version": value.Version, "created_at": value.CreatedAt.UTC(), "updated_at": value.UpdatedAt.UTC(), "team_mode": config.TeamMode, "qualification_mode": config.QualificationMode, "product_id": config.ProductID, "product_type": config.ProductType, "leaderboard_metric": config.LeaderboardMetric}
}

func campaignSummary(value referralport.CampaignSummary) map[string]any {
	response := campaign(value.Campaign)
	response["effective_state"] = value.EffectiveState
	response["participant_count"] = value.ParticipantCount
	response["invitation_count"] = value.InvitationCount
	response["team_count"] = value.TeamCount
	return response
}

func (h *Handler) addPublicCampaignLinks(ctx context.Context, response map[string]any, campaign referraldomain.Campaign) {
	if response == nil || campaign.ID < 1 {
		return
	}
	response["activity_url"] = "/referral?campaign=" + strconv.FormatInt(campaign.ID, 10)
	config := campaign.Config()
	if config.QualificationMode != referraldomain.QualificationProductPurchase || h.productTargets == nil {
		return
	}
	kind := productport.ProductOptionType(config.ProductType)
	if kind == "standard_product" {
		kind = productport.ProductOptionStandard
	} else if kind == "service_period" {
		kind = productport.ProductOptionServicePeriod
	} else {
		return
	}
	product, err := h.productTargets.ReadProductTarget(ctx, kind, productport.ID(config.ProductID))
	if err != nil || product.Code == "" {
		return
	}
	prefix := "/p/"
	if kind == productport.ProductOptionServicePeriod {
		prefix = "/s/"
	}
	response["product_url"] = prefix + url.PathEscape(product.Code)
}

func publicTeam(value referraldomain.Team) map[string]any {
	return map[string]any{"id": value.ID, "name": value.Name, "logo_url": value.LogoURL}
}

func adminTeam(value referraldomain.Team, captainName string) map[string]any {
	response := publicTeam(value)
	response["captain_name"] = captainName
	response["version"] = value.Version
	response["created_at"] = value.CreatedAt.UTC()
	return response
}

func campaignView(value referralport.CampaignView) map[string]any {
	response := campaignSummary(value.CampaignSummary)
	teams := make([]any, 0, len(value.Teams))
	for _, team := range value.Teams {
		teams = append(teams, publicTeam(team))
	}
	metrics := make([]any, 0, len(value.DailyMetrics))
	for _, metric := range value.DailyMetrics {
		metrics = append(metrics, map[string]any{"date": metric.Date.In(shanghai).Format("2006-01-02"), "participant_count": metric.ParticipantCount, "invitation_count": metric.InviteCount})
	}
	response["teams"] = teams
	response["daily_metrics"] = metrics
	return response
}

func myCampaign(value referralport.MyCampaign) map[string]any {
	response := map[string]any{"campaign": campaignSummary(value.Campaign), "direct_invitation_count": value.DirectInvitationCount, "personal_total_score": value.PersonalTotalScore, "team_total_score": value.TeamTotalScore, "personal_rank": value.PersonalRank, "team_rank": value.TeamRank, "invitation_available": value.InvitationAvailable, "is_captain": value.IsCaptain}
	if value.Participation != nil {
		response["participation"] = map[string]any{"joined_at": value.Participation.JoinedAt.UTC(), "state": value.Participation.State}
	}
	if value.Team != nil {
		response["team"] = publicTeam(*value.Team)
	}
	if value.CaptainTeam != nil {
		response["captain_team"] = publicTeam(*value.CaptainTeam)
	}
	return response
}

func leaderboardEntry(value referralport.LeaderboardEntry, profile customerport.DirectoryPublicProfile) map[string]any {
	response := map[string]any{"rank": value.Rank, "score": value.Score, "team_name": value.TeamName, "first_reached_at": value.FirstReachedAt.UTC(), "mine": value.Mine}
	if value.SalesAmountMinor > 0 || value.SalesOrderCount > 0 {
		response["sales_amount_minor"] = value.SalesAmountMinor
		response["sales_order_count"] = value.SalesOrderCount
	}
	if profile.DisplayName != "" {
		response["display_name"] = profile.DisplayName
	}
	if profile.AvatarURL != "" {
		response["avatar_url"] = profile.AvatarURL
	}
	return response
}

func (h *Handler) publicProfile(ctx context.Context, id customerdomain.CustomerID) (customerport.DirectoryPublicProfile, error) {
	profiles, err := h.publicProfiles(ctx, []customerdomain.CustomerID{id})
	return profiles[id], err
}

func (h *Handler) publicProfiles(ctx context.Context, ids []customerdomain.CustomerID) (map[customerdomain.CustomerID]customerport.DirectoryPublicProfile, error) {
	if h.profiles != nil {
		return h.profiles.PublicProfiles(ctx, ids)
	}
	names, err := h.displayNames(ctx, ids)
	profiles := make(map[customerdomain.CustomerID]customerport.DirectoryPublicProfile, len(names))
	for id, name := range names {
		profiles[id] = customerport.DirectoryPublicProfile{DisplayName: name}
	}
	return profiles, err
}

func (h *Handler) displayNames(ctx context.Context, ids []customerdomain.CustomerID) (map[customerdomain.CustomerID]string, error) {
	unique := make(map[customerdomain.CustomerID]struct{}, len(ids))
	filtered := make([]customerdomain.CustomerID, 0, len(ids))
	for _, customerID := range ids {
		if customerID > 0 {
			if _, seen := unique[customerID]; !seen {
				unique[customerID] = struct{}{}
				filtered = append(filtered, customerID)
			}
		}
	}
	if len(filtered) == 0 {
		return map[customerdomain.CustomerID]string{}, nil
	}
	result := make(map[customerdomain.CustomerID]string, len(filtered))
	// Directory projections may impose a conservative parameter limit. Batching
	// keeps a full CSV export within that Port contract without changing the
	// query snapshot already fixed by Referral's export read.
	const displayNameBatchSize = 100
	for start := 0; start < len(filtered); start += displayNameBatchSize {
		end := start + displayNameBatchSize
		if end > len(filtered) {
			end = len(filtered)
		}
		names, err := h.names.DisplayNames(ctx, filtered[start:end])
		if err != nil {
			return nil, err
		}
		for customerID, name := range names {
			result[customerID] = name
		}
	}
	return result, nil
}

func participantIDs(items []referralport.InviteItem) []customerdomain.CustomerID {
	ids := make([]customerdomain.CustomerID, 0, len(items))
	for _, item := range items {
		ids = append(ids, customerdomain.CustomerID(item.Participation.CustomerID))
	}
	return ids
}
func entryIDs(items []referralport.LeaderboardEntry) []customerdomain.CustomerID {
	ids := make([]customerdomain.CustomerID, 0, len(items))
	for _, item := range items {
		// Team boards have no customer subject. Keeping zero out of the
		// presentation Port prevents a team-only board from being mistaken for a
		// request to resolve a customer identity.
		if item.CustomerID > 0 {
			ids = append(ids, customerdomain.CustomerID(item.CustomerID))
		}
	}
	return ids
}
func adminReferralIDs(items []referralport.AdminReferralRecord) []customerdomain.CustomerID {
	ids := make([]customerdomain.CustomerID, 0, len(items)*2)
	for _, item := range items {
		ids = append(ids, customerdomain.CustomerID(item.Participation.CustomerID), customerdomain.CustomerID(item.Participation.InviterCustomerID))
	}
	return ids
}

func participantRecordIDs(items []referralport.AdminParticipantRecord) []customerdomain.CustomerID {
	ids := make([]customerdomain.CustomerID, 0, len(items)*2)
	for _, item := range items {
		ids = append(ids, customerdomain.CustomerID(item.Participation.CustomerID), customerdomain.CustomerID(item.InviterCustomerID))
	}
	return ids
}

func invitationRecordIDs(items []referralport.AdminInvitationRecord) []customerdomain.CustomerID {
	ids := make([]customerdomain.CustomerID, 0, len(items)*2)
	for _, item := range items {
		ids = append(ids, customerdomain.CustomerID(item.Participation.CustomerID), customerdomain.CustomerID(item.InviterCustomerID))
	}
	return ids
}

func adminParticipant(item referralport.AdminParticipantRecord, names map[customerdomain.CustomerID]string) map[string]any {
	return map[string]any{
		"participation_id":        item.Participation.ID,
		"participant_name":        names[customerdomain.CustomerID(item.Participation.CustomerID)],
		"inviter_name":            names[customerdomain.CustomerID(item.InviterCustomerID)],
		"team_name":               item.Team.Name,
		"team_logo_url":           item.Team.LogoURL,
		"joined_at":               item.Participation.JoinedAt.UTC(),
		"state":                   item.Participation.State,
		"role":                    participantRole(item.Participation.CustomerID, item.Team.CaptainCustomerID),
		"direct_invitation_count": item.DirectInvitationCount,
	}
}

func participantRole(customerID, captainCustomerID int64) string {
	if customerID > 0 && customerID == captainCustomerID {
		return "captain"
	}
	return "member"
}

func adminInvitation(item referralport.AdminInvitationRecord, names map[customerdomain.CustomerID]string) map[string]any {
	return map[string]any{
		"participation_id":      item.Participation.ID,
		"participant_name":      names[customerdomain.CustomerID(item.Participation.CustomerID)],
		"inviter_name":          names[customerdomain.CustomerID(item.InviterCustomerID)],
		"inviter_team_name":     item.InviterTeam.Name,
		"participant_team_name": item.ParticipantTeam.Name,
		"joined_at":             item.Participation.JoinedAt.UTC(),
		"score_state":           item.ScoreState,
	}
}

func csvSafe(value string) string {
	trimmed := strings.TrimLeft(value, " \t\r\n")
	if strings.HasPrefix(value, "\t") || strings.HasPrefix(value, "\r") || strings.HasPrefix(value, "\n") || (trimmed != "" && strings.ContainsRune("=+-@", rune(trimmed[0]))) {
		return "'" + value
	}
	return value
}

func csvHeaders(w http.ResponseWriter, filename string) *csv.Writer {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// Excel and WPS both use the signature to reliably open Chinese headers as
	// UTF-8 rather than the machine's legacy code page.
	_, _ = w.Write([]byte{0xef, 0xbb, 0xbf})
	return csv.NewWriter(w)
}

func writeTeamCSV(w http.ResponseWriter, items []referralport.AdminTeamSummary, names map[customerdomain.CustomerID]string) {
	writer := csvHeaders(w, "referral-teams.csv")
	_ = writer.Write([]string{"战队", "队长", "已确认队员数", "有效直接邀请数", "队长参加状态", "创建时间（北京时间）"})
	for _, item := range items {
		captainState := "待加入"
		if item.CaptainParticipated {
			captainState = "已参加"
		}
		_ = writer.Write([]string{csvSafe(item.Team.Name), csvSafe(names[customerdomain.CustomerID(item.Team.CaptainCustomerID)]), strconv.FormatInt(item.ParticipantCount, 10), strconv.FormatInt(item.DirectInvitationCount, 10), captainState, item.Team.CreatedAt.In(shanghai).Format("2006-01-02 15:04:05")})
	}
	writer.Flush()
}

func writeParticipantCSV(w http.ResponseWriter, items []referralport.AdminParticipantRecord, names map[customerdomain.CustomerID]string) {
	writer := csvHeaders(w, "referral-participants.csv")
	_ = writer.Write([]string{"参与人", "所属战队", "角色", "邀请人", "参加时间（北京时间）", "参与状态", "本人有效直接邀请数"})
	for _, item := range items {
		role := "队员"
		if participantRole(item.Participation.CustomerID, item.Team.CaptainCustomerID) == "captain" {
			role = "队长"
		}
		_ = writer.Write([]string{csvSafe(names[customerdomain.CustomerID(item.Participation.CustomerID)]), csvSafe(item.Team.Name), role, csvSafe(names[customerdomain.CustomerID(item.InviterCustomerID)]), item.Participation.JoinedAt.In(shanghai).Format("2006-01-02 15:04:05"), participationStateLabel(item.Participation.State), strconv.FormatInt(item.DirectInvitationCount, 10)})
	}
	writer.Flush()
}

func writeInvitationCSV(w http.ResponseWriter, items []referralport.AdminInvitationRecord, names map[customerdomain.CustomerID]string) {
	writer := csvHeaders(w, "referral-invitations.csv")
	_ = writer.Write([]string{"邀请人", "邀请人战队", "参加人", "参加人战队", "参加时间（北京时间）", "计分状态"})
	for _, item := range items {
		_ = writer.Write([]string{csvSafe(names[customerdomain.CustomerID(item.InviterCustomerID)]), csvSafe(item.InviterTeam.Name), csvSafe(names[customerdomain.CustomerID(item.Participation.CustomerID)]), csvSafe(item.ParticipantTeam.Name), item.Participation.JoinedAt.In(shanghai).Format("2006-01-02 15:04:05"), scoreStateLabel(item.ScoreState)})
	}
	writer.Flush()
}

func participationStateLabel(value referraldomain.ParticipationState) string {
	if value == referraldomain.ParticipationReversed {
		return "已撤销"
	}
	return "有效"
}

func scoreStateLabel(value string) string {
	if value == "reversed" {
		return "已撤销"
	}
	if value == "none" {
		return "无计分"
	}
	return "有效"
}

var shanghai, _ = time.LoadLocation("Asia/Shanghai")

func leaderboardQuery(r *http.Request, campaignID int64) (referralport.LeaderboardQuery, bool) {
	values := r.URL.Query()
	for key, entries := range values {
		if (key != "kind" && key != "period" && key != "date" && key != "team_id" && key != "cursor" && key != "limit") || len(entries) != 1 {
			return referralport.LeaderboardQuery{}, false
		}
	}
	kind := referralport.LeaderboardKind(values.Get("kind"))
	period := referralport.LeaderboardPeriod(values.Get("period"))
	if period == referralport.LeaderboardPeriod("total") {
		period = referralport.LeaderboardAll
	}
	if !kind.Valid() || !period.Valid() {
		return referralport.LeaderboardQuery{}, false
	}
	cursor, limit, ok := pageQuery(r)
	if !ok {
		return referralport.LeaderboardQuery{}, false
	}
	query := referralport.LeaderboardQuery{CampaignID: campaignID, Kind: kind, Period: period, Cursor: cursor, Limit: limit}
	if kind == referralport.LeaderboardInTeam {
		teamID, valid := id(values.Get("team_id"))
		if !valid {
			return referralport.LeaderboardQuery{}, false
		}
		query.TeamID = teamID
	} else if values.Get("team_id") != "" {
		return referralport.LeaderboardQuery{}, false
	}
	switch period {
	case referralport.LeaderboardAll:
		return query, values.Get("date") == ""
	case referralport.LeaderboardDay:
		anchor, err := time.ParseInLocation("2006-01-02", values.Get("date"), shanghai)
		if err != nil {
			return referralport.LeaderboardQuery{}, false
		}
		query.Anchor = anchor
		return query, true
	}
	return referralport.LeaderboardQuery{}, false
}

func pageQuery(r *http.Request) (string, int32, bool) {
	values := r.URL.Query()
	for key, entries := range values {
		if (key != "cursor" && key != "limit" && key != "kind" && key != "period" && key != "date" && key != "week" && key != "team_id" && key != "campaign_id") || len(entries) != 1 {
			return "", 0, false
		}
	}
	return cursorLimit(values)
}
func adminPageQuery(r *http.Request) (int64, string, int32, bool) {
	cursor, limit, ok := pageQuery(r)
	if !ok {
		return 0, "", 0, false
	}
	raw := r.URL.Query().Get("campaign_id")
	if raw == "" {
		return 0, cursor, limit, true
	}
	campaignID, valid := id(raw)
	return campaignID, cursor, limit, valid
}

func adminParticipantQuery(r *http.Request, campaignID int64) (referralport.AdminParticipantQuery, bool) {
	values := r.URL.Query()
	for key, entries := range values {
		if (key != "team_id" && key != "state" && key != "cursor" && key != "limit" && key != "view") || len(entries) != 1 {
			return referralport.AdminParticipantQuery{}, false
		}
	}
	teamID, ok := optionalID(values.Get("team_id"))
	if !ok {
		return referralport.AdminParticipantQuery{}, false
	}
	state, ok := optionalParticipationState(values.Get("state"))
	if !ok {
		return referralport.AdminParticipantQuery{}, false
	}
	cursor, limit, ok := cursorLimit(values)
	if !ok {
		return referralport.AdminParticipantQuery{}, false
	}
	return referralport.AdminParticipantQuery{CampaignID: campaignID, TeamID: teamID, State: state, Cursor: cursor, Limit: limit}, true
}

func adminInvitationQuery(r *http.Request, campaignID int64) (referralport.AdminInvitationQuery, bool) {
	values := r.URL.Query()
	for key, entries := range values {
		if (key != "team_id" && key != "inviter_customer_id" && key != "state" && key != "cursor" && key != "limit" && key != "view") || len(entries) != 1 {
			return referralport.AdminInvitationQuery{}, false
		}
	}
	teamID, ok := optionalID(values.Get("team_id"))
	if !ok {
		return referralport.AdminInvitationQuery{}, false
	}
	inviterCustomerID, ok := optionalID(values.Get("inviter_customer_id"))
	if !ok {
		return referralport.AdminInvitationQuery{}, false
	}
	state, ok := optionalParticipationState(values.Get("state"))
	if !ok {
		return referralport.AdminInvitationQuery{}, false
	}
	cursor, limit, ok := cursorLimit(values)
	if !ok {
		return referralport.AdminInvitationQuery{}, false
	}
	return referralport.AdminInvitationQuery{CampaignID: campaignID, TeamID: teamID, InviterCustomerID: inviterCustomerID, State: state, Cursor: cursor, Limit: limit}, true
}

func optionalID(raw string) (int64, bool) {
	if raw == "" {
		return 0, true
	}
	return id(raw)
}

func optionalParticipationState(raw string) (referraldomain.ParticipationState, bool) {
	if raw == "" {
		return "", true
	}
	state := referraldomain.ParticipationState(raw)
	return state, state.Valid()
}
func historyPageQuery(r *http.Request) (int64, string, int32, bool) {
	values := r.URL.Query()
	for key, entries := range values {
		if (key != "customer_id" && key != "cursor" && key != "limit") || len(entries) != 1 {
			return 0, "", 0, false
		}
	}
	customerID, ok := id(values.Get("customer_id"))
	if !ok {
		return 0, "", 0, false
	}
	// The allowed-key check above includes customer_id; do not send this
	// already-validated query back through pageQuery, whose generic surface
	// intentionally excludes customer identifiers.
	cursor, limit, ok := cursorLimit(values)
	return customerID, cursor, limit, ok
}

func cursorLimit(values url.Values) (string, int32, bool) {
	limit := int64(50)
	if raw := values.Get("limit"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 32)
		if err != nil || parsed < 1 || parsed > 100 {
			return "", 0, false
		}
		limit = parsed
	}
	cursor := values.Get("cursor")
	if len(cursor) > 2048 || strings.TrimSpace(cursor) != cursor {
		return "", 0, false
	}
	return cursor, int32(limit), true
}

func (h *Handler) sessionActor(w http.ResponseWriter, r *http.Request) (distributionport.TrustedSessionActor, bool) {
	cookie, err := r.Cookie(h.sessionCookieName)
	if err != nil || cookie.Value == "" {
		writeError(w, http.StatusUnauthorized, "referral_session_required")
		return distributionport.TrustedSessionActor{}, false
	}
	actor, err := h.sessions.Resolve(r.Context(), cookie.Value)
	if err != nil || !actor.Valid() {
		writeError(w, http.StatusUnauthorized, "referral_session_required")
		return distributionport.TrustedSessionActor{}, false
	}
	return actor, true
}
func (h *Handler) optionalSessionActor(r *http.Request) (distributionport.TrustedSessionActor, bool) {
	cookie, err := r.Cookie(h.sessionCookieName)
	if err != nil || cookie.Value == "" {
		return distributionport.TrustedSessionActor{}, false
	}
	actor, err := h.sessions.Resolve(r.Context(), cookie.Value)
	return actor, err == nil && actor.Valid()
}
func (h *Handler) authorizeMutation(w http.ResponseWriter, r *http.Request) bool {
	return h.sameOrigin(w, r) && h.validCSRF(w, r)
}
func (h *Handler) sameOrigin(w http.ResponseWriter, r *http.Request) bool {
	if site := strings.ToLower(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site"))); site != "" && site != "same-origin" && site != "same-site" && site != "none" {
		writeError(w, http.StatusForbidden, "cross_site_request")
		return false
	}
	origin, ok := canonicalOrigin(r.Header.Get("Origin"))
	if !ok {
		writeError(w, http.StatusForbidden, "cross_site_request")
		return false
	}
	if _, ok = h.allowedOrigins[origin]; !ok {
		writeError(w, http.StatusForbidden, "cross_site_request")
		return false
	}
	return true
}
func (h *Handler) validCSRF(w http.ResponseWriter, r *http.Request) bool {
	cookie, err := r.Cookie(h.csrfCookieName)
	header := r.Header.Get(h.csrfHeader)
	if err != nil || cookie.Value == "" || header == "" || len(cookie.Value) != len(header) || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(header)) != 1 {
		writeError(w, http.StatusForbidden, "csrf_required")
		return false
	}
	return true
}
func idempotencyKey(w http.ResponseWriter, r *http.Request) (string, bool) {
	value := r.Header.Get("Idempotency-Key")
	if value == "" || value != strings.TrimSpace(value) || len(value) < 16 || len(value) > 200 {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return "", false
	}
	return value, true
}
func randomToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
func validInvitationToken(value string) bool {
	if len(value) != 47 || !strings.HasPrefix(value, "rfi_") {
		return false
	}
	for _, r := range value[4:] {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}
func canonicalOrigin(raw string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return "", false
	}
	return strings.ToLower(parsed.Scheme + "://" + parsed.Host), true
}
func split(value string) []string {
	if value == "" {
		return nil
	}
	return strings.Split(value, "/")
}
func id(raw string) (int64, bool) {
	value, err := strconv.ParseInt(raw, 10, 64)
	return value, err == nil && value > 0 && raw == strconv.FormatInt(value, 10)
}
func decode(w http.ResponseWriter, r *http.Request, target any) bool {
	if r.Body == nil || r.ContentLength > maxBody {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return false
	}
	return true
}
func adminRole(principal accessdomain.Principal, write bool) bool {
	if principal.InternalID < 1 || (principal.Kind != accessdomain.KindAdmin && principal.Kind != accessdomain.KindStaff) {
		return false
	}
	for _, role := range principal.Roles {
		if write && (role == accessdomain.RoleAdmin || role == accessdomain.RoleSuperAdmin) {
			return true
		}
		if !write && (role == accessdomain.RoleViewer || role == accessdomain.RoleAdmin || role == accessdomain.RoleSuperAdmin) {
			return true
		}
	}
	return false
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"error": code})
}
func method(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	writeError(w, http.StatusMethodNotAllowed, "method_not_allowed")
}
func methodIfNeeded(w http.ResponseWriter, r *http.Request, allow string) {
	if r.Method != allow {
		method(w, allow)
	}
}
func resultError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, referralport.ErrUnauthorized), errors.Is(err, distributionport.ErrUnauthorized):
		writeError(w, http.StatusUnauthorized, "referral_session_required")
	case errors.Is(err, referralport.ErrParticipationRequired):
		writeError(w, http.StatusForbidden, "referral_participation_required")
	case errors.Is(err, referralport.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found")
	case errors.Is(err, referralport.ErrInvitationInvalid):
		writeError(w, http.StatusConflict, "invitation_invalid")
	case errors.Is(err, referralport.ErrCampaignUnavailable):
		writeError(w, http.StatusConflict, "campaign_unavailable")
	case errors.Is(err, referralport.ErrCampaignTeamLocked):
		writeError(w, http.StatusConflict, "campaign_team_locked")
	case errors.Is(err, referralport.ErrCaptainIneligible):
		writeError(w, http.StatusConflict, "captain_ineligible")
	case errors.Is(err, referralport.ErrTeamNameExists):
		writeError(w, http.StatusConflict, "team_name_exists")
	case errors.Is(err, referralport.ErrCaptainAlreadyAssigned):
		writeError(w, http.StatusConflict, "captain_already_assigned")
	case errors.Is(err, referralport.ErrIdempotencyConflict):
		writeError(w, http.StatusConflict, "idempotency_conflict")
	case errors.Is(err, referralport.ErrCampaignConfigLocked):
		writeError(w, http.StatusConflict, "campaign_config_locked")
	case errors.Is(err, referralport.ErrInvalidRequest):
		writeError(w, http.StatusBadRequest, "invalid_request")
	case errors.Is(err, referralport.ErrConflict):
		writeError(w, http.StatusConflict, "conflict")
	default:
		writeError(w, http.StatusServiceUnavailable, "unavailable")
	}
}

// listProductOptions shares the Referral admin authorization boundary and reads
// only Product's bounded, canonical selection projection.
func (h *Handler) listProductOptions(w http.ResponseWriter, r *http.Request) {
	if h.productOptions == nil || h.productTargets == nil {
		resultError(w, referralport.ErrUnavailable)
		return
	}
	if raw := r.URL.Query().Get("product_id"); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		kind := productport.ProductOptionType(r.URL.Query().Get("product_type"))
		if err != nil || id < 1 || (kind != productport.ProductOptionStandard && kind != productport.ProductOptionServicePeriod) {
			writeError(w, 400, "invalid_request")
			return
		}
		item, err := h.productTargets.ReadProductTarget(r.Context(), kind, productport.ID(id))
		if err != nil {
			if errors.Is(err, productport.ErrSaleableProductNotFound) {
				writeError(w, 404, "not_found")
			} else {
				resultError(w, referralport.ErrUnavailable)
			}
			return
		}
		writeJSON(w, 200, item)
		return
	}
	limit, offset := 50, 0
	var err error
	if v := r.URL.Query().Get("limit"); v != "" {
		limit, err = strconv.Atoi(v)
		if err != nil {
			writeError(w, 400, "invalid_request")
			return
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		offset, err = strconv.Atoi(v)
		if err != nil {
			writeError(w, 400, "invalid_request")
			return
		}
	}
	if limit < 1 || limit > 100 || offset < 0 || offset > 1000000 {
		writeError(w, 400, "invalid_request")
		return
	}
	page, err := h.productOptions.ListProductOptions(r.Context(), productport.ProductOptionQuery{Q: r.URL.Query().Get("q"), ProductType: productport.ProductOptionAll, Limit: int32(limit), Offset: int32(offset)})
	if err != nil {
		if errors.Is(err, productport.ErrInvalidProductOptionQuery) {
			writeError(w, 400, "invalid_request")
		} else {
			resultError(w, referralport.ErrUnavailable)
		}
		return
	}
	writeJSON(w, 200, page)
}
