// Package http exposes only Segment-owned local configuration routes.
package http

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accessport "github.com/qianlan33333-png/AI-CRM-v3/internal/access/port"
	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
	segmentapp "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/app"
	segmentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/domain"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
)

type RequestSecurity interface {
	Authenticate(context.Context, *http.Request) (accessdomain.Principal, error)
	AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error)
}
type ConfigurationApplication interface {
	ListGroups(context.Context) ([]segmentdomain.Group, error)
	CreateGroup(context.Context, segmentapp.GroupCommand) (segmentdomain.Group, error)
	UpdateGroup(context.Context, segmentapp.GroupCommand) (segmentdomain.Group, error)
	DeleteGroup(context.Context, segmentapp.VersionCommand) error
	ListPackages(context.Context, int, int, bool) (segmentapp.PackagePage, error)
	GetPackage(context.Context, int64) (segmentdomain.Package, error)
	CreatePackage(context.Context, segmentapp.PackageCreateCommand) (segmentdomain.Package, error)
	UpdatePackage(context.Context, segmentapp.PackageUpdateCommand) (segmentdomain.Package, error)
	CopyPackage(context.Context, segmentapp.VersionCommand) (segmentdomain.Package, error)
	TransitionPackage(context.Context, segmentapp.VersionCommand, segmentdomain.Lifecycle) (segmentdomain.Package, error)
	PutConfiguration(context.Context, segmentapp.ConfigurationCommand) (segmentdomain.ConfigurationVersion, error)
	CurrentConfiguration(context.Context, int64) (segmentdomain.ConfigurationVersion, error)
}
type SnapshotApplication interface {
	Preview(context.Context, int64, time.Time) (segmentapp.Preview, error)
	PreviewDefinition(context.Context, int64, json.RawMessage, time.Time) (segmentapp.Preview, error)
	AcceptRefresh(context.Context, segmentapp.RefreshCommand) (segmentdomain.RefreshRun, error)
	GetRefresh(context.Context, int64) (segmentdomain.RefreshRun, error)
	PublishedSnapshot(context.Context, segmentport.PackageID) (segmentport.Snapshot, bool, error)
	Members(context.Context, segmentport.SnapshotID, string, int) (segmentport.MemberPage, error)
}
type ExecutionApplication interface {
	PutBinding(context.Context, segmentapp.BindingCommand) (segmentdomain.AutomationBinding, error)
	CurrentBinding(context.Context, int64) (segmentdomain.AutomationBinding, error)
	DeleteBinding(context.Context, segmentapp.VersionCommand) error
	ReplaceSenders(context.Context, segmentapp.SendersCommand) (segmentdomain.SenderSet, error)
	CurrentSenderSet(context.Context, int64) (segmentdomain.SenderSet, error)
	Precheck(context.Context, int64) (segmentapp.Precheck, error)
}
type Handler struct {
	core            *segmentapp.CoreOperations
	service         ConfigurationApplication
	snapshots       SnapshotApplication
	execution       ExecutionApplication
	security        RequestSecurity
	owners          accessport.AudienceOwnerResolver
	ownerReferences accessport.AudienceOwnerReferenceReader
	products        AudienceProductReferenceResolver
	productOptions  productport.ProductOptionReader
	channels        AudienceChannelReferenceResolver
	radars          AudienceRadarReferenceResolver
	surveys         AudienceSurveyReferenceResolver
}
type AudienceProductReferenceResolver interface {
	ResolveAudienceProduct(context.Context, string) (string, bool, error)
}

func (h *Handler) BindCoreProductOptions(reader productport.ProductOptionReader) {
	h.productOptions = reader
}

func (h *Handler) BindAudienceProductReferences(resolver AudienceProductReferenceResolver) {
	h.products = resolver
}

type AudienceChannelReferenceResolver interface {
	ResolveAudienceChannel(context.Context, string) (string, bool, error)
}

func (h *Handler) BindAudienceChannelReferences(resolver AudienceChannelReferenceResolver) {
	h.channels = resolver
}

type AudienceRadarReferenceResolver interface {
	ResolveAudienceRadar(context.Context, string) (string, bool, error)
}

func (h *Handler) BindAudienceRadarReferences(resolver AudienceRadarReferenceResolver) {
	h.radars = resolver
}

type AudienceSurveyReferenceResolver interface {
	ResolveAudienceQuestionnaire(context.Context, string) (string, bool, error)
	ResolveAudienceQuestion(context.Context, string, string) (string, bool, error)
	ResolveAudienceOption(context.Context, string, string, string) (string, bool, error)
}

func (h *Handler) BindAudienceSurveyReferences(resolver AudienceSurveyReferenceResolver) {
	h.surveys = resolver
}

var (
	errOwnerInvalid         = errors.New("invalid owner selection")
	errOwnerUnknown         = errors.New("unknown owner")
	errOwnerUnavailable     = errors.New("owner resolver unavailable")
	errReferenceInvalid     = errors.New("invalid audience reference")
	errReferenceUnknown     = errors.New("unknown audience reference")
	errReferenceUnavailable = errors.New("audience reference resolver unavailable")
)

func definitionReferenceErrorCode(err error) string {
	switch {
	case errors.Is(err, errOwnerUnknown):
		return "owner_unknown"
	case errors.Is(err, errOwnerUnavailable):
		return "owner_unavailable"
	case errors.Is(err, errReferenceUnknown):
		return "reference_unknown"
	case errors.Is(err, errReferenceUnavailable):
		return "reference_unavailable"
	case errors.Is(err, errReferenceInvalid):
		return "reference_invalid"
	default:
		return "owner_invalid"
	}
}

