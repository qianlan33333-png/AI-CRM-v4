package port

import (
	"context"
	"encoding/json"
	"time"
)

// MemberGridWorkspace is Product-owned local workspace metadata.  It does not
// own member rows, customer records, or entitlement remarks.
type MemberGridWorkspace interface {
	Access(context.Context, ID, MemberGridActor) (MemberGridAccess, error)
	ListViews(context.Context, ID) ([]MemberGridView, error)
	CreateView(context.Context, CreateMemberGridViewCommand) (MemberGridView, error)
	UpdateView(context.Context, UpdateMemberGridViewCommand) (MemberGridView, error)
	DeleteView(context.Context, DeleteMemberGridViewCommand) (MemberGridView, error)
	ListCollaborators(context.Context, ID) ([]MemberGridCollaborator, error)
	CreateCollaborator(context.Context, CreateMemberGridCollaboratorCommand) (MemberGridCollaborator, error)
	UpdateCollaborator(context.Context, UpdateMemberGridCollaboratorCommand) (MemberGridCollaborator, error)
	DeleteCollaborator(context.Context, DeleteMemberGridCollaboratorCommand) (MemberGridCollaborator, error)
	Share(context.Context, ID) (MemberGridShare, error)
	SetShare(context.Context, SetMemberGridShareCommand) (MemberGridShare, bool, error)
	ResolveShare(context.Context, string) (MemberGridShare, error)
}

// MemberGridStaffDirectory is implemented by Access at composition time.
// Product stores the verified internal staff ID but never reads admin_users.
type MemberGridStaffDirectory interface {
	ActiveMemberGridStaff(context.Context, int64) (bool, error)
	MemberGridStaffByWeComUserID(context.Context, string) (MemberGridStaff, bool, error)
	MemberGridStaffByID(context.Context, int64) (MemberGridStaff, bool, error)
	ListActiveMemberGridStaff(context.Context) ([]MemberGridStaff, error)
}

// MemberGridStaff is a safe Access-owned internal-directory projection. It
// contains no customer identity and is used only to select or describe a
// collaborator in this Product-owned workspace.
type MemberGridStaff struct {
	AdminUserID int64
	WeComUserID string
	DisplayName string
	Active      bool
}

type MemberGridActor struct {
	AdminUserID  int64
	IsAdmin      bool
	IsSuperAdmin bool
}
type MemberGridAccess struct {
	CanView        bool
	CanEdit        bool
	CanManageViews bool
	CanShare       bool
}

type MemberGridView struct {
	ID        ID
	ProductID ID
	Name      string
	Position  int32
	Config    json.RawMessage
	Version   int64
	CreatedBy int64
	UpdatedBy int64
	CreatedAt time.Time
	UpdatedAt time.Time
}
type MemberGridCollaborator struct {
	ID          ID
	ProductID   ID
	AdminUserID int64
	Permission  string
	Version     int64
	CreatedBy   int64
	UpdatedBy   int64
	CreatedAt   time.Time
	UpdatedAt   time.Time
}
type MemberGridShare struct {
	ProductID  ID
	Enabled    bool
	PublicID   string
	Generation int64
	Version    int64
	CreatedBy  int64
	UpdatedBy  int64
	CreatedAt  time.Time
	UpdatedAt  time.Time
}
type CreateMemberGridViewCommand struct {
	ProductID      ID
	Name           string
	Config         json.RawMessage
	Actor          MemberGridActor
	IdempotencyKey string
}
type UpdateMemberGridViewCommand struct {
	ProductID, ViewID ID
	ExpectedVersion   int64
	Name              string
	Config            json.RawMessage
	Actor             MemberGridActor
	IdempotencyKey    string
}
type DeleteMemberGridViewCommand struct {
	ProductID, ViewID ID
	ExpectedVersion   int64
	Actor             MemberGridActor
	IdempotencyKey    string
}
type CreateMemberGridCollaboratorCommand struct {
	ProductID      ID
	AdminUserID    int64
	Permission     string
	Actor          MemberGridActor
	IdempotencyKey string
}
type UpdateMemberGridCollaboratorCommand struct {
	ProductID, CollaboratorID ID
	ExpectedVersion           int64
	Permission                string
	Actor                     MemberGridActor
	IdempotencyKey            string
}
type DeleteMemberGridCollaboratorCommand struct {
	ProductID, CollaboratorID ID
	ExpectedVersion           int64
	Actor                     MemberGridActor
	IdempotencyKey            string
}
type SetMemberGridShareCommand struct {
	ProductID       ID
	Enabled         bool
	ExpectedVersion int64
	Actor           MemberGridActor
	IdempotencyKey  string
}
