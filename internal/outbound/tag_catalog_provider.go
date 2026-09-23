package outbound

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	effect "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
)

// CatalogReader is read-only by construction: it exposes no customer target,
// mark, unmark, or mutation operation.
type CatalogReader interface {
	ListCatalog(context.Context) (CatalogSnapshot, error)
}

// ReadError tells the kernel whether a network request crossed the Provider
// boundary. It deliberately carries no provider code or response body.
type ReadError struct {
	Err           error
	CallAttempted bool
}

func (e *ReadError) Error() string { return e.Err.Error() }
func (e *ReadError) Unwrap() error { return e.Err }

type CatalogSnapshot struct {
	Groups []CatalogGroup `json:"groups"`
}
type CatalogGroup struct {
	ID    string       `json:"id"`
	Name  string       `json:"name"`
	Order int32        `json:"order"`
	Tags  []CatalogTag `json:"tags"`
}
type CatalogTag struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Order   int32  `json:"order"`
	Deleted bool   `json:"deleted,omitempty"`
}
type TagCatalogProvider struct{ reader CatalogReader }

func NewTagCatalogProvider(reader CatalogReader) (*TagCatalogProvider, error) {
	if reader == nil {
		return nil, errors.New("catalog reader required")
	}
	return &TagCatalogProvider{reader: reader}, nil
}
func (p *TagCatalogProvider) Execute(ctx context.Context, envelope effect.Envelope, attempt effect.Attempt) (effect.AdapterResult, error) {
	if p == nil || p.reader == nil || envelope.Kind != effect.KindWeComTagCatalog {
		return effect.AdapterResult{Completion: effect.StateFinalFailed, ReceiptDigest: effect.Hash("wecom.tag.catalog.unsupported")}, nil
	}
	snapshot, err := p.reader.ListCatalog(ctx)
	if err != nil {
		var readErr *ReadError
		if errors.As(err, &readErr) && readErr.CallAttempted {
			return effect.AdapterResult{Completion: effect.StateUnknown, CallAttempted: true, RealExternalCallExecuted: true}, err
		}
		return effect.AdapterResult{Completion: effect.StateRetryable}, err
	}
	canonical, ok := CanonicalCatalogSnapshot(snapshot)
	if !ok {
		// The Provider call happened but its body cannot safely become a catalog
		// observation. Do not persist it and require reconciliation.
		return effect.AdapterResult{Completion: effect.StateUnknown, CallAttempted: true, RealExternalCallExecuted: true, ReceiptDigest: effect.Hash("wecom.tag.catalog.invalid")}, nil
	}
	payload, err := json.Marshal(canonical)
	if err != nil {
		return effect.AdapterResult{Completion: effect.StateUnknown, CallAttempted: true, RealExternalCallExecuted: true, ReceiptDigest: effect.Hash("wecom.tag.catalog.encode")}, nil
	}
	artifact := effect.ResultArtifact{Kind: "wecom.tag_catalog.snapshot.v1", Payload: payload}
	artifact.Digest = effect.Hash("external-effect.artifact.v1", artifact.Kind, string(payload))
	return effect.AdapterResult{Completion: effect.StateExecuted, ReceiptDigest: catalogReceipt("observed", envelope, attempt, artifact.Digest), CallAttempted: true, RealExternalCallExecuted: true, Artifact: artifact}, nil
}

