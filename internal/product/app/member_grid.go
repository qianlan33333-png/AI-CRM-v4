package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

type MemberGridStore interface {
	GetServicePeriodProduct(context.Context, productport.ID) (productport.Product, error)
	GetServicePeriodProductForUpdate(context.Context, productport.ID) (productport.Product, error)
	ListMemberGridViews(context.Context, productport.ID) ([]productport.MemberGridView, error)
	GetMemberGridView(context.Context, productport.ID, productport.ID) (productport.MemberGridView, error)
	CreateMemberGridView(context.Context, productport.MemberGridView) (productport.MemberGridView, error)
	UpdateMemberGridView(context.Context, productport.MemberGridView) (productport.MemberGridView, error)
	DeleteMemberGridView(context.Context, productport.ID, productport.ID, int64) (productport.MemberGridView, error)
	ListMemberGridCollaborators(context.Context, productport.ID) ([]productport.MemberGridCollaborator, error)
	FindMemberGridCollaborator(context.Context, productport.ID, int64) (productport.MemberGridCollaborator, error)
	CreateMemberGridCollaborator(context.Context, productport.MemberGridCollaborator) (productport.MemberGridCollaborator, error)
	UpdateMemberGridCollaborator(context.Context, productport.MemberGridCollaborator) (productport.MemberGridCollaborator, error)
	DeleteMemberGridCollaborator(context.Context, productport.ID, productport.ID, int64) (productport.MemberGridCollaborator, error)
	GetMemberGridShare(context.Context, productport.ID) (productport.MemberGridShare, error)
	GetMemberGridShareByToken(context.Context, string) (productport.MemberGridShare, error)
	SetMemberGridShare(context.Context, productport.MemberGridShare, int64) (productport.MemberGridShare, error)
	Reserve(context.Context, Reservation) (Receipt, bool, error)
	Complete(context.Context, int64, json.RawMessage, time.Time) (Receipt, error)
}

func (s *MemberGridWorkspaceService) reserveMemberGrid(ctx context.Context, operation string, actor productport.MemberGridActor, key string, payload any) (Receipt, bool, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return Receipt{}, false, ErrInvalidCursor
	}
	payloadDigest := sha256.Sum256(raw)
	receipt, owned, err := s.store.Reserve(ctx, Reservation{Operation: "service_period_member_grid." + operation, ActorScope: "admin:" + strconv.FormatInt(actor.AdminUserID, 10), KeyDigest: sha256.Sum256([]byte(key)), PayloadDigest: payloadDigest, CreatedAt: s.now().UTC()})
	if err != nil {
		return Receipt{}, false, err
	}
	if receipt.PayloadDigest != payloadDigest {
		return Receipt{}, false, ErrConflict
	}
	// Product Store returns owned=true for the caller that inserted the receipt.
	// Workspace commands use the second result as replay=true, so invert it at
	// this boundary before callers decide whether to mutate or replay.
	return receipt, !owned, nil
}
func (s *MemberGridWorkspaceService) replayMemberGrid(receipt Receipt, target any) error {
	if receipt.State != "completed" || len(receipt.ResultSnapshot) == 0 || json.Unmarshal(receipt.ResultSnapshot, target) != nil {
		return ErrUnavailable
	}
	return nil
}
func (s *MemberGridWorkspaceService) completeMemberGrid(ctx context.Context, receipt Receipt, result any) error {
	raw, err := json.Marshal(result)
	if err != nil {
		return ErrUnavailable
	}
	_, err = s.store.Complete(ctx, receipt.ID, raw, s.now().UTC())
	return err
}

type MemberGridWorkspaceService struct {
	uow    platformport.UnitOfWork
	store  MemberGridStore
	staff  productport.MemberGridStaffDirectory
	events productport.EventAppender
	now    func() time.Time
}

