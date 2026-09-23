package app

import (
	"context"
	"errors"

	mediastore "github.com/qianlan33333-png/AI-CRM-v3/internal/media/store"
)

// HTTPFacade is the Media application boundary used by the compatibility
// adapter. The HTTP package depends on this interface, never on PostgreSQL.
type HTTPFacade interface {
	HTTPRepository
	// ValidImageVariant is owned by the Media application layer; adapters must
	// not maintain a second variant allow-list.
	ValidImageVariant(string) bool
	GetImageVariant(context.Context, int64, string) (ImageVariant, error)
	GetEnabledImageVariant(context.Context, int64, string) (ImageVariant, error)
	LocalImageExists(context.Context, int64) (bool, error)
	ReferenceConflict(error) (map[string][]int64, bool)
}

// HTTPRepository is the narrow persistence seam used by the compatibility
// facade. It excludes app-owned validation and conflict projection methods so
// a PostgreSQL repository cannot be used directly as the HTTP application API.
type HTTPRepository interface {
	ListImagesFiltered(context.Context, ImageQuery) ([]map[string]any, int, error)
	ImageFacets(context.Context) ([]string, []string, error)
	Image(context.Context, int64) (map[string]any, []byte, string, error)
	CreateImage(context.Context, int64, string, ImageInput) (map[string]any, error)
	UpdateImage(context.Context, int64, int64, string, map[string]any) (map[string]any, error)
	ListAttachments(context.Context, int, int, bool, string) ([]map[string]any, int, error)
	Attachment(context.Context, int64) (map[string]any, []byte, error)
	CreateAttachment(context.Context, int64, string, AttachmentInput) (map[string]any, error)
	UpdateAttachment(context.Context, int64, int64, string, map[string]any) (map[string]any, error)
	ListMiniPrograms(context.Context, int, int, bool, string) ([]map[string]any, int, error)
	MiniProgram(context.Context, int64) (map[string]any, error)
	CreateMiniProgram(context.Context, int64, string, map[string]any) (map[string]any, error)
	UpdateMiniProgram(context.Context, int64, int64, string, map[string]any) (map[string]any, error)
	ResolveMiniProgramThumbnail(context.Context, int64, int64, string) (map[string]any, error)
	ListGroupInvites(context.Context, int, int, bool, string) ([]map[string]any, int, error)
	GroupInvite(context.Context, int64) (map[string]any, error)
	CreateGroupInvite(context.Context, int64, string, map[string]any) (map[string]any, error)
	UpdateGroupInvite(context.Context, int64, int64, string, map[string]any) (map[string]any, error)
	ArchiveGroupInvite(context.Context, int64, int64, string) (map[string]any, error)
	Delete(context.Context, string, int64, int64, string) (map[string]any, error)
	InitiateAttachmentUpload(context.Context, int64, string, AttachmentUploadInput) (int64, error)
	PutAttachmentUploadPart(context.Context, int64, int, int64, string, string, []byte) error
	CompleteAttachmentUpload(context.Context, int64, int64, string) (int64, error)
}

type variantRepository interface {
	Within(context.Context, func(context.Context) error) error
	ReadImageVariant(context.Context, int64) (mediastore.StoredImageVariant, error)
}

type variantUOW struct{ repository variantRepository }

func (u variantUOW) Within(ctx context.Context, callback func(context.Context) error) error {
	return u.repository.Within(ctx, callback)
}

type variantStore struct{ repository variantRepository }

func (s variantStore) ReadImageVariant(ctx context.Context, id int64) (ImageVariantRow, error) {
	row, err := s.repository.ReadImageVariant(ctx, id)
	if errors.Is(err, mediastore.ErrNotFound) {
		return ImageVariantRow{}, ErrImageVariantNotFound
	}
	if err != nil {
		return ImageVariantRow{}, err
	}
	return ImageVariantRow{ID: row.ID, FileName: row.FileName, MimeType: row.MimeType, FileSize: row.FileSize, Width: row.Width, Height: row.Height, Enabled: row.Enabled, ImageChecksum: row.ImageChecksum, BlobChecksum: row.BlobChecksum, Content: row.Content}, nil
}

type httpFacade struct {
	store    HTTPRepository
	variants *Service
}

func NewHTTPFacade(store HTTPRepository) (HTTPFacade, error) {
	if store == nil {
		return nil, errors.New("media HTTP facade store is required")
	}
	variants, ok := store.(variantRepository)
	if !ok {
		return nil, errors.New("media HTTP facade variant reader is required")
	}
	return &httpFacade{store: store, variants: NewReadService(variantUOW{variants}, variantStore{variants})}, nil
}

