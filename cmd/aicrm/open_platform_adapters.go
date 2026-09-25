package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accessport "github.com/qianlan33333-png/AI-CRM-v3/internal/access/port"
	aiassistantport "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/port"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	archiveport "github.com/qianlan33333-png/AI-CRM-v3/internal/messagearchive/port"
	openplatformport "github.com/qianlan33333-png/AI-CRM-v3/internal/openplatform/port"
	orderdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	radarport "github.com/qianlan33333-png/AI-CRM-v3/internal/radar/port"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

var (
	errOpenPlatformRouteUnavailable    = errors.New("open platform route is not composed")
	errOpenPlatformResourceOutOfScope  = errors.New("machine client cannot access this resource")
	errOpenPlatformIdentityConflict    = errors.New("identity references resolve to different customers")
	errOpenPlatformIdentityNotFound    = errors.New("identity reference not found")
	errOpenPlatformIdentityPending     = errors.New("identity reference is pending")
	errOpenPlatformIdentityScopeDenied = errors.New("identity scope is not configured for this platform")
	errOpenPlatformOwnerUnavailable    = errors.New("customer owner projection is unavailable")
)

type openPlatformIdentityScopes struct {
	WeComScope        string
	UnionScopes       []string
	SurveyUnionScopes []string
	OpenIDScopes      []string
}

// openPlatformExternalUserIDReader is the small composition bridge used only
// after API-client authorization and OneID resolution. It returns a verified
// current WeCom identity transiently for the frozen machine-read response; the
// Host must neither log nor persist it.
type openPlatformExternalUserIDReader interface {
	VerifiedExternalUserID(context.Context, customerdomain.CustomerID, string) (string, bool, error)
}

// openPlatformSurveyIdentityReader supplies only current, already-verified
// aliases for the external Survey compatibility response. Historical union
// data stays in Survey; this reader never resolves or links identities.
type openPlatformSurveyIdentityReader interface {
	VerifiedUnionID(context.Context, customerdomain.CustomerID, string) (string, bool, error)
	RevealPhoneForMachine(context.Context, customerdomain.CustomerID, accessdomain.MachinePrincipal) (string, bool, error)
}

type openPlatformExecutor struct {
	coreAudience interface {
		segmentport.CoreOperationsReader
		segmentport.CoreSupervision
	}
	identity            identityport.Resolver
	externalUsers       openPlatformExternalUserIDReader
	orders              orderport.Query
	scopedOrders        orderport.CustomerScopedQuery
	profiles            customerport.SidebarProfileService
	archive             archiveport.CustomerMessageReader
	externalChat        archiveport.ExternalChatRecordReader
	radarLinks          radarport.ExternalLinkMappingReader
	radarClicks         radarport.ExternalClickReader
	survey              surveyport.ExternalSubmissionReader
	surveyAliases       openPlatformSurveyIdentityReader
	timeline            customerport.CustomerTimelineReader
	owners              wecomport.AudiencePrimaryOwnerReader
	contacts            wecomport.MachineContactPageReader
	contactTouches      archiveport.MachineContactTouchReader
	contactStatuses     customerport.MachineContactStatusReader
	contactStaff        accessport.Repository
	contactWindows      openplatformport.CustomerWindowRepository
	contactReadUOW      platformport.UnitOfWork
	contactWriteUOW     platformport.UnitOfWork
	customerDetails     wecomport.CustomerBusinessDetailReader
	scopes              openPlatformIdentityScopes
	activities          *openPlatformActivityReaders
	activityNow         func() time.Time
	operationAudit      *openPlatformOperationAuditor
	aiMachineIntake     aiassistantport.MachineTransactionalIntake
	aiMachineReader     aiassistantport.MachineReader
	aiUOW               platformport.UnitOfWork
	workbenchUnions     identityport.VerifiedScopedUnionReader
	v1Orders            orderport.ExternalReadQueryService
	v1OrderTimeline     orderport.ExternalOrderTimelineReader
	v1Refunds           paymentport.ExternalOrderRefundReader
	v1OrderCursorKey    []byte
	v1OrderUOW          platformport.UnitOfWork
	v1ExternalCursorKey []byte
}

func (executor *openPlatformExecutor) BindV1CustomerList(contacts wecomport.MachineContactPageReader, statuses customerport.MachineContactStatusReader, staff accessport.Repository, windows openplatformport.CustomerWindowRepository, readUOW, writeUOW platformport.UnitOfWork) error {
	if executor == nil || contacts == nil || statuses == nil || staff == nil || windows == nil || readUOW == nil || writeUOW == nil {
		return errOpenPlatformRouteUnavailable
	}
	executor.contacts, executor.contactStatuses, executor.contactStaff, executor.contactWindows = contacts, statuses, staff, windows
	executor.contactReadUOW, executor.contactWriteUOW = readUOW, writeUOW
	return nil
}

func (executor *openPlatformExecutor) BindV1ContactTouches(reader archiveport.MachineContactTouchReader) error {
	if executor == nil || reader == nil {
		return errOpenPlatformRouteUnavailable
	}
	executor.contactTouches = reader
	return nil
}

func (executor *openPlatformExecutor) BindV1Orders(orders orderport.ExternalReadQueryService, refunds paymentport.ExternalOrderRefundReader, uow platformport.UnitOfWork, signingKey []byte) error {
	if executor == nil || orders == nil || refunds == nil || uow == nil || len(signingKey) < 16 {
		return errOpenPlatformRouteUnavailable
	}
	timeline, ok := orders.(orderport.ExternalOrderTimelineReader)
	if !ok || timeline == nil {
		return errOpenPlatformRouteUnavailable
	}
	executor.v1Orders, executor.v1Refunds, executor.v1OrderCursorKey = orders, refunds, append([]byte(nil), signingKey...)
	executor.v1OrderTimeline = timeline
	executor.v1OrderUOW = uow
	return nil
}

// BindV1ExternalCursorKey keeps signed cursors for independent external
// record families separate from order pagination. The same process-local key
// material is acceptable; the cursor payload's operation discriminator keeps
// the MAC domains distinct.
func (executor *openPlatformExecutor) BindV1ExternalCursorKey(signingKey []byte) error {
	if executor == nil || len(signingKey) < 16 {
		return errOpenPlatformRouteUnavailable
	}
	executor.v1ExternalCursorKey = append([]byte(nil), signingKey...)
	return nil
}

func newOpenPlatformExecutor(identity identityport.Resolver, orders orderport.Query, profiles customerport.SidebarProfileService, archive archiveport.CustomerMessageReader, timeline customerport.CustomerTimelineReader, owners wecomport.AudiencePrimaryOwnerReader, scopes openPlatformIdentityScopes) (*openPlatformExecutor, error) {
	if identity == nil || orders == nil || profiles == nil || archive == nil || timeline == nil || owners == nil {
		return nil, errors.New("open platform core Port dependencies are required")
	}
	scopedOrders, ok := orders.(orderport.CustomerScopedQuery)
	if !ok || scopedOrders == nil {
		return nil, errors.New("open platform customer-scoped order Port is required")
	}
	externalChat, ok := archive.(archiveport.ExternalChatRecordReader)
	if !ok || externalChat == nil {
		return nil, errors.New("open platform external chat Port is required")
	}
	externalUsers, ok := identity.(openPlatformExternalUserIDReader)
	if !ok || externalUsers == nil {
		return nil, errors.New("open platform verified external identity Port is required")
	}
	scopes.WeComScope = strings.TrimSpace(scopes.WeComScope)
	scopes.UnionScopes = distinctScopes(scopes.UnionScopes, "wechat-open-platform:")
	// Survey's historical UnionID projection is tied to one declared donor
	// Open Platform scope. Do not fall back to the broad UnionID set here:
	// an HXC or another integration scope can resolve the same customer but
	// must never authorize release of questionnaire history.
	scopes.SurveyUnionScopes = distinctScopes(scopes.SurveyUnionScopes, "wechat-open-platform:")
	scopes.OpenIDScopes = distinctScopes(scopes.OpenIDScopes, "wechat-app:")
	return &openPlatformExecutor{identity: identity, externalUsers: externalUsers, orders: orders, scopedOrders: scopedOrders, profiles: profiles, archive: archive, externalChat: externalChat, timeline: timeline, owners: owners, scopes: scopes, activityNow: time.Now}, nil
}

// BindV1AI installs the AI owner Ports for the two V1 AI operations. The
// intake is transactional so the plan, its AI facts, and the machine operation
// audit can commit or roll back together.
func (executor *openPlatformExecutor) BindV1AI(intake aiassistantport.MachineTransactionalIntake, reader aiassistantport.MachineReader, uow platformport.UnitOfWork) error {
	if executor == nil || intake == nil || reader == nil || uow == nil {
		return errOpenPlatformRouteUnavailable
	}
	executor.aiMachineIntake, executor.aiMachineReader, executor.aiUOW = intake, reader, uow
	return nil
}

func (executor *openPlatformExecutor) BindV1WorkbenchUnions(reader identityport.VerifiedScopedUnionReader) error {
	if executor == nil || reader == nil {
		return errOpenPlatformRouteUnavailable
	}
	executor.workbenchUnions = reader
	return nil
}

// BindV1OperationAudit installs the Access-owned audit writer used for each
// V1 invocation. Composition passes the same PostgreSQL Unit of Work that
// owns Access, so no cross-domain table access is introduced.
func (executor *openPlatformExecutor) BindV1OperationAudit(writer openPlatformMachineAuditWriter, uow platformport.UnitOfWork) error {
	if executor == nil || writer == nil || uow == nil {
		return errOpenPlatformRouteUnavailable
	}
	executor.operationAudit = &openPlatformOperationAuditor{writer: writer, uow: uow}
	return nil
}