func NewHandler(service ConfigurationApplication, security RequestSecurity) (*Handler, error) {
	if service == nil || security == nil {
		return nil, errors.New("segment HTTP dependencies are required")
	}
	return &Handler{service: service, security: security}, nil
}
func NewRuntimeHandler(service ConfigurationApplication, snapshots SnapshotApplication, security RequestSecurity) (*Handler, error) {
	return NewRuntimeHandlerWithOwners(service, snapshots, security, nil)
}
func NewRuntimeHandlerWithOwners(service ConfigurationApplication, snapshots SnapshotApplication, security RequestSecurity, owners accessport.AudienceOwnerResolver) (*Handler, error) {
	return NewRuntimeHandlerWithOwnerReferences(service, snapshots, security, owners, nil)
}
func NewRuntimeHandlerWithOwnerReferences(service ConfigurationApplication, snapshots SnapshotApplication, security RequestSecurity, owners accessport.AudienceOwnerResolver, references accessport.AudienceOwnerReferenceReader) (*Handler, error) {
	h, err := NewHandler(service, security)
	if err != nil {
		return nil, err
	}
	if snapshots == nil {
		return nil, errors.New("segment snapshot dependency is required")
	}
	h.snapshots = snapshots
	h.execution, _ = snapshots.(ExecutionApplication)
	h.owners = owners
	h.ownerReferences = references
	return h, nil
}
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.URL.Path, "/api/admin/ai-audience/") {
		fail(w, 404, "not_found")
		return
	}
	tail := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/ai-audience/"), "/")
	parts := strings.Split(tail, "/")
	switch {
	case strings.HasPrefix(tail, "core/") || (len(parts) == 5 && parts[0] == "packages" && parts[2] == "members" && (parts[4] == "operations" || parts[4] == "history")):
		h.coreOperations(w, r, tail)
	case tail == "package-groups":
		h.groups(w, r)
	case len(parts) == 2 && parts[0] == "package-groups":
		h.group(w, r, id(parts[1]))
	case tail == "packages":
		h.packages(w, r)
	case tail == "templates":
		h.templates(w, r)
	case len(parts) == 2 && parts[0] == "packages":
		h.packageItem(w, r, id(parts[1]))
	case len(parts) == 3 && parts[0] == "packages":
		h.packageAction(w, r, id(parts[1]), parts[2])
	case len(parts) == 4 && parts[0] == "packages" && parts[2] == "refresh-runs":
		h.refreshRun(w, r, id(parts[3]))
	default:
		fail(w, 404, "not_found")
	}
}
func (h *Handler) groups(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		if !h.read(w, r) {
			return
		}
		items, e := h.service.ListGroups(r.Context())
		if e != nil {
			resultError(w, e)
			return
		}
		respond(w, 200, map[string]any{"items": items})
		return
	}
	if r.Method != http.MethodPost {
		method(w, "GET, POST")
		return
	}
	p, ok := h.write(w, r)
	if !ok {
		return
	}
	var in struct {
		Name      string `json:"name"`
		SortOrder int    `json:"sort_order"`
	}
	if !decode(w, r, &in) {
		return
	}
	key, ok := requestKey(w, r)
	if !ok {
		return
	}
	item, e := h.service.CreateGroup(r.Context(), segmentapp.GroupCommand{Name: in.Name, SortOrder: in.SortOrder, Actor: p.InternalID, IdempotencyKey: key})
	if e != nil {
		resultError(w, e)
		return
	}
	respond(w, 201, map[string]any{"group": item})
}
func (h *Handler) group(w http.ResponseWriter, r *http.Request, groupID int64) {
	if groupID < 1 {
		fail(w, 404, "not_found")
		return
	}
	p, ok := h.write(w, r)
	if !ok {
		return
	}
	key, ok := requestKey(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodPatch:
		var in struct {
			Name            string `json:"name"`
			SortOrder       int    `json:"sort_order"`
			ExpectedVersion int64  `json:"expected_version"`
		}
		if !decode(w, r, &in) {
			return
		}
		item, e := h.service.UpdateGroup(r.Context(), segmentapp.GroupCommand{ID: groupID, Name: in.Name, SortOrder: in.SortOrder, ExpectedVersion: in.ExpectedVersion, Actor: p.InternalID, IdempotencyKey: key})
		if e != nil {
			resultError(w, e)
			return
		}
		respond(w, 200, map[string]any{"group": item})
	case http.MethodDelete:
		e := h.service.DeleteGroup(r.Context(), segmentapp.VersionCommand{ID: groupID, ExpectedVersion: queryID(r, "expected_version"), Actor: p.InternalID, IdempotencyKey: key})
		if e != nil {
			resultError(w, e)
			return
		}
		respond(w, 200, map[string]any{"ok": true})
	default:
		method(w, "PATCH, DELETE")
	}
}
func (h *Handler) packages(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		if !h.read(w, r) {
			return
		}
		page, e := h.service.ListPackages(r.Context(), queryInt(r, "limit", 50), queryInt(r, "offset", 0), false)
		if e != nil {
			resultError(w, e)
			return
		}
		items := make([]map[string]any, 0, len(page.Items))
		for _, item := range page.Items {
			projected, projectionErr := h.packageReadDTO(r.Context(), item)
			if projectionErr != nil {
				resultError(w, projectionErr)
				return
			}
			items = append(items, projected)
		}
		respond(w, 200, map[string]any{"items": items, "total": page.Total, "limit": page.Limit, "offset": page.Offset})
		return
	}
	if r.Method != http.MethodPost {
		method(w, "GET, POST")
		return
	}
	p, ok := h.write(w, r)
	if !ok {
		return
	}
	var in struct {
		Name         string `json:"name"`
		GroupID      *int64 `json:"group_id"`
		TemplateKey  string `json:"template_key"`
		CreationMode string `json:"creation_mode"`
	}
	if !decode(w, r, &in) {
		return
	}
	key, ok := requestKey(w, r)
	if !ok {
		return
	}
	item, e := h.service.CreatePackage(r.Context(), segmentapp.PackageCreateCommand{Name: in.Name, GroupID: in.GroupID, TemplateKey: in.TemplateKey, CreationMode: in.CreationMode, Actor: p.InternalID, IdempotencyKey: key})
	if e != nil {
		resultError(w, e)
		return
	}
	respond(w, 201, map[string]any{"package": packageDTO(item)})
}
func (h *Handler) packageItem(w http.ResponseWriter, r *http.Request, packageID int64) {
	if packageID < 1 {
		fail(w, 404, "not_found")
		return
	}
	if r.Method == http.MethodGet {
		if !h.read(w, r) {
			return
		}
		item, e := h.service.GetPackage(r.Context(), packageID)
		if e != nil {
			resultError(w, e)
			return
		}
		projected, projectionErr := h.packageReadDTO(r.Context(), item)
		if projectionErr != nil {
			resultError(w, projectionErr)
			return
		}
		respond(w, 200, map[string]any{"package": projected})
		return
	}
	p, ok := h.write(w, r)
	if !ok {
		return
	}
	key, ok := requestKey(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodPatch:
		var in struct {
			Name            string `json:"name"`
			GroupID         *int64 `json:"group_id"`
			ExpectedVersion int64  `json:"expected_version"`
		}
		if !decode(w, r, &in) {
			return
		}
		item, e := h.service.UpdatePackage(r.Context(), segmentapp.PackageUpdateCommand{ID: packageID, Name: in.Name, GroupID: in.GroupID, ExpectedVersion: in.ExpectedVersion, Actor: p.InternalID, IdempotencyKey: key})
		if e != nil {
			resultError(w, e)
			return
		}
		respond(w, 200, map[string]any{"package": packageDTO(item)})
	case http.MethodDelete:
		item, e := h.service.TransitionPackage(r.Context(), segmentapp.VersionCommand{ID: packageID, ExpectedVersion: queryID(r, "expected_version"), Actor: p.InternalID, IdempotencyKey: key}, segmentdomain.Archived)
		if e != nil {
			resultError(w, e)
			return
		}
		respond(w, 200, map[string]any{"package": packageDTO(item)})
	default:
		method(w, "GET, PATCH, DELETE")
	}
}
func (h *Handler) packageAction(w http.ResponseWriter, r *http.Request, packageID int64, action string) {
	if action == "automation-binding" {
		h.binding(w, r, packageID)
		return
	}
	if action == "senders" {
		h.senders(w, r, packageID)
		return
	}
	if action == "precheck" {
		h.precheck(w, r, packageID)
		return
	}
	if action == "members" {
		h.members(w, r, packageID)
		return
	}
	if action == "configuration" {
		h.configuration(w, r, packageID)
		return
	}
	if action == "owner-references" {
		h.ownerReferenceList(w, r)
		return
	}
	if action == "preview" || action == "configuration-preview" {
		h.preview(w, r, packageID)
		return
	}
	if action == "refresh" || action == "configuration-materialize" || action == "refresh-runs" {
		h.refresh(w, r, packageID)
		return
	}
	if packageID < 1 || r.Method != http.MethodPost {
		method(w, "POST")
		return
	}
	p, ok := h.write(w, r)
	if !ok {
		return
	}
	key, ok := requestKey(w, r)
	if !ok {
		return
	}
	command := segmentapp.VersionCommand{ID: packageID, Actor: p.InternalID, IdempotencyKey: key}
	var item segmentdomain.Package
	var e error
	if action == "copy" {
		item, e = h.service.CopyPackage(r.Context(), command)
	} else {
		var in struct {
			ExpectedVersion int64 `json:"expected_version"`
		}
		if !decode(w, r, &in) {
			return
		}
		command.ExpectedVersion = in.ExpectedVersion
		switch action {
		case "pause":
			item, e = h.service.TransitionPackage(r.Context(), command, segmentdomain.Paused)
		case "activate":
			item, e = h.service.TransitionPackage(r.Context(), command, segmentdomain.Active)
		default:
			fail(w, 404, "not_found")
			return
		}
	}
	if e != nil {
		resultError(w, e)
		return
	}
	status := 200
	if action == "copy" {
		status = 201
	}
	respond(w, status, map[string]any{"package": packageDTO(item)})
}