func NewMemberGridWorkspaceService(uow platformport.UnitOfWork, store MemberGridStore, staff productport.MemberGridStaffDirectory, events productport.EventAppender) *MemberGridWorkspaceService {
	return &MemberGridWorkspaceService{uow: uow, store: store, staff: staff, events: events, now: time.Now}
}
func (s *MemberGridWorkspaceService) Access(ctx context.Context, id productport.ID, a productport.MemberGridActor) (out productport.MemberGridAccess, err error) {
	if !readyGrid(s, id, a) {
		return out, ErrNotFound
	}
	err = s.uow.Within(ctx, func(tx context.Context) error {
		if _, e := s.store.GetServicePeriodProduct(tx, id); e != nil {
			return e
		}
		if a.IsAdmin || a.IsSuperAdmin {
			out = productport.MemberGridAccess{
				CanView:        true,
				CanEdit:        true,
				CanManageViews: true,
				CanShare:       a.IsAdmin || a.IsSuperAdmin,
			}
			return nil
		}
		c, e := s.store.FindMemberGridCollaborator(tx, id, a.AdminUserID)
		if e != nil {
			return e
		}
		out = productport.MemberGridAccess{CanView: true, CanEdit: c.Permission == "edit", CanManageViews: c.Permission == "edit"}
		return nil
	})
	return out, classify(err)
}
func (s *MemberGridWorkspaceService) ListViews(ctx context.Context, id productport.ID) (out []productport.MemberGridView, err error) {
	err = s.uow.Within(ctx, func(tx context.Context) error {
		if _, e := s.store.GetServicePeriodProduct(tx, id); e != nil {
			return e
		}
		var e error
		out, e = s.store.ListMemberGridViews(tx, id)
		return e
	})
	return out, classify(err)
}
func (s *MemberGridWorkspaceService) ListCollaborators(ctx context.Context, id productport.ID) (out []productport.MemberGridCollaborator, err error) {
	err = s.uow.Within(ctx, func(tx context.Context) error {
		if _, e := s.store.GetServicePeriodProduct(tx, id); e != nil {
			return e
		}
		var e error
		out, e = s.store.ListMemberGridCollaborators(tx, id)
		return e
	})
	return out, classify(err)
}
func (s *MemberGridWorkspaceService) CreateView(ctx context.Context, c productport.CreateMemberGridViewCommand) (out productport.MemberGridView, err error) {
	if !validGridWrite(c.ProductID, c.Actor, c.IdempotencyKey) || !validView(c.Name, c.Config) {
		return out, ErrInvalidCursor
	}
	err = s.uow.Within(ctx, func(tx context.Context) error {
		receipt, replay, e := s.reserveMemberGrid(tx, "view.create", c.Actor, c.IdempotencyKey, c)
		if e != nil {
			return e
		}
		if replay {
			return s.replayMemberGrid(receipt, &out)
		}
		if e := s.lockWritableMemberGridProduct(tx, c.ProductID); e != nil {
			return e
		}
		allowed, authErr := s.memberGridWriteAllowed(tx, c.ProductID, c.Actor, false)
		if authErr != nil {
			return authErr
		}
		if !allowed {
			return ErrNotFound
		}
		out, e = s.store.CreateMemberGridView(tx, productport.MemberGridView{ProductID: c.ProductID, Name: strings.TrimSpace(c.Name), Config: c.Config, CreatedBy: c.Actor.AdminUserID, UpdatedBy: c.Actor.AdminUserID, CreatedAt: s.now().UTC(), UpdatedAt: s.now().UTC()})
		if e != nil {
			return e
		}
		if e = s.event(tx, "view.created", c.IdempotencyKey, c.Actor, out); e != nil {
			return e
		}
		return s.completeMemberGrid(tx, receipt, out)
	})
	return out, classify(err)
}
func (s *MemberGridWorkspaceService) UpdateView(ctx context.Context, c productport.UpdateMemberGridViewCommand) (out productport.MemberGridView, err error) {
	if !validGridWrite(c.ProductID, c.Actor, c.IdempotencyKey) || c.ViewID < 1 || c.ExpectedVersion < 1 || !validView(c.Name, c.Config) {
		return out, ErrInvalidCursor
	}
	err = s.uow.Within(ctx, func(tx context.Context) error {
		receipt, replay, e := s.reserveMemberGrid(tx, "view.update", c.Actor, c.IdempotencyKey, c)
		if e != nil {
			return e
		}
		if replay {
			return s.replayMemberGrid(receipt, &out)
		}
		if e := s.lockWritableMemberGridProduct(tx, c.ProductID); e != nil {
			return e
		}
		allowed, authErr := s.memberGridWriteAllowed(tx, c.ProductID, c.Actor, false)
		if authErr != nil {
			return authErr
		}
		if !allowed {
			return ErrNotFound
		}
		out, e = s.store.UpdateMemberGridView(tx, productport.MemberGridView{ID: c.ViewID, ProductID: c.ProductID, Name: strings.TrimSpace(c.Name), Config: c.Config, Version: c.ExpectedVersion, UpdatedBy: c.Actor.AdminUserID, UpdatedAt: s.now().UTC()})
		if e != nil {
			return e
		}
		if e = s.event(tx, "view.updated", c.IdempotencyKey, c.Actor, out); e != nil {
			return e
		}
		return s.completeMemberGrid(tx, receipt, out)
	})
	return out, classify(err)
}
func (s *MemberGridWorkspaceService) DeleteView(ctx context.Context, c productport.DeleteMemberGridViewCommand) (out productport.MemberGridView, err error) {
	if !validGridWrite(c.ProductID, c.Actor, c.IdempotencyKey) || c.ViewID < 1 || c.ExpectedVersion < 1 {
		return out, ErrInvalidCursor
	}
	err = s.uow.Within(ctx, func(tx context.Context) error {
		receipt, replay, e := s.reserveMemberGrid(tx, "view.delete", c.Actor, c.IdempotencyKey, c)
		if e != nil {
			return e
		}
		if replay {
			return s.replayMemberGrid(receipt, &out)
		}
		if e := s.lockWritableMemberGridProduct(tx, c.ProductID); e != nil {
			return e
		}
		allowed, authErr := s.memberGridWriteAllowed(tx, c.ProductID, c.Actor, false)
		if authErr != nil {
			return authErr
		}
		if !allowed {
			return ErrNotFound
		}
		out, e = s.store.DeleteMemberGridView(tx, c.ProductID, c.ViewID, c.ExpectedVersion)
		if e != nil {
			return e
		}
		if e = s.event(tx, "view.deleted", c.IdempotencyKey, c.Actor, out); e != nil {
			return e
		}
		return s.completeMemberGrid(tx, receipt, out)
	})
	return out, classify(err)
}
func (s *MemberGridWorkspaceService) CreateCollaborator(ctx context.Context, c productport.CreateMemberGridCollaboratorCommand) (out productport.MemberGridCollaborator, err error) {
	if !(c.Actor.IsAdmin || c.Actor.IsSuperAdmin) || c.ProductID < 1 || c.AdminUserID < 1 || !validPermission(c.Permission) || c.IdempotencyKey == "" || s.staff == nil {
		return out, ErrInvalidCursor
	}
	err = s.uow.Within(ctx, func(tx context.Context) error {
		receipt, replay, e := s.reserveMemberGrid(tx, "collaborator.create", c.Actor, c.IdempotencyKey, c)
		if e != nil {
			return e
		}
		if replay {
			return s.replayMemberGrid(receipt, &out)
		}
		if e := s.lockWritableMemberGridProduct(tx, c.ProductID); e != nil {
			return e
		}
		active, directoryErr := s.staff.ActiveMemberGridStaff(tx, c.AdminUserID)
		if directoryErr != nil {
			return ErrUnavailable
		}
		if !active {
			return ErrNotFound
		}
		out, e = s.store.CreateMemberGridCollaborator(tx, productport.MemberGridCollaborator{ProductID: c.ProductID, AdminUserID: c.AdminUserID, Permission: c.Permission, CreatedBy: c.Actor.AdminUserID, UpdatedBy: c.Actor.AdminUserID, CreatedAt: s.now().UTC(), UpdatedAt: s.now().UTC()})
		if e != nil {
			return e
		}
		if e = s.event(tx, "collaborator.created", c.IdempotencyKey, c.Actor, out); e != nil {
			return e
		}
		return s.completeMemberGrid(tx, receipt, out)
	})
	return out, classify(err)
}
func (s *MemberGridWorkspaceService) UpdateCollaborator(ctx context.Context, c productport.UpdateMemberGridCollaboratorCommand) (out productport.MemberGridCollaborator, err error) {
	if !(c.Actor.IsAdmin || c.Actor.IsSuperAdmin) || c.ProductID < 1 || c.CollaboratorID < 1 || c.ExpectedVersion < 1 || !validPermission(c.Permission) || c.IdempotencyKey == "" {
		return out, ErrInvalidCursor
	}
	err = s.uow.Within(ctx, func(tx context.Context) error {
		receipt, replay, e := s.reserveMemberGrid(tx, "collaborator.update", c.Actor, c.IdempotencyKey, c)
		if e != nil {
			return e
		}
		if replay {
			return s.replayMemberGrid(receipt, &out)
		}
		if e := s.lockWritableMemberGridProduct(tx, c.ProductID); e != nil {
			return e
		}
		out, e = s.store.UpdateMemberGridCollaborator(tx, productport.MemberGridCollaborator{ID: c.CollaboratorID, ProductID: c.ProductID, Permission: c.Permission, Version: c.ExpectedVersion, UpdatedBy: c.Actor.AdminUserID, UpdatedAt: s.now().UTC()})
		if e != nil {
			return e
		}
		if e = s.event(tx, "collaborator.updated", c.IdempotencyKey, c.Actor, out); e != nil {
			return e
		}
		return s.completeMemberGrid(tx, receipt, out)
	})
	return out, classify(err)
}
func (s *MemberGridWorkspaceService) DeleteCollaborator(ctx context.Context, c productport.DeleteMemberGridCollaboratorCommand) (out productport.MemberGridCollaborator, err error) {
	if !(c.Actor.IsAdmin || c.Actor.IsSuperAdmin) || c.ProductID < 1 || c.CollaboratorID < 1 || c.ExpectedVersion < 1 || c.IdempotencyKey == "" {
		return out, ErrInvalidCursor
	}
	err = s.uow.Within(ctx, func(tx context.Context) error {
		receipt, replay, e := s.reserveMemberGrid(tx, "collaborator.delete", c.Actor, c.IdempotencyKey, c)
		if e != nil {
			return e
		}
		if replay {
			return s.replayMemberGrid(receipt, &out)
		}
		if e := s.lockWritableMemberGridProduct(tx, c.ProductID); e != nil {
			return e
		}
		out, e = s.store.DeleteMemberGridCollaborator(tx, c.ProductID, c.CollaboratorID, c.ExpectedVersion)
		if e != nil {
			return e
		}
		if e = s.event(tx, "collaborator.deleted", c.IdempotencyKey, c.Actor, out); e != nil {
			return e
		}
		return s.completeMemberGrid(tx, receipt, out)
	})
	return out, classify(err)
}
func (s *MemberGridWorkspaceService) Share(ctx context.Context, id productport.ID) (out productport.MemberGridShare, err error) {
	err = s.uow.Within(ctx, func(tx context.Context) error {
		if _, e := s.store.GetServicePeriodProduct(tx, id); e != nil {
			return e
		}
		var e error
		out, e = s.store.GetMemberGridShare(tx, id)
		if e == productport.ErrProductReadNotFound {
			out = productport.MemberGridShare{ProductID: id}
			return nil
		}
		return e
	})
	return out, classify(err)
}
func (s *MemberGridWorkspaceService) SetShare(ctx context.Context, c productport.SetMemberGridShareCommand) (out productport.MemberGridShare, issued bool, err error) {
	if !(c.Actor.IsAdmin || c.Actor.IsSuperAdmin) || c.ProductID < 1 || c.ExpectedVersion < 0 || c.IdempotencyKey == "" {
		return out, false, ErrInvalidCursor
	}
	err = s.uow.Within(ctx, func(tx context.Context) error {
		receipt, replay, e := s.reserveMemberGrid(tx, "share.set", c.Actor, c.IdempotencyKey, c)
		if e != nil {
			return e
		}
		if replay {
			if e = s.replayMemberGrid(receipt, &out); e == nil {
				issued = out.Enabled
			}
			return e
		}
		if e := s.lockWritableMemberGridProduct(tx, c.ProductID); e != nil {
			return e
		}
		previous, e := s.store.GetMemberGridShare(tx, c.ProductID)
		if e == productport.ErrProductReadNotFound {
			previous = productport.MemberGridShare{ProductID: c.ProductID}
			e = nil
		}
		if e != nil {
			return e
		}
		if previous.Version != c.ExpectedVersion {
			return ErrConflict
		}
		token := ""
		gen := previous.Generation
		if c.Enabled {
			gen++
			token, e = newShareToken()
			if e != nil {
				return e
			}
			issued = true
		}
		out, e = s.store.SetMemberGridShare(tx, productport.MemberGridShare{ProductID: c.ProductID, Enabled: c.Enabled, PublicID: token, Generation: gen, CreatedBy: c.Actor.AdminUserID, UpdatedBy: c.Actor.AdminUserID, CreatedAt: s.now().UTC(), UpdatedAt: s.now().UTC()}, c.ExpectedVersion)
		if e != nil {
			return e
		}
		if e = s.event(tx, "share.set", c.IdempotencyKey, c.Actor, out); e != nil {
			return e
		}
		return s.completeMemberGrid(tx, receipt, out)
	})
	return out, issued, classify(err)
}
func (s *MemberGridWorkspaceService) ResolveShare(ctx context.Context, token string) (out productport.MemberGridShare, err error) {
	if !validToken(token) {
		return out, ErrNotFound
	}
	err = s.uow.Within(ctx, func(tx context.Context) error { out, err = s.store.GetMemberGridShareByToken(tx, token); return err })
	return out, classify(err)
}

