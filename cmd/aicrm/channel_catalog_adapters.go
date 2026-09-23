package main

import (
	"context"
	"errors"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	channelstore "github.com/qianlan33333-png/AI-CRM-v3/internal/channel"
	channeldomain "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/domain"
	channelport "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/port"
	mediasport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	tagdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/tag/domain"
)

type channelStaffUserReader interface {
	UserByID(context.Context, int64, bool) (accessdomain.User, error)
	ListUsers(context.Context) ([]accessdomain.User, error)
}

func (adapter channelStaffReferenceAdapter) ListAcquisitionStaff(ctx context.Context) ([]channelstore.AcquisitionStaff, error) {
	if adapter.users == nil {
		return nil, errors.New("channel staff reader unavailable")
	}
	users, err := adapter.users.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]channelstore.AcquisitionStaff, 0, len(users))
	for _, user := range staffWithDisplayProfiles(ctx, users, adapter.profiles) {
		result = append(result, channelstore.AcquisitionStaff{ID: user.ID, WeComUserID: user.WeComUserID, DisplayName: user.DisplayName, Active: user.Active})
	}
	return result, nil
}

type channelStaffReferenceAdapter struct {
	users    channelStaffUserReader
	profiles staffDisplayProfileReader
}

func (adapter channelStaffReferenceAdapter) ReadChannelStaff(ctx context.Context, ids []int64) ([]channelport.StaffSnapshot, error) {
	if adapter.users == nil {
		return nil, errors.New("channel staff reader unavailable")
	}
	result := make([]channelport.StaffSnapshot, 0, len(ids))
	users := make([]accessdomain.User, 0, len(ids))
	for _, id := range ids {
		user, err := adapter.users.UserByID(ctx, id, false)
		if err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	for _, user := range staffWithDisplayProfiles(ctx, users, adapter.profiles) {
		result = append(result, channelport.StaffSnapshot{ID: user.ID, Name: user.DisplayName, Active: user.Active})
	}
	return result, nil
}

type channelMaterialReader interface {
	mediasport.ImageMetadataReader
	mediasport.AttachmentMetadataReader
	mediasport.MiniProgramMetadataReader
	mediasport.GroupInviteMetadataReader
}

type channelMaterialReferenceAdapter struct{ media channelMaterialReader }

func (adapter channelMaterialReferenceAdapter) ValidateChannelMaterials(ctx context.Context, refs channelport.MaterialReferences) error {
	if adapter.media == nil {
		return errors.New("channel material reader unavailable")
	}
	checks := []struct {
		ids    []int64
		exists func(context.Context, int64) (bool, error)
	}{
		{refs.ImageIDs, adapter.media.ImageExists},
		{refs.MiniProgramIDs, adapter.media.MiniProgramExists},
		{refs.AttachmentIDs, adapter.media.AttachmentExists},
		{refs.GroupInviteIDs, adapter.media.GroupInviteExists},
	}
	for _, check := range checks {
		for _, id := range check.ids {
			exists, err := check.exists(ctx, id)
			if err != nil {
				return err
			}
			if !exists {
				return errors.New("channel material reference is not eligible")
			}
		}
	}
	return nil
}

type channelTagReader interface {
	GetTag(context.Context, int64) (tagdomain.Tag, error)
}

type channelTagReferenceAdapter struct{ tags channelTagReader }

func (adapter channelTagReferenceAdapter) ReadChannelTag(ctx context.Context, id int64) (channelport.TagSnapshot, error) {
	if adapter.tags == nil {
		return channelport.TagSnapshot{}, errors.New("channel tag reader unavailable")
	}
	tag, err := adapter.tags.GetTag(ctx, id)
	if err != nil {
		return channelport.TagSnapshot{}, err
	}
	return channelport.TagSnapshot{ID: tag.ID, Name: tag.Name, GroupName: tag.GroupName, Active: true}, nil
}

var _ channelport.StaffReferenceReader = channelStaffReferenceAdapter{}
var _ channelport.MaterialReferenceValidator = channelMaterialReferenceAdapter{}
var _ channelport.TagReferenceReader = channelTagReferenceAdapter{}

type channelPublicLeadCatalog interface {
	Get(context.Context, int64) (channeldomain.Channel, error)
}
type channelPublicLeadQRCodeAdapter struct{ catalog channelPublicLeadCatalog }

func (a channelPublicLeadQRCodeAdapter) ReadPublicLeadQRCode(ctx context.Context, id int64) (channelport.PublicLeadQRCode, error) {
	if a.catalog == nil || id < 1 {
		return channelport.PublicLeadQRCode{}, errors.New("public lead channel unavailable")
	}
	value, err := a.catalog.Get(ctx, id)
	if err != nil || value.Status != channeldomain.StatusActive || value.Config.QRCodeURL == "" {
		return channelport.PublicLeadQRCode{}, errors.New("public lead channel unavailable")
	}
	return channelport.PublicLeadQRCode{URL: value.Config.QRCodeURL}, nil
}

var _ channelport.PublicLeadQRCodeReader = channelPublicLeadQRCodeAdapter{}