// ownerReferenceList rehydrates frozen-form owner_userids from persisted local
// StaffIDs. It is a read-only Access Port bridge and never persists a userid.
func (h *Handler) ownerReferenceList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		method(w, "GET")
		return
	}
	if !h.read(w, r) {
		return
	}
	if h.ownerReferences == nil {
		fail(w, http.StatusServiceUnavailable, "owner_unavailable")
		return
	}
	ids := r.URL.Query()["staff_id"]
	if len(ids) > 100 {
		fail(w, http.StatusBadRequest, "invalid_request")
		return
	}
	values := make([]string, 0, len(ids))
	for _, raw := range ids {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id < 1 || strconv.FormatInt(id, 10) != raw {
			fail(w, http.StatusBadRequest, "invalid_request")
			return
		}
		value, found, err := h.ownerReferences.AudienceOwnerUserID(r.Context(), accessport.StaffID(id))
		if err != nil {
			fail(w, http.StatusServiceUnavailable, "owner_unavailable")
			return
		}
		if !found || value == "" {
			fail(w, http.StatusUnprocessableEntity, "owner_unknown")
			return
		}
		values = append(values, value)
	}
	respond(w, http.StatusOK, map[string]any{"owner_userids": values})
}
func (h *Handler) configuration(w http.ResponseWriter, r *http.Request, packageID int64) {
	if r.Method == http.MethodGet {
		if !h.read(w, r) {
			return
		}
		item, e := h.service.CurrentConfiguration(r.Context(), packageID)
		if e != nil {
			resultError(w, e)
			return
		}
		respond(w, 200, map[string]any{"configuration": configurationDTO(item)})
		return
	}
	if r.Method != http.MethodPut {
		method(w, "GET, PUT")
		return
	}
	p, ok := h.write(w, r)
	if !ok {
		return
	}
	key, ok := requestKey(w, r)
	if !ok {
		return
	}
	var in struct {
		ExpectedPackageVersion int64           `json:"expected_package_version"`
		RefreshCronUTC         string          `json:"refresh_cron_utc"`
		RefreshMode            string          `json:"refresh_mode"`
		Definition             json.RawMessage `json:"definition"`
	}
	if !decode(w, r, &in) {
		return
	}
	definition, e := h.normalizeDefinitionReferences(r.Context(), in.Definition)
	if e != nil {
		fail(w, http.StatusUnprocessableEntity, definitionReferenceErrorCode(e))
		return
	}
	item, e := h.service.PutConfiguration(r.Context(), segmentapp.ConfigurationCommand{PackageID: packageID, ExpectedPackageVersion: in.ExpectedPackageVersion, Definition: definition, RefreshCronUTC: in.RefreshCronUTC, RefreshMode: in.RefreshMode, Actor: p.InternalID, IdempotencyKey: key})
	if e != nil {
		resultError(w, e)
		return
	}
	respond(w, 200, map[string]any{"configuration": configurationDTO(item)})
}

