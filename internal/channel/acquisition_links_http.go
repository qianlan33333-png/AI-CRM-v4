package channel

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

const acquisitionLinksPath = "/api/admin/wecom-customer-acquisition-links"

type AcquisitionLinkApplication interface {
	List(context.Context, string, int) ([]wecomport.CustomerAcquisitionLink, string, error)
	Get(context.Context, string) (wecomport.CustomerAcquisitionLink, error)
	Mutate(context.Context, AcquisitionLinkCommand) (AcquisitionLinkReceipt, error)
	Reconcile(context.Context, AcquisitionLinkReconcileCommand) (AcquisitionLinkReceipt, error)
}

type AcquisitionLinkHTTPHandler struct {
	app      AcquisitionLinkApplication
	security interface {
		Authenticate(context.Context, *http.Request) (accessdomain.Principal, error)
		AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error)
	}
}

func NewAcquisitionLinkHTTPHandler(app AcquisitionLinkApplication, security interface {
	Authenticate(context.Context, *http.Request) (accessdomain.Principal, error)
	AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error)
}) (*AcquisitionLinkHTTPHandler, error) {
	if app == nil || security == nil {
		return nil, errors.New("channel acquisition link HTTP dependencies are required")
	}
	return &AcquisitionLinkHTTPHandler{app: app, security: security}, nil
}

func (handler *AcquisitionLinkHTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	if r.URL == nil || r.URL.RawPath != "" || strings.Contains(r.URL.Path, "\\") || strings.HasSuffix(r.URL.Path, "/") {
		writeCatalogError(w, http.StatusNotFound, "NOT_FOUND")
		return
	}
	if r.URL.Path == acquisitionLinksPath {
		switch r.Method {
		case http.MethodGet:
			handler.list(w, r)
		case http.MethodPost:
			handler.mutate(w, r, "create", "")
		default:
			writeCatalogError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED")
		}
		return
	}
	if !strings.HasPrefix(r.URL.Path, acquisitionLinksPath+"/") {
		writeCatalogError(w, http.StatusNotFound, "NOT_FOUND")
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, acquisitionLinksPath+"/"), "/")
	if len(parts) == 1 && validLinkID(parts[0]) {
		switch r.Method {
		case http.MethodGet:
			handler.get(w, r, parts[0])
		case http.MethodPatch:
			handler.mutate(w, r, "update", parts[0])
		case http.MethodDelete:
			handler.mutate(w, r, "delete", parts[0])
		default:
			writeCatalogError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED")
		}
		return
	}
	if len(parts) == 2 && validLinkID(parts[0]) && parts[1] == "reconcile" && r.Method == http.MethodPost {
		handler.reconcile(w, r, parts[0])
		return
	}
	writeCatalogError(w, http.StatusNotFound, "NOT_FOUND")
}

