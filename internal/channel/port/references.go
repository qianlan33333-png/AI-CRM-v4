package port

import "context"

type MaterialReferences struct {
	ImageIDs       []int64
	MiniProgramIDs []int64
	AttachmentIDs  []int64
	GroupInviteIDs []int64
}

// MaterialReferenceValidator is implemented by a composition adapter over
// Media's stable reference ports. Channel never reads Media-owned tables.
type MaterialReferenceValidator interface {
	ValidateChannelMaterials(context.Context, MaterialReferences) error
}

type TagSnapshot struct {
	ID        int64
	Name      string
	GroupName string
	Active    bool
}

// TagReferenceReader is implemented over Tag's stable catalog port.
type TagReferenceReader interface {
	ReadChannelTag(context.Context, int64) (TagSnapshot, error)
}

type StaffSnapshot struct {
	ID     int64
	Name   string
	Active bool
}

// StaffReferenceReader supplies local staff facts only. Provider follow-user
// eligibility is a separate WeCom read port used at publish time.
type StaffReferenceReader interface {
	ReadChannelStaff(context.Context, []int64) ([]StaffSnapshot, error)
}