// normalizeOwnerUserIDs is the frozen-form compatibility seam. Old forms send
// owner_userids; the persisted closed AST holds only Access-resolved local
// owner_staff_ids. Mixed or unknown input fails instead of widening a cohort.
func (h *Handler) normalizeOwnerUserIDs(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var definition struct {
		Parameters map[string]json.RawMessage `json:"parameters"`
	}
	if err := json.Unmarshal(raw, &definition); err != nil || definition.Parameters == nil {
		return nil, errOwnerInvalid
	}
	legacy, exists := definition.Parameters["owner_userids"]
	if !exists {
		var scope string
		if json.Unmarshal(definition.Parameters["owner_scope"], &scope) == nil && scope == "specified" && h.owners == nil {
			return nil, errOwnerUnavailable
		}
		return raw, nil
	}
	if _, mixed := definition.Parameters["owner_staff_ids"]; mixed {
		return nil, errOwnerInvalid
	}
	var scope string
	if err := json.Unmarshal(definition.Parameters["owner_scope"], &scope); err != nil || (scope != "all" && scope != "specified") {
		return nil, errOwnerInvalid
	}
	var values []string
	if err := json.Unmarshal(legacy, &values); err != nil || len(values) > 100 || (scope == "specified" && len(values) == 0) {
		return nil, errOwnerInvalid
	}
	// The frozen form sends an empty owner_userids list for all scope. It is a
	// valid no-filter selection and must not require a live Access projection.
	if scope == "all" && len(values) == 0 {
		definition.Parameters["owner_staff_ids"] = json.RawMessage("[]")
		delete(definition.Parameters, "owner_userids")
		return marshalNormalizedOwnerDefinition(raw, definition.Parameters)
	}
	if h.owners == nil {
		return nil, errOwnerUnavailable
	}
	staff := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		if value == "" || strings.TrimSpace(value) != value {
			return nil, errOwnerInvalid
		}
		resolved, found, err := h.owners.ResolveAudienceOwner(ctx, value)
		if err != nil {
			return nil, errOwnerUnavailable
		}
		if !found || resolved < 1 {
			return nil, errOwnerUnknown
		}
		id := strconv.FormatInt(int64(resolved), 10)
		if !seen[id] {
			seen[id] = true
			staff = append(staff, id)
		}
	}
	encoded, _ := json.Marshal(staff)
	definition.Parameters["owner_staff_ids"] = encoded
	delete(definition.Parameters, "owner_userids")
	return marshalNormalizedOwnerDefinition(raw, definition.Parameters)
}
func (h *Handler) normalizeDefinitionReferences(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	resolved, err := h.normalizeProductReferences(ctx, raw)
	if err != nil {
		return nil, err
	}
	resolved, err = h.normalizeChannelReferences(ctx, resolved)
	if err != nil {
		return nil, err
	}
	resolved, err = h.normalizeRadarReferences(ctx, resolved)
	if err != nil {
		return nil, err
	}
	resolved, err = h.normalizeSurveyReferences(ctx, resolved)
	if err != nil {
		return nil, err
	}
	return h.normalizeOwnerUserIDs(ctx, resolved)
}