// BindV1CustomerActivities installs the four owner-owned projections needed
// by the V1 aggregate stream. Every reader must be present: publishing a
// partial activity catalog would make a signed cursor silently omit facts.
func (executor *openPlatformExecutor) BindV1CustomerActivities(survey surveyport.CustomerHistoryReader, radar radarport.CustomerActivityReader, signingKey []byte) error {
	if executor == nil || executor.archive == nil || survey == nil || radar == nil || len(signingKey) < 32 {
		return errOpenPlatformRouteUnavailable
	}
	orders, ok := executor.orders.(orderport.CustomerActivityReader)
	if !ok || orders == nil {
		return errOpenPlatformRouteUnavailable
	}
	executor.activities = &openPlatformActivityReaders{
		messages:   executor.archive,
		survey:     survey,
		radar:      radar,
		orders:     orders,
		signingKey: append([]byte(nil), signingKey...),
	}
	return nil
}

// BindExternalRadarLinkMappings connects the Radar-owned historical mapping
// projection after all Radar dependencies are composed. The external route
// returns an explicit read-model-unavailable response until this Port is bound.
func (executor *openPlatformExecutor) BindExternalRadarLinkMappings(reader radarport.ExternalLinkMappingReader) error {
	if executor == nil || reader == nil {
		return radarport.ErrUnavailable
	}
	executor.radarLinks = reader
	return nil
}

// BindV1Radar binds the two Radar-owned public read projections together so
// neither route can be published against a partial Radar composition.
func (executor *openPlatformExecutor) BindV1Radar(clicks radarport.ExternalClickReader, links radarport.ExternalLinkMappingReader) error {
	if executor == nil || clicks == nil || links == nil {
		return radarport.ErrUnavailable
	}
	executor.radarClicks, executor.radarLinks = clicks, links
	return nil
}

// BindV1CustomerDetails installs the WeCom-owned business-detail projection.
// A partial composition must not silently turn unavailable provider facts into
// empty customer fields.
func (executor *openPlatformExecutor) BindV1CustomerDetails(reader wecomport.CustomerBusinessDetailReader) error {
	if executor == nil || reader == nil {
		return errOpenPlatformRouteUnavailable
	}
	executor.customerDetails = reader
	return nil
}

// BindExternalSurveySubmissions installs the Survey-owned compatibility
// projection and the minimal Identity alias reader used only after OneID has
// resolved the request. Binding remains optional for configurations that do
// not expose the external Survey route; an unbound route reports unavailable.
func (executor *openPlatformExecutor) BindExternalSurveySubmissions(reader surveyport.ExternalSubmissionReader, aliases openPlatformSurveyIdentityReader) error {
	if executor == nil || reader == nil || aliases == nil {
		return surveyport.ErrUnavailable
	}
	executor.survey, executor.surveyAliases = reader, aliases
	return nil
}

func configuredOpenPlatformScopes(corpID string, unionScopes, appIDs []string) openPlatformIdentityScopes {
	weComScope := ""
	if corpID = strings.TrimSpace(corpID); corpID != "" {
		weComScope = "wecom-corp:" + corpID
	}
	openIDScopes := make([]string, 0, len(appIDs))
	for _, appID := range appIDs {
		if appID = strings.TrimSpace(appID); appID != "" {
			openIDScopes = append(openIDScopes, "wechat-app:"+appID)
		}
	}
	return openPlatformIdentityScopes{WeComScope: weComScope, UnionScopes: unionScopes, OpenIDScopes: openIDScopes}
}

func distinctScopes(values []string, prefix string) []string {
	result := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if !strings.HasPrefix(value, prefix) || len(value) == len(prefix) {
			continue
		}
		if _, exists := seen[value]; !exists {
			seen[value] = struct{}{}
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}

func (executor *openPlatformExecutor) Execute(ctx context.Context, request openplatformport.Request) (openplatformport.Response, error) {
	switch request.Method + " " + request.Path {
	case "GET /api/identity/resolve":
		return executor.resolveIdentity(ctx, request.Query, request.Principal)
	case "GET /api/external/users/resolve":
		return executor.resolveExternalUser(ctx, request.Query, request.Principal)
	case "GET /api/external/chat-records":
		return executor.listExternalChatRecords(ctx, request.Query, request.Principal)
	case "GET /api/external/questionnaire-submissions":
		return executor.listExternalSurveySubmissions(ctx, request.Query, request.Principal)
	case "GET /api/external/radar-links":
		return executor.listExternalRadarLinks(ctx, request.Query, request.Principal)
	case "GET /api/external/orders":
		return executor.listOrders(ctx, request.Query, request.Principal)
	case "GET /api/external/orders/{order_no}":
		return executor.getOrder(ctx, request.PathParts["order_no"], request.Principal)
	case "POST /mcp":
		return executor.callMCP(ctx, request.Body, request.Principal)
	default:
		return openplatformport.Response{}, errOpenPlatformRouteUnavailable
	}
}

func (executor *openPlatformExecutor) resolveIdentity(ctx context.Context, query url.Values, principal accessdomain.MachinePrincipal) (openplatformport.Response, error) {
	references, err := executor.referencesFromValues(query)
	if err != nil {
		return responseForIdentityError(err), nil
	}
	result, err := executor.resolveReferences(ctx, references)
	if err != nil {
		return responseForIdentityError(err), nil
	}
	if err := executor.ensureCustomerScope(ctx, principal, result.CustomerID, references); err != nil {
		return responseError(404, "not_found"), nil
	}
	profile, profileErr := executor.profiles.ReadSidebarProfile(ctx, result.CustomerID)
	if profileErr != nil {
		return responseError(503, "customer_profile_unavailable"), nil
	}
	return responseOK(map[string]any{"ok": true, "identity": map[string]any{"customer_id": result.CustomerID, "identity_id": result.IdentityID, "status": result.Status}, "customer": profile}), nil
}

func (executor *openPlatformExecutor) resolveExternalUser(ctx context.Context, query url.Values, principal accessdomain.MachinePrincipal) (openplatformport.Response, error) {
	references, err := executor.referencesFromValues(query)
	if err != nil {
		return responseForIdentityError(err), nil
	}
	result, err := executor.resolveReferences(ctx, references)
	if err != nil {
		return responseForIdentityError(err), nil
	}
	if err := executor.ensureCustomerScope(ctx, principal, result.CustomerID, references); err != nil {
		return responseError(404, "not_found"), nil
	}
	profile, profileErr := executor.profiles.ReadSidebarProfile(ctx, result.CustomerID)
	if profileErr != nil {
		return responseError(503, "customer_profile_unavailable"), nil
	}
	return responseOK(map[string]any{
		"ok":            true,
		"user":          externalUser(result, references, profile),
		"route_owner":   "ai_crm_next",
		"source_status": "external_user_basic",
		"fallback_used": false,
	}), nil
}

// externalUser preserves the frozen external-user envelope while keeping OneID
// as the source of truth. customer_id is the V3 canonical replacement for the
// donor's person record. Identifiers are returned only when supplied in a
// successfully resolved scoped request; the adapter never reconstructs them.
func externalUser(result identityport.ResolveResult, references []identitydomain.Reference, profile customerport.SidebarProfile) map[string]any {
	externalUserID, mobile, unionID, openID, matchedBy := "", "", "", "", ""
	for _, reference := range references {
		switch reference.Kind {
		case identitydomain.KindWeComExternalUserID:
			externalUserID = reference.Value
			if matchedBy == "" {
				matchedBy = "external_userid"
			}
		case identitydomain.KindPhone:
			mobile = reference.Value
			if matchedBy == "" {
				matchedBy = "mobile"
			}
		case identitydomain.KindUnionID:
			unionID = reference.Value
			if matchedBy == "" {
				matchedBy = "unionid"
			}
		case identitydomain.KindMPOpenID, identitydomain.KindOAOpenID:
			openID = reference.Value
			if matchedBy == "" {
				matchedBy = "openid"
			}
		}
	}
	detailURL := ""
	if externalUserID != "" {
		detailURL = "/api/customers/" + url.PathEscape(externalUserID)
	}
	return map[string]any{
		"person_id":           strconv.FormatInt(int64(result.CustomerID), 10),
		"external_userid":     externalUserID,
		"mobile":              mobile,
		"customer_name":       profile.DisplayName,
		"unionid":             unionID,
		"openid":              openID,
		"owner_userid":        "",
		"owner_display_name":  "",
		"remark":              "",
		"follow_user_userid":  "",
		"follow_user_userids": []string{},
		"binding_status":      profile.Status,
		"is_bound":            profile.PhoneMasked != "" || mobile != "",
		"matched_by":          matchedBy,
		"identity_map_id":     result.IdentityID,
		"detail_url":          detailURL,
	}
}

// ensureCustomerScope resolves all scope keys from trusted machine state and
// canonical/WeCom read ports. Request query values are never used as scope
// evidence. This check happens before a customer profile, archive, or order
// Port can read the resource.
func (executor *openPlatformExecutor) ensureCustomerScope(ctx context.Context, principal accessdomain.MachinePrincipal, customerID customerdomain.CustomerID, references []identitydomain.Reference) error {
	if len(principal.OwnerScope) == 0 {
		return nil
	}
	resources := map[string]string{"customer_id": strconv.FormatInt(int64(customerID), 10)}
	if principal.CorpID != "" {
		resources["corp_id"] = principal.CorpID
	}
	for _, reference := range references {
		resources[string(reference.Kind)] = reference.Value
		if reference.Kind == identitydomain.KindWeComExternalUserID {
			resources["external_userid"] = reference.Value
		}
	}
	if _, requiresOwner := principal.OwnerScope["owner_userid"]; requiresOwner {
		if executor.scopes.WeComScope == "" {
			return errOpenPlatformResourceOutOfScope
		}
		owners, err := executor.owners.AudiencePrimaryOwners(ctx, []customerdomain.CustomerID{customerID})
		if err != nil {
			return errOpenPlatformOwnerUnavailable
		}
		if len(owners) != 1 || owners[0].CustomerID != customerID || owners[0].Status != "known" || owners[0].OwnerUserID == "" || owners[0].CorpScope != executor.scopes.WeComScope {
			return errOpenPlatformResourceOutOfScope
		}
		resources["owner_userid"] = owners[0].OwnerUserID
	}
	if !principal.OwnerScope.Allows(resources) {
		return errOpenPlatformResourceOutOfScope
	}
	return nil
}

// allowsUnboundScope is only safe for routes whose owning Query Port has no
// resource scope input. The V3 service is single-corporation, so corp_id can
// be checked from the credential record; every customer or owner scope is
// denied before the broad query starts.
func (executor *openPlatformExecutor) allowsUnboundScope(principal accessdomain.MachinePrincipal) bool {
	if len(principal.OwnerScope) == 0 {
		return true
	}
	return principal.OwnerScope.Allows(map[string]string{"corp_id": principal.CorpID})
}

func (executor *openPlatformExecutor) listExternalChatRecords(ctx context.Context, values url.Values, principal accessdomain.MachinePrincipal) (openplatformport.Response, error) {
	query, references, matchedBy, startText, err := executor.externalChatQuery(values)
	if err != nil {
		return externalChatError(400, "invalid_request"), nil
	}
	resolved, err := executor.resolveReferences(ctx, references)
	if err != nil {
		return externalChatIdentityError(err), nil
	}
	if executor.scopes.WeComScope == "" {
		return externalChatIdentityError(errOpenPlatformIdentityScopeDenied), nil
	}
	externalUserID, found, identityErr := executor.externalUsers.VerifiedExternalUserID(ctx, resolved.CustomerID, executor.scopes.WeComScope)
	if identityErr != nil {
		return externalChatUnavailable(), nil
	}
	if !found || externalUserID == "" {
		return externalChatError(404, "not_found"), nil
	}
	trustedExternal, trustedErr := executor.trustedReference(identitydomain.KindWeComExternalUserID, executor.scopes.WeComScope, externalUserID, "open_platform.chat_read")
	if trustedErr != nil {
		return externalChatIdentityError(trustedErr), nil
	}
	scopeReferences := append(append([]identitydomain.Reference(nil), references...), trustedExternal)
	if err := executor.ensureCustomerScope(ctx, principal, resolved.CustomerID, scopeReferences); err != nil {
		return externalChatError(404, "not_found"), nil
	}
	query.CustomerID, query.ExternalUserID = resolved.CustomerID, trustedExternal.Value
	page, err := executor.externalChat.ExternalCustomerMessages(ctx, query)
	if err != nil {
		return externalChatUnavailable(), nil
	}
	items := make([]map[string]any, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, map[string]any{
			"msgid":           item.MessageID,
			"chat_scene":      item.ChatScene,
			"chat_type":       item.ChatType,
			"unionid":         item.UnionID,
			"external_userid": item.ExternalUserID,
			"with_userid":     item.WithUserID,
			"sender":          item.Sender,
			"receiver":        item.Receiver,
			"chat_id":         item.ChatID,
			"roomid":          item.RoomID,
			"group_name":      item.GroupName,
			"msgtype":         item.MessageType,
			"content":         item.Content,
			"media_id":        item.MediaID,
			"send_time":       item.OccurredAt.UTC().Format("2006-01-02 15:04:05"),
			"source_id":       item.SourceID,
		})
	}
	nextOffset := query.Offset + len(items)
	nextCursor := ""
	if int64(nextOffset) < page.Total {
		nextCursor = encodeExternalChatCursor(nextOffset)
	}
	return responseOK(map[string]any{
		"ok":              true,
		"items":           items,
		"messages":        items,
		"total":           page.Total,
		"count":           len(items),
		"limit":           query.Limit,
		"next_cursor":     nextCursor,
		"has_more":        nextCursor != "",
		"external_userid": externalUserID,
		"matched_by":      matchedBy,
		"filters": map[string]string{
			"chat_scene":  query.ChatScene,
			"start_time":  startText,
			"with_userid": query.WithUserID,
		},
		"route_owner":       "ai_crm_next",
		"source_status":     "external_chat_records",
		"read_model_status": "primary",
		"fallback_used":     false,
	}), nil
}

