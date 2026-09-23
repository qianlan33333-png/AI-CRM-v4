package channel

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
)

type AssetApplication interface {
	Publish(context.Context, int64, int64, AcquisitionAssetKind, string) (AcquisitionAsset, error)
	Mutate(context.Context, int64, int64, string, string, string) (AcquisitionAsset, error)
	List(context.Context, int64, int, int64) ([]AcquisitionAsset, error)
	Get(context.Context, int64, string) (AcquisitionAsset, error)
}
type AssetHTTPHandler struct {
	app      AssetApplication
	security interface {
		Authenticate(context.Context, *http.Request) (accessdomain.Principal, error)
		AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error)
	}
	download *http.Client
}

func NewAssetHTTPHandler(app AssetApplication, security interface {
	Authenticate(context.Context, *http.Request) (accessdomain.Principal, error)
	AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error)
}) (*AssetHTTPHandler, error) {
	if app == nil || security == nil {
		return nil, errors.New("channel asset HTTP dependencies are required")
	}
	client := &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return &AssetHTTPHandler{app: app, security: security, download: client}, nil
}
func (handler *AssetHTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	path := strings.TrimSuffix(r.URL.Path, "/")
	prefix := "/api/admin/channels/"
	if !strings.HasPrefix(path, prefix) {
		writeCatalogError(w, http.StatusNotFound, "NOT_FOUND")
		return
	}
	parts := strings.Split(strings.TrimPrefix(path, prefix), "/")
	if len(parts) < 2 || len(parts) > 4 {
		writeCatalogError(w, http.StatusNotFound, "NOT_FOUND")
		return
	}
	channelID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || channelID < 1 {
		writeCatalogError(w, http.StatusNotFound, "NOT_FOUND")
		return
	}
	if parts[1] == "acquisition-assets" {
		if len(parts) == 2 {
			if r.Method == http.MethodGet {
				handler.list(w, r, channelID)
			} else if r.Method == http.MethodPost {
				handler.publish(w, r, channelID)
			} else {
				w.Header().Set("Allow", "GET, POST")
				writeCatalogError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED")
			}
			return
		}
		if len(parts) == 3 {
			switch r.Method {
			case http.MethodGet:
				handler.get(w, r, channelID, parts[2])
			case http.MethodPatch:
				handler.mutate(w, r, channelID, parts[2], "update")
			case http.MethodDelete:
				handler.mutate(w, r, channelID, parts[2], "delete")
			default:
				w.Header().Set("Allow", "GET, PATCH, DELETE")
				writeCatalogError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED")
			}
			return
		}
		if len(parts) == 4 && parts[3] == "reconcile" && r.Method == http.MethodPost {
			handler.reconcile(w, r, channelID, parts[2])
			return
		}
	}
	if parts[1] == "qrcode" && len(parts) == 3 && parts[2] == "generate" && r.Method == http.MethodPost {
		handler.generate(w, r, channelID)
		return
	}
	if parts[1] == "qrcode" && len(parts) == 3 && parts[2] == "download" && r.Method == http.MethodGet {
		handler.downloadQR(w, r, channelID)
		return
	}
	writeCatalogError(w, http.StatusNotFound, "NOT_FOUND")
}

func (handler *AssetHTTPHandler) mutate(w http.ResponseWriter, r *http.Request, channelID int64, effectRef, operation string) {
	principal, ok := handler.write(w, r)
	if !ok {
		return
	}
	key, err := singleCatalogIdempotencyKey(r)
	if err != nil || r.URL.RawQuery != "" {
		writeCatalogError(w, http.StatusBadRequest, "MALFORMED_REQUEST")
		return
	}
	if operation == "update" {
		if r.Header.Get("Content-Type") != "application/json" {
			writeCatalogError(w, http.StatusBadRequest, "MALFORMED_REQUEST")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, catalogHTTPMaxBody)
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		var body map[string]json.RawMessage
		if decoder.Decode(&body) != nil || len(body) != 0 || func() bool { e := decoder.Decode(&struct{}{}); return !errors.Is(e, io.EOF) }() {
			writeCatalogError(w, http.StatusBadRequest, "MALFORMED_REQUEST")
			return
		}
	} else if r.ContentLength > 0 {
		writeCatalogError(w, http.StatusBadRequest, "MALFORMED_REQUEST")
		return
	}
	asset, err := handler.app.Mutate(r.Context(), channelID, principal.InternalID, effectRef, operation, key)
	if err != nil {
		writeAssetError(w, err)
		return
	}
	w.Header().Set("Retry-After", "1")
	writeChannelJSON(w, http.StatusAccepted, assetAcceptanceJSON(asset))
}