type ImageInput = mediastore.ImageInput
type AttachmentInput = mediastore.AttachmentInput
type AttachmentUploadInput = mediastore.AttachmentUploadInput
type ImageQuery = mediastore.ImageQuery

var (
	ErrHTTPNotFound                   = mediastore.ErrNotFound
	ErrHTTPConflict                   = mediastore.ErrConflict
	ErrHTTPReferences                 = mediastore.ErrReferences
	ErrHTTPReferenceReaderUnavailable = mediastore.ErrReferenceReaderUnavailable
	ErrHTTPInvalid                    = mediastore.ErrInvalid
)

func (f *httpFacade) ListImagesFiltered(c context.Context, q ImageQuery) ([]map[string]any, int, error) {
	return f.store.ListImagesFiltered(c, q)
}
func (f *httpFacade) ValidImageVariant(key string) bool { return ValidImageVariantKey(key) }
func (f *httpFacade) GetImageVariant(ctx context.Context, imageID int64, key string) (ImageVariant, error) {
	if f == nil || f.variants == nil {
		return ImageVariant{}, ErrImageVariantUnavailable
	}
	return f.variants.GetImageVariant(ctx, imageID, key)
}
func (f *httpFacade) GetEnabledImageVariant(ctx context.Context, imageID int64, key string) (ImageVariant, error) {
	if f == nil || f.variants == nil {
		return ImageVariant{}, ErrImageVariantUnavailable
	}
	return f.variants.GetEnabledImageVariant(ctx, imageID, key)
}
func (f *httpFacade) LocalImageExists(ctx context.Context, imageID int64) (bool, error) {
	if f == nil || imageID < 1 {
		return false, ErrHTTPNotFound
	}
	_, _, _, err := f.store.Image(ctx, imageID)
	if errors.Is(err, ErrHTTPNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
func (f *httpFacade) ReferenceConflict(err error) (map[string][]int64, bool) {
	var conflict *mediastore.ReferenceConflict
	if !errors.As(err, &conflict) || conflict == nil {
		return nil, false
	}
	return conflict.References, true
}
func (f *httpFacade) ImageFacets(c context.Context) ([]string, []string, error) {
	return f.store.ImageFacets(c)
}
func (f *httpFacade) Image(c context.Context, id int64) (map[string]any, []byte, string, error) {
	return f.store.Image(c, id)
}
func (f *httpFacade) CreateImage(c context.Context, a int64, k string, v ImageInput) (map[string]any, error) {
	return f.store.CreateImage(c, a, k, v)
}
func (f *httpFacade) UpdateImage(c context.Context, id, a int64, k string, v map[string]any) (map[string]any, error) {
	return f.store.UpdateImage(c, id, a, k, v)
}
func (f *httpFacade) ListAttachments(c context.Context, l, o int, e bool, q string) ([]map[string]any, int, error) {
	return f.store.ListAttachments(c, l, o, e, q)
}
func (f *httpFacade) Attachment(c context.Context, id int64) (map[string]any, []byte, error) {
	return f.store.Attachment(c, id)
}
func (f *httpFacade) CreateAttachment(c context.Context, a int64, k string, v AttachmentInput) (map[string]any, error) {
	return f.store.CreateAttachment(c, a, k, v)
}
func (f *httpFacade) UpdateAttachment(c context.Context, id, a int64, k string, v map[string]any) (map[string]any, error) {
	return f.store.UpdateAttachment(c, id, a, k, v)
}
func (f *httpFacade) ListMiniPrograms(c context.Context, l, o int, e bool, q string) ([]map[string]any, int, error) {
	return f.store.ListMiniPrograms(c, l, o, e, q)
}
func (f *httpFacade) MiniProgram(c context.Context, id int64) (map[string]any, error) {
	return f.store.MiniProgram(c, id)
}
func (f *httpFacade) CreateMiniProgram(c context.Context, a int64, k string, v map[string]any) (map[string]any, error) {
	return f.store.CreateMiniProgram(c, a, k, v)
}
func (f *httpFacade) UpdateMiniProgram(c context.Context, id, a int64, k string, v map[string]any) (map[string]any, error) {
	return f.store.UpdateMiniProgram(c, id, a, k, v)
}
func (f *httpFacade) ResolveMiniProgramThumbnail(c context.Context, id, a int64, k string) (map[string]any, error) {
	return f.store.ResolveMiniProgramThumbnail(c, id, a, k)
}
func (f *httpFacade) ListGroupInvites(c context.Context, l, o int, e bool, q string) ([]map[string]any, int, error) {
	return f.store.ListGroupInvites(c, l, o, e, q)
}
func (f *httpFacade) GroupInvite(c context.Context, id int64) (map[string]any, error) {
	return f.store.GroupInvite(c, id)
}
func (f *httpFacade) CreateGroupInvite(c context.Context, a int64, k string, v map[string]any) (map[string]any, error) {
	return f.store.CreateGroupInvite(c, a, k, v)
}
func (f *httpFacade) UpdateGroupInvite(c context.Context, id, a int64, k string, v map[string]any) (map[string]any, error) {
	return f.store.UpdateGroupInvite(c, id, a, k, v)
}
func (f *httpFacade) ArchiveGroupInvite(c context.Context, id, a int64, k string) (map[string]any, error) {
	return f.store.ArchiveGroupInvite(c, id, a, k)
}
func (f *httpFacade) Delete(c context.Context, k string, id, a int64, key string) (map[string]any, error) {
	return f.store.Delete(c, k, id, a, key)
}
func (f *httpFacade) InitiateAttachmentUpload(c context.Context, a int64, k string, v AttachmentUploadInput) (int64, error) {
	return f.store.InitiateAttachmentUpload(c, a, k, v)
}
func (f *httpFacade) PutAttachmentUploadPart(c context.Context, u int64, p int, a int64, k, d string, b []byte) error {
	return f.store.PutAttachmentUploadPart(c, u, p, a, k, d, b)
}
func (f *httpFacade) CompleteAttachmentUpload(c context.Context, u, a int64, k string) (int64, error) {
	return f.store.CompleteAttachmentUpload(c, u, a, k)
}

// MaterialGrouping is V3-owned metadata; it does not upload provider material.
type MaterialGrouping interface {
	MaterialGroups(context.Context, string) ([]map[string]any, error)
	SetMaterialGroup(context.Context, string, int64, int64, int64, string, string) (map[string]any, error)
	ListAttachmentsInGroup(context.Context, int, int, bool, string, *string) ([]map[string]any, int, error)
	ListMiniProgramsInGroup(context.Context, int, int, bool, string, *string) ([]map[string]any, int, error)
}

func (f *httpFacade) MaterialGroups(c context.Context, k string) ([]map[string]any, error) {
	s, ok := f.store.(MaterialGrouping)
	if !ok {
		return nil, ErrHTTPInvalid
	}
	return s.MaterialGroups(c, k)
}
func (f *httpFacade) SetMaterialGroup(c context.Context, k string, id, a, v int64, key, g string) (map[string]any, error) {
	s, ok := f.store.(MaterialGrouping)
	if !ok {
		return nil, ErrHTTPInvalid
	}
	return s.SetMaterialGroup(c, k, id, a, v, key, g)
}
func (f *httpFacade) ListAttachmentsInGroup(c context.Context, l, o int, e bool, q string, g *string) ([]map[string]any, int, error) {
	s, ok := f.store.(MaterialGrouping)
	if !ok {
		return nil, 0, ErrHTTPInvalid
	}
	return s.ListAttachmentsInGroup(c, l, o, e, q, g)
}
func (f *httpFacade) ListMiniProgramsInGroup(c context.Context, l, o int, e bool, q string, g *string) ([]map[string]any, int, error) {
	s, ok := f.store.(MaterialGrouping)
	if !ok {
		return nil, 0, ErrHTTPInvalid
	}
	return s.ListMiniProgramsInGroup(c, l, o, e, q, g)
}

// GroupManagement owns stable group commands separately from legacy category adapters.
type GroupCommand = mediastore.GroupCommand
type GroupManagement interface {
	ManageMaterialGroup(context.Context, string, string, int64, string, GroupCommand) (map[string]any, error)
}

func (f *httpFacade) ManageMaterialGroup(c context.Context, k, op string, a int64, key string, cmd GroupCommand) (map[string]any, error) {
	s, ok := f.store.(GroupManagement)
	if !ok {
		return nil, ErrHTTPInvalid
	}
	return s.ManageMaterialGroup(c, k, op, a, key, cmd)
}

type GroupMemberReader interface {
	MaterialGroupMembers(context.Context, string, []int64) ([]map[string]any, error)
}

func (f *httpFacade) MaterialGroupMembers(c context.Context, k string, ids []int64) ([]map[string]any, error) {
	s, ok := f.store.(GroupMemberReader)
	if !ok {
		return nil, ErrHTTPInvalid
	}
	return s.MaterialGroupMembers(c, k, ids)
}