// externalChatQuery preserves the donor's declared HTTP shape. FastAPI ignores
// unrelated query values, takes the final value of repeated scalar parameters,
// and uses a fixed page size rather than accepting a caller-provided limit.
func (executor *openPlatformExecutor) externalChatQuery(values url.Values) (archiveport.ExternalChatRecordQuery, []identitydomain.Reference, string, string, error) {
	one := func(key string) (string, error) {
		items, exists := values[key]
		if !exists || len(items) == 0 {
			return "", nil
		}
		return strings.TrimSpace(items[len(items)-1]), nil
	}
	scene, err := one("chat_scene")
	if err != nil {
		return archiveport.ExternalChatRecordQuery{}, nil, "", "", err
	}
	switch strings.ToLower(scene) {
	case "private", "single", "私信":
		scene = "private"
	case "group", "群聊":
		scene = "group"
	default:
		return archiveport.ExternalChatRecordQuery{}, nil, "", "", errors.New("invalid chat scene")
	}
	startRaw, err := one("start_time")
	if err != nil || startRaw == "" {
		return archiveport.ExternalChatRecordQuery{}, nil, "", "", errors.New("start time required")
	}
	seconds, err := strconv.ParseInt(startRaw, 10, 64)
	if err != nil || seconds < 0 || seconds > 9_999_999_999 {
		return archiveport.ExternalChatRecordQuery{}, nil, "", "", errors.New("invalid start time")
	}
	withUserID, err := one("with_userid")
	if err != nil {
		return archiveport.ExternalChatRecordQuery{}, nil, "", "", err
	}
	if scene == "private" && withUserID == "" {
		withUserID = "HuangYouCan"
	}
	if scene == "group" {
		withUserID = ""
	}
	cursor, err := one("cursor")
	if err != nil {
		return archiveport.ExternalChatRecordQuery{}, nil, "", "", err
	}
	offset, err := decodeExternalChatCursor(cursor)
	if err != nil {
		return archiveport.ExternalChatRecordQuery{}, nil, "", "", err
	}
	identityValues := url.Values{}
	for _, key := range []string{"mobile", "unionid", "external_userid"} {
		if items, exists := values[key]; exists && len(items) > 0 {
			identityValues[key] = []string{items[len(items)-1]}
		}
	}
	references, err := executor.referencesFromValues(identityValues)
	if err != nil {
		return archiveport.ExternalChatRecordQuery{}, nil, "", "", err
	}
	matchedBy := "mobile"
	if value, _ := one("unionid"); value != "" {
		matchedBy = "unionid"
	}
	if value, _ := one("external_userid"); value != "" {
		matchedBy = "external_userid"
	}
	start := time.Unix(seconds, 0).UTC()
	return archiveport.ExternalChatRecordQuery{ChatScene: scene, StartAt: start, WithUserID: withUserID, Limit: 20, Offset: offset}, references, matchedBy, start.Format("2006-01-02 15:04:05"), nil
}

func encodeExternalChatCursor(offset int) string {
	return base64.RawURLEncoding.EncodeToString([]byte(`{"offset":` + strconv.Itoa(offset) + `}`))
}

func decodeExternalChatCursor(cursor string) (int, error) {
	if cursor == "" {
		return 0, nil
	}
	padded := cursor + strings.Repeat("=", (4-len(cursor)%4)%4)
	raw, err := base64.URLEncoding.DecodeString(padded)
	if err != nil {
		return 0, errors.New("invalid cursor")
	}
	var payload struct {
		Offset int `json:"offset"`
	}
	if err = json.Unmarshal(raw, &payload); err != nil || payload.Offset < 0 {
		return 0, errors.New("invalid cursor")
	}
	return payload.Offset, nil
}

func externalChatError(status int, code string) openplatformport.Response {
	return openplatformport.Response{Status: status, Body: map[string]any{
		"ok": false, "error_code": code, "route_owner": "ai_crm_next", "source_status": "external_chat_records", "fallback_used": false,
	}}
}

func externalChatIdentityError(err error) openplatformport.Response {
	response := responseForIdentityError(err)
	code, _ := response.Body.(map[string]any)["error_code"].(string)
	return externalChatError(response.Status, code)
}

func externalChatUnavailable() openplatformport.Response {
	return openplatformport.Response{Status: 503, Body: map[string]any{
		"ok": false, "degraded": true, "messages": []any{}, "items": []any{}, "count": 0,
		"source_status": "production_unavailable", "read_model_status": "unavailable", "fallback_used": false,
		"route_owner": "ai_crm_next", "error_code": "message_archive_read_unavailable", "page_error": "message archive read model unavailable",
	}}
}