func (h *Handler) normalizeProductReferences(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var definition struct {
		TemplateKey string                     `json:"template_key"`
		Parameters  map[string]json.RawMessage `json:"parameters"`
	}
	if err := json.Unmarshal(raw, &definition); err != nil || definition.Parameters == nil {
		return nil, errReferenceInvalid
	}
	if definition.TemplateKey != "paid_order" {
		return raw, nil
	}
	var values []string
	if err := json.Unmarshal(definition.Parameters["product_codes"], &values); err != nil || len(values) == 0 || len(values) > 100 {
		return nil, errReferenceInvalid
	}
	if h.products == nil {
		return nil, errReferenceUnavailable
	}
	resolved := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		if value == "" || strings.TrimSpace(value) != value {
			return nil, errReferenceInvalid
		}
		code, found, err := h.products.ResolveAudienceProduct(ctx, value)
		if err != nil {
			return nil, errReferenceUnavailable
		}
		if !found || code == "" {
			return nil, errReferenceUnknown
		}
		if !seen[code] {
			seen[code] = true
			resolved = append(resolved, code)
		}
	}
	encoded, err := json.Marshal(resolved)
	if err != nil {
		return nil, errReferenceInvalid
	}
	definition.Parameters["product_codes"] = encoded
	return marshalNormalizedOwnerDefinition(raw, definition.Parameters)
}

func (h *Handler) normalizeChannelReferences(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	return normalizeReferenceList(ctx, raw, "channel_entry", "channel_codes", h.channels != nil, func(ctx context.Context, value string) (string, bool, error) {
		return h.channels.ResolveAudienceChannel(ctx, value)
	})
}

func (h *Handler) normalizeRadarReferences(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	return normalizeReferenceList(ctx, raw, "radar_first_click_elapsed", "radar_ids", h.radars != nil, func(ctx context.Context, value string) (string, bool, error) {
		return h.radars.ResolveAudienceRadar(ctx, value)
	})
}

func normalizeReferenceList(ctx context.Context, raw json.RawMessage, templateKey, parameter string, available bool, resolve func(context.Context, string) (string, bool, error)) (json.RawMessage, error) {
	var definition struct {
		TemplateKey string                     `json:"template_key"`
		Parameters  map[string]json.RawMessage `json:"parameters"`
	}
	if err := json.Unmarshal(raw, &definition); err != nil || definition.Parameters == nil {
		return nil, errReferenceInvalid
	}
	if definition.TemplateKey != templateKey {
		return raw, nil
	}
	var values []string
	if err := json.Unmarshal(definition.Parameters[parameter], &values); err != nil || len(values) == 0 || len(values) > 100 {
		return nil, errReferenceInvalid
	}
	if !available {
		return nil, errReferenceUnavailable
	}
	resolved := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		if value == "" || strings.TrimSpace(value) != value {
			return nil, errReferenceInvalid
		}
		canonical, found, err := resolve(ctx, value)
		if err != nil {
			return nil, errReferenceUnavailable
		}
		if !found || canonical == "" {
			return nil, errReferenceUnknown
		}
		if !seen[canonical] {
			seen[canonical] = true
			resolved = append(resolved, canonical)
		}
	}
	encoded, err := json.Marshal(resolved)
	if err != nil {
		return nil, errReferenceInvalid
	}
	definition.Parameters[parameter] = encoded
	return marshalNormalizedOwnerDefinition(raw, definition.Parameters)
}

func (h *Handler) normalizeSurveyReferences(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var definition struct {
		TemplateKey string                     `json:"template_key"`
		Parameters  map[string]json.RawMessage `json:"parameters"`
	}
	if err := json.Unmarshal(raw, &definition); err != nil || definition.Parameters == nil {
		return nil, errReferenceInvalid
	}
	if definition.TemplateKey == "questionnaire_submissions" {
		return h.normalizeSubmissionReferences(ctx, raw, definition.Parameters)
	}
	if definition.TemplateKey != "questionnaire_choice_answers" {
		return raw, nil
	}
	if h.surveys == nil {
		return nil, errReferenceUnavailable
	}
	var questionnaire string
	if err := json.Unmarshal(definition.Parameters["questionnaire_id"], &questionnaire); err != nil || !validReferenceValue(questionnaire) {
		return nil, errReferenceInvalid
	}
	questionnaireID, found, err := h.surveys.ResolveAudienceQuestionnaire(ctx, questionnaire)
	if err != nil {
		return nil, errReferenceUnavailable
	}
	if !found || questionnaireID == "" {
		return nil, errReferenceUnknown
	}
	var conditions []struct {
		QuestionID string   `json:"question_id"`
		OptionIDs  []string `json:"option_ids"`
	}
	if err := json.Unmarshal(definition.Parameters["conditions"], &conditions); err != nil || len(conditions) == 0 || len(conditions) > 100 {
		return nil, errReferenceInvalid
	}
	for index := range conditions {
		condition := &conditions[index]
		if !validReferenceValue(condition.QuestionID) || len(condition.OptionIDs) == 0 || len(condition.OptionIDs) > 100 {
			return nil, errReferenceInvalid
		}
		questionID, questionFound, questionErr := h.surveys.ResolveAudienceQuestion(ctx, questionnaireID, condition.QuestionID)
		if questionErr != nil {
			return nil, errReferenceUnavailable
		}
		if !questionFound || questionID == "" {
			return nil, errReferenceUnknown
		}
		condition.QuestionID = questionID
		options := make([]string, 0, len(condition.OptionIDs))
		seen := map[string]bool{}
		for _, value := range condition.OptionIDs {
			if !validReferenceValue(value) {
				return nil, errReferenceInvalid
			}
			optionID, optionFound, optionErr := h.surveys.ResolveAudienceOption(ctx, questionnaireID, questionID, value)
			if optionErr != nil {
				return nil, errReferenceUnavailable
			}
			if !optionFound || optionID == "" {
				return nil, errReferenceUnknown
			}
			if !seen[optionID] {
				seen[optionID] = true
				options = append(options, optionID)
			}
		}
		condition.OptionIDs = options
	}
	questionnaireRaw, err := json.Marshal(questionnaireID)
	if err != nil {
		return nil, errReferenceInvalid
	}
	conditionsRaw, err := json.Marshal(conditions)
	if err != nil {
		return nil, errReferenceInvalid
	}
	definition.Parameters["questionnaire_id"] = questionnaireRaw
	definition.Parameters["conditions"] = conditionsRaw
	return marshalNormalizedOwnerDefinition(raw, definition.Parameters)
}

