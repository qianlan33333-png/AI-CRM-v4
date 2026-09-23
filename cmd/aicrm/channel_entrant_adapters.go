package main

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	channelport "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/port"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	groupopsport "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/port"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/outbound"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	tagport "github.com/qianlan33333-png/AI-CRM-v3/internal/tag/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/wecom"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

type channelWelcomeMaterialAdapter struct {
	resolver groupopsport.MaterialSnapshotResolver
}

func (adapter channelWelcomeMaterialAdapter) ResolveWelcomeMaterialSnapshot(ctx context.Context, plan channelport.WelcomeMaterialPlan, requiredThrough time.Time) (json.RawMessage, string, error) {
	if adapter.resolver == nil {
		return nil, "", errors.New("channel welcome material resolver unavailable")
	}
	references := make([]groupopsport.MaterialReference, 0, len(plan.ImageIDs)+len(plan.MiniProgramIDs)+len(plan.AttachmentIDs)+len(plan.GroupInviteIDs))
	for _, id := range plan.ImageIDs {
		references = append(references, groupopsport.MaterialReference{Kind: "image", ID: id})
	}
	for _, id := range plan.MiniProgramIDs {
		references = append(references, groupopsport.MaterialReference{Kind: "miniprogram", ID: id})
	}
	for _, id := range plan.AttachmentIDs {
		references = append(references, groupopsport.MaterialReference{Kind: "attachment", ID: id})
	}
	for _, id := range plan.GroupInviteIDs {
		references = append(references, groupopsport.MaterialReference{Kind: "group_invite", ID: id})
	}
	return adapter.resolver.ResolveMaterialSnapshot(ctx, groupopsport.MaterialPlan{References: references}, requiredThrough)
}

type entrantActionSource interface {
	ReadPublishedEntrantAction(context.Context, string) (channelport.PublishedEntrantAction, error)
	FreezePublishedWelcomeMessage(context.Context, channelport.WelcomeMessageFreezeRequest) (string, error)
}
type channelEntrantActionReaderAdapter struct {
	uow    platformport.UnitOfWork
	source entrantActionSource
}

func (adapter channelEntrantActionReaderAdapter) ReadPublishedEntrantAction(ctx context.Context, source string) (channelport.PublishedEntrantAction, error) {
	var result channelport.PublishedEntrantAction
	err := adapter.uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		result, readErr = adapter.source.ReadPublishedEntrantAction(tx, source)
		return readErr
	})
	return result, err
}

func (adapter channelEntrantActionReaderAdapter) FreezePublishedWelcomeMessage(ctx context.Context, request channelport.WelcomeMessageFreezeRequest) (string, error) {
	var result string
	err := adapter.uow.Within(ctx, func(tx context.Context) error {
		var freezeErr error
		result, freezeErr = adapter.source.FreezePublishedWelcomeMessage(tx, request)
		return freezeErr
	})
	return result, err
}

type entrantStaffReader interface {
	UserByID(context.Context, int64, bool) (accessdomain.User, error)
}
type entrantRelationshipReader interface {
	IsActive(context.Context, string, string, customerdomain.CustomerID) (bool, error)
}
type channelCurrentContactAdapter struct {
	uow           platformport.UnitOfWork
	corpID        string
	staff         entrantStaffReader
	relationships entrantRelationshipReader
	identities    identityport.ExternalIdentityValueReader
}

func (adapter channelCurrentContactAdapter) CurrentExternalContact(ctx context.Context, customerID customerdomain.CustomerID, staffID int64) (wecomport.CurrentExternalContact, error) {
	var result wecomport.CurrentExternalContact
	err := adapter.uow.Within(ctx, func(tx context.Context) error {
		user, err := adapter.staff.UserByID(tx, staffID, false)
		if err != nil || !user.Active || user.WeComUserID == "" {
			return errors.New("current channel staff unavailable")
		}
		active, err := adapter.relationships.IsActive(tx, adapter.corpID, user.WeComUserID, customerID)
		if err != nil || !active {
			return errors.New("current WeCom relationship unavailable")
		}
		value, found, err := adapter.identities.VerifiedExternalIdentityValue(tx, customerID, identitydomain.KindWeComExternalUserID, "wecom-corp:"+adapter.corpID)
		if err != nil || !found {
			return errors.New("current WeCom identity unavailable")
		}
		result = wecomport.CurrentExternalContact{EmployeeUserID: user.WeComUserID, ExternalUserID: value}
		return nil
	})
	return result, err
}

type channelProviderTagAdapter struct {
	uow  platformport.UnitOfWork
	tags tagport.ProviderTagBindingReader
}

func (adapter channelProviderTagAdapter) ProviderTagID(ctx context.Context, tagID int64) (string, bool, error) {
	var value string
	var found bool
	err := adapter.uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		value, found, readErr = adapter.tags.ProviderTagID(tx, tagID)
		return readErr
	})
	return value, found, err
}

var _ channelport.PublishedEntrantActionReader = channelEntrantActionReaderAdapter{}
var _ channelport.WelcomeMessageFreezer = channelEntrantActionReaderAdapter{}
var _ channelport.WelcomeMaterialSnapshotResolver = channelWelcomeMaterialAdapter{}
var _ wecomport.CurrentExternalContactReader = channelCurrentContactAdapter{}

// contactDescriptionTargetAdapter resolves the verified, corp-scoped external
// identity for a directory-observed follow employee. The directory observation
// is the source of the run's relationship eligibility; callback relationship
// rows are a separate lifecycle projection and are intentionally not required
// for a historical full sync.
type contactDescriptionTargetAdapter struct {
	uow        platformport.UnitOfWork
	corpID     string
	identities identityport.ExternalIdentityValueReader
}

func (a contactDescriptionTargetAdapter) ResolveContactDescriptionTarget(ctx context.Context, customerID customerdomain.CustomerID, employeeID string) (wecomport.CurrentExternalContact, error) {
	var result wecomport.CurrentExternalContact
	err := a.uow.Within(ctx, func(tx context.Context) error {
		value, found, err := a.identities.VerifiedExternalIdentityValue(tx, customerID, identitydomain.KindWeComExternalUserID, "wecom-corp:"+a.corpID)
		if err != nil || !found {
			return errors.New("current WeCom identity unavailable")
		}
		result = wecomport.CurrentExternalContact{EmployeeUserID: employeeID, ExternalUserID: value}
		return nil
	})
	return result, err
}

var _ outbound.ContactDescriptionTargetResolver = contactDescriptionTargetAdapter{}
var _ tagport.ProviderTagBindingReader = channelProviderTagAdapter{}
var _ wecom.WelcomeGrantRedeemer = (*wecom.PostgreSQLWelcomeGrantStore)(nil)
var _ wecomport.WelcomeGrantRedeemer = (*wecom.PostgreSQLWelcomeGrantStore)(nil)