func (executor *openPlatformExecutor) listExternalSurveySubmissions(ctx context.Context, values url.Values, principal accessdomain.MachinePrincipal) (openplatformport.Response, error) {
	query, references, filters, err := executor.externalSurveyQuery(values)
	if err != nil {
		return externalSurveyError(400, "invalid_request"), nil
	}
	resolved, err := executor.resolveReferences(ctx, references)
	if err != nil {
		return externalSurveyIdentityError(err), nil
	}
	if executor.survey == nil || executor.surveyAliases == nil {
		return externalSurveyUnavailable(), nil
	}
	unionIDs, unionReferences, err := executor.surveyHistoricalUnionIDs(ctx, resolved.CustomerID, references)
	if err != nil {
		return externalSurveyUnavailable(), nil
	}
	if err = executor.ensureCustomerScope(ctx, principal, resolved.CustomerID, append(append([]identitydomain.Reference(nil), references...), unionReferences...)); err != nil {
		return externalSurveyError(404, "not_found"), nil
	}
	query.CustomerID, query.HistoricalUnionIDs = int64(resolved.CustomerID), unionIDs
	page, err := executor.survey.ExternalSubmissions(ctx, query)
	if err != nil {
		switch {
		case errors.Is(err, surveyport.ErrConflict):
			// A mapped legacy questionnaire ID and an unrelated native numeric
			// ID are ambiguous. The Survey owner reports this deterministically;
			// keep it visible to the machine caller rather than disguising it as
			// transient read-model unavailability.
			return externalSurveyError(409, "conflict"), nil
		case errors.Is(err, surveyport.ErrInvalid):
			return externalSurveyError(400, "invalid_request"), nil
		default:
			return externalSurveyUnavailable(), nil
		}
	}

	mobile := surveyRequestAlias(references, identitydomain.KindPhone)
	if mobile == "" {
		mobile, _, err = executor.surveyAliases.RevealPhoneForMachine(ctx, resolved.CustomerID, principal)
		if err != nil {
			return externalSurveyUnavailable(), nil
		}
	}
	externalUserID := ""
	if executor.scopes.WeComScope != "" {
		externalUserID, _, err = executor.externalUsers.VerifiedExternalUserID(ctx, resolved.CustomerID, executor.scopes.WeComScope)
		if err != nil {
			return externalSurveyUnavailable(), nil
		}
	}
	nativeUnionID := ""
	if len(unionIDs) == 1 {
		nativeUnionID = unionIDs[0]
	}
	return externalSurveyResponse(page, filters, mobile, nativeUnionID, externalUserID), nil
}

// externalSurveyQuery freezes the donor's scalar request contract. Unrelated
// query fields are ignored by the donor router; known scalar inputs take their
// last supplied value, and all present identity/filter values are ANDed later.
func (executor *openPlatformExecutor) externalSurveyQuery(values url.Values) (surveyport.ExternalSubmissionQuery, []identitydomain.Reference, map[string]any, error) {
	one := func(key string) string {
		items := values[key]
		if len(items) == 0 {
			return ""
		}
		return strings.TrimSpace(items[len(items)-1])
	}
	identityValues := url.Values{}
	for _, key := range []string{"mobile", "unionid", "external_userid", "scope", "external_userid_scope"} {
		if value := one(key); value != "" {
			identityValues.Set(key, value)
		}
	}
	references, err := executor.referencesFromValues(identityValues)
	if err != nil {
		return surveyport.ExternalSubmissionQuery{}, nil, nil, err
	}
	query := surveyport.ExternalSubmissionQuery{Limit: 100}
	filters := map[string]any{}
	for _, key := range []string{"mobile", "unionid", "external_userid"} {
		if value := one(key); value != "" {
			filters[key] = value
		}
	}
	if raw := one("questionnaire_id"); raw != "" {
		id, parseErr := strconv.ParseInt(raw, 10, 64)
		if parseErr != nil || id < 1 {
			return surveyport.ExternalSubmissionQuery{}, nil, nil, errors.New("invalid questionnaire id")
		}
		query.QuestionnaireSourceID, filters["questionnaire_id"] = id, id
	}
	if raw := one("limit"); raw != "" {
		limit, parseErr := strconv.ParseInt(raw, 10, 32)
		if parseErr != nil || limit < 1 || limit > 500 {
			return surveyport.ExternalSubmissionQuery{}, nil, nil, errors.New("invalid limit")
		}
		query.Limit = int32(limit)
	}
	if raw := one("cursor"); raw != "" {
		offset, cursorErr := decodeExternalSurveyCursor(raw)
		if cursorErr != nil {
			return surveyport.ExternalSubmissionQuery{}, nil, nil, cursorErr
		}
		query.Offset = offset
	}
	if raw := one("submitted_from"); raw != "" {
		from, parseErr := unixSeconds(raw)
		if parseErr != nil {
			return surveyport.ExternalSubmissionQuery{}, nil, nil, parseErr
		}
		query.SubmittedFrom = *from
		filters["submitted_from"] = legacyExternalTimestamp(*from)
	}
	if raw := one("submitted_to"); raw != "" {
		to, parseErr := unixSeconds(raw)
		if parseErr != nil {
			return surveyport.ExternalSubmissionQuery{}, nil, nil, parseErr
		}
		query.SubmittedTo = *to
		filters["submitted_to"] = legacyExternalTimestamp(*to)
	}
	if !query.SubmittedFrom.IsZero() && !query.SubmittedTo.IsZero() && query.SubmittedFrom.After(query.SubmittedTo) {
		return surveyport.ExternalSubmissionQuery{}, nil, nil, errors.New("invalid submitted range")
	}
	return query, references, filters, nil
}

func decodeExternalSurveyCursor(cursor string) (int64, error) {
	padded := cursor + strings.Repeat("=", (4-len(cursor)%4)%4)
	raw, err := base64.URLEncoding.DecodeString(padded)
	if err != nil {
		return 0, errors.New("invalid cursor")
	}
	var payload struct {
		Offset json.RawMessage `json:"offset"`
	}
	if err = json.Unmarshal(raw, &payload); err != nil {
		return 0, errors.New("invalid cursor")
	}
	if len(payload.Offset) == 0 || string(payload.Offset) == "null" {
		return 0, nil
	}
	var offset int64
	if err = json.Unmarshal(payload.Offset, &offset); err != nil {
		var encoded string
		if json.Unmarshal(payload.Offset, &encoded) != nil || encoded == "" {
			return 0, errors.New("invalid cursor")
		}
		offset, err = strconv.ParseInt(encoded, 10, 64)
		if err != nil {
			return 0, errors.New("invalid cursor")
		}
	}
	if offset < 0 {
		return 0, nil
	}
	return offset, nil
}

func encodeExternalSurveyCursor(offset int64) string {
	if offset < 0 {
		offset = 0
	}
	encoded, err := json.Marshal(map[string]int64{"offset": offset})
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(encoded)
}

// surveyHistoricalUnionIDs collects only direct trusted union inputs and
// verified values from explicitly configured Survey Open Platform scopes. It
// cannot infer a UnionID from a phone/external user across an unrelated scope.
func (executor *openPlatformExecutor) surveyHistoricalUnionIDs(ctx context.Context, customerID customerdomain.CustomerID, references []identitydomain.Reference) ([]string, []identitydomain.Reference, error) {
	ids := make([]string, 0, len(references)+len(executor.scopes.SurveyUnionScopes))
	trusted := make([]identitydomain.Reference, 0, len(references)+len(executor.scopes.SurveyUnionScopes))
	seen := map[string]struct{}{}
	allowedSurveyScope := make(map[string]struct{}, len(executor.scopes.SurveyUnionScopes))
	for _, scope := range executor.scopes.SurveyUnionScopes {
		allowedSurveyScope[scope] = struct{}{}
	}
	appendUnion := func(reference identitydomain.Reference) {
		if reference.Kind != identitydomain.KindUnionID {
			return
		}
		// Historical questionnaire rows are keyed only by the donor UnionID
		// string. A same-looking value from an HXC or other configured Open
		// Platform scope may resolve to a different root, so never use it as a
		// historical selector. It still remains in the initial OneID resolution
		// and may authorize V3-native customer-scoped submissions.
		if _, allowed := allowedSurveyScope[reference.Scope]; !allowed {
			return
		}
		if _, exists := seen[reference.Value]; exists {
			return
		}
		seen[reference.Value] = struct{}{}
		ids, trusted = append(ids, reference.Value), append(trusted, reference)
	}
	for _, reference := range references {
		appendUnion(reference)
	}
	for _, scope := range executor.scopes.SurveyUnionScopes {
		unionID, found, readErr := executor.surveyAliases.VerifiedUnionID(ctx, customerID, scope)
		if readErr != nil {
			return nil, nil, readErr
		}
		if !found || unionID == "" {
			continue
		}
		reference, referenceErr := executor.trustedReference(identitydomain.KindUnionID, scope, unionID, "open_platform.survey_read")
		if referenceErr != nil {
			return nil, nil, referenceErr
		}
		appendUnion(reference)
	}
	return ids, trusted, nil
}

func surveyRequestAlias(references []identitydomain.Reference, kind identitydomain.Kind) string {
	for _, reference := range references {
		if reference.Kind == kind {
			return reference.Value
		}
	}
	return ""
}