// CanonicalCatalogSnapshot validates the donor catalog shape and eliminates
// deleted tags before it is ever persisted. Empty snapshots are valid: a
// tenant can have no catalog. IDs must still be unique even for deleted tags,
// matching the donor's defensive response validation.
func CanonicalCatalogSnapshot(snapshot CatalogSnapshot) (CatalogSnapshot, bool) {
	if snapshot.Groups == nil || len(snapshot.Groups) > 1000 {
		return CatalogSnapshot{}, false
	}
	groups := make(map[string]struct{}, len(snapshot.Groups))
	tags := map[string]struct{}{}
	tagCount := 0
	canonical := CatalogSnapshot{Groups: make([]CatalogGroup, 0, len(snapshot.Groups))}
	for _, group := range snapshot.Groups {
		if !validID(group.ID) || !validName(group.Name) {
			return CatalogSnapshot{}, false
		}
		if _, exists := groups[group.ID]; exists {
			return CatalogSnapshot{}, false
		}
		groups[group.ID] = struct{}{}
		clean := CatalogGroup{ID: group.ID, Name: group.Name, Order: group.Order, Tags: make([]CatalogTag, 0, len(group.Tags))}
		for _, tag := range group.Tags {
			tagCount++
			if tagCount > 10000 || !validID(tag.ID) || !validName(tag.Name) {
				return CatalogSnapshot{}, false
			}
			if _, exists := tags[tag.ID]; exists {
				return CatalogSnapshot{}, false
			}
			tags[tag.ID] = struct{}{}
			if !tag.Deleted {
				clean.Tags = append(clean.Tags, CatalogTag{ID: tag.ID, Name: tag.Name, Order: tag.Order})
			}
		}
		canonical.Groups = append(canonical.Groups, clean)
	}
	sort.SliceStable(canonical.Groups, func(i, j int) bool {
		if canonical.Groups[i].Order == canonical.Groups[j].Order {
			return canonical.Groups[i].ID < canonical.Groups[j].ID
		}
		return canonical.Groups[i].Order < canonical.Groups[j].Order
	})
	for i := range canonical.Groups {
		sort.SliceStable(canonical.Groups[i].Tags, func(a, b int) bool {
			if canonical.Groups[i].Tags[a].Order == canonical.Groups[i].Tags[b].Order {
				return canonical.Groups[i].Tags[a].ID < canonical.Groups[i].Tags[b].ID
			}
			return canonical.Groups[i].Tags[a].Order < canonical.Groups[i].Tags[b].Order
		})
	}
	return canonical, true
}

func validID(v string) bool   { return validRequiredText(v, 128) }
func validName(v string) bool { return validRequiredText(v, 256) }
func validRequiredText(value string, limit int) bool {
	if value == "" || value != strings.TrimSpace(value) || len(value) > limit || !utf8.ValidString(value) {
		return false
	}
	return !strings.ContainsFunc(value, unicode.IsControl)
}

func catalogReceipt(stage string, envelope effect.Envelope, attempt effect.Attempt, artifact effect.Digest) effect.Digest {
	return effect.Hash("wecom.tag.catalog."+stage, string(envelope.Fingerprint()), strconv.Itoa(int(attempt.Number)), strconv.FormatInt(attempt.Generation, 10), strconv.FormatInt(attempt.Fence, 10), string(artifact))
}

var _ effect.ProviderAdapter = (*TagCatalogProvider)(nil)

// ProviderRouter prevents a tag-catalog reader from accidentally handling a
// different outbound kind. Unsupported intents fail closed without a network
// call; their own future adapters can be added explicitly by composition.
type ProviderRouter struct {
	invitationCode     effect.ProviderAdapter
	sidebarMedia       effect.ProviderAdapter
	tagCatalog         effect.ProviderAdapter
	tagCatalogMutation effect.ProviderAdapter
	contactDescription effect.ProviderAdapter
	groupMessage       effect.ProviderAdapter
	channelAsset       effect.ProviderAdapter
	channelEntrant     effect.ProviderAdapter
	channelLink        effect.ProviderAdapter
	privateMessage     effect.ProviderAdapter
	automationMessage  effect.ProviderAdapter
	sidebarJSSDK       effect.ProviderAdapter
	surveyCompletion   effect.ProviderAdapter
	customerTag        effect.ProviderAdapter
	ownerHandoff       effect.ProviderAdapter
	commercePush       effect.ProviderAdapter
}