func validReferenceValue(value string) bool {
	return value != "" && strings.TrimSpace(value) == value && len([]rune(value)) <= 200
}

func marshalNormalizedOwnerDefinition(raw json.RawMessage, parameters map[string]json.RawMessage) (json.RawMessage, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(parameters)
	if err != nil {
		return nil, err
	}
	envelope["parameters"] = encoded
	return json.Marshal(envelope)
}

func (h *Handler) binding(w http.ResponseWriter, r *http.Request, packageID int64) {
	if h.execution == nil {
		resultError(w, segmentapp.ErrNotReady)
		return
	}
	if r.Method == http.MethodGet {
		if !h.read(w, r) {
			return
		}
		item, err := h.execution.CurrentBinding(r.Context(), packageID)
		if err != nil {
			resultError(w, err)
			return
		}
		respond(w, 200, map[string]any{"binding": item})
		return
	}
	if r.Method != http.MethodPut && r.Method != http.MethodDelete {
		method(w, "GET, PUT, DELETE")
		return
	}
	principal, ok := h.write(w, r)
	if !ok {
		return
	}
	key, ok := requestKey(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodDelete {
		err := h.execution.DeleteBinding(r.Context(), segmentapp.VersionCommand{ID: packageID, ExpectedVersion: queryID(r, "expected_version"), Actor: principal.InternalID, IdempotencyKey: key})
		if err != nil {
			resultError(w, err)
			return
		}
		respond(w, 200, map[string]any{"ok": true})
		return
	}
	var in struct {
		ExpectedVersion  int64  `json:"expected_version"`
		AgentID          int64  `json:"agent_id"`
		PublishedVersion int64  `json:"published_version"`
		AgentDigest      string `json:"agent_digest"`
	}
	if !decode(w, r, &in) {
		return
	}
	item, err := h.execution.PutBinding(r.Context(), segmentapp.BindingCommand{PackageID: packageID, ExpectedPackageVersion: in.ExpectedVersion, AgentID: automationport.AgentID(in.AgentID), ExpectedPublishedVersion: in.PublishedVersion, ExpectedAgentDigest: in.AgentDigest, Actor: principal.InternalID, IdempotencyKey: key})
	if err != nil {
		resultError(w, err)
		return
	}
	respond(w, 200, map[string]any{"binding": item})
}
func (h *Handler) senders(w http.ResponseWriter, r *http.Request, packageID int64) {
	if h.execution == nil {
		resultError(w, segmentapp.ErrNotReady)
		return
	}
	if r.Method == http.MethodGet {
		if !h.read(w, r) {
			return
		}
		item, err := h.execution.CurrentSenderSet(r.Context(), packageID)
		if err != nil {
			resultError(w, err)
			return
		}
		respond(w, 200, map[string]any{"sender_set": item})
		return
	}
	if r.Method != http.MethodPut {
		method(w, "GET, PUT")
		return
	}
	principal, ok := h.write(w, r)
	if !ok {
		return
	}
	key, ok := requestKey(w, r)
	if !ok {
		return
	}
	var in struct {
		ExpectedVersion          int64    `json:"expected_version"`
		ProviderMemberReferences []string `json:"provider_member_references"`
	}
	if !decode(w, r, &in) {
		return
	}
	item, err := h.execution.ReplaceSenders(r.Context(), segmentapp.SendersCommand{PackageID: packageID, ExpectedPackageVersion: in.ExpectedVersion, ProviderMemberIDs: in.ProviderMemberReferences, Actor: principal.InternalID, IdempotencyKey: key})
	if err != nil {
		resultError(w, err)
		return
	}
	respond(w, 200, map[string]any{"sender_set": item})
}
func (h *Handler) precheck(w http.ResponseWriter, r *http.Request, packageID int64) {
	if r.Method != http.MethodPost {
		method(w, "POST")
		return
	}
	if !h.read(w, r) {
		return
	}
	if h.execution == nil {
		resultError(w, segmentapp.ErrNotReady)
		return
	}
	result, err := h.execution.Precheck(r.Context(), packageID)
	if err != nil {
		resultError(w, err)
		return
	}
	respond(w, 200, map[string]any{"precheck": result})
}
func (h *Handler) preview(w http.ResponseWriter, r *http.Request, packageID int64) {
	if r.Method != http.MethodPost {
		method(w, "POST")
		return
	}
	if !h.read(w, r) {
		return
	}
	if h.snapshots == nil {
		resultError(w, segmentapp.ErrNotReady)
		return
	}
	var in struct {
		ReferenceTime time.Time       `json:"reference_time"`
		Definition    json.RawMessage `json:"definition"`
	}
	if !decode(w, r, &in) {
		return
	}
	var value segmentapp.Preview
	var err error
	if len(in.Definition) == 0 {
		value, err = h.snapshots.Preview(r.Context(), packageID, in.ReferenceTime)
	} else {
		definition, normalizeErr := h.normalizeDefinitionReferences(r.Context(), in.Definition)
		if normalizeErr != nil {
			fail(w, http.StatusUnprocessableEntity, definitionReferenceErrorCode(normalizeErr))
			return
		}
		value, err = h.snapshots.PreviewDefinition(r.Context(), packageID, definition, in.ReferenceTime)
	}
	if err != nil {
		resultError(w, err)
		return
	}
	respond(w, http.StatusOK, map[string]any{"preview": value})
}
func (h *Handler) refresh(w http.ResponseWriter, r *http.Request, packageID int64) {
	if r.Method != http.MethodPost {
		method(w, "POST")
		return
	}
	p, ok := h.write(w, r)
	if !ok {
		return
	}
	if h.snapshots == nil {
		resultError(w, segmentapp.ErrNotReady)
		return
	}
	key, ok := requestKey(w, r)
	if !ok {
		return
	}
	var in struct {
		ReferenceTime time.Time `json:"reference_time"`
	}
	if !decode(w, r, &in) {
		return
	}
	run, err := h.snapshots.AcceptRefresh(r.Context(), segmentapp.RefreshCommand{PackageID: packageID, Actor: p.InternalID, IdempotencyKey: key, ReferenceTime: in.ReferenceTime})
	if err != nil {
		resultError(w, err)
		return
	}
	respond(w, http.StatusAccepted, map[string]any{"refresh_run": run})
}
func (h *Handler) refreshRun(w http.ResponseWriter, r *http.Request, runID int64) {
	if r.Method != http.MethodGet {
		method(w, "GET")
		return
	}
	if !h.read(w, r) {
		return
	}
	if h.snapshots == nil {
		resultError(w, segmentapp.ErrNotReady)
		return
	}
	run, err := h.snapshots.GetRefresh(r.Context(), runID)
	if err != nil {
		resultError(w, err)
		return
	}
	respond(w, http.StatusOK, map[string]any{"refresh_run": run})
}
func (h *Handler) members(w http.ResponseWriter, r *http.Request, packageID int64) {
	if r.Method != http.MethodGet {
		method(w, "GET")
		return
	}
	if !h.read(w, r) {
		return
	}
	if h.snapshots == nil {
		resultError(w, segmentapp.ErrNotReady)
		return
	}
	snapshot, found, err := h.snapshots.PublishedSnapshot(r.Context(), segmentport.PackageID(packageID))
	if err != nil {
		resultError(w, err)
		return
	}
	if !found {
		fail(w, 404, "not_found")
		return
	}
	requested := queryID(r, "snapshot_id")
	if requested > 0 && requested != int64(snapshot.ID) {
		fail(w, 409, "snapshot_conflict")
		return
	}
	limit := queryInt(r, "limit", 100)
	page, err := h.snapshots.Members(r.Context(), snapshot.ID, r.URL.Query().Get("cursor"), limit)
	if err != nil {
		resultError(w, err)
		return
	}
	if h.core != nil {
		for i := range page.Items {
			detail, e := h.core.MemberDetail(r.Context(), packageID, int64(page.Items[i].CustomerID), "", 1)
			if e != nil {
				resultError(w, e)
				return
			}
			page.Items[i].Operations = &detail
		}
	}
	respond(w, http.StatusOK, map[string]any{"snapshot": snapshot, "items": page.Items, "next_cursor": page.NextCursor})
}
func (h *Handler) templates(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		method(w, "GET")
		return
	}
	if !h.read(w, r) {
		return
	}
	respond(w, 200, map[string]any{"items": segmentapp.Templates()})
}
func (h *Handler) read(w http.ResponseWriter, r *http.Request) bool {
	p, e := h.security.Authenticate(r.Context(), r)
	if e != nil {
		fail(w, 401, "unauthorized")
		return false
	}
	if !role(p, false) {
		fail(w, 403, "forbidden")
		return false
	}
	return true
}
func (h *Handler) write(w http.ResponseWriter, r *http.Request) (accessdomain.Principal, bool) {
	p, e := h.security.Authenticate(r.Context(), r)
	if e != nil {
		fail(w, 401, "unauthorized")
		return p, false
	}
	if !role(p, true) {
		fail(w, 403, "forbidden")
		return p, false
	}
	if _, e = h.security.AuthorizeCSRF(r.Context(), r); e != nil {
		fail(w, 403, "csrf_required")
		return p, false
	}
	return p, true
}
func role(p accessdomain.Principal, write bool) bool {
	if p.InternalID < 1 || (p.Kind != accessdomain.KindAdmin && p.Kind != accessdomain.KindStaff) {
		return false
	}
	for _, r := range p.Roles {
		if write && (r == accessdomain.RoleAdmin || r == accessdomain.RoleSuperAdmin) {
			return true
		}
		if !write && (r == accessdomain.RoleViewer || r == accessdomain.RoleAdmin || r == accessdomain.RoleSuperAdmin) {
			return true
		}
	}
	return false
}
func decode(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	e := d.Decode(target)
	if e == nil {
		var extra any
		e = d.Decode(&extra)
		if errors.Is(e, io.EOF) {
			e = nil
		}
	}
	if e == nil {
		return true
	}
	var max *http.MaxBytesError
	if errors.As(e, &max) {
		fail(w, 413, "request_too_large")
	} else {
		fail(w, 400, "invalid_request")
	}
	return false
}
func requestKey(w http.ResponseWriter, r *http.Request) (string, bool) {
	values := r.Header.Values("Idempotency-Key")
	if len(values) != 1 {
		fail(w, 400, "invalid_idempotency_key")
		return "", false
	}
	key := strings.TrimSpace(values[0])
	if len(key) < 16 || len(key) > 128 || key != values[0] || strings.ContainsAny(key, "\x00\r\n") {
		fail(w, 400, "invalid_idempotency_key")
		return "", false
	}
	return key, true
}
func id(v string) int64                       { n, _ := strconv.ParseInt(v, 10, 64); return n }
func queryID(r *http.Request, n string) int64 { return id(r.URL.Query().Get(n)) }
func queryInt(r *http.Request, n string, f int) int {
	if r.URL.Query().Get(n) == "" {
		return f
	}
	v, e := strconv.Atoi(r.URL.Query().Get(n))
	if e != nil {
		return -1
	}
	return v
}
func packageDTO(p segmentdomain.Package) map[string]any {
	v := map[string]any{"id": p.ID, "code": p.Code, "name": p.Name, "lifecycle": p.Lifecycle, "version": p.Version, "readiness": "not_ready"}
	if p.GroupID != nil {
		v["group_id"] = *p.GroupID
	}
	if p.CurrentConfigurationVersionID != nil {
		v["configuration_version_id"] = *p.CurrentConfigurationVersionID
	}
	if p.PublishedSnapshotID != nil {
		v["published_snapshot_id"] = *p.PublishedSnapshotID
	}
	if p.CurrentAutomationBindingID != nil {
		v["automation_binding_id"] = *p.CurrentAutomationBindingID
	}
	if p.CurrentSenderSetID != nil {
		v["sender_set_id"] = *p.CurrentSenderSetID
	}
	return v
}
func (h *Handler) packageReadDTO(ctx context.Context, p segmentdomain.Package) (map[string]any, error) {
	v := packageDTO(p)
	v["member_count"] = 0
	v["membership_mode"] = "empty"
	if p.CurrentConfigurationVersionID != nil {
		cfg, err := h.service.CurrentConfiguration(ctx, p.ID)
		if err != nil {
			return nil, err
		}
		v["membership_mode"] = "rule"
		var definition struct {
			TemplateKey string `json:"template_key"`
			Parameters  struct {
				CoreProductID int64 `json:"core_product_id"`
			} `json:"parameters"`
		}
		if err := json.Unmarshal(cfg.Definition, &definition); err != nil {
			return nil, err
		}
		if definition.TemplateKey == "core_ai_product" {
			v["membership_mode"] = "core_ai"
			v["core_product_id"] = definition.Parameters.CoreProductID
		}
	}
	if h == nil || h.snapshots == nil {
		return v, nil
	}
	snapshot, found, err := h.snapshots.PublishedSnapshot(ctx, segmentport.PackageID(p.ID))
	if err != nil {
		return nil, err
	}
	if !found {
		return v, nil
	}
	v["member_count"] = snapshot.MemberCount
	v["reference_time"] = snapshot.ReferenceTime
	if snapshot.PublishedAt != nil {
		v["published_at"] = *snapshot.PublishedAt
	}
	return v, nil
}
func configurationDTO(v segmentdomain.ConfigurationVersion) map[string]any {
	return map[string]any{"id": v.ID, "package_id": v.PackageID, "version": v.Version, "digest": hex.EncodeToString(v.Digest[:]), "definition": json.RawMessage(v.Definition), "refresh_cron_utc": v.RefreshCronUTC, "refresh_mode": v.RefreshMode}
}
func resultError(w http.ResponseWriter, e error) {
	switch {
	case errors.Is(e, segmentapp.ErrInvalid):
		fail(w, 400, "invalid_request")
	case errors.Is(e, segmentapp.ErrUnsupportedDefinition):
		fail(w, 422, "definition_not_supported")
	case errors.Is(e, segmentapp.ErrNotFound):
		fail(w, 404, "not_found")
	case errors.Is(e, segmentapp.ErrConflict):
		fail(w, 409, "conflict")
	case errors.Is(e, segmentapp.ErrNotReady):
		fail(w, 503, "capability_not_ready")
	default:
		fail(w, 503, "temporarily_unavailable")
	}
}
func method(w http.ResponseWriter, a string) {
	w.Header().Set("Allow", a)
	fail(w, 405, "method_not_allowed")
}
func fail(w http.ResponseWriter, s int, c string) { respond(w, s, map[string]any{"error": c}) }
func respond(w http.ResponseWriter, s int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(s)
	_ = json.NewEncoder(w).Encode(v)
}