func externalSurveyResponse(page surveyport.ExternalSubmissionPage, filters map[string]any, mobile, nativeUnionID, externalUserID string) openplatformport.Response {
	items := make([]map[string]any, 0, len(page.Items))
	for _, item := range page.Items {
		answers := make([]map[string]any, 0, len(item.Answers))
		for _, answer := range item.Answers {
			answers = append(answers, map[string]any{
				"question_title_snapshot":        answer.QuestionTitle,
				"selected_option_texts_snapshot": answer.SelectedOptionTexts,
				"text_value":                     answer.TextValue,
				"score_contribution":             answer.ScoreContribution,
			})
		}
		unionID := item.HistoricalUnionID
		if unionID == "" {
			unionID = nativeUnionID
		}
		items = append(items, map[string]any{
			"mobile":                     mobile,
			"unionid":                    unionID,
			"external_userid":            externalUserID,
			"submitted_at":               legacyExternalTimestamp(item.SubmittedAt),
			"questionnaire_id":           item.QuestionnaireSourceID,
			"questionnaire_title":        item.QuestionnaireTitle,
			"final_tags":                 item.FinalTags,
			"assessment_result_snapshot": item.AssessmentResult,
			"answers":                    answers,
		})
	}
	nextCursor := ""
	if page.Offset+int64(len(items)) < page.Total {
		nextCursor = encodeExternalSurveyCursor(page.Offset + int64(len(items)))
	}
	return responseOK(map[string]any{
		"ok":                true,
		"items":             items,
		"total":             page.Total,
		"limit":             page.Limit,
		"next_cursor":       nextCursor,
		"has_more":          nextCursor != "",
		"filters":           filters,
		"route_owner":       "ai_crm_next",
		"source_status":     "external_questionnaire_submissions",
		"read_model_status": "primary",
		"fallback_used":     false,
	})
}

func legacyExternalTimestamp(value time.Time) string {
	value = value.UTC()
	if value.Nanosecond() == 0 {
		return value.Format("2006-01-02T15:04:05-07:00")
	}
	return value.Format("2006-01-02T15:04:05.999999-07:00")
}

func externalSurveyError(status int, code string) openplatformport.Response {
	return openplatformport.Response{Status: status, Body: map[string]any{
		"ok": false, "error_code": code, "route_owner": "ai_crm_next", "source_status": "external_questionnaire_submissions", "fallback_used": false,
	}}
}

func externalSurveyIdentityError(err error) openplatformport.Response {
	response := responseForIdentityError(err)
	code, _ := response.Body.(map[string]any)["error_code"].(string)
	return externalSurveyError(response.Status, code)
}

func externalSurveyUnavailable() openplatformport.Response {
	return openplatformport.Response{Status: 503, Body: map[string]any{
		"ok": false, "error_code": "production_unavailable", "route_owner": "ai_crm_next", "source_status": "production_unavailable", "fallback_used": false,
	}}
}

func (executor *openPlatformExecutor) listExternalRadarLinks(ctx context.Context, values url.Values, principal accessdomain.MachinePrincipal) (openplatformport.Response, error) {
	query, filters, err := externalRadarLinksQuery(values)
	if err != nil {
		return externalRadarLinksError(400, "invalid_request"), nil
	}
	if !executor.allowsUnboundScope(principal) {
		return externalRadarLinksError(404, "not_found"), nil
	}
	if executor.radarLinks == nil {
		return externalRadarLinksUnavailable(), nil
	}
	page, err := executor.radarLinks.ExternalLinkMappings(ctx, query)
	if err != nil {
		return externalRadarLinksUnavailable(), nil
	}
	items := make([]map[string]any, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, map[string]any{
			"radar_id":   item.RadarID,
			"radar_code": item.RadarCode,
			"title":      item.Title,
		})
	}
	nextCursor := ""
	if page.HasMore && len(page.Items) > 0 {
		nextCursor = encodeExternalKeysetCursor("radar_id", int64(page.Items[len(page.Items)-1].RadarID))
	}
	return responseOK(map[string]any{
		"ok":            true,
		"items":         items,
		"total":         page.Total,
		"limit":         query.Limit,
		"next_cursor":   nextCursor,
		"has_more":      nextCursor != "",
		"filters":       filters,
		"route_owner":   "ai_crm_next",
		"source_status": "external_radar_links",
		"fallback_used": false,
	}), nil
}

// externalRadarLinksQuery follows the donor scalar and cursor rules: every
// supplied filter is exact and ANDed, unknown query values are ignored, and
// the opaque keyset token admits only the single expected field.
func externalRadarLinksQuery(values url.Values) (radarport.ExternalLinkMappingQuery, map[string]any, error) {
	one := func(key string) string {
		items := values[key]
		if len(items) == 0 {
			return ""
		}
		return strings.TrimSpace(items[len(items)-1])
	}
	parseOptionalPositive := func(value string) (int64, error) {
		if value == "" {
			return 0, nil
		}
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsed < 1 {
			return 0, errors.New("positive integer required")
		}
		return parsed, nil
	}
	radarRaw := one("radar_id")
	radarID, err := parseOptionalPositive(radarRaw)
	if err != nil {
		return radarport.ExternalLinkMappingQuery{}, nil, err
	}
	limitRaw := one("limit")
	limit := int64(100)
	if limitRaw != "" {
		limit, err = parseOptionalPositive(limitRaw)
		if err != nil || limit > 500 {
			return radarport.ExternalLinkMappingQuery{}, nil, errors.New("invalid limit")
		}
	}
	before, err := decodeExternalKeysetCursor(one("cursor"), "radar_id")
	if err != nil {
		return radarport.ExternalLinkMappingQuery{}, nil, err
	}
	radarCode := one("radar_code")
	filters := map[string]any{}
	if radarRaw != "" {
		filters["radar_id"] = radarID
	}
	if radarCode != "" {
		filters["radar_code"] = radarCode
	}
	return radarport.ExternalLinkMappingQuery{RadarID: radarport.RadarID(radarID), RadarCode: radarCode, BeforeRadarID: radarport.RadarID(before), Limit: int32(limit)}, filters, nil
}

func encodeExternalKeysetCursor(key string, value int64) string {
	payload, err := json.Marshal(map[string]int64{key: value})
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(payload)
}

func decodeExternalKeysetCursor(cursor, key string) (int64, error) {
	if cursor == "" {
		return 0, nil
	}
	padded := cursor + strings.Repeat("=", (4-len(cursor)%4)%4)
	raw, err := base64.URLEncoding.DecodeString(padded)
	if err != nil {
		return 0, errors.New("invalid cursor")
	}
	var payload map[string]json.RawMessage
	if err = json.Unmarshal(raw, &payload); err != nil || len(payload) != 1 {
		return 0, errors.New("invalid cursor")
	}
	encoded, ok := payload[key]
	if !ok {
		return 0, errors.New("invalid cursor")
	}
	var value int64
	if err = json.Unmarshal(encoded, &value); err != nil || value < 1 {
		return 0, errors.New("invalid cursor")
	}
	return value, nil
}

func externalRadarLinksError(status int, code string) openplatformport.Response {
	return openplatformport.Response{Status: status, Body: map[string]any{
		"ok": false, "error_code": code, "route_owner": "ai_crm_next", "source_status": "external_radar_links", "fallback_used": false,
	}}
}

func externalRadarLinksUnavailable() openplatformport.Response {
	return openplatformport.Response{Status: 503, Body: map[string]any{
		"ok": false, "error_code": "production_unavailable", "route_owner": "ai_crm_next", "source_status": "production_unavailable", "fallback_used": false,
	}}
}

func (executor *openPlatformExecutor) listOrders(ctx context.Context, values url.Values, principal accessdomain.MachinePrincipal) (openplatformport.Response, error) {
	query, references, err := executor.orderQuery(ctx, values)
	if err != nil {
		return responseError(400, "invalid_request"), nil
	}
	if query.CustomerID > 0 {
		if err := executor.ensureCustomerScope(ctx, principal, customerdomain.CustomerID(query.CustomerID), references); err != nil {
			return responseError(404, "not_found"), nil
		}
	} else if !executor.allowsUnboundScope(principal) {
		return responseError(404, "not_found"), nil
	}
	page, err := executor.orders.List(ctx, query)
	if err != nil {
		return responseForOrderError(err), nil
	}
	items := make([]map[string]any, 0, len(page.Items))
	for _, order := range page.Items {
		items = append(items, publicOrder(order))
	}
	return responseOK(map[string]any{
		"ok":            true,
		"items":         items,
		"total":         page.Total,
		"limit":         query.Limit,
		"next_cursor":   page.NextCursor,
		"has_more":      page.NextCursor != "",
		"filters":       externalOrderFilters(values),
		"providers":     externalOrderProviders(query.Provider),
		"route_owner":   "ai_crm_next",
		"source_status": "external_orders",
		"fallback_used": false,
	}), nil
}

func (executor *openPlatformExecutor) getOrder(ctx context.Context, reference string, principal accessdomain.MachinePrincipal) (openplatformport.Response, error) {
	if strings.TrimSpace(reference) == "" {
		return responseError(400, "invalid_request"), nil
	}
	if customerID, scoped := scopedOrderCustomerID(principal); scoped {
		order, err := executor.scopedOrders.GetByReferenceForCustomer(ctx, reference, customerID)
		if err != nil {
			return responseForOrderError(err), nil
		}
		return externalOrderDetailResponse(order), nil
	}
	if !executor.allowsUnboundScope(principal) {
		// Owner scopes and multi-customer constraints do not have an Order
		// resource predicate for detail lookup. Refuse them rather than read
		// broadly and inspect the returned order in this adapter.
		return responseError(404, "not_found"), nil
	}
	order, err := executor.orders.GetByReference(ctx, reference)
	if err != nil {
		return responseForOrderError(err), nil
	}
	return externalOrderDetailResponse(order), nil
}