// lockWritableMemberGridProduct deliberately keeps archived service-period
// products readable through the historical grid APIs, while serializing every
// new workspace mutation with the owner product row and refusing a terminal
// archived lifecycle before a view, collaborator, or public share can change.
func (s *MemberGridWorkspaceService) lockWritableMemberGridProduct(ctx context.Context, id productport.ID) error {
	product, err := s.store.GetServicePeriodProductForUpdate(ctx, id)
	if err != nil {
		return err
	}
	// GetServicePeriodProductForUpdate already restricts the product kind. For a
	// mutation gate the durable terminal marker is sufficient: even a malformed
	// old projection carrying service_period_archived must not reopen sharing or
	// workspace writes while historical reads remain available.
	var projection struct {
		Status string `json:"status"`
	}
	if json.Unmarshal(product.LegacyAdminProjection, &projection) != nil {
		return ErrUnavailable
	}
	if projection.Status == ServicePeriodProjectionArchivedStatus {
		return ErrNotFound
	}
	return nil
}

func readyGrid(s *MemberGridWorkspaceService, id productport.ID, a productport.MemberGridActor) bool {
	return s != nil && s.uow != nil && s.store != nil && s.events != nil && id > 0 && a.AdminUserID > 0
}
func validGridWrite(id productport.ID, a productport.MemberGridActor, key string) bool {
	return id > 0 && a.AdminUserID > 0 && key != ""
}

