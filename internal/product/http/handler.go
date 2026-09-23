// Package http exposes the bounded Product compatibility surface used by the
// frozen v2 admin bundle.  The handler only composes Product-owned
// applications; Media, Tag, Channel, OneID, orders, entitlements and member
// data stay behind their own ports (or are explicitly fail-closed).
package http

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	distributiondomain "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/domain"
	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
	hxcport "github.com/qianlan33333-png/AI-CRM-v3/internal/hxcdashboard/port"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	productapp "github.com/qianlan33333-png/AI-CRM-v3/internal/product/app"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

const (
	maxProductBodyBytes = 128 << 10
	maxProductLimit     = productapp.MaximumLimit
	maxProductOffset    = productapp.MaximumLegacyOffset
)

// RequestSecurity is the stable v3 request boundary.  A Product handler never
// trusts actor fields from the donor request body.
type RequestSecurity interface {
	Authenticate(context.Context, *http.Request) (accessdomain.Principal, error)
	AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error)
}

// CatalogApplication is the transport-neutral ordinary Product application.
// The concrete app service is supplied by the composition root.
type CatalogApplication interface {
	List(context.Context, string, int32) (productport.Page, error)
	Get(context.Context, productport.ID) (productport.Product, error)
	Create(context.Context, productport.CreateCommand) (productport.Product, error)
	Update(context.Context, productport.UpdateCommand) (productport.Product, error)
}

type Handler struct {
	catalog   CatalogApplication
	lifecycle productport.LocalProductLifecycleApplication
	service   productport.ServicePeriodApplication
	external  productport.CommerceExternalPushApplication
	security  RequestSecurity
	members   orderport.EntitlementService
	names     customerport.DirectoryDisplayNameReader
	// sharedFacts is the bounded, versioned HXC projection. It is optional:
	// a deployment before HXC has published facts still renders Order/Customer
	// fields and marks HXC-derived values unavailable.
	sharedFacts hxcport.VersionedSharedFactsReader
	// policyReader is a stable Distribution read port; Product never reads Distribution tables.
	policyReader distributionport.ProductPolicyReader
	// memberGridCursorKey signs only compact relation fingerprints and canonical
	// row references; never member names, remarks, or HXC values.
	memberGridCursorKey []byte
	workspace           productport.MemberGridWorkspace
	staff               productport.MemberGridStaffDirectory
}

// SetServicePeriodMemberWorkspace binds Product-owned local view,
// collaborator, and revocable-share metadata.  Authentication remains the
// existing Access boundary; this is not a second staff directory.
func (h *Handler) SetServicePeriodMemberWorkspace(workspace productport.MemberGridWorkspace) error {
	if h == nil || workspace == nil {
		return errors.New("service-period member workspace is required")
	}
	h.workspace = workspace
	return nil
}

// SetServicePeriodMemberStaffDirectory binds the existing Access projection
// for collaborator lookup. The Product HTTP host never queries admin_users.
func (h *Handler) SetServicePeriodMemberStaffDirectory(staff productport.MemberGridStaffDirectory) error {
	if h == nil || staff == nil {
		return errors.New("service-period member staff directory is required")
	}
	h.staff = staff
	return nil
}

// SetServicePeriodMemberReaders connects the Product-owned grid Host to the
// Order entitlement read/remark port and Customer's display-only projection.
// The Product module neither imports a Store nor reads another domain table.
func (h *Handler) SetServicePeriodMemberReaders(members orderport.EntitlementService, names customerport.DirectoryDisplayNameReader) error {
	if h == nil || members == nil || names == nil {
		return errors.New("service-period member readers are required")
	}
	h.members, h.names = members, names
	return nil
}

func (h *Handler) SetDistributionPolicyReader(reader distributionport.ProductPolicyReader) error {
	if h == nil || reader == nil {
		return errors.New("distribution policy reader is required")
	}
	h.policyReader = reader
	return nil
}