func externalOrderDetailResponse(order orderdomain.Snapshot) openplatformport.Response {
	return responseOK(map[string]any{"ok": true, "order": publicOrder(order), "route_owner": "ai_crm_next", "source_status": "external_order_detail", "fallback_used": false})
}

// scopedOrderCustomerID accepts only the owner-scope shape that the Order
// Port can enforce atomically: one concrete customer plus an optional matching
// corporation. Other constraints must not fall back to an unrestricted detail
// query.
func scopedOrderCustomerID(principal accessdomain.MachinePrincipal) (int64, bool) {
	values, exists := principal.OwnerScope["customer_id"]
	if !exists || len(values) != 1 {
		return 0, false
	}
	customerID, err := strconv.ParseInt(values[0], 10, 64)
	if err != nil || customerID < 1 {
		return 0, false
	}
	if !principal.OwnerScope.Allows(map[string]string{"customer_id": strconv.FormatInt(customerID, 10), "corp_id": principal.CorpID}) {
		return 0, false
	}
	return customerID, true
}

func (executor *openPlatformExecutor) callMCP(ctx context.Context, body []byte, principal accessdomain.MachinePrincipal) (openplatformport.Response, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var call struct {
		Method string `json:"method"`
		Params struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		} `json:"params"`
	}
	if err := decoder.Decode(&call); err != nil || call.Method != "tools/call" || strings.TrimSpace(call.Params.Name) == "" {
		return openplatformport.Response{}, errors.New("invalid MCP tool request")
	}
	if call.Params.Arguments == nil {
		call.Params.Arguments = map[string]any{}
	}
	customerID, references, err := executor.mcpCustomerID(ctx, call.Params.Arguments)
	if err != nil {
		return openplatformport.Response{}, err
	}
	if err := executor.ensureCustomerScope(ctx, principal, customerID, references); err != nil {
		return openplatformport.Response{}, errOpenPlatformResourceOutOfScope
	}
	profile, err := executor.profiles.ReadSidebarProfile(ctx, customerID)
	if err != nil {
		return openplatformport.Response{}, err
	}
	var content map[string]any
	switch call.Params.Name {
	case "resolve_customer":
		content = map[string]any{"customer_id": customerID, "customer": profile}
		if include, valid := optionalBool(call.Params.Arguments, "include_context"); !valid {
			return openplatformport.Response{}, errors.New("invalid include_context")
		} else if include {
			contextValue, contextErr := executor.customerContext(ctx, customerID, limitArgument(call.Params.Arguments, "recent_message_limit", 20), limitArgument(call.Params.Arguments, "timeline_limit", 20))
			if contextErr != nil {
				return openplatformport.Response{}, contextErr
			}
			content["context"] = contextValue
		}
	case "get_customer_context":
		contextValue, contextErr := executor.customerContext(ctx, customerID, limitArgument(call.Params.Arguments, "recent_message_limit", 20), limitArgument(call.Params.Arguments, "timeline_limit", 20))
		if contextErr != nil {
			return openplatformport.Response{}, contextErr
		}
		content = map[string]any{"customer_id": customerID, "customer": profile, "context": contextValue}
	case "get_recent_messages":
		messages, messageErr := executor.recentMessages(ctx, customerID, limitArgument(call.Params.Arguments, "limit", 20))
		if errors.Is(messageErr, archiveport.ErrNotReady) {
			content = map[string]any{"customer_id": customerID, "messages": []any{}, "archive_status": "not_ready"}
			break
		}
		if messageErr != nil {
			return openplatformport.Response{}, messageErr
		}
		content = map[string]any{"customer_id": customerID, "messages": messages}
	default:
		return openplatformport.Response{}, errors.New("unknown MCP tool")
	}
	return responseOK(map[string]any{"content": []map[string]any{{"type": "json", "json": content}}, "structuredContent": content}), nil
}

func (executor *openPlatformExecutor) customerContext(ctx context.Context, customerID customerdomain.CustomerID, messageLimit, timelineLimit int) (map[string]any, error) {
	if timelineLimit < 1 || timelineLimit > 100 {
		return nil, errors.New("invalid timeline limit")
	}
	timeline, timelineErr := executor.timeline.CustomerTimeline(ctx, customerID, customerport.PageQuery{Limit: timelineLimit, Watermark: time.Now().UTC()})
	if timelineErr != nil {
		return nil, timelineErr
	}
	messages, messageErr := executor.recentMessages(ctx, customerID, messageLimit)
	if errors.Is(messageErr, archiveport.ErrNotReady) {
		return map[string]any{"messages": []any{}, "archive_status": "not_ready", "timeline": timeline.Items, "timeline_status": timeline.Status.State}, nil
	}
	if messageErr != nil {
		return nil, messageErr
	}
	return map[string]any{"messages": messages, "timeline": timeline.Items, "timeline_status": timeline.Status.State}, nil
}

func (executor *openPlatformExecutor) recentMessages(ctx context.Context, customerID customerdomain.CustomerID, limit int) ([]archiveport.MessageItem, error) {
	if limit < 1 || limit > 100 {
		return nil, errors.New("invalid message limit")
	}
	page, err := executor.archive.CustomerMessages(ctx, archiveport.CustomerQuery{CustomerID: customerID, Limit: limit})
	if err != nil {
		return nil, err
	}
	return page.Items, nil
}

func (executor *openPlatformExecutor) mcpCustomerID(ctx context.Context, arguments map[string]any) (customerdomain.CustomerID, []identitydomain.Reference, error) {
	var directCustomer customerdomain.CustomerID
	references := make([]identitydomain.Reference, 0, 2)
	if raw, exists := arguments["customer_ref"]; exists {
		value, ok := raw.(string)
		if !ok || strings.TrimSpace(value) == "" {
			return 0, nil, errors.New("invalid customer_ref")
		}
		value = strings.TrimSpace(value)
		if strings.HasPrefix(value, "customer:") {
			id, err := strconv.ParseInt(strings.TrimPrefix(value, "customer:"), 10, 64)
			if err != nil || id < 1 {
				return 0, nil, errors.New("invalid customer_ref")
			}
			directCustomer = customerdomain.CustomerID(id)
		} else if isCN11(value) {
			references = append(references, identitydomain.Reference{Kind: identitydomain.KindPhone, Scope: "phone:cn11", Value: value, Assurance: identitydomain.AssuranceDeclared, Source: "open_platform.mcp"})
		} else {
			reference, referenceErr := executor.trustedReference(identitydomain.KindWeComExternalUserID, "", value, "open_platform.mcp")
			if referenceErr != nil {
				return 0, nil, referenceErr
			}
			references = append(references, reference)
		}
	}
	if raw, exists := arguments["external_userid"]; exists {
		value, ok := raw.(string)
		if !ok || strings.TrimSpace(value) == "" {
			return 0, nil, errors.New("invalid external_userid")
		}
		reference, referenceErr := executor.trustedReference(identitydomain.KindWeComExternalUserID, "", strings.TrimSpace(value), "open_platform.mcp")
		if referenceErr != nil {
			return 0, nil, referenceErr
		}
		references = append(references, reference)
	}
	if directCustomer == 0 && len(references) == 0 {
		return 0, nil, errors.New("customer_ref or external_userid is required")
	}
	if len(references) == 0 {
		return directCustomer, nil, nil
	}
	resolved, err := executor.resolveReferences(ctx, references)
	if err != nil {
		return 0, nil, err
	}
	if directCustomer != 0 && resolved.CustomerID != directCustomer {
		return 0, nil, errOpenPlatformIdentityConflict
	}
	return resolved.CustomerID, references, nil
}

func (executor *openPlatformExecutor) referencesFromValues(values url.Values) ([]identitydomain.Reference, error) {
	getOne := func(key string) (string, error) {
		items, found := values[key]
		if !found {
			return "", nil
		}
		if len(items) != 1 {
			return "", errors.New("duplicate identity parameter")
		}
		return strings.TrimSpace(items[0]), nil
	}
	kind, err := getOne("kind")
	if err != nil {
		return nil, err
	}
	if kind != "" {
		for _, key := range []string{"external_userid", "mobile", "unionid", "openid"} {
			value, valueErr := getOne(key)
			if valueErr != nil || value != "" {
				return nil, errors.New("generic identity cannot mix aliases")
			}
		}
		scope, scopeErr := getOne("scope")
		value, valueErr := getOne("value")
		if scopeErr != nil || valueErr != nil {
			return nil, errors.New("invalid generic identity")
		}
		reference, err := executor.trustedReference(identitydomain.Kind(kind), scope, value, "open_platform.api")
		if err != nil {
			return nil, err
		}
		return []identitydomain.Reference{reference}, nil
	}
	references := make([]identitydomain.Reference, 0, 4)
	if value, valueErr := getOne("external_userid"); valueErr != nil {
		return nil, valueErr
	} else if value != "" {
		scope, scopeErr := getOne("external_userid_scope")
		if scopeErr != nil {
			return nil, scopeErr
		}
		reference, err := executor.trustedReference(identitydomain.KindWeComExternalUserID, scope, value, "open_platform.api")
		if err != nil {
			return nil, err
		}
		references = append(references, reference)
	}
	if value, valueErr := getOne("mobile"); valueErr != nil {
		return nil, valueErr
	} else if value != "" {
		reference, err := executor.trustedReference(identitydomain.KindPhone, "", value, "open_platform.api")
		if err != nil {
			return nil, err
		}
		references = append(references, reference)
	}
	sharedScope, scopeErr := getOne("scope")
	if scopeErr != nil {
		return nil, scopeErr
	}
	if value, valueErr := getOne("unionid"); valueErr != nil {
		return nil, valueErr
	} else if value != "" {
		reference, err := executor.trustedReference(identitydomain.KindUnionID, sharedScope, value, "open_platform.api")
		if err != nil {
			return nil, err
		}
		references = append(references, reference)
	}
	if value, valueErr := getOne("openid"); valueErr != nil {
		return nil, valueErr
	} else if value != "" {
		reference, err := executor.trustedReference(identitydomain.KindOAOpenID, sharedScope, value, "open_platform.api")
		if err != nil {
			return nil, err
		}
		references = append(references, reference)
	}
	if len(references) == 0 {
		return nil, errors.New("identity query missing")
	}
	return references, nil
}