func (r *ProviderRouter) Preflight(ctx context.Context, envelope effect.Envelope, effectID string) (bool, time.Duration, error) {
	if r == nil {
		return true, 0, nil
	}
	var provider effect.ProviderAdapter
	switch envelope.Kind {
	case effect.KindOutboundMessage:
		provider = r.privateMessage
	case effect.KindAutomationMessage:
		provider = r.automationMessage
	case effect.KindGroupMessage:
		provider = r.groupMessage
	case effect.KindOutboundMedia:
		provider = r.sidebarMedia
	}
	if preflight, ok := provider.(effect.ProviderPreflighter); ok {
		return preflight.Preflight(ctx, envelope, effectID)
	}
	return true, 0, nil
}

func NewProviderRouterWithPrivate(tagCatalog, groupMessage, privateMessage effect.ProviderAdapter) *ProviderRouter {
	return &ProviderRouter{tagCatalog: tagCatalog, groupMessage: groupMessage, privateMessage: privateMessage}
}

// WithPrivateMessage extends the established tag/group router without
// bypassing the frozen Group Ops composition marker and behavior contract.
func (r *ProviderRouter) WithPrivateMessage(privateMessage effect.ProviderAdapter) *ProviderRouter {
	if r != nil {
		r.privateMessage = privateMessage
	}
	return r
}

func (r *ProviderRouter) WithAutomationMessage(message effect.ProviderAdapter) *ProviderRouter {
	if r != nil {
		r.automationMessage = message
	}
	return r
}

func (r *ProviderRouter) WithSidebarMedia(provider effect.ProviderAdapter) *ProviderRouter {
	if r != nil {
		r.sidebarMedia = provider
	}
	return r
}

func (r *ProviderRouter) WithSidebarJSSDK(provider effect.ProviderAdapter) *ProviderRouter {
	if r != nil {
		r.sidebarJSSDK = provider
	}
	return r
}

func (r *ProviderRouter) WithTagCatalogMutation(provider effect.ProviderAdapter) *ProviderRouter {
	if r != nil {
		r.tagCatalogMutation = provider
	}
	return r
}

func (r *ProviderRouter) WithContactDescription(provider effect.ProviderAdapter) *ProviderRouter {
	if r != nil {
		r.contactDescription = provider
	}
	return r
}

// WithSurveyCompletion installs the only outbound route for the opaque Survey
// completion effect. It may be a disabled provider; the router never falls
// back to a different outbound kind.
func (r *ProviderRouter) WithCustomerTag(provider effect.ProviderAdapter) *ProviderRouter {
	if r != nil {
		r.customerTag = provider
	}
	return r
}

func (r *ProviderRouter) WithCustomerOwnerHandoff(provider effect.ProviderAdapter) *ProviderRouter {
	if r != nil {
		r.ownerHandoff = provider
	}
	return r
}

func (r *ProviderRouter) WithSurveyCompletion(provider effect.ProviderAdapter) *ProviderRouter {
	if r != nil {
		r.surveyCompletion = provider
	}
	return r
}

// WithCommercePush installs the explicit Product/Order webhook route. It has
// no fallback because each frozen commerce envelope must use its own protocol.
func (r *ProviderRouter) WithCommercePush(provider effect.ProviderAdapter) *ProviderRouter {
	if r != nil {
		r.commercePush = provider
	}
	return r
}

func NewProviderRouter(tagCatalog effect.ProviderAdapter) *ProviderRouter {
	return &ProviderRouter{tagCatalog: tagCatalog}
}

func NewProviderRouterWithGroupMessage(tagCatalog, groupMessage effect.ProviderAdapter) *ProviderRouter {
	return &ProviderRouter{tagCatalog: tagCatalog, groupMessage: groupMessage}
}

func NewProviderRouterWithMessages(tagCatalog, groupMessage, message effect.ProviderAdapter) *ProviderRouter {
	return &ProviderRouter{tagCatalog: tagCatalog, groupMessage: groupMessage, automationMessage: message}
}

func NewProviderRouterWithChannels(tagCatalog, groupMessage, channelAsset effect.ProviderAdapter) *ProviderRouter {
	return &ProviderRouter{tagCatalog: tagCatalog, groupMessage: groupMessage, channelAsset: channelAsset}
}