func NewHandler(catalog CatalogApplication, lifecycle productport.LocalProductLifecycleApplication, service productport.ServicePeriodApplication, external productport.CommerceExternalPushApplication, security RequestSecurity) (*Handler, error) {
	if catalog == nil || lifecycle == nil || service == nil || external == nil || security == nil {
		return nil, errors.New("product HTTP dependencies are required")
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	return &Handler{catalog: catalog, lifecycle: lifecycle, service: service, external: external, security: security, memberGridCursorKey: key}, nil
}

// SetServicePeriodMemberSharedFacts adds HXC's versioned, bounded projection
// to the read-only member-grid composition. Product receives no HXC store or
// raw external identifiers.
func (h *Handler) SetServicePeriodMemberSharedFacts(reader hxcport.VersionedSharedFactsReader) error {
	if h == nil || reader == nil {
		return errors.New("service-period member shared facts reader is required")
	}
	h.sharedFacts = reader
	return nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.catalog == nil || h.lifecycle == nil || h.service == nil || h.external == nil || h.security == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	path := strings.TrimSuffix(r.URL.Path, "/")
	switch {
	case path == "/api/public/service-period-member-grid/scoped-query":
		h.publicScopedQuery(w, r)
	case path == "/api/public/service-period-member-grid/bootstrap":
		h.publicMemberGridBootstrap(w, r)
	case path == "/api/public/service-period-member-grid/query":
		h.publicMemberGridQuery(w, r)
	case path == "/api/v1/products":
		h.ordinaryRoot(w, r)
	case strings.HasPrefix(path, "/api/v1/products/"):
		h.ordinaryTail(w, r, strings.TrimPrefix(path, "/api/v1/products/"))
	case path == "/api/admin/wechat-pay/products":
		h.ordinaryAdminRoot(w, r)
	case strings.HasPrefix(path, "/api/admin/wechat-pay/products/"):
		h.ordinaryAdminTail(w, r, strings.TrimPrefix(path, "/api/admin/wechat-pay/products/"))
	case path == "/api/admin/service-period-products":
		h.serviceRoot(w, r)
	case strings.HasPrefix(path, "/api/admin/service-period-products/"):
		h.serviceTail(w, r, strings.TrimPrefix(path, "/api/admin/service-period-products/"))
	default:
		writeError(w, http.StatusNotFound, "not_found")
	}
}

func (h *Handler) ordinaryRoot(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if !h.read(w, r) {
			return
		}
		if !onlyQuery(r, "cursor", "limit") {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		limit, ok := queryLimit(r, productapp.DefaultLimit)
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		cursor := r.URL.Query().Get("cursor")
		page, err := h.catalog.List(r.Context(), cursor, limit)
		if err != nil {
			resultError(w, err)
			return
		}
		items := make([]productResponse, 0, len(page.Items))
		for _, item := range page.Items {
			projected, projectionErr := h.productResponse(r.Context(), item)
			if projectionErr != nil {
				resultError(w, projectionErr)
				return
			}
			items = append(items, projected)
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": page.NextCursor})
	case http.MethodPost:
		principal, ok := h.write(w, r)
		if !ok {
			return
		}
		if r.URL.RawQuery != "" {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		var body createRequest
		if decodeJSON(r, &body) != nil {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		key, err := requestIdempotencyKey(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		projection := body.AdminProjection
		if len(projection) == 0 {
			projection = productapp.DefaultLegacyAdminProjection()
		}
		projection, err = productapp.EnabledLegacyAdminProjectionForCreate(projection)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		product, err := h.catalog.Create(r.Context(), productport.CreateCommand{
			ProductCode: body.ProductCode, Name: body.Name, Description: body.Description,
			PriceMinor: body.PriceMinor, Currency: body.Currency, StockQuantity: body.StockQuantity,
			Images: body.Images, LegacyAdminProjection: projection, Actor: principal.InternalID,
			DistributionPolicy: body.DistributionPolicy.command(), IdempotencyKey: key,
		})
		if err != nil {
			resultError(w, err)
			return
		}
		projected, projectionErr := h.productResponse(r.Context(), product)
		if projectionErr != nil {
			resultError(w, projectionErr)
			return
		}
		writeJSON(w, http.StatusCreated, projected)
	default:
		methodNotAllowed(w, http.MethodGet+", "+http.MethodPost)
	}
}

func (h *Handler) ordinaryTail(w http.ResponseWriter, r *http.Request, tail string) {
	parts := strings.Split(tail, "/")
	if len(parts) == 2 && parts[1] == "local-entitlements" {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		if !h.read(w, r) || !onlyQuery(r, "limit") {
			if r.URL.RawQuery != "" && !onlyQuery(r, "limit") {
				writeError(w, http.StatusBadRequest, "invalid_request")
			}
			return
		}
		id, err := parseID(parts[0])
		if err != nil {
			writeError(w, http.StatusNotFound, "not_found")
			return
		}
		limit, ok := queryLimit(r, 100)
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		if limit > 100 {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		// Entitlements are intentionally not owned by Product in PR04.  Validate
		// the Product through its application and return a truthful empty local
		// projection; no order/customer/entitlement table is queried.
		if _, err = h.catalog.Get(r.Context(), productport.ID(id)); err != nil {
			resultError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": []any{}})
		return
	}
	if len(parts) != 1 {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	id, err := parseID(parts[0])
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	switch r.Method {
	case http.MethodGet:
		if !h.read(w, r) {
			return
		}
		if r.URL.RawQuery != "" {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		product, getErr := h.catalog.Get(r.Context(), productport.ID(id))
		if getErr != nil {
			resultError(w, getErr)
			return
		}
		projected, projectionErr := h.productResponse(r.Context(), product)
		if projectionErr != nil {
			resultError(w, projectionErr)
			return
		}
		writeJSON(w, http.StatusOK, projected)
	case http.MethodPut:
		principal, ok := h.write(w, r)
		if !ok {
			return
		}
		if r.URL.RawQuery != "" {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		var body updateRequest
		if decodeJSON(r, &body) != nil {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		key, keyErr := requestIdempotencyKey(r)
		if keyErr != nil {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		product, updateErr := h.catalog.Update(r.Context(), productport.UpdateCommand{
			ID: productport.ID(id), ExpectedVersion: body.ExpectedVersion, Name: body.Name,
			Description: body.Description, PriceMinor: body.PriceMinor, Currency: body.Currency,
			StockQuantity: body.StockQuantity, Images: body.Images, LegacyAdminProjection: body.AdminProjection,
			Actor: principal.InternalID, DistributionPolicy: body.DistributionPolicy.command(), IdempotencyKey: key,
		})
		if updateErr != nil {
			resultError(w, updateErr)
			return
		}
		projected, projectionErr := h.productResponse(r.Context(), product)
		if projectionErr != nil {
			resultError(w, projectionErr)
			return
		}
		writeJSON(w, http.StatusOK, projected)
	default:
		methodNotAllowed(w, http.MethodGet+", "+http.MethodPut)
	}
}

func (h *Handler) ordinaryAdminRoot(w http.ResponseWriter, r *http.Request) {
	// Compatibility callers use the same Product command and nested policy
	// DTO as /api/v1/products. There is one Product UoW, never a policy follow-up.
	switch r.Method {
	case http.MethodGet, http.MethodPost:
		h.ordinaryRoot(w, r)
	default:
		methodNotAllowed(w, http.MethodGet+", "+http.MethodPost)
	}
}

func (h *Handler) ordinaryAdminTail(w http.ResponseWriter, r *http.Request, tail string) {
	parts := strings.Split(tail, "/")
	if len(parts) < 1 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	id, err := parseID(parts[0])
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	switch strings.Join(parts[1:], "/") {
	case "enable":
		h.localEnable(w, r, id, true)
	case "disable":
		h.localEnable(w, r, id, false)
	case "copy":
		h.localCopy(w, r, id)
	case "share":
		h.localShare(w, r, id)
	case "external-push":
		h.externalRoute(w, r, id, productport.ExternalPushWeChatPay)
	case "external-push/preview":
		h.externalPreview(w, r, id, productport.ExternalPushWeChatPay)
	case "external-push/test":
		h.externalTest(w, r, id, productport.ExternalPushWeChatPay)
	case "":
		switch r.Method {
		case http.MethodGet, http.MethodPut:
			h.ordinaryTail(w, r, parts[0])
		case http.MethodDelete:
			h.localDelete(w, r, id)
		default:
			methodNotAllowed(w, http.MethodGet+", "+http.MethodPut+", "+http.MethodDelete)
		}
	default:
		writeError(w, http.StatusNotFound, "not_found")
	}
}

func (h *Handler) localEnable(w http.ResponseWriter, r *http.Request, id int64, enabled bool) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	principal, ok := h.write(w, r)
	if !ok {
		return
	}
	var body versionRequest
	if decodeJSON(r, &body) != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	key, err := requestIdempotencyKey(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	result, err := h.lifecycle.SetLocalProductEnabled(r.Context(), productport.SetLocalProductEnabledCommand{ID: productport.ID(id), ExpectedVersion: body.ExpectedVersion, Enabled: enabled, Actor: principal.InternalID, IdempotencyKey: key})
	if err != nil {
		resultError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) localCopy(w http.ResponseWriter, r *http.Request, id int64) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	principal, ok := h.write(w, r)
	if !ok {
		return
	}
	var body versionRequest
	if decodeJSON(r, &body) != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	key, err := requestIdempotencyKey(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	result, err := h.lifecycle.CopyLocalProduct(r.Context(), productport.CopyLocalProductCommand{ID: productport.ID(id), ExpectedVersion: body.ExpectedVersion, Actor: principal.InternalID, IdempotencyKey: key})
	if err != nil {
		resultError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

func (h *Handler) localDelete(w http.ResponseWriter, r *http.Request, id int64) {
	principal, ok := h.write(w, r)
	if !ok {
		return
	}
	var body versionRequest
	if decodeOptionalJSON(r, &body) != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	key, err := requestIdempotencyKey(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	result, err := h.lifecycle.ArchiveLocalProduct(r.Context(), productport.ArchiveLocalProductCommand{ID: productport.ID(id), ExpectedVersion: body.ExpectedVersion, Actor: principal.InternalID, IdempotencyKey: key})
	if err != nil {
		resultError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) localShare(w http.ResponseWriter, r *http.Request, id int64) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	if !h.read(w, r) {
		return
	}
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	result, err := h.lifecycle.ShareLocalProduct(r.Context(), productport.ID(id))
	if err != nil {
		if errors.Is(err, productapp.ErrLocalProductNotEnabled) {
			writeError(w, http.StatusConflict, "product_not_enabled")
			return
		}
		resultError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) serviceRoot(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if !h.read(w, r) {
			return
		}
		if !onlyQuery(r, "limit", "offset") {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		limit, ok := queryLimit(r, productapp.DefaultLimit)
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		offset, ok := queryOffset(r)
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		page, err := h.service.ListServicePeriodProducts(r.Context(), limit, offset)
		if err != nil {
			resultError(w, err)
			return
		}
		items := make([]servicePeriodResponse, 0, len(page.Items))
		for _, item := range page.Items {
			response, responseErr := h.servicePeriodResponse(r.Context(), item)
			if responseErr != nil {
				resultError(w, responseErr)
				return
			}
			items = append(items, response)
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": page.OK, "items": items, "total": page.Total, "limit": page.Limit, "offset": page.Offset})
	case http.MethodPost:
		principal, ok := h.write(w, r)
		if !ok {
			return
		}
		if r.URL.RawQuery != "" {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		var body serviceCreateRequest
		if decodeJSON(r, &body) != nil {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		key, err := requestIdempotencyKey(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		projection := body.AdminProjection
		if len(projection) == 0 {
			projection = productapp.DefaultLegacyAdminProjection()
		}
		product, err := h.service.CreateServicePeriodProduct(r.Context(), productport.CreateServicePeriodProductCommand{
			ProductCode: body.ProductCode, Name: body.Name, Description: body.Description,
			PriceMinor: body.PriceMinor, Currency: body.Currency, DurationDays: body.DurationDays, StockQuantity: body.StockQuantity,
			Images: body.Images, AdminProjection: projection, Actor: principal.InternalID, DistributionPolicy: body.DistributionPolicy.command(), IdempotencyKey: key,
		})
		if err != nil {
			resultError(w, err)
			return
		}
		response, responseErr := h.servicePeriodResponse(r.Context(), product)
		if responseErr != nil {
			resultError(w, responseErr)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "product": response})
	default:
		methodNotAllowed(w, http.MethodGet+", "+http.MethodPost)
	}
}

func (h *Handler) serviceTail(w http.ResponseWriter, r *http.Request, tail string) {
	parts := strings.Split(tail, "/")
	if len(parts) < 1 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	id, err := parseID(parts[0])
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if len(parts) == 1 {
		h.serviceDetail(w, r, id)
		return
	}
	suffix := strings.Join(parts[1:], "/")
	switch {
	case suffix == "enable":
		h.serviceEnable(w, r, id, true)
	case suffix == "disable":
		h.serviceEnable(w, r, id, false)
	case suffix == "copy":
		h.serviceCopy(w, r, id)
	case suffix == "share":
		h.serviceShare(w, r, id)
	case suffix == "external-push":
		h.externalRoute(w, r, id, productport.ExternalPushServicePeriod)
	case suffix == "external-push/preview":
		h.externalPreview(w, r, id, productport.ExternalPushServicePeriod)
	case suffix == "external-push/test":
		h.externalTest(w, r, id, productport.ExternalPushServicePeriod)
	case suffix == "members":
		h.serviceMembers(w, r, id)
	case strings.HasPrefix(suffix, "members/") && strings.HasSuffix(suffix, "/remark"):
		h.memberRemark(w, r, id, strings.TrimSuffix(strings.TrimPrefix(suffix, "members/"), "/remark"))
	case strings.HasPrefix(suffix, "members/") && strings.HasSuffix(suffix, "/alliance"):
		h.memberAlliance(w, r, id, strings.TrimSuffix(strings.TrimPrefix(suffix, "members/"), "/alliance"))
	case suffix == "member-grid/access":
		h.memberGridAccess(w, r, id)
	case suffix == "member-grid/schema":
		h.memberGridSchema(w, r, id)
	case suffix == "member-views":
		h.memberViews(w, r, id)
	case strings.HasPrefix(suffix, "member-views/"):
		h.memberView(w, r, id, strings.TrimPrefix(suffix, "member-views/"))
	case suffix == "member-grid/scoped-shares":
		h.scopedShares(w, r, id)
	case suffix == "member-grid/query":
		h.memberGridQuery(w, r, id)
	case suffix == "member-grid/collaborators":
		h.memberGridCollaborators(w, r, id)
	case strings.HasPrefix(suffix, "member-grid/collaborators/"):
		h.memberGridCollaborator(w, r, id, strings.TrimPrefix(suffix, "member-grid/collaborators/"))
	case suffix == "member-grid/staff":
		h.memberGridStaff(w, r, id)
	case suffix == "member-grid/share-settings":
		h.memberGridShareSettings(w, r, id)
	case suffix == "member-grid/external-share":
		h.memberGridExternalShare(w, r, id)
	default:
		// Member Grid data, writes, history and customer/entitlement joins are
		// outside PR04. Unknown and mutating paths fail closed.
		writeError(w, http.StatusNotFound, "not_found")
	}
}

func (h *Handler) serviceDetail(w http.ResponseWriter, r *http.Request, id int64) {
	switch r.Method {
	case http.MethodGet:
		if !h.read(w, r) {
			return
		}
		if r.URL.RawQuery != "" {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		product, err := h.service.GetServicePeriodProduct(r.Context(), productport.ID(id))
		if err != nil {
			resultError(w, err)
			return
		}
		response, responseErr := h.servicePeriodResponse(r.Context(), product)
		if responseErr != nil {
			resultError(w, responseErr)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "product": response})
	case http.MethodPut:
		principal, ok := h.write(w, r)
		if !ok {
			return
		}
		if r.URL.RawQuery != "" {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		var body serviceUpdateRequest
		if decodeJSON(r, &body) != nil {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		key, err := requestIdempotencyKey(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		product, err := h.service.UpdateServicePeriodProduct(r.Context(), productport.UpdateServicePeriodProductCommand{
			ID: productport.ID(id), ExpectedVersion: body.ExpectedVersion, Name: body.Name,
			Description: body.Description, PriceMinor: body.PriceMinor, Currency: body.Currency,
			DurationDays: body.DurationDays, StockQuantity: body.StockQuantity, Images: body.Images, AdminProjection: body.AdminProjection,
			Actor: principal.InternalID, DistributionPolicy: body.DistributionPolicy.command(), IdempotencyKey: key,
		})
		if err != nil {
			resultError(w, err)
			return
		}
		response, responseErr := h.servicePeriodResponse(r.Context(), product)
		if responseErr != nil {
			resultError(w, responseErr)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "product": response})
	case http.MethodDelete:
		principal, ok := h.write(w, r)
		if !ok {
			return
		}
		var body versionRequest
		if decodeJSON(r, &body) != nil {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		key, err := requestIdempotencyKey(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		product, err := h.service.ArchiveServicePeriodProduct(r.Context(), productport.ArchiveServicePeriodProductCommand{ID: productport.ID(id), ExpectedVersion: body.ExpectedVersion, Actor: principal.InternalID, IdempotencyKey: key})
		if err != nil {
			resultError(w, err)
			return
		}
		response, responseErr := h.servicePeriodResponse(r.Context(), product)
		if responseErr != nil {
			resultError(w, responseErr)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "product": response})
	default:
		methodNotAllowed(w, http.MethodGet+", "+http.MethodPut+", "+http.MethodDelete)
	}
}

func (h *Handler) serviceEnable(w http.ResponseWriter, r *http.Request, id int64, enabled bool) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	principal, ok := h.write(w, r)
	if !ok {
		return
	}
	var body versionRequest
	if decodeJSON(r, &body) != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	key, err := requestIdempotencyKey(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	product, err := h.service.SetServicePeriodProductEnabled(r.Context(), productport.SetServicePeriodProductEnabledCommand{ID: productport.ID(id), ExpectedVersion: body.ExpectedVersion, Enabled: enabled, Actor: principal.InternalID, IdempotencyKey: key})
	if err != nil {
		resultError(w, err)
		return
	}
	response, responseErr := h.servicePeriodResponse(r.Context(), product)
	if responseErr != nil {
		resultError(w, responseErr)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "product": response})
}

func (h *Handler) serviceCopy(w http.ResponseWriter, r *http.Request, id int64) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	principal, ok := h.write(w, r)
	if !ok {
		return
	}
	var body versionRequest
	if decodeJSON(r, &body) != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	key, err := requestIdempotencyKey(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	product, err := h.service.CopyServicePeriodProduct(r.Context(), productport.CopyServicePeriodProductCommand{ID: productport.ID(id), ExpectedVersion: body.ExpectedVersion, Actor: principal.InternalID, IdempotencyKey: key})
	if err != nil {
		resultError(w, err)
		return
	}
	response, responseErr := h.servicePeriodResponse(r.Context(), product)
	if responseErr != nil {
		resultError(w, responseErr)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "product": response})
}

func (h *Handler) serviceShare(w http.ResponseWriter, r *http.Request, id int64) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	if !h.read(w, r) {
		return
	}
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	product, err := h.service.GetServicePeriodProduct(r.Context(), productport.ID(id))
	if err != nil {
		resultError(w, err)
		return
	}
	if product.Lifecycle != productport.ServicePeriodEnabled {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "service_product_id": id, "product_code": product.ProductCode, "public_path": "/s/" + url.PathEscape(product.ProductCode), "local_only": true, "real_external_call_executed": false})
}

func (h *Handler) externalRoute(w http.ResponseWriter, r *http.Request, id int64, kind productport.ExternalPushProductKind) {
	switch r.Method {
	case http.MethodGet:
		if !h.read(w, r) {
			return
		}
		if r.URL.RawQuery != "" {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		configuration, err := h.external.GetExternalPushConfiguration(r.Context(), productport.ID(id), kind)
		if err != nil {
			resultError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, externalConfigurationJSONResponse(configuration))
	case http.MethodPut:
		principal, ok := h.write(w, r)
		if !ok {
			return
		}
		if r.URL.RawQuery != "" {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		var body externalConfigurationRequest
		if decodeJSON(r, &body) != nil {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		key, err := requestIdempotencyKey(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		businessSet, business, valid := externalConfigurationBusiness(body)
		if !valid {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		var mapping *productport.FieldMapping
		if len(body.FieldMapping) > 0 {
			var e error
			mapping, e = productport.DecodeFieldMapping(body.FieldMapping)
			if e != nil || !businessSet {
				writeError(w, http.StatusBadRequest, "invalid_request")
				return
			}
		}
		endpoint := body.URL
		if body.WebhookURL != nil {
			endpoint = body.WebhookURL
		}
		configuration, err := h.external.SaveExternalPushConfiguration(r.Context(), productport.SaveExternalPushConfigurationCommand{
			ProductID: productport.ID(id), ProductKind: kind, Enabled: body.Enabled, ConfigurationReference: body.ConfigurationReference,
			FieldMapping: mapping, FieldMappingSet: len(body.FieldMapping) > 0, URL: endpoint, BusinessParametersSet: businessSet, PushType: business.pushType, Day: business.day, Frequency: business.frequency, ExpiresAtTS: business.expiresAtTS, Remark: business.remark, CustomParams: business.customParams,
			ExpectedRevision: business.expectedRevision, Actor: principal.InternalID, IdempotencyKey: key,
		})
		if err != nil {
			resultError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, externalConfigurationJSONResponse(configuration))
	default:
		methodNotAllowed(w, http.MethodGet+", "+http.MethodPut)
	}
}

func (h *Handler) externalTest(w http.ResponseWriter, r *http.Request, id int64, kind productport.ExternalPushProductKind) {
	switch r.Method {
	case http.MethodGet:
		if !h.read(w, r) {
			return
		}
		if r.URL.RawQuery != "" {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		items, err := h.external.ListExternalPushTests(r.Context(), productport.ID(id), kind)
		if err != nil {
			resultError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	case http.MethodPost:
		principal, ok := h.write(w, r)
		if !ok {
			return
		}
		if r.URL.RawQuery != "" {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		if err := decodeEmptyJSON(r); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		key, err := requestIdempotencyKey(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		test, err := h.external.QueueExternalPushTest(r.Context(), productport.QueueExternalPushTestCommand{ProductID: productport.ID(id), ProductKind: kind, Actor: principal.InternalID, IdempotencyKey: key})
		if err != nil {
			resultError(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, test)
	default:
		methodNotAllowed(w, http.MethodGet+", "+http.MethodPost)
	}
}

func (h *Handler) serviceMembers(w http.ResponseWriter, r *http.Request, id int64) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	access, _, ok := h.memberGridAuthorize(w, r, id, false)
	if !ok {
		return
	}
	if !access.CanView {
		writeError(w, http.StatusForbidden, "permission_denied")
		return
	}
	if !onlyQuery(r, "state", "source", "limit", "cursor") {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	query := r.URL.Query()
	if raw := query.Get("state"); raw != "" && raw != "active" && raw != "expired" && raw != "removed" && raw != "all" {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if raw := query.Get("source"); raw != "" && raw != "manual" && raw != "paid_order" {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if len(query.Get("cursor")) > 1024 {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	limit, ok := queryLimit(r, productapp.DefaultLimit)
	if !ok || limit > 100 {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if _, err := h.service.GetServicePeriodProduct(r.Context(), productport.ID(id)); err != nil {
		resultError(w, err)
		return
	}
	if h.members == nil || h.names == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	page, err := h.members.ListServicePeriodMembers(r.Context(), orderport.ServicePeriodMemberQuery{ServiceProductID: id, State: query.Get("state"), Source: query.Get("source"), Cursor: query.Get("cursor"), Limit: limit})
	if err != nil {
		resultError(w, err)
		return
	}
	ids := make([]customerdomain.CustomerID, 0, len(page.Items))
	for _, item := range page.Items {
		ids = append(ids, customerdomain.CustomerID(item.CustomerID))
	}
	names, err := h.names.DisplayNames(r.Context(), ids)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	items := make([]map[string]any, 0, len(page.Items))
	for _, item := range page.Items {
		state := item.Status
		if state == "refunded" {
			state = "removed"
		}
		source := "manual"
		if item.SourceSystem == "native-payment" {
			source = "paid_order"
		}
		name := names[customerdomain.CustomerID(item.CustomerID)]
		if name == "" {
			name = "客户 #" + strconv.FormatInt(item.CustomerID, 10)
		}
		items = append(items, map[string]any{"member_ref": memberGridMemberRef(item.ID), "entitlement_id": item.ID, "service_product_id": item.ServiceProductID, "customer_id": item.CustomerID, "display_name": name, "state": state, "source": source, "starts_at": item.StartAt.UTC(), "expires_at": item.EndAt.UTC(), "remark": item.Remark, "alliance": item.Alliance, "version": item.Version, "updated_at": item.UpdatedAt.UTC()})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "limit": limit, "next_cursor": page.NextCursor, "has_more": page.NextCursor != ""})
}

func (h *Handler) memberRemark(w http.ResponseWriter, r *http.Request, productID int64, rawEntitlementID string) {
	if r.Method != http.MethodPut {
		methodNotAllowed(w, http.MethodPut)
		return
	}
	access, actor, ok := h.memberGridAuthorize(w, r, productID, true)
	if !ok {
		return
	}
	if !access.CanEdit {
		writeError(w, http.StatusForbidden, "permission_denied")
		return
	}
	if h.members == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	entitlementID, err := parseMemberGridMemberRef(rawEntitlementID)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	var body struct {
		CustomerID int64  `json:"customer_id"`
		Remark     string `json:"remark"`
		Version    int64  `json:"version"`
	}
	if decodeJSON(r, &body) != nil || body.CustomerID < 0 || body.Version < 1 {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	key, err := requestIdempotencyKey(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	result, err := h.members.UpdateEntitlementRemark(r.Context(), orderport.RemarkCommand{EntitlementID: entitlementID, CustomerID: body.CustomerID, ServiceProductID: productID, EmployeeID: strconv.FormatInt(actor.AdminUserID, 10), Remark: body.Remark, ExpectedVersion: body.Version, IdempotencyKey: key})
	if err != nil {
		resultError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "member_ref": memberGridMemberRef(result.ID), "remark": result.Remark, "version": result.Version, "updated_at": result.UpdatedAt.UTC()})
}

func (h *Handler) memberAlliance(w http.ResponseWriter, r *http.Request, productID int64, rawEntitlementID string) {
	if r.Method != http.MethodPut {
		methodNotAllowed(w, http.MethodPut)
		return
	}
	access, actor, ok := h.memberGridAuthorize(w, r, productID, true)
	if !ok {
		return
	}
	if !access.CanEdit {
		writeError(w, http.StatusForbidden, "permission_denied")
		return
	}
	if h.members == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	entitlementID, err := parseMemberGridMemberRef(rawEntitlementID)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	var body struct {
		CustomerID int64  `json:"customer_id"`
		Alliance   string `json:"alliance"`
		Version    int64  `json:"version"`
	}
	if decodeJSON(r, &body) != nil || body.CustomerID < 0 || body.Version < 1 {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	key, err := requestIdempotencyKey(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	result, err := h.members.UpdateEntitlementAlliance(r.Context(), orderport.AllianceCommand{EntitlementID: entitlementID, CustomerID: body.CustomerID, ServiceProductID: productID, EmployeeID: strconv.FormatInt(actor.AdminUserID, 10), Alliance: body.Alliance, ExpectedVersion: body.Version, IdempotencyKey: key})
	if err != nil {
		resultError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "member_ref": memberGridMemberRef(result.ID), "alliance": result.Alliance, "version": result.Version, "updated_at": result.UpdatedAt.UTC()})
}

func (h *Handler) memberGridAccess(w http.ResponseWriter, r *http.Request, id int64) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	_, actor, ok := h.memberGridAuthorize(w, r, id, false)
	if !ok {
		return
	}
	access, err := h.workspace.Access(r.Context(), productport.ID(id), actor)
	if err != nil {
		writeError(w, http.StatusForbidden, "permission_denied")
		return
	}
	value := map[string]any{"can_read": access.CanView, "can_edit": access.CanEdit, "can_manage_views": access.CanManageViews, "can_edit_cells": access.CanEdit, "can_manage_share": access.CanShare}
	writeJSON(w, http.StatusOK, map[string]any{"product_id": id, "access": value, "can_view": access.CanView, "can_query": access.CanView, "can_edit": access.CanEdit, "can_manage_views": access.CanManageViews, "can_share": access.CanShare})
}

func (h *Handler) memberGridSchema(w http.ResponseWriter, r *http.Request, id int64) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	access, _, ok := h.memberGridAuthorize(w, r, id, false)
	if !ok {
		return
	}
	if !access.CanView {
		writeError(w, http.StatusForbidden, "permission_denied")
		return
	}
	columns := []map[string]any{
		{"key": "member_ref", "label": "成员引用", "type": "string", "nullable": false},
		{"key": "service_product_id", "label": "周期商品", "type": "integer", "nullable": false},
		{"key": "customer_id", "label": "客户", "type": "integer", "nullable": false},
		{"key": "state", "label": "状态", "type": "enum", "nullable": false},
		{"key": "source", "label": "来源", "type": "enum", "nullable": false},
		{"key": "starts_at", "label": "开始时间", "type": "timestamp", "nullable": false},
		{"key": "expires_at", "label": "到期时间", "type": "timestamp", "nullable": true},
		{"key": "expired_at", "label": "过期时间", "type": "timestamp", "nullable": true},
		{"key": "removed_at", "label": "移除时间", "type": "timestamp", "nullable": true},
		{"key": "version", "label": "版本", "type": "integer", "nullable": false},
		{"key": "updated_at", "label": "更新时间", "type": "timestamp", "nullable": false},
		{"key": "display_name", "label": "显示名", "type": "string", "nullable": false},
		{"key": "remark", "label": "备注", "type": "string", "nullable": true, "editable": true},
	}
	writeJSON(w, http.StatusOK, map[string]any{"service_product_id": id, "columns": columns, "schema": donorGridSchema(access.CanEdit)})
}

func (h *Handler) memberViews(w http.ResponseWriter, r *http.Request, id int64) {
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	switch r.Method {
	case http.MethodGet:
		access, _, ok := h.memberGridAuthorize(w, r, id, false)
		if !ok {
			return
		}
		if !access.CanView {
			writeError(w, http.StatusForbidden, "permission_denied")
			return
		}
		views, err := h.workspace.ListViews(r.Context(), productport.ID(id))
		if err != nil {
			resultError(w, err)
			return
		}
		out := []any{defaultDonorGridView()}
		for _, view := range views {
			out = append(out, donorGridViewResponse(view))
		}
		writeJSON(w, http.StatusOK, map[string]any{"product_id": id, "views": out, "items": out})
	case http.MethodPost:
		access, actor, ok := h.memberGridAuthorize(w, r, id, true)
		if !ok {
			return
		}
		if !access.CanManageViews {
			writeError(w, http.StatusForbidden, "permission_denied")
			return
		}
		var body struct {
			Name   string          `json:"name"`
			Config json.RawMessage `json:"config"`
		}
		if decodeJSON(r, &body) != nil {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		config, err := decodeDonorGridConfig(body.Config)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		raw, _ := json.Marshal(config)
		key, err := requestIdempotencyKey(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		view, err := h.workspace.CreateView(r.Context(), productport.CreateMemberGridViewCommand{ProductID: productport.ID(id), Name: body.Name, Config: raw, Actor: actor, IdempotencyKey: key})
		if err != nil {
			resultError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "view": donorGridViewResponse(view)})
	default:
		methodNotAllowed(w, http.MethodGet+", "+http.MethodPost)
	}
}

func (h *Handler) memberGridShareSettings(w http.ResponseWriter, r *http.Request, id int64) {
	if r.URL.RawQuery != "" || r.Method != http.MethodGet {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
		} else {
			writeError(w, http.StatusBadRequest, "invalid_request")
		}
		return
	}
	access, _, ok := h.memberGridAuthorize(w, r, id, false)
	if !ok {
		return
	}
	if !access.CanView {
		writeError(w, http.StatusForbidden, "permission_denied")
		return
	}
	collaborators, err := h.workspace.ListCollaborators(r.Context(), productport.ID(id))
	if err != nil {
		resultError(w, err)
		return
	}
	items := make([]any, 0, len(collaborators))
	for _, collaborator := range collaborators {
		items = append(items, h.donorGridCollaboratorResponse(r.Context(), collaborator))
	}
	share, err := h.workspace.Share(r.Context(), productport.ID(id))
	if err != nil {
		resultError(w, err)
		return
	}
	external := map[string]any{"enabled": share.Enabled, "version": share.Version, "url": ""}
	if access.CanShare && share.Enabled {
		external["url"] = "/shared/service-period-member-grid#" + share.PublicID
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "service_product_id": id, "collaborators": items, "external_share": external, "external_share_supported": true, "external_share_enabled": share.Enabled, "external_share_version": share.Version, "real_external_call_executed": false})
}

func (h *Handler) memberGridExternalShare(w http.ResponseWriter, r *http.Request, id int64) {
	if r.Method != http.MethodPut {
		methodNotAllowed(w, http.MethodPut)
		return
	}
	access, actor, ok := h.memberGridAuthorize(w, r, id, true)
	if !ok {
		return
	}
	if !access.CanShare {
		writeError(w, http.StatusForbidden, "permission_denied")
		return
	}
	var body struct {
		Enabled bool  `json:"enabled"`
		Version int64 `json:"version"`
	}
	if decodeJSON(r, &body) != nil || body.Version < 0 {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	key, err := requestIdempotencyKey(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	share, issued, err := h.workspace.SetShare(r.Context(), productport.SetMemberGridShareCommand{ProductID: productport.ID(id), Enabled: body.Enabled, ExpectedVersion: body.Version, Actor: actor, IdempotencyKey: key})
	if err != nil {
		resultError(w, err)
		return
	}
	external := map[string]any{"enabled": share.Enabled, "version": share.Version, "url": ""}
	if share.Enabled {
		external["url"] = "/shared/service-period-member-grid#" + share.PublicID
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "external_share": external, "token_issued": issued, "real_external_call_executed": false})
}

func (h *Handler) memberGridStaff(w http.ResponseWriter, r *http.Request, id int64) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	access, _, ok := h.memberGridAuthorize(w, r, id, false)
	if !ok {
		return
	}
	if !access.CanShare {
		writeError(w, http.StatusForbidden, "permission_denied")
		return
	}
	if h.staff == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	staff, err := h.staff.ListActiveMemberGridStaff(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	items := make([]any, 0, len(staff))
	for _, item := range staff {
		items = append(items, map[string]any{"user_id": item.WeComUserID, "display_name": item.DisplayName})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) donorGridCollaboratorResponse(ctx context.Context, v productport.MemberGridCollaborator) map[string]any {
	out := map[string]any{"id": strconv.FormatInt(int64(v.ID), 10), "service_product_id": v.ProductID, "permission": v.Permission, "version": v.Version, "implicit": false, "display_name": "协作者", "wecom_userid": ""}
	if h.staff == nil {
		return out
	}
	staff, found, err := h.staff.MemberGridStaffByID(ctx, v.AdminUserID)
	if err == nil && found && staff.Active {
		out["display_name"] = staff.DisplayName
		out["wecom_userid"] = staff.WeComUserID
	}
	return out
}

func (h *Handler) memberGridAuthorize(w http.ResponseWriter, r *http.Request, id int64, mutate bool) (productport.MemberGridAccess, productport.MemberGridActor, bool) {
	if h.workspace == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable")
		return productport.MemberGridAccess{}, productport.MemberGridActor{}, false
	}
	principal, err := h.security.Authenticate(r.Context(), r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return productport.MemberGridAccess{}, productport.MemberGridActor{}, false
	}
	if !canRead(principal) {
		writeError(w, http.StatusForbidden, "permission_denied")
		return productport.MemberGridAccess{}, productport.MemberGridActor{}, false
	}
	writeAllowed := canWrite(principal)
	if mutate && !writeAllowed {
		writeError(w, http.StatusForbidden, "permission_denied")
		return productport.MemberGridAccess{}, productport.MemberGridActor{}, false
	}
	if mutate {
		if _, err = h.security.AuthorizeCSRF(r.Context(), r); err != nil {
			writeError(w, http.StatusForbidden, "csrf_required")
			return productport.MemberGridAccess{}, productport.MemberGridActor{}, false
		}
	}
	actor := productport.MemberGridActor{AdminUserID: principal.InternalID, IsSuperAdmin: principal.IsSuperAdmin()}
	actor.IsAdmin = writeAllowed
	access, err := h.workspace.Access(r.Context(), productport.ID(id), actor)
	if err != nil {
		writeError(w, http.StatusForbidden, "permission_denied")
		return productport.MemberGridAccess{}, productport.MemberGridActor{}, false
	}
	if !writeAllowed {
		// A local collaborator grant can refine an administrator's product
		// workspace access, but it can never turn the global viewer role into a
		// write, export, or share capability.
		access.CanEdit = false
		access.CanManageViews = false
		access.CanShare = false
	}
	return access, actor, true
}

func (h *Handler) memberView(w http.ResponseWriter, r *http.Request, productID int64, rawID string) {
	viewID, err := parseID(rawID)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	access, actor, ok := h.memberGridAuthorize(w, r, productID, true)
	if !ok {
		return
	}
	if !access.CanManageViews {
		writeError(w, http.StatusForbidden, "permission_denied")
		return
	}
	switch r.Method {
	case http.MethodPut:
		var body struct {
			Version int64           `json:"version"`
			Name    string          `json:"name"`
			Config  json.RawMessage `json:"config"`
		}
		if decodeJSON(r, &body) != nil || body.Version < 1 {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		config, err := decodeDonorGridConfig(body.Config)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		raw, _ := json.Marshal(config)
		key, err := requestIdempotencyKey(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		view, err := h.workspace.UpdateView(r.Context(), productport.UpdateMemberGridViewCommand{ProductID: productport.ID(productID), ViewID: productport.ID(viewID), ExpectedVersion: body.Version, Name: body.Name, Config: raw, Actor: actor, IdempotencyKey: key})
		if err != nil {
			resultError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "view": donorGridViewResponse(view)})
	case http.MethodDelete:
		var body struct {
			Version int64 `json:"version"`
		}
		if decodeJSON(r, &body) != nil || body.Version < 1 {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		key, err := requestIdempotencyKey(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		view, err := h.workspace.DeleteView(r.Context(), productport.DeleteMemberGridViewCommand{ProductID: productport.ID(productID), ViewID: productport.ID(viewID), ExpectedVersion: body.Version, Actor: actor, IdempotencyKey: key})
		if err != nil {
			resultError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "deleted": true, "view": donorGridViewResponse(view)})
	default:
		methodNotAllowed(w, http.MethodPut+", "+http.MethodDelete)
	}
}

func (h *Handler) memberGridCollaborators(w http.ResponseWriter, r *http.Request, id int64) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	access, actor, ok := h.memberGridAuthorize(w, r, id, true)
	if !ok {
		return
	}
	if !access.CanShare {
		writeError(w, http.StatusForbidden, "permission_denied")
		return
	}
	var body struct {
		WeComUserID string `json:"wecom_userid"`
		Permission  string `json:"permission"`
	}
	if decodeJSON(r, &body) != nil || strings.TrimSpace(body.WeComUserID) == "" || (body.Permission != "read" && body.Permission != "edit") {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if h.staff == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	staff, found, err := h.staff.MemberGridStaffByWeComUserID(r.Context(), strings.TrimSpace(body.WeComUserID))
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	if !found || !staff.Active {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	key, err := requestIdempotencyKey(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	collaborator, err := h.workspace.CreateCollaborator(r.Context(), productport.CreateMemberGridCollaboratorCommand{ProductID: productport.ID(id), AdminUserID: staff.AdminUserID, Permission: body.Permission, Actor: actor, IdempotencyKey: key})
	if err != nil {
		resultError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "collaborator": h.donorGridCollaboratorResponse(r.Context(), collaborator)})
}

func (h *Handler) memberGridCollaborator(w http.ResponseWriter, r *http.Request, id int64, rawID string) {
	collaboratorID, err := parseID(rawID)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	access, actor, ok := h.memberGridAuthorize(w, r, id, true)
	if !ok {
		return
	}
	if !access.CanShare {
		writeError(w, http.StatusForbidden, "permission_denied")
		return
	}
	var body struct {
		Permission string `json:"permission"`
		Version    int64  `json:"version"`
	}
	if decodeJSON(r, &body) != nil || body.Version < 1 {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	key, err := requestIdempotencyKey(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	switch r.Method {
	case http.MethodPut:
		if body.Permission != "read" && body.Permission != "edit" {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		value, err := h.workspace.UpdateCollaborator(r.Context(), productport.UpdateMemberGridCollaboratorCommand{ProductID: productport.ID(id), CollaboratorID: productport.ID(collaboratorID), ExpectedVersion: body.Version, Permission: body.Permission, Actor: actor, IdempotencyKey: key})
		if err != nil {
			resultError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "collaborator": h.donorGridCollaboratorResponse(r.Context(), value)})
	case http.MethodDelete:
		value, err := h.workspace.DeleteCollaborator(r.Context(), productport.DeleteMemberGridCollaboratorCommand{ProductID: productport.ID(id), CollaboratorID: productport.ID(collaboratorID), ExpectedVersion: body.Version, Actor: actor, IdempotencyKey: key})
		if err != nil {
			resultError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "deleted": true, "collaborator": h.donorGridCollaboratorResponse(r.Context(), value)})
	default:
		methodNotAllowed(w, http.MethodPut+", "+http.MethodDelete)
	}
}

func (h *Handler) memberGridQuery(w http.ResponseWriter, r *http.Request, id int64) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	access, _, ok := h.memberGridAuthorize(w, r, id, false)
	if !ok {
		return
	}
	if !access.CanView {
		writeError(w, http.StatusForbidden, "permission_denied")
		return
	}
	var body struct {
		Config json.RawMessage `json:"config"`
		Cursor string          `json:"cursor"`
		Limit  int32           `json:"limit"`
	}
	if decodeJSON(r, &body) != nil || body.Limit < 1 || body.Limit > 200 || len(body.Cursor) > 4096 {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	config, err := decodeDonorGridConfig(body.Config)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	var metrics memberGridMetrics
	rows, next, err := h.queryDonorGridWithMetrics(r.Context(), id, config, body.Cursor, body.Limit, &metrics)
	if err != nil {
		if !productMemberGridQueryError(w, err) {
			resultError(w, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rows": rows, "limit": body.Limit, "next_cursor": next, "has_more": next != "", "total": metrics.Total, "metrics": metrics})
}
func memberGridMemberRef(id int64) string {
	var raw [16]byte
	binary.BigEndian.PutUint64(raw[8:], uint64(id))
	return "spm_" + base64.RawURLEncoding.EncodeToString(raw[:])
}
func parseMemberGridMemberRef(value string) (int64, error) {
	if !strings.HasPrefix(value, "spm_") {
		return 0, errors.New("invalid member ref")
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, "spm_"))
	if err != nil || len(raw) != 16 {
		return 0, errors.New("invalid member ref")
	}
	if binary.BigEndian.Uint64(raw[:8]) != 0 {
		return 0, errors.New("invalid member ref")
	}
	id := int64(binary.BigEndian.Uint64(raw[8:]))
	if id < 1 || memberGridMemberRef(id) != value {
		return 0, errors.New("invalid member ref")
	}
	return id, nil
}

type distributionPolicyRequest struct {
	Enabled                   bool  `json:"enabled"`
	CommissionRateBasisPoints int32 `json:"commission_rate_basis_points"`
	WaitDays                  int32 `json:"wait_days"`
	Version                   int64 `json:"version"`
}

func (value *distributionPolicyRequest) command() *productport.DistributionPolicy {
	if value == nil {
		return nil
	}
	return &productport.DistributionPolicy{Enabled: value.Enabled, CommissionRateBasisPoints: value.CommissionRateBasisPoints, WaitDays: value.WaitDays, ExpectedVersion: value.Version}
}

type createRequest struct {
	ProductCode        string                     `json:"product_code"`
	Name               string                     `json:"name"`
	Description        string                     `json:"description"`
	PriceMinor         int64                      `json:"price_minor"`
	Currency           string                     `json:"currency"`
	StockQuantity      int32                      `json:"stock_quantity"`
	Images             []string                   `json:"images"`
	AdminProjection    json.RawMessage            `json:"admin_projection"`
	DistributionPolicy *distributionPolicyRequest `json:"distribution_policy"`
}

type updateRequest struct {
	ExpectedVersion    int64                      `json:"expected_version"`
	Name               string                     `json:"name"`
	Description        string                     `json:"description"`
	PriceMinor         int64                      `json:"price_minor"`
	Currency           string                     `json:"currency"`
	StockQuantity      int32                      `json:"stock_quantity"`
	Images             []string                   `json:"images"`
	AdminProjection    json.RawMessage            `json:"admin_projection"`
	DistributionPolicy *distributionPolicyRequest `json:"distribution_policy"`
}

type serviceCreateRequest struct {
	ProductCode        string                     `json:"product_code"`
	Name               string                     `json:"name"`
	Description        string                     `json:"description"`
	PriceMinor         int64                      `json:"price_minor"`
	Currency           string                     `json:"currency"`
	DurationDays       int32                      `json:"duration_days"`
	StockQuantity      int32                      `json:"stock_quantity"`
	Images             []string                   `json:"images"`
	AdminProjection    json.RawMessage            `json:"admin_projection"`
	DistributionPolicy *distributionPolicyRequest `json:"distribution_policy"`
}

type serviceUpdateRequest struct {
	ExpectedVersion    int64                      `json:"expected_version"`
	Name               string                     `json:"name"`
	Description        string                     `json:"description"`
	PriceMinor         int64                      `json:"price_minor"`
	Currency           string                     `json:"currency"`
	DurationDays       int32                      `json:"duration_days"`
	StockQuantity      int32                      `json:"stock_quantity"`
	Images             []string                   `json:"images"`
	AdminProjection    json.RawMessage            `json:"admin_projection"`
	DistributionPolicy *distributionPolicyRequest `json:"distribution_policy"`
}

type versionRequest struct {
	ExpectedVersion int64 `json:"expected_version"`
}

type externalConfigurationRequest struct {
	URL                    *string `json:"url"`
	WebhookURL             *string `json:"webhook_url"`
	Enabled                bool    `json:"enabled"`
	ConfigurationReference string  `json:"configuration_reference"`
	ExpectedRevision       *int64  `json:"expected_revision"`
	// PushType is the historic V3 spelling; LegacyPushType is the frozen
	// AI-CRM product-panel spelling. When both arrive, the legacy panel value
	// is authoritative so a compatibility client cannot overwrite it.
	PushType       *string         `json:"type"`
	LegacyPushType *string         `json:"push_type"`
	Day            json.RawMessage `json:"day"`
	Frequency      json.RawMessage `json:"frequency"`
	ExpiresAtTS    json.RawMessage `json:"expires_at_ts"`
	Remark         *string         `json:"remark"`
	CustomParams   json.RawMessage `json:"custom_params"`
	FieldMapping   json.RawMessage `json:"field_mapping"`
}

// externalConfigurationResponse keeps the canonical JSON text alongside the
// legacy decoded value. The Host returns this exact text to the frozen textarea
// so JavaScript never rounds a valid JSON integer before it is saved again.
type externalConfigurationResponse struct {
	productport.ExternalPushConfiguration
	// ConfigurationReference is intentionally repeated without omitempty.
	// The frozen Product Host reads the disabled binding too and needs an
	// explicit empty string rather than an absent field.
	ConfigurationReference string `json:"configuration_reference"`
	CustomParamsJSON       string `json:"custom_params_json"`
	URL                    string `json:"url"`
	WebhookURL             string `json:"webhook_url"`
	PushType               string `json:"push_type"`
}

func externalConfigurationJSONResponse(value productport.ExternalPushConfiguration) externalConfigurationResponse {
	if value.CustomParams == nil {
		value.CustomParams = map[string]any{}
	}
	raw, err := json.Marshal(value.CustomParams)
	if err != nil {
		// The Product application validates this field before persistence. Keep a
		// valid response shape for the disabled legacy configuration as a
		// defensive fallback; no request data is reflected here.
		raw = []byte("{}")
	}
	return externalConfigurationResponse{ExternalPushConfiguration: value, ConfigurationReference: value.ConfigurationReference, CustomParamsJSON: string(raw), URL: value.URL, WebhookURL: value.URL, PushType: value.PushType}
}

type externalConfigurationBusinessValue struct {
	expectedRevision int64
	pushType         string
	day              *int64
	frequency        *int64
	expiresAtTS      *int64
	remark           string
	customParams     map[string]any
}

// externalConfigurationBusiness accepts the frozen config's JSON-object and
// key/value-list forms. URL and secret remain absent: Product persists only
// non-sensitive payload fields while the opaque reference selects deployment
// credentials and the controlled target.
func externalConfigurationBusiness(value externalConfigurationRequest) (bool, externalConfigurationBusinessValue, bool) {
	pushType := value.PushType
	if value.LegacyPushType != nil {
		pushType = value.LegacyPushType
	}
	present := pushType != nil || len(value.Day) != 0 || len(value.Frequency) != 0 || len(value.ExpiresAtTS) != 0 || value.Remark != nil || len(value.CustomParams) != 0 || value.ExpectedRevision != nil
	if !present {
		return false, externalConfigurationBusinessValue{}, true
	}
	if pushType == nil || len(value.Day) == 0 || len(value.Frequency) == 0 || len(value.ExpiresAtTS) == 0 || value.Remark == nil || len(value.CustomParams) == 0 || value.ExpectedRevision == nil || *value.ExpectedRevision < 0 {
		return false, externalConfigurationBusinessValue{}, false
	}
	day, ok := externalConfigurationOptionalInteger(value.Day)
	if !ok {
		return false, externalConfigurationBusinessValue{}, false
	}
	frequency, ok := externalConfigurationOptionalInteger(value.Frequency)
	if !ok {
		return false, externalConfigurationBusinessValue{}, false
	}
	expiresAtTS, ok := externalConfigurationOptionalInteger(value.ExpiresAtTS)
	if !ok {
		return false, externalConfigurationBusinessValue{}, false
	}
	custom, ok := externalConfigurationCustomParams(value.CustomParams)
	if !ok {
		return false, externalConfigurationBusinessValue{}, false
	}
	return true, externalConfigurationBusinessValue{expectedRevision: *value.ExpectedRevision, pushType: strings.TrimSpace(*pushType), day: day, frequency: frequency, expiresAtTS: expiresAtTS, remark: strings.TrimSpace(*value.Remark), customParams: custom}, true
}

func externalConfigurationOptionalInteger(raw json.RawMessage) (*int64, bool) {
	value := strings.TrimSpace(string(raw))
	if value == "null" || value == `""` {
		return nil, true
	}
	var number int64
	if json.Unmarshal(raw, &number) == nil {
		return &number, number >= 0
	}
	var text string
	if json.Unmarshal(raw, &text) != nil {
		return nil, false
	}
	parsed, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
	if err != nil || parsed < 0 {
		return nil, false
	}
	return &parsed, true
}

func externalConfigurationCustomParams(raw json.RawMessage) (map[string]any, bool) {
	if len(raw) == 0 || len(raw) > 32768 || !json.Valid(raw) {
		return nil, false
	}
	value := strings.TrimSpace(string(raw))
	if value == `""` {
		return map[string]any{}, true
	}
	if len(value) > 1 && value[0] == '"' {
		var encoded string
		if json.Unmarshal(raw, &encoded) != nil {
			return nil, false
		}
		return externalConfigurationCustomParams(json.RawMessage(encoded))
	}
	if strings.HasPrefix(value, "{") {
		var object map[string]any
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if decoder.Decode(&object) != nil {
			return nil, false
		}
		return normalizeExternalConfigurationCustomParams(object)
	}
	if !strings.HasPrefix(value, "[") {
		return nil, false
	}
	var entries []map[string]json.RawMessage
	if json.Unmarshal(raw, &entries) != nil {
		return nil, false
	}
	object := make(map[string]any, len(entries))
	for _, entry := range entries {
		keyRaw := entry["key"]
		if len(keyRaw) == 0 {
			keyRaw = entry["name"]
		}
		var key string
		if len(keyRaw) == 0 || json.Unmarshal(keyRaw, &key) != nil {
			return nil, false
		}
		key = strings.TrimSpace(key)
		if key == "" {
			return nil, false
		}
		if _, duplicate := object[key]; duplicate {
			return nil, false
		}
		valueRaw := entry["value"]
		if len(valueRaw) == 0 {
			object[key] = ""
			continue
		}
		var item any
		decoder := json.NewDecoder(bytes.NewReader(valueRaw))
		decoder.UseNumber()
		if decoder.Decode(&item) != nil {
			return nil, false
		}
		object[key] = item
	}
	return normalizeExternalConfigurationCustomParams(object)
}

func normalizeExternalConfigurationCustomParams(value map[string]any) (map[string]any, bool) {
	if len(value) > 64 {
		return nil, false
	}
	result := make(map[string]any, len(value))
	for key, item := range value {
		normalized := strings.TrimSpace(key)
		if normalized == "" || len([]rune(normalized)) > 128 || strings.ContainsFunc(normalized, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
			return nil, false
		}
		if _, duplicate := result[normalized]; duplicate {
			return nil, false
		}
		raw, err := json.Marshal(item)
		if err != nil || !json.Valid(raw) || len(raw) > 4096 {
			return nil, false
		}
		result[normalized] = item
	}
	return result, true
}

type productResponse struct {
	ID                 productport.ID                    `json:"id"`
	ProductCode        string                            `json:"product_code"`
	Name               string                            `json:"name"`
	Description        string                            `json:"description"`
	PriceMinor         int64                             `json:"price_minor"`
	Currency           string                            `json:"currency"`
	StockQuantity      int32                             `json:"stock_quantity"`
	Images             []string                          `json:"images"`
	AdminProjection    json.RawMessage                   `json:"admin_projection"`
	CreatedBy          int64                             `json:"created_by"`
	CreatedAt          interface{}                       `json:"created_at"`
	UpdatedAt          interface{}                       `json:"updated_at"`
	Version            int64                             `json:"version"`
	Lifecycle          productport.LocalProductLifecycle `json:"lifecycle"`
	Enabled            bool                              `json:"enabled"`
	PaidOrderCount     int64                             `json:"paid_order_count"`
	RefundOrderCount   int64                             `json:"refund_order_count"`
	SoldCount          int64                             `json:"sold_count"`
	DistributionPolicy distributionPolicyResponse        `json:"distribution_policy"`
}

func productResponseFrom(value productport.Product) (productResponse, error) {
	local, err := productapp.ProjectLocalProduct(value)
	if err != nil || value.PaidOrderCount < 0 || value.RefundOrderCount < 0 || value.SoldCount < 0 || value.SoldCount != maxInt64(0, value.PaidOrderCount-value.RefundOrderCount) {
		return productResponse{}, productapp.ErrUnavailable
	}
	return productResponse{ID: value.ID, ProductCode: value.ProductCode, Name: value.Name, Description: value.Description, PriceMinor: value.PriceMinor, Currency: value.Currency, StockQuantity: value.StockQuantity, Images: append([]string(nil), value.Images...), AdminProjection: append(json.RawMessage(nil), value.LegacyAdminProjection...), CreatedBy: value.CreatedBy, CreatedAt: value.CreatedAt.UTC(), UpdatedAt: value.UpdatedAt.UTC(), Version: value.Version, Lifecycle: local.Lifecycle, Enabled: local.Enabled, PaidOrderCount: value.PaidOrderCount, RefundOrderCount: value.RefundOrderCount, SoldCount: value.SoldCount}, nil
}

type servicePeriodResponse struct {
	productport.ServicePeriodProduct
	DistributionPolicy distributionPolicyResponse `json:"distribution_policy"`
}

func (h *Handler) servicePeriodResponse(ctx context.Context, value productport.ServicePeriodProduct) (servicePeriodResponse, error) {
	policy, err := h.distributionPolicy(ctx, value.ServiceProductID, distributiondomain.ProductTypeServicePeriod)
	if err != nil {
		return servicePeriodResponse{}, err
	}
	return servicePeriodResponse{ServicePeriodProduct: value, DistributionPolicy: policy}, nil
}

type distributionPolicyResponse struct {
	Enabled                   bool  `json:"enabled"`
	CommissionRateBasisPoints int32 `json:"commission_rate_basis_points"`
	WaitDays                  int32 `json:"wait_days"`
	Version                   int64 `json:"version"`
}

func defaultDistributionPolicyResponse() distributionPolicyResponse {
	return distributionPolicyResponse{Enabled: false, CommissionRateBasisPoints: 0, WaitDays: 7, Version: 0}
}

func (h *Handler) distributionPolicy(ctx context.Context, id productport.ID, productType distributiondomain.ProductType) (distributionPolicyResponse, error) {
	if h == nil || h.policyReader == nil {
		return defaultDistributionPolicyResponse(), nil
	}
	policy, err := h.policyReader.ReadProductPolicy(ctx, int64(id), productType)
	if errors.Is(err, distributionport.ErrNotFound) {
		return defaultDistributionPolicyResponse(), nil
	}
	if err != nil {
		return distributionPolicyResponse{}, err
	}
	return distributionPolicyResponse{Enabled: policy.Enabled, CommissionRateBasisPoints: policy.CommissionRateBasisPoints, WaitDays: policy.WaitDays, Version: policy.Version}, nil
}

func (h *Handler) productResponse(ctx context.Context, value productport.Product) (productResponse, error) {
	response, err := productResponseFrom(value)
	if err != nil {
		return productResponse{}, err
	}
	response.DistributionPolicy, err = h.distributionPolicy(ctx, value.ID, distributiondomain.ProductTypeStandard)
	return response, err
}

func maxInt64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}

func (h *Handler) read(w http.ResponseWriter, r *http.Request) bool {
	principal, err := h.security.Authenticate(r.Context(), r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return false
	}
	if !canRead(principal) {
		writeError(w, http.StatusForbidden, "permission_denied")
		return false
	}
	return true
}

func (h *Handler) write(w http.ResponseWriter, r *http.Request) (accessdomain.Principal, bool) {
	principal, err := h.security.Authenticate(r.Context(), r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return accessdomain.Principal{}, false
	}
	if !canWrite(principal) {
		writeError(w, http.StatusForbidden, "permission_denied")
		return accessdomain.Principal{}, false
	}
	if _, err = h.security.AuthorizeCSRF(r.Context(), r); err != nil {
		writeError(w, http.StatusForbidden, "csrf_required")
		return accessdomain.Principal{}, false
	}
	return principal, true
}

func canRead(principal accessdomain.Principal) bool {
	if principal.InternalID < 1 || (principal.Kind != accessdomain.KindAdmin && principal.Kind != accessdomain.KindStaff) {
		return false
	}
	for _, role := range principal.Roles {
		if role == accessdomain.RoleViewer || role == accessdomain.RoleAdmin || role == accessdomain.RoleSuperAdmin {
			return true
		}
	}
	return false
}

func canWrite(principal accessdomain.Principal) bool {
	if !canRead(principal) {
		return false
	}
	for _, role := range principal.Roles {
		if role == accessdomain.RoleAdmin || role == accessdomain.RoleSuperAdmin {
			return true
		}
	}
	return false
}

func decodeJSON(r *http.Request, value any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, maxProductBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}

func decodeOptionalJSON(r *http.Request, value any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, maxProductBodyBytes))
	decoder.DisallowUnknownFields()
	err := decoder.Decode(value)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}

func decodeEmptyJSON(r *http.Request) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, maxProductBodyBytes))
	var value any
	if err := decoder.Decode(&value); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}

func requestIdempotencyKey(r *http.Request) (string, error) {
	values := r.Header.Values("Idempotency-Key")
	if len(values) > 1 {
		return "", errors.New("duplicate idempotency key")
	}
	if len(values) == 1 {
		return values[0], nil
	}
	return compatibilityIdempotencyKey(rand.Read)
}

func compatibilityIdempotencyKey(read func([]byte) (int, error)) (string, error) {
	var raw [20]byte
	if _, err := read(raw[:]); err != nil {
		return "", err
	}
	return "product_compat_" + hex.EncodeToString(raw[:]), nil
}

func parseID(value string) (int64, error) {
	if value == "" || strings.HasPrefix(value, "0") {
		return 0, errors.New("invalid id")
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return 0, errors.New("invalid id")
		}
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 1 || strconv.FormatInt(parsed, 10) != value {
		return 0, errors.New("invalid id")
	}
	return parsed, nil
}

func onlyQuery(r *http.Request, allowed ...string) bool {
	set := make(map[string]struct{}, len(allowed))
	for _, name := range allowed {
		set[name] = struct{}{}
	}
	for key, values := range r.URL.Query() {
		if _, ok := set[key]; !ok || len(values) != 1 {
			return false
		}
	}
	return true
}

func queryLimit(r *http.Request, defaultValue int32) (int32, bool) {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return defaultValue, true
	}
	value, err := parseInt32(raw)
	return value, err == nil && value >= 1 && value <= maxProductLimit
}

func queryOffset(r *http.Request) (int32, bool) {
	raw := r.URL.Query().Get("offset")
	if raw == "" {
		return 0, true
	}
	value, err := parseInt32(raw)
	return value, err == nil && value >= 0 && value <= maxProductOffset
}

func parseInt32(raw string) (int32, error) {
	if raw == "" || (len(raw) > 1 && raw[0] == '0') {
		return 0, errors.New("invalid integer")
	}
	value, err := strconv.ParseInt(raw, 10, 32)
	return int32(value), err
}

func resultError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, productapp.ErrInvalidProduct), errors.Is(err, productapp.ErrInvalidCursor):
		writeError(w, http.StatusBadRequest, "invalid_request")
	case errors.Is(err, productapp.ErrNotFound), errors.Is(err, productport.ErrProductReadNotFound):
		writeError(w, http.StatusNotFound, "not_found")
	case errors.Is(err, productapp.ErrConflict), errors.Is(err, productapp.ErrExternalPushNotConfigured), errors.Is(err, productport.ErrProductConflict):
		writeError(w, http.StatusConflict, "conflict")
	default:
		writeError(w, http.StatusServiceUnavailable, "unavailable")
	}
}

func methodNotAllowed(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	writeError(w, http.StatusMethodNotAllowed, "method_not_allowed")
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code string) {
	compat := map[string]string{"invalid_request": "MALFORMED_REQUEST", "not_found": "NOT_FOUND", "conflict": "CONFLICT", "unauthorized": "UNAUTHORIZED", "permission_denied": "FORBIDDEN", "csrf_required": "FORBIDDEN", "unavailable": "DEPENDENCY_UNAVAILABLE", "method_not_allowed": "METHOD_NOT_ALLOWED", "product_not_enabled": "product_not_enabled", "share_gone": "SHARE_GONE", "cursor_stale": "CURSOR_STALE", "member_grid_too_large": "MEMBER_GRID_TOO_LARGE"}[code]
	if compat == "" {
		compat = "DEPENDENCY_UNAVAILABLE"
	}
	writeJSON(w, status, map[string]any{"ok": false, "code": compat, "message": strings.ReplaceAll(strings.ToLower(compat), "_", " ")})
}

// externalPreview compiles synthetic facts only; it performs no Provider call or acceptance.
func (h *Handler) externalPreview(w http.ResponseWriter, r *http.Request, id int64, kind productport.ExternalPushProductKind) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if _, ok := h.write(w, r); !ok {
		return
	}
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	var body struct {
		FieldMapping json.RawMessage `json:"field_mapping"`
	}
	if decodeJSON(r, &body) != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	mapping, err := productport.DecodeFieldMapping(body.FieldMapping)
	if err != nil || mapping == nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if _, err = h.external.GetExternalPushConfiguration(r.Context(), productport.ID(id), kind); err != nil {
		resultError(w, err)
		return
	}
	payload, err := productport.CompileFieldMapping(mapping, productport.SyntheticFieldMappingVariables())
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	response := map[string]any{"payload": payload, "payload_json": string(payload), "synthetic": true, "real_external_call_executed": false}
	if previewer, ok := h.external.(interface {
		PreviewLegacyExternalPushConfiguration(context.Context, productport.ID) (json.RawMessage, error)
	}); ok {
		if legacy, e := previewer.PreviewLegacyExternalPushConfiguration(r.Context(), productport.ID(id)); e == nil && json.Valid(legacy) {
			response["legacy_payload_json"] = string(legacy)
		} else {
			response["legacy_preview_unavailable"] = true
		}
	}
	writeJSON(w, http.StatusOK, response)
}
