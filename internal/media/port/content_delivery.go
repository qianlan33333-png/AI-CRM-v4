package port

import (
	"context"
	"encoding/json"
	"time"
)

type ContentRef struct {
	Kind string `json:"kind"`
	ID   int64  `json:"id"`
}
type ContentPackage struct {
	ID          int64        `json:"id"`
	Name        string       `json:"name"`
	ContentText string       `json:"content_text"`
	Enabled     bool         `json:"enabled"`
	Version     int64        `json:"version"`
	Refs        []ContentRef `json:"refs"`
}
type ContentPackageCommand struct {
	Name, ContentText string
	Enabled           bool
	Refs              []ContentRef
	Actor             int64
	IdempotencyKey    string
}
type ContentPackageUpdateCommand struct {
	ID, ExpectedVersion int64
	ContentPackageCommand
}

// ContentDeliveryMutationReceipt and Reservation are the transaction-neutral
// persistence contract for Media content delivery. Keeping them in port lets
// the Media Store implement the application contract without importing app.
type ContentDeliveryMutationReceipt struct {
	ID             int64
	Operation      string
	Actor          int64
	KeyDigest      [32]byte
	PayloadDigest  [32]byte
	ResultSnapshot json.RawMessage
}
type ContentDeliveryMutationReservation struct {
	Operation     string
	Actor         int64
	KeyDigest     [32]byte
	PayloadDigest [32]byte
	CreatedAt     time.Time
}
type DeliveryBinding struct {
	ID            int64  `json:"id"`
	CampaignCode  string `json:"campaign_code"`
	PlanID        string `json:"plan_id"`
	PackageID     int64  `json:"package_id"`
	GroupInviteID int64  `json:"group_invite_id"`
	Version       int64  `json:"version"`
}
type DeliveryBindingCommand struct {
	CampaignCode, PlanID                             string
	PackageID, GroupInviteID, ExpectedVersion, Actor int64
	IdempotencyKey                                   string
}
type AttachmentUploadInitiateCommand struct {
	FileName, Name, Description, SHA256 string
	Size, Actor                         int64
	Enabled                             bool
	IdempotencyKey                      string
}
type AttachmentUploadPartCommand struct {
	UploadID       int64
	PartNumber     int32
	SHA256         string
	Content        []byte
	Actor          int64
	IdempotencyKey string
}
type AttachmentUploadCompleteCommand struct {
	UploadID, Actor int64
	IdempotencyKey  string
}
type ContentDeliveryService interface {
	Preview(context.Context, ContentPackageCommand) (ContentPackage, error)
	Create(context.Context, ContentPackageCommand) (ContentPackage, error)
	Update(context.Context, ContentPackageUpdateCommand) (ContentPackage, error)
	Bind(context.Context, DeliveryBindingCommand) (DeliveryBinding, error)
	GetBinding(context.Context, string, string) (DeliveryBinding, error)
	InitiatePDF(context.Context, AttachmentUploadInitiateCommand) (int64, error)
	PutPDFPart(context.Context, AttachmentUploadPartCommand) error
	CompletePDF(context.Context, AttachmentUploadCompleteCommand) (int64, error)
}