func (handler *AssetHTTPHandler) reconcile(w http.ResponseWriter, r *http.Request, channelID int64, effectRef string) {
	principal, ok := handler.write(w, r)
	if !ok {
		return
	}
	key, err := singleCatalogIdempotencyKey(r)
	if err != nil || r.URL.RawQuery != "" || r.Header.Get("Content-Type") != "application/json" {
		writeCatalogError(w, http.StatusBadRequest, "MALFORMED_REQUEST")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, catalogHTTPMaxBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var body struct {
		Resolution       string `json:"resolution"`
		EvidenceDigest   string `json:"evidence_digest"`
		ProviderAssetRef string `json:"provider_asset_ref"`
		AssetURL         string `json:"asset_url"`
	}
	if decoder.Decode(&body) != nil || func() bool { e := decoder.Decode(&struct{}{}); return !errors.Is(e, io.EOF) }() {
		writeCatalogError(w, http.StatusBadRequest, "MALFORMED_REQUEST")
		return
	}
	application, supported := handler.app.(interface {
		Reconcile(context.Context, AssetReconcileCommand) (AcquisitionAsset, error)
	})
	if !supported {
		writeCatalogError(w, http.StatusServiceUnavailable, "DEPENDENCY_UNAVAILABLE")
		return
	}
	asset, err := application.Reconcile(r.Context(), AssetReconcileCommand{ChannelID: channelID, ActorID: principal.InternalID, EffectRef: effectRef, Resolution: body.Resolution, EvidenceDigest: body.EvidenceDigest, ProviderAssetRef: body.ProviderAssetRef, ResultURL: body.AssetURL, IdempotencyKey: key})
	if err != nil {
		writeAssetError(w, err)
		return
	}
	writeChannelJSON(w, http.StatusOK, assetJSON(asset))
}
func (handler *AssetHTTPHandler) read(w http.ResponseWriter, r *http.Request) bool {
	principal, err := handler.security.Authenticate(r.Context(), r)
	if err != nil {
		writeCatalogError(w, http.StatusUnauthorized, "UNAUTHORIZED")
		return false
	}
	if !channelCatalogReadRole(principal) {
		writeCatalogError(w, http.StatusForbidden, "FORBIDDEN")
		return false
	}
	return true
}

func (handler *AssetHTTPHandler) export(w http.ResponseWriter, r *http.Request) bool {
	principal, err := handler.security.Authenticate(r.Context(), r)
	if err != nil {
		writeCatalogError(w, http.StatusUnauthorized, "UNAUTHORIZED")
		return false
	}
	if !channelBusinessWriteRole(principal) {
		writeCatalogError(w, http.StatusForbidden, "FORBIDDEN")
		return false
	}
	return true
}

func (handler *AssetHTTPHandler) write(w http.ResponseWriter, r *http.Request) (accessdomain.Principal, bool) {
	principal, err := handler.security.AuthorizeCSRF(r.Context(), r)
	if err != nil {
		writeCatalogError(w, http.StatusUnauthorized, "UNAUTHORIZED")
		return accessdomain.Principal{}, false
	}
	if channelBusinessWriteRole(principal) {
		return principal, true
	}
	writeCatalogError(w, http.StatusForbidden, "FORBIDDEN")
	return accessdomain.Principal{}, false
}
func (handler *AssetHTTPHandler) publish(w http.ResponseWriter, r *http.Request, id int64) {
	principal, ok := handler.write(w, r)
	if !ok {
		return
	}
	key, err := singleCatalogIdempotencyKey(r)
	if err != nil || r.URL.RawQuery != "" || r.Header.Get("Content-Type") != "application/json" {
		writeCatalogError(w, http.StatusBadRequest, "MALFORMED_REQUEST")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, catalogHTTPMaxBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var body struct {
		Kind AcquisitionAssetKind `json:"kind"`
	}
	if decoder.Decode(&body) != nil || func() bool { e := decoder.Decode(&struct{}{}); return !errors.Is(e, io.EOF) }() {
		writeCatalogError(w, http.StatusBadRequest, "MALFORMED_REQUEST")
		return
	}
	asset, err := handler.app.Publish(r.Context(), id, principal.InternalID, body.Kind, key)
	if err != nil {
		writeAssetError(w, err)
		return
	}
	w.Header().Set("Retry-After", "1")
	writeChannelJSON(w, http.StatusAccepted, assetAcceptanceJSON(asset))
}
func (handler *AssetHTTPHandler) generate(w http.ResponseWriter, r *http.Request, id int64) {
	principal, ok := handler.write(w, r)
	if !ok {
		return
	}
	key, err := singleCatalogIdempotencyKey(r)
	if err != nil || r.URL.RawQuery != "" || r.Header.Get("Content-Type") != "application/json" {
		writeCatalogError(w, http.StatusBadRequest, "MALFORMED_REQUEST")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, catalogHTTPMaxBody)
	var body map[string]json.RawMessage
	decoder := json.NewDecoder(r.Body)
	if decoder.Decode(&body) != nil || len(body) != 0 {
		writeCatalogError(w, http.StatusBadRequest, "MALFORMED_REQUEST")
		return
	}
	asset, err := handler.app.Publish(r.Context(), id, principal.InternalID, AcquisitionAssetQRCode, key)
	if err != nil {
		writeAssetError(w, err)
		return
	}
	writeChannelJSON(w, http.StatusAccepted, assetAcceptanceJSON(asset))
}
func (handler *AssetHTTPHandler) list(w http.ResponseWriter, r *http.Request, id int64) {
	if !handler.read(w, r) {
		return
	}
	if !onlyChannelQuery(r, "limit", "cursor") || r.ContentLength > 0 {
		writeCatalogError(w, http.StatusBadRequest, "MALFORMED_REQUEST")
		return
	}
	limit := 20
	if raw := r.URL.Query().Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 50 {
			writeCatalogError(w, http.StatusBadRequest, "MALFORMED_REQUEST")
			return
		}
		limit = value
	}
	before := int64(0)
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		before, _ = strconv.ParseInt(raw, 10, 64)
		if before < 1 {
			writeCatalogError(w, http.StatusBadRequest, "INVALID_CURSOR")
			return
		}
	}
	items, err := handler.app.List(r.Context(), id, limit+1, before)
	if err != nil {
		writeAssetError(w, err)
		return
	}
	more := len(items) > limit
	if more {
		items = items[:limit]
	}
	projected := make([]map[string]any, 0, len(items))
	for _, item := range items {
		projected = append(projected, assetJSON(item))
	}
	next := ""
	if more && len(items) > 0 {
		next = strconv.FormatInt(items[len(items)-1].ID, 10)
	}
	writeChannelJSON(w, http.StatusOK, map[string]any{"items": projected, "limit": limit, "has_more": more, "next_cursor": next})
}
func (handler *AssetHTTPHandler) get(w http.ResponseWriter, r *http.Request, id int64, effect string) {
	if !handler.read(w, r) {
		return
	}
	if r.URL.RawQuery != "" || r.ContentLength > 0 {
		writeCatalogError(w, http.StatusBadRequest, "MALFORMED_REQUEST")
		return
	}
	asset, err := handler.app.Get(r.Context(), id, effect)
	if err != nil {
		writeAssetError(w, err)
		return
	}
	writeChannelJSON(w, http.StatusOK, assetJSON(asset))
}
func (handler *AssetHTTPHandler) downloadQR(w http.ResponseWriter, r *http.Request, id int64) {
	if !handler.export(w, r) {
		return
	}
	if r.URL.RawQuery != "" || r.ContentLength > 0 {
		writeCatalogError(w, http.StatusBadRequest, "MALFORMED_REQUEST")
		return
	}
	items, err := handler.app.List(r.Context(), id, 50, 0)
	if err != nil {
		writeAssetError(w, err)
		return
	}
	var asset *AcquisitionAsset
	for index := range items {
		if items[index].Kind == AcquisitionAssetQRCode && items[index].Operation != "delete" && items[index].RetiredAt == nil && (items[index].State == "executed" || items[index].State == "reconciled" || items[index].State == "legacy_verified_active") && items[index].ResultURL != "" {
			asset = &items[index]
			break
		}
	}
	if asset == nil {
		writeCatalogError(w, http.StatusNotFound, "NOT_FOUND")
		return
	}
	parsed, err := url.Parse(asset.ResultURL)
	if err != nil || parsed.Scheme != "https" || !allowedQRHost(parsed.Hostname()) {
		writeCatalogError(w, http.StatusServiceUnavailable, "ASSET_FETCH_BLOCKED")
		return
	}
	request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, parsed.String(), nil)
	if err != nil {
		writeCatalogError(w, http.StatusServiceUnavailable, "ASSET_FETCH_FAILED")
		return
	}
	response, err := handler.download.Do(request)
	if err != nil {
		writeCatalogError(w, http.StatusServiceUnavailable, "ASSET_FETCH_FAILED")
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.ContentLength > 5<<20 {
		writeCatalogError(w, http.StatusServiceUnavailable, "ASSET_FETCH_FAILED")
		return
	}
	kind, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if kind != "image/jpeg" && kind != "image/png" {
		writeCatalogError(w, http.StatusServiceUnavailable, "ASSET_FETCH_FAILED")
		return
	}
	w.Header().Set("Content-Type", kind)
	w.Header().Set("Content-Disposition", `attachment; filename="channel-qrcode.`+map[bool]string{true: "png", false: "jpg"}[kind == "image/png"]+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	limited := io.LimitReader(response.Body, (5<<20)+1)
	data, err := io.ReadAll(limited)
	if err != nil || len(data) > 5<<20 {
		writeCatalogError(w, http.StatusServiceUnavailable, "ASSET_FETCH_FAILED")
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}
func allowedQRHost(host string) bool {
	switch strings.ToLower(host) {
	case "wework.qpic.cn", "p.qpic.cn", "wework.qlogo.cn":
		return true
	default:
		return false
	}
}
func assetAcceptanceJSON(a AcquisitionAsset) map[string]any {
	return map[string]any{"effect_id": a.EffectRef, "channel_id": a.ChannelID, "kind": a.Kind, "operation": a.Operation, "origin": "runtime", "asset_version": a.AssetVersion, "supersedes_version": max64(a.AssetVersion-1, 0), "state": "queued", "accept_receipt_id": a.AcceptReceiptRef, "queue_receipt_id": a.QueueReceiptRef, "entrant_ready": false, "status_url": "/api/admin/channels/" + strconv.FormatInt(a.ChannelID, 10) + "/acquisition-assets/" + a.EffectRef, "retry_after_seconds": 1}
}
func assetJSON(a AcquisitionAsset) map[string]any {
	ready := (a.State == "executed" || a.State == "reconciled" || a.State == "legacy_verified_active") && a.Operation != "delete" && a.RetiredAt == nil
	origin := a.Origin
	if origin == "" {
		origin = "runtime"
	}
	value := map[string]any{"effect_id": a.EffectRef, "channel_id": a.ChannelID, "kind": a.Kind, "operation": a.Operation, "origin": origin, "asset_version": a.AssetVersion, "supersedes_version": max64(a.AssetVersion-1, 0), "state": a.State, "retired": a.RetiredAt != nil, "accept_receipt_id": a.AcceptReceiptRef, "queue_receipt_id": a.QueueReceiptRef, "entrant_ready": ready, "created_at": a.CreatedAt.UTC().Format(time.RFC3339Nano), "updated_at": a.UpdatedAt.UTC().Format(time.RFC3339Nano)}
	if ready {
		if a.Kind == AcquisitionAssetLink {
			value["asset_url"] = a.ResultURL
		} else {
			value["download_url"] = "/api/admin/channels/" + strconv.FormatInt(a.ChannelID, 10) + "/qrcode/download"
		}
	}
	return value
}
func writeAssetError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrCatalogNotFound):
		writeCatalogError(w, http.StatusNotFound, "NOT_FOUND")
	case errors.Is(err, ErrCatalogConflict):
		writeCatalogError(w, http.StatusConflict, "CONFLICT")
	case errors.Is(err, ErrInvalidCatalogCommand):
		writeCatalogError(w, http.StatusUnprocessableEntity, "INVALID_ASSET_COMMAND")
	default:
		writeCatalogError(w, http.StatusServiceUnavailable, "DEPENDENCY_UNAVAILABLE")
	}
}
func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