func (executor *openPlatformExecutor) trustedReference(kind identitydomain.Kind, requestedScope, value, source string) (identitydomain.Reference, error) {
	requestedScope = strings.TrimSpace(requestedScope)
	scope := requestedScope
	switch kind {
	case identitydomain.KindWeComExternalUserID:
		if executor.scopes.WeComScope == "" {
			return identitydomain.Reference{}, errOpenPlatformIdentityScopeDenied
		}
		if scope == "" {
			scope = executor.scopes.WeComScope
		}
		if scope != executor.scopes.WeComScope {
			return identitydomain.Reference{}, errOpenPlatformIdentityScopeDenied
		}
	case identitydomain.KindPhone:
		if scope != "" && scope != "phone:cn11" {
			return identitydomain.Reference{}, errOpenPlatformIdentityScopeDenied
		}
		scope = "phone:cn11"
	case identitydomain.KindUnionID:
		scope, value := selectTrustedScope(executor.scopes.UnionScopes, scope, value)
		if value == "" {
			return identitydomain.Reference{}, errOpenPlatformIdentityScopeDenied
		}
		return identitydomain.Reference{Kind: kind, Scope: scope, Value: value, Assurance: identitydomain.AssuranceDeclared, Source: source}, nil
	case identitydomain.KindOAOpenID:
		scope, value := selectTrustedScope(executor.scopes.OpenIDScopes, scope, value)
		if value == "" {
			return identitydomain.Reference{}, errOpenPlatformIdentityScopeDenied
		}
		return identitydomain.Reference{Kind: kind, Scope: scope, Value: value, Assurance: identitydomain.AssuranceDeclared, Source: source}, nil
	}
	reference := identitydomain.Reference{Kind: kind, Scope: scope, Value: value, Assurance: identitydomain.AssuranceDeclared, Source: source}
	if _, err := identitydomain.Normalize(reference); err != nil {
		return identitydomain.Reference{}, err
	}
	return reference, nil
}

// selectTrustedScope preserves old callers that omitted an internal scope only
// when Composition has one unambiguous configured candidate. A supplied scope
// still has to be one of those configured values.
func selectTrustedScope(available []string, requested, value string) (string, string) {
	if requested != "" {
		for _, candidate := range available {
			if candidate == requested {
				return candidate, value
			}
		}
		return "", ""
	}
	if len(available) == 1 {
		return available[0], value
	}
	return "", ""
}

func (executor *openPlatformExecutor) resolveReferences(ctx context.Context, references []identitydomain.Reference) (identityport.ResolveResult, error) {
	var found identityport.ResolveResult
	for index, reference := range references {
		result, err := executor.identity.Resolve(ctx, reference)
		if err != nil {
			return identityport.ResolveResult{}, err
		}
		switch result.Status {
		case identityport.ResolveFound:
			if result.CustomerID < 1 {
				return identityport.ResolveResult{}, errOpenPlatformIdentityPending
			}
		case identityport.ResolveNotFound:
			return identityport.ResolveResult{}, errOpenPlatformIdentityNotFound
		case identityport.ResolveConflict:
			return identityport.ResolveResult{}, errOpenPlatformIdentityConflict
		default:
			return identityport.ResolveResult{}, errOpenPlatformIdentityPending
		}
		if index == 0 {
			found = result
			continue
		}
		if result.CustomerID != found.CustomerID {
			return identityport.ResolveResult{}, errOpenPlatformIdentityConflict
		}
	}
	return found, nil
}

func (executor *openPlatformExecutor) orderQuery(ctx context.Context, values url.Values) (orderport.ListQuery, []identitydomain.Reference, error) {
	for key, value := range values {
		if len(value) != 1 {
			return orderport.ListQuery{}, nil, errors.New("duplicate query value")
		}
		switch key {
		case "provider", "limit", "cursor", "order_no", "transaction_id", "product_code", "payment_status", "created_from", "created_to", "external_userid", "external_userid_scope", "mobile", "unionid", "openid", "kind", "value", "scope":
		default:
			return orderport.ListQuery{}, nil, errors.New("unsupported query parameter")
		}
	}
	query := orderport.ListQuery{Cursor: values.Get("cursor"), Limit: 50, OrderRef: firstNonEmpty(values.Get("order_no"), values.Get("transaction_id")), Product: values.Get("product_code")}
	if value := values.Get("limit"); value != "" {
		limit, err := strconv.ParseInt(value, 10, 32)
		if err != nil || limit < 1 || limit > 100 {
			return query, nil, errors.New("invalid limit")
		}
		query.Limit = int32(limit)
	}
	if provider := values.Get("provider"); provider != "" && provider != "all" {
		switch provider {
		case "wechat", "wechat_pay":
			query.Provider = orderdomain.ProviderWeChatPay
		case "wechat_shop":
			query.Provider = orderdomain.ProviderWeChatShop
		case "alipay":
			query.Provider = orderdomain.ProviderAlipay
		default:
			return query, nil, errors.New("invalid provider")
		}
	}
	if status := values.Get("payment_status"); status != "" {
		if status == "unpaid" {
			status = string(orderdomain.StatusPendingPayment)
		}
		if status == "refunding" || status == "refund_processing" {
			status = string(orderdomain.StatusPartiallyRefunded)
		}
		query.Status = orderdomain.Status(status)
	}
	var err error
	if query.CreatedFrom, err = unixSeconds(values.Get("created_from")); err != nil {
		return query, nil, err
	}
	if query.CreatedTo, err = unixSeconds(values.Get("created_to")); err != nil {
		return query, nil, err
	}
	if anyIdentityValue(values) {
		references, referenceErr := executor.referencesFromValues(values)
		if referenceErr != nil {
			return query, nil, referenceErr
		}
		resolved, resolveErr := executor.resolveReferences(ctx, references)
		if resolveErr != nil {
			return query, nil, resolveErr
		}
		query.CustomerID = int64(resolved.CustomerID)
		return query, references, nil
	}
	return query, nil, nil
}

func publicOrder(order orderdomain.Snapshot) map[string]any {
	productCode := ""
	if len(order.Items) > 0 {
		productCode = order.Items[0].ProductCode
	}
	provider := externalOrderProvider(order.Provider)
	status := externalOrderStatus(order.Status)
	refundStatus := ""
	if order.RefundedMinor > 0 {
		refundStatus = status
	}
	return map[string]any{
		"provider":              provider,
		"order_no":              order.MerchantOrderNo,
		"transaction_id":        order.ProviderTransactionNo,
		"paid_at":               "",
		"created_at":            order.CreatedAt,
		"product_code":          productCode,
		"payment_status":        status,
		"status_label":          status,
		"amount_total":          order.Amount.AmountMinor,
		"amount_yuan":           fmt.Sprintf("%d.%02d", order.Amount.AmountMinor/100, order.Amount.AmountMinor%100),
		"currency":              order.Amount.Currency,
		"is_paid":               order.Status == orderdomain.StatusPaid || order.Status == orderdomain.StatusPartiallyRefunded || order.Status == orderdomain.StatusRefunded,
		"is_refunded":           order.RefundedMinor > 0,
		"refund_status":         refundStatus,
		"refunded_amount_total": order.RefundedMinor,
		"mobile":                "",
		"unionid":               "",
		"external_userid":       "",
		"detail_url":            "/api/external/orders/" + url.PathEscape(order.MerchantOrderNo) + "?provider=" + url.QueryEscape(provider),
	}
}

func externalOrderProvider(provider orderdomain.Provider) string {
	if provider == orderdomain.ProviderWeChatPay {
		return "wechat"
	}
	return string(provider)
}

func externalOrderStatus(status orderdomain.Status) string {
	switch status {
	case orderdomain.StatusPendingPayment:
		return "unpaid"
	case orderdomain.StatusPartiallyRefunded:
		return "partial_refunded"
	case orderdomain.StatusRefunded:
		return "full_refunded"
	default:
		return string(status)
	}
}

func externalOrderFilters(values url.Values) map[string]string {
	filters := map[string]string{}
	for _, key := range []string{"payment_status", "product_code", "mobile", "external_userid", "unionid", "transaction_id", "order_no", "created_from", "created_to", "paid_from", "paid_to", "is_paid", "is_refunded"} {
		filters[key] = values.Get(key)
	}
	return filters
}

func externalOrderProviders(provider orderdomain.Provider) []string {
	if provider == "" {
		return []string{"wechat", "alipay", "wechat_shop"}
	}
	return []string{externalOrderProvider(provider)}
}