func (handler *AcquisitionLinkHTTPHandler) read(w http.ResponseWriter, r *http.Request) bool {
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

func (handler *AcquisitionLinkHTTPHandler) write(w http.ResponseWriter, r *http.Request) (accessdomain.Principal, string, bool) {
	principal, err := handler.security.AuthorizeCSRF(r.Context(), r)
	if err != nil {
		writeCatalogError(w, http.StatusUnauthorized, "UNAUTHORIZED")
		return accessdomain.Principal{}, "", false
	}
	if !channelBusinessWriteRole(principal) {
		writeCatalogError(w, http.StatusForbidden, "FORBIDDEN")
		return accessdomain.Principal{}, "", false
	}
	key, err := singleCatalogIdempotencyKey(r)
	if err != nil {
		writeCatalogError(w, http.StatusBadRequest, "MALFORMED_REQUEST")
		return accessdomain.Principal{}, "", false
	}
	return principal, key, true
}

func (handler *AcquisitionLinkHTTPHandler) list(w http.ResponseWriter, r *http.Request) {
	if !handler.read(w, r) {
		return
	}
	query := r.URL.Query()
	if len(query) > 2 || len(query["cursor"]) > 1 || len(query["limit"]) > 1 {
		writeCatalogError(w, http.StatusBadRequest, "MALFORMED_REQUEST")
		return
	}
	limit := 100
	var err error
	if query.Get("limit") != "" {
		limit, err = strconv.Atoi(query.Get("limit"))
	}
	if err != nil || limit < 1 || limit > 100 {
		writeCatalogError(w, http.StatusBadRequest, "MALFORMED_REQUEST")
		return
	}
	items, next, err := handler.app.List(r.Context(), query.Get("cursor"), limit)
	if err != nil {
		writeLinkError(w, err)
		return
	}
	summaries := make([]map[string]string, len(items))
	for index, item := range items {
		summaries[index] = map[string]string{"link_id": item.LinkID}
	}
	writeChannelJSON(w, http.StatusOK, map[string]any{"items": summaries, "next_cursor": next})
}

func (handler *AcquisitionLinkHTTPHandler) get(w http.ResponseWriter, r *http.Request, id string) {
	if !handler.read(w, r) {
		return
	}
	link, err := handler.app.Get(r.Context(), id)
	if err != nil {
		writeLinkError(w, err)
		return
	}
	writeChannelJSON(w, http.StatusOK, acquisitionLinkJSON(link))
}

func (handler *AcquisitionLinkHTTPHandler) mutate(w http.ResponseWriter, r *http.Request, operation, linkID string) {
	principal, key, ok := handler.write(w, r)
	if !ok {
		return
	}
	input := wecomport.CustomerAcquisitionLinkInput{}
	if operation == "delete" {
		if r.Body != nil {
			decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
			var empty map[string]json.RawMessage
			if err := decoder.Decode(&empty); err != io.EOF && (err != nil || len(empty) != 0) {
				writeCatalogError(w, http.StatusBadRequest, "MALFORMED_REQUEST")
				return
			}
		}
	} else {
		if r.Header.Get("Content-Type") != "application/json" || !decodeLinkInput(w, r, &input) {
			if r.Header.Get("Content-Type") != "application/json" {
				writeCatalogError(w, http.StatusBadRequest, "MALFORMED_REQUEST")
			}
			return
		}
	}
	receipt, err := handler.app.Mutate(r.Context(), AcquisitionLinkCommand{ActorID: principal.InternalID, IdempotencyKey: key, Operation: operation, LinkID: linkID, Input: input})
	if err != nil {
		writeLinkError(w, err)
		return
	}
	writeChannelJSON(w, http.StatusAccepted, acquisitionLinkReceiptJSON(receipt))
}

func (handler *AcquisitionLinkHTTPHandler) reconcile(w http.ResponseWriter, r *http.Request, linkID string) {
	principal, key, ok := handler.write(w, r)
	if !ok {
		return
	}
	var body struct {
		ReceiptID      int64  `json:"receipt_id"`
		Resolution     string `json:"resolution"`
		EvidenceDigest string `json:"evidence_digest"`
	}
	if r.Header.Get("Content-Type") != "application/json" {
		writeCatalogError(w, http.StatusBadRequest, "MALFORMED_REQUEST")
		return
	}
	if !decodeStrictLinkJSON(w, r, &body) {
		return
	}
	evidence, err := hex.DecodeString(body.EvidenceDigest)
	if err != nil || len(evidence) != 32 {
		writeCatalogError(w, http.StatusBadRequest, "MALFORMED_REQUEST")
		return
	}
	receipt, err := handler.app.Reconcile(r.Context(), AcquisitionLinkReconcileCommand{ActorID: principal.InternalID, ReceiptID: body.ReceiptID, LinkID: linkID, Resolution: body.Resolution, EvidenceDigest: "sha256:" + body.EvidenceDigest, IdempotencyKey: key})
	if err != nil {
		writeLinkError(w, err)
		return
	}
	writeChannelJSON(w, http.StatusOK, acquisitionLinkReceiptJSON(receipt))
}

func decodeLinkInput(w http.ResponseWriter, r *http.Request, input *wecomport.CustomerAcquisitionLinkInput) bool {
	var body struct {
		LinkName      string   `json:"link_name"`
		UserIDs       []string `json:"user_ids"`
		DepartmentIDs []int64  `json:"department_ids"`
		SkipVerify    bool     `json:"skip_verify"`
	}
	if !decodeStrictLinkJSON(w, r, &body) {
		return false
	}
	*input = wecomport.CustomerAcquisitionLinkInput{LinkName: body.LinkName, UserIDs: body.UserIDs, DepartmentIDs: body.DepartmentIDs, SkipVerify: body.SkipVerify}
	return true
}

func decodeStrictLinkJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, catalogHTTPMaxBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil {
		writeCatalogError(w, http.StatusBadRequest, "MALFORMED_REQUEST")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeCatalogError(w, http.StatusBadRequest, "MALFORMED_REQUEST")
		return false
	}
	return true
}

func acquisitionLinkJSON(link wecomport.CustomerAcquisitionLink) map[string]any {
	return map[string]any{"link_id": link.LinkID, "link_name": link.LinkName, "url": link.URL, "user_ids": link.UserIDs, "department_ids": link.DepartmentIDs, "skip_verify": link.SkipVerify}
}

func acquisitionLinkReceiptJSON(receipt AcquisitionLinkReceipt) map[string]any {
	result := map[string]any{"receipt_id": receipt.ID, "state": receipt.State, "business_endpoint_dispatched": receipt.BusinessEndpointDispatched, "real_external_call_executed": receipt.RealExternalCallExecuted}
	if receipt.OutcomeDigest != "" {
		result["outcome_digest"] = strings.TrimPrefix(receipt.OutcomeDigest, "sha256:")
	}
	if receipt.Resolution != "" {
		result["resolution"] = receipt.Resolution
	}
	if receipt.Link != nil {
		result["link"] = acquisitionLinkJSON(*receipt.Link)
	}
	return result
}

func writeLinkError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrInvalidCatalogCommand):
		writeCatalogError(w, http.StatusBadRequest, "INVALID_REQUEST")
	case errors.Is(err, ErrCatalogConflict):
		writeCatalogError(w, http.StatusConflict, "CONFLICT")
	case errors.Is(err, ErrCatalogNotFound):
		writeCatalogError(w, http.StatusNotFound, "NOT_FOUND")
	default:
		writeCatalogError(w, http.StatusServiceUnavailable, "DEPENDENCY_UNAVAILABLE")
	}
}

var _ http.Handler = (*AcquisitionLinkHTTPHandler)(nil)