func NewProviderRouterWithChannelEntrants(tagCatalog, groupMessage, channelAsset, channelEntrant effect.ProviderAdapter) *ProviderRouter {
	return &ProviderRouter{tagCatalog: tagCatalog, groupMessage: groupMessage, channelAsset: channelAsset, channelEntrant: channelEntrant}
}
func NewProviderRouterWithGroupMessageAndChannels(tagCatalog, groupMessage, channelAsset, channelEntrant, channelLink effect.ProviderAdapter) *ProviderRouter {
	return &ProviderRouter{tagCatalog: tagCatalog, groupMessage: groupMessage, channelAsset: channelAsset, channelEntrant: channelEntrant, channelLink: channelLink}
}

func (r *ProviderRouter) Execute(ctx context.Context, envelope effect.Envelope, attempt effect.Attempt) (effect.AdapterResult, error) {
	if r != nil {
		switch envelope.Kind {
		case effect.KindAutomationMessage:
			if r.automationMessage != nil {
				return r.automationMessage.Execute(ctx, envelope, attempt)
			}
		case effect.KindWeComTagCatalog:
			if r.tagCatalog != nil {
				return r.tagCatalog.Execute(ctx, envelope, attempt)
			}
		case effect.KindWeComTagCatalogMutation:
			if r.tagCatalogMutation != nil {
				return r.tagCatalogMutation.Execute(ctx, envelope, attempt)
			}
		case effect.KindWeComContactDescription:
			if r.contactDescription != nil {
				return r.contactDescription.Execute(ctx, envelope, attempt)
			}
		case effect.KindGroupMessage:
			if r.groupMessage != nil {
				return r.groupMessage.Execute(ctx, envelope, attempt)
			}
		case effect.KindChannelAsset:
			if r.channelAsset != nil {
				return r.channelAsset.Execute(ctx, envelope, attempt)
			}
		case effect.KindChannelWelcome, effect.KindChannelEntryTag:
			if r.channelEntrant != nil {
				return r.channelEntrant.Execute(ctx, envelope, attempt)
			}
		case effect.KindInvitationCode:
			if r.invitationCode != nil {
				return r.invitationCode.Execute(ctx, envelope, attempt)
			}
		case effect.KindChannelLink:
			if r.channelLink != nil {
				return r.channelLink.Execute(ctx, envelope, attempt)
			}
		case effect.KindOutboundMessage:
			if r.privateMessage != nil {
				return r.privateMessage.Execute(ctx, envelope, attempt)
			}
		case effect.KindOutboundMedia:
			if r.sidebarMedia != nil {
				return r.sidebarMedia.Execute(ctx, envelope, attempt)
			}
		case effect.KindSidebarJSSDKSend:
			if r.sidebarJSSDK != nil {
				return r.sidebarJSSDK.Execute(ctx, envelope, attempt)
			}
		case effect.KindSurveyCompletion:
			if r.surveyCompletion != nil {
				return r.surveyCompletion.Execute(ctx, envelope, attempt)
			}
		case effect.KindCustomerTagCommand:
			if r.customerTag != nil {
				return r.customerTag.Execute(ctx, envelope, attempt)
			}
		case effect.KindCustomerOwnerHandoff:
			if r.ownerHandoff != nil {
				return r.ownerHandoff.Execute(ctx, envelope, attempt)
			}
		case effect.KindCommerceProductPush:
			if r.commercePush != nil {
				return r.commercePush.Execute(ctx, envelope, attempt)
			}
		}
	}
	return effect.AdapterResult{Completion: effect.StateFinalFailed, ReceiptDigest: effect.Hash("outbound.provider.not-configured", string(envelope.Kind))}, nil
}

var _ effect.ProviderAdapter = (*ProviderRouter)(nil)

func (r *ProviderRouter) WithInvitationCode(p effect.ProviderAdapter) *ProviderRouter {
	r.invitationCode = p
	return r
}