func responseOK(body any) openplatformport.Response {
	return openplatformport.Response{Status: 200, Body: body}
}
func responseError(status int, code string) openplatformport.Response {
	return openplatformport.Response{Status: status, Body: map[string]any{"ok": false, "error_code": code}}
}
func responseForIdentityError(err error) openplatformport.Response {
	switch {
	case errors.Is(err, errOpenPlatformIdentityNotFound):
		return responseError(404, "not_found")
	case errors.Is(err, errOpenPlatformIdentityConflict):
		return responseError(409, "identity_conflict")
	case errors.Is(err, errOpenPlatformIdentityPending), errors.Is(err, errOpenPlatformIdentityScopeDenied):
		return responseError(409, "identity_pending")
	default:
		return responseError(400, "invalid_request")
	}
}
func responseForOrderError(err error) openplatformport.Response {
	if errors.Is(err, orderport.ErrNotFound) {
		return responseError(404, "not_found")
	}
	if errors.Is(err, orderport.ErrConflict) {
		return responseError(409, "conflict")
	}
	return responseError(503, "order_unavailable")
}
func optionalBool(values map[string]any, key string) (bool, bool) {
	value, ok := values[key]
	if !ok {
		return false, true
	}
	result, ok := value.(bool)
	return result, ok
}
func limitArgument(values map[string]any, key string, fallback int) int {
	value, ok := values[key]
	if !ok {
		return fallback
	}
	number, ok := value.(json.Number)
	if !ok {
		return 0
	}
	result, err := number.Int64()
	if err != nil || result < 1 || result > 100 {
		return 0
	}
	return int(result)
}
func anyIdentityValue(values url.Values) bool {
	return values.Get("kind") != "" || values.Get("external_userid") != "" || values.Get("mobile") != "" || values.Get("unionid") != "" || values.Get("openid") != ""
}
func isCN11(value string) bool {
	if len(value) != 11 || value[0] != '1' {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}
func unixSeconds(value string) (*time.Time, error) {
	if value == "" {
		return nil, nil
	}
	seconds, err := strconv.ParseInt(value, 10, 64)
	if err != nil || seconds < 0 || seconds > 9_999_999_999 {
		return nil, errors.New("invalid unix seconds")
	}
	parsed := time.Unix(seconds, 0).UTC()
	return &parsed, nil
}
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

var _ openplatformport.Executor = (*openPlatformExecutor)(nil)

// openPlatformOwnerAdapter gives the read-only WeCom owner fact the same UoW
// boundary as every other composed customer projection.
type openPlatformOwnerAdapter struct {
	uow    platformport.UnitOfWork
	reader wecomport.AudiencePrimaryOwnerReader
}

func (adapter openPlatformOwnerAdapter) AudiencePrimaryOwners(ctx context.Context, customerIDs []customerdomain.CustomerID) ([]wecomport.AudiencePrimaryOwner, error) {
	var owners []wecomport.AudiencePrimaryOwner
	err := adapter.uow.Within(ctx, func(tx context.Context) error {
		var err error
		owners, err = adapter.reader.AudiencePrimaryOwners(tx, customerIDs)
		return err
	})
	return owners, err
}

var _ wecomport.AudiencePrimaryOwnerReader = openPlatformOwnerAdapter{}

type openPlatformCustomerBusinessDetailAdapter struct {
	uow    platformport.UnitOfWork
	reader wecomport.CustomerBusinessDetailReader
}

func (adapter openPlatformCustomerBusinessDetailAdapter) CustomerBusinessDetails(ctx context.Context, customerIDs []customerdomain.CustomerID) ([]wecomport.CustomerBusinessDetail, error) {
	var details []wecomport.CustomerBusinessDetail
	err := adapter.uow.Within(ctx, func(tx context.Context) error {
		var err error
		details, err = adapter.reader.CustomerBusinessDetails(tx, customerIDs)
		return err
	})
	return details, err
}

var _ wecomport.CustomerBusinessDetailReader = openPlatformCustomerBusinessDetailAdapter{}

// openPlatformIdentityAdapter combines stable Identity Ports at Composition.
// The machine executor receives no store and cannot issue a cross-domain query.
type openPlatformMachineAuditWriter interface {
	AppendMachineAudit(context.Context, accessdomain.MachineAudit) error
}

type openPlatformIdentityAdapter struct {
	resolver     identityport.Resolver
	values       identityport.ExternalIdentityValueReader
	directory    identityport.DirectoryIdentityReader
	machineFacts identityport.MachineIdentityFactReader
	machineAudit openPlatformMachineAuditWriter
	uow          platformport.UnitOfWork
}

func (adapter openPlatformIdentityAdapter) MachineIdentityFacts(ctx context.Context, customerID customerdomain.CustomerID) ([]identityport.MachineIdentityFact, error) {
	if adapter.machineFacts == nil || adapter.uow == nil {
		return nil, errors.New("machine identity facts unavailable")
	}
	var facts []identityport.MachineIdentityFact
	err := adapter.uow.Within(ctx, func(tx context.Context) error {
		var e error
		facts, e = adapter.machineFacts.MachineIdentityFacts(tx, customerID)
		return e
	})
	return facts, err
}
func (adapter openPlatformIdentityAdapter) MachineIdentityFactsForMachine(ctx context.Context, customerID customerdomain.CustomerID, principal accessdomain.MachinePrincipal) ([]identityport.MachineIdentityFact, error) {
	export, err := adapter.MachineIdentityExportForMachine(ctx, customerID, principal)
	if err != nil {
		return nil, err
	}
	return export.Facts, nil
}
func (adapter openPlatformIdentityAdapter) MachineIdentityExportForMachine(ctx context.Context, customerID customerdomain.CustomerID, principal accessdomain.MachinePrincipal) (identityport.MachineIdentityExport, error) {
	if principal.ClientRecord < 1 || adapter.machineAudit == nil {
		return identityport.MachineIdentityExport{}, errors.New("machine identity audit unavailable")
	}
	exporter, ok := adapter.machineFacts.(identityport.MachineIdentityExportReader)
	if !ok || exporter == nil {
		return identityport.MachineIdentityExport{}, errors.New("machine identity export unavailable")
	}
	var export identityport.MachineIdentityExport
	err := adapter.uow.Within(ctx, func(tx context.Context) error {
		var e error
		export, e = exporter.MachineIdentityExport(tx, customerID)
		if e != nil {
			return e
		}
		return adapter.machineAudit.AppendMachineAudit(tx, accessdomain.MachineAudit{MachineClientID: principal.ClientRecord, Action: "machine_sensitive_read", Outcome: "open_platform_identity_read", Details: []byte(`{"field":"identity_facts"}`), CreatedAt: time.Now().UTC()})
	})
	return export, err
}

func (adapter openPlatformIdentityAdapter) Resolve(ctx context.Context, reference identitydomain.Reference) (identityport.ResolveResult, error) {
	if adapter.resolver == nil || adapter.uow == nil {
		return identityport.ResolveResult{}, errors.New("open platform identity resolver is unavailable")
	}
	var result identityport.ResolveResult
	err := adapter.uow.Within(ctx, func(tx context.Context) error {
		var resolveErr error
		result, resolveErr = adapter.resolver.Resolve(tx, reference)
		return resolveErr
	})
	return result, err
}

func (adapter openPlatformIdentityAdapter) VerifiedExternalUserID(ctx context.Context, customerID customerdomain.CustomerID, scope string) (string, bool, error) {
	return adapter.verifiedExternalIdentity(ctx, customerID, identitydomain.KindWeComExternalUserID, scope)
}

func (adapter openPlatformIdentityAdapter) VerifiedUnionID(ctx context.Context, customerID customerdomain.CustomerID, scope string) (string, bool, error) {
	return adapter.verifiedExternalIdentity(ctx, customerID, identitydomain.KindUnionID, scope)
}

func (adapter openPlatformIdentityAdapter) verifiedExternalIdentity(ctx context.Context, customerID customerdomain.CustomerID, kind identitydomain.Kind, scope string) (string, bool, error) {
	if adapter.values == nil || adapter.uow == nil || customerID < 1 || strings.TrimSpace(scope) != scope || scope == "" {
		return "", false, errors.New("open platform verified external identity is unavailable")
	}
	var value string
	var found bool
	err := adapter.uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		value, found, readErr = adapter.values.VerifiedExternalIdentityValue(tx, customerID, kind, scope)
		return readErr
	})
	return value, found, err
}

// RevealPhoneForMachine follows Identity's DirectoryIdentityReader boundary:
// raw phone leaves Identity only for an authenticated compatibility response,
// and the existing Access machine-audit record is written in the same UoW
// without placing the phone itself in audit data.
func (adapter openPlatformIdentityAdapter) RevealPhoneForMachine(ctx context.Context, customerID customerdomain.CustomerID, principal accessdomain.MachinePrincipal) (string, bool, error) {
	if adapter.directory == nil || adapter.machineAudit == nil || adapter.uow == nil || customerID < 1 || principal.ClientRecord < 1 || principal.ClientID == "" {
		return "", false, errors.New("open platform phone reader is unavailable")
	}
	var phone string
	var found bool
	err := adapter.uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		phone, found, readErr = adapter.directory.RevealPhone(tx, customerID)
		if readErr != nil || !found {
			return readErr
		}
		return adapter.machineAudit.AppendMachineAudit(tx, accessdomain.MachineAudit{
			MachineClientID: principal.ClientRecord,
			Action:          "machine_sensitive_read",
			Outcome:         "open_platform_identity_read",
			Details:         []byte(`{"field":"phone"}`),
			CreatedAt:       time.Now().UTC(),
		})
	})
	return phone, found, err
}

var _ identityport.Resolver = openPlatformIdentityAdapter{}
var _ openPlatformExternalUserIDReader = openPlatformIdentityAdapter{}
var _ openPlatformSurveyIdentityReader = openPlatformIdentityAdapter{}
var _ identityport.MachineIdentityFactReader = openPlatformIdentityAdapter{}