// memberGridWriteAllowed runs inside the same Product UoW as the metadata
// mutation.  A collaborator removed between the HTTP capability read and this
// check therefore cannot complete a write.  Admin is the existing Access-wide
// Product capability; edit is the local workspace grant.
func (s *MemberGridWorkspaceService) memberGridWriteAllowed(ctx context.Context, productID productport.ID, actor productport.MemberGridActor, manageShare bool) (bool, error) {
	if actor.IsAdmin || actor.IsSuperAdmin {
		return true, nil
	}
	if manageShare {
		return false, nil
	}
	c, err := s.store.FindMemberGridCollaborator(ctx, productID, actor.AdminUserID)
	if err == productport.ErrProductReadNotFound {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return c.Permission == "edit", nil
}
func validPermission(v string) bool { return v == "read" || v == "edit" }
func validView(name string, c json.RawMessage) bool {
	n := strings.TrimSpace(name)
	return len(n) > 0 && len(n) <= 60 && len(c) > 0 && len(c) <= 32768 && json.Valid(c) && c[0] == '{'
}
func validToken(v string) bool {
	p := strings.Split(v, ".")
	return len(p) == 3 && p[0] == "mgshare1" && len(p[1]) >= 16 && len(p[2]) == 43
}
func newShareToken() (string, error) {
	a := make([]byte, 18)
	b := make([]byte, 32)
	if _, e := rand.Read(a); e != nil {
		return "", e
	}
	if _, e := rand.Read(b); e != nil {
		return "", e
	}
	return "mgshare1." + base64.RawURLEncoding.EncodeToString(a) + "." + base64.RawURLEncoding.EncodeToString(b), nil
}
func (s *MemberGridWorkspaceService) event(ctx context.Context, kind, key string, a productport.MemberGridActor, v any) error {
	// The share's public_id is an opaque bearer credential.  Audit/outbox keeps
	// only revocation-relevant state, never the credential itself.
	var summary map[string]any
	switch value := v.(type) {
	case productport.MemberGridView:
		summary = map[string]any{"product_id": value.ProductID, "view_id": value.ID, "version": value.Version}
	case productport.MemberGridCollaborator:
		summary = map[string]any{"product_id": value.ProductID, "collaborator_id": value.ID, "staff_id": value.AdminUserID, "permission": value.Permission, "version": value.Version}
	case productport.MemberGridShare:
		summary = map[string]any{"product_id": value.ProductID, "enabled": value.Enabled, "generation": value.Generation, "version": value.Version}
	default:
		return ErrUnavailable
	}
	payload, e := json.Marshal(map[string]any{"product_id": summary["product_id"], "version": summary["version"], "actor": a.AdminUserID, "member_grid": summary})
	if e != nil {
		return e
	}
	_, e = s.events.Append(ctx, productport.Event{Type: "product.service_period_member_grid." + kind, Payload: payload, OccurredAt: s.now().UTC(), IdempotencyKey: key})
	return e
}
